package api

import (
	"net/http"

	"elb/daemon/internal/config"
	"elb/daemon/internal/logbuf"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/pdachapters"
)

func registerPdaRoutes(mux *http.ServeMux, cfg config.Config, mod *modclient.Client, logBuf *logbuf.Buffer) {
	mux.HandleFunc("GET /api/pda/chapters", func(w http.ResponseWriter, r *http.Request) {
		list, err := pdachapters.List(cfg, r.URL.Query().Get("lang"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, list)
	})
}
