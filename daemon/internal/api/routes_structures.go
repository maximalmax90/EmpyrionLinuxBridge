package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"elb/daemon/internal/audit"
	"elb/daemon/internal/blockclass"
	"elb/daemon/internal/config"
	"elb/daemon/internal/itemlocalization"
	"elb/daemon/internal/itemmap"
	"elb/daemon/internal/logbuf"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/settings"
	"elb/daemon/internal/structurebackup"
	"elb/daemon/internal/structurequeue"
	"elb/daemon/internal/structurerestore"
	"elb/daemon/internal/structuresdb"
)

func registerStructureRoutes(mux *http.ServeMux, cfg config.Config, mod *modclient.Client, logBuf *logbuf.Buffer) {

	mux.HandleFunc("POST /api/structures/destroy", destroyWithDockedCheck(cfg, mod))

	mux.HandleFunc("POST /api/structures/touch", queueableStructureModPost(cfg, mod, "/structures/touch"))
	mux.HandleFunc("POST /api/structures/faction", structureFactionHandler(cfg, mod))
	mux.HandleFunc("POST /api/structures/repair", queueableStructureModPost(cfg, mod, "/structures/repair"))
	mux.HandleFunc("POST /api/structures/spawn", spawnStructureHandler(mod))

	mux.HandleFunc("POST /api/structures/refuel", proxyModPost(mod, "/structures/refuel"))

	mux.HandleFunc("POST /api/structures/move", queueableStructureModPost(cfg, mod, "/structures/move"))

	mux.HandleFunc("GET /api/structures/pending", func(w http.ResponseWriter, r *http.Request) {
		ids, err := structurequeue.PendingIDs(cfg)
		if err != nil {
			writeErr(w, err)
			return
		}
		out := make([]int, 0, len(ids))
		for id := range ids {
			out = append(out, id)
		}
		writeJSON(w, map[string]any{"ids": out})
	})

	mux.HandleFunc("POST /api/structures/{id}/restore", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			writeErr(w, fmt.Errorf("некорректный id структуры: %w", err))
			return
		}
		var body struct {
			Mode             string `json:"mode"`
			SnapshotId       int    `json:"snapshotId"`
			PositionPlayerId int    `json:"positionPlayerId"`
		}
		if !decodeOrErr(w, r, &body) {
			return
		}
		mode := structurerestore.Mode(body.Mode)
		if mode != structurerestore.ModeAll && mode != structurerestore.ModeContainers && mode != structurerestore.ModeStructure {
			mode = structurerestore.ModeAll
		}
		result, err := structurerestore.Restore(cfg, mod, id, body.SnapshotId, mode, body.PositionPlayerId)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, result)
	})

	mux.HandleFunc("GET /api/structures/{id}/backups", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			writeErr(w, fmt.Errorf("некорректный id структуры: %w", err))
			return
		}
		entries, err := structurebackup.List(cfg, id)
		if err != nil {
			writeErr(w, err)
			return
		}
		type summary struct {
			Id             int    `json:"id"`
			Timestamp      string `json:"timestamp"`
			ContainerCount int    `json:"containerCount"`
			ItemCount      int    `json:"itemCount"`

			TypeName string `json:"typeName"`

			BlockCount int `json:"blockCount,omitempty"`

			FuelPercent     *float64 `json:"fuelPercent,omitempty"`
			OxygenPercent   *float64 `json:"oxygenPercent,omitempty"`
			PentaxidPercent *float64 `json:"pentaxidPercent,omitempty"`
		}
		out := make([]summary, 0, len(entries))
		for i := len(entries) - 1; i >= 0; i-- {
			e := entries[i]
			items := 0
			nonEmpty := 0
			for _, c := range e.Containers {
				if len(c.Items) > 0 {
					nonEmpty++
					items += len(c.Items)
				}
			}
			out = append(out, summary{

				Id: e.ID, Timestamp: e.Timestamp.Local().Format("2006-01-02 15:04:05"), ContainerCount: nonEmpty, ItemCount: items, TypeName: e.TypeName,
				BlockCount:      e.GeometryBlockCount,
				FuelPercent:     backupTankPercent(e.FuelTank),
				OxygenPercent:   backupTankPercent(e.OxygenTank),
				PentaxidPercent: backupTankPercent(e.PentaxidTank),
			})
		}
		writeJSON(w, out)
	})

	mux.HandleFunc("POST /api/structures/merge-identities", func(w http.ResponseWriter, r *http.Request) {
		report, err := structurebackup.MergeFragmentedIdentities(cfg)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, report)
	})

	mux.HandleFunc("GET /api/structures/{id}/blocks-breakdown", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			writeErr(w, fmt.Errorf("некорректный id структуры: %w", err))
			return
		}
		entry, ok, err := structurebackup.Latest(cfg, id)
		if err != nil {
			writeErr(w, err)
			return
		}
		if !ok || len(entry.GeometryBlocks) == 0 {
			writeJSON(w, map[string]any{"blocks": []blockBreakdownItem{}, "devices": []blockBreakdownItem{}})
			return
		}

		counts := map[int]int{}
		for _, b := range entry.GeometryBlocks {
			counts[b.Type]++
		}

		nameByID := map[int]string{}
		if ids, err := blockclass.NamesByID(cfg); err != nil {
			log.Printf("blocks-breakdown: legacy block ids from BlocksConfig.ecf: %v", err)
		} else {
			for id, name := range ids {
				nameByID[id] = name
			}
		}
		if path, err := cfg.BlocksmapPath(); err != nil {
			log.Printf("blocks-breakdown: blocksmap path: %v", err)
		} else if byName, err := itemmap.Read(path); err != nil {
			log.Printf("blocks-breakdown: read %s: %v", path, err)
		} else {
			for name, typeID := range byName {
				if _, ok := nameByID[typeID]; !ok {
					nameByID[typeID] = name
				}
			}
		}

		groups, err := blockclass.Groups(cfg)
		if err != nil {
			log.Printf("blocks-breakdown: blockclass groups: %v", err)
			groups = nil
		}
		displayNames, err := itemlocalization.DisplayNames(cfg, r.URL.Query().Get("lang"))
		if err != nil {
			log.Printf("blocks-breakdown: display names: %v", err)
			displayNames = nil
		}

		var blocks, devices []blockBreakdownItem
		for typeID, count := range counts {
			key := nameByID[typeID]
			if key == "" {
				key = fmt.Sprintf("#%d", typeID)
			}
			name := displayNames[key]
			if name == "" {
				name = key
			}
			item := blockBreakdownItem{Key: key, Name: name, Count: count}
			if blockclass.IsDeviceGroup(groups[key]) {
				devices = append(devices, item)
			} else {
				blocks = append(blocks, item)
			}
		}
		sortBlockBreakdownItems(blocks)
		sortBlockBreakdownItems(devices)

		writeJSON(w, map[string]any{
			"blocks":    blocks,
			"devices":   devices,
			"timestamp": entry.Timestamp.Local().Format("2006-01-02 15:04:05"),
		})
	})

	mux.HandleFunc("GET /api/structures", func(w http.ResponseWriter, r *http.Request) {
		v, err := mod.Get("/structures?" + r.URL.RawQuery)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, v)
	})

	mux.HandleFunc("GET /api/structures/all", func(w http.ResponseWriter, r *http.Request) {

		writeJSON(w, mergeDeletedIdentities(cfg, structuresdb.CachedAll()))
	})

	mux.HandleFunc("POST /api/structures/{id}/backup-now", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			writeErr(w, fmt.Errorf("некорректный id структуры: %w", err))
			return
		}
		playfield, ok := findStructurePlayfield(id)
		if !ok {
			writeErr(w, fmt.Errorf("структура %d не найдена (нужен активный playfield)", id))
			return
		}

		var before structurebackup.Entry
		hadBefore := false
		if ident, ok, _ := structurebackup.IdentityByEntityID(cfg, id); ok {
			before, hadBefore, _ = structurebackup.Latest(cfg, ident.ID)
		}
		if _, err := mod.Post("/structures/backup/force", map[string]any{"id": id, "playfield": playfield}); err != nil {
			writeErr(w, err)
			return
		}
		captured := false
		for i := 0; i < 6 && !captured; i++ {
			time.Sleep(1500 * time.Millisecond)
			if err := structurebackup.PollOnce(cfg, mod); err != nil {
				log.Printf("structures/backup-now %d: PollOnce: %v", id, err)
				continue
			}
			ident, ok, err := structurebackup.IdentityByEntityID(cfg, id)
			if err != nil || !ok {
				continue
			}
			latest, ok, err := structurebackup.Latest(cfg, ident.ID)
			if err != nil || !ok {
				continue
			}
			if !hadBefore || latest.Timestamp.After(before.Timestamp) {
				captured = true
			}
		}
		writeJSON(w, map[string]any{"ok": true, "captured": captured})
	})
}

