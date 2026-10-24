package api

import (
	"fmt"
	"net/http"
	"path/filepath"

	"elb/daemon/internal/config"
	"elb/daemon/internal/logbuf"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/remotestore"
	"elb/daemon/internal/scenariocache"
	"elb/daemon/internal/settings"
)

func registerScenarioCacheRoutes(mux *http.ServeMux, cfg config.Config, mod *modclient.Client, logBuf *logbuf.Buffer) {

	mux.HandleFunc("POST /api/scenario-cache/test", func(w http.ResponseWriter, r *http.Request) {
		var set settings.ScenarioCache
		if !decodeOrErr(w, r, &set) {
			return
		}
		store, err := remotestore.New(settings.BackupDestination{
			Type:        "ftp",
			FTPProtocol: set.FTPProtocol,
			FTPHost:     set.FTPHost,
			FTPPort:     set.FTPPort,
			FTPUser:     set.FTPUser,
			FTPPassword: set.FTPPassword,
			FTPKey:      set.FTPKey,
			FTPPath:     set.FTPPath,
		})
		if err != nil {
			writeErr(w, err)
			return
		}
		if err := store.Test(); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("GET /api/scenario-cache/file", func(w http.ResponseWriter, r *http.Request) {
		destZip := filepath.Join(cfg.ScenarioCacheDir, scenariocache.ZipName)
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, scenariocache.ZipName))
		w.Header().Set("Content-Type", "application/zip")
		http.ServeFile(w, r, destZip)
	})

	mux.HandleFunc("GET /api/scenario-cache/info", func(w http.ResponseWriter, r *http.Request) {
		set, err := settings.Load(cfg.SettingsFile)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, scenariocache.ReadInfo(cfg, set.ScenarioCache))
	})
}
