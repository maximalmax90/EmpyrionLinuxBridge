package watchdog

import (
	"os"
	"testing"
	"time"

	"elb/daemon/internal/config"
)

func TestManualDiagnosticsSmoke(t *testing.T) {
	if os.Getenv("ELB_SMOKE") != "1" {
		t.Skip("ELB_SMOKE=1 не задан — пропуск живого смоук-теста")
	}
	pid, err := findGamePID()
	if err != nil {
		t.Fatalf("findGamePID: %v", err)
	}
	t.Logf("найден PID EmpyrionDedicated.exe: %d", pid)

	cfg := config.Config{DataRoot: "/tmp/elb-smoke", SystemdUnit: "empyrion.service"}
	writeDiagnostics(cfg, time.Now())

	entries, err := os.ReadDir(HangDiagnosticsDir(cfg))
	if err != nil {
		t.Fatalf("не удалось прочитать директорию снимков: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("снимок не был создан")
	}
	data, err := os.ReadFile(HangDiagnosticsDir(cfg) + "/" + entries[len(entries)-1].Name())
	if err != nil {
		t.Fatalf("не удалось прочитать снимок: %v", err)
	}
	t.Logf("снимок (%d байт):\n%s", len(data), string(data))
}
