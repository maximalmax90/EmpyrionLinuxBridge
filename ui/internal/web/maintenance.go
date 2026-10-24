package web

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"elb/ui/internal/daemonclient"
)

func (s *Server) handleMaintenancePage(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)

	scenarios, err := s.daemon.Scenarios()
	if err != nil {
		log.Printf("maintenance: scenarios: %v", err)
	}
	cfg, err := s.daemon.Config()
	if err != nil {
		log.Printf("maintenance: config: %v", err)
	}

	configFiles, err := s.daemon.ConfigFiles()
	if err != nil {
		log.Printf("maintenance: config files: %v", err)
	}

	installedItems, err := s.daemon.SteamWorkshopInstalledItems()
	if err != nil {
		log.Printf("maintenance: installed workshop items: %v", err)
	}
	var installedScenarios []daemonclient.InstalledWorkshopItem
	for _, it := range installedItems {
		if it.Kind == "scenario" {
			installedScenarios = append(installedScenarios, it)
		}
	}

	scenarioCacheInfo, err := s.daemon.ScenarioCacheInfo()
	if err != nil {
		log.Printf("maintenance: scenario cache info: %v", err)
	}

	data := map[string]any{
		"Username":           u.Username,
		"Scenarios":          scenarios,
		"CurrentScenario":    cfg.Server.CustomScenario.Value,
		"ConfigFiles":        configFiles,
		"SteamUsername":      cfg.Steam.Username,
		"InstalledScenarios": installedScenarios,
		"ScenarioCacheInfo":  scenarioCacheInfo,
	}
	for k, v := range s.statusBarData(u) {
		data[k] = v
	}
	if err := s.execTemplate(w, r, "maintenance.html", data); err != nil {
		log.Printf("maintenance: render: %v", err)
	}
}

