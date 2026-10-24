package structurebackup

import (
	"database/sql"
	"encoding/base64"
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
	"elb/daemon/internal/settings"
	"elb/daemon/internal/structurequeue"
	"elb/daemon/internal/watchdog"

	_ "modernc.org/sqlite"
)

type Entry struct {
	ID        int
	Timestamp time.Time
	Playfield string
	Name      string

	TypeName     string
	PosX         float64
	PosY         float64
	PosZ         float64
	FactionGroup byte
	FactionId    int
	Containers   []modclient.ContainerSnapshot
	FuelTank     *modclient.TankSnapshot
	OxygenTank   *modclient.TankSnapshot
	PentaxidTank *modclient.TankSnapshot
	Blueprint    []byte

	GeometryBlocks     []modclient.BlockSnapshot
	GeometryBlockCount int
}

const defaultMaxEntries = 3

const pollInterval = 5 * time.Minute

const schemaSQL = `
CREATE TABLE IF NOT EXISTS structure_backups (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	structure_id INTEGER NOT NULL,
	playfield TEXT NOT NULL,
	name TEXT NOT NULL,
	type_name TEXT NOT NULL,
	pos_x REAL NOT NULL,
	pos_y REAL NOT NULL,
	pos_z REAL NOT NULL,
	faction_group INTEGER NOT NULL,
	faction_id INTEGER NOT NULL,
	timestamp TEXT NOT NULL,
	containers TEXT NOT NULL,
	fuel_tank TEXT,
	oxygen_tank TEXT,
	pentaxid_tank TEXT,
	blueprint BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_structure_backups_sid_ts ON structure_backups(structure_id, timestamp);
`

var geometryColumns = []string{
	`ALTER TABLE structure_backups ADD COLUMN geometry_blocks TEXT`,
	`ALTER TABLE structure_backups ADD COLUMN geometry_block_count INTEGER`,
}

const identitiesSchemaSQL = `
CREATE TABLE IF NOT EXISTS structure_identities (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	playfield TEXT NOT NULL,
	faction_group INTEGER NOT NULL,
	faction_id INTEGER NOT NULL,
	entity_id INTEGER NOT NULL,
	status TEXT NOT NULL DEFAULT 'active',
	missing_since TEXT,
	updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_structure_identities_fingerprint ON structure_identities(name, faction_group, faction_id);
CREATE INDEX IF NOT EXISTS idx_structure_identities_entity_id ON structure_identities(entity_id);
`

type IdentityStatus string

const (
	IdentityActive  IdentityStatus = "active"
	IdentityDeleted IdentityStatus = "deleted"
)

type Identity struct {
	ID           int
	Name         string
	Playfield    string
	FactionGroup byte
	FactionId    int
	EntityID     int
	Status       IdentityStatus
	UpdatedAt    time.Time
}

func resolveIdentity(db *sql.DB, name, playfield string, factionGroup byte, factionId int, entityID int) (int, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var id int

	err := db.QueryRow(`SELECT id FROM structure_identities WHERE entity_id = ? AND status = 'active' ORDER BY updated_at DESC LIMIT 1`, entityID).Scan(&id)
	if err != nil && err != sql.ErrNoRows {
		return 0, fmt.Errorf("find identity by entity_id: %w", err)
	}
	if err == nil {
		if _, err := db.Exec(`UPDATE structure_identities SET name = ?, playfield = ?, faction_group = ?, faction_id = ?, status = 'active', missing_since = NULL, updated_at = ? WHERE id = ?`,
			name, playfield, factionGroup, factionId, now, id); err != nil {
			return 0, fmt.Errorf("update identity (by entity_id): %w", err)
		}
		return id, nil
	}

	err = db.QueryRow(`SELECT id FROM structure_identities WHERE name = ? AND faction_group = ? AND faction_id = ?`,
		name, factionGroup, factionId).Scan(&id)
	switch {
	case err == sql.ErrNoRows:
		res, err := db.Exec(`INSERT INTO structure_identities (name, playfield, faction_group, faction_id, entity_id, status, updated_at) VALUES (?, ?, ?, ?, ?, 'active', ?)`,
			name, playfield, factionGroup, factionId, entityID, now)
		if err != nil {
			return 0, fmt.Errorf("insert identity: %w", err)
		}
		newID, err := res.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("insert identity: last id: %w", err)
		}
		return int(newID), nil
	case err != nil:
		return 0, fmt.Errorf("find identity: %w", err)
	}
	if _, err := db.Exec(`UPDATE structure_identities SET entity_id = ?, playfield = ?, status = 'active', missing_since = NULL, updated_at = ? WHERE id = ?`, entityID, playfield, now, id); err != nil {
		return 0, fmt.Errorf("update identity: %w", err)
	}
	return id, nil
}

