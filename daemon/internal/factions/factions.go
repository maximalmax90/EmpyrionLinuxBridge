package factions

import (
	"fmt"
	"log"
	"regexp"
	"sync"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/safego"
	"elb/daemon/internal/telnet"
	"elb/daemon/internal/watchdog"
)

type Faction struct {
	ID           int    `json:"id"`
	Abbreviation string `json:"abbreviation"`
	Name         string `json:"name"`
}

var (
	statsRe = regexp.MustCompile(`\[(\S+)\s+(\d+)\]:\s*(\d+)/(\d+)`)
	listRe  = regexp.MustCompile(`\[(.+?)\]\s+(\S+)`)
)

const telnetTimeout = 15 * time.Second

func List(cfg config.Config) ([]Faction, error) {
	addr, pwd, err := cfg.TelnetCreds()
	if err != nil {
		return nil, err
	}
	tn := telnet.New(addr, pwd)

	outs, err := tn.Commands([]string{"faction stats", "faction list"}, telnetTimeout)
	if err != nil {
		return nil, fmt.Errorf("faction stats/list: %w", err)
	}
	statsOut, listOut := outs[0], outs[1]

	names := map[string]string{}
	for _, m := range listRe.FindAllStringSubmatch(listOut, -1) {
		names[m[1]] = m[2]
	}

	var out []Faction
	for _, m := range statsRe.FindAllStringSubmatch(statsOut, -1) {
		abbrev := m[1]
		var id int
		fmt.Sscanf(m[2], "%d", &id)
		name := names[abbrev]
		if name == "" {
			name = abbrev
		}
		out = append(out, Faction{ID: id, Abbreviation: abbrev, Name: name})
	}
	return out, nil
}

const pollInterval = 15 * time.Second

var (
	bgCacheMu sync.RWMutex
	bgCache   []Faction // nil до первого успешного опроса
)

func Cached() []Faction {
	bgCacheMu.RLock()
	defer bgCacheMu.RUnlock()
	return bgCache
}

func RunPoller(cfg config.Config) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if !watchdog.CachedHealth().Ready() {
			<-ticker.C
			continue
		}
		safego.Tick("factions.RunPoller", func() {
			list, err := List(cfg)
			if err != nil {
				log.Printf("factions: poll: %v", err)
			} else {
				bgCacheMu.Lock()
				bgCache = list
				bgCacheMu.Unlock()
			}
		})
		<-ticker.C
	}
}
