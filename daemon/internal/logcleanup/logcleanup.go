package logcleanup

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/safego"
	"elb/daemon/internal/settings"
	"elb/daemon/internal/watchdog"
)

const pollInterval = 24 * time.Hour

func RunPoller(cfg config.Config) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for range ticker.C {
		safego.Tick("logcleanup.RunPoller", func() {
			s, err := settings.Load(cfg.SettingsFile)
			if err != nil {
				log.Printf("logcleanup: settings: %v", err)
				return
			}
			if s.Maintenance.CleanOldBuildLogs {
				if err := CleanOnce(cfg); err != nil {
					log.Printf("logcleanup: %v", err)
				}
			}

			if s.Watchdog.LogHangsEnabled() {
				if err := watchdog.CleanHangDiagnosticsOnce(cfg); err != nil {
					log.Printf("logcleanup: hang-diagnostics: %v", err)
				}
			}
		})
	}
}

func CleanOnce(cfg config.Config) error {
	current, err := currentBuild(cfg)
	if err != nil || current == "" {
		return err
	}
	logsDir := filepath.Join(cfg.DataRoot, "Logs")
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == current {
			continue
		}
		p := filepath.Join(logsDir, e.Name())
		if err := os.RemoveAll(p); err != nil {
			log.Printf("logcleanup: remove %s: %v", p, err)
			continue
		}
		log.Printf("logcleanup: removed logs for old build %s", e.Name())
	}
	return nil
}

func currentBuild(cfg config.Config) (string, error) {
	data, err := os.ReadFile(filepath.Join(cfg.DataRoot, "BuildNumber.txt"))
	if err != nil {
		return "", err
	}
	line := strings.SplitN(string(data), "\n", 2)[0]
	return strings.TrimSpace(line), nil
}