func scanIdentity(row *sql.Row) (Identity, error) {
	var e Identity
	var status string
	var updatedAt string
	if err := row.Scan(&e.ID, &e.Name, &e.Playfield, &e.FactionGroup, &e.FactionId, &e.EntityID, &status, &updatedAt); err != nil {
		return Identity{}, err
	}
	e.Status = IdentityStatus(status)
	e.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return e, nil
}

const identityColumns = `id, name, playfield, faction_group, faction_id, entity_id, status, updated_at`

func GetIdentity(cfg config.Config, id int) (Identity, bool, error) {
	db, err := getDB(cfg)
	if err != nil {
		return Identity{}, false, err
	}
	e, err := scanIdentity(db.QueryRow(`SELECT `+identityColumns+` FROM structure_identities WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return Identity{}, false, nil
	}
	if err != nil {
		return Identity{}, false, err
	}
	return e, true, nil
}

func IdentityByEntityID(cfg config.Config, entityID int) (Identity, bool, error) {
	db, err := getDB(cfg)
	if err != nil {
		return Identity{}, false, err
	}
	e, err := scanIdentity(db.QueryRow(`SELECT `+identityColumns+` FROM structure_identities WHERE entity_id = ? ORDER BY updated_at DESC LIMIT 1`, entityID))
	if err == sql.ErrNoRows {
		return Identity{}, false, nil
	}
	if err != nil {
		return Identity{}, false, err
	}
	return e, true, nil
}

type MergeReport struct {
	GroupsMerged      int `json:"groupsMerged"`
	IdentitiesRemoved int `json:"identitiesRemoved"`
	BackupsRepointed  int `json:"backupsRepointed"`
}

func MergeFragmentedIdentities(cfg config.Config) (MergeReport, error) {
	db, err := getDB(cfg)
	if err != nil {
		return MergeReport{}, err
	}

	rows, err := db.Query(`SELECT id, name, faction_group, faction_id FROM structure_identities ORDER BY name, faction_group, faction_id, updated_at DESC`)
	if err != nil {
		return MergeReport{}, fmt.Errorf("list identities: %w", err)
	}
	type idRow struct {
		id                      int
		name                    string
		factionGroup, factionId int
	}
	var all []idRow
	for rows.Next() {
		var r idRow
		if err := rows.Scan(&r.id, &r.name, &r.factionGroup, &r.factionId); err != nil {
			rows.Close()
			return MergeReport{}, fmt.Errorf("scan identity: %w", err)
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return MergeReport{}, err
	}

	type fingerprint struct {
		name                    string
		factionGroup, factionId int
	}
	groups := map[fingerprint][]idRow{}
	for _, r := range all {
		k := fingerprint{r.name, r.factionGroup, r.factionId}
		groups[k] = append(groups[k], r)
	}

	var report MergeReport
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}

		canonical := g[0].id
		mergedAny := false
		for _, r := range g[1:] {
			res, err := db.Exec(`UPDATE structure_backups SET structure_id = ? WHERE structure_id = ?`, canonical, r.id)
			if err != nil {
				return report, fmt.Errorf("repoint backups %d -> %d: %w", r.id, canonical, err)
			}
			n, _ := res.RowsAffected()
			report.BackupsRepointed += int(n)
			if _, err := db.Exec(`DELETE FROM structure_identities WHERE id = ?`, r.id); err != nil {
				return report, fmt.Errorf("delete merged identity %d: %w", r.id, err)
			}
			report.IdentitiesRemoved++
			mergedAny = true
		}
		if mergedAny {
			report.GroupsMerged++
		}
	}
	return report, nil
}

func ListIdentitiesByStatus(cfg config.Config, status IdentityStatus) ([]Identity, error) {
	db, err := getDB(cfg)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT `+identityColumns+` FROM structure_identities WHERE status = ? ORDER BY updated_at DESC`, string(status))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Identity
	for rows.Next() {
		var e Identity
		var statusStr, updatedAt string
		if err := rows.Scan(&e.ID, &e.Name, &e.Playfield, &e.FactionGroup, &e.FactionId, &e.EntityID, &statusStr, &updatedAt); err != nil {
			return nil, err
		}
		e.Status = IdentityStatus(statusStr)
		e.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		out = append(out, e)
	}
	return out, rows.Err()
}

func SetIdentityStatus(cfg config.Config, id int, status IdentityStatus) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE structure_identities SET status = ?, updated_at = ? WHERE id = ?`,
		string(status), time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

func SetIdentityEntityID(cfg config.Config, id, entityID int) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE structure_identities SET entity_id = ?, status = 'active', missing_since = NULL, updated_at = ? WHERE id = ?`,
		entityID, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

func ensureGeometryColumns(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(structure_backups)`)
	if err != nil {
		return fmt.Errorf("table_info: %w", err)
	}
	existing := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("scan table_info: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()

	if !existing["geometry_blocks"] {
		if _, err := db.Exec(geometryColumns[0]); err != nil {
			return fmt.Errorf("add geometry_blocks: %w", err)
		}
	}
	if !existing["geometry_block_count"] {
		if _, err := db.Exec(geometryColumns[1]); err != nil {
			return fmt.Errorf("add geometry_block_count: %w", err)
		}
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

	if db, ok := dbByPath[cfg.StructureBackupDBFile]; ok {
		return db, nil
	}
	if err := os.MkdirAll(filepath.Dir(cfg.StructureBackupDBFile), 0o777); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(cfg.StructureBackupDBFile), err)
	}
	db, err := sql.Open("sqlite", cfg.StructureBackupDBFile)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", cfg.StructureBackupDBFile, err)
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
	if err := ensureGeometryColumns(db); err != nil {
		return nil, fmt.Errorf("geometry columns migration: %w", err)
	}
	if _, err := db.Exec(identitiesSchemaSQL); err != nil {
		return nil, fmt.Errorf("identities schema: %w", err)
	}
	dbByPath[cfg.StructureBackupDBFile] = db
	return db, nil
}

func Wipe(cfg config.Config) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM structure_backups;`); err != nil {
		return err
	}
	_, err = db.Exec(`VACUUM;`)
	return err
}

