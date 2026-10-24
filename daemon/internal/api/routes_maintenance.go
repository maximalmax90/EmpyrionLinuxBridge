package api

import (
	"fmt"
	"net/http"
	"strings"

	"elb/daemon/internal/config"
	"elb/daemon/internal/globaldb"
	"elb/daemon/internal/logbuf"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/serverctl"
)

func registerMaintenanceRoutes(mux *http.ServeMux, cfg config.Config, mod *modclient.Client, logBuf *logbuf.Buffer) {

	mux.HandleFunc("POST /api/maintenance/wipe-local-db", func(w http.ResponseWriter, r *http.Request) {
		if !serverctl.IsStopped(cfg) {
			writeErr(w, fmt.Errorf("сервер запущен — остановите его перед очисткой"))
			return
		}
		var body struct {
			Stores []string `json:"stores"`
		}
		if !decodeOrErr(w, r, &body) {
			return
		}
		if len(body.Stores) == 0 {
			writeErr(w, fmt.Errorf("не выбрано ни одной базы"))
			return
		}
		var errs []string
		for _, name := range body.Stores {
			fn, ok := maintenanceWipeStores[name]
			if !ok {
				errs = append(errs, fmt.Sprintf("%s: неизвестная база", name))
				continue
			}
			if err := fn(cfg); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			}
		}
		if len(errs) > 0 {
			writeErr(w, fmt.Errorf("%s", strings.Join(errs, "; ")))
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("GET /api/maintenance/duplicate-players", func(w http.ResponseWriter, r *http.Request) {
		groups, err := globaldb.FindDuplicatePlayers(cfg)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, groups)
	})

	mux.HandleFunc("POST /api/maintenance/duplicate-players/cleanup", func(w http.ResponseWriter, r *http.Request) {
		if !serverctl.IsStopped(cfg) {
			writeErr(w, fmt.Errorf("сервер запущен — остановите его перед очисткой"))
			return
		}
		var groups []globaldb.DuplicateGroup
		if !decodeOrErr(w, r, &groups) {
			return
		}
		removed, err := globaldb.CleanupDuplicatePlayers(cfg, groups)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"removed": removed})
	})
}
