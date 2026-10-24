package presence

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/safego"

	_ "modernc.org/sqlite"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS player_presence (
	steam_id TEXT PRIMARY KEY,
	online_seconds REAL NOT NULL DEFAULT 0,
	last_online TEXT NOT NULL DEFAULT '',
	currently_online INTEGER NOT NULL DEFAULT 0
);
`

var (
	dbMu     sync.Mutex
	dbByPath = map[string]*sql.DB{}
)

func getDB(cfg config.Config) (*sql.DB, error) {
	dbMu.Lock()
	defer dbMu.Unlock()

	if db, ok := dbByPath[cfg.PresenceDBFile]; ok {
		return db, nil
	}

	if err := os.MkdirAll(filepath.Dir(cfg.PresenceDBFile), 0o777); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(cfg.PresenceDBFile), err)
	}
	db, err := sql.Open("sqlite", cfg.PresenceDBFile)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", cfg.PresenceDBFile, err)
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
	dbByPath[cfg.PresenceDBFile] = db
	return db, nil
}

func Wipe(cfg config.Config) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM player_presence;`); err != nil {
		return err
	}
	_, err = db.Exec(`VACUUM;`)
	return err
}

type Info struct {
	LastOnline    time.Time
	OnlineSeconds float64
}

func All(cfg config.Config) (map[string]Info, error) {
	db, err := getDB(cfg)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT steam_id, online_seconds, last_online FROM player_presence`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]Info{}
	for rows.Next() {
		var steamID, lastOnlineStr string
		var onlineSeconds float64
		if err := rows.Scan(&steamID, &onlineSeconds, &lastOnlineStr); err != nil {
			return nil, err
		}
		info := Info{OnlineSeconds: onlineSeconds}
		if t, err := time.Parse(time.RFC3339Nano, lastOnlineStr); err == nil {
			info.LastOnline = t
		}
		out[steamID] = info
	}
	return out, rows.Err()
}

func RunPoller(cfg config.Config, mod *modclient.Client) {
	interval := time.Duration(cfg.PresencePollSeconds) * time.Second
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		safego.Tick("presence.RunPoller", func() {
			if err := PollOnce(cfg, mod, interval); err != nil {
				log.Printf("presence: player poll: %v", err)
			}
		})
	}
}

func PollOnce(cfg config.Config, mod *modclient.Client, interval time.Duration) error {

	players := mod.CachedPlayers()
	db, err := getDB(cfg)
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, p := range players {
		if p.SteamId == "" {
			continue
		}
		if p.Online {
			_, err = db.Exec(`
				INSERT INTO player_presence (steam_id, online_seconds, last_online, currently_online)
				VALUES (?, ?, ?, 1)
				ON CONFLICT(steam_id) DO UPDATE SET
					online_seconds = online_seconds + excluded.online_seconds,
					last_online = excluded.last_online,
					currently_online = 1
			`, p.SteamId, interval.Seconds(), now)
		} else {
			_, err = db.Exec(`UPDATE player_presence SET currently_online = 0 WHERE steam_id = ?`, p.SteamId)
		}
		if err != nil {
			return fmt.Errorf("upsert %s: %w", p.SteamId, err)
		}
	}
	return nil
}
