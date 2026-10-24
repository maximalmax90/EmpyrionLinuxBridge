package api

import (
	"net/http"

	"elb/daemon/internal/config"
	"elb/daemon/internal/logbuf"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/playfieldwipe"
	"elb/daemon/internal/structuresdb"
	"elb/daemon/internal/watchdog"
)

func registerPlayfieldRoutes(mux *http.ServeMux, cfg config.Config, mod *modclient.Client, logBuf *logbuf.Buffer) {

	mux.HandleFunc("GET /api/playfields", func(w http.ResponseWriter, r *http.Request) {
		if !watchdog.CachedHealth().Ready() {
			writeJSON(w, []any{})
			return
		}
		v, err := mod.Get("/playfields")
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, v)
	})
	mux.HandleFunc("GET /api/playfield-servers", func(w http.ResponseWriter, r *http.Request) {
		if !watchdog.CachedHealth().Ready() {
			writeJSON(w, map[string]any{})
			return
		}
		v, err := mod.Get("/playfield-servers")
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, v)
	})

	mux.HandleFunc("GET /api/playfields/summary", func(w http.ResponseWriter, r *http.Request) {

		if !watchdog.CachedHealth().Ready() {
			writeJSON(w, map[string]int{"active": 0, "total": 0})
			return
		}
		all, err := mod.Get("/playfields")
		if err != nil {
			writeErr(w, err)
			return
		}
		servers, err := mod.Get("/playfield-servers")
		if err != nil {
			writeErr(w, err)
			return
		}
		total := 0
		if list, ok := all.([]any); ok {
			total = len(list)
		}
		active := 0
		if byPid, ok := servers.(map[string]any); ok {
			for _, names := range byPid {
				if list, ok := names.([]any); ok {
					active += len(list)
				}
			}
		}
		writeJSON(w, map[string]int{"active": active, "total": total})
	})

	mux.HandleFunc("GET /api/playfields/wipeable", proxyGet(func() (any, error) { return playfieldwipe.List(cfg) }))

	mux.HandleFunc("GET /api/playfields/systems", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, structuresdb.CachedPlayfields())
	})
}
