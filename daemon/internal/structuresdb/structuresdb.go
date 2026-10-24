package structuresdb

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

const (
	FactionGroupFaction = 0
	FactionGroupPlayer  = 1
)

var factionGroupNames = map[int]string{
	0:  "Faction",
	1:  "Player",
	2:  "Zirax",
	3:  "Predator",
	4:  "Prey",
	5:  "Admin",
	6:  "Talon",
	7:  "Polaris",
	8:  "Alien",
	9:  "Pirates",
	10: "Kriel",
	11: "UCH",
	12: "Trader",
	13: "Civilian",
}

func FactionGroupName(group int) string {
	return factionGroupNames[group]
}

var entityTypeNames = []string{
	"Unknown", "Player", "BA", "CV", "SV", "HV", "AstRes", "AstVoxel",
	"EscapePod", "Animal", "Turret", "Item", "PlayerDrone", "Trader",
}

func EntityTypeName(t int) string {
	if t < 0 || t >= len(entityTypeNames) {
		return "Unknown"
	}
	return entityTypeNames[t]
}

type Structure struct {
	ID              int    `json:"id"`
	Name            string `json:"name"`
	Type            int    `json:"type"`
	TypeName        string `json:"typeName"`
	ClassNr         int    `json:"classNr"`
	FactionID       int    `json:"factionId"`
	FactionGroup    int    `json:"factionGroup"`
	FactionGroupStr string `json:"factionGroupName"`
	IsPOI           bool   `json:"isPoi"`
	Playfield       string `json:"playfield"`
	SolarSystem     string `json:"solarSystem"`
	Pos             Vec3   `json:"pos"`
	Rot             Vec3   `json:"rot"`
	Powered         bool   `json:"powered"`
	Fuel            int    `json:"fuel"`
	CntDevices      int    `json:"cntDevices"`
	CntBlocks       int    `json:"cntBlocks"`
	CntTriangles    int    `json:"cntTriangles"`
	CntLights       int    `json:"cntLights"`
	CoreType        int    `json:"coreType"`
	PilotID         int    `json:"pilotId"`
	DockedTo        int    `json:"dockedTo,omitempty"`
	DockedToName    string `json:"dockedToName,omitempty"`
	DockedShips     []int  `json:"dockedShips,omitempty"`

	LastVisitedTicks int64 `json:"lastVisitedTicks,omitempty"`

	IdentityID int  `json:"identityId,omitempty"`
	Deleted    bool `json:"deleted,omitempty"`

	SnapshotCount int    `json:"snapshotCount,omitempty"`
	LastSnapshot  string `json:"lastSnapshot,omitempty"`
}

func dbPath(cfg config.Config) (string, error) {
	name, err := cfg.GameName()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg.DataRoot, "Saves", "Games", name, "global.db"), nil
}

func All(cfg config.Config) ([]Structure, error) {
	path, err := dbPath(cfg)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	return allFromPath(path)
}

