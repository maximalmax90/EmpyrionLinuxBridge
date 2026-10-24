package web

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

func (s *Server) handleMonitoringPage(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)

	data := map[string]any{
		"Username": u.Username,
	}
	for k, v := range s.statusBarData(u) {
		data[k] = v
	}
	if err := s.execTemplate(w, r, "monitoring.html", data); err != nil {
		log.Printf("monitoring: render: %v", err)
	}
}

func (s *Server) handleMonitoringData(w http.ResponseWriter, r *http.Request) {
	var (
		history any
		err     error
	)
	sinceStr, untilStr := r.URL.Query().Get("since"), r.URL.Query().Get("until")
	if sinceStr != "" && untilStr != "" {
		since, errS := time.Parse(time.RFC3339, sinceStr)
		until, errU := time.Parse(time.RFC3339, untilStr)
		if errS != nil || errU != nil {
			http.Error(w, "since/until: неверный формат времени (нужен RFC3339)", http.StatusBadRequest)
			return
		}
		history, err = s.daemon.MetricsHistoryRange(since, until)
	} else {
		rangeParam := r.URL.Query().Get("range")
		if rangeParam == "" {
			rangeParam = "24h"
		}
		history, err = s.daemon.MetricsHistory(rangeParam)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}
