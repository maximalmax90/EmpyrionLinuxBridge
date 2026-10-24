package globaldb

import (
	"database/sql"
	"fmt"
	"log"
	"sort"

	"elb/daemon/internal/config"
)

type DuplicateGroup struct {
	Name   string `json:"name"`
	Keep   int    `json:"keep"`
	Remove []int  `json:"remove"`
}

func FindDuplicatePlayers(cfg config.Config) ([]DuplicateGroup, error) {
	path, err := dbPath(cfg)
	if err != nil {
		return nil, err
	}
	db, err := getDB(path)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT entityid, name FROM Entities WHERE etype = 1 AND isremoved = 0 ORDER BY name, entityid`)
	if err != nil {
		return nil, fmt.Errorf("query Entities: %w", err)
	}
	defer rows.Close()

	var entities []entityNameID
	for rows.Next() {
		var e entityNameID
		if err := rows.Scan(&e.ID, &e.Name); err != nil {
			return nil, err
		}
		entities = append(entities, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return groupDuplicates(entities), nil
}

type entityNameID struct {
	ID   int
	Name string
}

func groupDuplicates(entities []entityNameID) []DuplicateGroup {
	byName := map[string][]int{}
	for _, e := range entities {
		byName[e.Name] = append(byName[e.Name], e.ID)
	}
	var out []DuplicateGroup
	for name, ids := range byName {
		if len(ids) < 2 {
			continue
		}
		sort.Ints(ids)
		out = append(out, DuplicateGroup{Name: name, Keep: ids[len(ids)-1], Remove: ids[:len(ids)-1]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

var duplicateCleanupTables = []string{
	"Entities", "PlayerData", "PlayerStatistics", "PlayerStatisticsOres",
	"PlayerStatisticsCores", "PlayerStatisticsPDAChapters", "PlayerStatisticsAIVessels",
	"PlayerInventory", "LoginLogoff", "PlayerLevelUp", "PlayerPosHistory",
	"DialogueVars", "PlayerSkillValues", "PerformanceData", "Bookmarks",
	"DiscoveredPlayfields", "DiscoveredPOIs", "VisitedStructures", "TraderHistory",
	"ChangedPlayfields", "PlayerDeaths",
}

func CleanupDuplicatePlayers(cfg config.Config, groups []DuplicateGroup) (removed int, err error) {
	if len(groups) == 0 {
		return 0, nil
	}
	path, err := dbPath(cfg)
	if err != nil {
		return 0, err
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return 0, fmt.Errorf("open %s (rw): %w", path, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for _, g := range groups {
		for _, id := range g.Remove {
			for _, table := range duplicateCleanupTables {
				if _, err := tx.Exec(fmt.Sprintf("DELETE FROM %s WHERE entityid = ?", table), id); err != nil {
					return removed, fmt.Errorf("удаление из %s (entityid=%d, %q): %w", table, id, g.Name, err)
				}
			}
			removed++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	log.Printf("globaldb: cleanup: removed %d duplicate player entit(y/ies) across %d group(s)", removed, len(groups))

	dbMu.Lock()
	if cached, ok := dbByPath[path]; ok {
		cached.Close()
		delete(dbByPath, path)
	}
	dbMu.Unlock()
	return removed, nil
}
