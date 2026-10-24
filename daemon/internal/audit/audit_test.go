package audit

import (
	"path/filepath"
	"testing"
	"time"

	"elb/daemon/internal/config"
)

func testCfg(t *testing.T) config.Config {
	t.Helper()
	return config.Config{AuditDBFile: filepath.Join(t.TempDir(), "audit.db")}
}

func TestRecordAndRecent(t *testing.T) {
	cfg := testCfg(t)
	if err := Record(cfg, CategoryPositionJump, "76561198000000000", "прыжок 350м/с", `{"deltaM":700,"seconds":2}`); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := Record(cfg, CategoryLoginTracking, "", "вход admin OK", "127.0.0.1"); err != nil {
		t.Fatalf("Record: %v", err)
	}

	events, err := Recent(cfg, 0)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	if events[0].Category != CategoryLoginTracking {
		t.Errorf("expected newest first (loginTracking), got %s", events[0].Category)
	}
	if events[1].Category != CategoryPositionJump || events[1].Player != "76561198000000000" {
		t.Errorf("unexpected second event: %+v", events[1])
	}
}

func TestRecentLimit(t *testing.T) {
	cfg := testCfg(t)
	for i := 0; i < 5; i++ {
		if err := Record(cfg, CategoryWipeLeftover, "p", "s", ""); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	events, err := Recent(cfg, 2)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events (limit), got %d", len(events))
	}
}

func TestBetween(t *testing.T) {
	cfg := testCfg(t)
	if err := Record(cfg, CategoryInventoryFuelAnomaly, "p1", "stack > max", ""); err != nil {
		t.Fatalf("Record: %v", err)
	}

	now := time.Now().UTC()
	from := now.Add(-time.Hour).Format(time.RFC3339Nano)
	to := now.Add(time.Hour).Format(time.RFC3339Nano)
	events, err := Between(cfg, from, to, 0)
	if err != nil {
		t.Fatalf("Between: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event in range, got %d", len(events))
	}

	farFrom := now.Add(2 * time.Hour).Format(time.RFC3339Nano)
	farTo := now.Add(3 * time.Hour).Format(time.RFC3339Nano)
	events, err = Between(cfg, farFrom, farTo, 0)
	if err != nil {
		t.Fatalf("Between: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events outside range, got %d", len(events))
	}
}
