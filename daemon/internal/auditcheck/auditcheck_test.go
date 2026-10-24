package auditcheck

import (
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
		AuditDBFile:  filepath.Join(dir, "audit.db"),
		SettingsFile: filepath.Join(dir, "settings.json"),
		DataRoot:     dir,
	}
	if err := settings.Save(cfg.SettingsFile, settings.Default()); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func resetState() {
	mu.Lock()
	lastPos = map[string]lastSeen{}
	flaggedFuel = map[int]bool{}
	flaggedStacks = map[string]int{}
	mu.Unlock()
	modclient.ResetAllStructuresCache()
}

func writeItemsConfig(t *testing.T, cfg config.Config, content string) {
	dir := filepath.Join(cfg.DataRoot, "Content", "Configuration")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ItemsConfig.ecf"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func onlinePlayer(steamID string, pos modclient.Vec3) modclient.Player {
	return modclient.Player{
		SteamId: steamID,
		Name:    steamID,
		Online:  true,
		Info:    &modclient.PlayerLiveInfo{Pos: pos},
	}
}

func TestCheckPositionJumps_NoEventOnFirstSighting(t *testing.T) {
	resetState()
	cfg := testCfg(t)

	checkPositionJumps(cfg, []modclient.Player{onlinePlayer("steam1", modclient.Vec3{X: 100})})

	events, err := audit.Recent(cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("первое наблюдение — базы для сравнения ещё нет, событий быть не должно, получили %d", len(events))
	}
}

func TestCheckPositionJumps_FlagsFastJump(t *testing.T) {
	resetState()
	cfg := testCfg(t)
	lastPos["steam1"] = lastSeen{pos: Vec3Pos{0, 0, 0}, at: time.Now().Add(-10 * time.Second)}

	checkPositionJumps(cfg, []modclient.Player{onlinePlayer("steam1", modclient.Vec3{X: 150000})})

	events, err := audit.Recent(cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Category != audit.CategoryPositionJump || events[0].Player != "steam1" {
		t.Fatalf("ожидали 1 событие PositionJump у steam1, получили %+v", events)
	}
}

func TestCheckPositionJumps_NoEventUnderThreshold(t *testing.T) {
	resetState()
	cfg := testCfg(t)
	lastPos["steam1"] = lastSeen{pos: Vec3Pos{0, 0, 0}, at: time.Now().Add(-10 * time.Second)}

	checkPositionJumps(cfg, []modclient.Player{onlinePlayer("steam1", modclient.Vec3{X: 500})})

	events, err := audit.Recent(cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("скорость ниже порога — событий быть не должно, получили %d", len(events))
	}
}

func TestCheckPositionJumps_SkipsOfflineAndMissingInfo(t *testing.T) {
	resetState()
	cfg := testCfg(t)
	lastPos["steam1"] = lastSeen{pos: Vec3Pos{0, 0, 0}, at: time.Now().Add(-10 * time.Second)}
	lastPos["steam2"] = lastSeen{pos: Vec3Pos{0, 0, 0}, at: time.Now().Add(-10 * time.Second)}

	offline := onlinePlayer("steam1", modclient.Vec3{X: 5000})
	offline.Online = false
	noInfo := modclient.Player{SteamId: "steam2", Online: true, Info: nil}

	checkPositionJumps(cfg, []modclient.Player{offline, noInfo})

	events, err := audit.Recent(cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("оффлайн-игрок и игрок без Info должны игнорироваться, получили %d событий", len(events))
	}
}

func TestCheckFuelAnomalies_FlagsOverCapacityAndDedupes(t *testing.T) {
	resetState()
	cfg := testCfg(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"id": 7, "name": "Base1", "fuel": 150}]`))
	}))
	defer srv.Close()
	c := modclient.New(srv.URL)

	checkFuelAnomalies(cfg, c)
	checkFuelAnomalies(cfg, c)

	events, err := audit.Recent(cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Category != audit.CategoryInventoryFuelAnomaly {
		t.Fatalf("ожидали 1 событие (дедуп повторного тика), получили %+v", events)
	}
}

func TestCheckFuelAnomalies_NoOpAtOrUnderCapacity(t *testing.T) {
	resetState()
	cfg := testCfg(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"id": 7, "name": "Base1", "fuel": 100}]`))
	}))
	defer srv.Close()

	checkFuelAnomalies(cfg, modclient.New(srv.URL))

	events, err := audit.Recent(cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("100%% — предел, не превышение, событий быть не должно, получили %d", len(events))
	}
}

