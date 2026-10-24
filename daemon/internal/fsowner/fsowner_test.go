package fsowner

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"elb/daemon/internal/config"
)

func ownerOf(t *testing.T, path string) (uid, gid int) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	sysStat, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("syscall.Stat_t недоступен на этой платформе")
	}
	return int(sysStat.Uid), int(sysStat.Gid)
}

func TestFixTree_NoErrorOnAlreadyCorrectTree(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "Content", "Scenarios", "Test")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DataRoot: root}
	if err := FixTree(cfg, sub); err != nil {
		t.Fatalf("FixTree: %v", err)
	}
	uid, gid := ownerOf(t, filepath.Join(sub, "file.txt"))
	wantUID, wantGID := ownerOf(t, root)
	if uid != wantUID || gid != wantGID {
		t.Fatalf("владелец файла = %d:%d, ожидали как у DataRoot %d:%d", uid, gid, wantUID, wantGID)
	}
}

func TestFixTree_SkipsElbModDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("маркер основан на правах доступа — не работает под root (root обходит проверку прав)")
	}
	root := t.TempDir()
	elbMod := filepath.Join(root, "Saves", "Games", "Test", "Mods", "ElbMod")
	locked := filepath.Join(elbMod, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o755)

	cfg := config.Config{DataRoot: root}
	if err := FixTree(cfg, root); err != nil {
		t.Fatalf("FixTree должна пропускать ElbMod целиком, а вернула ошибку (значит зашла внутрь): %v", err)
	}
}

func TestFixTree_SkipsExtraDirNames(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("маркер основан на правах доступа — не работает под root (root обходит проверку прав)")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "steamapps", "workshop", "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o755)

	cfg := config.Config{DataRoot: root}
	if err := FixTree(cfg, root, "steamapps"); err != nil {
		t.Fatalf("FixTree должна пропускать steamapps целиком, а вернула ошибку (значит зашла внутрь): %v", err)
	}
}
