package backpack

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"elb/daemon/internal/audit"
	"elb/daemon/internal/config"
	"elb/daemon/internal/fsowner"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/safego"
	"elb/daemon/internal/settings"
	"elb/daemon/internal/watchdog"

	_ "modernc.org/sqlite"
)

type Entry struct {
	Timestamp time.Time             `json:"timestamp"`
	Toolbelt  []modclient.ItemStack `json:"toolbelt"`
	Bag       []modclient.ItemStack `json:"bag"`
}

const defaultMaxEntriesPerPlayer = 300

const schemaSQL = `
CREATE TABLE IF NOT EXISTS backpack_history (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	steam_id TEXT NOT NULL,
	timestamp TEXT NOT NULL,
	toolbelt TEXT NOT NULL,
	bag TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_backpack_history_steam_ts ON backpack_history(steam_id, timestamp);
`

var (
	dbMu     sync.Mutex
	dbByPath = map[string]*sql.DB{}
)

func getDB(cfg config.Config) (*sql.DB, error) {
	dbMu.Lock()
	defer dbMu.Unlock()

	if db, ok := dbByPath[cfg.BackpackDBFile]; ok {
		return db, nil
	}

	if err := os.MkdirAll(filepath.Dir(cfg.BackpackDBFile), 0o777); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(cfg.BackpackDBFile), err)
	}
	db, err := sql.Open("sqlite", cfg.BackpackDBFile)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", cfg.BackpackDBFile, err)
	}

	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		return nil, fmt.Errorf("pragma busy_timeout: %w", err)
	}

	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		return nil, fmt.Errorf("pragma journal_mode: %w", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	dbByPath[cfg.BackpackDBFile] = db
	return db, nil
}

