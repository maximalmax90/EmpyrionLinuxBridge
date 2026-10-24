package api

import (
	"fmt"
	"log"
	"net/http"

	"elb/daemon/internal/config"
	"elb/daemon/internal/logbuf"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/settings"
	"elb/daemon/internal/steamjob"
)

func registerSteamRoutes(mux *http.ServeMux, cfg config.Config, mod *modclient.Client, logBuf *logbuf.Buffer) {
	mux.HandleFunc("GET /api/steam/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, steamjob.Current())
	})

	mux.HandleFunc("POST /api/steam/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if !decodeOrErr(w, r, &body) {
			return
		}
		if body.Username == "" || body.Password == "" {
			http.Error(w, "укажите логин и пароль", http.StatusBadRequest)
			return
		}
		if !steamjob.Login(cfg, body.Username, body.Password) {
			http.Error(w, "steamcmd уже выполняет другую задачу", http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("POST /api/steam/login/code", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Code string `json:"code"`
		}
		if !decodeOrErr(w, r, &body) {
			return
		}
		if !steamjob.SubmitCode(body.Code) {
			http.Error(w, "сейчас код никто не ждёт", http.StatusConflict)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})

	mux.HandleFunc("POST /api/steam/logout", func(w http.ResponseWriter, r *http.Request) {
		if err := steamjob.Logout(cfg); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})

	mux.HandleFunc("GET /api/steam/workshop/lookup", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "укажите id", http.StatusBadRequest)
			return
		}
		info, err := steamjob.LookupWorkshopItem(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, info)
	})
	mux.HandleFunc("POST /api/steam/workshop/install", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ID string `json:"id"`
		}
		if !decodeOrErr(w, r, &body) {
			return
		}
		if body.ID == "" {
			http.Error(w, "укажите id", http.StatusBadRequest)
			return
		}
		ok, err := steamjob.InstallWorkshopItem(cfg, body.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !ok {
			http.Error(w, "steamcmd уже выполняет другую задачу", http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})

	mux.HandleFunc("GET /api/steam/workshop/installed", func(w http.ResponseWriter, r *http.Request) {
		items, err := steamjob.ListInstalledItems(cfg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		type installedItemView struct {
			settings.InstalledWorkshopItem
			TargetPath string `json:"targetPath"`
			SizeBytes  int64  `json:"sizeBytes"`
		}
		views := make([]installedItemView, len(items))
		for i, it := range items {
			size, err := steamjob.ItemDiskUsage(cfg, it.Kind, it.TargetDir)
			if err != nil {
				log.Printf("steam workshop installed: size %s: %v", it.TargetDir, err)
			}
			views[i] = installedItemView{
				InstalledWorkshopItem: it,
				TargetPath:            steamjob.ItemTargetPath(cfg, it.Kind, it.TargetDir),
				SizeBytes:             size,
			}
		}
		writeJSON(w, views)
	})
	mux.HandleFunc("POST /api/steam/workshop/check", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ID string `json:"id"`
		}
		if !decodeOrErr(w, r, &body) {
			return
		}
		status, err := steamjob.CheckInstalledItemUpdate(cfg, body.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, status)
	})
	mux.HandleFunc("POST /api/steam/workshop/remove", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ID string `json:"id"`
		}
		if !decodeOrErr(w, r, &body) {
			return
		}
		if err := steamjob.RemoveInstalledItem(cfg, body.ID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})

	mux.HandleFunc("GET /api/steam/workshop/download", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id == "" {
			writeErr(w, fmt.Errorf("id обязателен"))
			return
		}
		baseDir, targetDir, filename, err := steamjob.ResolveInstalledItemArchive(cfg, id)
		if err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
		if err := steamjob.StreamItemArchive(baseDir, targetDir, w); err != nil {
			log.Printf("steam workshop download %s: %v", id, err)
		}
	})
}
