package notify

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"elb/daemon/internal/config"

	_ "modernc.org/sqlite"
)

type Level string

const (
	LevelInfo     Level = "info"
	LevelWarning  Level = "warning"
	LevelCritical Level = "critical"
)

type Notification struct {
	ID        int64     `json:"id"`
	Level     Level     `json:"level"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
	Read      bool      `json:"read"`
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS notifications (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	level TEXT NOT NULL,
	text TEXT NOT NULL,
	created_at TEXT NOT NULL,
	read INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_notifications_unread ON notifications(read);
`

const maxKept = 500

var (
	dbMu sync.Mutex
	db   *sql.DB
)

func getDB(cfg config.Config) (*sql.DB, error) {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db != nil {
		return db, nil
	}
	if err := os.MkdirAll(filepath.Dir(cfg.NotifyDBFile), 0o777); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(cfg.NotifyDBFile), err)
	}
	d, err := sql.Open("sqlite", cfg.NotifyDBFile)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", cfg.NotifyDBFile, err)
	}

	d.SetMaxOpenConns(1)
	if _, err := d.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		return nil, fmt.Errorf("pragma busy_timeout: %w", err)
	}
	if _, err := d.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		return nil, fmt.Errorf("pragma journal_mode: %w", err)
	}
	if _, err := d.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	db = d
	return db, nil
}

func Add(cfg config.Config, level Level, text string) error {
	d, err := getDB(cfg)
	if err != nil {
		return err
	}
	if _, err := d.Exec(`INSERT INTO notifications (level, text, created_at, read) VALUES (?, ?, ?, 0)`,
		level, text, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("insert: %w", err)
	}
	if _, err := d.Exec(`DELETE FROM notifications WHERE id NOT IN (SELECT id FROM notifications ORDER BY id DESC LIMIT ?)`, maxKept); err != nil {
		return fmt.Errorf("trim: %w", err)
	}
	return nil
}

func List(cfg config.Config, limit int) ([]Notification, error) {
	d, err := getDB(cfg)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > maxKept {
		limit = 50
	}
	rows, err := d.Query(`SELECT id, level, text, created_at, read FROM notifications ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	out := []Notification{}
	for rows.Next() {
		var n Notification
		var level, createdAt string
		var read int
		if err := rows.Scan(&n.ID, &level, &n.Text, &createdAt, &read); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		n.Level = Level(level)
		n.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		n.Read = read != 0
		out = append(out, n)
	}
	return out, rows.Err()
}

func UnreadCount(cfg config.Config) (int, error) {
	d, err := getDB(cfg)
	if err != nil {
		return 0, err
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM notifications WHERE read = 0`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	return n, nil
}

func MarkRead(cfg config.Config, id int64) error {
	d, err := getDB(cfg)
	if err != nil {
		return err
	}
	_, err = d.Exec(`UPDATE notifications SET read = 1 WHERE id = ?`, id)
	return err
}

func MarkAllRead(cfg config.Config) error {
	d, err := getDB(cfg)
	if err != nil {
		return err
	}
	_, err = d.Exec(`UPDATE notifications SET read = 1 WHERE read = 0`)
	return err
}

func ClearAll(cfg config.Config) error {
	d, err := getDB(cfg)
	if err != nil {
		return err
	}
	_, err = d.Exec(`DELETE FROM notifications`)
	return err
}
