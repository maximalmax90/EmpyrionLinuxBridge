package api

import (
	"net/http"
	"strconv"

	"elb/daemon/internal/config"
	"elb/daemon/internal/logbuf"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/notify"
)

func registerNotificationRoutes(mux *http.ServeMux, cfg config.Config, mod *modclient.Client, logBuf *logbuf.Buffer) {

	mux.HandleFunc("GET /api/notifications", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		list, err := notify.List(cfg, limit)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, list)
	})
	mux.HandleFunc("GET /api/notifications/unread-count", func(w http.ResponseWriter, r *http.Request) {
		n, err := notify.UnreadCount(cfg)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]int{"count": n})
	})
	mux.HandleFunc("POST /api/notifications/read", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ID int64 `json:"id"`
		}
		if !decodeOrErr(w, r, &body) {
			return
		}
		runOrErr(w, func() error { return notify.MarkRead(cfg, body.ID) })
	})
	mux.HandleFunc("POST /api/notifications/read-all", func(w http.ResponseWriter, r *http.Request) {
		runOrErr(w, func() error { return notify.MarkAllRead(cfg) })
	})
	mux.HandleFunc("POST /api/notifications/clear", func(w http.ResponseWriter, r *http.Request) {
		runOrErr(w, func() error { return notify.ClearAll(cfg) })
	})
}
