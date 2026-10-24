package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"elb/ui/internal/daemonclient"
	"elb/ui/internal/i18n"
	"elb/ui/internal/logbuf"
	"elb/ui/internal/store"
)

const (
	daemonLogLines    = 400
	serverLogMaxBytes = 200 * 1024
)

type logLine struct {
	Time     time.Time
	Text     string
	Severity string
}

func classifyLogSeverity(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "не удал"), strings.Contains(lower, "паник"), strings.Contains(lower, "panic"):
		return "critical"
	case strings.Contains(lower, "зависш"), strings.Contains(lower, "не отвечает"), strings.Contains(lower, "предупрежд"):
		return "warning"
	default:
		return ""
	}
}

func toDaemonLogLines(entries []daemonclient.DaemonLogEntry) []logLine {
	out := make([]logLine, len(entries))
	for i, e := range entries {
		out[i] = logLine{Time: e.Time, Text: e.Text, Severity: classifyLogSeverity(e.Text)}
	}
	return out
}

func toWebUILogLines(entries []logbuf.Entry) []logLine {
	out := make([]logLine, len(entries))
	for i, e := range entries {
		out[i] = logLine{Time: e.Time, Text: e.Text, Severity: classifyLogSeverity(e.Text)}
	}
	return out
}

func logsSince24h[T any](items []T, timeOf func(T) time.Time) []T {
	cutoff := time.Now().Add(-24 * time.Hour)
	out := make([]T, 0, len(items))
	for _, it := range items {
		if t := timeOf(it); t.IsZero() || t.After(cutoff) {
			out = append(out, it)
		}
	}
	return out
}

func schedulerLogLines(lang string, runs []daemonclient.TaskRun) []logLine {
	out := make([]logLine, len(runs))
	for i, r := range runs {
		t, _ := time.Parse(time.RFC3339Nano, r.RanAt)
		outcome := i18n.T(lang, "scheduler.runs.ok")

		sev := ""
		if !r.Success {
			outcome = r.Message
			sev = "critical"
		}

		text := taskDescriptionL(lang, r.ActionType, r.Params, nil)

		if r.SubtaskID != 0 {
			text = "↳ " + text
		}
		out[i] = logLine{Time: t, Text: text + " — " + outcome, Severity: sev}
	}
	return out
}

func auditLogLines(events []daemonclient.AuditEvent) []logLine {
	out := make([]logLine, len(events))
	for i, e := range events {
		t, _ := time.Parse(time.RFC3339Nano, e.Ts)
		sev := "warning"
		if e.Category == "loginTracking" && strings.Contains(e.Summary, " OK ") {
			sev = ""
		}
		out[i] = logLine{Time: t, Text: e.Summary, Severity: sev}
	}
	return out
}

func reversed[T any](s []T) []T {
	out := make([]T, len(s))
	for i, v := range s {
		out[len(s)-1-i] = v
	}
	return out
}

