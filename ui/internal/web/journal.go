package web

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"

	"elb/ui/internal/daemonclient"
	"elb/ui/internal/i18n"
)

func (s *Server) handleJournalPage(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)

	daemonLogs, err := s.daemon.DaemonLogs(0)
	if err != nil {
		log.Printf("journal: daemon logs: %v", err)
	}
	serverLog, err := s.daemon.ServerLog(serverLogMaxBytes)
	if err != nil {
		log.Printf("journal: server log: %v", err)
	}
	taskRuns, err := s.daemon.TaskRuns()
	if err != nil {
		log.Printf("journal: scheduler runs: %v", err)
	}
	dates, err := s.daemon.ServerLogDates()
	if err != nil {
		log.Printf("journal: server log dates: %v", err)
	}
	daemonDates, err := s.daemon.DaemonLogDates()
	if err != nil {
		log.Printf("journal: daemon log dates: %v", err)
	}
	webuiDates, err := s.uiLog.Dates()
	if err != nil {
		log.Printf("journal: webui log dates: %v", err)
	}
	auditEnabled, err := s.daemon.AuditEnabled()
	if err != nil {
		log.Printf("journal: audit enabled: %v", err)
	}
	var auditEvents []daemonclient.AuditEvent
	if auditEnabled {
		auditEvents, err = s.daemon.AuditEvents(0)
		if err != nil {
			log.Printf("journal: audit events: %v", err)
		}
	}

	data := map[string]any{
		"Username":            u.Username,
		"DaemonLogs":          reversed(toDaemonLogLines(daemonLogs)),
		"ServerLog":           reversed(serverLog),
		"WebUILogs":           reversed(toWebUILogLines(s.uiLog.Recent(0))),
		"SchedulerLog":        schedulerLogLines(u.Locale, taskRuns),
		"AuditEnabled":        auditEnabled,
		"AuditLog":            auditLogLines(auditEvents),
		"ServerLogDates":      dates,
		"DaemonLogDates":      daemonDates,
		"WebUILogDates":       webuiDates,
		"JournalDateControls": true,
	}
	for k, v := range s.statusBarData(u) {
		data[k] = v
	}
	if err := s.execTemplate(w, r, "journal.html", data); err != nil {
		log.Printf("journal: render: %v", err)
	}
}

func (s *Server) handleActionJournalDaemon(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	var lines []logLine
	if date != "" {
		entries, err := s.daemon.DaemonLogByDate(date)
		if err != nil {
			fmt.Fprintf(w, `<div class="empty">%s</div>`, template.HTMLEscapeString(err.Error()))
			return
		}
		lines = reversed(toDaemonLogLines(entries))
	} else {
		logs, err := s.daemon.DaemonLogs(0)
		if err != nil {
			fmt.Fprintf(w, `<div class="empty">%s</div>`, template.HTMLEscapeString(err.Error()))
			return
		}
		lines = reversed(toDaemonLogLines(logs))
	}
	if err := s.execTemplate(w, r, "simple_log_rows", lines); err != nil {
		log.Printf("journal/daemon: render: %v", err)
	}
}

func (s *Server) handleActionJournalWebui(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	var lines []logLine
	if date != "" {
		entries, err := s.uiLog.ReadDate(date)
		if err != nil {
			fmt.Fprintf(w, `<div class="empty">%s</div>`, template.HTMLEscapeString(err.Error()))
			return
		}
		lines = reversed(toWebUILogLines(entries))
	} else {
		lines = reversed(toWebUILogLines(s.uiLog.Recent(0)))
	}
	if err := s.execTemplate(w, r, "simple_log_rows", lines); err != nil {
		log.Printf("journal/webui: render: %v", err)
	}
}

func (s *Server) handleActionJournalServer(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	var logs []daemonclient.ServerLogLine
	var err error
	if date != "" {
		logs, err = s.daemon.ServerLogByDate(date)
	} else {
		logs, err = s.daemon.ServerLog(serverLogMaxBytes)
	}
	if err != nil {
		fmt.Fprintf(w, `<div class="empty">%s</div>`, template.HTMLEscapeString(err.Error()))
		return
	}
	if err := s.execTemplate(w, r, "server_log_rows", reversed(logs)); err != nil {
		log.Printf("journal/server: render: %v", err)
	}
}

func (s *Server) handleActionJournalScheduler(w http.ResponseWriter, r *http.Request) {
	lang := i18n.Default
	if u := userFromCtx(r); u != nil {
		lang = u.Locale
	}
	date := r.URL.Query().Get("date")
	var runs []daemonclient.TaskRun
	var err error
	if date != "" {
		from, to, perr := dayRangeRFC3339(date)
		if perr != nil {
			fmt.Fprintf(w, `<div class="empty">%s</div>`, template.HTMLEscapeString(perr.Error()))
			return
		}
		runs, err = s.daemon.TaskRunsBetween(from, to, 0)
	} else {
		runs, err = s.daemon.TaskRuns()
	}
	if err != nil {
		fmt.Fprintf(w, `<div class="empty">%s</div>`, template.HTMLEscapeString(err.Error()))
		return
	}
	if err := s.execTemplate(w, r, "simple_log_rows", schedulerLogLines(lang, runs)); err != nil {
		log.Printf("journal/scheduler: render: %v", err)
	}
}

func (s *Server) handleActionJournalAudit(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	var events []daemonclient.AuditEvent
	var err error
	if date != "" {
		from, to, perr := dayRangeRFC3339(date)
		if perr != nil {
			fmt.Fprintf(w, `<div class="empty">%s</div>`, template.HTMLEscapeString(perr.Error()))
			return
		}
		events, err = s.daemon.AuditEventsBetween(from, to, 0)
	} else {
		events, err = s.daemon.AuditEvents(0)
	}
	if err != nil {
		fmt.Fprintf(w, `<div class="empty">%s</div>`, template.HTMLEscapeString(err.Error()))
		return
	}
	if err := s.execTemplate(w, r, "simple_log_rows", auditLogLines(events)); err != nil {
		log.Printf("journal/audit: render: %v", err)
	}
}

func dayRangeRFC3339(dateStr string) (from, to string, err error) {
	d, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return "", "", err
	}
	start := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.Local).UTC()
	end := start.AddDate(0, 0, 1)
	return start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), nil
}
