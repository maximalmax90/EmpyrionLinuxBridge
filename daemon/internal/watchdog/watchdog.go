package watchdog

import (
	"fmt"
	"log"
	"sync"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/notify"
	"elb/daemon/internal/safego"
	"elb/daemon/internal/serverctl"
	"elb/daemon/internal/settings"
)

const StartupGrace = 90 * time.Second

const HangThreshold = 3 * time.Minute

const pollInterval = 20 * time.Second

type Health struct {
	serverctl.Status
	Hung     bool      `json:"hung"`
	BadSince time.Time `json:"badSince,omitempty"`
}

var (
	mu           sync.Mutex
	badSince     time.Time // zero — сейчас всё в порядке
	cachedHealth Health    // последний результат Check() — см. CachedHealth ниже
	lastVersion  string    // последняя УСПЕШНО определённая версия игры — см. Check()
)

func Check(cfg config.Config) Health {
	st := serverctl.GetStatus(cfg)

	mu.Lock()
	defer mu.Unlock()

	if st.Version != "" {
		lastVersion = st.Version
	} else if lastVersion != "" {
		st.Version = lastVersion
	}

	bad := st.Active && !st.TelnetOK
	if !bad {
		badSince = time.Time{}
		cachedHealth = Health{Status: st}
		return cachedHealth
	}
	if badSince.IsZero() {
		badSince = time.Now()

		if s, err := settings.Load(cfg.SettingsFile); err != nil {
			log.Printf("watchdog: could not read settings for LogHangs (defaulting to enabled): %v", err)
			go writeDiagnostics(cfg, badSince)
		} else if s.Watchdog.LogHangsEnabled() {
			go writeDiagnostics(cfg, badSince)
		}
	}

	hung := serverctl.TimeSinceLastAction() > StartupGrace && time.Since(badSince) > HangThreshold
	cachedHealth = Health{Status: st, Hung: hung, BadSince: badSince}
	return cachedHealth
}

func CachedHealth() Health {
	mu.Lock()
	defer mu.Unlock()
	return cachedHealth
}

func (h Health) Ready() bool {
	return h.Active && h.TelnetOK && serverctl.TimeSinceLastAction() > pollInterval
}

func RunWatchdog(cfg config.Config, modPing func() error) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	wasHung := false
	for {
		safego.Tick("watchdog.RunWatchdog", func() {
			h := Check(cfg)
			if h.Hung && !wasHung {
				log.Printf("watchdog: %s looks hung — Active=%v TelnetOK=%v, unresponsive since %s",
					cfg.SystemdUnit, h.Active, h.TelnetOK, h.BadSince.Format(time.RFC3339))

				if modPing != nil && modPing() == nil {
					log.Printf("watchdog: %s telnet unresponsive, but mod API (another HTTP port of the same process) is alive — looks like an isolated telnet console failure, not an engine hang; skipping alert and auto-restart to avoid kicking players needlessly", cfg.SystemdUnit)
					if err := notify.Add(cfg, notify.LevelWarning, fmt.Sprintf(
						"Консоль сервера %s не отвечает (с %s), но сама игра работает нормально — проверьте вручную, если не пройдёт само", cfg.SystemdUnit, h.BadSince.Format("2006-01-02 15:04:05"))); err != nil {
						log.Printf("watchdog: notify: %v", err)
					}
				} else {

					if err := notify.Add(cfg, notify.LevelCritical, fmt.Sprintf(
						"Сервер %s похож на зависший (не отвечает с %s)", cfg.SystemdUnit, h.BadSince.Format("2006-01-02 15:04:05"))); err != nil {
						log.Printf("watchdog: notify: %v", err)
					}

					s, err := settings.Load(cfg.SettingsFile)
					if err != nil {
						log.Printf("watchdog: could not read settings for AutoRestartOnHang: %v", err)
					} else if s.Watchdog.AutoRestartOnHang {
						log.Printf("watchdog: AutoRestartOnHang is enabled — restarting %s", cfg.SystemdUnit)
						if err := serverctl.Restart(cfg.SystemdUnit); err != nil {
							log.Printf("watchdog: restart failed: %v", err)
							if err := notify.Add(cfg, notify.LevelCritical, fmt.Sprintf(
								"Автоматический перезапуск зависшего сервера %s НЕ удался: %v", cfg.SystemdUnit, err)); err != nil {
								log.Printf("watchdog: notify: %v", err)
							}
						} else if err := notify.Add(cfg, notify.LevelWarning, fmt.Sprintf(
							"Сервер %s завис и был автоматически перезапущен", cfg.SystemdUnit)); err != nil {
							log.Printf("watchdog: notify: %v", err)
						}
					}
				}
			}
			wasHung = h.Hung
		})
		<-ticker.C
	}
}
