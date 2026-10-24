package scenariocache

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"elb/daemon/internal/config"
)

func SharedDataDir(cfg config.Config) (string, error) {
	scenario, ok, err := cfg.GameField("CustomScenario")
	if err != nil {
		return "", err
	}
	if !ok || scenario == "" {
		return "", fmt.Errorf("сценарий не выбран (CustomScenario)")
	}
	dir := filepath.Join(cfg.DataRoot, "Content", "Scenarios", scenario, "SharedData")
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("SharedData не найдена для сценария %q: %w", scenario, err)
	}
	return dir, nil
}

func Fingerprint(sharedDataDir string) (string, error) {
	type entry struct {
		rel   string
		size  int64
		mtime int64
	}
	var entries []entry
	err := filepath.Walk(sharedDataDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(sharedDataDir, path)
		if err != nil {
			return err
		}
		entries = append(entries, entry{rel: filepath.ToSlash(rel), size: info.Size(), mtime: info.ModTime().UnixNano()})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk %s: %w", sharedDataDir, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })

	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%s\x00%d\x00%d\n", e.rel, e.size, e.mtime)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fingerprintSidecarPath(destZip string) string {
	return destZip + ".fingerprint"
}

func ReadStampedFingerprint(destZip string) (string, error) {
	data, err := os.ReadFile(fingerprintSidecarPath(destZip))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func WriteStampedFingerprint(destZip, fingerprint string) error {
	return os.WriteFile(fingerprintSidecarPath(destZip), []byte(fingerprint), 0o644)
}

func BuildZip(sharedDataDir, destZip string) (err error) {
	tmp := destZip + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("creating %s: %w", tmp, err)
	}
	defer func() {
		cerr := f.Close()
		if err != nil {
			os.Remove(tmp)
			return
		}
		if cerr != nil {
			err = fmt.Errorf("closing %s: %w", tmp, cerr)
			os.Remove(tmp)
			return
		}
		err = os.Rename(tmp, destZip)
	}()

	zw := zip.NewWriter(f)
	walkErr := filepath.Walk(sharedDataDir, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(sharedDataDir, path)
		if rerr != nil {
			return rerr
		}
		arcname := filepath.ToSlash(filepath.Join("SharedData", rel))
		hdr, herr := zip.FileInfoHeader(info)
		if herr != nil {
			return herr
		}
		hdr.Name = arcname
		hdr.Method = zip.Deflate
		w, cerr := zw.CreateHeader(hdr)
		if cerr != nil {
			return cerr
		}
		src, oerr := os.Open(path)
		if oerr != nil {
			return oerr
		}
		defer src.Close()
		_, cerr = io.Copy(w, src)
		return cerr
	})
	if closeErr := zw.Close(); walkErr == nil {
		walkErr = closeErr
	}
	if walkErr != nil {
		return fmt.Errorf("building zip from %s: %w", sharedDataDir, walkErr)
	}
	return nil
}
