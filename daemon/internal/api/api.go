package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"elb/daemon/internal/audit"
	"elb/daemon/internal/backpack"
	"elb/daemon/internal/chatlog"
	"elb/daemon/internal/config"
	"elb/daemon/internal/factions"
	"elb/daemon/internal/globaldb"
	"elb/daemon/internal/history"
	"elb/daemon/internal/logbuf"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/notify"
	"elb/daemon/internal/permissions"
	"elb/daemon/internal/playerqueue"
	"elb/daemon/internal/playerstats"
	"elb/daemon/internal/presence"
	"elb/daemon/internal/safego"
	"elb/daemon/internal/scheduler"
	"elb/daemon/internal/serverctl"
	"elb/daemon/internal/settings"
	"elb/daemon/internal/structurequeue"
	"elb/daemon/internal/telnet"
	"elb/daemon/internal/watchdog"
)

func Router(cfg config.Config, logBuf *logbuf.Buffer) http.Handler {
	mod := modclient.New(cfg.ModAPI)
	mux := http.NewServeMux()

	registerServerRoutes(mux, cfg, mod, logBuf)
	registerSteamRoutes(mux, cfg, mod, logBuf)
	registerChatRoutes(mux, cfg, mod, logBuf)
	registerPlayerRoutes(mux, cfg, mod, logBuf)
	registerStructureRoutes(mux, cfg, mod, logBuf)
	registerPlayfieldRoutes(mux, cfg, mod, logBuf)
	registerItemRoutes(mux, cfg, mod, logBuf)
	registerHistoryRoutes(mux, cfg, mod, logBuf)
	registerNotificationRoutes(mux, cfg, mod, logBuf)
	registerBackupRoutes(mux, cfg, mod, logBuf)
	registerConfigRoutes(mux, cfg, mod, logBuf)
	registerContentRoutes(mux, cfg, mod, logBuf)
	registerSchedulerRoutes(mux, cfg, mod, logBuf)
	registerIconRoutes(mux, cfg, mod, logBuf)
	registerLogRoutes(mux, cfg, mod, logBuf)
	registerMaintenanceRoutes(mux, cfg, mod, logBuf)
	registerQueueRoutes(mux, cfg, mod, logBuf)
	registerMetricsRoutes(mux, cfg, mod, logBuf)
	registerAuditRoutes(mux, cfg, mod, logBuf)
	registerScenarioCacheRoutes(mux, cfg, mod, logBuf)
	registerPdaRoutes(mux, cfg, mod, logBuf)

	return logMiddleware(authMiddleware(cfg, mux))
}

var maintenanceWipeStores = map[string]func(config.Config) error{
	"backpack":       backpack.Wipe,
	"chat":           chatlog.Wipe,
	"history":        history.Wipe,
	"notify":         notify.ClearAll,
	"presence":       presence.Wipe,
	"playerqueue":    playerqueue.Wipe,
	"structurequeue": structurequeue.Wipe,
	"playerstats":    playerstats.Wipe,
}

type playerActionBody struct {
	Player   string `json:"player"`
	Duration string `json:"duration"`
	Message  string `json:"message"`
	Role     string `json:"role"`
}

var builtinCommandNames = map[string]bool{"?": true, "help": true, "wipe": true, "get": true, "ship": true, "destroy": true, "restart": true, "stop": true, "time": true, "survival": true, "rules": true}

func validateSurvivalItems(items []settings.SurvivalItem) error {
	if len(items) > 10 {
		return fmt.Errorf("survivalItems: максимум 10 предметов, получено %d", len(items))
	}
	for _, it := range items {
		if it.ItemID <= 0 {
			return fmt.Errorf("survivalItems: недопустимый itemId %d", it.ItemID)
		}
		if it.Count <= 0 {
			return fmt.Errorf("survivalItems: count должен быть положительным (предмет %d)", it.ItemID)
		}
	}
	return nil
}

var customCommandNameRe = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

var scriptNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

var dedicatedLogNameRe = regexp.MustCompile(`^Dedicated_(\d{2})(\d{2})(\d{2})-`)

var validActionTypes = map[string]bool{"": true, settings.ActionDialog: true, settings.ActionChat: true, settings.ActionBanner: true}

