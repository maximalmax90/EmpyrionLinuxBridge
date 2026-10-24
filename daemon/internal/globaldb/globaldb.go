package globaldb

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/safego"

	_ "modernc.org/sqlite"
)

type Vec3 struct {
	X, Y, Z float64
}

type OfflineInfo struct {
	Pos          *Vec3
	Rot          *Vec3
	Credits      *int64
	FactionGroup *int
	FactionID    *int
	Playfield    string
	SolarSystem  string
	Kills        *int
	Died         *int
	Playtime     *float64
	Level        *int
	Exp          *int
}

func dbPath(cfg config.Config) (string, error) {
	name, err := cfg.GameName()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg.DataRoot, "Saves", "Games", name, "global.db"), nil
}

var (
	dbMu     sync.Mutex
	dbByPath = map[string]*sql.DB{}
)

func getDB(path string) (*sql.DB, error) {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db, ok := dbByPath[path]; ok {
		return db, nil
	}
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(3000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	dbByPath[path] = db
	return db, nil
}

func AllOfflinePlayers(cfg config.Config) (map[int]*OfflineInfo, error) {
	path, err := dbPath(cfg)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	db, err := getDB(path)
	if err != nil {
		return nil, err
	}

	result := map[int]*OfflineInfo{}
	ensure := func(entityID int) *OfflineInfo {
		info, ok := result[entityID]
		if !ok {
			info = &OfflineInfo{}
			result[entityID] = info
		}
		return info
	}

	pfidByEntity := map[int]int64{}
	rows, err := db.Query(`SELECT entityid, pfid, posx, posy, posz, rotx, roty, rotz, credits FROM PlayerData`)
	if err != nil {
		return nil, fmt.Errorf("PlayerData: %w", err)
	}
	for rows.Next() {
		var entityID int
		var pfid sql.NullInt64
		var posx, posy, posz, rotx, roty, rotz sql.NullFloat64
		var credits sql.NullInt64
		if err := rows.Scan(&entityID, &pfid, &posx, &posy, &posz, &rotx, &roty, &rotz, &credits); err != nil {
			rows.Close()
			return nil, fmt.Errorf("PlayerData scan: %w", err)
		}
		info := ensure(entityID)
		if posx.Valid && posy.Valid && posz.Valid {
			info.Pos = &Vec3{X: posx.Float64, Y: posy.Float64, Z: posz.Float64}
		}
		if rotx.Valid && roty.Valid && rotz.Valid {
			info.Rot = &Vec3{X: rotx.Float64, Y: roty.Float64, Z: rotz.Float64}
		}
		if credits.Valid {
			v := credits.Int64
			info.Credits = &v
		}
		if pfid.Valid {
			pfidByEntity[entityID] = pfid.Int64
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("PlayerData rows: %w", err)
	}

	rows, err = db.Query(`SELECT entityid, facgroup, facid FROM Entities`)
	if err != nil {
		return nil, fmt.Errorf("Entities: %w", err)
	}
	for rows.Next() {
		var entityID int
		var facgroup, facid sql.NullInt64
		if err := rows.Scan(&entityID, &facgroup, &facid); err != nil {
			rows.Close()
			return nil, fmt.Errorf("Entities scan: %w", err)
		}

		if info, ok := result[entityID]; ok {
			if facgroup.Valid {
				v := int(facgroup.Int64)
				info.FactionGroup = &v
			}
			if facid.Valid {
				v := int(facid.Int64)
				info.FactionID = &v
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Entities rows: %w", err)
	}

	type pfInfo struct{ Name, System string }
	pfByID := map[int64]pfInfo{}
	rows, err = db.Query(`SELECT pf.pfid, pf.name, ss.name FROM Playfields pf LEFT JOIN SolarSystems ss ON ss.ssid = pf.ssid`)
	if err != nil {
		return nil, fmt.Errorf("Playfields: %w", err)
	}
	for rows.Next() {
		var pfid int64
		var name, ssName sql.NullString
		if err := rows.Scan(&pfid, &name, &ssName); err != nil {
			rows.Close()
			return nil, fmt.Errorf("Playfields scan: %w", err)
		}
		pfByID[pfid] = pfInfo{Name: name.String, System: ssName.String}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Playfields rows: %w", err)
	}
	for entityID, pfid := range pfidByEntity {
		if pf, ok := pfByID[pfid]; ok {
			result[entityID].Playfield = pf.Name
			result[entityID].SolarSystem = pf.System
		}
	}

	rows, err = db.Query(`SELECT entityid, killedplayers, died, playtime FROM PlayerStatistics`)
	if err != nil {
		return nil, fmt.Errorf("PlayerStatistics: %w", err)
	}
	for rows.Next() {
		var entityID int
		var killedplayers, died sql.NullInt64
		var playtime sql.NullFloat64
		if err := rows.Scan(&entityID, &killedplayers, &died, &playtime); err != nil {
			rows.Close()
			return nil, fmt.Errorf("PlayerStatistics scan: %w", err)
		}
		info := ensure(entityID)
		if killedplayers.Valid {
			v := int(killedplayers.Int64)
			info.Kills = &v
		}
		if died.Valid {
			v := int(died.Int64)
			info.Died = &v
		}
		if playtime.Valid {
			v := playtime.Float64
			info.Playtime = &v
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("PlayerStatistics rows: %w", err)
	}

	rows, err = db.Query(`SELECT entityid, level, exppoints FROM PlayerLevelUp ORDER BY plid ASC`)
	if err != nil {
		return nil, fmt.Errorf("PlayerLevelUp: %w", err)
	}
	for rows.Next() {
		var entityID int
		var level, exp sql.NullInt64
		if err := rows.Scan(&entityID, &level, &exp); err != nil {
			rows.Close()
			return nil, fmt.Errorf("PlayerLevelUp scan: %w", err)
		}
		info := ensure(entityID)
		if level.Valid {
			v := int(level.Int64)
			info.Level = &v
		}
		if exp.Valid {
			v := int(exp.Int64)
			info.Exp = &v
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("PlayerLevelUp rows: %w", err)
	}

	return result, nil
}

const cachePollInterval = 15 * time.Second

var (
	cacheMu   sync.RWMutex
	cacheData map[int]*OfflineInfo // nil до первого успешного опроса
)

func CachedOfflinePlayers() map[int]*OfflineInfo {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	return cacheData
}

func RunPoller(cfg config.Config) {
	ticker := time.NewTicker(cachePollInterval)
	defer ticker.Stop()
	for {
		safego.Tick("globaldb.RunPoller", func() {
			data, err := AllOfflinePlayers(cfg)
			if err != nil {
				log.Printf("globaldb: offline player poll: %v", err)
			} else {
				cacheMu.Lock()
				cacheData = data
				cacheMu.Unlock()
			}
		})
		<-ticker.C
	}
}
