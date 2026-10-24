package daemonclient

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Client struct {
	baseURL string
	token   string
	http    *http.Client

	reachMu     sync.RWMutex
	unreachable bool
	lastProbeAt time.Time
}

var ErrDaemonUnreachable = errors.New("daemon unreachable")

const unreachableProbeBackoff = 5 * time.Second

func (c *Client) skipDueToUnreachable() bool {
	c.reachMu.RLock()
	defer c.reachMu.RUnlock()
	return c.unreachable && time.Since(c.lastProbeAt) < unreachableProbeBackoff
}

func (c *Client) markProbeResult(ok bool) {
	c.reachMu.Lock()
	defer c.reachMu.Unlock()
	if ok {
		c.unreachable = false
		return
	}
	c.unreachable = true
	c.lastProbeAt = time.Now()
}

func (c *Client) Unreachable() bool {
	c.reachMu.RLock()
	defer c.reachMu.RUnlock()
	return c.unreachable
}

func New(baseURL, token, clientCertFingerprint string) *Client {
	tlsCfg := &tls.Config{InsecureSkipVerify: true}

	want := strings.ToLower(strings.ReplaceAll(clientCertFingerprint, ":", ""))
	if want != "" {
		tlsCfg.InsecureSkipVerify = true
		tlsCfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			for _, raw := range rawCerts {
				sum := sha256.Sum256(raw)
				if hex.EncodeToString(sum[:]) == want {
					return nil
				}
			}
			return fmt.Errorf("daemon certificate does not match the pinned fingerprint")
		}
	}

	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http: &http.Client{
			Timeout:   15 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
		},
	}
}

func (c *Client) do(method, path string, body any) ([]byte, error) {
	return c.doWithClient(c.http, method, path, body)
}

func (c *Client) doWithClient(client *http.Client, method, path string, body any) ([]byte, error) {

	if c.skipDueToUnreachable() {
		return nil, ErrDaemonUnreachable
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		c.markProbeResult(false)
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	c.markProbeResult(true)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s %s: read response: %w", method, path, err)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s", errorMessage(data, resp.StatusCode))
	}
	return data, nil
}

func errorMessage(data []byte, status int) string {
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &body) == nil && body.Error != "" {
		return body.Error
	}
	if msg := strings.TrimSpace(string(data)); msg != "" {
		return msg
	}
	return fmt.Sprintf("статус %d", status)
}

type Vec3 struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

type PlayerLive struct {
	Health       float64 `json:"health"`
	HealthMax    float64 `json:"healthMax"`
	Food         float64 `json:"food"`
	FoodMax      float64 `json:"foodMax"`
	Oxygen       float64 `json:"oxygen"`
	OxygenMax    float64 `json:"oxygenMax"`
	Stamina      float64 `json:"stamina"`
	StaminaMax   float64 `json:"staminaMax"`
	Radiation    float64 `json:"radiation"`
	RadiationMax float64 `json:"radiationMax"`
	BodyTemp     float64 `json:"bodyTemp"`
	BodyTempMax  float64 `json:"bodyTempMax"`
	FactionId    int     `json:"factionId"`
	FactionRole  int     `json:"factionRole"`
	Kills        int     `json:"kills"`
	Died         int     `json:"died"`
	Credits      float64 `json:"credits"`
	Exp          int     `json:"exp"`
	Upgrade      int     `json:"upgrade"`
	Ping         int     `json:"ping"`
	Permission   int     `json:"permission"`

	BpRemainingTime      float64            `json:"bpRemainingTime"`
	BpInFactory          string             `json:"bpInFactory"`
	BpResourcesInFactory map[string]float64 `json:"bpResourcesInFactory"`
}

type PlayerOffline struct {
	Credits         *int64   `json:"credits,omitempty"`
	Kills           *int     `json:"kills,omitempty"`
	Died            *int     `json:"died,omitempty"`
	PlaytimeSeconds *float64 `json:"playtimeSeconds,omitempty"`
	Exp             *int     `json:"exp,omitempty"`
}

type Player struct {
	EntityId  int    `json:"entityId"`
	Name      string `json:"name"`
	SteamId   string `json:"steamId"`
	Playfield string `json:"playfield"`
	Online    bool   `json:"online"`

	FactionId   int    `json:"factionId,omitempty"`
	FactionTag  string `json:"factionTag,omitempty"`
	FactionName string `json:"factionName,omitempty"`

	Pos         *Vec3  `json:"pos,omitempty"`
	Rot         *Vec3  `json:"rot,omitempty"`
	SolarSystem string `json:"solarSystem,omitempty"`

	LastOnline    string  `json:"lastOnline,omitempty"`
	OnlineSeconds float64 `json:"onlineSeconds,omitempty"`

	Role        string `json:"role,omitempty"`
	BannedUntil string `json:"bannedUntil,omitempty"`

	MutedUntil string `json:"mutedUntil,omitempty"`

	Live    *PlayerLive    `json:"live,omitempty"`
	Offline *PlayerOffline `json:"offline,omitempty"`
}

func (c *Client) Players() ([]Player, error) {
	data, err := c.do(http.MethodGet, "/api/players", nil)
	if err != nil {
		return nil, err
	}
	var players []Player
	if err := json.Unmarshal(data, &players); err != nil {
		return nil, fmt.Errorf("players: parse response: %w", err)
	}

	sort.Slice(players, func(i, j int) bool {
		if players[i].Online != players[j].Online {
			return players[i].Online
		}
		return strings.ToLower(players[i].Name) < strings.ToLower(players[j].Name)
	})
	return players, nil
}

const (
	FactionGroupFaction = 0
	FactionGroupPlayer  = 1
)

type Structure struct {
	ID              int    `json:"id"`
	Name            string `json:"name"`
	Type            int    `json:"type"`
	TypeName        string `json:"typeName"`
	ClassNr         int    `json:"classNr"`
	FactionId       int    `json:"factionId"`
	FactionGroup    int    `json:"factionGroup"`
	FactionGroupStr string `json:"factionGroupName"`
	IsPOI           bool   `json:"isPoi"`
	Playfield       string `json:"playfield"`
	SolarSystem     string `json:"solarSystem"`
	Pos             Vec3   `json:"pos"`
	Rot             Vec3   `json:"rot"`
	Powered         bool   `json:"powered"`
	Fuel            int    `json:"fuel"`
	CntDevices      int    `json:"cntDevices"`
	CntBlocks       int    `json:"cntBlocks"`
	CntTriangles    int    `json:"cntTriangles"`
	CntLights       int    `json:"cntLights"`
	CoreType        int    `json:"coreType"`
	PilotID         int    `json:"pilotId"`
	DockedTo        int    `json:"dockedTo,omitempty"`
	DockedToName    string `json:"dockedToName,omitempty"`
	DockedShips     []int  `json:"dockedShips,omitempty"`

	LastVisitedTicks int64 `json:"lastVisitedTicks,omitempty"`

	IdentityID int  `json:"identityId,omitempty"`
	Deleted    bool `json:"deleted,omitempty"`

	SnapshotCount int    `json:"snapshotCount,omitempty"`
	LastSnapshot  string `json:"lastSnapshot,omitempty"`

	FactionTag string `json:"-"`
	OwnerName  string `json:"-"`

	HistoryCount int `json:"-"`

	Pending bool `json:"-"`
}

func (c *Client) Structures() ([]Structure, error) {
	data, err := c.do(http.MethodGet, "/api/structures/all", nil)
	if err != nil {
		return nil, err
	}
	var list []Structure
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("structures: parse response: %w", err)
	}
	return list, nil
}

func (c *Client) StructurePendingIDs() (map[int]bool, error) {
	data, err := c.do(http.MethodGet, "/api/structures/pending", nil)
	if err != nil {
		return nil, err
	}
	var res struct {
		IDs []int `json:"ids"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("structure pending ids: parse response: %w", err)
	}
	out := make(map[int]bool, len(res.IDs))
	for _, id := range res.IDs {
		out[id] = true
	}
	return out, nil
}

type StructureQueuedResult struct {
	Queued bool `json:"queued"`
}

func (c *Client) StructureTouch(id int, playfield string) (bool, error) {
	data, err := c.do(http.MethodPost, "/api/structures/touch", map[string]any{"id": id, "playfield": playfield})
	if err != nil {
		return false, err
	}
	var res StructureQueuedResult
	if err := json.Unmarshal(data, &res); err != nil {
		return false, fmt.Errorf("structure touch: parse response: %w", err)
	}
	return res.Queued, nil
}

func (c *Client) StructureRepair(id int, playfield string) (bool, error) {
	data, err := c.do(http.MethodPost, "/api/structures/repair", map[string]any{"id": id, "playfield": playfield})
	if err != nil {
		return false, err
	}
	var res StructureQueuedResult
	if err := json.Unmarshal(data, &res); err != nil {
		return false, fmt.Errorf("structure repair: parse response: %w", err)
	}
	return res.Queued, nil
}

func (c *Client) StructureDestroy(id int, playfield string) error {
	_, err := c.do(http.MethodPost, "/api/structures/destroy", map[string]any{"id": id, "playfield": playfield})
	return err
}

func (c *Client) StructureBackupNow(id int) (bool, error) {
	data, err := c.do(http.MethodPost, fmt.Sprintf("/api/structures/%d/backup-now", id), nil)
	if err != nil {
		return false, err
	}
	var resp struct {
		Captured bool `json:"captured"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return false, err
	}
	return resp.Captured, nil
}

func (c *Client) StructureSetFaction(id int, abbrev string) error {
	_, err := c.do(http.MethodPost, "/api/structures/faction", map[string]any{"id": id, "abbrev": abbrev})
	return err
}

func (c *Client) StructureRefuel(id int, playfield string, fuelPercent, oxygenPercent, pentaxidPercent *float64) error {
	body := map[string]any{"id": id, "playfield": playfield}
	if fuelPercent != nil {
		body["fuelPercent"] = *fuelPercent
	}
	if oxygenPercent != nil {
		body["oxygenPercent"] = *oxygenPercent
	}
	if pentaxidPercent != nil {
		body["pentaxidPercent"] = *pentaxidPercent
	}
	_, err := c.do(http.MethodPost, "/api/structures/refuel", body)
	return err
}

func (c *Client) StructureMove(id int, playfield string, x, y, z float64) (bool, error) {
	data, err := c.do(http.MethodPost, "/api/structures/move", map[string]any{"id": id, "playfield": playfield, "x": x, "y": y, "z": z})
	if err != nil {
		return false, err
	}
	var res StructureQueuedResult
	if err := json.Unmarshal(data, &res); err != nil {
		return false, fmt.Errorf("structure move: parse response: %w", err)
	}
	return res.Queued, nil
}