func Wipe(cfg config.Config) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM backpack_history;`); err != nil {
		return err
	}
	_, err = db.Exec(`VACUUM;`)
	return err
}

func History(cfg config.Config, steamID string) ([]Entry, error) {
	db, err := getDB(cfg)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT timestamp, toolbelt, bag FROM backpack_history WHERE steam_id = ? ORDER BY timestamp ASC`, steamID)
	if err != nil {
		return nil, fmt.Errorf("query history %s: %w", steamID, err)
	}
	defer rows.Close()

	var out []Entry
	for rows.Next() {
		var ts, tb, bag string
		if err := rows.Scan(&ts, &tb, &bag); err != nil {
			return nil, fmt.Errorf("scan history %s: %w", steamID, err)
		}
		e, err := parseEntry(ts, tb, bag)
		if err != nil {
			return nil, fmt.Errorf("parse history %s: %w", steamID, err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func parseEntry(ts, tb, bag string) (Entry, error) {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return Entry{}, fmt.Errorf("parse timestamp %q: %w", ts, err)
	}
	var e Entry
	e.Timestamp = t
	if err := json.Unmarshal([]byte(tb), &e.Toolbelt); err != nil {
		return Entry{}, fmt.Errorf("parse toolbelt: %w", err)
	}
	if err := json.Unmarshal([]byte(bag), &e.Bag); err != nil {
		return Entry{}, fmt.Errorf("parse bag: %w", err)
	}
	return e, nil
}

func latestEntry(db *sql.DB, steamID string) (*Entry, int64, error) {
	row := db.QueryRow(`SELECT id, timestamp, toolbelt, bag FROM backpack_history WHERE steam_id = ? ORDER BY timestamp DESC LIMIT 1`, steamID)
	var id int64
	var ts, tb, bag string
	if err := row.Scan(&id, &ts, &tb, &bag); err != nil {
		if err == sql.ErrNoRows {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	e, err := parseEntry(ts, tb, bag)
	if err != nil {
		return nil, 0, err
	}
	return &e, id, nil
}

func insertEntry(db *sql.DB, steamID string, e Entry) error {
	tb, err := json.Marshal(nonNil(e.Toolbelt))
	if err != nil {
		return err
	}
	bag, err := json.Marshal(nonNil(e.Bag))
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO backpack_history (steam_id, timestamp, toolbelt, bag) VALUES (?, ?, ?, ?)`,
		steamID, e.Timestamp.UTC().Format(time.RFC3339Nano), tb, bag)
	return err
}

func updateEntry(db *sql.DB, id int64, e Entry) error {
	tb, err := json.Marshal(nonNil(e.Toolbelt))
	if err != nil {
		return err
	}
	bag, err := json.Marshal(nonNil(e.Bag))
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE backpack_history SET timestamp = ?, toolbelt = ?, bag = ? WHERE id = ?`,
		e.Timestamp.UTC().Format(time.RFC3339Nano), tb, bag, id)
	return err
}

func nonNil(s []modclient.ItemStack) []modclient.ItemStack {
	if s == nil {
		return []modclient.ItemStack{}
	}
	return s
}

func pruneOld(db *sql.DB, steamID string, maxEntries int) error {
	if maxEntries <= 0 {
		maxEntries = defaultMaxEntriesPerPlayer
	}
	_, err := db.Exec(`DELETE FROM backpack_history WHERE steam_id = ? AND id NOT IN (
		SELECT id FROM backpack_history WHERE steam_id = ? ORDER BY timestamp DESC LIMIT ?
	)`, steamID, steamID, maxEntries)
	return err
}

func RunPoller(cfg config.Config, mod *modclient.Client) {
	interval := time.Duration(cfg.BackpackPollSeconds) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		if !watchdog.CachedHealth().Ready() {
			continue
		}
		safego.Tick("backpack.RunPoller", func() {
			if err := PollOnce(cfg, mod); err != nil {
				log.Printf("backpack: player poll: %v", err)
			}
		})
	}
}

func PollOnce(cfg config.Config, mod *modclient.Client) error {

	players := mod.CachedPlayers()

	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	minInterval := time.Duration(s.Backpack.MinHistoryIntervalSeconds) * time.Second
	if minInterval <= 0 {
		minInterval = time.Minute
	}
	maxEntries := s.Backpack.MaxHistoryEntries

	for _, p := range players {
		if !p.Online || p.SteamId == "" {
			continue
		}
		inv, err := mod.PlayerInventory(p.EntityId)
		if err != nil {

			if errors.Is(err, modclient.ErrInventoryBusy) {
				log.Printf("backpack: inventory for player %s (%s): %v, will retry next poll", p.Name, p.SteamId, err)
			} else {
				log.Printf("backpack: inventory for player %s (%s): %v", p.Name, p.SteamId, err)
			}
			continue
		}
		if err := appendIfNeeded(cfg, p.SteamId, inv, minInterval, maxEntries); err != nil {
			log.Printf("backpack: history write %s: %v", p.SteamId, err)
		}
	}
	return nil
}

func appendIfNeeded(cfg config.Config, steamID string, inv modclient.Inventory, minInterval time.Duration, maxEntries int) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}

	last, lastID, err := latestEntry(db, steamID)
	if err != nil {
		return err
	}

	now := time.Now()
	newEntry := Entry{Timestamp: now, Toolbelt: inv.Toolbelt, Bag: inv.Bag}

	if last == nil {
		if err := insertEntry(db, steamID, newEntry); err != nil {
			return err
		}
		return pruneOld(db, steamID, maxEntries)
	}

	if itemsEqualIgnoringWear(last.Toolbelt, inv.Toolbelt) && itemsEqualIgnoringWear(last.Bag, inv.Bag) {
		return nil
	}

	if last.Timestamp.Before(now) && now.Sub(last.Timestamp) < minInterval {

		return updateEntry(db, lastID, newEntry)
	}

	if err := insertEntry(db, steamID, newEntry); err != nil {
		return err
	}
	return pruneOld(db, steamID, maxEntries)
}

func itemsEqualIgnoringWear(a, b []modclient.ItemStack) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Id != b[i].Id || a[i].Count != b[i].Count || a[i].SlotIdx != b[i].SlotIdx {
			return false
		}
	}
	return true
}

func MigrateJSONFiles(cfg config.Config) error {
	entries, err := os.ReadDir(cfg.PlayerHistoryDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("readdir %s: %w", cfg.PlayerHistoryDir, err)
	}

	db, err := getDB(cfg)
	if err != nil {
		return err
	}

	maxEntries := defaultMaxEntriesPerPlayer
	if s, err := settings.Load(cfg.SettingsFile); err == nil {
		maxEntries = s.Backpack.MaxHistoryEntries
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		steamID := strings.TrimSuffix(name, ".json")
		path := filepath.Join(cfg.PlayerHistoryDir, name)

		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("backpack: migration %s: read: %v", path, err)
			continue
		}
		var old []Entry
		if err := json.Unmarshal(data, &old); err != nil {
			log.Printf("backpack: migration %s: parse: %v", path, err)
			continue
		}
		for _, e := range old {
			if err := insertEntry(db, steamID, e); err != nil {
				log.Printf("backpack: migration %s: DB write: %v", path, err)
			}
		}
		if err := pruneOld(db, steamID, maxEntries); err != nil {
			log.Printf("backpack: migration %s: prune: %v", path, err)
		}
		if err := os.Rename(path, path+".migrated"); err != nil {
			log.Printf("backpack: migration %s: rename after transfer: %v", path, err)
		}
		log.Printf("backpack: migrated %d history entries for %s from %s", len(old), steamID, path)
	}
	return nil
}

