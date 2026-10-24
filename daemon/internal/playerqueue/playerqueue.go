package playerqueue

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/notify"
	"elb/daemon/internal/safego"
	"elb/daemon/internal/settings"
	"elb/daemon/internal/watchdog"

	_ "modernc.org/sqlite"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS pending_edits (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	steam_id TEXT NOT NULL,
	mod_path TEXT NOT NULL,
	payload TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pending_edits_steam ON pending_edits(steam_id);
`

var (
	dbMu     sync.Mutex
	dbByPath = map[string]*sql.DB{}
)

func getDB(cfg config.Config) (*sql.DB, error) {
	dbMu.Lock()
	defer dbMu.Unlock()

	if db, ok := dbByPath[cfg.PlayerQueueDBFile]; ok {
		return db, nil
	}
	if err := os.MkdirAll(filepath.Dir(cfg.PlayerQueueDBFile), 0o777); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(cfg.PlayerQueueDBFile), err)
	}
	db, err := sql.Open("sqlite", cfg.PlayerQueueDBFile)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", cfg.PlayerQueueDBFile, err)
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
	dbByPath[cfg.PlayerQueueDBFile] = db
	return db, nil
}

func Wipe(cfg config.Config) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM pending_edits;`); err != nil {
		return err
	}
	_, err = db.Exec(`VACUUM;`)
	return err
}

var replaceableModPaths = map[string]bool{
	"/players/inventory": true,
	"/players/setinfo":   true,

	"/players/teleport": true,
}

func Enqueue(cfg config.Config, steamID, modPath string, payload []byte) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)

	if !replaceableModPaths[modPath] {
		_, err = db.Exec(`INSERT INTO pending_edits (steam_id, mod_path, payload, created_at) VALUES (?, ?, ?, ?)`,
			steamID, modPath, string(payload), createdAt)
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("начало транзакции: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM pending_edits WHERE steam_id = ? AND mod_path = ?`, steamID, modPath); err != nil {
		return fmt.Errorf("замена прежней заявки: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO pending_edits (steam_id, mod_path, payload, created_at) VALUES (?, ?, ?, ?)`,
		steamID, modPath, string(payload), createdAt); err != nil {
		return err
	}
	return tx.Commit()
}

func friendlyGroupName(modPath string) string {
	return strings.TrimPrefix(modPath, "/players/")
}

type PendingSummary struct {
	ID        int64
	SteamID   string
	ModPath   string
	CreatedAt time.Time
}

func ListAllPending(cfg config.Config) ([]PendingSummary, error) {
	db, err := getDB(cfg)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT id, steam_id, mod_path, created_at FROM pending_edits ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PendingSummary
	for rows.Next() {
		var id int64
		var steamID, modPath, createdAt string
		if err := rows.Scan(&id, &steamID, &modPath, &createdAt); err != nil {
			return nil, err
		}
		t, _ := time.Parse(time.RFC3339Nano, createdAt)
		out = append(out, PendingSummary{ID: id, SteamID: steamID, ModPath: modPath, CreatedAt: t})
	}
	return out, rows.Err()
}

func DeleteByID(cfg config.Config, id int64) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	_, err = db.Exec(`DELETE FROM pending_edits WHERE id = ?`, id)
	return err
}

func FriendlyGroupName(modPath string) string {
	return friendlyGroupName(modPath)
}