func (s *Server) handleActionMaintenanceUpdate(w http.ResponseWriter, r *http.Request) {
	if err := s.daemon.StartServerUpdate(); err != nil {
		http.Error(w, fmt.Sprintf("Не удалось начать обновление: %v", err), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleActionMaintenanceDuplicatePlayers(w http.ResponseWriter, r *http.Request) {
	groups, err := s.daemon.DuplicatePlayers()
	if err != nil {
		log.Printf("maintenance: duplicate players: %v", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(groups)
}

func (s *Server) handleActionMaintenanceCleanupDuplicatePlayers(w http.ResponseWriter, r *http.Request) {
	var groups []daemonclient.DuplicateGroup
	if err := json.NewDecoder(r.Body).Decode(&groups); err != nil {
		http.Error(w, "Некорректный запрос", http.StatusBadRequest)
		return
	}
	removed, err := s.daemon.CleanupDuplicatePlayers(groups)
	if err != nil {
		log.Printf("maintenance: cleanup duplicate players: %v", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"removed": removed})
}

func (s *Server) handleActionMaintenanceWipeLocalDB(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Stores []string `json:"stores"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Некорректный запрос", http.StatusBadRequest)
		return
	}
	if err := s.daemon.WipeLocalData(body.Stores); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleActionSteamJobStatus(w http.ResponseWriter, r *http.Request) {
	job, err := s.daemon.SteamJobStatus()
	if err != nil {
		log.Printf("maintenance: steam job status: %v", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(job)
}

func (s *Server) handleActionSteamLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректный запрос", http.StatusBadRequest)
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")
	if username == "" || password == "" {
		http.Error(w, "Логин и пароль обязательны", http.StatusBadRequest)
		return
	}
	if err := s.daemon.SteamLogin(username, password); err != nil {
		http.Error(w, fmt.Sprintf("Не удалось начать вход: %v", err), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleActionSteamCode(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректный запрос", http.StatusBadRequest)
		return
	}
	code := r.FormValue("code")
	if code == "" {
		http.Error(w, "Введите код", http.StatusBadRequest)
		return
	}
	if err := s.daemon.SteamSubmitCode(code); err != nil {
		http.Error(w, fmt.Sprintf("Не удалось отправить код: %v", err), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleActionSteamLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.daemon.SteamLogout(); err != nil {
		http.Error(w, fmt.Sprintf("Не удалось выйти: %v", err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleActionSteamWorkshopLookup(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "укажите id", http.StatusBadRequest)
		return
	}
	info, err := s.daemon.SteamWorkshopLookup(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

func (s *Server) handleActionSteamWorkshopInstall(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректный запрос", http.StatusBadRequest)
		return
	}
	id := r.FormValue("id")
	if id == "" {
		http.Error(w, "укажите id", http.StatusBadRequest)
		return
	}
	if err := s.daemon.SteamWorkshopInstall(id); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleActionScenariosRefresh(w http.ResponseWriter, r *http.Request) {
	scenarios, err := s.daemon.Scenarios()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	cfg, err := s.daemon.Config()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"scenarios": scenarios,
		"current":   cfg.Server.CustomScenario.Value,
	})
}

func (s *Server) handleActionSteamWorkshopInstalled(w http.ResponseWriter, r *http.Request) {
	items, err := s.daemon.SteamWorkshopInstalledItems()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	var scenarios []daemonclient.InstalledWorkshopItem
	for _, it := range items {
		if it.Kind == "scenario" {
			scenarios = append(scenarios, it)
		}
	}
	cfg, err := s.daemon.Config()
	if err != nil {
		log.Printf("maintenance: config: %v", err)
	}
	data := map[string]any{"Items": scenarios, "CurrentScenario": cfg.Server.CustomScenario.Value}
	if err := s.execTemplate(w, r, "workshop_rows", data); err != nil {
		log.Printf("maintenance: workshop rows: %v", err)
	}
}

func (s *Server) handleActionConfigFiles(w http.ResponseWriter, r *http.Request) {
	files, err := s.daemon.ConfigFiles()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if err := s.execTemplate(w, r, "configfile_rows", files); err != nil {
		log.Printf("maintenance: config file rows: %v", err)
	}
}

func (s *Server) handleActionSteamWorkshopDownload(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	body, disposition, err := s.daemon.SteamWorkshopArchiveReader(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	if disposition != "" {
		w.Header().Set("Content-Disposition", disposition)
	}
	if _, err := io.Copy(w, body); err != nil {
		log.Printf("maintenance: downloading workshop item %s: %v", id, err)
	}
}

func (s *Server) handleActionSteamWorkshopCheck(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректный запрос", http.StatusBadRequest)
		return
	}
	id := r.FormValue("id")
	if id == "" {
		http.Error(w, "укажите id", http.StatusBadRequest)
		return
	}
	status, err := s.daemon.SteamWorkshopCheck(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (s *Server) handleActionSteamWorkshopRemove(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректный запрос", http.StatusBadRequest)
		return
	}
	id := r.FormValue("id")
	if id == "" {
		http.Error(w, "укажите id", http.StatusBadRequest)
		return
	}
	if err := s.daemon.SteamWorkshopRemove(id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleActionServerUpdateCheck(w http.ResponseWriter, r *http.Request) {
	status, err := s.daemon.CheckServerUpdate()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (s *Server) handleActionMaintenanceScenario(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	name := r.FormValue("name")
	if err := s.daemon.SelectScenario(name); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось выбрать сценарий: %v", err))
		return
	}
	writeToast(w, "Сценарий выбран — применится после перезапуска сервера")
}

func (s *Server) handleActionMaintenanceFileRead(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	content, err := s.daemon.ReadConfigFile(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(content))
}

func (s *Server) handleActionMaintenanceFileWrite(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeToast(w, fmt.Sprintf("Не удалось прочитать тело запроса: %v", err))
		return
	}
	if err := s.daemon.WriteConfigFile(name, string(body)); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось сохранить: %v", err))
		return
	}
	writeToast(w, "Файл загружен")
}
