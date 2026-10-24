package playerstats

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
	"elb/daemon/internal/safego"

	_ "modernc.org/sqlite"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS player_stats (
	steam_id TEXT PRIMARY KEY,
	health REAL NOT NULL, health_max REAL NOT NULL,
	food REAL NOT NULL, food_max REAL NOT NULL,
	oxygen REAL NOT NULL, oxygen_max REAL NOT NULL,
	stamina REAL NOT NULL, stamina_max REAL NOT NULL,
	radiation REAL NOT NULL, radiation_max REAL NOT NULL,
	body_temp REAL NOT NULL, body_temp_max REAL NOT NULL,
	faction_group INTEGER NOT NULL, faction_id INTEGER NOT NULL, faction_role INTEGER NOT NULL,
	credits REAL NOT NULL, exp INTEGER NOT NULL, upgrade INTEGER NOT NULL,
	updated_at TEXT NOT NULL
);
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

	if db, ok := dbByPath[cfg.PlayerStatsDBFile]; ok {
		return db, nil
	}
	if err := os.MkdirAll(filepath.Dir(cfg.PlayerStatsDBFile), 0o777); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(cfg.PlayerStatsDBFile), err)
	}
	db, err := sql.Open("sqlite", cfg.PlayerStatsDBFile)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", cfg.PlayerStatsDBFile, err)
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

	if err := migrateAddColumn(db, "player_stats", "bp_remaining_time REAL NOT NULL DEFAULT 0"); err != nil {
		return nil, err
	}
	if err := migrateAddColumn(db, "player_stats", "bp_in_factory TEXT NOT NULL DEFAULT ''"); err != nil {
		return nil, err
	}

	if err := migrateAddColumn(db, "player_stats", "bp_resources_in_factory TEXT NOT NULL DEFAULT '{}'"); err != nil {
		return nil, err
	}

	if err := migrateAddColumn(db, "player_stats", "kills INTEGER NOT NULL DEFAULT 0"); err != nil {
		return nil, err
	}
	if err := migrateAddColumn(db, "player_stats", "died INTEGER NOT NULL DEFAULT 0"); err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	dbByPath[cfg.PlayerStatsDBFile] = db
	return db, nil
}