func backupTankPercent(t *modclient.TankSnapshot) *float64 {
	if t == nil || t.Capacity <= 0 {
		return nil
	}
	pct := t.Content / t.Capacity * 100
	return &pct
}

func findStructurePlayfield(id int) (string, bool) {
	for _, s := range structuresdb.CachedAll() {
		if s.ID == id {
			return s.Playfield, true
		}
	}
	return "", false
}

type blockBreakdownItem struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func sortBlockBreakdownItems(items []blockBreakdownItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		return items[i].Name < items[j].Name
	})
}

func entityIDToIdentityID(active []structurebackup.Identity) map[int]int {
	byEntityID := make(map[int]int, len(active))
	for _, ident := range active {
		if _, taken := byEntityID[ident.EntityID]; taken {
			continue
		}
		byEntityID[ident.EntityID] = ident.ID
	}
	return byEntityID
}

func mergeDeletedIdentities(cfg config.Config, live []structuresdb.Structure) []structuresdb.Structure {
	active, err := structurebackup.ListIdentitiesByStatus(cfg, structurebackup.IdentityActive)
	if err != nil {
		log.Printf("structures/all: active identity list: %v", err)
		active = nil
	}
	byEntityID := entityIDToIdentityID(active)

	backupSummaries, err := structurebackup.Summary(cfg)
	if err != nil {
		log.Printf("structures/all: backup summary: %v", err)
		backupSummaries = nil
	}

	out := make([]structuresdb.Structure, len(live))
	for i, s := range live {
		s.IdentityID = byEntityID[s.ID]
		if sum, ok := backupSummaries[s.IdentityID]; ok {
			s.SnapshotCount = sum.Count
			s.LastSnapshot = sum.Timestamp.Local().Format("2006-01-02 15:04:05")
		}
		out[i] = s
	}

	deleted, err := structurebackup.ListIdentitiesByStatus(cfg, structurebackup.IdentityDeleted)
	if err != nil {
		log.Printf("structures/all: deleted identity list: %v", err)
		return out
	}
	for _, ident := range deleted {
		row := structuresdb.Structure{
			ID:              ident.EntityID,
			Name:            ident.Name,
			Playfield:       ident.Playfield,
			FactionID:       ident.FactionId,
			FactionGroup:    int(ident.FactionGroup),
			FactionGroupStr: structuresdb.FactionGroupName(int(ident.FactionGroup)),
			IdentityID:      ident.ID,
			Deleted:         true,
		}
		if sum, ok := backupSummaries[ident.ID]; ok {
			row.SnapshotCount = sum.Count
			row.LastSnapshot = sum.Timestamp.Local().Format("2006-01-02 15:04:05")
		}
		out = append(out, row)
	}
	return out
}

