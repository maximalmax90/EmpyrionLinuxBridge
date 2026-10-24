package iconsync

import (
	"log"
	"sync"
	"time"

	"elb/ui/internal/daemonclient"
	"elb/ui/internal/store"
)

type Status struct {
	Scenario string
	Running  bool
	Cached   int
	SyncedAt *time.Time
}

const minRecheckInterval = 30 * time.Second

type Syncer struct {
	daemon *daemonclient.Client
	store  *store.Store

	mu            sync.Mutex
	status        Status
	lastCheckedAt time.Time
}

func New(daemon *daemonclient.Client, st *store.Store) *Syncer {
	return &Syncer{daemon: daemon, store: st}
}

func (s *Syncer) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *Syncer) Start() {
	s.mu.Lock()
	if s.status.Running || time.Since(s.lastCheckedAt) < minRecheckInterval {
		s.mu.Unlock()
		return
	}
	s.status.Running = true
	s.lastCheckedAt = time.Now()
	s.mu.Unlock()

	go s.run()
}

func (s *Syncer) run() {
	manifestResp, err := s.daemon.IconManifest()
	if err != nil {
		s.finish("", err, false)
		return
	}
	scenario := manifestResp.Scenario

	have, err := s.store.IconNames(scenario)
	if err != nil {
		s.finish(scenario, err, false)
		return
	}

	var todo []daemonclient.IconInfo
	for _, icon := range manifestResp.Icons {
		if !have[icon.Name] {
			todo = append(todo, icon)
		}
	}

	s.mu.Lock()
	s.status.Scenario = scenario
	s.mu.Unlock()

	if len(todo) == 0 {

		s.finish(scenario, nil, false)
		return
	}
	log.Printf("iconsync: starting icon sync for scenario %s: %d new out of %d total", scenario, len(todo), len(manifestResp.Icons))

	failed := 0
	for _, icon := range todo {
		data, err := s.daemon.IconData(icon.Name)
		if err != nil {
			log.Printf("iconsync: %s: %v", icon.Name, err)
			failed++
			continue
		}
		if err := s.store.SaveIcon(scenario, icon.Name, data); err != nil {
			log.Printf("iconsync: saving %s: %v", icon.Name, err)
			failed++
			continue
		}
	}
	if failed > 0 {
		log.Printf("iconsync: %s: %d of %d icons failed to download (see log above)", scenario, failed, len(todo))
	}

	s.finish(scenario, nil, true)
}

func (s *Syncer) finish(scenario string, err error, didWork bool) {
	s.mu.Lock()
	s.status.Running = false
	if scenario != "" {
		s.status.Scenario = scenario
	}
	if err == nil {
		now := time.Now()
		s.status.SyncedAt = &now
	}
	if scenario != "" {
		if n, cerr := s.store.IconCount(scenario); cerr == nil {
			s.status.Cached = n
		}
	}
	cached := s.status.Cached
	s.mu.Unlock()

	switch {
	case err != nil:
		log.Printf("iconsync: sync failed: %v", err)
	case didWork && scenario != "":
		log.Printf("iconsync: icon sync for scenario %s complete, %d cached", scenario, cached)
	}
}
