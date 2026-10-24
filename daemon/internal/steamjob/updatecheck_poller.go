package steamjob

import (
	"fmt"
	"log"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/icons"
	"elb/daemon/internal/notify"
	"elb/daemon/internal/safego"
	"elb/daemon/internal/settings"
)

const updateCheckPollInterval = 6 * time.Hour

func RunUpdateCheckPoller(cfg config.Config) {
	ticker := time.NewTicker(updateCheckPollInterval)
	defer ticker.Stop()
	for range ticker.C {
		safego.Tick("steamjob.RunUpdateCheckPoller", func() {
			checkUpdatesOnce(cfg)
		})
	}
}

func checkUpdatesOnce(cfg config.Config) {
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		log.Printf("steamjob: update-check: settings: %v", err)
		return
	}
	if !s.Maintenance.CheckUpdatesPeriodically {
		return
	}

	checkServerUpdateOnce(cfg)
	checkActiveScenarioUpdateOnce(cfg)
}

func checkServerUpdateOnce(cfg config.Config) {
	status, err := CheckServerUpdate(cfg)
	if err != nil {
		log.Printf("steamjob: update-check: server: %v", err)
		return
	}
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		log.Printf("steamjob: update-check: settings (server): %v", err)
		return
	}
	if !status.UpdateAvailable || status.RemoteBuildID == s.Maintenance.LastNotifiedServerBuildID {
		return
	}
	text := fmt.Sprintf("Доступно обновление игрового сервера: %s → %s", status.LocalBuildID, status.RemoteBuildID)
	if err := notify.Add(cfg, notify.LevelInfo, text); err != nil {
		log.Printf("steamjob: update-check: notification (server): %v", err)
		return
	}
	s.Maintenance.LastNotifiedServerBuildID = status.RemoteBuildID
	if err := settings.Save(cfg.SettingsFile, s); err != nil {
		log.Printf("steamjob: update-check: saving settings (server): %v", err)
	}
}

func checkActiveScenarioUpdateOnce(cfg config.Config) {
	scenario, err := icons.CurrentScenario(cfg)
	if err != nil {
		return
	}
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		log.Printf("steamjob: update-check: settings (scenario): %v", err)
		return
	}
	var publishedFileID string
	for _, it := range s.Steam.InstalledItems {
		if it.Kind == "scenario" && it.TargetDir == scenario {
			publishedFileID = it.PublishedFileID
			break
		}
	}
	if publishedFileID == "" {
		return
	}
	status, err := CheckInstalledItemUpdate(cfg, publishedFileID)
	if err != nil {
		log.Printf("steamjob: update-check: scenario %s: %v", scenario, err)
		return
	}
	if !status.UpdateAvailable || status.RemoteTimeUpdated == s.Maintenance.LastNotifiedScenarioTimeUpdated {
		return
	}
	text := fmt.Sprintf("Доступно обновление активного сценария (%s) в Мастерской", scenario)
	if err := notify.Add(cfg, notify.LevelInfo, text); err != nil {
		log.Printf("steamjob: update-check: notification (scenario): %v", err)
		return
	}

	fresh, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		log.Printf("steamjob: update-check: re-reading settings (scenario): %v", err)
		return
	}
	fresh.Maintenance.LastNotifiedScenarioTimeUpdated = status.RemoteTimeUpdated
	if err := settings.Save(cfg.SettingsFile, fresh); err != nil {
		log.Printf("steamjob: update-check: saving settings (scenario): %v", err)
	}
}