func findPlayerByName(players []daemonclient.Player, name string) (daemonclient.Player, bool) {
	for _, p := range players {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return daemonclient.Player{}, false
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)

	players, err := s.daemon.Players()
	if err != nil {
		log.Printf("dashboard: players: %v", err)
	}
	chat, err := s.daemon.Chat()
	if err != nil {
		log.Printf("dashboard: chat: %v", err)
	}
	adminPlayers, err := s.store.AdminPlayers()
	if err != nil {
		log.Printf("dashboard: admin players: %v", err)
	}
	factionList, err := s.daemon.Factions()
	if err != nil {
		log.Printf("dashboard: factions: %v", err)
	}

	alliancesResult, err := s.daemon.Alliances()
	if err != nil {
		log.Printf("dashboard: alliances: %v", err)
	}
	daemonLogs, err := s.daemon.DaemonLogs(daemonLogLines)
	if err != nil {
		log.Printf("dashboard: daemon logs: %v", err)
	}
	serverLog, err := s.daemon.ServerLog(serverLogMaxBytes)
	if err != nil {
		log.Printf("dashboard: server log: %v", err)
	}
	taskRuns, err := s.daemon.TaskRuns()
	if err != nil {
		log.Printf("dashboard: scheduler runs: %v", err)
	}

	auditEnabled, err := s.daemon.AuditEnabled()
	if err != nil {
		log.Printf("dashboard: audit enabled: %v", err)
	}
	var auditEvents []daemonclient.AuditEvent
	if auditEnabled {
		auditEvents, err = s.daemon.AuditEvents(daemonLogLines)
		if err != nil {
			log.Printf("dashboard: audit events: %v", err)
		}
	}

	data := map[string]any{
		"Username":            u.Username,
		"Players":             players,
		"Chat":                chat,
		"AdminIdentities":     adminPlayers,
		"Factions":            factionList,
		"Alliances":           alliancesResult.Pairs,
		"AlliancesEnabled":    alliancesResult.Enabled,
		"DaemonLogs":          reversed(logsSince24h(toDaemonLogLines(daemonLogs), func(l logLine) time.Time { return l.Time })),
		"ServerLog":           reversed(logsSince24h(serverLog, func(l daemonclient.ServerLogLine) time.Time { return l.Time })),
		"WebUILogs":           reversed(logsSince24h(toWebUILogLines(s.uiLog.Recent(daemonLogLines)), func(l logLine) time.Time { return l.Time })),
		"SchedulerLog":        logsSince24h(schedulerLogLines(u.Locale, taskRuns), func(l logLine) time.Time { return l.Time }),
		"AuditEnabled":        auditEnabled,
		"AuditLog":            logsSince24h(auditLogLines(auditEvents), func(l logLine) time.Time { return l.Time }),
		"LogsRefreshControls": true,
	}
	for k, v := range s.statusBarData(u) {
		data[k] = v
	}
	if err := s.execTemplate(w, r, "dashboard.html", data); err != nil {
		log.Printf("dashboard: render: %v", err)
	}
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	locale := i18n.Default
	if u := userFromCtx(r); u != nil && u.Locale != "" {
		locale = u.Locale
	}
	s.hub.ServeWS(w, r, locale)
}

func (s *Server) handleActionLogsDaemon(w http.ResponseWriter, r *http.Request) {
	logs, err := s.daemon.DaemonLogs(daemonLogLines)
	if err != nil {
		fmt.Fprintf(w, `<div class="empty">%s</div>`, template.HTMLEscapeString(err.Error()))
		return
	}
	lines := logsSince24h(toDaemonLogLines(logs), func(l logLine) time.Time { return l.Time })
	if err := s.execTemplate(w, r, "simple_log_rows", reversed(lines)); err != nil {
		log.Printf("logs/daemon: render: %v", err)
	}
}

func (s *Server) handleActionLogsWebui(w http.ResponseWriter, r *http.Request) {
	lines := logsSince24h(toWebUILogLines(s.uiLog.Recent(daemonLogLines)), func(l logLine) time.Time { return l.Time })
	if err := s.execTemplate(w, r, "simple_log_rows", reversed(lines)); err != nil {
		log.Printf("logs/webui: render: %v", err)
	}
}

func (s *Server) handleActionLogsServer(w http.ResponseWriter, r *http.Request) {
	logs, err := s.daemon.ServerLog(serverLogMaxBytes)
	if err != nil {
		fmt.Fprintf(w, `<div class="empty">%s</div>`, template.HTMLEscapeString(err.Error()))
		return
	}
	lines := logsSince24h(logs, func(l daemonclient.ServerLogLine) time.Time { return l.Time })
	if err := s.execTemplate(w, r, "server_log_rows", reversed(lines)); err != nil {
		log.Printf("logs/server: render: %v", err)
	}
}

func (s *Server) handleActionLogsScheduler(w http.ResponseWriter, r *http.Request) {
	runs, err := s.daemon.TaskRuns()
	if err != nil {
		fmt.Fprintf(w, `<div class="empty">%s</div>`, template.HTMLEscapeString(err.Error()))
		return
	}
	lang := i18n.Default
	if u := userFromCtx(r); u != nil {
		lang = u.Locale
	}
	lines := logsSince24h(schedulerLogLines(lang, runs), func(l logLine) time.Time { return l.Time })
	if err := s.execTemplate(w, r, "simple_log_rows", lines); err != nil {
		log.Printf("logs/scheduler: render: %v", err)
	}
}

func (s *Server) handleActionLogsAudit(w http.ResponseWriter, r *http.Request) {
	events, err := s.daemon.AuditEvents(daemonLogLines)
	if err != nil {
		fmt.Fprintf(w, `<div class="empty">%s</div>`, template.HTMLEscapeString(err.Error()))
		return
	}
	lines := logsSince24h(auditLogLines(events), func(l logLine) time.Time { return l.Time })
	if err := s.execTemplate(w, r, "simple_log_rows", lines); err != nil {
		log.Printf("logs/audit: render: %v", err)
	}
}

