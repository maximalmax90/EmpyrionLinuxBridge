package api

import (
	"net/http"
	"strconv"

	"elb/daemon/internal/config"
	"elb/daemon/internal/logbuf"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/scheduler"
)

func registerSchedulerRoutes(mux *http.ServeMux, cfg config.Config, mod *modclient.Client, logBuf *logbuf.Buffer) {

	mux.HandleFunc("GET /api/scheduler/tasks", func(w http.ResponseWriter, r *http.Request) {
		v, err := scheduler.List(cfg)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, v)
	})
	mux.HandleFunc("POST /api/scheduler/tasks", func(w http.ResponseWriter, r *http.Request) {
		var t scheduler.Task
		if !decodeOrErr(w, r, &t) {
			return
		}
		t.ID = 0
		saved, err := scheduler.Save(cfg, t)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, saved)
	})
	mux.HandleFunc("PUT /api/scheduler/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeErr(w, err)
			return
		}
		var t scheduler.Task
		if !decodeOrErr(w, r, &t) {
			return
		}
		t.ID = id
		saved, err := scheduler.Save(cfg, t)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, saved)
	})
	mux.HandleFunc("DELETE /api/scheduler/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeErr(w, err)
			return
		}
		runOrErr(w, func() error { return scheduler.Delete(cfg, id) })
	})
	mux.HandleFunc("POST /api/scheduler/tasks/{id}/run", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeErr(w, err)
			return
		}
		runOrErr(w, func() error { return scheduler.RunNow(cfg, mod, id) })
	})

	mux.HandleFunc("POST /api/scheduler/tasks/{id}/clone", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeErr(w, err)
			return
		}
		clone, err := scheduler.CloneTask(cfg, id)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, clone)
	})

	mux.HandleFunc("GET /api/scheduler/runs", func(w http.ResponseWriter, r *http.Request) {
		limit := 200
		if v := r.URL.Query().Get("limit"); v != "" {
			if p, err := strconv.Atoi(v); err == nil && p > 0 {
				limit = p
			}
		}
		from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
		var v []scheduler.Run
		var err error
		if from != "" || to != "" {
			v, err = scheduler.RunsBetween(cfg, from, to, limit)
		} else {
			v, err = scheduler.Runs(cfg, limit)
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, v)
	})

	mux.HandleFunc("GET /api/scheduler/tasks/{id}/subtasks", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeErr(w, err)
			return
		}
		v, err := scheduler.SubtasksForTask(cfg, id)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, v)
	})
	mux.HandleFunc("POST /api/scheduler/tasks/{id}/subtasks", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeErr(w, err)
			return
		}
		var s scheduler.Subtask
		if !decodeOrErr(w, r, &s) {
			return
		}
		s.ID = 0
		s.TaskID = id
		saved, err := scheduler.SaveSubtask(cfg, s)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, saved)
	})
	mux.HandleFunc("PUT /api/scheduler/subtasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeErr(w, err)
			return
		}
		var s scheduler.Subtask
		if !decodeOrErr(w, r, &s) {
			return
		}
		s.ID = id
		saved, err := scheduler.SaveSubtask(cfg, s)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, saved)
	})
	mux.HandleFunc("DELETE /api/scheduler/subtasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeErr(w, err)
			return
		}
		runOrErr(w, func() error { return scheduler.DeleteSubtask(cfg, id) })
	})
	mux.HandleFunc("POST /api/scheduler/subtasks/{id}/run", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeErr(w, err)
			return
		}
		runOrErr(w, func() error { return scheduler.RunSubtaskNow(cfg, mod, id) })
	})
}
