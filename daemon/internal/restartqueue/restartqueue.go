package restartqueue

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/safego"
	"elb/daemon/internal/serverctl"
	"elb/daemon/internal/settings"
)

const pollInterval = 5 * time.Second

type request struct {
	SteamID        string `json:"steamId"`
	PlayerName     string `json:"playerName"`
	RequestedAtUtc string `json:"requestedAtUtc"`
	Action         string `json:"action"`
}

func queueDir(cfg config.Config) string {
	return filepath.Join(filepath.Dir(cfg.SettingsFile), "restart-queue")
}

type QueuedRequest struct {
	SteamID    string
	PlayerName string
	Action     string
	QueuedAt   time.Time
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
		action := req.Action
		if action == "" {
			action = "restart"
		}
		out = append(out, QueuedRequest{SteamID: req.SteamID, PlayerName: req.PlayerName, Action: action, QueuedAt: info.ModTime()})
	}
	return out, nil
}

func RunPoller(cfg config.Config) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for range ticker.C {
		safego.Tick("restartqueue.RunPoller", func() {
			if err := PollOnce(cfg); err != nil {
				log.Printf("restartqueue: %v", err)
			}
		})
	}
}

func PollOnce(cfg config.Config) error {
	dir := queueDir(cfg)
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
		processOne(cfg, path)
		if err := os.Remove(path); err != nil {
			log.Printf("restartqueue: remove processed request %s: %v", path, err)
		}
	}
	return nil
}

func processOne(cfg config.Config, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("restartqueue: read request %s: %v", path, err)
		return
	}
	var req request
	if err := json.Unmarshal(data, &req); err != nil {
		log.Printf("restartqueue: parse request %s: %v", path, err)
		return
	}

	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		log.Printf("restartqueue: read settings.json: %v", err)
		return
	}
	if !isAdmin(s.Chatbot.AdminSteamIds, req.SteamID) {
		log.Printf("restartqueue: request from %s (%s) rejected — not in the admin list", req.PlayerName, req.SteamID)
		return
	}

	switch req.Action {
	case "stop":
		log.Printf("restartqueue: stop requested by %s (%s)", req.PlayerName, req.SteamID)
		if err := serverctl.Stop(cfg.SystemdUnit); err != nil {
			log.Printf("restartqueue: stop failed: %v", err)
		}
	default:
		log.Printf("restartqueue: restart requested by %s (%s)", req.PlayerName, req.SteamID)
		if err := serverctl.Restart(cfg.SystemdUnit); err != nil {
			log.Printf("restartqueue: restart failed: %v", err)
		}
	}
}

func isAdmin(ids []string, steamID string) bool {
	for _, id := range ids {
		if id == steamID {
			return true
		}
	}
	return false
}