type StructureRestoreResult struct {
	Branch    string `json:"branch"`
	NewID     int    `json:"newId,omitempty"`
	Playfield string `json:"playfield"`
}

func (c *Client) StructureRestore(id, snapshotId int, mode string, positionPlayerId int) (StructureRestoreResult, error) {
	restoreClient := &http.Client{Transport: c.http.Transport, Timeout: 90 * time.Second}
	data, err := c.doWithClient(restoreClient, http.MethodPost, fmt.Sprintf("/api/structures/%d/restore", id), map[string]any{"mode": mode, "snapshotId": snapshotId, "positionPlayerId": positionPlayerId})
	if err != nil {
		return StructureRestoreResult{}, err
	}
	var res StructureRestoreResult
	if err := json.Unmarshal(data, &res); err != nil {
		return StructureRestoreResult{}, fmt.Errorf("structure restore: parse response: %w", err)
	}
	return res, nil
}

type StructureBackupSnapshot struct {
	Id             int    `json:"id"`
	Timestamp      string `json:"timestamp"`
	ContainerCount int    `json:"containerCount"`
	ItemCount      int    `json:"itemCount"`

	TypeName string `json:"typeName"`

	BlockCount      int      `json:"blockCount,omitempty"`
	FuelPercent     *float64 `json:"fuelPercent,omitempty"`
	OxygenPercent   *float64 `json:"oxygenPercent,omitempty"`
	PentaxidPercent *float64 `json:"pentaxidPercent,omitempty"`
}

func (c *Client) StructureBackups(id int) ([]StructureBackupSnapshot, error) {
	data, err := c.do(http.MethodGet, fmt.Sprintf("/api/structures/%d/backups", id), nil)
	if err != nil {
		return nil, err
	}
	var res []StructureBackupSnapshot
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("structure backups: parse response: %w", err)
	}
	return res, nil
}

type BlockBreakdownItem struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type BlocksBreakdown struct {
	Blocks    []BlockBreakdownItem `json:"blocks"`
	Devices   []BlockBreakdownItem `json:"devices"`
	Timestamp string               `json:"timestamp,omitempty"`
}

func (c *Client) StructureBlocksBreakdown(id int, lang string) (BlocksBreakdown, error) {
	q := url.Values{}
	q.Set("lang", lang)
	data, err := c.do(http.MethodGet, fmt.Sprintf("/api/structures/%d/blocks-breakdown?", id)+q.Encode(), nil)
	if err != nil {
		return BlocksBreakdown{}, err
	}
	var res BlocksBreakdown
	if err := json.Unmarshal(data, &res); err != nil {
		return BlocksBreakdown{}, fmt.Errorf("structure blocks breakdown: parse response: %w", err)
	}
	return res, nil
}

type ChatEntry struct {
	Sender       string `json:"sender"`
	SteamId      string `json:"steamId,omitempty"`
	Text         string `json:"text"`
	GameTime     uint64 `json:"gameTime"`
	Timestamp    string `json:"timestamp,omitempty"`
	Channel      string `json:"channel,omitempty"`
	Target       string `json:"target,omitempty"`
	SessionStart bool   `json:"sessionStart,omitempty"`

	Priority string `json:"priority,omitempty"`
}

func (c *Client) Chat() ([]ChatEntry, error) {
	data, err := c.do(http.MethodGet, "/api/chat", nil)
	if err != nil {
		return nil, err
	}
	var entries []ChatEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("chat: parse response: %w", err)
	}
	return entries, nil
}

type ChatHistoryFilter struct {
	From, To        string
	SteamId, Sender string
	Channel, Target string
	Limit, Offset   int
}

type ChatHistoryResult struct {
	Entries []ChatEntry `json:"entries"`
	Total   int         `json:"total"`
}

func (c *Client) ChatHistory(f ChatHistoryFilter) (ChatHistoryResult, error) {
	body := map[string]any{
		"from": f.From, "to": f.To,
		"steamId": f.SteamId, "sender": f.Sender,
		"channel": f.Channel, "target": f.Target,
		"limit": f.Limit, "offset": f.Offset,
	}
	data, err := c.do(http.MethodPost, "/api/chat/history", body)
	if err != nil {
		return ChatHistoryResult{}, err
	}
	var result ChatHistoryResult
	if err := json.Unmarshal(data, &result); err != nil {
		return ChatHistoryResult{}, fmt.Errorf("chat history: parse response: %w", err)
	}
	return result, nil
}

type SendChatOptions struct {
	Text    string
	Channel string

	FactionID string

	AllianceFactionIds string
	AsSteamID          string
	RecipientSteamID   string
}

func (c *Client) SendChat(opts SendChatOptions) error {
	body := map[string]any{"text": opts.Text}
	if opts.Channel != "" {
		body["channel"] = opts.Channel
	}
	if opts.Channel == "faction" && opts.FactionID != "" {
		body["factionId"] = opts.FactionID
	}
	if opts.Channel == "alliance" && opts.AllianceFactionIds != "" {
		body["allianceFactionIds"] = opts.AllianceFactionIds
	}
	if opts.AsSteamID != "" {
		body["asSteamId"] = opts.AsSteamID
	}
	if opts.RecipientSteamID != "" {
		body["recipientSteamId"] = opts.RecipientSteamID
	}
	_, err := c.do(http.MethodPost, "/api/chat", body)
	return err
}

type Faction struct {
	ID           int    `json:"id"`
	Abbreviation string `json:"abbreviation"`
	Name         string `json:"name"`
}

func (c *Client) Factions() ([]Faction, error) {
	data, err := c.do(http.MethodGet, "/api/factions", nil)
	if err != nil {
		return nil, err
	}
	var list []Faction
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("factions: parse response: %w", err)
	}
	return list, nil
}

type Alliance struct {
	Faction1Id  int    `json:"faction1Id"`
	Faction1Tag string `json:"faction1Tag"`
	Faction2Id  int    `json:"faction2Id"`
	Faction2Tag string `json:"faction2Tag"`
}

type AlliancesResult struct {
	Enabled bool       `json:"enabled"`
	Pairs   []Alliance `json:"pairs"`
}

func (c *Client) Alliances() (AlliancesResult, error) {
	data, err := c.do(http.MethodGet, "/api/alliances", nil)
	if err != nil {
		return AlliancesResult{}, err
	}
	var out AlliancesResult
	if err := json.Unmarshal(data, &out); err != nil {
		return AlliancesResult{}, fmt.Errorf("alliances: parse response: %w", err)
	}
	return out, nil
}

type SysInfo struct {
	CPUPercent     float64 `json:"cpuPercent"`
	MemUsedBytes   uint64  `json:"memUsedBytes"`
	MemTotalBytes  uint64  `json:"memTotalBytes"`
	SwapUsedBytes  uint64  `json:"swapUsedBytes"`
	SwapTotalBytes uint64  `json:"swapTotalBytes"`
	DiskUsedBytes  uint64  `json:"diskUsedBytes"`
	DiskTotalBytes uint64  `json:"diskTotalBytes"`
	IOWaitPercent  float64 `json:"ioWaitPercent"`
}

func (c *Client) SysInfo() (SysInfo, error) {
	data, err := c.do(http.MethodGet, "/api/sysinfo", nil)
	if err != nil {
		return SysInfo{}, err
	}
	var s SysInfo
	if err := json.Unmarshal(data, &s); err != nil {
		return SysInfo{}, fmt.Errorf("sysinfo: parse response: %w", err)
	}
	return s, nil
}

type MetricsSample struct {
	Ts            time.Time `json:"ts"`
	CPUPercent    float64   `json:"cpuPercent"`
	IOWaitPercent float64   `json:"ioWaitPercent"`
	MemPercent    float64   `json:"memPercent"`
	SwapPercent   float64   `json:"swapPercent"`
}

type MetricsDiskPoint struct {
	Ts      time.Time `json:"ts"`
	Percent float64   `json:"percent"`
}

type MetricsSensorPoint struct {
	Ts time.Time `json:"ts"`
	C  float64   `json:"c"`
}

type MetricsNetPoint struct {
	Ts    time.Time `json:"ts"`
	RxBps float64   `json:"rxBps"`
	TxBps float64   `json:"txBps"`
}

type MetricsActivityPoint struct {
	Ts               time.Time `json:"ts"`
	PlayfieldsActive int       `json:"playfieldsActive"`
	OnlinePlayers    int       `json:"onlinePlayers"`
}

type MetricsHistoryResponse struct {
	Samples  []MetricsSample                 `json:"samples"`
	Disks    map[string][]MetricsDiskPoint   `json:"disks"`
	Sensors  map[string][]MetricsSensorPoint `json:"sensors"`
	Net      map[string][]MetricsNetPoint    `json:"net"`
	Activity []MetricsActivityPoint          `json:"activity,omitempty"`
	Governor string                          `json:"governor,omitempty"`
}

func (c *Client) MetricsHistory(rangeParam string) (MetricsHistoryResponse, error) {
	data, err := c.do(http.MethodGet, fmt.Sprintf("/api/metrics/history?range=%s", url.QueryEscape(rangeParam)), nil)
	if err != nil {
		return MetricsHistoryResponse{}, err
	}
	var out MetricsHistoryResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return MetricsHistoryResponse{}, fmt.Errorf("metrics history: parse response: %w", err)
	}
	return out, nil
}

func (c *Client) MetricsHistoryRange(since, until time.Time) (MetricsHistoryResponse, error) {
	data, err := c.do(http.MethodGet, fmt.Sprintf("/api/metrics/history?since=%s&until=%s",
		url.QueryEscape(since.UTC().Format(time.RFC3339)), url.QueryEscape(until.UTC().Format(time.RFC3339))), nil)
	if err != nil {
		return MetricsHistoryResponse{}, err
	}
	var out MetricsHistoryResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return MetricsHistoryResponse{}, fmt.Errorf("metrics history: parse response: %w", err)
	}
	return out, nil
}

type Identity struct {
	ServerName    string `json:"serverName"`
	Port          string `json:"port,omitempty"`
	LocalIP       string `json:"localIp"`
	ExternalIP    string `json:"externalIp"`
	DaemonVersion string `json:"daemonVersion"`
}

