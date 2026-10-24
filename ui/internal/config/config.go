package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

type Config struct {
	ListenAddr string `json:"listen_addr"`

	DaemonURL string `json:"daemon_url"`

	DaemonToken string `json:"daemon_token"`

	DaemonCertFingerprint string `json:"daemon_cert_fingerprint"`

	DBFile string `json:"db_file"`

	PollIntervalSeconds int `json:"poll_interval_seconds"`

	LocalesDir string `json:"locales_dir"`

	TrustProxyHeaders bool `json:"trust_proxy_headers,omitempty"`
}

func defaults() Config {
	return Config{
		ListenAddr:          ":8081",
		DBFile:              "/var/lib/elb-ui/ui.db",
		PollIntervalSeconds: 3,
	}
}

func Load(path string) (Config, error) {
	if path == "" {
		path = envOr("ELB_UI_CONFIG", "/etc/elb/ui.json")
	}

	cfg := defaults()

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return Config{}, fmt.Errorf("read %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return Config{}, fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
		}
		out, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return Config{}, err
		}
		if err := os.WriteFile(path, out, 0o600); err != nil {
			return Config{}, fmt.Errorf("write %s: %w", path, err)
		}
		log.Printf("config: created %s with default settings — fill in daemon_url/daemon_token and restart", path)
	} else if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}

	if cfg.ListenAddr == "" {
		cfg.ListenAddr = defaults().ListenAddr
	}
	if cfg.DBFile == "" {
		cfg.DBFile = defaults().DBFile
	}
	if cfg.PollIntervalSeconds <= 0 {
		cfg.PollIntervalSeconds = defaults().PollIntervalSeconds
	}

	if cfg.DaemonURL == "" || cfg.DaemonToken == "" {
		log.Printf("config: daemon_url/daemon_token not set in %s — screens that depend on the daemon will be unavailable", path)
	}

	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
