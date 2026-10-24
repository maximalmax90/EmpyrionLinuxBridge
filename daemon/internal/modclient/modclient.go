package modclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"elb/daemon/internal/safego"
	"elb/daemon/internal/watchdog"
)

var ErrInventoryBusy = errors.New("inventory busy: container UI open")

type Client struct {
	BaseURL string
	http    *http.Client

	slowHTTP *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		BaseURL:  baseURL,
		http:     &http.Client{Timeout: 5 * time.Second},
		slowHTTP: &http.Client{Timeout: 15 * time.Second},
	}
}

type ChatEntry struct {
	Sender    string `json:"sender"`
	SteamId   string `json:"steamId,omitempty"`
	Text      string `json:"text"`
	GameTime  uint64 `json:"gameTime"`
	Timestamp string `json:"timestamp,omitempty"`
	Channel   string `json:"channel,omitempty"`

	Target string `json:"target,omitempty"`

	FactionId int `json:"factionId,omitempty"`
}

type Vec3 struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

type PlayerLiveInfo struct {
	ClientId       int     `json:"clientId"`
	Pos            Vec3    `json:"pos"`
	Rot            Vec3    `json:"rot"`
	StartPlayfield string  `json:"startPlayfield"`
	SteamOwnerId   string  `json:"steamOwnerId"`
	Origin         int     `json:"origin"`
	FactionGroup   int     `json:"factionGroup"`
	FactionId      int     `json:"factionId"`
	FactionRole    int     `json:"factionRole"`
	Health         float64 `json:"health"`
	HealthMax      float64 `json:"healthMax"`
	Oxygen         float64 `json:"oxygen"`
	OxygenMax      float64 `json:"oxygenMax"`
	Stamina        float64 `json:"stamina"`
	StaminaMax     float64 `json:"staminaMax"`
	Food           float64 `json:"food"`
	FoodMax        float64 `json:"foodMax"`
	Radiation      float64 `json:"radiation"`
	RadiationMax   float64 `json:"radiationMax"`
	BodyTemp       float64 `json:"bodyTemp"`
	BodyTempMax    float64 `json:"bodyTempMax"`
	Kills          int     `json:"kills"`
	Died           int     `json:"died"`
	Credits        float64 `json:"credits"`
	Exp            int     `json:"exp"`
	Upgrade        int     `json:"upgrade"`
	Ping           int     `json:"ping"`
	Permission     int     `json:"permission"`

	BpRemainingTime      float64            `json:"bpRemainingTime"`
	BpInFactory          string             `json:"bpInFactory"`
	BpResourcesInFactory map[string]float64 `json:"bpResourcesInFactory"`
	ProducedPrefabs      []string           `json:"producedPrefabs"`
}

type Player struct {
	EntityId  int             `json:"entityId"`
	Name      string          `json:"name"`
	SteamId   string          `json:"steamId"`
	Playfield string          `json:"playfield"`
	Online    bool            `json:"online"`
	Info      *PlayerLiveInfo `json:"info"`
}

func (c *Client) Players() ([]Player, error) {
	resp, err := getWithRetry(c.http, c.BaseURL+"/players")
	if err != nil {
		return nil, fmt.Errorf("mod GET /players: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mod GET /players: status %d", resp.StatusCode)
	}
	var players []Player
	if err := json.NewDecoder(resp.Body).Decode(&players); err != nil {
		return nil, fmt.Errorf("mod GET /players decode: %w", err)
	}
	return players, nil
}

type AlliancePair struct {
	Faction1Id int `json:"faction1Id"`
	Faction2Id int `json:"faction2Id"`
}

var alliancesHTTPClient = &http.Client{Timeout: 30 * time.Second}

func (c *Client) AlliancePairs(ids []int) ([]AlliancePair, error) {
	q := ""
	for i, id := range ids {
		if i > 0 {
			q += ","
		}
		q += fmt.Sprintf("%d", id)
	}
	resp, err := alliancesHTTPClient.Get(c.BaseURL + "/alliances/pairs?ids=" + q)
	if err != nil {
		return nil, fmt.Errorf("mod GET /alliances/pairs: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mod GET /alliances/pairs: status %d", resp.StatusCode)
	}
	var pairs []AlliancePair
	if err := json.NewDecoder(resp.Body).Decode(&pairs); err != nil {
		return nil, fmt.Errorf("mod GET /alliances/pairs decode: %w", err)
	}
	return pairs, nil
}

