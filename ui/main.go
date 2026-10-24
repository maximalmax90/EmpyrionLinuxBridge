package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"elb/ui/internal/config"
	"elb/ui/internal/daemonclient"
	"elb/ui/internal/hub"
	"elb/ui/internal/i18n"
	"elb/ui/internal/iconsync"
	"elb/ui/internal/itemsync"
	"elb/ui/internal/logbuf"
	"elb/ui/internal/poller"
	"elb/ui/internal/store"
	"elb/ui/internal/web"
)

func main() {

	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
		fmt.Println(web.UIVersion)
		return
	}

	uiLogBuf := logbuf.New(2000)

	log.SetFlags(0)
	log.SetOutput(io.MultiWriter(os.Stderr, uiLogBuf))

	cfg, err := config.Load("")
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	uiLogBuf.SetPersist(logbuf.NewDailyFile(filepath.Join(filepath.Dir(cfg.DBFile), "logs"), "webui"))

	if err := i18n.LoadExternalDir(cfg.LocalesDir); err != nil {
		log.Printf("i18n: %v", err)
	}

	st, err := store.Open(cfg.DBFile)
	if err != nil {
		log.Fatalf("store: %v", err)
	}

	daemon := daemonclient.New(cfg.DaemonURL, cfg.DaemonToken, cfg.DaemonCertFingerprint)
	h := hub.New()

	var ownListenPort int
	if _, portStr, err := net.SplitHostPort(cfg.ListenAddr); err == nil {
		ownListenPort, _ = strconv.Atoi(portStr)
	}

	var startupGameName string
	var scenarioCachePanelListenAddr string
	if appCfg, err := daemon.Config(); err != nil {
		log.Printf("config: could not read GameName for item-mapping sync (daemon unreachable?): %v", err)
	} else {
		if appCfg.Server.GameName.Set {
			startupGameName = appCfg.Server.GameName.Value
		}

		if appCfg.ScenarioCache.Mode == "panel" && appCfg.ScenarioCache.PanelListenPort != 0 && appCfg.ScenarioCache.PanelListenPort != ownListenPort {
			scenarioCachePanelListenAddr = fmt.Sprintf(":%d", appCfg.ScenarioCache.PanelListenPort)
		}
	}
	itemsync.Sync(daemon, st, startupGameName)

	iconSyncer := iconsync.New(daemon, st)

	srv, err := web.New(st, daemon, h, iconSyncer, uiLogBuf, cfg.TrustProxyHeaders, scenarioCachePanelListenAddr, ownListenPort)
	if err != nil {
		log.Fatalf("web: %v", err)
	}
	if scenarioCachePanelListenAddr != "" {
		go func() {
			log.Printf("elb-ui: scenario cache distribution listening on %s", scenarioCachePanelListenAddr)
			if err := http.ListenAndServe(scenarioCachePanelListenAddr, srv.ScenarioCacheRouter()); err != nil {
				log.Printf("elb-ui: scenario cache distribution server: %v", err)
			}
		}()
	}

	iconSyncer.Start()

	go poller.New(daemon, h, srv.TemplatesByLocale(), iconSyncer, st, time.Duration(cfg.PollIntervalSeconds)*time.Second).Run()

	log.Printf("elb-ui: listening on %s, daemon=%s", cfg.ListenAddr, cfg.DaemonURL)
	if err := http.ListenAndServe(cfg.ListenAddr, srv.Router()); err != nil {
		log.Fatal(err)
	}
}