func Wipe(cfg config.Config) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM player_stats;`); err != nil {
		return err
	}
	_, err = db.Exec(`VACUUM;`)
	return err
}

type Snapshot struct {
	Health, HealthMax                    float64
	Food, FoodMax                        float64
	Oxygen, OxygenMax                    float64
	Stamina, StaminaMax                  float64
	Radiation, RadiationMax              float64
	BodyTemp, BodyTempMax                float64
	FactionGroup, FactionId, FactionRole int
	Credits                              float64
	Exp, Upgrade                         int

	BpRemainingTime float64
	BpInFactory     string

	BpResourcesInFactory map[string]float64

	Kills, Died int
	UpdatedAt   time.Time
}

func Get(cfg config.Config, steamID string) (Snapshot, bool, error) {
	db, err := getDB(cfg)
	if err != nil {
		return Snapshot{}, false, err
	}
	var s Snapshot
	var updatedAt, resourcesJSON string
	err = db.QueryRow(`
		SELECT health, health_max, food, food_max, oxygen, oxygen_max,
		       stamina, stamina_max, radiation, radiation_max, body_temp, body_temp_max,
		       faction_group, faction_id, faction_role, credits, exp, upgrade,
		       bp_remaining_time, bp_in_factory, bp_resources_in_factory, kills, died, updated_at
		FROM player_stats WHERE steam_id = ?`, steamID).Scan(
		&s.Health, &s.HealthMax, &s.Food, &s.FoodMax, &s.Oxygen, &s.OxygenMax,
		&s.Stamina, &s.StaminaMax, &s.Radiation, &s.RadiationMax, &s.BodyTemp, &s.BodyTempMax,
		&s.FactionGroup, &s.FactionId, &s.FactionRole, &s.Credits, &s.Exp, &s.Upgrade,
		&s.BpRemainingTime, &s.BpInFactory, &resourcesJSON, &s.Kills, &s.Died, &updatedAt)
	if err == sql.ErrNoRows {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, err
	}
	if t, perr := time.Parse(time.RFC3339Nano, updatedAt); perr == nil {
		s.UpdatedAt = t
	}
	_ = json.Unmarshal([]byte(resourcesJSON), &s.BpResourcesInFactory)
	return s, true, nil
}

type KillsDied struct{ Kills, Died int }

var (
	killsDiedMu   sync.RWMutex
	killsDiedData = map[string]KillsDied{} // steamId -> последний известный снимок
)

func CachedKillsDied() map[string]KillsDied {
	killsDiedMu.RLock()
	defer killsDiedMu.RUnlock()
	out := make(map[string]KillsDied, len(killsDiedData))
	for k, v := range killsDiedData {
		out[k] = v
	}
	return out
}

func refreshKillsDiedCache(cfg config.Config) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	rows, err := db.Query(`SELECT steam_id, kills, died FROM player_stats`)
	if err != nil {
		return fmt.Errorf("select kills/died: %w", err)
	}
	defer rows.Close()
	next := map[string]KillsDied{}
	for rows.Next() {
		var steamID string
		var kd KillsDied
		if err := rows.Scan(&steamID, &kd.Kills, &kd.Died); err != nil {
			return fmt.Errorf("scan kills/died: %w", err)
		}
		next[steamID] = kd
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("kills/died rows: %w", err)
	}
	killsDiedMu.Lock()
	killsDiedData = next
	killsDiedMu.Unlock()
	return nil
}

func RunPoller(cfg config.Config, mod *modclient.Client) {
	interval := time.Duration(cfg.PlayerStatsPollSeconds) * time.Second
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		safego.Tick("playerstats.RunPoller", func() {
			if err := PollOnce(cfg, mod); err != nil {
				log.Printf("playerstats: player poll: %v", err)
			}
		})
	}
}

func PollOnce(cfg config.Config, mod *modclient.Client) error {

	players := mod.CachedPlayers()
	db, err := getDB(cfg)
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, p := range players {
		if !p.Online || p.SteamId == "" || p.Info == nil {
			continue
		}
		info := p.Info
		resourcesJSON, err := json.Marshal(info.BpResourcesInFactory)
		if err != nil {
			return fmt.Errorf("marshal bpResourcesInFactory %s: %w", p.SteamId, err)
		}
		_, err = db.Exec(`
			INSERT INTO player_stats (
				steam_id, health, health_max, food, food_max, oxygen, oxygen_max,
				stamina, stamina_max, radiation, radiation_max, body_temp, body_temp_max,
				faction_group, faction_id, faction_role, credits, exp, upgrade,
				bp_remaining_time, bp_in_factory, bp_resources_in_factory, kills, died, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(steam_id) DO UPDATE SET
				health = excluded.health, health_max = excluded.health_max,
				food = excluded.food, food_max = excluded.food_max,
				oxygen = excluded.oxygen, oxygen_max = excluded.oxygen_max,
				stamina = excluded.stamina, stamina_max = excluded.stamina_max,
				radiation = excluded.radiation, radiation_max = excluded.radiation_max,
				body_temp = excluded.body_temp, body_temp_max = excluded.body_temp_max,
				faction_group = excluded.faction_group, faction_id = excluded.faction_id, faction_role = excluded.faction_role,
				credits = excluded.credits, exp = excluded.exp, upgrade = excluded.upgrade,
				bp_remaining_time = excluded.bp_remaining_time, bp_in_factory = excluded.bp_in_factory,
				bp_resources_in_factory = excluded.bp_resources_in_factory,
				kills = excluded.kills, died = excluded.died,
				updated_at = excluded.updated_at
		`,
			p.SteamId, info.Health, info.HealthMax, info.Food, info.FoodMax, info.Oxygen, info.OxygenMax,
			info.Stamina, info.StaminaMax, info.Radiation, info.RadiationMax, info.BodyTemp, info.BodyTempMax,
			info.FactionGroup, info.FactionId, info.FactionRole, info.Credits, info.Exp, info.Upgrade,
			info.BpRemainingTime, info.BpInFactory, string(resourcesJSON), info.Kills, info.Died, now)
		if err != nil {
			return fmt.Errorf("upsert %s: %w", p.SteamId, err)
		}
	}
	if err := refreshKillsDiedCache(cfg); err != nil {
		return fmt.Errorf("refresh kills/died cache: %w", err)
	}
	return nil
}
