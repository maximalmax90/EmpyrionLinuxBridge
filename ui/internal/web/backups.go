package web

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"

	"elb/ui/internal/daemonclient"
)

func (s *Server) handleBackupsPage(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)

	snapshots, err := s.daemon.Snapshots()
	if err != nil {
		log.Printf("backups: snapshots: %v", err)
	}

	destinations, err := s.daemon.BackupDestinations()
	if err != nil {
		log.Printf("backups: destinations: %v", err)
	}

	hasSecondaryDestination := len(destinations) > 1 && destinations[1].Configured

	data := map[string]any{
		"Username": u.Username, "Snapshots": snapshots, "Destinations": destinations,
		"AnyArchiving": anyArchiving(snapshots), "HasSecondaryDestination": hasSecondaryDestination,
	}
	for k, v := range s.statusBarData(u) {
		data[k] = v
	}
	if err := s.execTemplate(w, r, "backups.html", data); err != nil {
		log.Printf("backups: render: %v", err)
	}
}

func anyArchiving(snapshots []daemonclient.Snapshot) bool {
	for _, s := range snapshots {
		if s.Archiving {
			return true
		}
	}
	return false
}

func (s *Server) handleActionBackupsTables(w http.ResponseWriter, r *http.Request) {
	snapshots, err := s.daemon.Snapshots()
	if err != nil {
		log.Printf("backups: tables: snapshots: %v", err)
	}
	destinations, err := s.daemon.BackupDestinations()
	if err != nil {
		log.Printf("backups: tables: destinations: %v", err)
	}

	health, err := s.daemon.Health()
	if err != nil {
		log.Printf("backups: tables: health: %v", err)
	}
	data := map[string]any{"Snapshots": snapshots, "Destinations": destinations, "AnyArchiving": anyArchiving(snapshots), "Health": health}
	if err := s.execTemplate(w, r, "backups_tables", data); err != nil {
		log.Printf("backups: tables render: %v", err)
	}
}

func (s *Server) handleActionBackupsRun(w http.ResponseWriter, r *http.Request) {
	if err := s.daemon.RunBackupNow(); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось выполнить бэкап: %v", err))
		return
	}
	writeToast(w, "Бэкап выполнен")
}

func (s *Server) handleActionBackupsRestore(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.daemon.RestoreSnapshot(name); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось восстановить: %v", err))
		return
	}
	writeToast(w, "Восстановлено, сервер перезапущен")
}

func (s *Server) handleActionBackupsDelete(w http.ResponseWriter, r *http.Request) {
	dest, err := strconv.Atoi(r.URL.Query().Get("dest"))
	if err != nil {
		writeToast(w, "Некорректное хранилище")
		return
	}
	name := r.URL.Query().Get("name")
	if err := s.daemon.DeleteBackup(dest, name); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось удалить: %v", err))
		return
	}
	writeToast(w, "Копия удалена")
}

func (s *Server) handleActionBackupsBrowse(w http.ResponseWriter, r *http.Request) {
	dest, err := strconv.Atoi(r.URL.Query().Get("dest"))
	if err != nil {
		http.Error(w, "bad dest", http.StatusBadRequest)
		return
	}
	entries, err := s.daemon.BrowseBackup(dest, r.URL.Query().Get("name"), r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

func (s *Server) handleActionBackupsRestoreFiles(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Dest  int      `json:"dest"`
		Name  string   `json:"name"`
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	if err := s.daemon.RestoreBackup(body.Dest, body.Name, body.Paths); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось восстановить: %v", err))
		return
	}
	writeToast(w, "Восстановлено")
}

func (s *Server) handleActionBackupsDownload(w http.ResponseWriter, r *http.Request) {
	dest, err := strconv.Atoi(r.URL.Query().Get("dest"))
	if err != nil {
		http.Error(w, "bad dest", http.StatusBadRequest)
		return
	}
	name := r.URL.Query().Get("name")
	body, size, err := s.daemon.BackupArchiveReader(dest, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.tar.zst"`, name))
	if size > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	}
	if _, err := io.Copy(w, body); err != nil {
		log.Printf("backups: downloading archive %s: %v", name, err)
	}
}
