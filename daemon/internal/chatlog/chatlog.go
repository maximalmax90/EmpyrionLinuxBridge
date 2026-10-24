package chatlog

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/factions"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/safego"
	"elb/daemon/internal/watchdog"

	_ "modernc.org/sqlite"
)

type Entry struct {
	Sender       string `json:"sender"`
	SteamId      string `json:"steamId,omitempty"`
	Text         string `json:"text"`
	GameTime     uint64 `json:"gameTime"`
	Timestamp    string `json:"timestamp,omitempty"`
	Channel      string `json:"channel,omitempty"`
	Target       string `json:"target,omitempty"`
	SessionStart bool   `json:"sessionStart,omitempty"`

	Priority string `json:"priority,omitempty"`
}

func PriorityName(prio int) string {
	switch prio {
	case 0:
		return "alarm"
	case 1:
		return "message"
	default:
		return "info"
	}
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS chat_sessions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	unit_timestamp TEXT NOT NULL UNIQUE,
	started_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS chat_log (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id INTEGER NOT NULL,
	sender TEXT NOT NULL,
	steam_id TEXT,
	text TEXT NOT NULL,
	game_time INTEGER NOT NULL,
	timestamp TEXT NOT NULL,
	channel TEXT,
	target TEXT,
	UNIQUE(session_id, timestamp, sender, text)
);
CREATE INDEX IF NOT EXISTS idx_chat_log_session_ts ON chat_log(session_id, timestamp);
`

func migrateAddColumn(db *sql.DB, table, ddl string) error {
	_, err := db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + ddl)
	if err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("migrate %s add column: %w", table, err)
	}
	return nil
}

var (
	dbMu     sync.Mutex
	dbByPath = map[string]*sql.DB{}
)

func getDB(cfg config.Config) (*sql.DB, error) {
	dbMu.Lock()
	defer dbMu.Unlock()

	if db, ok := dbByPath[cfg.ChatDBFile]; ok {
		return db, nil
	}

	if err := os.MkdirAll(filepath.Dir(cfg.ChatDBFile), 0o777); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(cfg.ChatDBFile), err)
	}
	db, err := sql.Open("sqlite", cfg.ChatDBFile)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", cfg.ChatDBFile, err)
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
	if err := migrateAddColumn(db, "chat_log", "priority TEXT"); err != nil {
		return nil, err
	}
	dbByPath[cfg.ChatDBFile] = db
	return db, nil
}

func Wipe(cfg config.Config) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM chat_log; DELETE FROM chat_sessions;`); err != nil {
		return err
	}
	_, err = db.Exec(`VACUUM;`)
	return err
}

func unitActiveEnterTimestamp(unit string) (string, error) {
	out, err := exec.Command("systemctl", "show", unit, "-p", "ActiveEnterTimestamp", "--value").Output()
	if err != nil {
		return "", fmt.Errorf("systemctl show %s: %w", unit, err)
	}
	ts := strings.TrimSpace(string(out))
	if ts == "" {
		return "", fmt.Errorf("systemctl show %s: empty ActiveEnterTimestamp (unit never started?)", unit)
	}
	return ts, nil
}

type dbtx interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

func ensureSession(db dbtx, unitTimestamp string) (int64, error) {
	if _, err := db.Exec(`INSERT OR IGNORE INTO chat_sessions (unit_timestamp, started_at) VALUES (?, ?)`,
		unitTimestamp, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return 0, fmt.Errorf("ensure session: %w", err)
	}
	var id int64
	if err := db.QueryRow(`SELECT id FROM chat_sessions WHERE unit_timestamp = ?`, unitTimestamp).Scan(&id); err != nil {
		return 0, fmt.Errorf("session id: %w", err)
	}
	return id, nil
}

var (
	sessionCacheMu sync.Mutex
	sessionCache   = map[string]struct {
		unitTimestamp string
		id            int64
	}{}
)

func cachedSessionID(db dbtx, dbPath, unitTimestamp string) (int64, error) {
	sessionCacheMu.Lock()
	if c, ok := sessionCache[dbPath]; ok && c.unitTimestamp == unitTimestamp {
		sessionCacheMu.Unlock()
		return c.id, nil
	}
	sessionCacheMu.Unlock()

	id, err := ensureSession(db, unitTimestamp)
	if err != nil {
		return 0, err
	}
	sessionCacheMu.Lock()
	sessionCache[dbPath] = struct {
		unitTimestamp string
		id            int64
	}{unitTimestamp, id}
	sessionCacheMu.Unlock()
	return id, nil
}