const playersPollInterval = 3 * time.Second

var (
	playersCacheMu sync.RWMutex
	playersCache   []Player // nil до первого успешного опроса
)

func (c *Client) CachedPlayers() []Player {
	playersCacheMu.RLock()
	defer playersCacheMu.RUnlock()
	return playersCache
}

func RunPlayersPoller(c *Client) {
	ticker := time.NewTicker(playersPollInterval)
	defer ticker.Stop()
	for {
		if !watchdog.CachedHealth().Ready() {
			<-ticker.C
			continue
		}
		safego.Tick("modclient.RunPlayersPoller", func() {
			players, err := c.Players()
			if err != nil {
				log.Printf("modclient: player poll: %v", err)
			} else {
				playersCacheMu.Lock()
				playersCache = dedupeBySteamID(players)
				playersCacheMu.Unlock()
			}
		})
		<-ticker.C
	}
}

func dedupeBySteamID(players []Player) []Player {
	bySteam := map[string]int{}
	out := make([]Player, 0, len(players))
	for _, p := range players {
		if p.SteamId == "" {
			out = append(out, p)
			continue
		}
		idx, ok := bySteam[p.SteamId]
		if !ok {
			bySteam[p.SteamId] = len(out)
			out = append(out, p)
			continue
		}
		existing := out[idx]
		keepNew := p.EntityId > existing.EntityId
		if p.Online != existing.Online {
			keepNew = p.Online
		}
		if keepNew {
			out[idx] = p
		}
	}
	return out
}

func (c *Client) RecentChat() ([]ChatEntry, error) {
	resp, err := getWithRetry(c.http, c.BaseURL+"/chat")
	if err != nil {
		return nil, fmt.Errorf("mod GET /chat: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mod GET /chat: status %d", resp.StatusCode)
	}
	var entries []ChatEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("mod GET /chat decode: %w", err)
	}
	return entries, nil
}

func (c *Client) Get(path string) (any, error) { return c.get(c.http, path) }

func (c *Client) GetSlow(path string) (any, error) { return c.get(c.slowHTTP, path) }

func (c *Client) get(client *http.Client, path string) (any, error) {
	resp, err := getWithRetry(client, c.BaseURL+path)
	if err != nil {
		return nil, fmt.Errorf("mod GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mod GET %s: status %d", path, resp.StatusCode)
	}
	var v any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, fmt.Errorf("mod GET %s decode: %w", path, err)
	}
	return v, nil
}

