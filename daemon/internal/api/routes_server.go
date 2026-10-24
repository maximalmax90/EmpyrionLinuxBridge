package api

import (
	"net/http"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/factions"
	"elb/daemon/internal/gameoptions"
	"elb/daemon/internal/identity"
	"elb/daemon/internal/logbuf"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/serverctl"
	"elb/daemon/internal/steamjob"
	"elb/daemon/internal/sysinfo"
	"elb/daemon/internal/watchdog"
)

func registerServerRoutes(mux *http.ServeMux, cfg config.Config, mod *modclient.Client, logBuf *logbuf.Buffer) {

	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, serverctl.GetStatus(cfg))
	})

	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, watchdog.CachedHealth())
	})

	mux.HandleFunc("GET /api/time", func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		_, offsetSeconds := now.Zone()
		writeJSON(w, map[string]any{
			"formatted":     now.Format("02.01.2006 15:04:05"),
			"offsetSeconds": offsetSeconds,
		})
	})

	mux.HandleFunc("GET /api/sysinfo", func(w http.ResponseWriter, r *http.Request) {
		snap, err := sysinfo.Read(cfg.DataRoot)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, snap)
	})

	mux.HandleFunc("GET /api/sysinfo/mounts", func(w http.ResponseWriter, r *http.Request) {
		mounts, err := sysinfo.ListMounts()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, mounts)
	})

	mux.HandleFunc("GET /api/identity", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, identity.Get(cfg))
	})

	mux.HandleFunc("GET /api/factions", func(w http.ResponseWriter, r *http.Request) {

		writeJSON(w, factions.Cached())
	})

	mux.HandleFunc("GET /api/alliances", func(w http.ResponseWriter, r *http.Request) {
		type alliancesResp struct {
			Enabled bool `json:"enabled"`
			Pairs   any  `json:"pairs"`
		}

		snap, err := gameoptions.Read(cfg)
		if err == nil && snap.Found {
			for _, f := range snap.Fields {
				if f.Name == "OriginAccessOthers" && f.Value != "true" {
					writeJSON(w, alliancesResp{Enabled: false, Pairs: []any{}})
					return
				}
			}
		}
		list := factions.Cached()
		if len(list) < 2 {
			writeJSON(w, alliancesResp{Enabled: true, Pairs: []any{}})
			return
		}
		byID := make(map[int]string, len(list))
		ids := make([]int, len(list))
		for i, f := range list {
			ids[i] = f.ID
			byID[f.ID] = f.Abbreviation
		}
		pairs, err := mod.AlliancePairs(ids)
		if err != nil {
			writeErr(w, err)
			return
		}
		type alliance struct {
			Faction1Id  int    `json:"faction1Id"`
			Faction1Tag string `json:"faction1Tag"`
			Faction2Id  int    `json:"faction2Id"`
			Faction2Tag string `json:"faction2Tag"`
		}
		out := make([]alliance, 0, len(pairs))
		for _, p := range pairs {
			out = append(out, alliance{
				Faction1Id: p.Faction1Id, Faction1Tag: byID[p.Faction1Id],
				Faction2Id: p.Faction2Id, Faction2Tag: byID[p.Faction2Id],
			})
		}
		writeJSON(w, alliancesResp{Enabled: true, Pairs: out})
	})

	mux.HandleFunc("POST /api/server/start", func(w http.ResponseWriter, r *http.Request) {
		runOrErr(w, func() error { return serverctl.Start(cfg.SystemdUnit) })
	})
	mux.HandleFunc("POST /api/server/stop", func(w http.ResponseWriter, r *http.Request) {
		runOrErr(w, func() error { return serverctl.Stop(cfg.SystemdUnit) })
	})
	mux.HandleFunc("POST /api/server/restart", func(w http.ResponseWriter, r *http.Request) {
		runOrErr(w, func() error { return serverctl.Restart(cfg.SystemdUnit) })
	})

	mux.HandleFunc("POST /api/server/manual-stop", func(w http.ResponseWriter, r *http.Request) {
		handleManualAction(w, cfg, "stop")
	})
	mux.HandleFunc("POST /api/server/manual-restart", func(w http.ResponseWriter, r *http.Request) {
		handleManualAction(w, cfg, "restart")
	})

	mux.HandleFunc("GET /api/server/manual-status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, serverctl.ManualStatus())
	})

	mux.HandleFunc("POST /api/server/update", func(w http.ResponseWriter, r *http.Request) {

		if !serverctl.IsStopped(cfg) {
			http.Error(w, "сервер запущен — остановите его перед обновлением", http.StatusConflict)
			return
		}
		if !steamjob.UpdateServer(cfg) {
			http.Error(w, "steamcmd уже выполняет другую задачу", http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})

	mux.HandleFunc("GET /api/server/update-check", func(w http.ResponseWriter, r *http.Request) {
		status, err := steamjob.CheckServerUpdate(cfg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, status)
	})

	mux.HandleFunc("POST /api/console", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Command string `json:"command"`
		}
		if !decodeOrErr(w, r, &body) {
			return
		}
		runConsole(w, cfg, body.Command, 15*time.Second)
	})
}