func validateCustomCommands(cmds []settings.CustomCommand) error {
	seen := map[string]bool{}
	for _, c := range cmds {
		name := strings.ToLower(strings.TrimSpace(c.Name))
		if !customCommandNameRe.MatchString(name) {
			return fmt.Errorf("customCommands: недопустимое имя %q (только a-z0-9_- длиной 1-32)", c.Name)
		}
		if builtinCommandNames[name] {
			return fmt.Errorf("customCommands: имя %q занято встроенной командой", name)
		}
		if seen[name] {
			return fmt.Errorf("customCommands: имя %q повторяется", name)
		}
		seen[name] = true
		if !validActionTypes[c.ActionType] {
			return fmt.Errorf("customCommands: команда %q — недопустимый actionType %q", name, c.ActionType)
		}
		if c.ActionType == settings.ActionBanner && (c.BannerPriority < 0 || c.BannerPriority > 2) {
			return fmt.Errorf("customCommands: команда %q — bannerPriority должен быть 0..2", name)
		}
		if c.ScriptName != "" && !scriptNameRe.MatchString(c.ScriptName) {
			return fmt.Errorf("customCommands: команда %q — недопустимое имя скрипта %q", name, c.ScriptName)
		}
		if c.CooldownMinutes < 0 {
			return fmt.Errorf("customCommands: команда %q — cooldownMinutes не может быть отрицательным", name)
		}
	}
	return nil
}

var validWelcomeActionTypes = map[string]bool{"": true, settings.ActionChat: true, settings.ActionBanner: true}

const maxWelcomeMessages = 5

func validateWelcomeMessage(w settings.WelcomeMessage) error {
	if len(w.Messages) > maxWelcomeMessages {
		return fmt.Errorf("welcomeMessage: максимум %d сообщений, получено %d", maxWelcomeMessages, len(w.Messages))
	}
	for i, m := range w.Messages {
		if !validWelcomeActionTypes[m.ActionType] {
			return fmt.Errorf("welcomeMessage: сообщение %d — недопустимый actionType %q", i+1, m.ActionType)
		}
		if m.ActionType == settings.ActionBanner && (m.BannerPriority < 0 || m.BannerPriority > 2) {
			return fmt.Errorf("welcomeMessage: сообщение %d — bannerPriority должен быть 0..2", i+1)
		}
		if m.DelaySeconds < 0 {
			return fmt.Errorf("welcomeMessage: сообщение %d — delaySeconds не может быть отрицательным", i+1)
		}
	}
	return nil
}

func playerAction(cfg config.Config, build func(player string, body playerActionBody) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body playerActionBody
		if !decodeOrErr(w, r, &body) {
			return
		}
		if body.Player == "" {
			writeErr(w, fmt.Errorf("player обязателен"))
			return
		}
		runConsole(w, cfg, build(body.Player, body), 15*time.Second)
	}
}

func runConsole(w http.ResponseWriter, cfg config.Config, command string, timeout time.Duration) {
	out, err := runConsoleStr(cfg, command, timeout)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"output": filterConsoleNoise(out)})
}

func runConsoleStr(cfg config.Config, command string, timeout time.Duration) (string, error) {
	addr, pwd, err := cfg.TelnetCreds()
	if err != nil {
		return "", err
	}
	return telnet.New(addr, pwd).Command(command, timeout)
}

func filterConsoleNoise(raw string) string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	skipUntilClose := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if skipUntilClose {
			if strings.HasSuffix(trimmed, ")") {
				skipUntilClose = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "Thread '") {
			continue
		}

		if strings.HasPrefix(trimmed, "Command '") && strings.Contains(trimmed, "sent to player with entity id") {
			continue
		}
		if strings.HasPrefix(trimmed, "{:ElbMod}") {
			continue
		}
		if strings.HasPrefix(trimmed, "Telnet Connection closed while writing") {

			if !strings.HasSuffix(trimmed, ")") {
				skipUntilClose = true
			}
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func authMiddleware(cfg config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" || !cfg.Tokens[token] {
			recordUnauthorizedRequest(cfg, r, token)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func recordUnauthorizedRequest(cfg config.Config, r *http.Request, token string) {
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil || !s.Audit.UnauthorizedAPIRequests {
		return
	}
	reason := "invalid token"
	if token == "" {
		reason = "no token"
	}
	summary := fmt.Sprintf("%s %s: %s, %s", r.Method, r.URL.Path, reason, r.RemoteAddr)
	if err := audit.Record(cfg, audit.CategoryUnauthorizedAPIRequests, "", summary, ""); err != nil {
		log.Printf("audit: unauthorized request: %v", err)
	}
}

func queueableModPost(cfg config.Config, mod *modclient.Client, modPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			writeErr(w, err)
			return
		}
		var probe struct {
			SteamId string `json:"steamId"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			writeErr(w, err)
			return
		}
		if probe.SteamId == "" {
			writeErr(w, fmt.Errorf("steamId обязателен"))
			return
		}

		players := mod.CachedPlayers()
		online := false
		for _, p := range players {
			if p.SteamId == probe.SteamId && p.Online {
				online = true
				break
			}
		}

		if online {
			v, err := mod.Post(modPath, json.RawMessage(raw))
			if err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, v)
			return
		}

		if err := playerqueue.Enqueue(cfg, probe.SteamId, modPath, raw); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "queued": true})
	}
}

func queueableStructureModPost(cfg config.Config, mod *modclient.Client, modPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			writeErr(w, err)
			return
		}
		var probe struct {
			Playfield string `json:"playfield"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			writeErr(w, err)
			return
		}
		if probe.Playfield == "" {
			writeErr(w, fmt.Errorf("playfield обязателен"))
			return
		}

		loaded, err := structurequeue.LoadedPlayfields(mod)
		if err != nil {
			writeErr(w, err)
			return
		}

		if loaded[probe.Playfield] {
			v, err := mod.Post(modPath, json.RawMessage(raw))
			if err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, v)
			return
		}

		if err := structurequeue.Enqueue(cfg, probe.Playfield, modPath, raw); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "queued": true})
	}
}

