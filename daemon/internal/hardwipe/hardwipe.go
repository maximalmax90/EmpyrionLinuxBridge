package hardwipe

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/fsowner"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/safego"
	"elb/daemon/internal/telnet"
	"elb/daemon/internal/watchdog"
)

const (
	pollInterval  = 10 * time.Second
	telnetTimeout = 15 * time.Second
)

type request struct {
	SteamID        string `json:"steamId"`
	EntityID       int    `json:"entityId"`
	FactionID      int    `json:"factionId"`
	FactionDeleted *bool  `json:"factionDeleted,omitempty"`
}

func queueDir(cfg config.Config) string {
	return filepath.Join(filepath.Dir(cfg.SettingsFile), "hardwipe-queue")
}

type QueuedRequest struct {
	SteamID   string
	EntityID  int
	FactionID int
	QueuedAt  time.Time
}

func ListQueued(cfg config.Config) ([]QueuedRequest, error) {
	dir := queueDir(cfg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read queue dir %s: %w", dir, err)
	}
	var out []QueuedRequest
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var req request
		if err := json.Unmarshal(data, &req); err != nil {
			continue
		}
		out = append(out, QueuedRequest{SteamID: req.SteamID, EntityID: req.EntityID, FactionID: req.FactionID, QueuedAt: info.ModTime()})
	}
	return out, nil
}

type PendingDestroy struct {
	Playfield string
	IDs       []int
	QueuedAt  time.Time
}

func ListPendingDestroys(cfg config.Config) ([]PendingDestroy, error) {
	dir := pendingDir(cfg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}
	var out []PendingDestroy
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var pf pendingFile
		if err := json.Unmarshal(data, &pf); err != nil {
			continue
		}
		if len(pf.IDs) == 0 {
			continue
		}
		out = append(out, PendingDestroy{Playfield: pf.Playfield, IDs: pf.IDs, QueuedAt: info.ModTime()})
	}
	return out, nil
}

func RunPoller(cfg config.Config, mod *modclient.Client) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for range ticker.C {
		if !watchdog.CachedHealth().Ready() {
			continue
		}
		safego.Tick("hardwipe.RunPoller", func() {
			if err := PollOnce(cfg, mod); err != nil {
				log.Printf("hardwipe: %v", err)
			}
		})
	}
}

func PollOnce(cfg config.Config, mod *modclient.Client) error {
	dir := queueDir(cfg)

	if _, statErr := os.Stat(dir); statErr == nil {
		if fixErr := fsowner.FixTree(cfg, dir); fixErr != nil {
			log.Printf("hardwipe: could not set owner on %s: %v", dir, fixErr)
		}
	}

	pdir := pendingDir(cfg)
	if _, statErr := os.Stat(pdir); statErr == nil {
		if fixErr := fsowner.FixTree(cfg, pdir); fixErr != nil {
			log.Printf("hardwipe: could not set owner on %s: %v", pdir, fixErr)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read queue dir %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if err := processOne(cfg, mod, path); err != nil {
			log.Printf("hardwipe: %s: %v (request stays queued for retry)", path, err)
			continue
		}
		if err := os.Remove(path); err != nil {
			log.Printf("hardwipe: remove processed request %s: %v", path, err)
		}
	}
	return nil
}

var factionStatsRe = regexp.MustCompile(`\[(\S+)\s+(\d+)\]:\s*(\d+)/(\d+)`)

func processOne(cfg config.Config, mod *modclient.Client, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read request: %w", err)
	}
	var req request
	if err := json.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("parse request: %w", err)
	}

	addr, pwd, err := cfg.TelnetCreds()
	if err != nil {
		return fmt.Errorf("telnet creds: %w", err)
	}
	tn := telnet.New(addr, pwd)

	if req.FactionDeleted == nil {
		deleted, err := decideAndDeleteFaction(tn, req.FactionID)
		if err != nil {
			return fmt.Errorf("faction %d decision: %w", req.FactionID, err)
		}
		req.FactionDeleted = &deleted
		if out, err := json.Marshal(req); err != nil {
			log.Printf("hardwipe: could not serialize faction decision for %s: %v", path, err)
		} else if err := os.WriteFile(path, out, 0o644); err != nil {
			log.Printf("hardwipe: could not save faction decision for %s: %v", path, err)
		}
	}

	return destroyStructures(cfg, mod, req)
}

