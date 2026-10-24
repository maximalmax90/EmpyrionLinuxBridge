package api

import (
	"net/http"

	"elb/daemon/internal/config"
	"elb/daemon/internal/itemlocalization"
	"elb/daemon/internal/itemmap"
	"elb/daemon/internal/itemstacks"
	"elb/daemon/internal/logbuf"
	"elb/daemon/internal/modclient"
)

func registerItemRoutes(mux *http.ServeMux, cfg config.Config, mod *modclient.Client, logBuf *logbuf.Buffer) {

	mux.HandleFunc("GET /api/items", func(w http.ResponseWriter, r *http.Request) {
		if v, err := mod.Get("/items"); err == nil {
			writeJSON(w, v)
			return
		}
		path, err := cfg.BlocksmapPath()
		if err != nil {
			writeErr(w, err)
			return
		}
		items, err := itemmap.Read(path)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, items)
	})

	mux.HandleFunc("GET /api/items/stacksize", func(w http.ResponseWriter, r *http.Request) {
		m, err := itemstacks.All(cfg)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, m)
	})

	mux.HandleFunc("GET /api/items/categories", func(w http.ResponseWriter, r *http.Request) {
		m, err := itemstacks.Categories(cfg)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, m)
	})

	mux.HandleFunc("GET /api/items/ammotype", func(w http.ResponseWriter, r *http.Request) {
		m, err := itemstacks.AmmoTypes(cfg)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, m)
	})

	mux.HandleFunc("GET /api/items/displayname", func(w http.ResponseWriter, r *http.Request) {
		m, err := itemlocalization.DisplayNames(cfg, r.URL.Query().Get("lang"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, m)
	})

	mux.HandleFunc("GET /api/items/description", func(w http.ResponseWriter, r *http.Request) {
		m, err := itemlocalization.Descriptions(cfg, r.URL.Query().Get("lang"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, m)
	})
}