func List(cfg config.Config, structureID int) ([]Entry, error) {
	db, err := getDB(cfg)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT id, timestamp, playfield, name, type_name, pos_x, pos_y, pos_z, faction_group, faction_id, containers, fuel_tank, oxygen_tank, pentaxid_tank, blueprint, geometry_blocks, geometry_block_count
		FROM structure_backups WHERE structure_id = ? ORDER BY timestamp ASC`, structureID)
	if err != nil {
		return nil, fmt.Errorf("query structure_backups %d: %w", structureID, err)
	}
	defer rows.Close()

	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("scan structure_backups %d: %w", structureID, err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type BackupSummary struct {
	Count     int
	Timestamp time.Time
}

func Summary(cfg config.Config) (map[int]BackupSummary, error) {
	db, err := getDB(cfg)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT structure_id, COUNT(*), MAX(timestamp) FROM structure_backups GROUP BY structure_id`)
	if err != nil {
		return nil, fmt.Errorf("summary structure_backups: %w", err)
	}
	defer rows.Close()

	out := map[int]BackupSummary{}
	for rows.Next() {
		var id, count int
		var ts string
		if err := rows.Scan(&id, &count, &ts); err != nil {
			return nil, fmt.Errorf("scan summary: %w", err)
		}
		t, _ := time.Parse(time.RFC3339Nano, ts)
		out[id] = BackupSummary{Count: count, Timestamp: t}
	}
	return out, rows.Err()
}