type cleanupRequest struct {
	SteamID  string `json:"steamId"`
	EntityID int    `json:"entityId"`
}

func cleanupQueueDir(cfg config.Config) string {
	return filepath.Join(filepath.Dir(cfg.SettingsFile), "wipe-cleanup-queue")
}

func RunCleanupPoller(cfg config.Config, mod *modclient.Client) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		safego.Tick("backpack.RunCleanupPoller", func() {
			if err := PollCleanupOnce(cfg, mod); err != nil {
				log.Printf("backpack: history cleanup after wipe: %v", err)
			}
		})
	}
}

func PollCleanupOnce(cfg config.Config, mod *modclient.Client) error {
	dir := cleanupQueueDir(cfg)

	if _, statErr := os.Stat(dir); statErr == nil {
		if fixErr := fsowner.FixTree(cfg, dir); fixErr != nil {
			log.Printf("backpack: history cleanup: could not set owner on %s: %v", dir, fixErr)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read queue dir %s: %w", dir, err)
	}

	db, err := getDB(cfg)
	if err != nil {
		return err
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("backpack: read request %s: %v", path, err)
			continue
		}
		var req cleanupRequest
		if err := json.Unmarshal(data, &req); err != nil || req.SteamID == "" {
			log.Printf("backpack: parse request %s: %v", path, err)
			continue
		}

		res, err := db.Exec(`DELETE FROM backpack_history WHERE steam_id = ?`, req.SteamID)
		if err != nil {
			log.Printf("backpack: delete history %s: %v", req.SteamID, err)
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			log.Printf("backpack: backpack history for %s cleared after wipe (%d entries)", req.SteamID, n)
		}
		checkWipeLeftoverStructures(cfg, mod, req)
		if err := os.Remove(path); err != nil {
			log.Printf("backpack: remove processed request %s: %v", path, err)
		}
	}
	return nil
}

func checkWipeLeftoverStructures(cfg config.Config, mod *modclient.Client, req cleanupRequest) {
	if req.EntityID == 0 {
		return
	}
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil || !s.Audit.WipeLeftover {
		return
	}
	list, err := mod.AllStructures(0)
	if err != nil {
		log.Printf("backpack: wipe leftover check: /structures/all: %v", err)
		return
	}
	count := 0
	for _, item := range list {
		st, ok := item.(map[string]any)
		if !ok {
			continue
		}
		factionGroup, _ := st["factionGroup"].(float64)
		factionID, _ := st["factionId"].(float64)
		if int(factionGroup) == 1 && int(factionID) == req.EntityID {
			count++
		}
	}
	if count == 0 {
		return
	}
	summary := fmt.Sprintf("after cb:wipe %s still has %d personal structures left", req.SteamID, count)
	if err := audit.Record(cfg, audit.CategoryWipeLeftover, req.SteamID, summary, ""); err != nil {
		log.Printf("audit: wipe leftover: %v", err)
	}
}

func FindEntry(cfg config.Config, steamID string, timestamp time.Time) (*Entry, error) {
	db, err := getDB(cfg)
	if err != nil {
		return nil, err
	}
	row := db.QueryRow(`SELECT timestamp, toolbelt, bag FROM backpack_history WHERE steam_id = ? AND timestamp = ?`,
		steamID, timestamp.UTC().Format(time.RFC3339Nano))
	var ts, tb, bag string
	if err := row.Scan(&ts, &tb, &bag); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("снимок с timestamp=%s для игрока %s не найден", timestamp.Format(time.RFC3339Nano), steamID)
		}
		return nil, fmt.Errorf("find entry %s: %w", steamID, err)
	}
	e, err := parseEntry(ts, tb, bag)
	if err != nil {
		return nil, err
	}
	return &e, nil
}
