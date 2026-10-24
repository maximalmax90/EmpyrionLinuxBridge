package auditcheck

import (
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"elb/daemon/internal/audit"
	"elb/daemon/internal/config"
	"elb/daemon/internal/itemmap"
	"elb/daemon/internal/itemstacks"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/safego"
	"elb/daemon/internal/settings"
	"elb/daemon/internal/watchdog"
)

const (
	pollInterval = 20 * time.Second

	maxWalkSpeedMps = 10000.0

	maxFuelPercent = 100.0
)

type lastSeen struct {
	pos Vec3Pos
	at  time.Time
}

type Vec3Pos struct{ X, Y, Z float64 }

func dist(a, b Vec3Pos) float64 {
	dx, dy, dz := a.X-b.X, a.Y-b.Y, a.Z-b.Z
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

var (
	mu            sync.Mutex
	lastPos       = map[string]lastSeen{}
	flaggedFuel   = map[int]bool{}
	flaggedStacks = map[string]int{}
)

func RunPoller(cfg config.Config, mod *modclient.Client) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for range ticker.C {
		safego.Tick("auditcheck.RunPoller", func() {
			if err := PollOnce(cfg, mod); err != nil {
				log.Printf("auditcheck: %v", err)
			}
		})
	}
}

func PollOnce(cfg config.Config, mod *modclient.Client) error {
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	if !s.Audit.PositionJump && !s.Audit.InventoryFuelAnomaly {
		return nil
	}

	if !watchdog.CachedHealth().Ready() {
		return nil
	}

	players := mod.CachedPlayers()

	if s.Audit.PositionJump {
		checkPositionJumps(cfg, players)
	}
	if s.Audit.InventoryFuelAnomaly {
		checkFuelAnomalies(cfg, mod)
		checkStackAnomalies(cfg, mod, players)
	}
	return nil
}

func checkPositionJumps(cfg config.Config, players []modclient.Player) {
	now := time.Now()
	mu.Lock()
	defer mu.Unlock()
	for _, p := range players {
		if !p.Online || p.Info == nil {
			continue
		}
		cur := Vec3Pos{p.Info.Pos.X, p.Info.Pos.Y, p.Info.Pos.Z}
		prev, ok := lastPos[p.SteamId]
		lastPos[p.SteamId] = lastSeen{pos: cur, at: now}
		if !ok {
			continue
		}
		dt := now.Sub(prev.at).Seconds()
		if dt <= 0 {
			continue
		}
		speed := dist(prev.pos, cur) / dt
		if speed <= maxWalkSpeedMps {
			continue
		}
		summary := fmt.Sprintf("sudden position jump for %s: %.0f m/s (over %.0fs)", p.Name, speed, dt)
		if err := audit.Record(cfg, audit.CategoryPositionJump, p.SteamId, summary, ""); err != nil {
			log.Printf("audit: position jump: %v", err)
		}
	}
}

func checkFuelAnomalies(cfg config.Config, mod *modclient.Client) {
	list, err := mod.AllStructures(modclient.AllStructuresMaxAge)
	if err != nil {
		log.Printf("auditcheck: /structures/all: %v", err)
		return
	}
	mu.Lock()
	defer mu.Unlock()
	seen := map[int]bool{}
	for _, item := range list {
		st, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, _ := st["id"].(float64)
		fuel, _ := st["fuel"].(float64)
		name, _ := st["name"].(string)
		seen[int(id)] = true
		if fuel <= maxFuelPercent {
			delete(flaggedFuel, int(id))
			continue
		}
		if flaggedFuel[int(id)] {
			continue
		}
		flaggedFuel[int(id)] = true
		summary := fmt.Sprintf("fuel of structure %q (id %d) exceeds 100%%: %.0f%%", name, int(id), fuel)
		if err := audit.Record(cfg, audit.CategoryInventoryFuelAnomaly, "", summary, ""); err != nil {
			log.Printf("audit: fuel anomaly: %v", err)
		}
	}

	for id := range flaggedFuel {
		if !seen[id] {
			delete(flaggedFuel, id)
		}
	}
}

func checkStackAnomalies(cfg config.Config, mod *modclient.Client, players []modclient.Player) {
	maxByID, err := stackSizeByItemID(cfg, mod)
	if err != nil {
		log.Printf("auditcheck: stack sizes: %v", err)
		return
	}
	if len(maxByID) == 0 {
		return
	}

	mu.Lock()
	defer mu.Unlock()
	for _, p := range players {
		if !p.Online {
			continue
		}
		inv, err := mod.PlayerInventory(p.EntityId)
		if err != nil {
			continue
		}
		all := append(append([]modclient.ItemStack{}, inv.Toolbelt...), inv.Bag...)
		for _, it := range all {
			max, ok := maxByID[it.Id]
			if !ok || it.Count <= max {
				continue
			}
			key := fmt.Sprintf("%s:%d", p.SteamId, it.Id)
			if flaggedStacks[key] == it.Count {
				continue
			}
			flaggedStacks[key] = it.Count
			summary := fmt.Sprintf("item stack id %d for %s exceeds max: %d (max %d)", it.Id, p.Name, it.Count, max)
			if err := audit.Record(cfg, audit.CategoryInventoryFuelAnomaly, p.SteamId, summary, ""); err != nil {
				log.Printf("audit: stack anomaly: %v", err)
			}
		}
	}
}

func stackSizeByItemID(cfg config.Config, mod *modclient.Client) (map[int]int, error) {
	var nameToID map[string]int
	if v, err := mod.Get("/items"); err == nil {
		if m, ok := v.(map[string]any); ok {
			nameToID = map[string]int{}
			for k, val := range m {
				if f, ok := val.(float64); ok {
					nameToID[k] = int(f)
				}
			}
		}
	}
	if len(nameToID) == 0 {
		path, err := cfg.BlocksmapPath()
		if err != nil {
			return nil, err
		}
		nameToID, err = itemmap.Read(path)
		if err != nil {
			return nil, err
		}
	}
	sizes, err := itemstacks.All(cfg)
	if err != nil {
		return nil, err
	}
	out := make(map[int]int, len(sizes))
	for name, size := range sizes {
		if id, ok := nameToID[name]; ok {
			out[id] = size
		}
	}
	return out, nil
}
