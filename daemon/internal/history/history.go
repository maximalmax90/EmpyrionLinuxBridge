package history

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/globaldb"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/safego"
	"elb/daemon/internal/structuresdb"

	_ "modernc.org/sqlite"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS history_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	ts TEXT NOT NULL,
	kind TEXT NOT NULL,
	entity_key TEXT NOT NULL,
	name TEXT NOT NULL,
	playfield TEXT NOT NULL,
	pos_x REAL NOT NULL,
	pos_y REAL NOT NULL,
	pos_z REAL NOT NULL,
	changed TEXT NOT NULL,
	is_first INTEGER NOT NULL DEFAULT 0,
	is_deleted INTEGER NOT NULL DEFAULT 0,
	only_position INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_history_kind_key_ts ON history_events(kind, entity_key, ts);
CREATE INDEX IF NOT EXISTS idx_history_ts ON history_events(ts);
`

const posEpsilon = 1.0

var (
	dbMu     sync.Mutex
	dbByPath = map[string]*sql.DB{}

	snapMu         sync.Mutex
	lastPlayers    = map[string]playerSnap{}
	lastStructures = map[string]structSnap{}
)

type vec3 struct{ X, Y, Z float64 }

func (a vec3) distanceTo(b vec3) float64 {
	dx, dy, dz := a.X-b.X, a.Y-b.Y, a.Z-b.Z
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

type playerSnap struct {
	Name      string
	Playfield string
	Pos       vec3

	Online       bool
	Kills        int
	Died         int
	Exp          int
	Credits      float64
	FactionID    int
	FactionGroup int
}

type structSnap struct {
	Name         string
	Playfield    string
	Pos          vec3
	CntBlocks    int
	CntDevices   int
	CntTriangles int
	CntLights    int
	Powered      bool
	Fuel         int
	FactionID    int
	FactionGroup int
}

func getDB(cfg config.Config) (*sql.DB, error) {
	dbMu.Lock()
	defer dbMu.Unlock()

	if db, ok := dbByPath[cfg.HistoryDBFile]; ok {
		return db, nil
	}
	if err := os.MkdirAll(filepath.Dir(cfg.HistoryDBFile), 0o777); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(cfg.HistoryDBFile), err)
	}
	db, err := sql.Open("sqlite", cfg.HistoryDBFile)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", cfg.HistoryDBFile, err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		return nil, fmt.Errorf("pragma busy_timeout: %w", err)
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		return nil, fmt.Errorf("pragma journal_mode: %w", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	db.SetMaxOpenConns(1)
	dbByPath[cfg.HistoryDBFile] = db
	return db, nil
}

func Wipe(cfg config.Config) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM history_events;`); err != nil {
		return err
	}
	_, err = db.Exec(`VACUUM;`)
	return err
}

func RunPoller(cfg config.Config, mod *modclient.Client) {

	seedSnapshots(cfg)

	interval := time.Duration(cfg.HistoryPollSeconds) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		safego.Tick("history.RunPoller", func() {
			if err := Tick(cfg, mod); err != nil {
				log.Printf("history: %v", err)
			}
		})
	}
}

func seedSnapshots(cfg config.Config) {
	db, err := getDB(cfg)
	if err != nil {
		log.Printf("history: seed snapshots: %v", err)
		return
	}
	if err := seedPlayers(db); err != nil {
		log.Printf("history: seed players: %v", err)
	}
	if err := seedStructures(db); err != nil {
		log.Printf("history: seed structures: %v", err)
	}
}

func seedPlayers(db *sql.DB) error {
	rows, err := db.Query(`SELECT entity_key, name, playfield, pos_x, pos_y, pos_z, changed FROM history_events WHERE kind = 'player' ORDER BY ts`)
	if err != nil {
		return err
	}
	defer rows.Close()

	seeded := map[string]playerSnap{}
	for rows.Next() {
		var key, name, playfield, changedRaw string
		var x, y, z float64
		if err := rows.Scan(&key, &name, &playfield, &x, &y, &z, &changedRaw); err != nil {
			return err
		}
		snap := seeded[key]
		snap.Name, snap.Playfield, snap.Pos = name, playfield, vec3{x, y, z}
		applyPlayerChanged(&snap, changedRaw)
		seeded[key] = snap
	}
	if err := rows.Err(); err != nil {
		return err
	}

	snapMu.Lock()
	for k, v := range seeded {
		lastPlayers[k] = v
	}
	snapMu.Unlock()
	return nil
}

