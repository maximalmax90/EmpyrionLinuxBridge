package api

import (
	"fmt"
	"net/http"
	"os"
	"sort"

	"elb/daemon/internal/config"
	"elb/daemon/internal/logbuf"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/scriptqueue"
	"elb/daemon/internal/settings"
	"elb/daemon/internal/watchdog"
)

func registerConfigRoutes(mux *http.ServeMux, cfg config.Config, mod *modclient.Client, logBuf *logbuf.Buffer) {

	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		s, err := settings.Load(cfg.SettingsFile)
		if err != nil {
			writeErr(w, err)
			return
		}
		server := map[string]any{}
		for _, f := range []struct{ jsonKey, field string }{
			{"srvName", "Srv_Name"},
			{"srvPassword", "Srv_Password"},
			{"srvMaxPlayers", "Srv_MaxPlayers"},
			{"srvDescription", "Srv_Description"},
			{"srvReservePlayfields", "Srv_ReservePlayfields"},
			{"srvPublic", "Srv_Public"},
			{"srvStopPeriod", "Srv_StopPeriod"},
			{"eacActive", "EACActive"},
			{"maxAllowedSizeClass", "MaxAllowedSizeClass"},
			{"allowedBlueprints", "AllowedBlueprints"},
			{"heartbeatServer", "HeartbeatServer"},
			{"heartbeatClient", "HeartbeatClient"},
			{"logFlags", "LogFlags"},
			{"disableSteamFamilySharing", "DisableSteamFamilySharing"},
			{"kickPlayerWithPing", "KickPlayerWithPing"},
			{"timeoutBootingPfServer", "TimeoutBootingPfServer"},
			{"playerLoginParallelCount", "PlayerLoginParallelCount"},
			{"playerLoginVipNames", "PlayerLoginVipNames"},
			{"enableDLC", "EnableDLC"},
		} {
			v, ok, err := cfg.ServerField(f.field)
			if err != nil {
				writeErr(w, err)
				return
			}
			server[f.jsonKey] = map[string]any{"value": v, "set": ok}
		}
		mode, modeOk, err := cfg.GameField("Mode")
		if err != nil {
			writeErr(w, err)
			return
		}
		server["mode"] = map[string]any{"value": mode, "set": modeOk}

		scenario, scenarioOk, err := cfg.GameField("CustomScenario")
		if err != nil {
			writeErr(w, err)
			return
		}
		server["customScenario"] = map[string]any{"value": scenario, "set": scenarioOk}
		gameName, gameNameOk, err := cfg.GameField("GameName")
		if err != nil {
			writeErr(w, err)
			return
		}
		server["gameName"] = map[string]any{"value": gameName, "set": gameNameOk}
		seed, seedOk, err := cfg.GameField("Seed")
		if err != nil {
			writeErr(w, err)
			return
		}
		server["seed"] = map[string]any{"value": seed, "set": seedOk}
		writeJSON(w, map[string]any{"chatbot": s.Chatbot, "watchdog": s.Watchdog, "backpack": s.Backpack, "structures": s.Structures, "backup": s.Backup, "monitoring": s.Monitoring, "maintenance": s.Maintenance, "steam": s.Steam, "scheduler": s.Scheduler, "audit": s.Audit, "scenarioCache": s.ScenarioCache, "server": server})
	})

	mux.HandleFunc("POST /api/config", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Chatbot *struct {
				WipeCooldownHours              *int               `json:"wipeCooldownHours"`
				VehicleTeleportCooldownMinutes *int               `json:"vehicleTeleportCooldownMinutes"`
				HardWipe                       *bool              `json:"hardWipe"`
				CommandsEnabled                *map[string]bool   `json:"commandsEnabled"`
				CommandDescriptions            *map[string]string `json:"commandDescriptions"`

				RulesText      *string                   `json:"rulesText"`
				CustomCommands *[]settings.CustomCommand `json:"customCommands"`
				AdminSteamIds  *[]string                 `json:"adminSteamIds"`

				ModeratorSteamIds    *[]string                `json:"moderatorSteamIds"`
				NotifyPlayerPresence *bool                    `json:"notifyPlayerPresence"`
				SurvivalCredits      *float64                 `json:"survivalCredits"`
				SurvivalItems        *[]settings.SurvivalItem `json:"survivalItems"`
				ServerSenderName     *string                  `json:"serverSenderName"`

				WelcomeMessage *settings.WelcomeMessage `json:"welcomeMessage"`
			} `json:"chatbot"`
			Watchdog *struct {
				AutoRestartOnHang *bool `json:"autoRestartOnHang"`

				LogHangs *bool `json:"logHangs"`
			} `json:"watchdog"`
			Backpack *struct {
				MinHistoryIntervalSeconds *int  `json:"minHistoryIntervalSeconds"`
				ForceOverwriteInventory   *bool `json:"forceOverwriteInventory"`
				MaxHistoryEntries         *int  `json:"maxHistoryEntries"`
			} `json:"backpack"`

			Structures *struct {
				BackupIntervalMinutes *int  `json:"backupIntervalMinutes"`
				MaxBackupEntries      *int  `json:"maxBackupEntries"`
				RequireOwnerNearby    *bool `json:"requireOwnerNearby"`
			} `json:"structures"`

			Backup *struct {
				Destinations *[2]settings.BackupDestination `json:"destinations"`

				Exclude *settings.BackupExclude `json:"exclude"`
			} `json:"backup"`
			Monitoring *struct {
				EnabledMounts   *[]string `json:"enabledMounts"`
				WarningPercent  *int      `json:"warningPercent"`
				CriticalPercent *int      `json:"criticalPercent"`

				HistoryRetentionDays *int `json:"historyRetentionDays"`
			} `json:"monitoring"`
			Maintenance *struct {
				CleanOldBuildLogs        *bool `json:"cleanOldBuildLogs"`
				ManualActionWarnMinutes  *int  `json:"manualActionWarnMinutes"`
				CheckUpdatesPeriodically *bool `json:"checkUpdatesPeriodically"`
			} `json:"maintenance"`

			Scheduler *struct {
				Paused *bool `json:"paused"`
			} `json:"scheduler"`

			Audit *struct {
				UnauthorizedAPIRequests *bool `json:"unauthorizedApiRequests"`
				InventoryFuelAnomaly    *bool `json:"inventoryFuelAnomaly"`
				WipeLeftover            *bool `json:"wipeLeftover"`
				PositionJump            *bool `json:"positionJump"`
				PermissionFactionChange *bool `json:"permissionFactionChange"`
				LoginTracking           *bool `json:"loginTracking"`
			} `json:"audit"`

			ScenarioCache *struct {
				Mode          *string `json:"mode"`
				PublicURLBase *string `json:"publicUrlBase"`
				FTPProtocol   *string `json:"ftpProtocol"`
				FTPHost       *string `json:"ftpHost"`
				FTPPort       *int    `json:"ftpPort"`
				FTPUser       *string `json:"ftpUser"`
				FTPPassword   *string `json:"ftpPassword"`
				FTPKey        *string `json:"ftpKey"`
				FTPPath       *string `json:"ftpPath"`

				EmbeddedListenAddr *string `json:"embeddedListenAddr"`
				PanelListenPort    *int    `json:"panelListenPort"`
			} `json:"scenarioCache"`
			Server *struct {
				SrvName        *string `json:"srvName"`
				SrvPassword    *string `json:"srvPassword"`
				SrvMaxPlayers  *int    `json:"srvMaxPlayers"`
				SrvDescription *string `json:"srvDescription"`

				SrvReservePlayfields      *string `json:"srvReservePlayfields"`
				SrvPublic                 *string `json:"srvPublic"`
				SrvStopPeriod             *string `json:"srvStopPeriod"`
				EACActive                 *string `json:"eacActive"`
				MaxAllowedSizeClass       *string `json:"maxAllowedSizeClass"`
				AllowedBlueprints         *string `json:"allowedBlueprints"`
				HeartbeatServer           *string `json:"heartbeatServer"`
				HeartbeatClient           *string `json:"heartbeatClient"`
				LogFlags                  *string `json:"logFlags"`
				DisableSteamFamilySharing *string `json:"disableSteamFamilySharing"`
				KickPlayerWithPing        *string `json:"kickPlayerWithPing"`
				TimeoutBootingPfServer    *string `json:"timeoutBootingPfServer"`
				PlayerLoginParallelCount  *string `json:"playerLoginParallelCount"`
				PlayerLoginVipNames       *string `json:"playerLoginVipNames"`
				EnableDLC                 *string `json:"enableDLC"`
				Mode                      *string `json:"mode"`
				GameName                  *string `json:"gameName"`
				Seed                      *string `json:"seed"`
			} `json:"server"`
		}
		if !decodeOrErr(w, r, &body) {
			return
		}

		serverChanged := false
		if body.Server != nil {
			edits := []struct {
				field string
				value *string
				num   *int
			}{
				{"Srv_Name", body.Server.SrvName, nil},
				{"Srv_Password", body.Server.SrvPassword, nil},
				{"Srv_Description", body.Server.SrvDescription, nil},
				{"Srv_MaxPlayers", nil, body.Server.SrvMaxPlayers},
				{"Srv_ReservePlayfields", body.Server.SrvReservePlayfields, nil},
				{"Srv_Public", body.Server.SrvPublic, nil},
				{"Srv_StopPeriod", body.Server.SrvStopPeriod, nil},
				{"EACActive", body.Server.EACActive, nil},
				{"MaxAllowedSizeClass", body.Server.MaxAllowedSizeClass, nil},
				{"AllowedBlueprints", body.Server.AllowedBlueprints, nil},
				{"HeartbeatServer", body.Server.HeartbeatServer, nil},
				{"HeartbeatClient", body.Server.HeartbeatClient, nil},
				{"LogFlags", body.Server.LogFlags, nil},
				{"DisableSteamFamilySharing", body.Server.DisableSteamFamilySharing, nil},
				{"KickPlayerWithPing", body.Server.KickPlayerWithPing, nil},
				{"TimeoutBootingPfServer", body.Server.TimeoutBootingPfServer, nil},
				{"PlayerLoginParallelCount", body.Server.PlayerLoginParallelCount, nil},
				{"PlayerLoginVipNames", body.Server.PlayerLoginVipNames, nil},
				{"EnableDLC", body.Server.EnableDLC, nil},
			}
			for _, e := range edits {
				var v string
				switch {
				case e.value != nil:
					v = *e.value
				case e.num != nil:
					v = fmt.Sprintf("%d", *e.num)
				default:
					continue
				}
				if err := cfg.SetServerField(e.field, v); err != nil {
					writeErr(w, err)
					return
				}
				serverChanged = true
			}
			if body.Server.Mode != nil {
				if err := cfg.SetGameField("Mode", *body.Server.Mode); err != nil {
					writeErr(w, err)
					return
				}
				serverChanged = true
			}
			if body.Server.GameName != nil {
				if err := cfg.SetGameField("GameName", *body.Server.GameName); err != nil {
					writeErr(w, err)
					return
				}
				serverChanged = true
			}
			if body.Server.Seed != nil {
				if err := cfg.SetGameField("Seed", *body.Server.Seed); err != nil {
					writeErr(w, err)
					return
				}
				serverChanged = true
			}
		}

		if body.Chatbot != nil || body.Watchdog != nil || body.Backpack != nil || body.Structures != nil || body.Backup != nil || body.Monitoring != nil || body.Maintenance != nil || body.Scheduler != nil || body.Audit != nil || body.ScenarioCache != nil {
			s, err := settings.Load(cfg.SettingsFile)
			if err != nil {
				writeErr(w, err)
				return
			}
			if body.Chatbot != nil {
				if body.Chatbot.WipeCooldownHours != nil {
					s.Chatbot.WipeCooldownHours = *body.Chatbot.WipeCooldownHours
				}
				if body.Chatbot.VehicleTeleportCooldownMinutes != nil {
					s.Chatbot.VehicleTeleportCooldownMinutes = *body.Chatbot.VehicleTeleportCooldownMinutes
				}
				if body.Chatbot.HardWipe != nil {
					s.Chatbot.HardWipe = *body.Chatbot.HardWipe
				}
				if body.Chatbot.CommandsEnabled != nil {
					s.Chatbot.CommandsEnabled = *body.Chatbot.CommandsEnabled
				}
				if body.Chatbot.CommandDescriptions != nil {
					s.Chatbot.CommandDescriptions = *body.Chatbot.CommandDescriptions
				}
				if body.Chatbot.RulesText != nil {
					s.Chatbot.RulesText = *body.Chatbot.RulesText
				}
				if body.Chatbot.CustomCommands != nil {
					if err := validateCustomCommands(*body.Chatbot.CustomCommands); err != nil {
						writeErr(w, err)
						return
					}
					s.Chatbot.CustomCommands = *body.Chatbot.CustomCommands
				}
				if body.Chatbot.AdminSteamIds != nil {
					s.Chatbot.AdminSteamIds = *body.Chatbot.AdminSteamIds
				}
				if body.Chatbot.ModeratorSteamIds != nil {
					s.Chatbot.ModeratorSteamIds = *body.Chatbot.ModeratorSteamIds
				}
				if body.Chatbot.NotifyPlayerPresence != nil {
					s.Chatbot.NotifyPlayerPresence = *body.Chatbot.NotifyPlayerPresence
				}
				if body.Chatbot.SurvivalCredits != nil {
					s.Chatbot.SurvivalCredits = *body.Chatbot.SurvivalCredits
				}
				if body.Chatbot.SurvivalItems != nil {
					if err := validateSurvivalItems(*body.Chatbot.SurvivalItems); err != nil {
						writeErr(w, err)
						return
					}
					s.Chatbot.SurvivalItems = *body.Chatbot.SurvivalItems
				}
				if body.Chatbot.ServerSenderName != nil {
					s.Chatbot.ServerSenderName = *body.Chatbot.ServerSenderName
				}
				if body.Chatbot.WelcomeMessage != nil {
					if err := validateWelcomeMessage(*body.Chatbot.WelcomeMessage); err != nil {
						writeErr(w, err)
						return
					}
					s.Chatbot.WelcomeMessage = *body.Chatbot.WelcomeMessage
				}
			}
			if body.Watchdog != nil && body.Watchdog.AutoRestartOnHang != nil {
				s.Watchdog.AutoRestartOnHang = *body.Watchdog.AutoRestartOnHang
			}
			if body.Watchdog != nil && body.Watchdog.LogHangs != nil {
				s.Watchdog.LogHangs = body.Watchdog.LogHangs
			}
			if body.Backpack != nil && body.Backpack.MinHistoryIntervalSeconds != nil {
				s.Backpack.MinHistoryIntervalSeconds = *body.Backpack.MinHistoryIntervalSeconds
			}
			if body.Backpack != nil && body.Backpack.ForceOverwriteInventory != nil {
				s.Backpack.ForceOverwriteInventory = *body.Backpack.ForceOverwriteInventory
			}
			if body.Backpack != nil && body.Backpack.MaxHistoryEntries != nil {
				s.Backpack.MaxHistoryEntries = *body.Backpack.MaxHistoryEntries
			}
			if body.Structures != nil && body.Structures.BackupIntervalMinutes != nil {
				s.Structures.BackupIntervalMinutes = *body.Structures.BackupIntervalMinutes
			}
			if body.Structures != nil && body.Structures.MaxBackupEntries != nil {
				s.Structures.MaxBackupEntries = *body.Structures.MaxBackupEntries
			}
			if body.Structures != nil && body.Structures.RequireOwnerNearby != nil {
				s.Structures.RequireOwnerNearby = *body.Structures.RequireOwnerNearby
			}
			if body.Backup != nil && body.Backup.Destinations != nil {
				dst := *body.Backup.Destinations

				if dst[0].Retention.Off() {
					writeErr(w, fmt.Errorf("для основного хранилища должен быть указан хотя бы один параметр хранения"))
					return
				}
				dst[0].Type = "snapshot"

				switch dst[1].Type {
				case "ftp":
					if dst[1].FTPHost == "" || dst[1].FTPUser == "" {
						writeErr(w, fmt.Errorf("для FTP/SFTP-хранилища обязательны адрес и логин"))
						return
					}
					if dst[1].FTPProtocol == "sftp" && dst[1].FTPPassword == "" && dst[1].FTPKey == "" {
						writeErr(w, fmt.Errorf("для SFTP укажите пароль или приватный ключ"))
						return
					}
				case "s3":
					if dst[1].S3Endpoint == "" || dst[1].S3Bucket == "" || dst[1].S3AccessKey == "" || dst[1].S3SecretKey == "" {
						writeErr(w, fmt.Errorf("для S3-хранилища обязательны эндпоинт, бакет, ID и ключ"))
						return
					}
				}
				s.Backup.Destinations = dst
			}
			if body.Backup != nil && body.Backup.Exclude != nil {
				s.Backup.Exclude = *body.Backup.Exclude
			}
			if body.Maintenance != nil && body.Maintenance.CleanOldBuildLogs != nil {
				s.Maintenance.CleanOldBuildLogs = *body.Maintenance.CleanOldBuildLogs
			}
			if body.Maintenance != nil && body.Maintenance.ManualActionWarnMinutes != nil {
				s.Maintenance.ManualActionWarnMinutes = *body.Maintenance.ManualActionWarnMinutes
			}
			if body.Maintenance != nil && body.Maintenance.CheckUpdatesPeriodically != nil {
				s.Maintenance.CheckUpdatesPeriodically = *body.Maintenance.CheckUpdatesPeriodically
			}
			if body.Monitoring != nil && body.Monitoring.EnabledMounts != nil {
				s.Monitoring.EnabledMounts = *body.Monitoring.EnabledMounts
			}
			if body.Monitoring != nil && body.Monitoring.WarningPercent != nil {
				s.Monitoring.WarningPercent = *body.Monitoring.WarningPercent
			}
			if body.Monitoring != nil && body.Monitoring.HistoryRetentionDays != nil {
				s.Monitoring.HistoryRetentionDays = *body.Monitoring.HistoryRetentionDays
			}
			if body.Monitoring != nil && body.Monitoring.CriticalPercent != nil {
				s.Monitoring.CriticalPercent = *body.Monitoring.CriticalPercent
			}
			if body.Scheduler != nil && body.Scheduler.Paused != nil {
				s.Scheduler.Paused = *body.Scheduler.Paused
			}
			if body.Audit != nil {
				if body.Audit.UnauthorizedAPIRequests != nil {
					s.Audit.UnauthorizedAPIRequests = *body.Audit.UnauthorizedAPIRequests
				}
				if body.Audit.InventoryFuelAnomaly != nil {
					s.Audit.InventoryFuelAnomaly = *body.Audit.InventoryFuelAnomaly
				}
				if body.Audit.WipeLeftover != nil {
					s.Audit.WipeLeftover = *body.Audit.WipeLeftover
				}
				if body.Audit.PositionJump != nil {
					s.Audit.PositionJump = *body.Audit.PositionJump
				}
				if body.Audit.PermissionFactionChange != nil {
					s.Audit.PermissionFactionChange = *body.Audit.PermissionFactionChange
				}
				if body.Audit.LoginTracking != nil {
					s.Audit.LoginTracking = *body.Audit.LoginTracking
				}
			}
			if body.ScenarioCache != nil {
				if body.ScenarioCache.Mode != nil {
					s.ScenarioCache.Mode = *body.ScenarioCache.Mode
				}
				if body.ScenarioCache.PublicURLBase != nil {
					s.ScenarioCache.PublicURLBase = *body.ScenarioCache.PublicURLBase
				}
				if body.ScenarioCache.FTPProtocol != nil {
					s.ScenarioCache.FTPProtocol = *body.ScenarioCache.FTPProtocol
				}
				if body.ScenarioCache.FTPHost != nil {
					s.ScenarioCache.FTPHost = *body.ScenarioCache.FTPHost
				}
				if body.ScenarioCache.FTPPort != nil {
					s.ScenarioCache.FTPPort = *body.ScenarioCache.FTPPort
				}
				if body.ScenarioCache.FTPUser != nil {
					s.ScenarioCache.FTPUser = *body.ScenarioCache.FTPUser
				}
				if body.ScenarioCache.FTPPassword != nil {
					s.ScenarioCache.FTPPassword = *body.ScenarioCache.FTPPassword
				}
				if body.ScenarioCache.FTPKey != nil {
					s.ScenarioCache.FTPKey = *body.ScenarioCache.FTPKey
				}
				if body.ScenarioCache.FTPPath != nil {
					s.ScenarioCache.FTPPath = *body.ScenarioCache.FTPPath
				}
				if body.ScenarioCache.EmbeddedListenAddr != nil {
					s.ScenarioCache.EmbeddedListenAddr = *body.ScenarioCache.EmbeddedListenAddr
				}
				if body.ScenarioCache.PanelListenPort != nil {
					s.ScenarioCache.PanelListenPort = *body.ScenarioCache.PanelListenPort
				}

				if s.ScenarioCache.Mode == "own" {
					if s.ScenarioCache.FTPHost == "" || s.ScenarioCache.FTPUser == "" {
						writeErr(w, fmt.Errorf("для FTP/SFTP-раздачи кэша сценария обязательны адрес и логин"))
						return
					}
					if s.ScenarioCache.FTPProtocol == "sftp" && s.ScenarioCache.FTPPassword == "" && s.ScenarioCache.FTPKey == "" {
						writeErr(w, fmt.Errorf("для SFTP укажите пароль или приватный ключ"))
						return
					}
				}
				if s.ScenarioCache.Mode != "" && s.ScenarioCache.Mode != "off" && s.ScenarioCache.PublicURLBase == "" {
					writeErr(w, fmt.Errorf("укажите публичную ссылку (PublicURLBase) для раздачи кэша сценария"))
					return
				}
			}
			if err := settings.Save(cfg.SettingsFile, s); err != nil {
				writeErr(w, err)
				return
			}

			watchdog.SyncAuditRule(s.Watchdog.LogHangsEnabled())

		}

		resp := map[string]any{"ok": true}
		if serverChanged {
			resp["note"] = "изменения dedicated.yaml применятся только после перезапуска сервера"
		}
		writeJSON(w, resp)
	})

	mux.HandleFunc("GET /api/scripts", func(w http.ResponseWriter, r *http.Request) {
		entries, err := os.ReadDir(scriptqueue.ScriptsDir)
		if err != nil {
			if os.IsNotExist(err) {
				writeJSON(w, map[string]any{"scripts": []string{}})
				return
			}
			writeErr(w, err)
			return
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if !e.IsDir() {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		writeJSON(w, map[string]any{"scripts": names})
	})
}