func (c *Client) Identity() (Identity, error) {
	data, err := c.do(http.MethodGet, "/api/identity", nil)
	if err != nil {
		return Identity{}, err
	}
	var id Identity
	if err := json.Unmarshal(data, &id); err != nil {
		return Identity{}, fmt.Errorf("identity: parse response: %w", err)
	}
	return id, nil
}

type Playfields struct {
	Active int `json:"active"`
	Total  int `json:"total"`
}

func (c *Client) Playfields() (Playfields, error) {
	data, err := c.do(http.MethodGet, "/api/playfields/summary", nil)
	if err != nil {
		return Playfields{}, err
	}
	var p Playfields
	if err := json.Unmarshal(data, &p); err != nil {
		return Playfields{}, fmt.Errorf("playfields: parse response: %w", err)
	}
	return p, nil
}

type Status struct {
	Active          bool   `json:"active"`
	TelnetOK        bool   `json:"telnet_ok"`
	Version         string `json:"version,omitempty"`
	Mode            string `json:"mode,omitempty"`
	Name            string `json:"name,omitempty"`
	Seed            string `json:"seed,omitempty"`
	StartInProgress bool   `json:"startInProgress"`

	ScenarioCache ScenarioCacheStatus `json:"scenarioCache,omitzero"`
}

type Health struct {
	Status
	Hung     bool      `json:"hung"`
	BadSince time.Time `json:"badSince,omitempty"`
}

func (c *Client) Health() (Health, error) {
	data, err := c.do(http.MethodGet, "/api/health", nil)
	if err != nil {
		return Health{}, err
	}
	var h Health
	if err := json.Unmarshal(data, &h); err != nil {
		return Health{}, fmt.Errorf("health: parse response: %w", err)
	}
	return h, nil
}

type serverTimeResponse struct {
	Formatted     string `json:"formatted"`
	OffsetSeconds int    `json:"offsetSeconds"`
}

func (c *Client) serverTime() (serverTimeResponse, error) {
	data, err := c.do(http.MethodGet, "/api/time", nil)
	if err != nil {
		return serverTimeResponse{}, err
	}
	var resp serverTimeResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return serverTimeResponse{}, fmt.Errorf("server time: parse response: %w", err)
	}
	return resp, nil
}

func (c *Client) ServerTime() (string, error) {
	resp, err := c.serverTime()
	if err != nil {
		return "", err
	}
	return resp.Formatted, nil
}

func (c *Client) ServerLocation() (*time.Location, error) {
	resp, err := c.serverTime()
	if err != nil {
		return nil, err
	}
	return time.FixedZone("server", resp.OffsetSeconds), nil
}

type ManualAction struct {
	Type     string    `json:"type"`
	Since    time.Time `json:"since"`
	Deadline time.Time `json:"deadline,omitempty"`
}

func (c *Client) ManualActionStatus() (ManualAction, error) {
	data, err := c.do(http.MethodGet, "/api/server/manual-status", nil)
	if err != nil {
		return ManualAction{}, err
	}
	if string(data) == "null" {
		return ManualAction{}, nil
	}
	var m ManualAction
	if err := json.Unmarshal(data, &m); err != nil {
		return ManualAction{}, fmt.Errorf("manualActionStatus: parse response: %w", err)
	}
	return m, nil
}

func (c *Client) StartServer() error {
	_, err := c.do(http.MethodPost, "/api/server/start", nil)
	return err
}

func (c *Client) ManualStopServer() error {
	_, err := c.do(http.MethodPost, "/api/server/manual-stop", nil)
	return err
}

func (c *Client) ManualRestartServer() error {
	_, err := c.do(http.MethodPost, "/api/server/manual-restart", nil)
	return err
}

func (c *Client) Items() (map[string]int, error) {
	data, err := c.do(http.MethodGet, "/api/items", nil)
	if err != nil {
		return nil, err
	}
	var items map[string]int
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("items: parse response: %w", err)
	}
	return items, nil
}

func (c *Client) ItemStackSizes() (map[string]int, error) {
	data, err := c.do(http.MethodGet, "/api/items/stacksize", nil)
	if err != nil {
		return nil, err
	}
	var m map[string]int
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("stacksize: parse response: %w", err)
	}
	return m, nil
}

func (c *Client) ItemCategories() (map[string]string, error) {
	data, err := c.do(http.MethodGet, "/api/items/categories", nil)
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("categories: parse response: %w", err)
	}
	return m, nil
}

func (c *Client) ItemAmmoTypes() (map[string]string, error) {
	data, err := c.do(http.MethodGet, "/api/items/ammotype", nil)
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("ammotype: parse response: %w", err)
	}
	return m, nil
}

func (c *Client) ItemDisplayNames(lang string) (map[string]string, error) {
	q := url.Values{}
	q.Set("lang", lang)
	data, err := c.do(http.MethodGet, "/api/items/displayname?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("displayname: parse response: %w", err)
	}
	return m, nil
}

func (c *Client) ItemDescriptions(lang string) (map[string]string, error) {
	q := url.Values{}
	q.Set("lang", lang)
	data, err := c.do(http.MethodGet, "/api/items/description?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("description: parse response: %w", err)
	}
	return m, nil
}

type IconInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type IconManifestResponse struct {
	Scenario string     `json:"scenario"`
	Icons    []IconInfo `json:"icons"`
}

func (c *Client) IconManifest() (IconManifestResponse, error) {
	data, err := c.do(http.MethodGet, "/api/icons", nil)
	if err != nil {
		return IconManifestResponse{}, err
	}
	var out IconManifestResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return IconManifestResponse{}, fmt.Errorf("icons: parse response: %w", err)
	}
	return out, nil
}

func (c *Client) IconData(name string) ([]byte, error) {
	return c.do(http.MethodGet, "/api/icons/"+url.PathEscape(name), nil)
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
	SteamId  string      `json:"steamId"`
	Toolbelt []ItemStack `json:"toolbelt"`
	Bag      []ItemStack `json:"bag"`

	BaselineToolbelt []ItemStack `json:"baselineToolbelt,omitempty"`
	BaselineBag      []ItemStack `json:"baselineBag,omitempty"`

	Part string `json:"part,omitempty"`
}

func (c *Client) PlayerInventory(playerId int) (Inventory, error) {
	data, err := c.do(http.MethodGet, fmt.Sprintf("/api/players/inventory?playerId=%d", playerId), nil)
	if err != nil {
		return Inventory{}, err
	}
	var inv Inventory
	if err := json.Unmarshal(data, &inv); err != nil {
		return Inventory{}, fmt.Errorf("inventory: parse response: %w", err)
	}
	return inv, nil
}

type SetInventoryResult struct {
	Applied  bool `json:"applied"`
	Queued   bool `json:"queued"`
	Conflict bool `json:"conflict"`
}

func (c *Client) SetPlayerInventory(inv Inventory) (SetInventoryResult, error) {
	data, err := c.do(http.MethodPost, "/api/players/inventory", inv)
	if err != nil {
		return SetInventoryResult{}, err
	}
	var res SetInventoryResult
	if err := json.Unmarshal(data, &res); err != nil {
		return SetInventoryResult{}, fmt.Errorf("set inventory: parse response: %w", err)
	}
	return res, nil
}

type BackpackEntry struct {
	Timestamp time.Time   `json:"timestamp"`
	Toolbelt  []ItemStack `json:"toolbelt"`
	Bag       []ItemStack `json:"bag"`
}

func (c *Client) BackpackHistory(steamID string) ([]BackpackEntry, error) {
	data, err := c.do(http.MethodGet, "/api/players/backpack-history?steamId="+url.QueryEscape(steamID), nil)
	if err != nil {
		return nil, err
	}
	var out []BackpackEntry
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("backpack history: parse response: %w", err)
	}
	return out, nil
}

func (c *Client) BackpackRestore(steamID string, playerId int, timestamp time.Time, part string) error {
	_, err := c.do(http.MethodPost, "/api/players/backpack-restore", map[string]any{
		"steamId":   steamID,
		"playerId":  playerId,
		"timestamp": timestamp,
		"part":      part,
	})
	return err
}

func (c *Client) PendingEdits(steamID string) (map[string]json.RawMessage, error) {
	data, err := c.do(http.MethodGet, "/api/players/pending?steamId="+url.QueryEscape(steamID), nil)
	if err != nil {
		return nil, err
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("pending edits: parse response: %w", err)
	}
	return out, nil
}

type PlayerStatsSnapshot struct {
	Found                                bool
	Health, HealthMax                    float64
	Food, FoodMax                        float64
	Oxygen, OxygenMax                    float64
	Stamina, StaminaMax                  float64
	Radiation, RadiationMax              float64
	BodyTemp, BodyTempMax                float64
	FactionGroup, FactionId, FactionRole int
	Credits                              float64
	Exp, Upgrade                         int

	BpRemainingTime      float64
	BpInFactory          string
	BpResourcesInFactory map[string]float64
}

func (c *Client) PlayerStats(steamID string) (PlayerStatsSnapshot, error) {
	data, err := c.do(http.MethodGet, "/api/players/stats?steamId="+url.QueryEscape(steamID), nil)
	if err != nil {
		return PlayerStatsSnapshot{}, err
	}
	var out PlayerStatsSnapshot
	if err := json.Unmarshal(data, &out); err != nil {
		return PlayerStatsSnapshot{}, fmt.Errorf("player stats: parse response: %w", err)
	}
	return out, nil
}

type DaemonLogEntry struct {
	Time time.Time `json:"time"`
	Text string    `json:"text"`
}

func (c *Client) DaemonLogs(n int) ([]DaemonLogEntry, error) {
	data, err := c.do(http.MethodGet, fmt.Sprintf("/api/logs/daemon?n=%d", n), nil)
	if err != nil {
		return nil, err
	}
	var out []DaemonLogEntry
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("daemon logs: parse response: %w", err)
	}
	return out, nil
}

func (c *Client) DaemonLogByDate(date string) ([]DaemonLogEntry, error) {
	data, err := c.do(http.MethodGet, "/api/logs/daemon?date="+url.QueryEscape(date), nil)
	if err != nil {
		return nil, err
	}
	var out []DaemonLogEntry
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("daemon log by date: parse response: %w", err)
	}
	return out, nil
}

func (c *Client) DaemonLogDates() ([]string, error) {
	data, err := c.do(http.MethodGet, "/api/logs/daemon/dates", nil)
	if err != nil {
		return nil, err
	}
	var out []string
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("daemon log dates: parse response: %w", err)
	}
	return out, nil
}