func insertEntry(db dbtx, sessionID int64, e modclient.ChatEntry) error {
	_, err := db.Exec(`INSERT OR IGNORE INTO chat_log (session_id, sender, steam_id, text, game_time, timestamp, channel, target)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, e.Sender, e.SteamId, e.Text, e.GameTime, e.Timestamp, e.Channel, e.Target)
	return err
}

func RunCapturePoller(cfg config.Config, mod *modclient.Client) {
	interval := time.Duration(cfg.ChatPollSeconds) * time.Second
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		if !watchdog.CachedHealth().Ready() {
			continue
		}
		safego.Tick("chatlog.RunCapturePoller", func() {
			if err := CaptureOnce(cfg, mod); err != nil {
				log.Printf("chatlog: capture: %v", err)
			}
		})
	}
}

func CaptureOnce(cfg config.Config, mod *modclient.Client) error {
	unitTS, err := unitActiveEnterTimestamp(cfg.SystemdUnit)
	if err != nil {
		return err
	}

	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	sessionID, err := cachedSessionID(db, cfg.ChatDBFile, unitTS)
	if err != nil {
		return err
	}

	entries, err := mod.RecentChat()
	if err != nil {
		return fmt.Errorf("mod chat: %w", err)
	}

	needsLookup := false
	for _, e := range entries {
		if e.Channel == "faction" && e.Target == "" && e.FactionId > 0 {
			needsLookup = true
			break
		}
	}
	if needsLookup {

		if list := factions.Cached(); list != nil {
			byID := make(map[int]string, len(list))
			for _, f := range list {
				byID[f.ID] = f.Abbreviation
			}
			for i := range entries {
				if entries[i].Channel == "faction" && entries[i].Target == "" && entries[i].FactionId > 0 {
					if tag, ok := byID[entries[i].FactionId]; ok {
						entries[i].Target = tag
					}
				}
			}
		}
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for _, e := range entries {
		if err := insertEntry(tx, sessionID, e); err != nil {
			log.Printf("chatlog: message write: %v", err)
		}
	}
	return tx.Commit()
}

func LogBanner(cfg config.Config, sender, text, target, priority string) error {
	unitTS, err := unitActiveEnterTimestamp(cfg.SystemdUnit)
	if err != nil {
		return err
	}
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	sessionID, err := cachedSessionID(db, cfg.ChatDBFile, unitTS)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT OR IGNORE INTO chat_log (session_id, sender, steam_id, text, game_time, timestamp, channel, target, priority)
		VALUES (?, ?, '', ?, 0, ?, 'banner', ?, ?)`,
		sessionID, sender, text, time.Now().UTC().Format(time.RFC3339Nano), target, priority)
	return err
}

func Last24Hours(cfg config.Config) ([]Entry, error) {
	db, err := getDB(cfg)
	if err != nil {
		return nil, err
	}
	since := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339Nano)

	rows, err := db.Query(`SELECT sender, steam_id, text, game_time, timestamp, channel, target, priority
		FROM chat_log WHERE timestamp >= ? ORDER BY timestamp ASC, id ASC`, since)
	if err != nil {
		return nil, fmt.Errorf("query chat_log: %w", err)
	}
	defer rows.Close()

	out, err := scanEntries(rows)
	if err != nil {
		return nil, err
	}

	sessRows, err := db.Query(`SELECT started_at FROM chat_sessions WHERE started_at >= ? ORDER BY started_at ASC`, since)
	if err != nil {
		return nil, fmt.Errorf("query chat_sessions: %w", err)
	}
	defer sessRows.Close()
	var dividers []Entry
	for sessRows.Next() {
		var startedAt string
		if err := sessRows.Scan(&startedAt); err != nil {
			return nil, fmt.Errorf("scan chat_sessions: %w", err)
		}
		dividers = append(dividers, Entry{Timestamp: startedAt, SessionStart: true})
	}
	if err := sessRows.Err(); err != nil {
		return nil, err
	}
	if len(dividers) == 0 {
		return out, nil
	}

	merged := make([]Entry, 0, len(out)+len(dividers))
	i, j := 0, 0
	for i < len(out) && j < len(dividers) {
		if out[i].Timestamp <= dividers[j].Timestamp {
			merged = append(merged, out[i])
			i++
		} else {
			merged = append(merged, dividers[j])
			j++
		}
	}
	merged = append(merged, out[i:]...)
	merged = append(merged, dividers[j:]...)
	return merged, nil
}

func scanEntries(rows *sql.Rows) ([]Entry, error) {
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var steamID, target, priority sql.NullString
		if err := rows.Scan(&e.Sender, &steamID, &e.Text, &e.GameTime, &e.Timestamp, &e.Channel, &target, &priority); err != nil {
			return nil, fmt.Errorf("scan chat_log: %w", err)
		}
		e.SteamId = steamID.String
		e.Target = target.String
		e.Priority = priority.String
		out = append(out, e)
	}
	return out, rows.Err()
}

type QueryFilter struct {
	FromTs string
	ToTs   string

	SteamId string

	Sender string

	Channel string

	Target string

	Limit  int
	Offset int
}

type QueryResult struct {
	Entries []Entry `json:"entries"`
	Total   int     `json:"total"`
}

func Query(cfg config.Config, f QueryFilter) (QueryResult, error) {
	db, err := getDB(cfg)
	if err != nil {
		return QueryResult{}, err
	}

	where := `timestamp >= ? AND timestamp <= ?`
	args := []any{f.FromTs, f.ToTs}
	if f.SteamId != "" {
		where += ` AND steam_id = ?`
		args = append(args, f.SteamId)
	} else if f.Sender != "" {
		where += ` AND sender LIKE ?`
		args = append(args, "%"+f.Sender+"%")
	}
	if f.Channel != "" {
		where += ` AND channel = ?`
		args = append(args, f.Channel)
	}
	if f.Target != "" {
		where += ` AND target LIKE ?`
		args = append(args, "%"+f.Target+"%")
	}

	var total int
	if err := db.QueryRow(`SELECT COUNT(*) FROM chat_log WHERE `+where, args...).Scan(&total); err != nil {
		return QueryResult{}, fmt.Errorf("count chat_log: %w", err)
	}

	limit := f.Limit
	if limit <= 0 {
		limit = total
	}
	pageArgs := append(append([]any{}, args...), limit, f.Offset)
	rows, err := db.Query(`SELECT sender, steam_id, text, game_time, timestamp, channel, target, priority
		FROM chat_log WHERE `+where+`
		ORDER BY timestamp DESC, id DESC LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return QueryResult{}, fmt.Errorf("query chat_log: %w", err)
	}
	defer rows.Close()
	entries, err := scanEntries(rows)
	if err != nil {
		return QueryResult{}, err
	}

	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return QueryResult{Entries: entries, Total: total}, nil
}