func seedStructures(db *sql.DB) error {
	rows, err := db.Query(`SELECT entity_key, name, playfield, pos_x, pos_y, pos_z, changed, is_deleted FROM history_events WHERE kind = 'structure' ORDER BY ts`)
	if err != nil {
		return err
	}
	defer rows.Close()

	seeded := map[string]structSnap{}
	for rows.Next() {
		var key, name, playfield, changedRaw string
		var x, y, z float64
		var isDeleted int
		if err := rows.Scan(&key, &name, &playfield, &x, &y, &z, &changedRaw, &isDeleted); err != nil {
			return err
		}
		if isDeleted != 0 {

			delete(seeded, key)
			continue
		}
		snap := seeded[key]
		snap.Name, snap.Playfield, snap.Pos = name, playfield, vec3{x, y, z}
		applyStructureChanged(&snap, changedRaw)
		seeded[key] = snap
	}
	if err := rows.Err(); err != nil {
		return err
	}

	snapMu.Lock()
	for k, v := range seeded {
		lastStructures[k] = v
	}
	snapMu.Unlock()
	return nil
}

func applyPlayerChanged(snap *playerSnap, changedRaw string) {
	if changedRaw == "" || changedRaw == "{}" {
		return
	}
	var changed map[string]changedField
	if json.Unmarshal([]byte(changedRaw), &changed) != nil {
		return
	}
	if v, ok := changed["online"]; ok {
		if b, ok2 := v.New.(bool); ok2 {
			snap.Online = b
		}
	}
	if v, ok := changed["kills"]; ok {
		if f, ok2 := v.New.(float64); ok2 {
			snap.Kills = int(f)
		}
	}
	if v, ok := changed["died"]; ok {
		if f, ok2 := v.New.(float64); ok2 {
			snap.Died = int(f)
		}
	}
	if v, ok := changed["exp"]; ok {
		if f, ok2 := v.New.(float64); ok2 {
			snap.Exp = int(f)
		}
	}
	if v, ok := changed["credits"]; ok {
		if f, ok2 := v.New.(float64); ok2 {
			snap.Credits = f
		}
	}
	if v, ok := changed["factionId"]; ok {
		if f, ok2 := v.New.(float64); ok2 {
			snap.FactionID = int(f)
		}
	}
	if v, ok := changed["factionGroup"]; ok {
		if f, ok2 := v.New.(float64); ok2 {
			snap.FactionGroup = int(f)
		}
	}
}

func applyStructureChanged(snap *structSnap, changedRaw string) {
	if changedRaw == "" || changedRaw == "{}" {
		return
	}
	var changed map[string]changedField
	if json.Unmarshal([]byte(changedRaw), &changed) != nil {
		return
	}
	if v, ok := changed["cntBlocks"]; ok {
		if f, ok2 := v.New.(float64); ok2 {
			snap.CntBlocks = int(f)
		}
	}
	if v, ok := changed["cntDevices"]; ok {
		if f, ok2 := v.New.(float64); ok2 {
			snap.CntDevices = int(f)
		}
	}
	if v, ok := changed["cntTriangles"]; ok {
		if f, ok2 := v.New.(float64); ok2 {
			snap.CntTriangles = int(f)
		}
	}
	if v, ok := changed["cntLights"]; ok {
		if f, ok2 := v.New.(float64); ok2 {
			snap.CntLights = int(f)
		}
	}
	if v, ok := changed["powered"]; ok {
		if b, ok2 := v.New.(bool); ok2 {
			snap.Powered = b
		}
	}
	if v, ok := changed["fuel"]; ok {
		if f, ok2 := v.New.(float64); ok2 {
			snap.Fuel = int(f)
		}
	}
	if v, ok := changed["factionId"]; ok {
		if f, ok2 := v.New.(float64); ok2 {
			snap.FactionID = int(f)
		}
	}
	if v, ok := changed["factionGroup"]; ok {
		if f, ok2 := v.New.(float64); ok2 {
			snap.FactionGroup = int(f)
		}
	}
}

