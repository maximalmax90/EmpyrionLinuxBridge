package scriptqueue

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/safego"
	"elb/daemon/internal/watchdog"
)

const ScriptsDir = "/opt/elb/scripts"

const pollInterval = 2 * time.Second

const scriptTimeout = 15 * time.Second

const fallbackText = "Команда временно недоступна."

type request struct {
	SteamID        string `json:"steamId"`
	PlayerName     string `json:"playerName"`
	ScriptName     string `json:"scriptName"`
	Arg            string `json:"arg"`
	ActionType     string `json:"actionType"`
	BannerPriority int    `json:"bannerPriority"`
}

func queueDir(cfg config.Config) string {
	return filepath.Join(filepath.Dir(cfg.SettingsFile), "script-queue")
}

type QueuedRequest struct {
	SteamID    string
	PlayerName string
	ScriptName string
	Arg        string
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
		out = append(out, QueuedRequest{SteamID: req.SteamID, PlayerName: req.PlayerName, ScriptName: req.ScriptName, Arg: req.Arg, QueuedAt: info.ModTime()})
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
		safego.Tick("scriptqueue.RunPoller", func() {
			if err := PollOnce(cfg, mod); err != nil {
				log.Printf("scriptqueue: %v", err)
			}
		})
	}
}

func PollOnce(cfg config.Config, mod *modclient.Client) error {
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
		processOne(mod, path)
		if err := os.Remove(path); err != nil {
			log.Printf("scriptqueue: remove processed request %s: %v", path, err)
		}
	}
	return nil
}

func processOne(mod *modclient.Client, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("scriptqueue: read request %s: %v", path, err)
		return
	}
	var req request
	if err := json.Unmarshal(data, &req); err != nil {
		log.Printf("scriptqueue: parse request %s: %v", path, err)
		return
	}

	text, ok := runScript(req)
	if !ok {
		text = fallbackText
	}
	deliver(mod, req, text)
}

func runScript(req request) (text string, ok bool) {
	var args []string
	if req.Arg != "" {
		args = []string{req.Arg}
	}
	out, err := Run(req.ScriptName, scriptTimeout, args...)
	if err != nil {
		log.Printf("scriptqueue: %v (request from %s)", err, req.SteamID)
		return "", false
	}
	return out, true
}

func Run(scriptName string, timeout time.Duration, args ...string) (string, error) {
	name := filepath.Base(scriptName)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "", fmt.Errorf("empty/invalid script name")
	}
	scriptPath := filepath.Join(ScriptsDir, name)
	if _, err := os.Stat(scriptPath); err != nil {
		return "", fmt.Errorf("script %s not found: %w", name, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmdArgs := append([]string{scriptPath}, args...)
	cmd := exec.CommandContext(ctx, "/bin/bash", cmdArgs...)
	cmd.Dir = ScriptsDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	out := bytes.TrimSpace(stdout.Bytes())
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("script %s exceeded timeout %s", name, timeout)
		}
		return "", fmt.Errorf("script %s failed: %w, stderr: %s", name, err, bytes.TrimSpace(stderr.Bytes()))
	}
	if len(out) == 0 {
		return "", fmt.Errorf("script %s returned empty output", name)
	}
	return string(out), nil
}

func deliver(mod *modclient.Client, req request, text string) {
	body := map[string]any{
		"steamId":        req.SteamID,
		"actionType":     req.ActionType,
		"text":           text,
		"bannerPriority": req.BannerPriority,
	}
	if _, err := mod.Post("/chatbot/deliver", body); err != nil {
		log.Printf("scriptqueue: delivering result to player %s: %v", req.SteamID, err)
	}
}