const minPlayerFactionID = 100

func decideAndDeleteFaction(tn *telnet.Client, factionID int) (bool, error) {
	if factionID < minPlayerFactionID {
		return false, nil
	}

	statsOut, err := tn.Command("faction stats", telnetTimeout)
	if err != nil {
		return false, fmt.Errorf("faction stats: %w", err)
	}

	abbrev, total, found := "", 0, false
	for _, m := range factionStatsRe.FindAllStringSubmatch(statsOut, -1) {
		id, _ := strconv.Atoi(m[2])
		if id != factionID {
			continue
		}
		abbrev = m[1]
		total, _ = strconv.Atoi(m[3])
		found = true
		break
	}
	if !found {

		return true, nil
	}
	if total > 1 {
		return false, nil
	}

	delOut, err := tn.Command(fmt.Sprintf("faction delete %s", abbrev), telnetTimeout)
	if err != nil {
		return false, fmt.Errorf("faction delete %s: %w", abbrev, err)
	}

	if !strings.Contains(strings.ToLower(delOut), "deleted") {
		return false, fmt.Errorf("faction delete %s: unexpected engine response: %q", abbrev, strings.TrimSpace(delOut))
	}
	log.Printf("hardwipe: faction %s (id %d) deleted — sole member was wiped", abbrev, factionID)
	return true, nil
}

func destroyStructures(cfg config.Config, mod *modclient.Client, req request) error {

	list, err := mod.AllStructures(0)
	if err != nil {
		return fmt.Errorf("/structures/all: %w", err)
	}

	factionDeleted := req.FactionDeleted != nil && *req.FactionDeleted
	for _, item := range list {
		s, ok := item.(map[string]any)
		if !ok {
			continue
		}
		factionGroup, _ := s["factionGroup"].(float64)
		factionID, _ := s["factionId"].(float64)
		isPersonal := int(factionGroup) == 1 && int(factionID) == req.EntityID
		isFactionOwned := factionDeleted && int(factionGroup) == 0 && int(factionID) == req.FactionID
		if !isPersonal && !isFactionOwned {
			continue
		}
		id, _ := s["id"].(float64)
		playfield, _ := s["playfield"].(string)
		if _, err := mod.Post("/structures/destroy", map[string]any{"id": int(id), "playfield": playfield}); err != nil {
			log.Printf("hardwipe: destroying structure %d (steamId=%s) failed immediately (%v) — deferring until someone enters %s", int(id), req.SteamID, err, playfield)
			if qerr := queuePendingDestroy(cfg, playfield, int(id)); qerr != nil {
				log.Printf("hardwipe: could not defer structure %d: %v", int(id), qerr)
			}
		}
	}
	return nil
}

type pendingFile struct {
	Playfield string `json:"playfield"`
	IDs       []int  `json:"ids"`
}

var unsafeFilenameChars = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

func pendingDir(cfg config.Config) string {
	return filepath.Join(filepath.Dir(cfg.SettingsFile), "hardwipe-pending")
}

func pendingPath(cfg config.Config, playfield string) string {
	safe := unsafeFilenameChars.ReplaceAllString(playfield, "_")
	return filepath.Join(pendingDir(cfg), safe+".json")
}

func queuePendingDestroy(cfg config.Config, playfield string, id int) error {
	dir := pendingDir(cfg)
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	if err := fsowner.FixTree(cfg, dir); err != nil {
		log.Printf("hardwipe: could not set owner on %s: %v", dir, err)
	}
	path := pendingPath(cfg, playfield)

	var pf pendingFile
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &pf)
	}
	pf.Playfield = playfield
	for _, existing := range pf.IDs {
		if existing == id {
			return nil
		}
	}
	pf.IDs = append(pf.IDs, id)

	out, err := json.Marshal(pf)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}