func handlePlayerInventoryPost(cfg config.Config, mod *modclient.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			writeErr(w, err)
			return
		}
		var probe struct {
			SteamId string `json:"steamId"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			writeErr(w, err)
			return
		}
		if probe.SteamId == "" {
			writeErr(w, fmt.Errorf("steamId обязателен"))
			return
		}

		players := mod.CachedPlayers()
		online := false
		for _, p := range players {
			if p.SteamId == probe.SteamId && p.Online {
				online = true
				break
			}
		}

		if online {
			applied, err := playerqueue.ApplyInventoryEdit(cfg, mod, raw)
			if err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, map[string]any{"applied": applied, "queued": false, "conflict": !applied})
			return
		}

		if err := playerqueue.Enqueue(cfg, probe.SteamId, "/players/inventory", raw); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"applied": false, "queued": true, "conflict": false})
	}
}

func proxyModPost(mod *modclient.Client, modPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body any
		if !decodeOrErr(w, r, &body) {
			return
		}
		v, err := mod.Post(modPath, body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, v)
	}
}

func spawnStructureHandler(mod *modclient.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeErr(w, fmt.Errorf("parse multipart form: %w", err))
			return
		}
		file, _, err := r.FormFile("epb")
		if err != nil {
			writeErr(w, fmt.Errorf("epb file required: %w", err))
			return
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			writeErr(w, err)
			return
		}

		body := map[string]any{
			"epbBase64": base64.StdEncoding.EncodeToString(data),
			"type":      r.FormValue("type"),
		}
		if v := r.FormValue("name"); v != "" {
			body["name"] = v
		}
		if v := r.FormValue("playfield"); v != "" {
			body["playfield"] = v
		}
		if v := r.FormValue("ownerEntityId"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				body["ownerEntityId"] = n
			}
		}
		for _, f := range []string{"x", "y", "z", "rotX", "rotY", "rotZ"} {
			if v := r.FormValue(f); v != "" {
				if n, err := strconv.ParseFloat(v, 64); err == nil {
					body[f] = n
				}
			}
		}

		v, err := mod.Post("/structures/spawn", body)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, v)
	}
}

type PlayerOut struct {
	EntityId  int    `json:"entityId"`
	Name      string `json:"name"`
	SteamId   string `json:"steamId"`
	Playfield string `json:"playfield"`
	Online    bool   `json:"online"`

	FactionId    int    `json:"factionId,omitempty"`
	FactionGroup int    `json:"factionGroup,omitempty"`
	FactionTag   string `json:"factionTag,omitempty"`
	FactionName  string `json:"factionName,omitempty"`

	Pos         *modclient.Vec3 `json:"pos,omitempty"`
	Rot         *modclient.Vec3 `json:"rot,omitempty"`
	SolarSystem string          `json:"solarSystem,omitempty"`

	LastOnline    string  `json:"lastOnline,omitempty"`
	OnlineSeconds float64 `json:"onlineSeconds,omitempty"`

	Role        string `json:"role,omitempty"`
	BannedUntil string `json:"bannedUntil,omitempty"`

	MutedUntil string `json:"mutedUntil,omitempty"`

	Live    *modclient.PlayerLiveInfo `json:"live,omitempty"`
	Offline *playerOffline            `json:"offline,omitempty"`
}

type playerOffline struct {
	Credits         *int64   `json:"credits,omitempty"`
	Kills           *int     `json:"kills,omitempty"`
	Died            *int     `json:"died,omitempty"`
	PlaytimeSeconds *float64 `json:"playtimeSeconds,omitempty"`
	Level           *int     `json:"level,omitempty"`
	Exp             *int     `json:"exp,omitempty"`
}

func handlePlayers(cfg config.Config, mod *modclient.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		players := mod.CachedPlayers()

		list := factions.Cached()
		byFactionID := make(map[int]factions.Faction, len(list))
		for _, f := range list {
			byFactionID[f.ID] = f
		}

		presenceBySteamID, err := presence.All(cfg)
		if err != nil {
			log.Printf("api: /api/players: presence.All: %v", err)
		}

		perm := permissions.Cached()

		offlineByEntity := globaldb.CachedOfflinePlayers()

		killsDiedBySteamID := playerstats.CachedKillsDied()

		out := make([]PlayerOut, 0, len(players))
		for _, p := range players {
			po := PlayerOut{
				EntityId:  p.EntityId,
				Name:      p.Name,
				SteamId:   p.SteamId,
				Playfield: p.Playfield,
				Online:    p.Online,
				Role:      perm.Roles[p.SteamId],
			}
			if until, ok := perm.Banned[p.SteamId]; ok {
				po.BannedUntil = until
			}
			if until, ok := permissions.MutedUntil(p.SteamId); ok {
				po.MutedUntil = until.Format(time.RFC3339)
			}

			if pr, ok := presenceBySteamID[p.SteamId]; ok {
				po.OnlineSeconds = pr.OnlineSeconds
				if !pr.LastOnline.IsZero() {
					po.LastOnline = pr.LastOnline.Format(time.RFC3339)
				}
			}

			if p.Info != nil {
				po.Live = p.Info
				pos, rot := p.Info.Pos, p.Info.Rot
				po.Pos, po.Rot = &pos, &rot
				if p.Info.FactionGroup == 0 && p.Info.FactionId > 0 {
					po.FactionGroup, po.FactionId = p.Info.FactionGroup, p.Info.FactionId
				}

				if off, ok := offlineByEntity[p.EntityId]; ok {
					po.SolarSystem = off.SolarSystem
				}
			} else if off, ok := offlineByEntity[p.EntityId]; ok {
				po.Offline = &playerOffline{
					Credits:         off.Credits,
					Kills:           off.Kills,
					Died:            off.Died,
					PlaytimeSeconds: off.Playtime,
					Level:           off.Level,
					Exp:             off.Exp,
				}

				if kd, ok := killsDiedBySteamID[p.SteamId]; ok {
					kills, died := kd.Kills, kd.Died
					po.Offline.Kills = &kills
					po.Offline.Died = &died
				}
				if off.Pos != nil {
					po.Pos = &modclient.Vec3{X: off.Pos.X, Y: off.Pos.Y, Z: off.Pos.Z}
				}
				if off.Rot != nil {
					po.Rot = &modclient.Vec3{X: off.Rot.X, Y: off.Rot.Y, Z: off.Rot.Z}
				}
				po.SolarSystem = off.SolarSystem
				if off.Playfield != "" {
					po.Playfield = off.Playfield
				}
				if off.FactionGroup != nil && *off.FactionGroup == 0 && off.FactionID != nil && *off.FactionID > 0 {
					po.FactionGroup, po.FactionId = *off.FactionGroup, *off.FactionID
				}
			}

			if po.FactionId > 0 {
				if f, ok := byFactionID[po.FactionId]; ok {
					po.FactionTag, po.FactionName = f.Abbreviation, f.Name
				}
			}

			out = append(out, po)
		}
		writeJSON(w, out)
	}
}

func secondaryDestination(cfg config.Config) (settings.BackupDestination, error) {
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		return settings.BackupDestination{}, err
	}
	d := s.Backup.Destinations[1]
	if d.Type == "" {
		return settings.BackupDestination{}, fmt.Errorf("доп. хранилище (dest=1) не настроено")
	}
	return d, nil
}

func handleManualAction(w http.ResponseWriter, cfg config.Config, actionType string) {
	warnMinutes := 0
	if s, err := settings.Load(cfg.SettingsFile); err == nil {
		warnMinutes = s.Maintenance.ManualActionWarnMinutes
	}
	if !serverctl.TryBeginManualAction(actionType, warnMinutes) {
		http.Error(w, "операция уже выполняется", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	safego.Go("api.handleManualAction", func() {
		defer serverctl.EndManualAction()
		scheduler.WarnBeforeStop(cfg, warnMinutes)
		var err error
		if actionType == "restart" {
			err = serverctl.Restart(cfg.SystemdUnit)
		} else {
			err = serverctl.Stop(cfg.SystemdUnit)
		}
		if err != nil {
			log.Printf("manual %s: %v", actionType, err)
		}

		watchdog.Check(cfg)
	})
}

func proxyGet(fn func() (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := fn()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, v)
	}
}

func runOrErr(w http.ResponseWriter, fn func() error) {
	if err := fn(); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func decodeOrErr(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, err)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeErr(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}