func allFromPath(path string) ([]Structure, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(3000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	playfields, err := readPlayfields(db)
	if err != nil {
		return nil, fmt.Errorf("Playfields: %w", err)
	}

	rows, err := db.Query(`
SELECT Structures.entityid, pfid, classNr, cntLights, cntTriangles, cntBlocks,
       cntDevices, fuel, ispowered, rotX, rotY, rotZ, posX, posY, posZ,
       name, facid, lastvisitedticks, facgroup, dockedTo, etype, coretype, pilotid
FROM Structures
LEFT JOIN Entities ON Structures.entityid = Entities.entityid
WHERE Entities.isremoved IS NULL OR Entities.isremoved = 0
ORDER BY pfid`)
	if err != nil {
		return nil, fmt.Errorf("Structures: %w", err)
	}
	defer rows.Close()

	var result []Structure
	byID := make(map[int]int)

	for rows.Next() {
		var (
			entityID, pfid                               int
			classNr, cntLights, cntTriangles, cntBlocks  sql.NullInt64
			cntDevices                                   sql.NullInt64
			fuel                                         sql.NullFloat64
			ispowered                                    sql.NullBool
			rotX, rotY, rotZ, posX, posY, posZ           sql.NullFloat64
			name                                         sql.NullString
			facid, lastvisitedticks                      sql.NullInt64
			facgroup, dockedTo, etype, coretype, pilotid sql.NullInt64
		)
		if err := rows.Scan(&entityID, &pfid, &classNr, &cntLights, &cntTriangles, &cntBlocks,
			&cntDevices, &fuel, &ispowered, &rotX, &rotY, &rotZ, &posX, &posY, &posZ,
			&name, &facid, &lastvisitedticks, &facgroup, &dockedTo, &etype, &coretype, &pilotid); err != nil {
			return nil, fmt.Errorf("scan Structures: %w", err)
		}

		pf := playfields[pfid]
		fg := int(facgroup.Int64)

		s := Structure{
			ID:               entityID,
			Name:             name.String,
			Type:             int(etype.Int64),
			TypeName:         EntityTypeName(int(etype.Int64)),
			ClassNr:          int(classNr.Int64),
			FactionID:        int(facid.Int64),
			FactionGroup:     fg,
			FactionGroupStr:  FactionGroupName(fg),
			IsPOI:            fg != FactionGroupFaction && fg != FactionGroupPlayer,
			Playfield:        pf.Name,
			SolarSystem:      pf.SolarSystem,
			Pos:              Vec3{X: posX.Float64, Y: posY.Float64, Z: posZ.Float64},
			Rot:              Vec3{X: rotX.Float64, Y: rotY.Float64, Z: rotZ.Float64},
			Powered:          ispowered.Bool,
			Fuel:             int(fuel.Float64),
			CntDevices:       int(cntDevices.Int64),
			CntBlocks:        int(cntBlocks.Int64),
			CntTriangles:     int(cntTriangles.Int64),
			CntLights:        int(cntLights.Int64),
			CoreType:         int(coretype.Int64),
			PilotID:          int(pilotid.Int64),
			LastVisitedTicks: lastvisitedticks.Int64,
		}
		if dockedTo.Valid {
			s.DockedTo = int(dockedTo.Int64)
		}

		byID[entityID] = len(result)
		result = append(result, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Structures: %w", err)
	}

	for i, s := range result {
		if s.DockedTo == 0 {
			continue
		}
		if parentIdx, ok := byID[s.DockedTo]; ok {
			result[parentIdx].DockedShips = append(result[parentIdx].DockedShips, s.ID)
			result[i].DockedToName = result[parentIdx].Name
		}
	}

	return result, nil
}

const cachePollInterval = 15 * time.Second

var (
	cacheMu        sync.RWMutex
	cacheData      []Structure       // nil до первого успешного опроса
	cachePlayfield map[string]string // то же для Playfields(), см. ниже
)

func CachedAll() []Structure {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	return cacheData
}

func CachedPlayfields() map[string]string {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	return cachePlayfield
}

func RunPoller(cfg config.Config) {
	ticker := time.NewTicker(cachePollInterval)
	defer ticker.Stop()
	for {
		safego.Tick("structuresdb.RunPoller", func() {
			if data, err := All(cfg); err != nil {
				log.Printf("structuresdb: structure poll: %v", err)
			} else {
				cacheMu.Lock()
				cacheData = data
				cacheMu.Unlock()
			}
			if pf, err := Playfields(cfg); err != nil {
				log.Printf("structuresdb: playfield poll: %v", err)
			} else {
				cacheMu.Lock()
				cachePlayfield = pf
				cacheMu.Unlock()
			}
		})
		<-ticker.C
	}
}

type playfieldInfo struct {
	Name        string
	SolarSystem string
}

func Playfields(cfg config.Config) (map[string]string, error) {
	path, err := dbPath(cfg)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(3000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	infos, err := readPlayfields(db)
	if err != nil {
		return nil, fmt.Errorf("Playfields: %w", err)
	}
	result := make(map[string]string, len(infos))
	for _, pf := range infos {
		if pf.Name != "" {
			result[pf.Name] = pf.SolarSystem
		}
	}
	return result, nil
}

func readPlayfields(db *sql.DB) (map[int]playfieldInfo, error) {
	rows, err := db.Query(`
SELECT Playfields.pfid, Playfields.name, SolarSystems.name
FROM Playfields
JOIN SolarSystems ON Playfields.ssid = SolarSystems.ssid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int]playfieldInfo)
	for rows.Next() {
		var pfid int
		var pfName, ssName sql.NullString
		if err := rows.Scan(&pfid, &pfName, &ssName); err != nil {
			return nil, err
		}
		result[pfid] = playfieldInfo{Name: pfName.String, SolarSystem: ssName.String}
	}
	return result, rows.Err()
}