type ServerLogLine struct {
	Text     string    `json:"text"`
	Category string    `json:"category"`
	Severity string    `json:"severity,omitempty"`
	Time     time.Time `json:"time"`
}

func (c *Client) ServerLog(maxBytes int64) ([]ServerLogLine, error) {
	data, err := c.do(http.MethodGet, fmt.Sprintf("/api/logs/server?maxBytes=%d", maxBytes), nil)
	if err != nil {
		return nil, err
	}
	var out []ServerLogLine
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("server log: parse response: %w", err)
	}
	return out, nil
}

func (c *Client) ServerLogByDate(date string) ([]ServerLogLine, error) {
	data, err := c.do(http.MethodGet, "/api/logs/server?date="+url.QueryEscape(date), nil)
	if err != nil {
		return nil, err
	}
	var out []ServerLogLine
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("server log by date: parse response: %w", err)
	}
	return out, nil
}

func (c *Client) ServerLogDates() ([]string, error) {
	data, err := c.do(http.MethodGet, "/api/logs/server/dates", nil)
	if err != nil {
		return nil, err
	}
	var out []string
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("server log dates: parse response: %w", err)
	}
	return out, nil
}

func (c *Client) KickPlayer(steamID, message string) error {
	_, err := c.do(http.MethodPost, "/api/players/kick", map[string]string{"player": steamID, "message": message})
	return err
}

func (c *Client) BanPlayer(steamID, duration string) error {
	_, err := c.do(http.MethodPost, "/api/players/ban", map[string]string{"player": steamID, "duration": duration})
	return err
}

func (c *Client) MutePlayer(steamID, duration string) error {
	_, err := c.do(http.MethodPost, "/api/players/mute", map[string]string{"player": steamID, "duration": duration})
	return err
}

func (c *Client) UnbanPlayer(steamID string) error {
	_, err := c.do(http.MethodPost, "/api/players/unban", map[string]string{"player": steamID})
	return err
}

func (c *Client) UnmutePlayer(steamID string) error {
	_, err := c.do(http.MethodPost, "/api/players/unmute", map[string]string{"player": steamID})
	return err
}

func (c *Client) SetRolePlayer(steamID, role string) error {
	_, err := c.do(http.MethodPost, "/api/players/setrole", map[string]string{"player": steamID, "role": role})
	return err
}

func (c *Client) SetPlayerInfo(entityID int, steamID string, fields map[string]any) error {
	body := map[string]any{"playerId": entityID, "steamId": steamID}
	for k, v := range fields {
		body[k] = v
	}
	_, err := c.do(http.MethodPost, "/api/players/setinfo", body)
	return err
}

func (c *Client) AddCredits(entityID int, steamID string, amount float64) error {
	_, err := c.do(http.MethodPost, "/api/players/addcredits", map[string]any{"playerId": entityID, "steamId": steamID, "amount": amount})
	return err
}

func (c *Client) FinishFactoryBlueprint(entityID int) error {
	_, err := c.do(http.MethodPost, "/api/players/factory/finish", map[string]any{"id": entityID})
	return err
}

func (c *Client) Teleport(entityID int, steamID, playfield string, pos Vec3, rot *Vec3) error {
	body := map[string]any{
		"playerId": entityID, "steamId": steamID,
		"x": pos.X, "y": pos.Y, "z": pos.Z,
	}
	if playfield != "" {
		body["playfield"] = playfield
	}
	if rot != nil {
		body["rotX"], body["rotY"], body["rotZ"] = rot.X, rot.Y, rot.Z
	}
	_, err := c.do(http.MethodPost, "/api/players/teleport", body)
	return err
}

type HistoryEvent struct {
	ID           int64          `json:"id"`
	Ts           string         `json:"ts"`
	Kind         string         `json:"kind"`
	EntityKey    string         `json:"entityKey"`
	Name         string         `json:"name"`
	Playfield    string         `json:"playfield"`
	Pos          Vec3           `json:"pos"`
	Changed      map[string]any `json:"changed,omitempty"`
	IsFirst      bool           `json:"isFirst"`
	IsDeleted    bool           `json:"isDeleted"`
	OnlyPosition bool           `json:"onlyPosition"`
	Distance     float64        `json:"distance"`
}

type HistoryResult struct {
	Events []HistoryEvent `json:"events"`
	Total  int            `json:"total"`
}

func (c *Client) HistoryAround(kind, key, eventKind, playfield, from, to string, distance float64, hideFirst, hideOnlyPosition bool, limit, offset int) (HistoryResult, error) {
	body := map[string]any{
		"kind": kind, "key": key, "eventKind": eventKind, "playfield": playfield,
		"from": from, "to": to, "distance": distance,
		"hideFirst": hideFirst, "hideOnlyPosition": hideOnlyPosition, "limit": limit, "offset": offset,
	}
	data, err := c.do(http.MethodPost, "/api/history/around", body)
	if err != nil {
		return HistoryResult{}, err
	}
	var result HistoryResult
	if err := json.Unmarshal(data, &result); err != nil {
		return HistoryResult{}, fmt.Errorf("history: parse response: %w", err)
	}
	return result, nil
}

func (c *Client) PlayfieldSystems() (map[string]string, error) {
	data, err := c.do(http.MethodGet, "/api/playfields/systems", nil)
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("playfields/systems: parse response: %w", err)
	}
	return m, nil
}

func (c *Client) HistoryCounts(kind string) (map[string]int, error) {
	data, err := c.do(http.MethodGet, "/api/history/counts?kind="+url.QueryEscape(kind), nil)
	if err != nil {
		return nil, err
	}
	var out map[string]int
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("history counts: parse response: %w", err)
	}
	return out, nil
}

func (c *Client) Console(command string) (string, error) {
	data, err := c.do(http.MethodPost, "/api/console", map[string]string{"command": command})
	if err != nil {
		return "", err
	}
	var v struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return "", fmt.Errorf("console: parse response: %w", err)
	}
	return v.Output, nil
}

type PdaChapter struct {
	Number int    `json:"number"`
	Key    string `json:"key"`
	Title  string `json:"title"`
}

func (c *Client) PdaChapters(lang string) ([]PdaChapter, error) {
	q := url.Values{}
	q.Set("lang", lang)
	data, err := c.do(http.MethodGet, "/api/pda/chapters?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var v []PdaChapter
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("pda chapters: parse response: %w", err)
	}
	return v, nil
}

func (c *Client) PlayerRemoteEx(steamId, command string) (string, error) {
	data, err := c.do(http.MethodPost, "/api/players/remoteex", map[string]string{"steamId": steamId, "command": command})
	if err != nil {
		return "", err
	}
	var v struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return "", fmt.Errorf("player remoteex: parse response: %w", err)
	}
	return v.Output, nil
}

func (c *Client) MessageAll(text string, prio int, duration float64) error {
	_, err := c.do(http.MethodPost, "/api/message/all", map[string]any{
		"text": text, "prio": prio, "duration": duration,
	})
	return err
}

func (c *Client) MessagePlayer(entityID int, text string, prio int, duration float64) error {
	_, err := c.do(http.MethodPost, "/api/message/player", map[string]any{
		"playerId": entityID, "text": text, "prio": prio, "duration": duration,
	})
	return err
}

type TaskParams struct {
	Text    string `json:"text,omitempty"`
	Command string `json:"command,omitempty"`

	SnapshotName string `json:"snapshotName,omitempty"`

	LowPriority bool `json:"lowPriority,omitempty"`

	MakeSnapshot bool `json:"makeSnapshot,omitempty"`

	BackupEnabled      bool   `json:"backupEnabled,omitempty"`
	BackupDeferred     bool   `json:"backupDeferred,omitempty"`
	BackupDeferredTime string `json:"backupDeferredTime,omitempty"`

	WipePlayfields []string `json:"wipePlayfields,omitempty"`
	WipeTokens     []string `json:"wipeTokens,omitempty"`

	WarnMinutes int `json:"warnMinutes,omitempty"`

	Channel       string `json:"channel,omitempty"`
	FactionID     string `json:"factionId,omitempty"`
	RecipientName string `json:"recipientName,omitempty"`

	BannerText          string `json:"bannerText,omitempty"`
	BannerPriority      int    `json:"bannerPriority,omitempty"`
	BannerRecipientName string `json:"bannerRecipientName,omitempty"`

	ResetMode          string `json:"resetMode,omitempty"`
	ResetIfEmpty       bool   `json:"resetIfEmpty,omitempty"`
	ResetScanAllDefect bool   `json:"resetScanAllDefect,omitempty"`

	Days int `json:"days,omitempty"`

	ScriptName           string `json:"scriptName,omitempty"`
	ScriptTimeoutSeconds int    `json:"scriptTimeoutSeconds,omitempty"`
	ScriptOutputMode     string `json:"scriptOutputMode,omitempty"`
}

type Task struct {
	ID              int64      `json:"id"`
	Active          bool       `json:"active"`
	ActionType      string     `json:"actionType"`
	Params          TaskParams `json:"params"`
	Repeat          string     `json:"repeat"`
	IntervalMinutes int        `json:"intervalMinutes,omitempty"`
	TimeOfDay       string     `json:"timeOfDay,omitempty"`
	Weekday         int        `json:"weekday,omitempty"`
	MonthDay        int        `json:"monthDay,omitempty"`
	OnceAt          string     `json:"onceAt,omitempty"`
	NextRun         string     `json:"nextRun,omitempty"`
	CreatedAt       string     `json:"createdAt,omitempty"`
	UpdatedAt       string     `json:"updatedAt,omitempty"`
}

type TaskRun struct {
	ID         int64      `json:"id"`
	TaskID     int64      `json:"taskId"`
	SubtaskID  int64      `json:"subtaskId,omitempty"`
	ActionType string     `json:"actionType"`
	Params     TaskParams `json:"params"`
	RanAt      string     `json:"ranAt"`
	Success    bool       `json:"success"`
	Message    string     `json:"message"`
}

type Subtask struct {
	ID            int64      `json:"id"`
	TaskID        int64      `json:"taskId"`
	Active        bool       `json:"active"`
	ActionType    string     `json:"actionType"`
	Params        TaskParams `json:"params"`
	OffsetMinutes int        `json:"offsetMinutes"`
	RunCondition  string     `json:"runCondition"`
	NextRun       string     `json:"nextRun,omitempty"`
	CreatedAt     string     `json:"createdAt,omitempty"`
	UpdatedAt     string     `json:"updatedAt,omitempty"`
}