func Tick(cfg config.Config, mod *modclient.Client) error {

	players := mod.CachedPlayers()
	structures, err := structuresdb.All(cfg)
	if err != nil {
		return fmt.Errorf("structures: %w", err)
	}

	db, err := getDB(cfg)
	if err != nil {
		return err
	}

	offlineByEntity := globaldb.CachedOfflinePlayers()

	now := time.Now().UTC().Format(time.RFC3339Nano)

	snapMu.Lock()
	defer snapMu.Unlock()

	seenPlayers := make(map[string]bool, len(players))
	for _, p := range players {
		key := p.SteamId
		if key == "" {
			continue
		}
		seenPlayers[key] = true

		prev, existed := lastPlayers[key]

		snap := prev
		snap.Name = p.Name
		snap.Online = p.Online
		if p.Playfield != "" {
			snap.Playfield = p.Playfield
		}

		if p.Online && p.Info != nil {
			snap.Pos = vec3{p.Info.Pos.X, p.Info.Pos.Y, p.Info.Pos.Z}
			snap.Kills = p.Info.Kills
			snap.Died = p.Info.Died
			snap.Exp = p.Info.Exp
			snap.Credits = p.Info.Credits
			snap.FactionID = p.Info.FactionId
			snap.FactionGroup = p.Info.FactionGroup
		} else if off, ok := offlineByEntity[p.EntityId]; ok {
			if off.Kills != nil {
				snap.Kills = *off.Kills
			}
			if off.Died != nil {
				snap.Died = *off.Died
			}
			if off.Exp != nil {
				snap.Exp = *off.Exp
			}
			if off.Credits != nil {
				snap.Credits = float64(*off.Credits)
			}
			if off.FactionID != nil {
				snap.FactionID = *off.FactionID
			}
			if off.FactionGroup != nil {
				snap.FactionGroup = *off.FactionGroup
			}
			if off.Playfield != "" {
				snap.Playfield = off.Playfield
			}
			if off.Pos != nil {
				snap.Pos = vec3{off.Pos.X, off.Pos.Y, off.Pos.Z}
			}
		}

		if !existed {

			if snap.Online {
				if err := writeEvent(db, now, "player", key, snap.Name, snap.Playfield, snap.Pos, map[string]changedField{}, true, false); err != nil {
					log.Printf("history: write player %s: %v", key, err)
				}
			}
		} else {
			changed := diffPlayer(prev, snap)
			posChanged := prev.Pos.distanceTo(snap.Pos) > posEpsilon
			if len(changed) > 0 || posChanged {
				if err := writeEvent(db, now, "player", key, snap.Name, snap.Playfield, snap.Pos, changed, false, false); err != nil {
					log.Printf("history: write player %s: %v", key, err)
				}
			}
		}
		lastPlayers[key] = snap
	}

	seenStructures := make(map[string]bool, len(structures))
	for _, s := range structures {
		key := strconv.Itoa(s.ID)
		seenStructures[key] = true
		snap := structSnap{
			Name: s.Name, Playfield: s.Playfield,
			Pos:          vec3{s.Pos.X, s.Pos.Y, s.Pos.Z},
			CntBlocks:    s.CntBlocks,
			CntDevices:   s.CntDevices,
			CntTriangles: s.CntTriangles,
			CntLights:    s.CntLights,
			Powered:      s.Powered,
			Fuel:         s.Fuel,
			FactionID:    s.FactionID,
			FactionGroup: s.FactionGroup,
		}
		prev, existed := lastStructures[key]
		if !existed {
			if err := writeEvent(db, now, "structure", key, snap.Name, snap.Playfield, snap.Pos, map[string]changedField{}, true, false); err != nil {
				log.Printf("history: write structure %s: %v", key, err)
			}
		} else {
			changed := diffStructure(prev, snap)
			posChanged := prev.Pos.distanceTo(snap.Pos) > posEpsilon
			if len(changed) > 0 || posChanged {
				if err := writeEvent(db, now, "structure", key, snap.Name, snap.Playfield, snap.Pos, changed, false, false); err != nil {
					log.Printf("history: write structure %s: %v", key, err)
				}
			}
		}
		lastStructures[key] = snap
	}

	for key, prev := range lastStructures {
		if seenStructures[key] {
			continue
		}
		if err := writeEvent(db, now, "structure", key, prev.Name, prev.Playfield, prev.Pos, map[string]changedField{}, false, true); err != nil {
			log.Printf("history: write structure-deleted %s: %v", key, err)
		}
		delete(lastStructures, key)
	}

	return nil
}

