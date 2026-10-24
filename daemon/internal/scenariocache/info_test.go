package scenariocache

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"elb/daemon/internal/settings"
)

func TestReadInfo_ModeOff_Inactive(t *testing.T) {
	cfg := testCfg(t)
	info := ReadInfo(cfg, settings.ScenarioCache{Mode: "off"})
	if info.Active {
		t.Fatal("Mode=off must report Active=false")
	}
	if !info.BuiltAt.IsZero() || info.SizeBytes != 0 || info.URL != "" {
		t.Fatalf("Mode=off must report zero fields, got %+v", info)
	}
}

func TestReadInfo_ActiveButNeverBuilt(t *testing.T) {
	cfg := testCfg(t)
	info := ReadInfo(cfg, settings.ScenarioCache{Mode: "embedded", PublicURLBase: "http://host:8899"})
	if !info.Active {
		t.Fatal("Mode=embedded must report Active=true")
	}
	if !info.BuiltAt.IsZero() || info.SizeBytes != 0 || info.URL != "" {
		t.Fatalf("never-built cache must report zero facts, got %+v", info)
	}
}

func TestReadInfo_AfterPrepare_ReportsBuiltAtSizeAndURL(t *testing.T) {
	cfg := testCfg(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(cfg.ScenarioCacheDir, ZipName))
	}))
	defer srv.Close()

	set := settings.ScenarioCache{Mode: "embedded", PublicURLBase: srv.URL}
	Prepare(cfg, set)

	info := ReadInfo(cfg, set)
	if !info.Active {
		t.Fatal("expected Active=true")
	}
	if info.BuiltAt.IsZero() {
		t.Fatal("expected a non-zero BuiltAt")
	}
	if info.SizeBytes == 0 {
		t.Fatal("expected a non-zero SizeBytes")
	}
	if !strings.HasPrefix(info.URL, srv.URL+"/"+ZipName+"?v=") {
		t.Fatalf("unexpected URL: %q", info.URL)
	}
}

func TestComputeURL(t *testing.T) {
	got := ComputeURL("http://host:8899/", "abc123")
	want := "http://host:8899/" + ZipName + "?v=abc123"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