func (c *Client) Tasks() ([]Task, error) {
	data, err := c.do(http.MethodGet, "/api/scheduler/tasks", nil)
	if err != nil {
		return nil, err
	}
	var v []Task
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("tasks: parse response: %w", err)
	}
	return v, nil
}

func (c *Client) CreateTask(t Task) (Task, error) {
	data, err := c.do(http.MethodPost, "/api/scheduler/tasks", t)
	if err != nil {
		return Task{}, err
	}
	var v Task
	if err := json.Unmarshal(data, &v); err != nil {
		return Task{}, fmt.Errorf("createTask: parse response: %w", err)
	}
	return v, nil
}

func (c *Client) UpdateTask(t Task) (Task, error) {
	data, err := c.do(http.MethodPut, fmt.Sprintf("/api/scheduler/tasks/%d", t.ID), t)
	if err != nil {
		return Task{}, err
	}
	var v Task
	if err := json.Unmarshal(data, &v); err != nil {
		return Task{}, fmt.Errorf("updateTask: parse response: %w", err)
	}
	return v, nil
}

func (c *Client) DeleteTask(id int64) error {
	_, err := c.do(http.MethodDelete, fmt.Sprintf("/api/scheduler/tasks/%d", id), nil)
	return err
}

func (c *Client) RunTaskNow(id int64) error {
	_, err := c.do(http.MethodPost, fmt.Sprintf("/api/scheduler/tasks/%d/run", id), nil)
	return err
}

func (c *Client) CloneTask(id int64) (Task, error) {
	data, err := c.do(http.MethodPost, fmt.Sprintf("/api/scheduler/tasks/%d/clone", id), nil)
	if err != nil {
		return Task{}, err
	}
	var v Task
	if err := json.Unmarshal(data, &v); err != nil {
		return Task{}, fmt.Errorf("cloneTask: parse response: %w", err)
	}
	return v, nil
}

func (c *Client) Subtasks(taskID int64) ([]Subtask, error) {
	data, err := c.do(http.MethodGet, fmt.Sprintf("/api/scheduler/tasks/%d/subtasks", taskID), nil)
	if err != nil {
		return nil, err
	}
	var v []Subtask
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("subtasks: parse response: %w", err)
	}
	return v, nil
}

func (c *Client) CreateSubtask(s Subtask) (Subtask, error) {
	data, err := c.do(http.MethodPost, fmt.Sprintf("/api/scheduler/tasks/%d/subtasks", s.TaskID), s)
	if err != nil {
		return Subtask{}, err
	}
	var v Subtask
	if err := json.Unmarshal(data, &v); err != nil {
		return Subtask{}, fmt.Errorf("createSubtask: parse response: %w", err)
	}
	return v, nil
}

func (c *Client) UpdateSubtask(s Subtask) (Subtask, error) {
	data, err := c.do(http.MethodPut, fmt.Sprintf("/api/scheduler/subtasks/%d", s.ID), s)
	if err != nil {
		return Subtask{}, err
	}
	var v Subtask
	if err := json.Unmarshal(data, &v); err != nil {
		return Subtask{}, fmt.Errorf("updateSubtask: parse response: %w", err)
	}
	return v, nil
}

func (c *Client) DeleteSubtask(id int64) error {
	_, err := c.do(http.MethodDelete, fmt.Sprintf("/api/scheduler/subtasks/%d", id), nil)
	return err
}

func (c *Client) RunSubtaskNow(id int64) error {
	_, err := c.do(http.MethodPost, fmt.Sprintf("/api/scheduler/subtasks/%d/run", id), nil)
	return err
}

func (c *Client) WipeablePlayfields() ([]string, error) {
	data, err := c.do(http.MethodGet, "/api/playfields/wipeable", nil)
	if err != nil {
		return nil, err
	}
	var v []string
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("wipeablePlayfields: parse response: %w", err)
	}
	return v, nil
}

type Snapshot struct {
	Name             string   `json:"name"`
	Path             string   `json:"path"`
	Subvolumes       []string `json:"subvolumes"`
	Archiving        bool     `json:"archiving,omitempty"`
	HasArchive       bool     `json:"hasArchive,omitempty"`
	ArchiveSizeBytes int64    `json:"archiveSizeBytes,omitempty"`

	SizeBytes int64 `json:"sizeBytes,omitempty"`
}

func (c *Client) Snapshots() ([]Snapshot, error) {
	data, err := c.do(http.MethodGet, "/api/snapshots", nil)
	if err != nil {
		return nil, err
	}
	var v []Snapshot
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("snapshots: parse response: %w", err)
	}
	return v, nil
}

func (c *Client) RestoreSnapshot(name string) error {
	_, err := c.do(http.MethodPost, "/api/snapshots/"+url.PathEscape(name)+"/restore", nil)
	return err
}

func (c *Client) DeleteBackup(dest int, name string) error {
	_, err := c.do(http.MethodDelete, fmt.Sprintf("/api/backups?dest=%d&name=%s", dest, url.QueryEscape(name)), nil)
	return err
}

func (c *Client) BackupArchiveReader(dest int, name string) (io.ReadCloser, int64, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/backups/download?dest=%d&name=%s", c.baseURL, dest, url.QueryEscape(name)), nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	streamClient := &http.Client{Transport: c.http.Transport}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("archive %s: %w", name, err)
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return nil, 0, fmt.Errorf("archive %s: %s", name, errorMessage(data, resp.StatusCode))
	}
	return resp.Body, resp.ContentLength, nil
}

func (c *Client) ScenarioCacheReader() (io.ReadCloser, int64, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/api/scenario-cache/file", nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	streamClient := &http.Client{Transport: c.http.Transport}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("scenario cache file: %w", err)
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return nil, 0, fmt.Errorf("scenario cache file: %s", errorMessage(data, resp.StatusCode))
	}
	return resp.Body, resp.ContentLength, nil
}