func TestCheckFuelAnomalies_ReflagsAfterDroppingBelowAndRisingAgain(t *testing.T) {
	resetState()
	cfg := testCfg(t)
	responses := []string{
		`[{"id": 7, "name": "Base1", "fuel": 150}]`,
		`[{"id": 7, "name": "Base1", "fuel": 50}]`,
		`[{"id": 7, "name": "Base1", "fuel": 150}]`,
	}
	tick := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(responses[tick]))
		tick++
	}))
	defer srv.Close()
	c := modclient.New(srv.URL)

	for range responses {
		modclient.ResetAllStructuresCache()
		checkFuelAnomalies(cfg, c)
	}

	events, err := audit.Recent(cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("превышение/сброс/превышение снова — ожидали 2 отдельных события, получили %d: %+v", len(events), events)
	}
}

func TestStackSizeByItemID_PrefersModItemsEndpoint(t *testing.T) {
	cfg := testCfg(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/items":
			w.Write([]byte(`{"Iron": 1, "Wood": 2}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	writeItemsConfig(t, cfg, `{ +Item Id: 1, Name: Iron
  StackSize: 500
}
{ +Item Id: 2, Name: Wood
  StackSize: 64
}
`)

	sizes, err := stackSizeByItemID(cfg, modclient.New(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if sizes[1] != 500 || sizes[2] != 64 {
		t.Fatalf("unexpected sizes: %+v", sizes)
	}
}

func blocksmapBytes(entries map[string]int) []byte {
	buf := make([]byte, 9)
	for name, id := range entries {
		buf = append(buf, byte(len(name)))
		buf = append(buf, []byte(name)...)
		buf = append(buf, byte(id&0xff), byte((id>>8)&0xff))
	}
	return buf
}

func TestStackSizeByItemID_FallsBackToBlocksmapWhenModItemsUnavailable(t *testing.T) {
	cfg := testCfg(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	writeItemsConfig(t, cfg, `{ +Item Id: 1, Name: Iron
  StackSize: 500
}
`)
	dedicatedYAML := filepath.Join(cfg.DataRoot, "dedicated.yaml")
	if err := os.WriteFile(dedicatedYAML, []byte("GameName: TestGame\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.DedicatedYAML = dedicatedYAML

	saveDir := filepath.Join(cfg.DataRoot, "Saves", "Games", "TestGame")
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(saveDir, "blocksmap.dat"), blocksmapBytes(map[string]int{"Iron": 1}), 0o644); err != nil {
		t.Fatal(err)
	}

	sizes, err := stackSizeByItemID(cfg, modclient.New(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if sizes[1] != 500 {
		t.Fatalf("unexpected sizes: %+v", sizes)
	}
}

func TestCheckStackAnomalies_FlagsOverStackSize(t *testing.T) {
	resetState()
	cfg := testCfg(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/items":
			w.Write([]byte(`{"Iron": 1}`))
		case "/players/inventory":
			w.Write([]byte(`{"playerId": 42, "toolbelt": [{"id": 1, "count": 9999, "slotIdx": 0}], "bag": []}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	writeItemsConfig(t, cfg, `{ +Item Id: 1, Name: Iron
  StackSize: 500
}
`)

	players := []modclient.Player{{SteamId: "steam1", Name: "P1", EntityId: 42, Online: true}}
	checkStackAnomalies(cfg, modclient.New(srv.URL), players)
	checkStackAnomalies(cfg, modclient.New(srv.URL), players)

	events, err := audit.Recent(cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Category != audit.CategoryInventoryFuelAnomaly || events[0].Player != "steam1" {
		t.Fatalf("ожидали 1 событие превышения стека у steam1, получили %+v", events)
	}
}

func TestCheckStackAnomalies_NoOpWithinStackSize(t *testing.T) {
	resetState()
	cfg := testCfg(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/items":
			w.Write([]byte(`{"Iron": 1}`))
		case "/players/inventory":
			w.Write([]byte(`{"playerId": 42, "toolbelt": [{"id": 1, "count": 100, "slotIdx": 0}], "bag": []}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	writeItemsConfig(t, cfg, `{ +Item Id: 1, Name: Iron
  StackSize: 500
}
`)

	players := []modclient.Player{{SteamId: "steam1", Name: "P1", EntityId: 42, Online: true}}
	checkStackAnomalies(cfg, modclient.New(srv.URL), players)

	events, err := audit.Recent(cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("стек в пределах лимита — событий быть не должно, получили %d", len(events))
	}
}

func TestPollOnce_NoOpWhenAllDisabled(t *testing.T) {
	resetState()
	cfg := testCfg(t)

	if err := PollOnce(cfg, modclient.New("http://127.0.0.1:0")); err != nil {
		t.Fatalf("PollOnce не должен даже пытаться сходить к моду, когда всё выключено: %v", err)
	}

	events, err := audit.Recent(cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("всё выключено — событий быть не должно, получили %d", len(events))
	}
}
