package web

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"elb/ui/internal/daemonclient"
)

const scenarioCacheZipName = "sharedData.zip"

func decodeScenarioCacheSettings(r *http.Request) (daemonclient.ScenarioCacheSettings, error) {
	var sc daemonclient.ScenarioCacheSettings
	err := json.NewDecoder(r.Body).Decode(&sc)
	return sc, err
}

func (s *Server) handleActionSettingsScenarioCache(w http.ResponseWriter, r *http.Request) {
	sc, err := decodeScenarioCacheSettings(r)
	if err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	if err := s.daemon.SetScenarioCacheConfig(sc); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось сохранить: %v", err))
		return
	}
	writeToast(w, "Сохранено")
}

func (s *Server) handleActionScenarioCacheTest(w http.ResponseWriter, r *http.Request) {
	sc, err := decodeScenarioCacheSettings(r)
	if err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	if err := s.daemon.ScenarioCacheTestDestination(sc); err != nil {
		writeToast(w, fmt.Sprintf("Проверка не пройдена: %v", err))
		return
	}
	writeToast(w, "Соединение успешно")
}

func (s *Server) handleScenarioCacheFile(w http.ResponseWriter, r *http.Request) {
	if !s.scenarioCacheLimiter.Allow(clientIP(r, s.trustProxyHeaders)) {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	body, size, err := s.daemon.ScenarioCacheReader()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, scenarioCacheZipName))
	if size > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	}
	if _, err := io.Copy(w, body); err != nil {
		log.Printf("scenario cache: streaming %s: %v", scenarioCacheZipName, err)
	}
}