func (c *Client) ClearBackupDestination(dest int) (skipped []string, err error) {
	data, err := c.do(http.MethodPost, fmt.Sprintf("/api/backups/clear?dest=%d", dest), nil)
	if err != nil {
		return nil, err
	}
	var v struct {
		Skipped []string `json:"skipped"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("clearBackupDestination: parse response: %w", err)
	}
	return v.Skipped, nil
}

type ServerField struct {
	Value string `json:"value"`
	Set   bool   `json:"set"`
}

const (
	ActionDialog = "dialog"
	ActionChat   = "chat"
	ActionBanner = "banner"
)

type CustomCommand struct {
	Name string `json:"name"`

	Text    string `json:"text"`
	Enabled bool   `json:"enabled"`

	ActionType string `json:"actionType,omitempty"`

	BannerPriority int `json:"bannerPriority,omitempty"`

	GameDescription string `json:"gameDescription,omitempty"`

	ScriptName string `json:"scriptName,omitempty"`

	CooldownMinutes int `json:"cooldownMinutes,omitempty"`
}

type ChatbotSettings struct {
	WipeCooldownHours              int             `json:"wipeCooldownHours"`
	VehicleTeleportCooldownMinutes int             `json:"vehicleTeleportCooldownMinutes"`
	HardWipe                       bool            `json:"hardWipe"`
	CommandsEnabled                map[string]bool `json:"commandsEnabled,omitempty"`

	CommandDescriptions map[string]string `json:"commandDescriptions,omitempty"`

	RulesText      string          `json:"rulesText,omitempty"`
	CustomCommands []CustomCommand `json:"customCommands,omitempty"`

	AdminSteamIds []string `json:"adminSteamIds,omitempty"`

	ModeratorSteamIds []string `json:"moderatorSteamIds,omitempty"`

	NotifyPlayerPresence bool `json:"notifyPlayerPresence,omitempty"`

	SurvivalCredits float64        `json:"survivalCredits"`
	SurvivalItems   []SurvivalItem `json:"survivalItems,omitempty"`

	ServerSenderName string `json:"serverSenderName,omitempty"`

	WelcomeMessage WelcomeMessage `json:"welcomeMessage,omitempty"`
}

type WelcomeMessage struct {
	Enabled bool `json:"enabled"`

	Messages []WelcomeMessageItem `json:"messages,omitempty"`
}

type WelcomeMessageItem struct {
	DelaySeconds int    `json:"delaySeconds"`
	Text         string `json:"text"`

	ActionType string `json:"actionType,omitempty"`

	BannerPriority int `json:"bannerPriority,omitempty"`
}

type SurvivalItem struct {
	ItemID int `json:"itemId"`
	Count  int `json:"count"`
}

type WatchdogSettings struct {
	AutoRestartOnHang bool `json:"autoRestartOnHang"`

	LogHangs *bool `json:"logHangs,omitempty"`
}

type SchedulerSettings struct {
	Paused bool `json:"paused,omitempty"`
}

type BackpackSettings struct {
	MinHistoryIntervalSeconds int `json:"minHistoryIntervalSeconds"`

	ForceOverwriteInventory bool `json:"forceOverwriteInventory"`

	MaxHistoryEntries int `json:"maxHistoryEntries"`
}

type StructuresSettings struct {
	BackupIntervalMinutes int  `json:"backupIntervalMinutes"`
	MaxBackupEntries      int  `json:"maxBackupEntries"`
	RequireOwnerNearby    bool `json:"requireOwnerNearby"`
}

type BackupSettings struct {
	Destinations [2]BackupDestination `json:"destinations"`

	Exclude BackupExclude `json:"exclude,omitempty"`
}

type BackupExclude struct {
	Elb         bool     `json:"elb,omitempty"`
	Wine        bool     `json:"wine,omitempty"`
	Saves       []string `json:"saves,omitempty"`
	ContentMode string   `json:"contentMode,omitempty"`
	Mods        []string `json:"mods,omitempty"`
	Scenarios   []string `json:"scenarios,omitempty"`
}

type BackupDestination struct {
	Type     string `json:"type,omitempty"`
	DiskPath string `json:"diskPath,omitempty"`

	FTPProtocol string `json:"ftpProtocol,omitempty"`
	FTPHost     string `json:"ftpHost,omitempty"`
	FTPPort     int    `json:"ftpPort,omitempty"`
	FTPUser     string `json:"ftpUser,omitempty"`
	FTPPassword string `json:"ftpPassword,omitempty"`
	FTPKey      string `json:"ftpKey,omitempty"`
	FTPPath     string `json:"ftpPath,omitempty"`

	S3Endpoint  string `json:"s3Endpoint,omitempty"`
	S3Region    string `json:"s3Region,omitempty"`
	S3Bucket    string `json:"s3Bucket,omitempty"`
	S3AccessKey string `json:"s3AccessKey,omitempty"`
	S3SecretKey string `json:"s3SecretKey,omitempty"`

	Retention Retention `json:"retention,omitempty"`
}

type Retention struct {
	Last    int `json:"last,omitempty"`
	Hourly  int `json:"hourly,omitempty"`
	Daily   int `json:"daily,omitempty"`
	Weekly  int `json:"weekly,omitempty"`
	Monthly int `json:"monthly,omitempty"`
	Yearly  int `json:"yearly,omitempty"`
}

type MonitoringSettings struct {
	EnabledMounts   []string `json:"enabledMounts"`
	WarningPercent  int      `json:"warningPercent"`
	CriticalPercent int      `json:"criticalPercent"`

	HistoryRetentionDays int `json:"historyRetentionDays"`
}

type MountInfo struct {
	Mountpoint string `json:"mountpoint"`
	FSType     string `json:"fsType"`
	Device     string `json:"device"`
	UsedBytes  uint64 `json:"usedBytes"`
	TotalBytes uint64 `json:"totalBytes"`
}

func (c *Client) Mounts() ([]MountInfo, error) {
	data, err := c.do(http.MethodGet, "/api/sysinfo/mounts", nil)
	if err != nil {
		return nil, err
	}
	var v []MountInfo
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("mounts: parse response: %w", err)
	}
	return v, nil
}

func FilterMounts(mounts []MountInfo, enabled []string) []MountInfo {
	if enabled == nil {
		return mounts
	}
	want := map[string]bool{}
	for _, m := range enabled {
		want[m] = true
	}
	var out []MountInfo
	for _, m := range mounts {
		if want[m.Mountpoint] {
			out = append(out, m)
		}
	}
	return out
}

type ServerSettings struct {
	SrvName        ServerField `json:"srvName"`
	SrvPassword    ServerField `json:"srvPassword"`
	SrvMaxPlayers  ServerField `json:"srvMaxPlayers"`
	SrvDescription ServerField `json:"srvDescription"`

	SrvReservePlayfields      ServerField `json:"srvReservePlayfields"`
	SrvPublic                 ServerField `json:"srvPublic"`
	SrvStopPeriod             ServerField `json:"srvStopPeriod"`
	EACActive                 ServerField `json:"eacActive"`
	MaxAllowedSizeClass       ServerField `json:"maxAllowedSizeClass"`
	AllowedBlueprints         ServerField `json:"allowedBlueprints"`
	HeartbeatServer           ServerField `json:"heartbeatServer"`
	HeartbeatClient           ServerField `json:"heartbeatClient"`
	LogFlags                  ServerField `json:"logFlags"`
	DisableSteamFamilySharing ServerField `json:"disableSteamFamilySharing"`
	KickPlayerWithPing        ServerField `json:"kickPlayerWithPing"`
	TimeoutBootingPfServer    ServerField `json:"timeoutBootingPfServer"`
	PlayerLoginParallelCount  ServerField `json:"playerLoginParallelCount"`
	PlayerLoginVipNames       ServerField `json:"playerLoginVipNames"`
	EnableDLC                 ServerField `json:"enableDLC"`
	Mode                      ServerField `json:"mode"`

	GameName ServerField `json:"gameName"`
	Seed     ServerField `json:"seed"`

	CustomScenario ServerField `json:"customScenario"`
}

type AppConfig struct {
	Chatbot       ChatbotSettings       `json:"chatbot"`
	Watchdog      WatchdogSettings      `json:"watchdog"`
	Backpack      BackpackSettings      `json:"backpack"`
	Structures    StructuresSettings    `json:"structures"`
	Backup        BackupSettings        `json:"backup"`
	Monitoring    MonitoringSettings    `json:"monitoring"`
	Maintenance   MaintenanceSettings   `json:"maintenance"`
	Steam         SteamSettings         `json:"steam"`
	Scheduler     SchedulerSettings     `json:"scheduler"`
	Server        ServerSettings        `json:"server"`
	Audit         AuditSettings         `json:"audit"`
	ScenarioCache ScenarioCacheSettings `json:"scenarioCache"`
}

type ScenarioCacheSettings struct {
	Mode          string `json:"mode,omitempty"`
	PublicURLBase string `json:"publicUrlBase,omitempty"`
	FTPProtocol   string `json:"ftpProtocol,omitempty"`
	FTPHost       string `json:"ftpHost,omitempty"`
	FTPPort       int    `json:"ftpPort,omitempty"`
	FTPUser       string `json:"ftpUser,omitempty"`
	FTPPassword   string `json:"ftpPassword,omitempty"`
	FTPKey        string `json:"ftpKey,omitempty"`
	FTPPath       string `json:"ftpPath,omitempty"`

	EmbeddedListenAddr string `json:"embeddedListenAddr,omitempty"`
	PanelListenPort    int    `json:"panelListenPort,omitempty"`
}

type AuditSettings struct {
	UnauthorizedAPIRequests bool `json:"unauthorizedApiRequests,omitempty"`
	InventoryFuelAnomaly    bool `json:"inventoryFuelAnomaly,omitempty"`
	WipeLeftover            bool `json:"wipeLeftover,omitempty"`
	PositionJump            bool `json:"positionJump,omitempty"`
	PermissionFactionChange bool `json:"permissionFactionChange,omitempty"`
	LoginTracking           bool `json:"loginTracking,omitempty"`
}

type SteamSettings struct {
	Username       string                  `json:"username,omitempty"`
	WebAPIKey      string                  `json:"webApiKey,omitempty"`
	SteamID64      string                  `json:"steamId64,omitempty"`
	WorkshopAppID  string                  `json:"workshopAppId,omitempty"`
	InstalledItems []InstalledWorkshopItem `json:"installedItems,omitempty"`
}

type InstalledWorkshopItem struct {
	PublishedFileID string `json:"publishedFileId"`
	Title           string `json:"title"`
	Kind            string `json:"kind"`
	TargetDir       string `json:"targetDir"`
	TargetPath      string `json:"targetPath"`
	SizeBytes       int64  `json:"sizeBytes"`
	TimeUpdated     int64  `json:"timeUpdated"`
}

type MaintenanceSettings struct {
	CleanOldBuildLogs bool `json:"cleanOldBuildLogs,omitempty"`

	ManualActionWarnMinutes int `json:"manualActionWarnMinutes,omitempty"`

	CheckUpdatesPeriodically bool `json:"checkUpdatesPeriodically,omitempty"`
}

func (c *Client) SetBackupConfig(backup BackupSettings) error {
	body := map[string]any{"backup": map[string]any{
		"destinations": backup.Destinations,
		"exclude":      backup.Exclude,
	}}
	_, err := c.do(http.MethodPost, "/api/config", body)
	return err
}

func (c *Client) RunBackupNow() error {
	_, err := c.do(http.MethodPost, "/api/backups/run", nil)
	return err
}

func (c *Client) WipeLocalData(stores []string) error {
	_, err := c.do(http.MethodPost, "/api/maintenance/wipe-local-db", map[string]any{"stores": stores})
	return err
}

type DuplicateGroup struct {
	Name   string `json:"name"`
	Keep   int    `json:"keep"`
	Remove []int  `json:"remove"`
}

func (c *Client) DuplicatePlayers() ([]DuplicateGroup, error) {
	data, err := c.do(http.MethodGet, "/api/maintenance/duplicate-players", nil)
	if err != nil {
		return nil, err
	}
	var v []DuplicateGroup
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("duplicate players: parse response: %w", err)
	}
	return v, nil
}

func (c *Client) CleanupDuplicatePlayers(groups []DuplicateGroup) (int, error) {
	data, err := c.do(http.MethodPost, "/api/maintenance/duplicate-players/cleanup", groups)
	if err != nil {
		return 0, err
	}
	var v struct {
		Removed int `json:"removed"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return 0, fmt.Errorf("cleanup duplicate players: parse response: %w", err)
	}
	return v.Removed, nil
}

func (c *Client) TestBackupDestination(d BackupDestination) error {
	_, err := c.do(http.MethodPost, "/api/backups/destinations/test", d)
	return err
}

type BackupDestinationInfo struct {
	Index    int    `json:"index"`
	Type     string `json:"type"`
	DiskPath string `json:"diskPath,omitempty"`

	FTPProtocol string             `json:"ftpProtocol,omitempty"`
	FTPHost     string             `json:"ftpHost,omitempty"`
	FTPPath     string             `json:"ftpPath,omitempty"`
	S3Endpoint  string             `json:"s3Endpoint,omitempty"`
	S3Bucket    string             `json:"s3Bucket,omitempty"`
	Retention   Retention          `json:"retention"`
	Stats       BackupStorageStats `json:"stats"`

	Configured bool `json:"configured"`
}

type BackupStorageStats struct {
	Unsupported  bool   `json:"unsupported,omitempty"`
	UsedBytes    uint64 `json:"usedBytes,omitempty"`
	TotalBytes   uint64 `json:"totalBytes,omitempty"`
	BackupsBytes int64  `json:"backupsBytes,omitempty"`
	BackupsCount int    `json:"backupsCount,omitempty"`

	Entries []BackupEntry `json:"entries,omitempty"`
}

type BackupEntry struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"sizeBytes"`
}

func (c *Client) BackupDestinations() ([]BackupDestinationInfo, error) {
	data, err := c.do(http.MethodGet, "/api/backups/destinations", nil)
	if err != nil {
		return nil, err
	}
	var v []BackupDestinationInfo
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("backups/destinations: parse response: %w", err)
	}
	return v, nil
}

type BackupBrowseEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"isDir,omitempty"`
	Size  int64  `json:"size,omitempty"`
}

func (c *Client) BrowseBackup(dest int, name, subPath string) ([]BackupBrowseEntry, error) {
	q := url.Values{}
	q.Set("dest", strconv.Itoa(dest))
	q.Set("name", name)
	q.Set("path", subPath)
	data, err := c.do(http.MethodGet, "/api/backups/browse?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var v []BackupBrowseEntry
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("backups/browse: parse response: %w", err)
	}
	return v, nil
}

func (c *Client) RestoreBackup(dest int, name string, paths []string) error {
	body := map[string]any{"dest": dest, "name": name, "paths": paths}
	_, err := c.do(http.MethodPost, "/api/backups/restore", body)
	return err
}

