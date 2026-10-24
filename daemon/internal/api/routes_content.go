package api

import (
	"fmt"
	"io"
	"net/http"
	"strconv"

	"elb/daemon/internal/config"
	"elb/daemon/internal/gamefiles"
	"elb/daemon/internal/gameoptions"
	"elb/daemon/internal/logbuf"
	"elb/daemon/internal/modclient"
)

func registerContentRoutes(mux *http.ServeMux, cfg config.Config, mod *modclient.Client, logBuf *logbuf.Buffer) {

	mux.HandleFunc("GET /api/files/logs", func(w http.ResponseWriter, r *http.Request) {
		v, err := gamefiles.ListLogs(cfg)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, v)
	})
	mux.HandleFunc("GET /api/files/logs/{name...}", func(w http.ResponseWriter, r *http.Request) {
		maxBytes := int64(0)
		if v := r.URL.Query().Get("maxBytes"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				maxBytes = n
			}
		}
		v, err := gamefiles.ReadLog(cfg, r.PathValue("name"), maxBytes)
		if err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, v)
	})

	mux.HandleFunc("GET /api/files/config", func(w http.ResponseWriter, r *http.Request) {
		files, err := gamefiles.ListScenarioConfigFiles(cfg)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, files)
	})
	mux.HandleFunc("GET /api/files/config/{name...}", func(w http.ResponseWriter, r *http.Request) {
		v, err := gamefiles.ReadConfigFile(cfg, r.PathValue("name"))
		if err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, v)
	})
	mux.HandleFunc("POST /api/files/config/{name...}", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeErr(w, err)
			return
		}
		runOrErr(w, func() error { return gamefiles.WriteConfigFile(cfg, r.PathValue("name"), string(body)) })
	})

	mux.HandleFunc("GET /api/mods", func(w http.ResponseWriter, r *http.Request) {
		v, err := gamefiles.ListMods(cfg)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, v)
	})
	mux.HandleFunc("POST /api/mods", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			writeErr(w, fmt.Errorf("parse multipart form: %w", err))
			return
		}
		file, header, err := r.FormFile("zip")
		if err != nil {
			writeErr(w, fmt.Errorf("файл 'zip' обязателен: %w", err))
			return
		}
		defer file.Close()
		name, err := gamefiles.InstallModZip(cfg, file, header.Size)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "name": name})
	})
	mux.HandleFunc("DELETE /api/mods/{name...}", func(w http.ResponseWriter, r *http.Request) {
		runOrErr(w, func() error { return gamefiles.RemoveMod(cfg, r.PathValue("name")) })
	})

	mux.HandleFunc("GET /api/scenarios", func(w http.ResponseWriter, r *http.Request) {
		v, err := gamefiles.ListScenarios(cfg)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, v)
	})
	mux.HandleFunc("POST /api/scenario", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		if !decodeOrErr(w, r, &body) {
			return
		}
		if err := gamefiles.SelectScenario(cfg, body.Name); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "note": "применится только после перезапуска сервера"})
	})

	mux.HandleFunc("GET /api/saves", func(w http.ResponseWriter, r *http.Request) {
		v, err := gamefiles.ListSaves(cfg)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, v)
	})

	mux.HandleFunc("GET /api/gameoptions", proxyGet(func() (any, error) { return gameoptions.Read(cfg) }))
	mux.HandleFunc("POST /api/gameoptions", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if !decodeOrErr(w, r, &body) {
			return
		}
		values := make(map[string]string, len(body))
		for k, v := range body {
			if v != "" {
				values[k] = v
			}
		}
		runOrErr(w, func() error { return gameoptions.SetFields(cfg, values) })
	})
}
