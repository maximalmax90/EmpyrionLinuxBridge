package backpack

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"elb/daemon/internal/audit"
	"elb/daemon/internal/config"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/settings"
)

func testCfg(t *testing.T) config.Config {
	dir := t.TempDir()
	cfg := config.Config{
		BackpackDBFile:   filepath.Join(dir, "backpack.db"),
		PlayerHistoryDir: filepath.Join(dir, "players"),
		SettingsFile:     filepath.Join(dir, "settings.json"),
		AuditDBFile:      filepath.Join(dir, "audit.db"),
	}
	if err := settings.Save(cfg.SettingsFile, settings.Default()); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func stack(id, count, slot int) modclient.ItemStack {
	return modclient.ItemStack{Id: id, Count: count, SlotIdx: slot}
}

func TestAppendIfNeeded_FirstCallAlwaysInserts(t *testing.T) {
	cfg := testCfg(t)
	inv := modclient.Inventory{Toolbelt: []modclient.ItemStack{stack(100, 1, 0)}, Bag: nil}
	if err := appendIfNeeded(cfg, "steam1", inv, time.Minute, 0); err != nil {
		t.Fatal(err)
	}
	entries, err := History(cfg, "steam1")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("ожидали 1 запись, получили %d", len(entries))
	}
}

func TestAppendIfNeeded_AmmoDecayDriftIgnored(t *testing.T) {
	cfg := testCfg(t)
	inv1 := modclient.Inventory{Bag: []modclient.ItemStack{{Id: 9497, Count: 1, SlotIdx: 0, Ammo: 6999}}}
	if err := appendIfNeeded(cfg, "steam1", inv1, time.Minute, 0); err != nil {
		t.Fatal(err)
	}

	inv2 := modclient.Inventory{Bag: []modclient.ItemStack{{Id: 9497, Count: 1, SlotIdx: 0, Ammo: 6998}}}
	if err := appendIfNeeded(cfg, "steam1", inv2, time.Minute, 0); err != nil {
		t.Fatal(err)
	}
	entries, err := History(cfg, "steam1")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("ожидали 1 запись (ammo-дрейф не должен создавать новую), получили %d", len(entries))
	}
	if entries[0].Bag[0].Ammo != 6999 {
		t.Fatalf("ammo-дрейф не должен трогать даже существующую запись, получили %d", entries[0].Bag[0].Ammo)
	}
}

func TestAppendIfNeeded_RealChangeWithinIntervalUpdatesInPlace(t *testing.T) {
	cfg := testCfg(t)
	inv1 := modclient.Inventory{Bag: []modclient.ItemStack{stack(1, 1, 0)}}
	if err := appendIfNeeded(cfg, "steam1", inv1, time.Minute, 0); err != nil {
		t.Fatal(err)
	}
	inv2 := modclient.Inventory{Bag: []modclient.ItemStack{stack(1, 1, 0), stack(2, 1, 1)}}
	if err := appendIfNeeded(cfg, "steam1", inv2, time.Minute, 0); err != nil {
		t.Fatal(err)
	}
	entries, err := History(cfg, "steam1")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("реальное изменение раньше интервала должно ПРАВИТЬ последнюю запись, а не добавлять новую; записей: %d", len(entries))
	}
	if len(entries[0].Bag) != 2 {
		t.Fatalf("запись должна отражать последнее состояние (2 предмета), получили %d", len(entries[0].Bag))
	}
}

func TestAppendIfNeeded_FutureLastEntryIsNotOverwritten(t *testing.T) {
	cfg := testCfg(t)
	db, err := getDB(cfg)
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := insertEntry(db, "steam1", Entry{Timestamp: future, Bag: []modclient.ItemStack{stack(1, 1, 0)}}); err != nil {
		t.Fatal(err)
	}

	inv := modclient.Inventory{Bag: []modclient.ItemStack{stack(2, 1, 0)}}
	if err := appendIfNeeded(cfg, "steam1", inv, time.Minute, 0); err != nil {
		t.Fatal(err)
	}

	entries, err := History(cfg, "steam1")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("запись из будущего должна остаться нетронутой, новое изменение — добавиться отдельно; записей: %d", len(entries))
	}

	if entries[1].Bag[0].Id != 1 || !entries[1].Timestamp.Equal(future) {
		t.Fatalf("исходная 'будущая' запись повреждена или не последняя: %#v", entries[1])
	}
	if entries[0].Bag[0].Id != 2 {
		t.Fatalf("новое изменение должно быть отдельной (более ранней по сортировке) записью: %#v", entries[0])
	}
}