func (s *Server) handleIcon(w http.ResponseWriter, r *http.Request) {
	data, ok, err := s.store.Icon(s.iconSync.Status().Scenario, r.PathValue("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		data = placeholderIcon()
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(data)
}

func (s *Server) handleActionPlayer(w http.ResponseWriter, r *http.Request) {
	entityId, _ := strconv.Atoi(r.URL.Query().Get("entityId"))
	steamId := r.URL.Query().Get("steamId")
	name := r.URL.Query().Get("name")

	var wg sync.WaitGroup

	var players []daemonclient.Player
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		if players, err = s.daemon.Players(); err != nil {
			log.Printf("player panel: players: %v", err)
		}
	}()

	var toolbelt, bag []daemonclient.ItemStack
	var asOf *time.Time
	var invErr string
	wg.Add(1)
	go func() {
		defer wg.Done()
		if inv, err := s.daemon.PlayerInventory(entityId); err == nil {
			toolbelt, bag = inv.Toolbelt, inv.Bag
		} else if steamId != "" {
			if history, herr := s.daemon.BackpackHistory(steamId); herr == nil && len(history) > 0 {
				last := history[len(history)-1]
				toolbelt, bag = last.Toolbelt, last.Bag
				asOf = &last.Timestamp
			} else {
				invErr = "Не удалось получить инвентарь (игрок оффлайн, истории тоже нет)"
			}
		} else {
			invErr = fmt.Sprintf("Не удалось получить инвентарь: %v", err)
		}
	}()

	var pendingResult map[string]json.RawMessage
	if steamId != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			if pendingResult, err = s.daemon.PendingEdits(steamId); err != nil {
				log.Printf("player panel: pending edits: %v", err)
			}
		}()
	}

	var statsSnapshot daemonclient.PlayerStatsSnapshot
	if steamId != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			if statsSnapshot, err = s.daemon.PlayerStats(steamId); err != nil {
				log.Printf("player panel: player stats: %v", err)
			}
		}()
	}

	var byName map[string]int
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		if byName, err = s.store.ItemMapping(); err != nil {
			log.Printf("player panel: item mapping: %v", err)
		}
	}()

	var factionList []daemonclient.Faction
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		if factionList, err = s.daemon.Factions(); err != nil {
			log.Printf("player panel: factions: %v", err)
		}
	}()

	var stackSizes map[string]int
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		if stackSizes, err = s.daemon.ItemStackSizes(); err != nil {
			log.Printf("player panel: stack sizes: %v", err)
		}
	}()

	var categories map[string]string
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		if categories, err = s.daemon.ItemCategories(); err != nil {
			log.Printf("player panel: item categories: %v", err)
		}
	}()

	var ammoTypes map[string]string
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		if ammoTypes, err = s.daemon.ItemAmmoTypes(); err != nil {
			log.Printf("player panel: ammo types: %v", err)
		}
	}()

	itemLang := i18n.Default
	if u := userFromCtx(r); u != nil {
		itemLang = u.Locale
	}
	var displayNames map[string]string
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		if displayNames, err = s.daemon.ItemDisplayNames(itemLang); err != nil {
			log.Printf("player panel: display names: %v", err)
		}
	}()

	var descriptions map[string]string
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		if descriptions, err = s.daemon.ItemDescriptions(itemLang); err != nil {
			log.Printf("player panel: descriptions: %v", err)
		}
	}()

	var vipCfg daemonclient.AppConfig
	var vipCfgErr error
	if steamId != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			vipCfg, vipCfgErr = s.daemon.Config()
		}()
	}

	var adminPlayers []store.AdminPlayer

	var moderatorPlayers []store.ModeratorPlayer
	if steamId != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			if adminPlayers, err = s.store.AdminPlayers(); err != nil {
				log.Printf("player panel: admin players: %v", err)
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			if moderatorPlayers, err = s.store.ModeratorPlayers(); err != nil {
				log.Printf("player panel: moderator players: %v", err)
			}
		}()
	}

	wg.Wait()

	isAdminPlayer := false
	for _, ap := range adminPlayers {
		if ap.SteamId == steamId {
			isAdminPlayer = true
			break
		}
	}
	isModeratorPlayer := false
	for _, mp := range moderatorPlayers {
		if mp.SteamId == steamId {
			isModeratorPlayer = true
			break
		}
	}

	var player *daemonclient.Player
	for i := range players {
		if players[i].EntityId == entityId {
			player = &players[i]
			break
		}
	}

	beforeToolbelt, beforeBag := toolbelt, bag

	var invPending bool
	var setinfoRaw json.RawMessage
	if pendingResult != nil {
		if raw, ok := pendingResult["inventory"]; ok {
			var pendingInv daemonclient.Inventory
			if jerr := json.Unmarshal(raw, &pendingInv); jerr == nil {

				switch pendingInv.Part {
				case "toolbelt":
					toolbelt = pendingInv.Toolbelt
				case "bag":
					bag = pendingInv.Bag
				default:
					toolbelt, bag = pendingInv.Toolbelt, pendingInv.Bag
				}
				invErr = ""
				invPending = true
			}
		}
		setinfoRaw = pendingResult["setinfo"]
	}
	setinfoPending := len(setinfoRaw) > 0

	effSetInfo := effectiveSetInfo(player, setinfoRaw, statsSnapshot)
	factoryInFactory, factoryRemaining, factoryResources, factoryKnown := effectiveFactory(player, statsSnapshot)
	factoryResourcesJSON, _ := json.Marshal(factoryResources)

	factoryOnline := player != nil && player.Live != nil

	isVip := false
	if steamId != "" {
		if vipCfgErr != nil {
			log.Printf("player panel: config (vip): %v", vipCfgErr)
		} else {
			isVip = vipListContains(vipCfg.Server.PlayerLoginVipNames.Value, steamId)
		}
	}

	itemNamesJSON, _ := json.Marshal(byName)
	stackSizesJSON, _ := json.Marshal(stackSizes)
	categoriesJSON, _ := json.Marshal(categories)
	ammoTypesJSON, _ := json.Marshal(ammoTypes)
	displayNamesJSON, _ := json.Marshal(displayNames)
	descriptionsJSON, _ := json.Marshal(descriptions)
	toolbeltJSON, _ := json.Marshal(toolbelt)
	bagJSON, _ := json.Marshal(bag)

	beforeToolbeltJSON, _ := json.Marshal(beforeToolbelt)
	beforeBagJSON, _ := json.Marshal(beforeBag)

	data := map[string]any{
		"EntityId":         entityId,
		"SteamId":          steamId,
		"Name":             name,
		"Player":           player,
		"AsOf":             asOf,
		"InventoryError":   invErr,
		"InventoryPending": invPending,
		"SetInfoPending":   setinfoPending,
		"HasPending":       invPending || setinfoPending,
		"EffectiveSetInfo": effSetInfo,

		"FactoryInFactory":     factoryInFactory,
		"FactoryRemaining":     factoryRemaining,
		"FactoryKnown":         factoryKnown,
		"FactoryOnline":        factoryOnline,
		"FactoryResourcesJSON": template.JS(factoryResourcesJSON),
		"Factions":             factionList,
		"ItemIDsJSON":          template.JS(itemNamesJSON),
		"StackSizesJSON":       template.JS(stackSizesJSON),
		"CategoriesJSON":       template.JS(categoriesJSON),
		"AmmoTypesJSON":        template.JS(ammoTypesJSON),
		"DisplayNamesJSON":     template.JS(displayNamesJSON),
		"DescriptionsJSON":     template.JS(descriptionsJSON),
		"BeforeToolbeltJSON":   template.JS(beforeToolbeltJSON),
		"BeforeBagJSON":        template.JS(beforeBagJSON),
		"ToolbeltJSON":         template.JS(toolbeltJSON),
		"BagJSON":              template.JS(bagJSON),
		"IsVip":                isVip,
		"IsAdminPlayer":        isAdminPlayer,
		"IsModeratorPlayer":    isModeratorPlayer,

		"IsAdmin": userFromCtx(r).IsAdmin(),
	}
	if err := s.execTemplate(w, r, "player_panel_fragment", data); err != nil {
		log.Printf("player panel: render: %v", err)
	}
}

