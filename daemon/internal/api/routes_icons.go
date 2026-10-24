package api

import (
	"net/http"

	"elb/daemon/internal/config"
	"elb/daemon/internal/icons"
	"elb/daemon/internal/logbuf"
	"elb/daemon/internal/modclient"
)

func registerIconRoutes(mux *http.ServeMux, cfg config.Config, mod *modclient.Client, logBuf *logbuf.Buffer) {

	mux.HandleFunc("GET /api/icons", func(w http.ResponseWriter, r *http.Request) {
		scenario, err := icons.CurrentScenario(cfg)
		if err != nil {
			writeErr(w, err)
			return
		}
		v, err := icons.List(cfg)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"scenario": scenario, "icons": v})
	})
	mux.HandleFunc("GET /api/icons/{name...}", func(w http.ResponseWriter, r *http.Request) {
		data, err := icons.Read(cfg, r.PathValue("name"))
		if err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(data)
	})
}
