package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"elb/daemon/internal/api"
	"elb/daemon/internal/auditcheck"
	"elb/daemon/internal/backpack"
	"elb/daemon/internal/chatlog"
	"elb/daemon/internal/config"
	"elb/daemon/internal/factions"
	"elb/daemon/internal/globaldb"
	"elb/daemon/internal/hardwipe"
	"elb/daemon/internal/history"
	"elb/daemon/internal/icons"
	"elb/daemon/internal/identity"
	"elb/daemon/internal/logbuf"
	"elb/daemon/internal/logcleanup"
	"elb/daemon/internal/metrics"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/permissions"
	"elb/daemon/internal/playerqueue"
	"elb/daemon/internal/playerstats"
	"elb/daemon/internal/presence"
	"elb/daemon/internal/presencenotify"
	"elb/daemon/internal/restartqueue"
	"elb/daemon/internal/safego"
	"elb/daemon/internal/scenariocache"
	"elb/daemon/internal/scheduler"
	"elb/daemon/internal/scriptqueue"
	"elb/daemon/internal/settings"
	"elb/daemon/internal/snapshot"
	"elb/daemon/internal/steamjob"
	"elb/daemon/internal/structurebackup"
	"elb/daemon/internal/structurequeue"
	"elb/daemon/internal/structuresdb"
	"elb/daemon/internal/sysinfo"
	"elb/daemon/internal/tlsutil"
	"elb/daemon/internal/watchdog"
	"elb/daemon/internal/welcome"
)

func main() {

	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
		fmt.Println(identity.Version)
		return
	}

	if len(os.Args) > 2 && os.Args[1] == "scenario-cache" && os.Args[2] == "prepare" {
		runScenarioCachePrepare()
		return
	}
	cfg := config.Load()

	logBuf := logbuf.New(2000)

	log.SetFlags(0)
	log.SetOutput(io.MultiWriter(os.Stderr, logBuf))

	logBuf.SetPersist(logbuf.NewDailyFile(filepath.Join(cfg.DataRoot, "elb", "logs"), "daemon"))

	if err := tlsutil.EnsureCert(cfg.TLSCertFile, cfg.TLSKeyFile); err != nil {
		log.Fatalf("tls: %v", err)
	}

	if err := backpack.MigrateJSONFiles(cfg); err != nil {
		log.Printf("backpack: legacy JSON history migration: %v", err)
	}

	mod := modclient.New(cfg.ModAPI)

	go modclient.RunPlayersPoller(mod)
	go globaldb.RunPoller(cfg)

	go factions.RunPoller(cfg)
	go permissions.RunPoller(cfg, mod)

	go structuresdb.RunPoller(cfg)

	go snapshot.RunSizePoller(cfg)

	permissions.InitMuted(cfg.MutedFile)
	go backpack.RunPoller(cfg, mod)
	go backpack.RunCleanupPoller(cfg, mod)
	go auditcheck.RunPoller(cfg, mod)
	go chatlog.RunCapturePoller(cfg, mod)
	go hardwipe.RunPoller(cfg, mod)
	go history.RunPoller(cfg, mod)
	go presence.RunPoller(cfg, mod)
	go welcome.RunPoller(cfg, mod)
	go presencenotify.RunPoller(cfg, mod)
	go playerqueue.RunPoller(cfg, mod)
	go structurequeue.RunPoller(cfg, mod)
	go playerstats.RunPoller(cfg, mod)
	go restartqueue.RunPoller(cfg)
	go scriptqueue.RunPoller(cfg, mod)
	go scheduler.RunPoller(cfg, mod)

	go watchdog.RunWatchdog(cfg, func() error {
		_, err := mod.Players()
		return err
	})

	if s, err := settings.Load(cfg.SettingsFile); err != nil {
		log.Printf("watchdog: auditd: could not read settings at startup (default — enabled): %v", err)
		watchdog.SyncAuditRule(true)
	} else {
		watchdog.SyncAuditRule(s.Watchdog.LogHangsEnabled())
	}
	go logcleanup.RunPoller(cfg)
	go metrics.RunPoller(cfg, mod)
	go metrics.RunRetentionPoller(cfg)
	go steamjob.RunUpdateCheckPoller(cfg)
	go structurebackup.RunPoller(cfg, mod)
	go structurebackup.RunIdentityReconciler(cfg, mod)
	go sysinfo.RunSampler()
	safego.Go("identity.RunPoller", identity.RunPoller)

	safego.Go("main.icons.EnsureReady", func() {
		if err := icons.EnsureReady(cfg); err != nil {
			log.Printf("icons: preparation failed (request will retry): %v", err)
		}
	})

	if s, err := settings.Load(cfg.SettingsFile); err != nil {
		log.Printf("scenariocache: could not read settings at startup (embedded distribution server not started): %v", err)
	} else if s.ScenarioCache.Mode == "embedded" {
		go func() {
			addr := s.ScenarioCache.EmbeddedListenAddr
			if addr == "" {
				addr = cfg.ScenarioCacheListenAddr
			}

			limiter := scenariocache.NewIPRateLimiter(20, time.Minute)
			h := limiter.Middleware(http.FileServer(http.Dir(cfg.ScenarioCacheDir)))
			log.Printf("scenariocache: embedded distribution listening on http://%s", addr)
			if err := http.ListenAndServe(addr, h); err != nil {
				log.Printf("scenariocache: embedded distribution server: %v", err)
			}
		}()
	}

	log.Printf("elb-daemon: listening on https://%s, mod-api=%s, tokens=%d",
		cfg.ListenAddr, cfg.ModAPI, len(cfg.Tokens))

	err := http.ListenAndServeTLS(cfg.ListenAddr, cfg.TLSCertFile, cfg.TLSKeyFile, api.Router(cfg, logBuf))
	if err != nil {
		log.Fatal(err)
	}
}

func runScenarioCachePrepare() {
	dataRoot := envOrDefault("ELB_DATA_ROOT", "/server/EMP")
	elbHostDir := filepath.Join(dataRoot, "elb")
	cfg := config.Config{
		DedicatedYAML:    envOrDefault("ELB_DEDICATED_YAML", "/server/EMP/dedicated.yaml"),
		DataRoot:         dataRoot,
		ScenarioCacheDir: envOrDefault("ELB_SCENARIO_CACHE_DIR", filepath.Join(elbHostDir, "scenario-cache")),
		SettingsFile:     envOrDefault("ELB_SETTINGS_FILE", filepath.Join(elbHostDir, "settings.json")),
		NotifyDBFile:     envOrDefault("ELB_NOTIFY_DB_FILE", filepath.Join(elbHostDir, "notify.db")),
	}
	if err := os.MkdirAll(cfg.ScenarioCacheDir, 0o755); err != nil {
		log.Printf("scenariocache: prepare: mkdir %s: %v", cfg.ScenarioCacheDir, err)
		return
	}
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		log.Printf("scenariocache: prepare: loading settings: %v", err)
		return
	}
	scenariocache.Prepare(cfg, s.ScenarioCache)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
