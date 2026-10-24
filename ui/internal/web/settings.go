package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"

	"elb/ui/internal/daemonclient"
	"elb/ui/internal/store"
)

type gameOptionGroup struct {
	Name   string
	Fields []daemonclient.GameOptionField
}

func groupGameOptions(fields []daemonclient.GameOptionField) []gameOptionGroup {
	var groups []gameOptionGroup
	index := map[string]int{}
	for _, f := range fields {
		i, ok := index[f.Group]
		if !ok {
			i = len(groups)
			index[f.Group] = i
			groups = append(groups, gameOptionGroup{Name: f.Group})
		}
		groups[i].Fields = append(groups[i].Fields, f)
	}
	return groups
}

func (s *Server) handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)

	cfg, err := s.daemon.Config()
	if err != nil {
		log.Printf("settings: config: %v", err)
	}
	adminPlayers, err := s.store.AdminPlayers()
	if err != nil {
		log.Printf("settings: admin players: %v", err)
	}
	moderatorPlayers, err := s.store.ModeratorPlayers()
	if err != nil {
		log.Printf("settings: moderator players: %v", err)
	}

	allPlayers, err := s.daemon.Players()
	if err != nil {
		log.Printf("settings: players: %v", err)
	}

	gameOptions, err := s.daemon.GameOptions()
	if err != nil {
		log.Printf("settings: gameoptions: %v", err)
	}

	itemsByName, err := s.store.ItemMapping()
	if err != nil {
		log.Printf("settings: item mapping: %v", err)
	}
	itemsJSON, _ := json.Marshal(itemsByName)
	survivalItemsJSON, _ := json.Marshal(cfg.Chatbot.SurvivalItems)

	displayNames, err := s.daemon.ItemDisplayNames(u.Locale)
	if err != nil {
		log.Printf("settings: display names: %v", err)
	}
	descriptions, err := s.daemon.ItemDescriptions(u.Locale)
	if err != nil {
		log.Printf("settings: descriptions: %v", err)
	}
	displayNamesJSON, _ := json.Marshal(displayNames)
	descriptionsJSON, _ := json.Marshal(descriptions)

	mounts, err := s.daemon.Mounts()
	if err != nil {
		log.Printf("settings: mounts: %v", err)
	}

	mods, err := s.daemon.Mods()
	if err != nil {
		log.Printf("settings: mods: %v", err)
	}
	scenarios, err := s.daemon.Scenarios()
	if err != nil {
		log.Printf("settings: scenarios: %v", err)
	}
	saves, err := s.daemon.Saves()
	if err != nil {
		log.Printf("settings: saves: %v", err)
	}

	scripts, err := s.daemon.Scripts()
	if err != nil {
		log.Printf("settings: scripts: %v", err)
	}
	scriptsJSON, _ := json.Marshal(scripts)

	defaultTab := "server"
	if !u.IsAdmin() {
		perms := s.store.ModeratorPermissions()
		switch {
		case perms.ChatbotBasic:
			defaultTab = "chatbot"
		case perms.PanelBasic:
			defaultTab = "panel"
		}
	}

	data := map[string]any{
		"Username":            u.Username,
		"Config":              cfg,
		"DefaultTab":          defaultTab,
		"AdminIdentities":     adminPlayers,
		"ModeratorIdentities": moderatorPlayers,
		"AllPlayers":          allPlayers,
		"GameOptions":         gameOptions,
		"GameOptionGroups":    groupGameOptions(gameOptions.Fields),
		"ItemIDsJSON":         template.JS(itemsJSON),
		"SurvivalItemsJSON":   template.JS(survivalItemsJSON),
		"DisplayNamesJSON":    template.JS(displayNamesJSON),
		"DescriptionsJSON":    template.JS(descriptionsJSON),
		"Mounts":              mounts,
		"Mods":                mods,
		"Scenarios":           scenarios,
		"Saves":               saves,
		"Scripts":             scripts,
		"ScriptsJSON":         template.JS(scriptsJSON),

		"PlayerColumns": s.store.PlayerColumns(),

		"UIListenPort": s.listenPort,
	}
	for k, v := range s.statusBarData(u) {
		data[k] = v
	}
	if err := s.execTemplate(w, r, "settings.html", data); err != nil {
		log.Printf("settings: render: %v", err)
	}
}

const restartRequiredNote = "Сохранено — вступит в силу после перезапуска сервера"

