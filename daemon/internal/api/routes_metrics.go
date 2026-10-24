package api

import (
	"fmt"
	"net/http"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/logbuf"
	"elb/daemon/internal/metrics"
	"elb/daemon/internal/modclient"
)

var metricsRangeDurations = map[string]time.Duration{
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
}

func registerMetricsRoutes(mux *http.ServeMux, cfg config.Config, mod *modclient.Client, logBuf *logbuf.Buffer) {

	mux.HandleFunc("GET /api/metrics/history", func(w http.ResponseWriter, r *http.Request) {
		var since, until time.Time
		sinceStr, untilStr := r.URL.Query().Get("since"), r.URL.Query().Get("until")
		if sinceStr != "" && untilStr != "" {
			s, errS := time.Parse(time.RFC3339, sinceStr)
			u, errU := time.Parse(time.RFC3339, untilStr)
			if errS != nil || errU != nil {
				writeErr(w, fmt.Errorf("since/until: неверный формат времени (нужен RFC3339)"))
				return
			}
			since, until = s, u
		} else {
			d, ok := metricsRangeDurations[r.URL.Query().Get("range")]
			if !ok {
				d = metricsRangeDurations["24h"]
			}
			until = time.Now()
			since = until.Add(-d)
		}
		history, err := metrics.Query(cfg, since, until)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, history)
	})
}