type changedField struct {
	Old any `json:"old"`
	New any `json:"new"`
}

func diffPlayer(a, b playerSnap) map[string]changedField {
	out := map[string]changedField{}
	if a.Online != b.Online {
		out["online"] = changedField{a.Online, b.Online}
	}
	if a.Kills != b.Kills {
		out["kills"] = changedField{a.Kills, b.Kills}
	}
	if a.Died != b.Died {
		out["died"] = changedField{a.Died, b.Died}
	}
	if a.Exp != b.Exp {
		out["exp"] = changedField{a.Exp, b.Exp}
	}
	if a.Credits != b.Credits {
		out["credits"] = changedField{a.Credits, b.Credits}
	}
	if a.FactionID != b.FactionID || a.FactionGroup != b.FactionGroup {
		out["factionId"] = changedField{a.FactionID, b.FactionID}
		out["factionGroup"] = changedField{a.FactionGroup, b.FactionGroup}
	}
	if a.Playfield != b.Playfield {
		out["playfield"] = changedField{a.Playfield, b.Playfield}
	}
	return out
}

func diffStructure(a, b structSnap) map[string]changedField {
	out := map[string]changedField{}
	if a.CntBlocks != b.CntBlocks {
		out["cntBlocks"] = changedField{a.CntBlocks, b.CntBlocks}
	}
	if a.CntDevices != b.CntDevices {
		out["cntDevices"] = changedField{a.CntDevices, b.CntDevices}
	}
	if a.CntTriangles != b.CntTriangles {
		out["cntTriangles"] = changedField{a.CntTriangles, b.CntTriangles}
	}
	if a.CntLights != b.CntLights {
		out["cntLights"] = changedField{a.CntLights, b.CntLights}
	}
	if a.Powered != b.Powered {
		out["powered"] = changedField{a.Powered, b.Powered}
	}
	if a.Fuel != b.Fuel {
		out["fuel"] = changedField{a.Fuel, b.Fuel}
	}
	if a.FactionID != b.FactionID || a.FactionGroup != b.FactionGroup {
		out["factionId"] = changedField{a.FactionID, b.FactionID}
		out["factionGroup"] = changedField{a.FactionGroup, b.FactionGroup}
	}
	if a.Playfield != b.Playfield {
		out["playfield"] = changedField{a.Playfield, b.Playfield}
	}
	return out
}

func writeEvent(db *sql.DB, ts, kind, key, name, playfield string, pos vec3, changed map[string]changedField, isFirst, isDeleted bool) error {
	data, err := json.Marshal(changed)
	if err != nil {
		return err
	}
	onlyPosition := !isFirst && !isDeleted && len(changed) == 0
	_, err = db.Exec(`
INSERT INTO history_events (ts, kind, entity_key, name, playfield, pos_x, pos_y, pos_z, changed, is_first, is_deleted, only_position)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ts, kind, key, name, playfield, pos.X, pos.Y, pos.Z, string(data),
		boolToInt(isFirst), boolToInt(isDeleted), boolToInt(onlyPosition))
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
