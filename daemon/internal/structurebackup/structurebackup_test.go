package structurebackup

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/modclient"
)

func TestReconcileIdentities_SkipsStructuresAllWhenNoPlayfieldsLoaded(t *testing.T) {
	cfg := config.Config{StructureBackupDBFile: filepath.Join(t.TempDir(), "structurebackup.db")}
	db, err := getDB(cfg)
	if err != nil {
		t.Fatalf("getDB: %v", err)
	}
	if _, err := resolveIdentity(db, "База", "Akua", 1, 100, 555); err != nil {
		t.Fatalf("resolveIdentity: %v", err)
	}

	structuresAllHit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/playfield-servers":
			json.NewEncoder(w).Encode(map[string]any{})
		case "/structures/all":
			structuresAllHit = true
			json.NewEncoder(w).Encode([]any{})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	if err := ReconcileIdentities(cfg, modclient.New(srv.URL)); err != nil {
		t.Fatalf("ReconcileIdentities: %v", err)
	}
	if structuresAllHit {
		t.Error("/structures/all не должен вызываться при нуле загруженных плейфилдов")
	}
}

func TestReconcileIdentities_ChecksStructuresAllWhenPlayfieldsLoaded(t *testing.T) {
	cfg := config.Config{StructureBackupDBFile: filepath.Join(t.TempDir(), "structurebackup.db")}
	db, err := getDB(cfg)
	if err != nil {
		t.Fatalf("getDB: %v", err)
	}
	if _, err := resolveIdentity(db, "База", "Akua", 1, 100, 555); err != nil {
		t.Fatalf("resolveIdentity: %v", err)
	}

	structuresAllHit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/playfield-servers":
			json.NewEncoder(w).Encode(map[string]any{"1234": []any{"Akua"}})
		case "/structures/all":
			structuresAllHit = true
			json.NewEncoder(w).Encode([]any{map[string]any{"id": float64(555)}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	if err := ReconcileIdentities(cfg, modclient.New(srv.URL)); err != nil {
		t.Fatalf("ReconcileIdentities: %v", err)
	}
	if !structuresAllHit {
		t.Error("/structures/all должен вызываться, когда есть загруженные плейфилды")
	}
}

func TestResolveIdentity_SamePlayfieldChangeKeepsIdentity(t *testing.T) {
	cfg := config.Config{StructureBackupDBFile: filepath.Join(t.TempDir(), "structurebackup.db")}
	db, err := getDB(cfg)
	if err != nil {
		t.Fatalf("getDB: %v", err)
	}
	id1, err := resolveIdentity(db, "Забуханка", "Akua", 1, 100, 555)
	if err != nil {
		t.Fatalf("resolveIdentity (Akua): %v", err)
	}
	id2, err := resolveIdentity(db, "Забуханка", "Omicron", 1, 100, 555)
	if err != nil {
		t.Fatalf("resolveIdentity (Omicron, тот же entity_id): %v", err)
	}
	if id1 != id2 {
		t.Fatalf("identity id изменился при смене плейфилда (%d -> %d), тот же entity_id должен давать ту же identity", id1, id2)
	}
}

func TestResolveIdentity_RespawnWithSameFingerprintReusesIdentity(t *testing.T) {
	cfg := config.Config{StructureBackupDBFile: filepath.Join(t.TempDir(), "structurebackup.db")}
	db, err := getDB(cfg)
	if err != nil {
		t.Fatalf("getDB: %v", err)
	}
	id1, err := resolveIdentity(db, "Забуханка", "Akua", 1, 100, 555)
	if err != nil {
		t.Fatalf("resolveIdentity (до рестарта): %v", err)
	}
	id2, err := resolveIdentity(db, "Забуханка", "Omicron", 1, 100, 777)
	if err != nil {
		t.Fatalf("resolveIdentity (после рестарта, новый entity_id): %v", err)
	}
	if id1 != id2 {
		t.Fatalf("identity id изменился после респауна (%d -> %d), ожидали переиспользование по отпечатку имя+фракция", id1, id2)
	}
}

func TestResolveIdentity_DifferentNameGetsDifferentIdentity(t *testing.T) {
	cfg := config.Config{StructureBackupDBFile: filepath.Join(t.TempDir(), "structurebackup.db")}
	db, err := getDB(cfg)
	if err != nil {
		t.Fatalf("getDB: %v", err)
	}
	id1, err := resolveIdentity(db, "Забуханка", "Akua", 1, 100, 555)
	if err != nil {
		t.Fatalf("resolveIdentity (Забуханка): %v", err)
	}
	id2, err := resolveIdentity(db, "FST_SV", "Akua", 1, 100, 556)
	if err != nil {
		t.Fatalf("resolveIdentity (FST_SV): %v", err)
	}
	if id1 == id2 {
		t.Fatalf("разные структуры получили одну identity (%d)", id1)
	}
}

func insertRawBackup(t *testing.T, cfg config.Config, structureID int, timestamp string) {
	t.Helper()
	db, err := getDB(cfg)
	if err != nil {
		t.Fatalf("getDB: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO structure_backups
		(structure_id, playfield, name, type_name, pos_x, pos_y, pos_z, faction_group, faction_id, timestamp, containers, blueprint)
		VALUES (?, 'Akua', 'X', 'CV', 0, 0, 0, 1, 1, ?, '[]', x'00')`, structureID, timestamp); err != nil {
		t.Fatalf("insert raw backup: %v", err)
	}
}

func TestMergeFragmentedIdentities_MergesSameFingerprintGroup(t *testing.T) {
	cfg := config.Config{StructureBackupDBFile: filepath.Join(t.TempDir(), "structurebackup.db")}
	db, err := getDB(cfg)
	if err != nil {
		t.Fatalf("getDB: %v", err)
	}

	mk := func(name, playfield string, entityID int, updatedAt string) int {
		res, err := db.Exec(`INSERT INTO structure_identities (name, playfield, faction_group, faction_id, entity_id, status, updated_at) VALUES (?, ?, 1, 1, ?, 'active', ?)`,
			name, playfield, entityID, updatedAt)
		if err != nil {
			t.Fatalf("insert identity: %v", err)
		}
		id, _ := res.LastInsertId()
		return int(id)
	}
	old1 := mk("Забуханка", "Akua", 12, "2026-10-09T01:00:00Z")
	old2 := mk("Забуханка", "Omicron", 17, "2026-10-11T08:00:00Z")

	canonical := mk("Забуханка", "Ningues", 27, "2026-10-13T08:16:00Z")
	other := mk("FST_SV", "Akua", 4, "2026-10-13T08:16:00Z")

	insertRawBackup(t, cfg, old1, "2026-10-09T01:00:00Z")
	insertRawBackup(t, cfg, old2, "2026-10-11T08:00:00Z")
	insertRawBackup(t, cfg, canonical, "2026-10-13T08:16:00Z")
	insertRawBackup(t, cfg, other, "2026-10-13T08:16:00Z")

	report, err := MergeFragmentedIdentities(cfg)
	if err != nil {
		t.Fatalf("MergeFragmentedIdentities: %v", err)
	}
	if report.GroupsMerged != 1 || report.IdentitiesRemoved != 2 || report.BackupsRepointed != 2 {
		t.Fatalf("report = %+v, want {GroupsMerged:1 IdentitiesRemoved:2 BackupsRepointed:2}", report)
	}

	entries, err := List(cfg, canonical)
	if err != nil {
		t.Fatalf("List(canonical): %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("List(canonical) = %d записей, want 3 (все перевешены на канонический id)", len(entries))
	}

	for _, oldID := range []int{old1, old2} {
		if _, ok, err := GetIdentity(cfg, oldID); err != nil {
			t.Fatalf("GetIdentity(%d): %v", oldID, err)
		} else if ok {
			t.Fatalf("identity %d должна быть удалена после склейки", oldID)
		}
	}

	otherEntries, err := List(cfg, other)
	if err != nil {
		t.Fatalf("List(other): %v", err)
	}
	if len(otherEntries) != 1 {
		t.Fatalf("List(other) = %d, другая структура не должна была затронута", len(otherEntries))
	}
}

func TestSummary_AggregatesCountAndLatestTimestampPerStructure(t *testing.T) {
	cfg := config.Config{StructureBackupDBFile: filepath.Join(t.TempDir(), "structurebackup.db")}
	insertRawBackup(t, cfg, 1, "2026-10-09T01:00:00Z")
	insertRawBackup(t, cfg, 1, "2026-10-11T08:00:00Z")
	insertRawBackup(t, cfg, 1, "2026-10-13T08:16:00Z")
	insertRawBackup(t, cfg, 2, "2026-10-12T00:00:00Z")

	summary, err := Summary(cfg)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if summary[1].Count != 3 {
		t.Fatalf("summary[1].Count = %d, want 3", summary[1].Count)
	}
	want := time.Date(2026, 10, 13, 8, 16, 0, 0, time.UTC)
	if !summary[1].Timestamp.Equal(want) {
		t.Fatalf("summary[1].Timestamp = %v, want %v", summary[1].Timestamp, want)
	}
	if summary[2].Count != 1 {
		t.Fatalf("summary[2].Count = %d, want 1", summary[2].Count)
	}
	if _, ok := summary[3]; ok {
		t.Fatal("summary[3] не должно быть — у этого id нет ни одного бэкапа")
	}
}
