package api

import (
	"fmt"
	"net/http"
	"strconv"

	"elb/daemon/internal/audit"
	"elb/daemon/internal/config"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/settings"

	"elb/daemon/internal/logbuf"
)

func registerAuditRoutes(mux *http.ServeMux, cfg config.Config, mod *modclient.Client, logBuf *logbuf.Buffer) {
	mux.HandleFunc("GET /api/audit/events", func(w http.ResponseWriter, r *http.Request) {
		limit := 200
		if v := r.URL.Query().Get("limit"); v != "" {
			if p, err := strconv.Atoi(v); err == nil && p > 0 {
				limit = p
			}
		}
		from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
		var v []audit.Event
		var err error
		if from != "" || to != "" {
			v, err = audit.Between(cfg, from, to, limit)
		} else {
			v, err = audit.Recent(cfg, limit)
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, v)
	})

	mux.HandleFunc("GET /api/audit/enabled", func(w http.ResponseWriter, r *http.Request) {
		s, err := settings.Load(cfg.SettingsFile)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"enabled": s.Audit.Enabled()})
	})

	mux.HandleFunc("POST /api/audit/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			User       string `json:"user"`
			Success    bool   `json:"success"`
			RemoteAddr string `json:"remoteAddr"`
		}
		if !decodeOrErr(w, r, &body) {
			return
		}
		s, err := settings.Load(cfg.SettingsFile)
		if err != nil {
			writeErr(w, err)
			return
		}
		if !s.Audit.LoginTracking {
			writeJSON(w, map[string]bool{"ok": true})
			return
		}
		result := "OK"
		if !body.Success {
			result = "FAILED"
		}
		summary := fmt.Sprintf("panel login %s %q from %s", result, body.User, body.RemoteAddr)
		if err := audit.Record(cfg, audit.CategoryLoginTracking, body.User, summary, ""); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
}