func (s *Server) handleActionSettingsServer(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	maxPlayers, _ := strconv.Atoi(r.FormValue("srvMaxPlayers"))
	edit := daemonclient.ServerConfigEdit{
		SrvName:        r.FormValue("srvName"),
		SrvPassword:    r.FormValue("srvPassword"),
		SrvDescription: r.FormValue("srvDescription"),
		SrvMaxPlayers:  maxPlayers,

		SrvReservePlayfields:      r.FormValue("srvReservePlayfields"),
		SrvPublic:                 r.FormValue("srvPublic"),
		SrvStopPeriod:             r.FormValue("srvStopPeriod"),
		EACActive:                 r.FormValue("eacActive"),
		MaxAllowedSizeClass:       r.FormValue("maxAllowedSizeClass"),
		AllowedBlueprints:         r.FormValue("allowedBlueprints"),
		HeartbeatServer:           r.FormValue("heartbeatServer"),
		HeartbeatClient:           r.FormValue("heartbeatClient"),
		LogFlags:                  r.FormValue("logFlags"),
		DisableSteamFamilySharing: r.FormValue("disableSteamFamilySharing"),
		KickPlayerWithPing:        r.FormValue("kickPlayerWithPing"),
		TimeoutBootingPfServer:    r.FormValue("timeoutBootingPfServer"),
		PlayerLoginParallelCount:  r.FormValue("playerLoginParallelCount"),
		PlayerLoginVipNames:       r.FormValue("playerLoginVipNames"),
		EnableDLC:                 r.FormValue("enableDLC"),
		Mode:                      r.FormValue("mode"),
		GameName:                  r.FormValue("gameName"),
		Seed:                      r.FormValue("seed"),
	}
	if err := s.daemon.SetServerConfig(edit); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось сохранить: %v", err))
		return
	}
	writeToast(w, restartRequiredNote)
}

func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func (s *Server) handleActionSettingsGameOptions(w http.ResponseWriter, r *http.Request) {
	var values map[string]string
	if err := json.NewDecoder(r.Body).Decode(&values); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	if err := s.daemon.SetGameOptions(values); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось сохранить: %v", err))
		return
	}
	writeToast(w, restartRequiredNote)
}

func (s *Server) handleActionSettingsChatbot(w http.ResponseWriter, r *http.Request) {
	var chatbot daemonclient.ChatbotSettings
	if err := json.NewDecoder(r.Body).Decode(&chatbot); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}

	if u := userFromCtx(r); !u.IsAdmin() {
		cur, err := s.daemon.Config()
		if err != nil {
			writeToast(w, fmt.Sprintf("Не удалось сохранить: %v", err))
			return
		}
		if chatbot.CommandsEnabled == nil {
			chatbot.CommandsEnabled = map[string]bool{}
		}
		chatbot.CommandsEnabled["restart"] = cur.Chatbot.CommandsEnabled["restart"]
		chatbot.CommandsEnabled["stop"] = cur.Chatbot.CommandsEnabled["stop"]
		chatbot.CustomCommands = cur.Chatbot.CustomCommands
	}
	if err := s.daemon.SetChatbotConfig(chatbot); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось сохранить: %v", err))
		return
	}
	writeToast(w, "Сохранено")
}

