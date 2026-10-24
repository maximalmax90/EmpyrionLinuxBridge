package scenariocache

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"elb/daemon/internal/config"
	"elb/daemon/internal/settings"
)

const testDedicatedYAML = "ServerConfig:\r\n" +
	"    Srv_Port: 30000\r\n" +
	"\r\n" +
	"GameConfig:\r\n" +
	"    CustomScenario: TestScenario\r\n"

func testCfg(t *testing.T) config.Config {
	t.Helper()
	root := t.TempDir()
	sharedData := filepath.Join(root, "Content", "Scenarios", "TestScenario", "SharedData")
	if err := os.MkdirAll(filepath.Join(sharedData, "Content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedData, "Content", "a.yaml"), []byte("a: 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	yamlPath := filepath.Join(root, "dedicated.yaml")
	if err := os.WriteFile(yamlPath, []byte(testDedicatedYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(root, "scenario-cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return config.Config{
		DedicatedYAML:    yamlPath,
		DataRoot:         root,
		ScenarioCacheDir: cacheDir,
		NotifyDBFile:     filepath.Join(root, "notify.db"),
	}
}

func TestPrepare_ModeOff_DoesNothing(t *testing.T) {
	cfg := testCfg(t)
	Prepare(cfg, settings.ScenarioCache{Mode: "off"})
	if _, err := os.Stat(filepath.Join(cfg.ScenarioCacheDir, ZipName)); !os.IsNotExist(err) {
		t.Fatalf("Mode=off must not build anything, stat err=%v", err)
	}
	st, err := ReadStatus(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != PhaseIdle {
		t.Fatalf("got phase %q, want idle", st.Phase)
	}
}

func TestPrepare_SwitchingToModeOff_ClearsStaleSharedDataURL(t *testing.T) {
	cfg := testCfg(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(cfg.ScenarioCacheDir, ZipName))
	}))
	defer srv.Close()

	Prepare(cfg, settings.ScenarioCache{Mode: "embedded", PublicURLBase: srv.URL})
	if _, ok, err := cfg.GameField("SharedDataURL"); err != nil || !ok {
		t.Fatalf("precondition: SharedDataURL должен быть активен после успешного Prepare, ok=%v err=%v", ok, err)
	}

	Prepare(cfg, settings.ScenarioCache{Mode: "off"})
	_, ok, err := cfg.GameField("SharedDataURL")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("SharedDataURL должен быть очищен при переключении на Mode=off")
	}
	st, err := ReadStatus(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != PhaseIdle {
		t.Fatalf("got phase %q, want idle после Mode=off", st.Phase)
	}
}

func TestPrepare_HappyPath_WritesSharedDataURLAndDoneStatus(t *testing.T) {
	cfg := testCfg(t)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(cfg.ScenarioCacheDir, ZipName))
	}))
	defer srv.Close()

	Prepare(cfg, settings.ScenarioCache{Mode: "embedded", PublicURLBase: srv.URL})

	st, err := ReadStatus(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != PhaseDone {
		t.Fatalf("got phase %q (%s), want done", st.Phase, st.Message)
	}

	data, err := os.ReadFile(cfg.DedicatedYAML)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "SharedDataURL: _"+srv.URL+"/"+ZipName+"?v=") {
		t.Fatalf("SharedDataURL not written as expected:\n%s", content)
	}

	v, ok, err := cfg.GameField("SharedDataURL")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !strings.HasPrefix(v, "_"+srv.URL) {
		t.Fatalf("GameField round-trip: v=%q ok=%v", v, ok)
	}
}

func TestPrepare_UnreachableURL_FallsBackAndClearsField(t *testing.T) {
	cfg := testCfg(t)

	if err := cfg.SetGameField("SharedDataURL", "_http://stale.example/old.zip?v=1"); err != nil {
		t.Fatal(err)
	}

	Prepare(cfg, settings.ScenarioCache{Mode: "embedded", PublicURLBase: "http://127.0.0.1:1"})

	st, err := ReadStatus(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != PhaseFailed {
		t.Fatalf("got phase %q, want failed", st.Phase)
	}
	if st.Message == "" {
		t.Fatal("expected a failure message")
	}

	_, ok, err := cfg.GameField("SharedDataURL")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("SharedDataURL should be commented out (cleared) after a failed prepare")
	}
}

func TestPrepare_MissingPublicURLBase_FallsBack(t *testing.T) {
	cfg := testCfg(t)
	Prepare(cfg, settings.ScenarioCache{Mode: "panel"})
	st, err := ReadStatus(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != PhaseFailed {
		t.Fatalf("got phase %q, want failed", st.Phase)
	}
}

func TestPrepare_SecondCallWithUnchangedSharedData_SkipsRebuild(t *testing.T) {
	cfg := testCfg(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(cfg.ScenarioCacheDir, ZipName))
	}))
	defer srv.Close()

	Prepare(cfg, settings.ScenarioCache{Mode: "embedded", PublicURLBase: srv.URL})
	zipPath := filepath.Join(cfg.ScenarioCacheDir, ZipName)
	info1, err := os.Stat(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	Prepare(cfg, settings.ScenarioCache{Mode: "embedded", PublicURLBase: srv.URL})
	info2, err := os.Stat(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Fatalf("zip was rebuilt despite unchanged SharedData: mtime %v -> %v", info1.ModTime(), info2.ModTime())
	}
}