func destroyWithDockedCheck(cfg config.Config, mod *modclient.Client) http.HandlerFunc {
	inner := queueableStructureModPost(cfg, mod, "/structures/destroy")
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			writeErr(w, err)
			return
		}
		var body struct {
			Id            int  `json:"id"`
			ConfirmDocked bool `json:"confirmDocked"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			writeErr(w, err)
			return
		}
		if !body.ConfirmDocked {
			byID := map[int]string{}
			for _, s := range structuresdb.CachedAll() {
				byID[s.ID] = s.Name
			}
			for _, s := range structuresdb.CachedAll() {
				if s.ID != body.Id || len(s.DockedShips) == 0 {
					continue
				}
				names := make([]string, 0, len(s.DockedShips))
				for _, childID := range s.DockedShips {
					if n, ok := byID[childID]; ok {
						names = append(names, n)
					} else {
						names = append(names, fmt.Sprintf("id %d", childID))
					}
				}
				w.WriteHeader(http.StatusConflict)

				writeJSON(w, map[string]any{
					"error":       fmt.Sprintf("к структуре пристыкованы: %s — они будут уничтожены вместе с ней. Удалите/отстыкуйте их сами, либо повторите запрос с confirmDocked:true", strings.Join(names, ", ")),
					"dockedShips": s.DockedShips,
					"dockedNames": names,
				})
				return
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		inner(w, r)
	}
}

func structureFactionHandler(cfg config.Config, mod *modclient.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			writeErr(w, err)
			return
		}
		var body struct {
			Id     int    `json:"id"`
			Abbrev string `json:"abbrev"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			writeErr(w, err)
			return
		}
		v, err := mod.Post("/structures/faction", json.RawMessage(raw))
		if err != nil {
			writeErr(w, err)
			return
		}
		recordFactionChangeAudit(cfg, body.Id, body.Abbrev)
		writeJSON(w, v)
	}
}

func recordFactionChangeAudit(cfg config.Config, structureID int, abbrev string) {
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil || !s.Audit.PermissionFactionChange {
		return
	}
	summary := fmt.Sprintf("structure id %d assigned to faction %q", structureID, abbrev)
	if err := audit.Record(cfg, audit.CategoryPermissionFactionChange, "", summary, ""); err != nil {
		log.Printf("audit: structure faction change: %v", err)
	}
}