var setInfoDisplayFields = []string{
	"health", "healthMax", "food", "foodMax", "oxygen", "oxygenMax",
	"stamina", "staminaMax", "radiation", "radiationMax", "bodyTemp", "bodyTempMax",
	"upgradePoints",
}

func effectiveSetInfo(player *daemonclient.Player, pendingRaw json.RawMessage, stats daemonclient.PlayerStatsSnapshot) map[string]string {
	out := map[string]string{}

	if stats.Found {
		vals := map[string]float64{
			"health": stats.Health, "healthMax": stats.HealthMax,
			"food": stats.Food, "foodMax": stats.FoodMax,
			"oxygen": stats.Oxygen, "oxygenMax": stats.OxygenMax,
			"stamina": stats.Stamina, "staminaMax": stats.StaminaMax,
			"radiation": stats.Radiation, "radiationMax": stats.RadiationMax,
			"bodyTemp": stats.BodyTemp, "bodyTempMax": stats.BodyTempMax,
			"upgradePoints": float64(stats.Upgrade),
			"credits":       stats.Credits,
		}
		for k, v := range vals {
			out[k] = strconv.FormatFloat(v, 'f', 0, 64)
		}
		out["factionRole"] = strconv.Itoa(stats.FactionRole)
		out["experiencePoints"] = strconv.Itoa(stats.Exp)
	}

	if player != nil {
		if live := player.Live; live != nil {
			vals := map[string]float64{
				"health": live.Health, "healthMax": live.HealthMax,
				"food": live.Food, "foodMax": live.FoodMax,
				"oxygen": live.Oxygen, "oxygenMax": live.OxygenMax,
				"stamina": live.Stamina, "staminaMax": live.StaminaMax,
				"radiation": live.Radiation, "radiationMax": live.RadiationMax,
				"bodyTemp": live.BodyTemp, "bodyTempMax": live.BodyTempMax,
				"upgradePoints": float64(live.Upgrade),
				"credits":       live.Credits,
			}
			for k, v := range vals {
				out[k] = strconv.FormatFloat(v, 'f', 0, 64)
			}
			out["factionRole"] = strconv.Itoa(live.FactionRole)
			out["experiencePoints"] = strconv.Itoa(live.Exp)
		}

		out["factionId"] = strconv.Itoa(player.FactionId)
	}

	if len(pendingRaw) > 0 {
		var raw map[string]any
		if err := json.Unmarshal(pendingRaw, &raw); err == nil {
			for _, key := range append(append([]string{}, setInfoDisplayFields...), "factionId", "factionRole", "experiencePoints") {
				v, ok := raw[key]
				if !ok {
					continue
				}
				if f, ok := v.(float64); ok {
					out[key] = strconv.FormatFloat(f, 'f', 0, 64)
				}
			}
		}
	}
	return out
}