func (c *Client) Post(path string, body any) (any, error) {
	data, _ := json.Marshal(body)
	resp, err := c.http.Post(c.BaseURL+path, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("mod POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mod POST %s: status %d: %s", path, resp.StatusCode, b)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("mod POST %s decode: %w", path, err)
	}
	return v, nil
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func isRetryableSendError(err error) bool {
	return isTimeout(err) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

func getWithRetry(client *http.Client, url string) (*http.Response, error) {
	resp, err := client.Get(url)
	if err == nil || !isRetryableSendError(err) {
		return resp, err
	}
	time.Sleep(messageRetryDelay)
	return client.Get(url)
}

const messageRetryDelay = 3 * time.Second

func (c *Client) chatRecentContains(text string) bool {
	entries, err := c.RecentChat()
	if err != nil || text == "" {
		return false
	}
	n := len(entries)
	if n > 15 {
		n = 15
	}
	for _, e := range entries[len(entries)-n:] {
		if e.Text == text {
			return true
		}
	}
	return false
}

func (c *Client) PostMessageWithRetry(path string, body map[string]any) (any, error) {
	v, err := c.Post(path, body)
	if err == nil || !isRetryableSendError(err) {
		return v, err
	}
	time.Sleep(messageRetryDelay)
	if path == "/chat" {
		if text, _ := body["text"].(string); c.chatRecentContains(text) {
			return v, nil
		}
	}
	return c.Post(path, body)
}

type ItemStack struct {
	Id      int `json:"id"`
	Count   int `json:"count"`
	SlotIdx int `json:"slotIdx"`
	Ammo    int `json:"ammo"`
	Decay   int `json:"decay"`
}

type Inventory struct {
	PlayerId int         `json:"playerId"`
	Toolbelt []ItemStack `json:"toolbelt"`
	Bag      []ItemStack `json:"bag"`
}

type InventoryEditPayload struct {
	PlayerId int         `json:"playerId"`
	SteamId  string      `json:"steamId"`
	Toolbelt []ItemStack `json:"toolbelt"`
	Bag      []ItemStack `json:"bag"`

	BaselineToolbelt []ItemStack `json:"baselineToolbelt,omitempty"`
	BaselineBag      []ItemStack `json:"baselineBag,omitempty"`

	Part string `json:"part,omitempty"`
}

func (c *Client) PlayerInventory(playerId int) (Inventory, error) {
	resp, err := getWithRetry(c.http, fmt.Sprintf("%s/players/inventory?playerId=%d", c.BaseURL, playerId))
	if err != nil {
		return Inventory{}, fmt.Errorf("mod GET /players/inventory: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {

		var eb struct {
			Error string `json:"error"`
		}
		body, _ := io.ReadAll(resp.Body)
		_ = json.Unmarshal(body, &eb)
		if strings.Contains(eb.Error, "IOError") {
			return Inventory{}, fmt.Errorf("%w (%s)", ErrInventoryBusy, eb.Error)
		}
		if eb.Error != "" {
			return Inventory{}, fmt.Errorf("mod GET /players/inventory: status %d: %s", resp.StatusCode, eb.Error)
		}
		return Inventory{}, fmt.Errorf("mod GET /players/inventory: status %d", resp.StatusCode)
	}
	var inv Inventory
	if err := json.NewDecoder(resp.Body).Decode(&inv); err != nil {
		return Inventory{}, fmt.Errorf("mod GET /players/inventory decode: %w", err)
	}
	return inv, nil
}

func (c *Client) SetPlayerInventory(playerId int, toolbelt, bag []ItemStack) error {
	if toolbelt == nil {
		toolbelt = []ItemStack{}
	}
	if bag == nil {
		bag = []ItemStack{}
	}
	body, _ := json.Marshal(map[string]any{
		"playerId": playerId,
		"toolbelt": toolbelt,
		"bag":      bag,
	})
	resp, err := c.http.Post(c.BaseURL+"/players/inventory", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mod POST /players/inventory: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mod POST /players/inventory: status %d: %s", resp.StatusCode, b)
	}
	return nil
}

type StructurePosition struct {
	X int `json:"x"`
	Y int `json:"y"`
	Z int `json:"z"`
}

type ContainerSnapshot struct {
	Name       string            `json:"name"`
	DeviceType string            `json:"deviceType"`
	Position   StructurePosition `json:"position"`
	Items      []ItemStack       `json:"items"`
}

type TankSnapshot struct {
	Capacity           float64 `json:"capacity"`
	Content            float64 `json:"content"`
	UsesIntegerAmounts bool    `json:"usesIntegerAmounts"`
}

type BlockSnapshot struct {
	X      int  `json:"x"`
	Y      int  `json:"y"`
	Z      int  `json:"z"`
	Type   int  `json:"type"`
	Shape  int  `json:"shape"`
	Rot    int  `json:"rot"`
	Active bool `json:"active"`
}

type StructureBackupPayload struct {
	Id        int    `json:"id"`
	Playfield string `json:"playfield"`
	Name      string `json:"name"`

	TypeName     string              `json:"typeName"`
	PosX         float64             `json:"posX"`
	PosY         float64             `json:"posY"`
	PosZ         float64             `json:"posZ"`
	FactionGroup byte                `json:"factionGroup"`
	FactionId    int                 `json:"factionId"`
	Containers   []ContainerSnapshot `json:"containers"`
	FuelTank     *TankSnapshot       `json:"fuelTank"`
	OxygenTank   *TankSnapshot       `json:"oxygenTank"`
	PentaxidTank *TankSnapshot       `json:"pentaxidTank"`

	BlueprintBase64 string `json:"blueprintBase64"`

	GeometryBlocks     []BlockSnapshot `json:"geometryBlocks,omitempty"`
	GeometryBlockCount int             `json:"geometryBlockCount,omitempty"`
}

func (c *Client) PendingStructureBackups() ([]StructureBackupPayload, error) {
	resp, err := getWithRetry(c.slowHTTP, c.BaseURL+"/structures/backup/pending")
	if err != nil {
		return nil, fmt.Errorf("mod GET /structures/backup/pending: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mod GET /structures/backup/pending: status %d", resp.StatusCode)
	}
	var list []StructureBackupPayload
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("mod GET /structures/backup/pending decode: %w", err)
	}
	return list, nil
}