func Latest(cfg config.Config, structureID int) (Entry, bool, error) {
	db, err := getDB(cfg)
	if err != nil {
		return Entry{}, false, err
	}
	row := db.QueryRow(`SELECT id, timestamp, playfield, name, type_name, pos_x, pos_y, pos_z, faction_group, faction_id, containers, fuel_tank, oxygen_tank, pentaxid_tank, blueprint, geometry_blocks, geometry_block_count
		FROM structure_backups WHERE structure_id = ? ORDER BY timestamp DESC LIMIT 1`, structureID)
	e, err := scanEntry(row)
	if err == sql.ErrNoRows {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	return e, true, nil
}

func ByID(cfg config.Config, structureID, id int) (Entry, bool, error) {
	db, err := getDB(cfg)
	if err != nil {
		return Entry{}, false, err
	}
	row := db.QueryRow(`SELECT id, timestamp, playfield, name, type_name, pos_x, pos_y, pos_z, faction_group, faction_id, containers, fuel_tank, oxygen_tank, pentaxid_tank, blueprint, geometry_blocks, geometry_block_count
		FROM structure_backups WHERE structure_id = ? AND id = ?`, structureID, id)
	e, err := scanEntry(row)
	if err == sql.ErrNoRows {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	return e, true, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanEntry(s scanner) (Entry, error) {
	var id int
	var ts, playfield, name, typeName, containersJSON string
	var posX, posY, posZ float64
	var factionGroup byte
	var factionID int
	var fuelJSON, oxygenJSON, pentaxidJSON, geometryBlocksJSON sql.NullString
	var geometryBlockCount sql.NullInt64
	var blueprint []byte
	if err := s.Scan(&id, &ts, &playfield, &name, &typeName, &posX, &posY, &posZ, &factionGroup, &factionID,
		&containersJSON, &fuelJSON, &oxygenJSON, &pentaxidJSON, &blueprint, &geometryBlocksJSON, &geometryBlockCount); err != nil {
		return Entry{}, err
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return Entry{}, fmt.Errorf("parse timestamp %q: %w", ts, err)
	}
	e := Entry{
		ID: id, Timestamp: t, Playfield: playfield, Name: name, Blueprint: blueprint,
		TypeName: typeName, PosX: posX, PosY: posY, PosZ: posZ,
		FactionGroup: factionGroup, FactionId: factionID,
		GeometryBlockCount: int(geometryBlockCount.Int64),
	}
	if err := json.Unmarshal([]byte(containersJSON), &e.Containers); err != nil {
		return Entry{}, fmt.Errorf("parse containers: %w", err)
	}
	if geometryBlocksJSON.Valid && geometryBlocksJSON.String != "" {
		if err := json.Unmarshal([]byte(geometryBlocksJSON.String), &e.GeometryBlocks); err != nil {
			return Entry{}, fmt.Errorf("parse geometryBlocks: %w", err)
		}
	}
	if tank, err := parseTank(fuelJSON); err != nil {
		return Entry{}, fmt.Errorf("parse fuelTank: %w", err)
	} else {
		e.FuelTank = tank
	}
	if tank, err := parseTank(oxygenJSON); err != nil {
		return Entry{}, fmt.Errorf("parse oxygenTank: %w", err)
	} else {
		e.OxygenTank = tank
	}
	if tank, err := parseTank(pentaxidJSON); err != nil {
		return Entry{}, fmt.Errorf("parse pentaxidTank: %w", err)
	} else {
		e.PentaxidTank = tank
	}
	return e, nil
}

func parseTank(s sql.NullString) (*modclient.TankSnapshot, error) {
	if !s.Valid || s.String == "" {
		return nil, nil
	}
	var t modclient.TankSnapshot
	if err := json.Unmarshal([]byte(s.String), &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func insertEntry(db *sql.DB, structureID int, p modclient.StructureBackupPayload) error {
	containers := p.Containers
	if containers == nil {
		containers = []modclient.ContainerSnapshot{}
	}
	containersJSON, err := json.Marshal(containers)
	if err != nil {
		return err
	}
	fuelJSON, err := marshalTank(p.FuelTank)
	if err != nil {
		return err
	}
	oxygenJSON, err := marshalTank(p.OxygenTank)
	if err != nil {
		return err
	}
	pentaxidJSON, err := marshalTank(p.PentaxidTank)
	if err != nil {
		return err
	}
	blueprint, err := decodeBlueprint(p.BlueprintBase64)
	if err != nil {
		return err
	}

	var geometryBlocksJSON any
	if p.GeometryBlocks != nil {
		b, err := json.Marshal(p.GeometryBlocks)
		if err != nil {
			return err
		}
		geometryBlocksJSON = string(b)
	}
	_, err = db.Exec(`INSERT INTO structure_backups
		(structure_id, playfield, name, type_name, pos_x, pos_y, pos_z, faction_group, faction_id, timestamp, containers, fuel_tank, oxygen_tank, pentaxid_tank, blueprint, geometry_blocks, geometry_block_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		structureID, p.Playfield, p.Name, p.TypeName, p.PosX, p.PosY, p.PosZ, p.FactionGroup, p.FactionId,
		time.Now().UTC().Format(time.RFC3339Nano),
		containersJSON, fuelJSON, oxygenJSON, pentaxidJSON, blueprint, geometryBlocksJSON, p.GeometryBlockCount)
	return err
}

func decodeBlueprint(b64 string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("decode blueprint base64: %w", err)
	}
	return b, nil
}

func marshalTank(t *modclient.TankSnapshot) (any, error) {
	if t == nil {
		return nil, nil
	}
	b, err := json.Marshal(t)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func pruneOld(db *sql.DB, structureID int, maxEntries int) error {
	if maxEntries <= 0 {
		maxEntries = defaultMaxEntries
	}
	_, err := db.Exec(`DELETE FROM structure_backups WHERE structure_id = ? AND id NOT IN (
		SELECT id FROM structure_backups WHERE structure_id = ? ORDER BY timestamp DESC LIMIT ?
	)`, structureID, structureID, maxEntries)
	return err
}

func RunPoller(cfg config.Config, mod *modclient.Client) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for range ticker.C {
		if !watchdog.CachedHealth().Ready() {
			continue
		}
		safego.Tick("structurebackup.RunPoller", func() {
			if err := PollOnce(cfg, mod); err != nil {
				log.Printf("structurebackup: poll: %v", err)
			}
		})
	}
}

const identityReconcileInterval = 15 * time.Second
const missingGrace = 45 * time.Second

func RunIdentityReconciler(cfg config.Config, mod *modclient.Client) {
	ticker := time.NewTicker(identityReconcileInterval)
	defer ticker.Stop()
	for range ticker.C {
		if !watchdog.CachedHealth().Ready() {
			continue
		}
		safego.Tick("structurebackup.RunIdentityReconciler", func() {
			if err := ReconcileIdentities(cfg, mod); err != nil {
				log.Printf("structurebackup: identity reconciliation: %v", err)
			}
		})
	}
}

func ReconcileIdentities(cfg config.Config, mod *modclient.Client) error {
	active, err := ListIdentitiesByStatus(cfg, IdentityActive)
	if err != nil {
		return fmt.Errorf("active identities list: %w", err)
	}
	if len(active) == 0 {
		return nil
	}
	loaded, err := structurequeue.LoadedPlayfields(mod)
	if err != nil {
		return fmt.Errorf("loaded playfields: %w", err)
	}
	if len(loaded) == 0 {
		return nil
	}
	live, err := liveEntityIDs(mod)
	if err != nil {
		return fmt.Errorf("live structures list: %w", err)
	}
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, ident := range active {
		if live[ident.EntityID] {
			if _, err := db.Exec(`UPDATE structure_identities SET missing_since = NULL WHERE id = ? AND missing_since IS NOT NULL`, ident.ID); err != nil {
				log.Printf("structurebackup: identity %d: clear missing_since: %v", ident.ID, err)
			}
			continue
		}
		var missingSince sql.NullString
		if err := db.QueryRow(`SELECT missing_since FROM structure_identities WHERE id = ?`, ident.ID).Scan(&missingSince); err != nil {
			log.Printf("structurebackup: identity %d: read missing_since: %v", ident.ID, err)
			continue
		}
		if !missingSince.Valid {
			if _, err := db.Exec(`UPDATE structure_identities SET missing_since = ? WHERE id = ?`, now.Format(time.RFC3339Nano), ident.ID); err != nil {
				log.Printf("structurebackup: identity %d: set missing_since: %v", ident.ID, err)
			}
			continue
		}
		since, err := time.Parse(time.RFC3339Nano, missingSince.String)
		if err != nil || now.Sub(since) < missingGrace {
			continue
		}
		if err := SetIdentityStatus(cfg, ident.ID, IdentityDeleted); err != nil {
			log.Printf("structurebackup: identity %d: mark deleted: %v", ident.ID, err)
		}
	}
	return nil
}

func liveEntityIDs(mod *modclient.Client) (map[int]bool, error) {
	list, err := mod.AllStructures(modclient.AllStructuresMaxAge)
	if err != nil {
		return nil, err
	}
	out := map[int]bool{}
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if idF, ok := m["id"].(float64); ok {
			out[int(idF)] = true
		}
	}
	return out, nil
}

func PollOnce(cfg config.Config, mod *modclient.Client) error {
	pending, err := mod.PendingStructureBackups()
	if err != nil {
		return fmt.Errorf("fetch pending snapshots: %w", err)
	}
	if len(pending) == 0 {
		return nil
	}

	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	maxEntries := s.Structures.MaxBackupEntries

	db, err := getDB(cfg)
	if err != nil {
		return err
	}

	for _, p := range pending {
		identityID, err := resolveIdentity(db, p.Name, p.Playfield, p.FactionGroup, p.FactionId, p.Id)
		if err != nil {
			log.Printf("structurebackup: identity for structure %d: %v", p.Id, err)
			continue
		}
		if err := insertEntry(db, identityID, p); err != nil {
			log.Printf("structurebackup: backup write for structure %d (identity %d): %v", p.Id, identityID, err)
			continue
		}
		if err := pruneOld(db, identityID, maxEntries); err != nil {
			log.Printf("structurebackup: retention for structure %d (identity %d): %v", p.Id, identityID, err)
		}
	}
	return nil
}
