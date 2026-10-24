package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"elb/daemon/internal/audit"
	"elb/daemon/internal/backpack"
	"elb/daemon/internal/config"
	"elb/daemon/internal/logbuf"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/permissions"
	"elb/daemon/internal/playerqueue"
	"elb/daemon/internal/playerstats"
	"elb/daemon/internal/settings"
)

func isProtectedAdminPlayer(cfg config.Config, steamID string) bool {
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		log.Printf("isProtectedAdminPlayer: settings: %v", err)
		return false
	}
	for _, id := range s.Chatbot.AdminSteamIds {
		if id == steamID {
			return true
		}
	}
	return false
}

func isProtectedModeratorPlayer(cfg config.Config, steamID string) bool {
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		log.Printf("isProtectedModeratorPlayer: settings: %v", err)
		return false
	}
	for _, id := range s.Chatbot.ModeratorSteamIds {
		if id == steamID {
			return true
		}
	}
	return false
}

func registerPlayerRoutes(mux *http.ServeMux, cfg config.Config, mod *modclient.Client, logBuf *logbuf.Buffer) {

	mux.HandleFunc("GET /api/players", handlePlayers(cfg, mod))
	mux.HandleFunc("GET /api/players/inventory", func(w http.ResponseWriter, r *http.Request) {
		v, err := mod.Get("/players/inventory?" + r.URL.RawQuery)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, v)
	})

	mux.HandleFunc("POST /api/players/teleport", queueableModPost(cfg, mod, "/players/teleport"))
	mux.HandleFunc("POST /api/players/additem", proxyModPost(mod, "/players/additem"))
	mux.HandleFunc("POST /api/players/removeitem", proxyModPost(mod, "/players/removeitem"))

	mux.HandleFunc("POST /api/players/factory/finish", proxyModPost(mod, "/players/factory/finish"))

	mux.HandleFunc("POST /api/players/setinfo", queueableModPost(cfg, mod, "/players/setinfo"))
	mux.HandleFunc("POST /api/players/addcredits", queueableModPost(cfg, mod, "/players/addcredits"))

	mux.HandleFunc("POST /api/players/inventory", handlePlayerInventoryPost(cfg, mod))

	mux.HandleFunc("GET /api/players/pending", func(w http.ResponseWriter, r *http.Request) {
		steamID := r.URL.Query().Get("steamId")
		if steamID == "" {
			writeErr(w, fmt.Errorf("нужен ?steamId="))
			return
		}
		m, err := playerqueue.AllPending(cfg, steamID)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, m)
	})

	mux.HandleFunc("GET /api/players/stats", func(w http.ResponseWriter, r *http.Request) {
		steamID := r.URL.Query().Get("steamId")
		if steamID == "" {
			writeErr(w, fmt.Errorf("нужен ?steamId="))
			return
		}
		s, found, err := playerstats.Get(cfg, steamID)
		if err != nil {
			writeErr(w, err)
			return
		}
		if !found {
			writeJSON(w, map[string]any{"found": false})
			return
		}
		writeJSON(w, map[string]any{
			"found":                true,
			"health":               s.Health,
			"healthMax":            s.HealthMax,
			"food":                 s.Food,
			"foodMax":              s.FoodMax,
			"oxygen":               s.Oxygen,
			"oxygenMax":            s.OxygenMax,
			"stamina":              s.Stamina,
			"staminaMax":           s.StaminaMax,
			"radiation":            s.Radiation,
			"radiationMax":         s.RadiationMax,
			"bodyTemp":             s.BodyTemp,
			"bodyTempMax":          s.BodyTempMax,
			"factionGroup":         s.FactionGroup,
			"factionId":            s.FactionId,
			"factionRole":          s.FactionRole,
			"credits":              s.Credits,
			"exp":                  s.Exp,
			"upgrade":              s.Upgrade,
			"bpRemainingTime":      s.BpRemainingTime,
			"bpInFactory":          s.BpInFactory,
			"bpResourcesInFactory": s.BpResourcesInFactory,
			"updatedAt":            s.UpdatedAt,
		})
	})

	mux.HandleFunc("GET /api/players/backpack-history", func(w http.ResponseWriter, r *http.Request) {
		steamID := r.URL.Query().Get("steamId")
		if steamID == "" {
			writeErr(w, fmt.Errorf("нужен ?steamId="))
			return
		}
		entries, err := backpack.History(cfg, steamID)
		if err != nil {
			writeErr(w, err)
			return
		}
		if entries == nil {
			entries = []backpack.Entry{}
		}
		writeJSON(w, entries)
	})

	mux.HandleFunc("POST /api/players/backpack-restore", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SteamID   string    `json:"steamId"`
			PlayerId  int       `json:"playerId"`
			Timestamp time.Time `json:"timestamp"`

			Part string `json:"part"`
		}
		if !decodeOrErr(w, r, &body) {
			return
		}
		if body.SteamID == "" || body.PlayerId == 0 {
			writeErr(w, fmt.Errorf("нужны steamId и playerId"))
			return
		}
		entry, err := backpack.FindEntry(cfg, body.SteamID, body.Timestamp)
		if err != nil {
			writeErr(w, err)
			return
		}

		editPayload := modclient.InventoryEditPayload{
			PlayerId: body.PlayerId,
			SteamId:  body.SteamID,
			Part:     body.Part,
		}
		switch body.Part {
		case "toolbelt":
			editPayload.Toolbelt = entry.Toolbelt
		case "bag":
			editPayload.Bag = entry.Bag
		default:
			editPayload.Toolbelt, editPayload.Bag = entry.Toolbelt, entry.Bag
		}
		payload, err := json.Marshal(editPayload)
		if err != nil {
			writeErr(w, err)
			return
		}
		if err := playerqueue.Enqueue(cfg, body.SteamID, "/players/inventory", payload); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"queued": true})
	})

	mux.HandleFunc("POST /api/players/kick", playerAction(cfg, func(p string, args playerActionBody) string {
		if args.Message != "" {
			return fmt.Sprintf("kick %s %s", p, args.Message)
		}
		return "kick " + p
	}))

	mux.HandleFunc("POST /api/players/ban", func(w http.ResponseWriter, r *http.Request) {
		var body playerActionBody
		if !decodeOrErr(w, r, &body) {
			return
		}
		if body.Player == "" {
			writeErr(w, fmt.Errorf("player обязателен"))
			return
		}
		out, err := runConsoleStr(cfg, fmt.Sprintf("ban %s %s", body.Player, body.Duration), 15*time.Second)
		if err != nil {
			writeErr(w, err)
			return
		}
		if _, rerr := permissions.Refresh(cfg); rerr != nil {
			log.Printf("api: /api/players/ban: refresh ban cache: %v", rerr)
		}
		writeJSON(w, map[string]string{"output": filterConsoleNoise(out)})
	})

	mux.HandleFunc("POST /api/players/mute", func(w http.ResponseWriter, r *http.Request) {
		var body playerActionBody
		if !decodeOrErr(w, r, &body) {
			return
		}
		if body.Player == "" {
			writeErr(w, fmt.Errorf("player обязателен"))
			return
		}
		until, err := permissions.DurationFromEngineString(body.Duration)
		if err != nil {
			writeErr(w, err)
			return
		}
		out, err := runConsoleStr(cfg, fmt.Sprintf("mute %s %s", body.Player, body.Duration), 15*time.Second)
		if err != nil {
			writeErr(w, err)
			return
		}
		if err := permissions.SetMuted(body.Player, time.Now().Add(until)); err != nil {
			log.Printf("api: /api/players/mute: save local status: %v", err)
		}
		writeJSON(w, map[string]string{"output": filterConsoleNoise(out)})
	})
	mux.HandleFunc("POST /api/players/unban", func(w http.ResponseWriter, r *http.Request) {
		var body playerActionBody
		if !decodeOrErr(w, r, &body) {
			return
		}
		if body.Player == "" {
			writeErr(w, fmt.Errorf("player обязателен"))
			return
		}
		out, err := runConsoleStr(cfg, "unban "+body.Player, 15*time.Second)
		if err != nil {
			writeErr(w, err)
			return
		}
		if _, rerr := permissions.Refresh(cfg); rerr != nil {
			log.Printf("api: /api/players/unban: refresh ban cache: %v", rerr)
		}
		writeJSON(w, map[string]string{"output": filterConsoleNoise(out)})
	})

	mux.HandleFunc("POST /api/players/unmute", func(w http.ResponseWriter, r *http.Request) {
		var body playerActionBody
		if !decodeOrErr(w, r, &body) {
			return
		}
		if body.Player == "" {
			writeErr(w, fmt.Errorf("player обязателен"))
			return
		}
		out, err := runConsoleStr(cfg, fmt.Sprintf("mute %s 0h", body.Player), 15*time.Second)
		if err != nil {
			writeErr(w, err)
			return
		}
		if err := permissions.ClearMuted(body.Player); err != nil {
			log.Printf("api: /api/players/unmute: clear local status: %v", err)
		}
		writeJSON(w, map[string]string{"output": filterConsoleNoise(out)})
	})

	mux.HandleFunc("POST /api/players/setrole", func(w http.ResponseWriter, r *http.Request) {
		var body playerActionBody
		if !decodeOrErr(w, r, &body) {
			return
		}
		if body.Player == "" {
			writeErr(w, fmt.Errorf("player обязателен"))
			return
		}

		if isProtectedAdminPlayer(cfg, body.Player) && body.Role != "admin" {
			writeErr(w, fmt.Errorf("роль игрока-админа управляется списком \"Игроки-админы\", не вручную"))
			return
		}

		if isProtectedModeratorPlayer(cfg, body.Player) && body.Role != "mod" {
			writeErr(w, fmt.Errorf("роль игрока-модератора управляется списком \"Игроки-модераторы\", не вручную"))
			return
		}
		out, err := runConsoleStr(cfg, fmt.Sprintf("setrole %s %s", body.Player, body.Role), 15*time.Second)
		if err != nil {
			writeErr(w, err)
			return
		}
		if _, rerr := permissions.Refresh(cfg); rerr != nil {
			log.Printf("api: /api/players/setrole: refresh role cache: %v", rerr)
		}
		recordRoleChangeAudit(cfg, body.Player, body.Role)
		writeJSON(w, map[string]string{"output": filterConsoleNoise(out)})
	})

	mux.HandleFunc("POST /api/players/remoteex", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SteamId string `json:"steamId"`
			Command string `json:"command"`
		}
		if !decodeOrErr(w, r, &body) {
			return
		}
		if body.SteamId == "" || body.Command == "" {
			writeErr(w, fmt.Errorf("steamId и command обязательны"))
			return
		}
		var clientId int
		found := false
		for _, p := range mod.CachedPlayers() {
			if p.SteamId == body.SteamId && p.Online && p.Info != nil {
				clientId = p.Info.ClientId
				found = true
				break
			}
		}
		if !found {
			writeErr(w, fmt.Errorf("игрок не в сети — команда применяется только к игроку, подключённому прямо сейчас"))
			return
		}
		cmd := fmt.Sprintf("remoteex cl=%d '%s'", clientId, strings.ReplaceAll(body.Command, "'", "\\'"))
		runConsole(w, cfg, cmd, 15*time.Second)
	})

	mux.HandleFunc("GET /api/players/banned", func(w http.ResponseWriter, r *http.Request) {
		runConsole(w, cfg, "list banned", 15*time.Second)
	})
	mux.HandleFunc("GET /api/players/permissions", func(w http.ResponseWriter, r *http.Request) {
		runConsole(w, cfg, "list permissions", 15*time.Second)
	})
}

func recordRoleChangeAudit(cfg config.Config, player, role string) {
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil || !s.Audit.PermissionFactionChange {
		return
	}
	summary := fmt.Sprintf("player role %s changed to %q", player, role)
	if err := audit.Record(cfg, audit.CategoryPermissionFactionChange, player, summary, ""); err != nil {
		log.Printf("audit: role change: %v", err)
	}
}
