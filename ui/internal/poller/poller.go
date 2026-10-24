package poller

import (
	"bytes"
	"html/template"
	"log"
	"sync"
	"time"

	"elb/ui/internal/daemonclient"
	"elb/ui/internal/hub"
	"elb/ui/internal/i18n"
	"elb/ui/internal/iconsync"
	"elb/ui/internal/itemsync"
	"elb/ui/internal/store"
)

type Poller struct {
	client *daemonclient.Client
	hub    *hub.Hub

	tmplByLocale map[string]*template.Template
	iconSyncer   *iconsync.Syncer
	store        *store.Store
	interval     time.Duration

	mu sync.Mutex

	lastHTML map[string][]byte

	lastHealth     daemonclient.Health
	lastManual     daemonclient.ManualAction
	lastSysInfo    daemonclient.SysInfo
	lastIdent      daemonclient.Identity
	lastPlayfields daemonclient.Playfields
	lastMounts     []daemonclient.MountInfo
	lastCfg        daemonclient.AppConfig

	lastItemGameName string
}

func New(client *daemonclient.Client, h *hub.Hub, tmplByLocale map[string]*template.Template, iconSyncer *iconsync.Syncer, st *store.Store, interval time.Duration) *Poller {
	lastItemGameName, err := st.ItemMappingSyncedGameName()
	if err != nil {
		log.Printf("poller: could not read last synced item-mapping GameName (starting from unknown): %v", err)
	}
	return &Poller{
		client:           client,
		hub:              h,
		tmplByLocale:     tmplByLocale,
		iconSyncer:       iconSyncer,
		store:            st,
		interval:         interval,
		lastHTML:         map[string][]byte{},
		lastItemGameName: lastItemGameName,
	}
}

func (p *Poller) Run() {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for range ticker.C {
		p.tick()
	}
}

func (p *Poller) tick() {

	var wg sync.WaitGroup

	var players []daemonclient.Player
	var errPlayers error
	wg.Add(1)
	go func() { defer wg.Done(); players, errPlayers = p.client.Players() }()

	var chat []daemonclient.ChatEntry
	var errChat error
	wg.Add(1)
	go func() { defer wg.Done(); chat, errChat = p.client.Chat() }()

	var health daemonclient.Health
	var errHealth error
	wg.Add(1)
	go func() { defer wg.Done(); health, errHealth = p.client.Health() }()

	var sysInfo daemonclient.SysInfo
	var errSys error
	wg.Add(1)
	go func() { defer wg.Done(); sysInfo, errSys = p.client.SysInfo() }()

	var ident daemonclient.Identity
	var errIdent error
	wg.Add(1)
	go func() { defer wg.Done(); ident, errIdent = p.client.Identity() }()

	var playfields daemonclient.Playfields
	var errPF error
	wg.Add(1)
	go func() { defer wg.Done(); playfields, errPF = p.client.Playfields() }()

	var mounts []daemonclient.MountInfo
	var errMounts error
	wg.Add(1)
	go func() { defer wg.Done(); mounts, errMounts = p.client.Mounts() }()

	var cfg daemonclient.AppConfig
	var errCfg error
	wg.Add(1)
	go func() { defer wg.Done(); cfg, errCfg = p.client.Config() }()

	var manual daemonclient.ManualAction
	var errManual error
	wg.Add(1)
	go func() { defer wg.Done(); manual, errManual = p.client.ManualActionStatus() }()

	wg.Wait()

	if errPlayers != nil {
		log.Printf("poller: players: %v", errPlayers)
	} else {
		p.renderAndBroadcast("players", "players_oob", players)
		p.renderAndBroadcast("players_count", "players_count_oob", players)
	}

	if errChat != nil {
		log.Printf("poller: chat: %v", errChat)
	} else {
		p.renderAndBroadcast("chat", "chat_oob", chat)
	}

	p.iconSyncer.Start()

	if errHealth != nil {
		log.Printf("poller: health: %v", errHealth)
		health = p.lastHealth
	} else {
		p.lastHealth = health
	}
	if errManual != nil {
		log.Printf("poller: manual action status: %v", errManual)
		manual = p.lastManual
	} else {
		p.lastManual = manual
	}
	if errSys != nil {
		log.Printf("poller: sysinfo: %v", errSys)
		sysInfo = p.lastSysInfo
	} else {
		p.lastSysInfo = sysInfo
	}
	if errIdent != nil {
		log.Printf("poller: identity: %v", errIdent)
		ident = p.lastIdent
	} else {
		p.lastIdent = ident
	}
	if errPF != nil {
		log.Printf("poller: playfields: %v", errPF)
		playfields = p.lastPlayfields
	} else {
		p.lastPlayfields = playfields
	}
	if errMounts != nil {
		log.Printf("poller: mounts: %v", errMounts)
		mounts = p.lastMounts
	} else {
		p.lastMounts = mounts
	}
	if errCfg != nil {
		log.Printf("poller: config: %v", errCfg)
		cfg = p.lastCfg
	} else {
		p.lastCfg = cfg
	}

	if cfg.Server.GameName.Set && cfg.Server.GameName.Value != p.lastItemGameName {

		adopted := false
		if p.lastItemGameName == "" {
			if n, cerr := p.store.ItemMappingCount(); cerr == nil && n > 0 {
				if aerr := p.store.AdoptItemMappingGameName(cfg.Server.GameName.Value); aerr != nil {
					log.Printf("poller: adopting item-mapping GameName: %v", aerr)
				} else {
					p.lastItemGameName = cfg.Server.GameName.Value
					adopted = true
				}
			}
		}
		if !adopted && itemsync.Sync(p.client, p.store, cfg.Server.GameName.Value) {
			p.lastItemGameName = cfg.Server.GameName.Value
		}
	}

	statusData := map[string]any{
		"Health":          health,
		"SysInfo":         sysInfo,
		"Identity":        ident,
		"Playfields":      playfields,
		"ManualAction":    manual,
		"ActiveMounts":    daemonclient.FilterMounts(mounts, cfg.Monitoring.EnabledMounts),
		"WarningPercent":  cfg.Monitoring.WarningPercent,
		"CriticalPercent": cfg.Monitoring.CriticalPercent,

		"DaemonUnreachable": p.client.Unreachable(),
	}
	p.renderAndBroadcast("statusbar", "statusbar_oob", statusData)
	p.renderAndBroadcast("sidebar_monitor", "sidebar_monitor_oob", statusData)
	p.renderAndBroadcast("topbar_ip", "topbar_ip_oob", statusData)
}

func (p *Poller) renderAndBroadcast(key, tmplName string, data any) {
	variants := make(map[string][]byte, len(p.tmplByLocale))
	for locale, tmpl := range p.tmplByLocale {
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, tmplName, data); err != nil {
			log.Printf("poller: render %s (%s): %v", tmplName, locale, err)
			return
		}
		variants[locale] = buf.Bytes()
	}

	sentinel := variants[i18n.Default]

	p.mu.Lock()
	unchanged := bytes.Equal(p.lastHTML[key], sentinel)
	if !unchanged {
		p.lastHTML[key] = append([]byte(nil), sentinel...)
	}
	p.mu.Unlock()

	if !unchanged {
		p.hub.Broadcast(variants, i18n.Default)
	}
}
