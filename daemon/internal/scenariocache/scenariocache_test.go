package scenariocache

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestFingerprint_StableForSameContent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Content", "a.txt"), "a")
	writeFile(t, filepath.Join(dir, "Content", "b.txt"), "b")

	f1, err := Fingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	f2, err := Fingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f1 != f2 {
		t.Fatalf("fingerprint changed with no modification: %s vs %s", f1, f2)
	}
}

func TestFingerprint_ChangesWhenFileContentChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Content", "a.txt")
	writeFile(t, path, "a")
	f1, err := Fingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}

	future := time.Now().Add(time.Hour)
	writeFile(t, path, "ab")
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	f2, err := Fingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f1 == f2 {
		t.Fatalf("fingerprint did not change after size+mtime change")
	}
}

func TestFingerprint_ErrorsOnMissingDir(t *testing.T) {
	if _, err := Fingerprint(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected error for missing directory")
	}
}

func TestBuildZip_WrapsContentUnderSharedDataPrefix(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Content", "Extras", "x.yaml"), "x: 1")
	writeFile(t, filepath.Join(dir, "Content", "Configuration", "y.ecf"), "y")

	destZip := filepath.Join(t.TempDir(), "out.zip")
	if err := BuildZip(dir, destZip); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.OpenReader(destZip)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	want := []string{
		"SharedData/Content/Extras/x.yaml",
		"SharedData/Content/Configuration/y.ecf",
	}
	for _, w := range want {
		if !names[w] {
			t.Errorf("expected zip entry %q, got entries: %v", w, names)
		}
	}
}

func TestBuildZip_NoPartialFileOnFailure(t *testing.T) {
	destZip := filepath.Join(t.TempDir(), "out.zip")
	if err := BuildZip(filepath.Join(t.TempDir(), "does-not-exist"), destZip); err == nil {
		t.Fatal("expected error for missing source dir")
	}
	if _, err := os.Stat(destZip); !os.IsNotExist(err) {
		t.Fatalf("destZip should not exist after failed build, stat err=%v", err)
	}
	if _, err := os.Stat(destZip + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("tmp file should be cleaned up after failed build, stat err=%v", err)
	}
}