func AllPending(cfg config.Config, steamID string) (map[string]json.RawMessage, error) {
	db, err := getDB(cfg)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT mod_path, payload FROM pending_edits WHERE steam_id = ?`, steamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]json.RawMessage{}
	for rows.Next() {
		var modPath, payload string
		if err := rows.Scan(&modPath, &payload); err != nil {
			return nil, err
		}
		if !replaceableModPaths[modPath] {
			continue
		}
		out[friendlyGroupName(modPath)] = json.RawMessage(payload)
	}
	return out, rows.Err()
}

func RunPoller(cfg config.Config, mod *modclient.Client) {
	interval := time.Duration(cfg.PlayerQueuePollSeconds) * time.Second
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		if !watchdog.CachedHealth().Ready() {
			continue
		}
		safego.Tick("playerqueue.RunPoller", func() {
			if err := ApplyDue(cfg, mod); err != nil {
				log.Printf("playerqueue: %v", err)
			}
		})
	}
}

func ApplyDue(cfg config.Config, mod *modclient.Client) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}

	rows, err := db.Query(`SELECT DISTINCT steam_id FROM pending_edits`)
	if err != nil {
		return err
	}
	var pending []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, s)
	}
	rows.Close()
	if len(pending) == 0 {
		return nil
	}

	players := mod.CachedPlayers()
	online := map[string]bool{}
	for _, p := range players {
		if p.Online {
			online[p.SteamId] = true
		}
	}

	for _, steamID := range pending {
		if !online[steamID] {
			continue
		}
		if err := applyForPlayer(cfg, mod, db, steamID); err != nil {
			log.Printf("playerqueue: apply for %s: %v (request stays queued for retry)", steamID, err)
		}
	}
	return nil
}

func applyForPlayer(cfg config.Config, mod *modclient.Client, db *sql.DB, steamID string) error {
	rows, err := db.Query(`SELECT id, mod_path, payload FROM pending_edits WHERE steam_id = ? ORDER BY id`, steamID)
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
		applied := true
		if e.modPath == "/players/inventory" {
			var aerr error
			applied, aerr = ApplyInventoryEdit(cfg, mod, []byte(e.payload))
			if aerr != nil {
				return fmt.Errorf("%s (request %d): %w", e.modPath, e.id, aerr)
			}
		} else if _, err := mod.Post(e.modPath, json.RawMessage(e.payload)); err != nil {
			return fmt.Errorf("%s (request %d): %w", e.modPath, e.id, err)
		}
		if _, err := db.Exec(`DELETE FROM pending_edits WHERE id = ?`, e.id); err != nil {
			return fmt.Errorf("delete request %d: %w", e.id, err)
		}
		if applied {
			log.Printf("playerqueue: applied request %s for %s", e.modPath, steamID)
		} else {

			log.Printf("playerqueue: inventory request (id=%d) for %s cancelled without applying (conflict)", e.id, steamID)
		}
	}
	return nil
}

func ApplyInventoryEdit(cfg config.Config, mod *modclient.Client, raw []byte) (applied bool, err error) {
	var p modclient.InventoryEditPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return false, fmt.Errorf("parse inventory request: %w", err)
	}

	toolbelt, bag := p.Toolbelt, p.Bag

	if p.Part == "toolbelt" || p.Part == "bag" {
		current, cerr := mod.PlayerInventory(p.PlayerId)
		if cerr != nil {
			return false, fmt.Errorf("current inventory: %w", cerr)
		}
		if p.Part == "toolbelt" {
			bag = current.Bag
		} else {
			toolbelt = current.Toolbelt
		}
	}

	if p.BaselineToolbelt != nil || p.BaselineBag != nil {
		current, ferr := mod.PlayerInventory(p.PlayerId)

		if ferr == nil && (!itemStacksEqual(current.Toolbelt, p.BaselineToolbelt) || !itemStacksEqual(current.Bag, p.BaselineBag)) {
			s, serr := settings.Load(cfg.SettingsFile)
			if serr != nil {
				return false, fmt.Errorf("settings: %w", serr)
			}
			if !s.Backpack.ForceOverwriteInventory {
				if nerr := notify.Add(cfg, notify.LevelWarning, fmt.Sprintf(
					"Правки инвентаря для %s не применены — инвентарь изменился с момента редактирования (принудительная перезапись выключена в Настройки → Панель)", p.SteamId)); nerr != nil {
					log.Printf("playerqueue: notify: %v", nerr)
				}
				return false, nil
			}
			log.Printf("playerqueue: inventory for %s changed since editing — force overwrite is on, applying as-is", p.SteamId)
		}
	}

	if err := mod.SetPlayerInventory(p.PlayerId, toolbelt, bag); err != nil {
		return false, err
	}
	return true, nil
}

func itemStacksEqual(a, b []modclient.ItemStack) bool {
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