func (s *Server) handleActionSettingsPanel(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	logHangs := r.FormValue("logHangs") == "on"
	watchdog := daemonclient.WatchdogSettings{AutoRestartOnHang: r.FormValue("autoRestartOnHang") == "on", LogHangs: &logHangs}

	backpack := daemonclient.BackpackSettings{
		MinHistoryIntervalSeconds: atoiOr(r.FormValue("minHistoryIntervalMinutes"), 1) * 60,
		ForceOverwriteInventory:   r.FormValue("forceOverwriteInventory") == "on",
		MaxHistoryEntries:         atoiOr(r.FormValue("maxHistoryEntries"), 300),
	}

	structures := daemonclient.StructuresSettings{
		BackupIntervalMinutes: atoiOr(r.FormValue("structuresBackupIntervalMinutes"), 60),
		MaxBackupEntries:      atoiOr(r.FormValue("structuresMaxBackupEntries"), 3),
		RequireOwnerNearby:    r.FormValue("structuresRequireOwnerNearby") == "on",
	}

	enabledMounts := r.Form["enabledMounts"]
	if enabledMounts == nil {
		enabledMounts = []string{}
	}
	monitoring := daemonclient.MonitoringSettings{
		EnabledMounts:        enabledMounts,
		WarningPercent:       atoiOr(r.FormValue("warningPercent"), 60),
		CriticalPercent:      atoiOr(r.FormValue("criticalPercent"), 85),
		HistoryRetentionDays: atoiOr(r.FormValue("monitoringRetentionDays"), 7),
	}
	maintenance := daemonclient.MaintenanceSettings{
		CleanOldBuildLogs:        r.FormValue("cleanOldBuildLogs") == "on",
		ManualActionWarnMinutes:  atoiOr(r.FormValue("manualActionWarnMinutes"), 0),
		CheckUpdatesPeriodically: r.FormValue("checkUpdatesPeriodically") == "on",
	}

	audit := daemonclient.AuditSettings{
		UnauthorizedAPIRequests: r.FormValue("auditUnauthorizedApiRequests") == "on",
		InventoryFuelAnomaly:    r.FormValue("auditInventoryFuelAnomaly") == "on",
		WipeLeftover:            r.FormValue("auditWipeLeftover") == "on",
		PositionJump:            r.FormValue("auditPositionJump") == "on",
		PermissionFactionChange: r.FormValue("auditPermissionFactionChange") == "on",
		LoginTracking:           r.FormValue("auditLoginTracking") == "on",
	}

	if u := userFromCtx(r); !u.IsAdmin() {
		cur, err := s.daemon.Config()
		if err != nil {
			writeToast(w, fmt.Sprintf("Не удалось сохранить: %v", err))
			return
		}
		watchdog = cur.Watchdog
		backpack = cur.Backpack
		structures = cur.Structures
		maintenance = cur.Maintenance
		audit = cur.Audit
	}

	if err := s.daemon.SetPanelBehaviorConfig(watchdog, backpack, structures, monitoring, maintenance, audit); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось сохранить: %v", err))
		return
	}

	cols := store.PlayerColumns{
		Faction:     r.FormValue("colFaction") == "on",
		Coords:      r.FormValue("colCoords") == "on",
		Kills:       r.FormValue("colKills") == "on",
		Died:        r.FormValue("colDied") == "on",
		Credits:     r.FormValue("colCredits") == "on",
		Exp:         r.FormValue("colExp") == "on",
		Ping:        r.FormValue("colPing") == "on",
		LastOnline:  r.FormValue("colLastOnline") == "on",
		OnlineHours: r.FormValue("colOnlineHours") == "on",
		QuickLinks:  r.FormValue("colQuickLinks") == "on",
	}
	if err := s.store.SetPlayerColumns(cols); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось сохранить: %v", err))
		return
	}

	chatColors := store.ChatColors{
		Light: store.ChatChannelColors{
			Global:       r.FormValue("chatColorLightGlobal"),
			Faction:      r.FormValue("chatColorLightFaction"),
			Alliance:     r.FormValue("chatColorLightAlliance"),
			Server:       r.FormValue("chatColorLightServer"),
			Singleplayer: r.FormValue("chatColorLightSingleplayer"),
		},
		Dark: store.ChatChannelColors{
			Global:       r.FormValue("chatColorDarkGlobal"),
			Faction:      r.FormValue("chatColorDarkFaction"),
			Alliance:     r.FormValue("chatColorDarkAlliance"),
			Server:       r.FormValue("chatColorDarkServer"),
			Singleplayer: r.FormValue("chatColorDarkSingleplayer"),
		},

		LightSenders: store.ChatSenderColors{
			Server: r.FormValue("chatColorSenderLightServer"),
			Admin:  r.FormValue("chatColorSenderLightAdmin"),
			VIP:    r.FormValue("chatColorSenderLightVip"),
		},
		DarkSenders: store.ChatSenderColors{
			Server: r.FormValue("chatColorSenderDarkServer"),
			Admin:  r.FormValue("chatColorSenderDarkAdmin"),
			VIP:    r.FormValue("chatColorSenderDarkVip"),
		},

		LightBanner: store.ChatBannerColors{
			Alarm:   r.FormValue("chatColorBannerLightAlarm"),
			Message: r.FormValue("chatColorBannerLightMessage"),
			Info:    r.FormValue("chatColorBannerLightInfo"),
		},
		DarkBanner: store.ChatBannerColors{
			Alarm:   r.FormValue("chatColorBannerDarkAlarm"),
			Message: r.FormValue("chatColorBannerDarkMessage"),
			Info:    r.FormValue("chatColorBannerDarkInfo"),
		},
	}
	if err := s.store.SetChatColors(chatColors); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось сохранить: %v", err))
		return
	}

	itemRenderConfig := store.ItemRenderConfig{
		LocalizedNames:   r.FormValue("item-render-localized-names") == "on",
		ShowDescriptions: r.FormValue("item-render-show-descriptions") == "on",
		RenderTags:       r.FormValue("item-render-tags") == "on",
		DarkBackground:   r.FormValue("item-render-dark-bg") == "on",
	}
	if err := s.store.SetItemRenderConfig(itemRenderConfig); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось сохранить: %v", err))
		return
	}
	writeToast(w, "Сохранено")
}

func (s *Server) handleActionSettingsBackup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Destinations [2]daemonclient.BackupDestination `json:"destinations"`

		Exclude daemonclient.BackupExclude `json:"exclude"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}

	body.Destinations[0].Type = "snapshot"
	if err := s.daemon.SetBackupConfig(daemonclient.BackupSettings{Destinations: body.Destinations, Exclude: body.Exclude}); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось сохранить: %v", err))
		return
	}
	writeToast(w, "Сохранено")
}

func (s *Server) handleActionSettingsBackupTest(w http.ResponseWriter, r *http.Request) {
	var d daemonclient.BackupDestination
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		http.Error(w, "Некорректный запрос", http.StatusBadRequest)
		return
	}
	if err := s.daemon.TestBackupDestination(d); err != nil {

		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeToast(w, "Соединение успешно")
}

func (s *Server) handleActionSettingsBackupClear(w http.ResponseWriter, r *http.Request) {
	dest, err := strconv.Atoi(r.URL.Query().Get("dest"))
	if err != nil {
		writeToast(w, "Некорректное хранилище")
		return
	}
	skipped, err := s.daemon.ClearBackupDestination(dest)
	if err != nil {
		writeToast(w, fmt.Sprintf("Не удалось очистить: %v", err))
		return
	}
	if len(skipped) > 0 {
		writeToast(w, fmt.Sprintf("Очищено, пропущено занятых копий: %d", len(skipped)))
		return
	}
	writeToast(w, "Хранилище очищено")
}