func (c *Client) Config() (AppConfig, error) {
	data, err := c.do(http.MethodGet, "/api/config", nil)
	if err != nil {
		return AppConfig{}, err
	}
	var v AppConfig
	if err := json.Unmarshal(data, &v); err != nil {
		return AppConfig{}, fmt.Errorf("config: parse response: %w", err)
	}
	return v, nil
}

func (c *Client) Scripts() ([]string, error) {
	data, err := c.do(http.MethodGet, "/api/scripts", nil)
	if err != nil {
		return nil, err
	}
	var v struct {
		Scripts []string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("scripts: parse response: %w", err)
	}
	return v.Scripts, nil
}

func (c *Client) SetPlayerLoginVipNames(value string) error {
	body := map[string]any{"server": map[string]any{"playerLoginVipNames": value}}
	_, err := c.do(http.MethodPost, "/api/config", body)
	return err
}

type ServerConfigEdit struct {
	SrvName        string
	SrvPassword    string
	SrvDescription string
	SrvMaxPlayers  int

	SrvReservePlayfields      string
	SrvPublic                 string
	SrvStopPeriod             string
	EACActive                 string
	MaxAllowedSizeClass       string
	AllowedBlueprints         string
	HeartbeatServer           string
	HeartbeatClient           string
	LogFlags                  string
	DisableSteamFamilySharing string
	KickPlayerWithPing        string
	TimeoutBootingPfServer    string
	PlayerLoginParallelCount  string
	PlayerLoginVipNames       string
	EnableDLC                 string
	Mode                      string
	GameName                  string
	Seed                      string
}

func (c *Client) SetServerConfig(e ServerConfigEdit) error {

	server := map[string]any{
		"srvName": e.SrvName, "srvDescription": e.SrvDescription, "srvMaxPlayers": e.SrvMaxPlayers,
		"mode": e.Mode, "gameName": e.GameName, "seed": e.Seed,

		"srvPassword": e.SrvPassword,
	}
	optional := map[string]string{
		"srvReservePlayfields":      e.SrvReservePlayfields,
		"srvPublic":                 e.SrvPublic,
		"srvStopPeriod":             e.SrvStopPeriod,
		"eacActive":                 e.EACActive,
		"maxAllowedSizeClass":       e.MaxAllowedSizeClass,
		"allowedBlueprints":         e.AllowedBlueprints,
		"heartbeatServer":           e.HeartbeatServer,
		"heartbeatClient":           e.HeartbeatClient,
		"logFlags":                  e.LogFlags,
		"disableSteamFamilySharing": e.DisableSteamFamilySharing,
		"kickPlayerWithPing":        e.KickPlayerWithPing,
		"timeoutBootingPfServer":    e.TimeoutBootingPfServer,
		"playerLoginParallelCount":  e.PlayerLoginParallelCount,
		"playerLoginVipNames":       e.PlayerLoginVipNames,
		"enableDLC":                 e.EnableDLC,
	}
	for jsonKey, v := range optional {
		if v != "" {
			server[jsonKey] = v
		}
	}
	body := map[string]any{"server": server}
	_, err := c.do(http.MethodPost, "/api/config", body)
	return err
}

func (c *Client) SetChatbotConfig(chatbot ChatbotSettings) error {
	body := map[string]any{"chatbot": map[string]any{
		"wipeCooldownHours":              chatbot.WipeCooldownHours,
		"vehicleTeleportCooldownMinutes": chatbot.VehicleTeleportCooldownMinutes,
		"hardWipe":                       chatbot.HardWipe,
		"commandsEnabled":                chatbot.CommandsEnabled,
		"commandDescriptions":            chatbot.CommandDescriptions,
		"rulesText":                      chatbot.RulesText,
		"customCommands":                 chatbot.CustomCommands,
		"survivalCredits":                chatbot.SurvivalCredits,
		"survivalItems":                  chatbot.SurvivalItems,
		"serverSenderName":               chatbot.ServerSenderName,
		"welcomeMessage":                 chatbot.WelcomeMessage,
		"notifyPlayerPresence":           chatbot.NotifyPlayerPresence,
	}}
	_, err := c.do(http.MethodPost, "/api/config", body)
	return err
}

func (c *Client) SetModeratorSteamIds(ids []string) error {
	body := map[string]any{"chatbot": map[string]any{"moderatorSteamIds": ids}}
	_, err := c.do(http.MethodPost, "/api/config", body)
	return err
}

func (c *Client) SetAdminSteamIds(ids []string) error {
	body := map[string]any{"chatbot": map[string]any{"adminSteamIds": ids}}
	_, err := c.do(http.MethodPost, "/api/config", body)
	return err
}

func (c *Client) SetSchedulerPaused(paused bool) error {
	body := map[string]any{"scheduler": map[string]any{"paused": paused}}
	_, err := c.do(http.MethodPost, "/api/config", body)
	return err
}

func (c *Client) SetPanelBehaviorConfig(watchdog WatchdogSettings, backpack BackpackSettings, structures StructuresSettings, monitoring MonitoringSettings, maintenance MaintenanceSettings, audit AuditSettings) error {
	body := map[string]any{
		"watchdog": map[string]any{"autoRestartOnHang": watchdog.AutoRestartOnHang, "logHangs": watchdog.LogHangs},
		"backpack": map[string]any{
			"minHistoryIntervalSeconds": backpack.MinHistoryIntervalSeconds,
			"forceOverwriteInventory":   backpack.ForceOverwriteInventory,
			"maxHistoryEntries":         backpack.MaxHistoryEntries,
		},

		"structures": map[string]any{
			"backupIntervalMinutes": structures.BackupIntervalMinutes,
			"maxBackupEntries":      structures.MaxBackupEntries,
			"requireOwnerNearby":    structures.RequireOwnerNearby,
		},
		"monitoring": map[string]any{
			"enabledMounts":        monitoring.EnabledMounts,
			"warningPercent":       monitoring.WarningPercent,
			"criticalPercent":      monitoring.CriticalPercent,
			"historyRetentionDays": monitoring.HistoryRetentionDays,
		},

		"maintenance": map[string]any{
			"cleanOldBuildLogs":        maintenance.CleanOldBuildLogs,
			"manualActionWarnMinutes":  maintenance.ManualActionWarnMinutes,
			"checkUpdatesPeriodically": maintenance.CheckUpdatesPeriodically,
		},
		"audit": map[string]any{
			"unauthorizedApiRequests": audit.UnauthorizedAPIRequests,
			"inventoryFuelAnomaly":    audit.InventoryFuelAnomaly,
			"wipeLeftover":            audit.WipeLeftover,
			"positionJump":            audit.PositionJump,
			"permissionFactionChange": audit.PermissionFactionChange,
			"loginTracking":           audit.LoginTracking,
		},
	}
	_, err := c.do(http.MethodPost, "/api/config", body)
	return err
}

func (c *Client) SetScenarioCacheConfig(sc ScenarioCacheSettings) error {
	body := map[string]any{"scenarioCache": map[string]any{
		"mode":               sc.Mode,
		"publicUrlBase":      sc.PublicURLBase,
		"ftpProtocol":        sc.FTPProtocol,
		"ftpHost":            sc.FTPHost,
		"ftpPort":            sc.FTPPort,
		"ftpUser":            sc.FTPUser,
		"ftpPassword":        sc.FTPPassword,
		"ftpKey":             sc.FTPKey,
		"ftpPath":            sc.FTPPath,
		"embeddedListenAddr": sc.EmbeddedListenAddr,
		"panelListenPort":    sc.PanelListenPort,
	}}
	_, err := c.do(http.MethodPost, "/api/config", body)
	return err
}

