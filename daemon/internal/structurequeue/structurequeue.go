package structurequeue

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/safego"
	"elb/daemon/internal/watchdog"

	_ "modernc.org/sqlite"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS pending_structure_edits (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	playfield TEXT NOT NULL,
	mod_path TEXT NOT NULL,
	payload TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pending_structure_edits_pf ON pending_structure_edits(playfield);
`

var (
	dbMu     sync.Mutex
	dbByPath = map[string]*sql.DB{}
)

func getDB(cfg config.Config) (*sql.DB, error) {
	dbMu.Lock()
	defer dbMu.Unlock()

	if db, ok := dbByPath[cfg.StructureQueueDBFile]; ok {
		return db, nil
	}
	if err := os.MkdirAll(filepath.Dir(cfg.StructureQueueDBFile), 0o777); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(cfg.StructureQueueDBFile), err)
	}
	db, err := sql.Open("sqlite", cfg.StructureQueueDBFile)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", cfg.StructureQueueDBFile, err)
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
	dbByPath[cfg.StructureQueueDBFile] = db
	return db, nil
}

func Wipe(cfg config.Config) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM pending_structure_edits;`); err != nil {
		return err
	}
	_, err = db.Exec(`VACUUM;`)
	return err
}

func PendingIDs(cfg config.Config) (map[int]bool, error) {
	db, err := getDB(cfg)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT DISTINCT payload FROM pending_structure_edits`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := map[int]bool{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var probe struct {
			Id int `json:"id"`
		}
		if err := json.Unmarshal([]byte(payload), &probe); err != nil {
			continue
		}
		ids[probe.Id] = true
	}
	return ids, rows.Err()
}

type PendingSummary struct {
	ID          int64
	Playfield   string
	ModPath     string
	StructureID int
	CreatedAt   time.Time
}

func ListAllPending(cfg config.Config) ([]PendingSummary, error) {
	db, err := getDB(cfg)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT id, playfield, mod_path, payload, created_at FROM pending_structure_edits ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PendingSummary
	for rows.Next() {
		var id int64
		var playfield, modPath, payload, createdAt string
		if err := rows.Scan(&id, &playfield, &modPath, &payload, &createdAt); err != nil {
			return nil, err
		}
		var probe struct {
			Id int `json:"id"`
		}
		_ = json.Unmarshal([]byte(payload), &probe)
		t, _ := time.Parse(time.RFC3339Nano, createdAt)
		out = append(out, PendingSummary{ID: id, Playfield: playfield, ModPath: modPath, StructureID: probe.Id, CreatedAt: t})
	}
	return out, rows.Err()
}

func DeleteByID(cfg config.Config, id int64) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	_, err = db.Exec(`DELETE FROM pending_structure_edits WHERE id = ?`, id)
	return err
}

func Enqueue(cfg config.Config, playfield, modPath string, payload []byte) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO pending_structure_edits (playfield, mod_path, payload, created_at) VALUES (?, ?, ?, ?)`,
		playfield, modPath, string(payload), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func RunPoller(cfg config.Config, mod *modclient.Client) {
	interval := time.Duration(cfg.StructureQueuePollSeconds) * time.Second
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		if !watchdog.CachedHealth().Ready() {
			continue
		}
		safego.Tick("structurequeue.RunPoller", func() {
			if err := ApplyDue(cfg, mod); err != nil {
				log.Printf("structurequeue: %v", err)
			}
		})
	}
}

func LoadedPlayfields(mod *modclient.Client) (map[string]bool, error) {
	raw, err := mod.Get("/playfield-servers")
	if err != nil {
		return nil, err
	}
	byPid, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected /playfield-servers format: %T", raw)
	}
	loaded := map[string]bool{}
	for _, names := range byPid {
		list, ok := names.([]any)
		if !ok {
			continue
		}
		for _, n := range list {
			if s, ok := n.(string); ok {
				loaded[s] = true
			}
		}
	}
	return loaded, nil
}

func ApplyDue(cfg config.Config, mod *modclient.Client) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}

	rows, err := db.Query(`SELECT DISTINCT playfield FROM pending_structure_edits`)
	if err != nil {
		return err
	}
	var pending []string
	for rows.Next() {
		var pf string
		if err := rows.Scan(&pf); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, pf)
	}
	rows.Close()
	if len(pending) == 0 {
		return nil
	}

	loaded, err := LoadedPlayfields(mod)
	if err != nil {
		return fmt.Errorf("loaded playfields list: %w", err)
	}

	for _, pf := range pending {
		if !loaded[pf] {
			continue
		}
		if err := applyForPlayfield(mod, db, pf); err != nil {
			log.Printf("structurequeue: apply for playfield %q: %v (request stays queued for retry)", pf, err)
		}
	}
	return nil
}

func applyForPlayfield(mod *modclient.Client, db *sql.DB, playfield string) error {
	rows, err := db.Query(`SELECT id, mod_path, payload FROM pending_structure_edits WHERE playfield = ? ORDER BY id`, playfield)
	if err != nil {
		return err
	}
	type entry struct {
		id      int64
		modPath string
		payload string
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.id, &e.modPath, &e.payload); err != nil {
			rows.Close()
			return err
		}
		entries = append(entries, e)
	}
	rows.Close()

	for _, e := range entries {
		if _, err := mod.Post(e.modPath, json.RawMessage(e.payload)); err != nil {
			return fmt.Errorf("%s (request %d): %w", e.modPath, e.id, err)
		}
		if _, err := db.Exec(`DELETE FROM pending_structure_edits WHERE id = ?`, e.id); err != nil {
			return fmt.Errorf("delete request %d: %w", e.id, err)
		}
		log.Printf("structurequeue: applied request %s for playfield %s", e.modPath, playfield)
	}
	return nil
}