func effectiveFactory(player *daemonclient.Player, stats daemonclient.PlayerStatsSnapshot) (inFactory string, remaining float64, resources map[string]float64, known bool) {
	if stats.Found {
		inFactory, remaining, resources, known = stats.BpInFactory, stats.BpRemainingTime, stats.BpResourcesInFactory, true
	}
	if player != nil && player.Live != nil {
		inFactory, remaining, resources, known = player.Live.BpInFactory, player.Live.BpRemainingTime, player.Live.BpResourcesInFactory, true
	}
	return
}

func (s *Server) handleActionPlayerSetInventory(w http.ResponseWriter, r *http.Request) {
	var inv daemonclient.Inventory
	if err := json.NewDecoder(r.Body).Decode(&inv); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	if inv.PlayerId == 0 || inv.SteamId == "" {
		writeToast(w, "Не указан игрок")
		return
	}
	res, err := s.daemon.SetPlayerInventory(inv)
	if err != nil {
		writeToast(w, fmt.Sprintf("Не удалось изменить инвентарь: %v", err))
		return
	}
	switch {
	case res.Conflict:
		writeToast(w, "Инвентарь НЕ изменён — он успел измениться с момента редактирования (в настройках можно включить принудительную перезапись)")
	case res.Queued:
		writeToast(w, "Инвентарь поставлен в очередь (игрок offline)")
	default:
		writeToast(w, "Инвентарь изменён")
	}
}