type ScenarioCacheStatus struct {
	Phase     string    `json:"phase"`
	Message   string    `json:"message,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (s ScenarioCacheStatus) InProgress() bool {
	return s.Phase == "packaging" || s.Phase == "uploading" || s.Phase == "verifying"
}

type ScenarioCacheInfo struct {
	Active    bool      `json:"active"`
	BuiltAt   time.Time `json:"builtAt,omitzero"`
	SizeBytes int64     `json:"sizeBytes,omitempty"`
	URL       string    `json:"url,omitempty"`
}

func (c *Client) ScenarioCacheInfo() (ScenarioCacheInfo, error) {
	data, err := c.do(http.MethodGet, "/api/scenario-cache/info", nil)
	if err != nil {
		return ScenarioCacheInfo{}, err
	}
	var v ScenarioCacheInfo
	if err := json.Unmarshal(data, &v); err != nil {
		return ScenarioCacheInfo{}, fmt.Errorf("scenario cache info: parse response: %w", err)
	}
	return v, nil
}

func (c *Client) ScenarioCacheTestDestination(sc ScenarioCacheSettings) error {
	body := map[string]any{
		"ftpProtocol": sc.FTPProtocol,
		"ftpHost":     sc.FTPHost,
		"ftpPort":     sc.FTPPort,
		"ftpUser":     sc.FTPUser,
		"ftpPassword": sc.FTPPassword,
		"ftpKey":      sc.FTPKey,
		"ftpPath":     sc.FTPPath,
	}
	_, err := c.do(http.MethodPost, "/api/scenario-cache/test", body)
	return err
}

func (c *Client) StartServerUpdate() error {
	_, err := c.do(http.MethodPost, "/api/server/update", nil)
	return err
}

type SteamJob struct {
	Kind      string    `json:"kind"`
	State     string    `json:"state"`
	Message   string    `json:"message,omitempty"`
	Progress  float64   `json:"progress"`
	Output    []string  `json:"output"`
	StartedAt time.Time `json:"startedAt"`
}

func (c *Client) SteamJobStatus() (*SteamJob, error) {
	data, err := c.do(http.MethodGet, "/api/steam/status", nil)
	if err != nil {
		return nil, err
	}
	if string(data) == "null" {
		return nil, nil
	}
	var j SteamJob
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("steamJobStatus: parse response: %w", err)
	}
	return &j, nil
}

func (c *Client) SteamLogin(username, password string) error {
	_, err := c.do(http.MethodPost, "/api/steam/login", map[string]string{"username": username, "password": password})
	return err
}

func (c *Client) SteamSubmitCode(code string) error {
	_, err := c.do(http.MethodPost, "/api/steam/login/code", map[string]string{"code": code})
	return err
}

func (c *Client) SteamLogout() error {
	_, err := c.do(http.MethodPost, "/api/steam/logout", nil)
	return err
}

type WorkshopItemInfo struct {
	PublishedFileID string   `json:"publishedFileId"`
	Title           string   `json:"title"`
	FileSize        int64    `json:"fileSize"`
	TimeUpdated     int64    `json:"timeUpdated"`
	Tags            []string `json:"tags"`
}

func (c *Client) SteamWorkshopLookup(id string) (*WorkshopItemInfo, error) {
	data, err := c.do(http.MethodGet, "/api/steam/workshop/lookup?id="+url.QueryEscape(id), nil)
	if err != nil {
		return nil, err
	}
	var info WorkshopItemInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("steamWorkshopLookup: parse response: %w", err)
	}
	return &info, nil
}

func (c *Client) SteamWorkshopInstall(id string) error {
	_, err := c.do(http.MethodPost, "/api/steam/workshop/install", map[string]string{"id": id})
	return err
}

func (c *Client) SteamWorkshopInstalledItems() ([]InstalledWorkshopItem, error) {
	data, err := c.do(http.MethodGet, "/api/steam/workshop/installed", nil)
	if err != nil {
		return nil, err
	}
	var items []InstalledWorkshopItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("steamWorkshopInstalledItems: parse response: %w", err)
	}
	return items, nil
}

func (c *Client) SteamWorkshopArchiveReader(id string) (body io.ReadCloser, contentDisposition string, err error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/steam/workshop/download?id=%s", c.baseURL, url.QueryEscape(id)), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	streamClient := &http.Client{Transport: c.http.Transport}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("workshop archive %s: %w", id, err)
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("workshop archive %s: %s", id, errorMessage(data, resp.StatusCode))
	}
	return resp.Body, resp.Header.Get("Content-Disposition"), nil
}

type WorkshopUpdateStatus struct {
	PublishedFileID   string `json:"publishedFileId"`
	LocalTimeUpdated  int64  `json:"localTimeUpdated"`
	RemoteTimeUpdated int64  `json:"remoteTimeUpdated"`
	UpdateAvailable   bool   `json:"updateAvailable"`
}

func (c *Client) SteamWorkshopCheck(id string) (*WorkshopUpdateStatus, error) {
	checkClient := &http.Client{Transport: c.http.Transport, Timeout: 90 * time.Second}
	data, err := c.doWithClient(checkClient, http.MethodPost, "/api/steam/workshop/check", map[string]string{"id": id})
	if err != nil {
		return nil, err
	}
	var status WorkshopUpdateStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, fmt.Errorf("steamWorkshopCheck: parse response: %w", err)
	}
	return &status, nil
}

func (c *Client) SteamWorkshopRemove(id string) error {
	_, err := c.do(http.MethodPost, "/api/steam/workshop/remove", map[string]string{"id": id})
	return err
}

type ServerUpdateStatus struct {
	LocalBuildID    string `json:"localBuildId"`
	RemoteBuildID   string `json:"remoteBuildId"`
	UpdateAvailable bool   `json:"updateAvailable"`
}

func (c *Client) CheckServerUpdate() (*ServerUpdateStatus, error) {
	checkClient := &http.Client{Transport: c.http.Transport, Timeout: 90 * time.Second}
	data, err := c.doWithClient(checkClient, http.MethodGet, "/api/server/update-check", nil)
	if err != nil {
		return nil, err
	}
	var status ServerUpdateStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, fmt.Errorf("checkServerUpdate: parse response: %w", err)
	}
	return &status, nil
}

func (c *Client) Scenarios() ([]string, error) {
	data, err := c.do(http.MethodGet, "/api/scenarios", nil)
	if err != nil {
		return nil, err
	}
	var v []string
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("scenarios: parse response: %w", err)
	}
	return v, nil
}

func (c *Client) SelectScenario(name string) error {
	_, err := c.do(http.MethodPost, "/api/scenario", map[string]string{"name": name})
	return err
}

type ModInfo struct {
	Name    string `json:"name"`
	HasInfo bool   `json:"hasInfo"`
}

func (c *Client) Mods() ([]ModInfo, error) {
	data, err := c.do(http.MethodGet, "/api/mods", nil)
	if err != nil {
		return nil, err
	}
	var v []ModInfo
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("mods: parse response: %w", err)
	}
	return v, nil
}

func (c *Client) Saves() ([]string, error) {
	data, err := c.do(http.MethodGet, "/api/saves", nil)
	if err != nil {
		return nil, err
	}
	var v []string
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("saves: parse response: %w", err)
	}
	return v, nil
}

type GameOptionField struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Enum        []string `json:"enum,omitempty"`
	Group       string   `json:"group"`
	Description string   `json:"description"`
	Value       string   `json:"value"`
	Set         bool     `json:"set"`
}

type GameOptions struct {
	ValidFor []string          `json:"validFor"`
	Found    bool              `json:"found"`
	Fields   []GameOptionField `json:"fields"`
}

func (c *Client) GameOptions() (GameOptions, error) {
	data, err := c.do(http.MethodGet, "/api/gameoptions", nil)
	if err != nil {
		return GameOptions{}, err
	}
	var v GameOptions
	if err := json.Unmarshal(data, &v); err != nil {
		return GameOptions{}, fmt.Errorf("gameOptions: parse response: %w", err)
	}
	return v, nil
}

func (c *Client) SetGameOptions(values map[string]string) error {
	_, err := c.do(http.MethodPost, "/api/gameoptions", values)
	return err
}

type ScenarioConfigFile struct {
	Path        string    `json:"path"`
	DisplayPath string    `json:"displayPath"`
	Ext         string    `json:"ext"`
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"modTime"`
}

func (c *Client) ConfigFiles() ([]ScenarioConfigFile, error) {
	data, err := c.do(http.MethodGet, "/api/files/config", nil)
	if err != nil {
		return nil, err
	}
	var v []ScenarioConfigFile
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("configFiles: parse response: %w", err)
	}
	return v, nil
}

func configFilePath(name string) string { return "/api/files/config/" + name }

func (c *Client) ReadConfigFile(name string) (string, error) {
	data, err := c.do(http.MethodGet, configFilePath(name), nil)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (c *Client) WriteConfigFile(name, content string) error {
	req, err := http.NewRequest(http.MethodPost, c.baseURL+configFilePath(name), bytes.NewReader([]byte(content)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("writeConfigFile %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("writeConfigFile %s: %s", name, errorMessage(data, resp.StatusCode))
	}
	return nil
}

type Notification struct {
	ID        int64     `json:"id"`
	Level     string    `json:"level"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
	Read      bool      `json:"read"`
}

func (c *Client) Notifications(limit int) ([]Notification, error) {
	data, err := c.do(http.MethodGet, fmt.Sprintf("/api/notifications?limit=%d", limit), nil)
	if err != nil {
		return nil, err
	}
	var out []Notification
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("notifications: parse response: %w", err)
	}
	return out, nil
}

func (c *Client) NotificationsUnreadCount() (int, error) {
	data, err := c.do(http.MethodGet, "/api/notifications/unread-count", nil)
	if err != nil {
		return 0, err
	}
	var v struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return 0, fmt.Errorf("notifications/unread-count: parse response: %w", err)
	}
	return v.Count, nil
}

func (c *Client) MarkNotificationRead(id int64) error {
	_, err := c.do(http.MethodPost, "/api/notifications/read", map[string]any{"id": id})
	return err
}

func (c *Client) MarkAllNotificationsRead() error {
	_, err := c.do(http.MethodPost, "/api/notifications/read-all", nil)
	return err
}

func (c *Client) ClearAllNotifications() error {
	_, err := c.do(http.MethodPost, "/api/notifications/clear", nil)
	return err
}

func (c *Client) TaskRuns() ([]TaskRun, error) {
	data, err := c.do(http.MethodGet, "/api/scheduler/runs", nil)
	if err != nil {
		return nil, err
	}
	var v []TaskRun
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("taskRuns: parse response: %w", err)
	}
	return v, nil
}

func (c *Client) TaskRunsBetween(from, to string, limit int) ([]TaskRun, error) {
	q := url.Values{}
	if from != "" {
		q.Set("from", from)
	}
	if to != "" {
		q.Set("to", to)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	data, err := c.do(http.MethodGet, "/api/scheduler/runs?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var v []TaskRun
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("taskRunsBetween: parse response: %w", err)
	}
	return v, nil
}

type QueueItem struct {
	Kind       string `json:"kind"`
	ID         int64  `json:"id,omitempty"`
	Label      string `json:"label"`
	Target     string `json:"target"`
	TargetName string `json:"targetName,omitempty"`
	Detail     string `json:"detail,omitempty"`
	QueuedAt   string `json:"queuedAt"`
	WaitingFor string `json:"waitingFor"`
	Cancelable bool   `json:"cancelable,omitempty"`
}

func (c *Client) Queues() ([]QueueItem, error) {
	data, err := c.do(http.MethodGet, "/api/queues", nil)
	if err != nil {
		return nil, err
	}
	var v []QueueItem
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("queues: parse response: %w", err)
	}
	return v, nil
}

func (c *Client) CancelQueueItem(kind string, id int64) error {
	_, err := c.do(http.MethodDelete, "/api/queues/"+kind+"/"+strconv.FormatInt(id, 10), nil)
	return err
}

type AuditEvent struct {
	ID       int64  `json:"id"`
	Ts       string `json:"ts"`
	Category string `json:"category"`
	Player   string `json:"player,omitempty"`
	Summary  string `json:"summary"`
	Details  string `json:"details,omitempty"`
}

func (c *Client) AuditEnabled() (bool, error) {
	data, err := c.do(http.MethodGet, "/api/audit/enabled", nil)
	if err != nil {
		return false, err
	}
	var v struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return false, fmt.Errorf("auditEnabled: parse response: %w", err)
	}
	return v.Enabled, nil
}

func (c *Client) AuditEvents(limit int) ([]AuditEvent, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	data, err := c.do(http.MethodGet, "/api/audit/events?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var v []AuditEvent
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("auditEvents: parse response: %w", err)
	}
	return v, nil
}

func (c *Client) AuditEventsBetween(from, to string, limit int) ([]AuditEvent, error) {
	q := url.Values{}
	if from != "" {
		q.Set("from", from)
	}
	if to != "" {
		q.Set("to", to)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	data, err := c.do(http.MethodGet, "/api/audit/events?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var v []AuditEvent
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("auditEventsBetween: parse response: %w", err)
	}
	return v, nil
}

func (c *Client) RecordLogin(user string, success bool, remoteAddr string) error {
	_, err := c.do(http.MethodPost, "/api/audit/login", map[string]any{
		"user": user, "success": success, "remoteAddr": remoteAddr,
	})
	return err
}
