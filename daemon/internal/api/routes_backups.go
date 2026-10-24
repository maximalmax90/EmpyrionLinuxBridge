package api

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"elb/daemon/internal/config"
	"elb/daemon/internal/logbuf"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/remotestore"
	"elb/daemon/internal/serverctl"
	"elb/daemon/internal/settings"
	"elb/daemon/internal/snapshot"
)

func registerBackupRoutes(mux *http.ServeMux, cfg config.Config, mod *modclient.Client, logBuf *logbuf.Buffer) {

	mux.HandleFunc("POST /api/snapshots", func(w http.ResponseWriter, r *http.Request) {
		s, err := settings.Load(cfg.SettingsFile)
		if err != nil {
			writeErr(w, err)
			return
		}

		info, err := snapshot.CreateAndRetain(cfg, s.Backup.Destinations[0])
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, info)
	})

	mux.HandleFunc("GET /api/snapshots", proxyGet(func() (any, error) {
		infos, err := snapshot.List(cfg)
		if err != nil {
			return nil, err
		}
		for i := range infos {
			infos[i].SizeBytes = snapshot.CachedSizeBytes(infos[i].Name)
		}
		return infos, nil
	}))

	mux.HandleFunc("POST /api/backups/run", func(w http.ResponseWriter, r *http.Request) {
		if !serverctl.IsStopped(cfg) {
			writeErr(w, fmt.Errorf("сервер запущен — остановите его перед бэкапом"))
			return
		}
		s, err := settings.Load(cfg.SettingsFile)
		if err != nil {
			writeErr(w, err)
			return
		}
		info, err := snapshot.CreateAndRetain(cfg, s.Backup.Destinations[0])
		if err != nil {
			writeErr(w, err)
			return
		}

		hasDestination := false
		for _, d := range s.Backup.Destinations {
			if snapshot.DestinationConfigured(d) {
				hasDestination = true
				break
			}
		}
		if hasDestination {
			if err := snapshot.RunBackup(cfg, s.Backup.Destinations, info.Name, false); err != nil {
				writeErr(w, err)
				return
			}
		}
		writeJSON(w, info)
	})

	mux.HandleFunc("POST /api/snapshots/{name}/restore", func(w http.ResponseWriter, r *http.Request) {
		if !serverctl.IsStopped(cfg) {
			writeErr(w, fmt.Errorf("сервер запущен — остановите его перед восстановлением"))
			return
		}
		runOrErr(w, func() error { return snapshot.Restore(cfg, r.PathValue("name")) })
	})

	mux.HandleFunc("DELETE /api/backups", func(w http.ResponseWriter, r *http.Request) {
		idx, err := strconv.Atoi(r.URL.Query().Get("dest"))
		if err != nil || idx < 0 || idx > 1 {
			writeErr(w, fmt.Errorf("dest должен быть 0 или 1"))
			return
		}
		name := r.URL.Query().Get("name")
		if name == "" {
			writeErr(w, fmt.Errorf("name обязателен"))
			return
		}
		if idx == 0 {
			runOrErr(w, func() error { return snapshot.Delete(cfg, name) })
			return
		}
		d, err := secondaryDestination(cfg)
		if err != nil {
			writeErr(w, err)
			return
		}
		switch d.Type {
		case "disk":
			runOrErr(w, func() error { return snapshot.DeleteFromDisk(d.DiskPath, name) })
		case "ftp", "s3":
			runOrErr(w, func() error { return snapshot.DeleteFromRemote(d, name) })
		default:
			writeErr(w, fmt.Errorf("доп. хранилище (dest=1) не настроено"))
		}
	})
	mux.HandleFunc("GET /api/backups/download", func(w http.ResponseWriter, r *http.Request) {
		idx, err := strconv.Atoi(r.URL.Query().Get("dest"))
		if err != nil || idx < 0 || idx > 1 {
			writeErr(w, fmt.Errorf("dest должен быть 0 или 1"))
			return
		}
		name := r.URL.Query().Get("name")
		if idx == 0 {

			path, err := snapshot.ArchivePathOrCreate(cfg, name)
			if err != nil {
				writeErr(w, err)
				return
			}
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.tar.zst"`, name))
			http.ServeFile(w, r, path)
			return
		}
		d, err := secondaryDestination(cfg)
		if err != nil {
			writeErr(w, err)
			return
		}
		if d.Type == "disk" {
			path := filepath.Join(d.DiskPath, name+".tar.zst")
			if _, statErr := os.Stat(path); statErr != nil {
				writeErr(w, statErr)
				return
			}
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.tar.zst"`, name))
			http.ServeFile(w, r, path)
			return
		}
		if d.Type != "ftp" && d.Type != "s3" {
			writeErr(w, fmt.Errorf("доп. хранилище (dest=1) не настроено"))
			return
		}

		store, err := remotestore.New(d)
		if err != nil {
			writeErr(w, err)
			return
		}
		rc, err := store.Open(name + ".tar.zst")
		if err != nil {
			writeErr(w, err)
			return
		}
		defer rc.Close()
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.tar.zst"`, name))
		w.Header().Set("Content-Type", "application/octet-stream")
		io.Copy(w, rc)
	})

	mux.HandleFunc("POST /api/backups/clear", func(w http.ResponseWriter, r *http.Request) {
		idx, err := strconv.Atoi(r.URL.Query().Get("dest"))
		if err != nil || idx < 0 || idx > 1 {
			writeErr(w, fmt.Errorf("dest должен быть 0 или 1"))
			return
		}
		if idx == 0 {
			skipped, err := snapshot.DeleteAllSnapshots(cfg)
			if err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, map[string]any{"ok": true, "skipped": skipped})
			return
		}
		d, err := secondaryDestination(cfg)
		if err != nil {
			writeErr(w, err)
			return
		}
		switch d.Type {
		case "disk":
			err = snapshot.ClearDisk(d.DiskPath)
		case "ftp", "s3":
			err = snapshot.ClearRemote(d)
		default:
			err = fmt.Errorf("доп. хранилище (dest=1) не настроено")
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("GET /api/backups/destinations", func(w http.ResponseWriter, r *http.Request) {
		s, err := settings.Load(cfg.SettingsFile)
		if err != nil {
			writeErr(w, err)
			return
		}
		type destOut struct {
			Index    int    `json:"index"`
			Type     string `json:"type"`
			DiskPath string `json:"diskPath,omitempty"`

			FTPProtocol string             `json:"ftpProtocol,omitempty"`
			FTPHost     string             `json:"ftpHost,omitempty"`
			FTPPath     string             `json:"ftpPath,omitempty"`
			S3Endpoint  string             `json:"s3Endpoint,omitempty"`
			S3Bucket    string             `json:"s3Bucket,omitempty"`
			Retention   settings.Retention `json:"retention"`
			Stats       snapshot.Stats     `json:"stats"`

			Configured bool `json:"configured"`
		}
		out := make([]destOut, 0, len(s.Backup.Destinations))
		for i, d := range s.Backup.Destinations {
			stats, err := snapshot.DestinationStats(cfg, d)
			if err != nil {
				log.Printf("api: destination %d stats: %v", i, err)
			}
			out = append(out, destOut{
				Index: i, Type: d.Type, DiskPath: d.DiskPath,
				FTPProtocol: d.FTPProtocol, FTPHost: d.FTPHost, FTPPath: d.FTPPath,
				S3Endpoint: d.S3Endpoint, S3Bucket: d.S3Bucket,
				Retention: d.Retention, Stats: stats,
				Configured: snapshot.DestinationConfigured(d),
			})
		}
		writeJSON(w, out)
	})

	mux.HandleFunc("GET /api/backups/browse", func(w http.ResponseWriter, r *http.Request) {
		idx, err := strconv.Atoi(r.URL.Query().Get("dest"))
		if err != nil || idx < 0 || idx > 1 {
			writeErr(w, fmt.Errorf("dest должен быть 0 или 1"))
			return
		}
		name := r.URL.Query().Get("name")
		subPath := r.URL.Query().Get("path")
		s, err := settings.Load(cfg.SettingsFile)
		if err != nil {
			writeErr(w, err)
			return
		}
		d := s.Backup.Destinations[idx]
		var entries []snapshot.BrowseEntry
		switch d.Type {
		case "snapshot":
			entries, err = snapshot.BrowseSnapshot(cfg, name, subPath)
		case "disk":
			entries, err = snapshot.BrowseArchive(d.DiskPath, name, subPath)
		case "ftp", "s3":
			entries, err = snapshot.BrowseRemoteArchive(d, name, subPath)
		default:
			err = fmt.Errorf("хранилище %d не поддерживает просмотр", idx)
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, entries)
	})

	mux.HandleFunc("POST /api/backups/restore", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Dest  int      `json:"dest"`
			Name  string   `json:"name"`
			Paths []string `json:"paths"`
		}
		if !decodeOrErr(w, r, &body) {
			return
		}
		if body.Dest < 0 || body.Dest > 1 {
			writeErr(w, fmt.Errorf("dest должен быть 0 или 1"))
			return
		}
		if !serverctl.IsStopped(cfg) {
			writeErr(w, fmt.Errorf("сервер запущен — остановите его перед восстановлением"))
			return
		}
		s, err := settings.Load(cfg.SettingsFile)
		if err != nil {
			writeErr(w, err)
			return
		}
		d := s.Backup.Destinations[body.Dest]
		switch d.Type {
		case "snapshot":
			runOrErr(w, func() error { return snapshot.RestoreFromSnapshot(cfg, body.Name, body.Paths) })
		case "disk":
			runOrErr(w, func() error { return snapshot.RestoreFromArchive(cfg, d.DiskPath, body.Name, body.Paths) })
		case "ftp", "s3":
			runOrErr(w, func() error { return snapshot.RestoreFromRemote(cfg, d, body.Name, body.Paths) })
		default:
			writeErr(w, fmt.Errorf("хранилище %d не поддерживает восстановление", body.Dest))
		}
	})

	mux.HandleFunc("POST /api/backups/destinations/test", func(w http.ResponseWriter, r *http.Request) {
		var d settings.BackupDestination
		if !decodeOrErr(w, r, &d) {
			return
		}
		if d.Type != "ftp" && d.Type != "s3" {
			writeErr(w, fmt.Errorf("проверка соединения поддерживается только для ftp/sftp/s3"))
			return
		}
		store, err := remotestore.New(d)
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
}
