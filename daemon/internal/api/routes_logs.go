package api

import (
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/gamefiles"
	"elb/daemon/internal/logbuf"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/serverlog"
)

func dailyLogDir(cfg config.Config) string {
	return filepath.Join(cfg.DataRoot, "elb", "logs")
}

func registerLogRoutes(mux *http.ServeMux, cfg config.Config, mod *modclient.Client, logBuf *logbuf.Buffer) {

	mux.HandleFunc("GET /api/logs/daemon", func(w http.ResponseWriter, r *http.Request) {

		if date := r.URL.Query().Get("date"); date != "" {
			entries, err := logbuf.ReadDate(dailyLogDir(cfg), "daemon", date)
			if err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, entries)
			return
		}
		n := 500
		if v := r.URL.Query().Get("n"); v != "" {
			if p, err := strconv.Atoi(v); err == nil {
				n = p
			}
		}
		writeJSON(w, logBuf.Recent(n))
	})
	mux.HandleFunc("GET /api/logs/daemon/dates", func(w http.ResponseWriter, r *http.Request) {
		dates, err := logbuf.Dates(dailyLogDir(cfg), "daemon")
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, dates)
	})

	mux.HandleFunc("GET /api/logs/server", func(w http.ResponseWriter, r *http.Request) {
		logs, err := gamefiles.ListLogs(cfg)
		if err != nil {
			writeErr(w, err)
			return
		}
		dateParam := r.URL.Query().Get("date")
		if dateParam == "" {
			maxBytes := int64(0)
			if v := r.URL.Query().Get("maxBytes"); v != "" {
				if n, err := strconv.ParseInt(v, 10, 64); err == nil {
					maxBytes = n
				}
			}
			if len(logs) == 0 {
				writeJSON(w, []serverlog.Line{})
				return
			}
			text, err := gamefiles.ReadLog(cfg, logs[0].Name, maxBytes)
			if err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, serverlog.ParseLines(text))
			return
		}
		d, err := time.Parse("2006-01-02", dateParam)
		if err != nil {
			writeErr(w, fmt.Errorf("date: %w", err))
			return
		}
		prefix := "Dedicated_" + d.Format("060102") + "-"
		var names []string
		for _, lf := range logs {
			if strings.HasPrefix(lf.Name, prefix) {
				names = append(names, lf.Name)
			}
		}
		sort.Strings(names)
		var combined strings.Builder
		for _, name := range names {
			text, err := gamefiles.ReadLog(cfg, name, 0)
			if err != nil {
				continue
			}
			combined.WriteString(text)
			if !strings.HasSuffix(text, "\n") {
				combined.WriteString("\n")
			}
		}
		writeJSON(w, serverlog.ParseLines(combined.String()))
	})

	mux.HandleFunc("GET /api/logs/server/dates", func(w http.ResponseWriter, r *http.Request) {
		logs, err := gamefiles.ListLogs(cfg)
		if err != nil {
			writeErr(w, err)
			return
		}
		seen := map[string]bool{}
		var dates []string
		for _, lf := range logs {
			m := dedicatedLogNameRe.FindStringSubmatch(lf.Name)
			if m == nil {
				continue
			}
			d := "20" + m[1] + "-" + m[2] + "-" + m[3]
			if !seen[d] {
				seen[d] = true
				dates = append(dates, d)
			}
		}
		sort.Sort(sort.Reverse(sort.StringSlice(dates)))
		writeJSON(w, dates)
	})
}
