package audit

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"elb/daemon/internal/config"
)

type Category string

const (
	CategoryUnauthorizedAPIRequests Category = "unauthorizedApiRequests"
	CategoryInventoryFuelAnomaly    Category = "inventoryFuelAnomaly"
	CategoryWipeLeftover            Category = "wipeLeftover"
	CategoryPositionJump            Category = "positionJump"
	CategoryPermissionFactionChange Category = "permissionFactionChange"
	CategoryLoginTracking           Category = "loginTracking"
)

type Event struct {
	ID       int64     `json:"id"`
	Ts       time.Time `json:"ts"`
	Category Category  `json:"category"`
	Player   string    `json:"player,omitempty"`
	Summary  string    `json:"summary"`
	Details  string    `json:"details,omitempty"`
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS audit_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	ts TEXT NOT NULL,
	category TEXT NOT NULL,
	player TEXT NOT NULL DEFAULT '',
	summary TEXT NOT NULL,
	details TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_audit_events_ts ON audit_events(ts);
`

var (
	dbMu     sync.Mutex
	dbByPath = map[string]*sql.DB{}
)

func getDB(cfg config.Config) (*sql.DB, error) {
	dbMu.Lock()
	defer dbMu.Unlock()

	if db, ok := dbByPath[cfg.AuditDBFile]; ok {
		return db, nil
	}
	if err := os.MkdirAll(filepath.Dir(cfg.AuditDBFile), 0o777); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(cfg.AuditDBFile), err)
	}
	db, err := sql.Open("sqlite", cfg.AuditDBFile)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", cfg.AuditDBFile, err)
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
	dbByPath[cfg.AuditDBFile] = db
	return db, nil
}

func Record(cfg config.Config, category Category, player, summary, details string) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO audit_events (ts, category, player, summary, details) VALUES (?, ?, ?, ?, ?)`,
		time.Now().UTC().Format(time.RFC3339Nano), string(category), player, summary, details)
	return err
}

func Recent(cfg config.Config, n int) ([]Event, error) {
	db, err := getDB(cfg)
	if err != nil {
		return nil, err
	}
	q := `SELECT id, ts, category, player, summary, details FROM audit_events ORDER BY id DESC`
	args := []any{}
	if n > 0 {
		q += ` LIMIT ?`
		args = append(args, n)
	}
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

func Between(cfg config.Config, from, to string, n int) ([]Event, error) {
	db, err := getDB(cfg)
	if err != nil {
		return nil, err
	}
	q := `SELECT id, ts, category, player, summary, details FROM audit_events WHERE ts >= ? AND ts < ? ORDER BY id DESC`
	args := []any{from, to}
	if n > 0 {
		q += ` LIMIT ?`
		args = append(args, n)
	}
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

func scanEvents(rows *sql.Rows) ([]Event, error) {
	var out []Event
	for rows.Next() {
		var e Event
		var ts, category string
		if err := rows.Scan(&e.ID, &ts, &category, &e.Player, &e.Summary, &e.Details); err != nil {
			return nil, err
		}
		e.Ts, _ = time.Parse(time.RFC3339Nano, ts)
		e.Category = Category(category)
		out = append(out, e)
	}
	return out, rows.Err()
}
