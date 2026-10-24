package sensors

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadTempsFrom_LiveHardwareShape(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "hwmon0", "name"), "nvme\n")
	writeFile(t, filepath.Join(root, "hwmon0", "temp1_input"), "33850")
	writeFile(t, filepath.Join(root, "hwmon0", "temp1_label"), "Composite\n")
	writeFile(t, filepath.Join(root, "hwmon0", "temp2_input"), "33850")
	writeFile(t, filepath.Join(root, "hwmon0", "temp2_label"), "Sensor 1\n")
	writeFile(t, filepath.Join(root, "hwmon0", "temp3_input"), "30850")
	writeFile(t, filepath.Join(root, "hwmon0", "temp3_label"), "Sensor 2\n")

	writeFile(t, filepath.Join(root, "hwmon1", "name"), "coretemp\n")
	writeFile(t, filepath.Join(root, "hwmon1", "temp1_input"), "35000")
	writeFile(t, filepath.Join(root, "hwmon1", "temp1_label"), "Package id 0\n")
	writeFile(t, filepath.Join(root, "hwmon1", "temp2_input"), "31000")
	writeFile(t, filepath.Join(root, "hwmon1", "temp2_label"), "Core 0\n")

	writeFile(t, filepath.Join(root, "hwmon2", "name"), "nouveau\n")
	writeFile(t, filepath.Join(root, "hwmon2", "temp1_input"), "55000")

	got, err := ReadTempsFrom(root)
	if err != nil {
		t.Fatalf("ReadTempsFrom: %v", err)
	}
	if len(got) != 6 {
		t.Fatalf("got %d temps, want 6: %+v", len(got), got)
	}

	want := map[string]Temp{
		"nvme/Composite":        {"nvme", "Composite", 33.85},
		"nvme/Sensor 1":         {"nvme", "Sensor 1", 33.85},
		"nvme/Sensor 2":         {"nvme", "Sensor 2", 30.85},
		"coretemp/Package id 0": {"coretemp", "Package id 0", 35.0},
		"coretemp/Core 0":       {"coretemp", "Core 0", 31.0},
		"nouveau/temp1":         {"nouveau", "temp1", 55.0},
	}
	for _, tp := range got {
		key := tp.Chip + "/" + tp.Label
		w, ok := want[key]
		if !ok {
			t.Fatalf("unexpected temp %+v", tp)
		}
		if tp.C != w.C {
			t.Fatalf("%s: got %.2f°C, want %.2f°C", key, tp.C, w.C)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatalf("missing temps: %+v", want)
	}
}

func TestReadTempsFrom_DisambiguatesDuplicateChipNames(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "hwmon0", "name"), "nvme\n")
	writeFile(t, filepath.Join(root, "hwmon0", "temp1_input"), "34000")
	writeFile(t, filepath.Join(root, "hwmon0", "temp1_label"), "Composite\n")

	writeFile(t, filepath.Join(root, "hwmon1", "name"), "nvme\n")
	writeFile(t, filepath.Join(root, "hwmon1", "temp1_input"), "38000")
	writeFile(t, filepath.Join(root, "hwmon1", "temp1_label"), "Composite\n")

	writeFile(t, filepath.Join(root, "hwmon2", "name"), "coretemp\n")
	writeFile(t, filepath.Join(root, "hwmon2", "temp1_input"), "35000")
	writeFile(t, filepath.Join(root, "hwmon2", "temp1_label"), "Package id 0\n")

	got, err := ReadTempsFrom(root)
	if err != nil {
		t.Fatalf("ReadTempsFrom: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d temps, want 3: %+v", len(got), got)
	}

	want := map[string]float64{
		"nvme 1/Composite":      34.0,
		"nvme 2/Composite":      38.0,
		"coretemp/Package id 0": 35.0,
	}
	for _, tp := range got {
		key := tp.Chip + "/" + tp.Label
		w, ok := want[key]
		if !ok {
			t.Fatalf("unexpected temp %+v (все ключи: %+v)", tp, got)
		}
		if tp.C != w {
			t.Fatalf("%s: got %.2f°C, want %.2f°C", key, tp.C, w)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatalf("missing temps: %+v", want)
	}
}

func TestReadTempsFrom_MissingRoot(t *testing.T) {
	got, err := ReadTempsFrom(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("expected no error for missing hwmon root, got: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty result, got %+v", got)
	}
}

func TestReadGovernorFrom(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cpu0", "cpufreq", "scaling_governor"), "performance\n")

	got, err := ReadGovernorFrom(root)
	if err != nil {
		t.Fatalf("ReadGovernorFrom: %v", err)
	}
	if got != "performance" {
		t.Fatalf("got %q, want %q", got, "performance")
	}
}

func TestReadGovernorFrom_Missing(t *testing.T) {
	got, err := ReadGovernorFrom(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}