func (s *Server) handleActionPlayerBackpackHistory(w http.ResponseWriter, r *http.Request) {
	steamId := r.URL.Query().Get("steamId")
	if steamId == "" {
		http.Error(w, "нужен ?steamId=", http.StatusBadRequest)
		return
	}
	history, err := s.daemon.BackpackHistory(steamId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

func (s *Server) handleActionPlayerBackpackRestore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SteamID   string    `json:"steamId"`
		PlayerId  int       `json:"playerId"`
		Timestamp time.Time `json:"timestamp"`

		Part string `json:"part"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	if body.SteamID == "" {
		writeToast(w, "Не указан игрок")
		return
	}
	if err := s.daemon.BackpackRestore(body.SteamID, body.PlayerId, body.Timestamp, body.Part); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось поставить восстановление в очередь: %v", err))
		return
	}
	switch body.Part {
	case "toolbelt":
		writeToast(w, "Восстановление тулбара поставлено в очередь")
	case "bag":
		writeToast(w, "Восстановление рюкзака поставлено в очередь")
	default:
		writeToast(w, "Восстановление инвентаря поставлено в очередь")
	}
}

func (s *Server) handleActionKick(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	steamID := r.FormValue("steamId")
	if steamID == "" {
		writeToast(w, "Не указан игрок")
		return
	}
	if !userFromCtx(r).IsAdmin() && s.isAdminPlayer(steamID) {
		writeToast(w, "Игрок-админ защищён от кика — управляется списком \"Игроки-админы\"")
		return
	}
	if err := s.daemon.KickPlayer(steamID, ""); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось кикнуть: %v", err))
		return
	}
	writeToast(w, "Игрок кикнут")
}

var setInfoIntFields = []string{
	"health", "healthMax", "stamina", "staminaMax",
	"food", "foodMax", "oxygen", "oxygenMax",
	"radiation", "radiationMax", "bodyTemp", "bodyTempMax",

	"experiencePoints",
	"upgradePoints", "factionGroup", "factionId", "factionRole",
}

func (s *Server) handleActionPlayerSetInfo(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	entityID, _ := strconv.Atoi(r.FormValue("entityId"))
	steamID := r.FormValue("steamId")
	if entityID == 0 || steamID == "" {
		writeToast(w, "Не указан игрок")
		return
	}
	fields := map[string]any{}
	for _, name := range setInfoIntFields {
		v := r.FormValue(name)
		if v == "" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			writeToast(w, fmt.Sprintf("Некорректное значение поля %s", name))
			return
		}
		fields[name] = n
	}
	if err := s.daemon.SetPlayerInfo(entityID, steamID, fields); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось применить: %v", err))
		return
	}
	writeToast(w, "Применено (или поставлено в очередь, если игрок offline)")
}

func (s *Server) handleActionPlayerSetRole(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	steamID, role := r.FormValue("steamId"), r.FormValue("role")
	if steamID == "" || role == "" {
		writeToast(w, "Не указан игрок или роль")
		return
	}

	if !userFromCtx(r).IsAdmin() && (role == "gm" || role == "admin") {
		writeToast(w, "Эту роль может выдать только администратор панели")
		return
	}
	if err := s.daemon.SetRolePlayer(steamID, role); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось задать роль: %v", err))
		return
	}
	writeToast(w, "Роль изменена")
}

func (s *Server) handleActionPlayerBan(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	steamID, duration := r.FormValue("steamId"), r.FormValue("duration")
	if steamID == "" || duration == "" {
		writeToast(w, "Не указан игрок или длительность")
		return
	}
	if !userFromCtx(r).IsAdmin() && s.isAdminPlayer(steamID) {
		writeToast(w, "Игрок-админ защищён от бана — управляется списком \"Игроки-админы\"")
		return
	}
	if err := s.daemon.BanPlayer(steamID, duration); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось забанить: %v", err))
		return
	}
	writeToast(w, "Игрок забанен")
}

func (s *Server) handleActionPlayerUnban(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	steamID := r.FormValue("steamId")
	if steamID == "" {
		writeToast(w, "Не указан игрок")
		return
	}
	if err := s.daemon.UnbanPlayer(steamID); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось снять бан: %v", err))
		return
	}
	writeToast(w, "Бан снят")
}

func (s *Server) handleActionPlayerUnmute(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	steamID := r.FormValue("steamId")
	if steamID == "" {
		writeToast(w, "Не указан игрок")
		return
	}
	if err := s.daemon.UnmutePlayer(steamID); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось снять мьют: %v", err))
		return
	}
	writeToast(w, "Мьют снят")
}

func (s *Server) handleActionPlayerMute(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	steamID, duration := r.FormValue("steamId"), r.FormValue("duration")
	if steamID == "" || duration == "" {
		writeToast(w, "Не указан игрок или длительность")
		return
	}
	if !userFromCtx(r).IsAdmin() && s.isAdminPlayer(steamID) {
		writeToast(w, "Игрок-админ защищён от мьюта — управляется списком \"Игроки-админы\"")
		return
	}
	if err := s.daemon.MutePlayer(steamID, duration); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось замьютить: %v", err))
		return
	}
	writeToast(w, "Игрок замьючен")
}

func (s *Server) handleActionPlayerAddCredits(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	entityID, _ := strconv.Atoi(r.FormValue("entityId"))
	steamID := r.FormValue("steamId")
	amount, err := strconv.ParseFloat(r.FormValue("amount"), 64)
	if entityID == 0 || steamID == "" || err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	if err := s.daemon.AddCredits(entityID, steamID, amount); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось изменить кредиты: %v", err))
		return
	}
	writeToast(w, "Кредиты изменены")
}

func (s *Server) handleActionPlayerFactoryFinish(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	entityID, _ := strconv.Atoi(r.FormValue("entityId"))
	if entityID == 0 {
		writeToast(w, "Не указан игрок")
		return
	}
	if err := s.daemon.FinishFactoryBlueprint(entityID); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось завершить производство: %v", err))
		return
	}
	writeToast(w, "Производство завершено")
}

func (s *Server) handleActionConsole(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		fmt.Fprint(w, "некорректный запрос")
		return
	}
	command := r.FormValue("command")
	if command == "" {
		return
	}
	out, err := s.daemon.Console(command)
	if err != nil {
		fmt.Fprint(w, err.Error())
		return
	}
	fmt.Fprint(w, out)
}

func (s *Server) handleActionPlayerRemoteEx(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		fmt.Fprint(w, "некорректный запрос")
		return
	}
	steamID := r.FormValue("steamId")
	command := r.FormValue("command")
	if steamID == "" || command == "" {
		return
	}
	out, err := s.daemon.PlayerRemoteEx(steamID, command)
	if err != nil {
		fmt.Fprint(w, err.Error())
		return
	}
	fmt.Fprint(w, out)
}

func (s *Server) handleActionPdaChapters(w http.ResponseWriter, r *http.Request) {
	lang := ""
	if u := userFromCtx(r); u != nil {
		lang = u.Locale
	}
	chapters, err := s.daemon.PdaChapters(lang)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(chapters)
}

func (s *Server) handleActionMessage(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	text := r.FormValue("text")
	if text == "" {
		writeToast(w, "Пустой текст")
		return
	}

	if r.FormValue("type") == "banner" {
		s.sendBanner(w, r, text)
		return
	}
	s.sendChat(w, r, text)
}

func (s *Server) sendChat(w http.ResponseWriter, r *http.Request, text string) {
	channel := r.FormValue("channel")
	opts := daemonclient.SendChatOptions{
		Text:      text,
		Channel:   channel,
		AsSteamID: r.FormValue("asSteamId"),
	}
	if channel == "faction" {
		opts.FactionID = r.FormValue("factionId")
		if opts.FactionID == "" {
			writeToast(w, "Не выбрана фракция")
			return
		}
	}
	if channel == "alliance" {
		opts.AllianceFactionIds = r.FormValue("allianceFactionIds")
		if opts.AllianceFactionIds == "" {
			writeToast(w, "Не выбран альянс")
			return
		}
	}
	if channel == "singleplayer" {
		name := strings.TrimSpace(r.FormValue("recipientName"))
		if name == "" {
			writeToast(w, "Не указан игрок")
			return
		}
		players, err := s.daemon.Players()
		if err != nil {
			writeToast(w, fmt.Sprintf("Не удалось получить список игроков: %v", err))
			return
		}
		p, ok := findPlayerByName(players, name)
		if !ok || !p.Online {
			writeToast(w, fmt.Sprintf("Игрок не найден или оффлайн: %s", name))
			return
		}

		if opts.AsSteamID != "" && opts.AsSteamID != "__server__" && opts.AsSteamID == p.SteamId {
			writeToast(w, "Нельзя отправить сообщение самому себе")
			return
		}
		opts.RecipientSteamID = p.SteamId
	}
	if err := s.daemon.SendChat(opts); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось отправить: %v", err))
		return
	}

}

func (s *Server) sendBanner(w http.ResponseWriter, r *http.Request, text string) {
	prio := 2
	switch r.FormValue("priority") {
	case "alarm":
		prio = 0
	case "message":
		prio = 1
	}

	name := strings.TrimSpace(r.FormValue("bannerRecipientName"))
	var err error
	if name == "" {
		err = s.daemon.MessageAll(text, prio, 0)
	} else {
		players, perr := s.daemon.Players()
		if perr != nil {
			writeToast(w, fmt.Sprintf("Не удалось получить список игроков: %v", perr))
			return
		}
		p, ok := findPlayerByName(players, name)
		if !ok || !p.Online {
			writeToast(w, fmt.Sprintf("Игрок не найден или оффлайн: %s", name))
			return
		}
		err = s.daemon.MessagePlayer(p.EntityId, text, prio, 0)
	}
	if err != nil {
		writeToast(w, fmt.Sprintf("Не удалось показать баннер: %v", err))
		return
	}
	writeToast(w, "Баннер отправлен")
}

func (s *Server) handleAdminPlayerAdd(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	steamID := r.FormValue("steamId")
	if steamID == "" {
		writeToast(w, "Не указан игрок")
		return
	}

	players, err := s.daemon.Players()
	if err != nil {
		writeToast(w, fmt.Sprintf("Не удалось получить список игроков: %v", err))
		return
	}
	name := steamID
	for _, p := range players {
		if p.SteamId == steamID {
			name = p.Name
			break
		}
	}
	if err := s.store.AddAdminPlayer(steamID, name); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось добавить: %v", err))
		return
	}
	s.syncAdminSteamIds()
	s.renderAdminPlayers(w, r)
}

func (s *Server) handleAdminPlayerRemove(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	steamID := r.FormValue("steamId")
	if err := s.store.RemoveAdminPlayer(steamID); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось убрать: %v", err))
		return
	}
	s.syncAdminSteamIds()
	s.renderAdminPlayers(w, r)
}

func (s *Server) isAdminPlayer(steamID string) bool {
	admins, err := s.store.AdminPlayers()
	if err != nil {
		log.Printf("isAdminPlayer: %v", err)
		return false
	}
	for _, a := range admins {
		if a.SteamId == steamID {
			return true
		}
	}
	return false
}

func (s *Server) syncAdminSteamIds() {
	admins, err := s.store.AdminPlayers()
	if err != nil {
		log.Printf("syncAdminSteamIds: %v", err)
		return
	}
	ids := make([]string, len(admins))
	for i, a := range admins {
		ids[i] = a.SteamId
	}
	if err := s.daemon.SetAdminSteamIds(ids); err != nil {
		log.Printf("syncAdminSteamIds: push: %v", err)
	}
}

func (s *Server) renderAdminPlayers(w http.ResponseWriter, r *http.Request) {
	admins, err := s.store.AdminPlayers()
	if err != nil {
		log.Printf("admin players: %v", err)
		return
	}
	if err := s.execTemplate(w, r, "admin_players_fragment", admins); err != nil {
		log.Printf("admin players: render: %v", err)
	}
}

func (s *Server) isModeratorPlayer(steamID string) bool {
	mods, err := s.store.ModeratorPlayers()
	if err != nil {
		log.Printf("isModeratorPlayer: %v", err)
		return false
	}
	for _, m := range mods {
		if m.SteamId == steamID {
			return true
		}
	}
	return false
}

func (s *Server) handleModeratorPlayerAdd(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	steamID := r.FormValue("steamId")
	if steamID == "" {
		writeToast(w, "Не указан игрок")
		return
	}
	players, err := s.daemon.Players()
	if err != nil {
		writeToast(w, fmt.Sprintf("Не удалось получить список игроков: %v", err))
		return
	}
	name := steamID
	for _, p := range players {
		if p.SteamId == steamID {
			name = p.Name
			break
		}
	}
	if err := s.store.AddModeratorPlayer(steamID, name); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось добавить: %v", err))
		return
	}
	s.syncModeratorSteamIds()
	s.renderModeratorPlayers(w, r)
}

func (s *Server) handleModeratorPlayerRemove(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	steamID := r.FormValue("steamId")
	if err := s.store.RemoveModeratorPlayer(steamID); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось убрать: %v", err))
		return
	}
	s.syncModeratorSteamIds()
	s.renderModeratorPlayers(w, r)
}

func (s *Server) syncModeratorSteamIds() {
	mods, err := s.store.ModeratorPlayers()
	if err != nil {
		log.Printf("syncModeratorSteamIds: %v", err)
		return
	}
	ids := make([]string, len(mods))
	for i, m := range mods {
		ids[i] = m.SteamId
	}
	if err := s.daemon.SetModeratorSteamIds(ids); err != nil {
		log.Printf("syncModeratorSteamIds: push: %v", err)
	}
}

func (s *Server) renderModeratorPlayers(w http.ResponseWriter, r *http.Request) {
	mods, err := s.store.ModeratorPlayers()
	if err != nil {
		log.Printf("moderator players: %v", err)
		return
	}
	if err := s.execTemplate(w, r, "moderator_players_fragment", mods); err != nil {
		log.Printf("moderator players: render: %v", err)
	}
}

func writeToast(w http.ResponseWriter, msg string) {
	fmt.Fprint(w, msg)
}
