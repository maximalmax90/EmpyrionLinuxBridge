package sysinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func writeNetFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadNetIfacesFrom_SkipsLoopback(t *testing.T) {
	root := t.TempDir()
	writeNetFile(t, filepath.Join(root, "lo", "statistics", "rx_bytes"), "123")
	writeNetFile(t, filepath.Join(root, "lo", "statistics", "tx_bytes"), "123")
	writeNetFile(t, filepath.Join(root, "eth0", "statistics", "rx_bytes"), "1000")
	writeNetFile(t, filepath.Join(root, "eth0", "statistics", "tx_bytes"), "2000")

	got, err := ReadNetIfacesFrom(root)
	if err != nil {
		t.Fatalf("ReadNetIfacesFrom: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d ifaces, want 1 (lo excluded): %+v", len(got), got)
	}
	if got[0] != (NetIface{Name: "eth0", RxBytes: 1000, TxBytes: 2000}) {
		t.Fatalf("got %+v", got[0])
	}
}

func TestReadNetIfacesFrom_MultipleIfacesSortedByName(t *testing.T) {
	root := t.TempDir()
	writeNetFile(t, filepath.Join(root, "wlan0", "statistics", "rx_bytes"), "5")
	writeNetFile(t, filepath.Join(root, "wlan0", "statistics", "tx_bytes"), "6")
	writeNetFile(t, filepath.Join(root, "eth0", "statistics", "rx_bytes"), "1")
	writeNetFile(t, filepath.Join(root, "eth0", "statistics", "tx_bytes"), "2")

	got, err := ReadNetIfacesFrom(root)
	if err != nil {
		t.Fatalf("ReadNetIfacesFrom: %v", err)
	}
	if len(got) != 2 || got[0].Name != "eth0" || got[1].Name != "wlan0" {
		t.Fatalf("got %+v, want eth0 then wlan0", got)
	}
}

func TestReadNetIfacesFrom_SkipsIfaceWithoutStatistics(t *testing.T) {
	root := t.TempDir()

	if err := os.MkdirAll(filepath.Join(root, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeNetFile(t, filepath.Join(root, "eth0", "statistics", "rx_bytes"), "1")
	writeNetFile(t, filepath.Join(root, "eth0", "statistics", "tx_bytes"), "2")

	got, err := ReadNetIfacesFrom(root)
	if err != nil {
		t.Fatalf("ReadNetIfacesFrom: %v", err)
	}
	if len(got) != 1 || got[0].Name != "eth0" {
		t.Fatalf("got %+v, want only eth0", got)
	}
}

func TestReadNetIfacesFrom_SkipsVirtualIfaces(t *testing.T) {
	root := t.TempDir()
	devRoot := t.TempDir()

	physTarget := filepath.Join(devRoot, "pci0000:00", "net", "enp7s0")
	writeNetFile(t, filepath.Join(physTarget, "statistics", "rx_bytes"), "10")
	writeNetFile(t, filepath.Join(physTarget, "statistics", "tx_bytes"), "20")
	if err := os.Symlink(physTarget, filepath.Join(root, "enp7s0")); err != nil {
		t.Fatal(err)
	}

	virtTarget := filepath.Join(devRoot, "devices", "virtual", "net", "docker0")
	writeNetFile(t, filepath.Join(virtTarget, "statistics", "rx_bytes"), "999999")
	writeNetFile(t, filepath.Join(virtTarget, "statistics", "tx_bytes"), "999999")
	if err := os.Symlink(virtTarget, filepath.Join(root, "docker0")); err != nil {
		t.Fatal(err)
	}

	got, err := ReadNetIfacesFrom(root)
	if err != nil {
		t.Fatalf("ReadNetIfacesFrom: %v", err)
	}
	if len(got) != 1 || got[0].Name != "enp7s0" {
		t.Fatalf("got %+v, want только enp7s0 (docker0 — виртуальный, должен быть исключён)", got)
	}
}

func TestReadNetIfacesFrom_MissingRootIsNotAnError(t *testing.T) {
	got, err := ReadNetIfacesFrom(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("expected no error for missing root, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil result for missing root, got %+v", got)
	}
}
