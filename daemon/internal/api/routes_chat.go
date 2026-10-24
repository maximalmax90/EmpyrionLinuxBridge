package api

import (
	"log"
	"net/http"
	"strconv"

	"elb/daemon/internal/chatlog"
	"elb/daemon/internal/config"
	"elb/daemon/internal/logbuf"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/settings"
)

func registerChatRoutes(mux *http.ServeMux, cfg config.Config, mod *modclient.Client, logBuf *logbuf.Buffer) {

	mux.HandleFunc("GET /api/chat", func(w http.ResponseWriter, r *http.Request) {
		entries, err := chatlog.Last24Hours(cfg)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, entries)
	})

	mux.HandleFunc("POST /api/chat", proxyModPost(mod, "/chat"))

	mux.HandleFunc("POST /api/chat/history", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			From    string `json:"from"`
			To      string `json:"to"`
			SteamId string `json:"steamId"`
			Sender  string `json:"sender"`
			Channel string `json:"channel"`
			Target  string `json:"target"`
			Limit   int    `json:"limit"`
			Offset  int    `json:"offset"`
		}
		if !decodeOrErr(w, r, &body) {
			return
		}
		result, err := chatlog.Query(cfg, chatlog.QueryFilter{
			FromTs: body.From, ToTs: body.To,
			SteamId: body.SteamId, Sender: body.Sender,
			Channel: body.Channel, Target: body.Target,
			Limit: body.Limit, Offset: body.Offset,
		})
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, result)
	})

	mux.HandleFunc("POST /api/message/all", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text     string  `json:"text"`
			Prio     int     `json:"prio"`
			Duration float64 `json:"duration"`
		}
		if !decodeOrErr(w, r, &body) {
			return
		}
		v, err := mod.Post("/message/all", map[string]any{"text": body.Text, "prio": body.Prio, "duration": body.Duration})
		if err != nil {
			writeErr(w, err)
			return
		}
		if err := chatlog.LogBanner(cfg, bannerSenderName(cfg), body.Text, "", chatlog.PriorityName(body.Prio)); err != nil {
			log.Printf("chatlog: log banner (all): %v", err)
		}
		writeJSON(w, v)
	})
	mux.HandleFunc("POST /api/message/player", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			PlayerId int     `json:"playerId"`
			Text     string  `json:"text"`
			Prio     int     `json:"prio"`
			Duration float64 `json:"duration"`
		}
		if !decodeOrErr(w, r, &body) {
			return
		}
		v, err := mod.Post("/message/player", map[string]any{"playerId": body.PlayerId, "text": body.Text, "prio": body.Prio, "duration": body.Duration})
		if err != nil {
			writeErr(w, err)
			return
		}

		if err := chatlog.LogBanner(cfg, bannerSenderName(cfg), body.Text, strconv.Itoa(body.PlayerId), chatlog.PriorityName(body.Prio)); err != nil {
			log.Printf("chatlog: log banner (player): %v", err)
		}
		writeJSON(w, v)
	})
}

func bannerSenderName(cfg config.Config) string {
	if s, err := settings.Load(cfg.SettingsFile); err == nil && s.Chatbot.ServerSenderName != "" {
		return s.Chatbot.ServerSenderName
	}
	return "Сервер"
}