func TestAppendIfNeeded_RealChangeAfterIntervalAppendsNew(t *testing.T) {
	cfg := testCfg(t)
	inv1 := modclient.Inventory{Bag: []modclient.ItemStack{stack(1, 1, 0)}}
	if err := appendIfNeeded(cfg, "steam1", inv1, 10*time.Millisecond, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	inv2 := modclient.Inventory{Bag: []modclient.ItemStack{stack(1, 1, 0), stack(2, 1, 1)}}
	if err := appendIfNeeded(cfg, "steam1", inv2, 10*time.Millisecond, 0); err != nil {
		t.Fatal(err)
	}
	entries, err := History(cfg, "steam1")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("реальное изменение после интервала должно ДОБАВИТЬ новую запись; записей: %d", len(entries))
	}
}

func TestMigrateJSONFiles_ImportsAndRenamesSource(t *testing.T) {
	cfg := testCfg(t)
	if err := os.MkdirAll(cfg.PlayerHistoryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := []Entry{
		{Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Toolbelt: nil, Bag: []modclient.ItemStack{stack(1, 1, 0)}},
		{Timestamp: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Toolbelt: nil, Bag: []modclient.ItemStack{stack(1, 1, 0), stack(2, 1, 1)}},
	}
	data, _ := json.Marshal(old)
	jsonPath := filepath.Join(cfg.PlayerHistoryDir, "76500000000000001.json")
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := MigrateJSONFiles(cfg); err != nil {
		t.Fatal(err)
	}

	entries, err := History(cfg, "76500000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("ожидали 2 перенесённые записи, получили %d", len(entries))
	}
	if !entries[0].Timestamp.Equal(old[0].Timestamp) {
		t.Fatalf("timestamp первой записи не совпал после переноса: %v vs %v", entries[0].Timestamp, old[0].Timestamp)
	}

	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Fatal("исходный .json должен быть переименован после успешного переноса")
	}
	if _, err := os.Stat(jsonPath + ".migrated"); err != nil {
		t.Fatalf("ожидали %s.migrated: %v", jsonPath, err)
	}

	if err := MigrateJSONFiles(cfg); err != nil {
		t.Fatal(err)
	}
	entries, err = History(cfg, "76500000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("повторная миграция задублировала записи: %d", len(entries))
	}
}

func TestPollCleanupOnce_DeletesHistoryAndConsumesQueueFile(t *testing.T) {
	cfg := testCfg(t)
	inv := modclient.Inventory{Bag: []modclient.ItemStack{stack(1, 1, 0)}}
	if err := appendIfNeeded(cfg, "steam1", inv, time.Minute, 0); err != nil {
		t.Fatal(err)
	}

	dir := cleanupQueueDir(cfg)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	reqPath := filepath.Join(dir, "steam1_1.json")
	if err := os.WriteFile(reqPath, []byte(`{"steamId":"steam1"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := PollCleanupOnce(cfg, modclient.New("http://127.0.0.1:0")); err != nil {
		t.Fatal(err)
	}

	entries, err := History(cfg, "steam1")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("история должна быть очищена, осталось %d записей", len(entries))
	}
	if _, err := os.Stat(reqPath); !os.IsNotExist(err) {
		t.Fatal("обработанный файл заявки должен быть удалён")
	}
}

func TestCheckWipeLeftoverStructures_RecordsWhenEnabledAndOwnedStructuresRemain(t *testing.T) {
	cfg := testCfg(t)
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		t.Fatal(err)
	}
	s.Audit.WipeLeftover = true
	if err := settings.Save(cfg.SettingsFile, s); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		w.Write([]byte(`[
			{"id": 1, "factionGroup": 1, "factionId": 42},
			{"id": 2, "factionGroup": 1, "factionId": 99}
		]`))
	}))
	defer srv.Close()

	checkWipeLeftoverStructures(cfg, modclient.New(srv.URL), cleanupRequest{SteamID: "steam1", EntityID: 42})

	events, err := audit.Recent(cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	if events[0].Category != audit.CategoryWipeLeftover || events[0].Player != "steam1" {
		t.Errorf("unexpected event: %+v", events[0])
	}
}

func TestCheckWipeLeftoverStructures_NoOpWhenDisabled(t *testing.T) {
	cfg := testCfg(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"id": 1, "factionGroup": 1, "factionId": 42}]`))
	}))
	defer srv.Close()

	checkWipeLeftoverStructures(cfg, modclient.New(srv.URL), cleanupRequest{SteamID: "steam1", EntityID: 42})

	events, err := audit.Recent(cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("audit выключен — событий быть не должно, получили %d", len(events))
	}
}

func TestCheckWipeLeftoverStructures_NoOpWhenNoStructuresLeft(t *testing.T) {
	cfg := testCfg(t)
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		t.Fatal(err)
	}
	s.Audit.WipeLeftover = true
	if err := settings.Save(cfg.SettingsFile, s); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"id": 1, "factionGroup": 1, "factionId": 99}]`))
	}))
	defer srv.Close()

	checkWipeLeftoverStructures(cfg, modclient.New(srv.URL), cleanupRequest{SteamID: "steam1", EntityID: 42})

	events, err := audit.Recent(cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("у игрока нет оставшихся структур — событий быть не должно, получили %d", len(events))
	}
}
