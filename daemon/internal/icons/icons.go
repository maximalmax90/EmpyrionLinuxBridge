package icons

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"elb/daemon/internal/config"
	"elb/daemon/internal/gamefiles"
	"elb/daemon/internal/iconextract"
)

type Icon struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

func CurrentScenario(cfg config.Config) (string, error) {
	scenario, ok, err := cfg.GameField("CustomScenario")
	if err != nil {
		return "", err
	}
	if !ok || scenario == "" {
		return "", fmt.Errorf("CustomScenario не выбран в dedicated.yaml")
	}
	return scenario, nil
}

func scenarioIconsDir(cfg config.Config, scenario string) string {
	return filepath.Join(cfg.DataRoot, "Content", "Scenarios", scenario, "SharedData", "Content", "Bundles", "ItemIcons")
}

func defaultIconsDir(cfg config.Config) string {
	return filepath.Join(cfg.DataRoot, "elb", "default-icons")
}

func itemIconsBundlePath(cfg config.Config) string {
	return filepath.Join(cfg.DataRoot, "Content", "Bundles", "itemicons")
}

func mergedDir(cfg config.Config, scenario string) string {
	return filepath.Join(cfg.DataRoot, "elb", "icons-merged", scenario)
}

func gameBuild(cfg config.Config) (string, error) {
	data, err := os.ReadFile(filepath.Join(cfg.DataRoot, "BuildNumber.txt"))
	if err != nil {
		return "", fmt.Errorf("BuildNumber.txt: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func scenarioFingerprint(cfg config.Config, scenario string) (string, error) {
	dir := scenarioIconsDir(cfg, scenario)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "empty", nil
		}
		return "", err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".png") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	h := sha256.New()
	for _, name := range names {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s:%d:%d;", name, info.Size(), info.ModTime().Unix())
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func readBuildMarker(dir string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(dir, ".build"))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(data)), true
}

func writeBuildMarker(dir, value string) error {
	return os.WriteFile(filepath.Join(dir, ".build"), []byte(value), 0o644)
}

var buildMu sync.Mutex

func EnsureReady(cfg config.Config) error {
	scenario, err := CurrentScenario(cfg)
	if err != nil {
		return err
	}

	buildMu.Lock()
	defer buildMu.Unlock()

	build, err := gameBuild(cfg)
	if err != nil {
		return err
	}

	defaults := defaultIconsDir(cfg)
	if marker, ok := readBuildMarker(defaults); !ok || marker != build {
		if err := os.RemoveAll(defaults); err != nil {
			return fmt.Errorf("очистка устаревших дефолтных иконок: %w", err)
		}
		bundle := itemIconsBundlePath(cfg)
		if _, statErr := os.Stat(bundle); statErr != nil {
			return fmt.Errorf("бандл иконок базовой игры не найден (%s): %w", bundle, statErr)
		}
		if _, err := iconextract.Extract(cfg.IconExtractPython, bundle, defaults); err != nil {
			return fmt.Errorf("извлечение дефолтных иконок: %w", err)
		}
		if err := writeBuildMarker(defaults, build); err != nil {
			return fmt.Errorf("запись маркера дефолтных иконок: %w", err)
		}
	}

	scenFP, err := scenarioFingerprint(cfg, scenario)
	if err != nil {
		return fmt.Errorf("отпечаток иконок сценария: %w", err)
	}
	wantMarker := build + "\n" + scenFP

	merged := mergedDir(cfg, scenario)
	if marker, ok := readBuildMarker(merged); ok && marker == wantMarker {
		return nil
	}

	if err := os.RemoveAll(merged); err != nil {
		return fmt.Errorf("очистка устаревшей merged-директории: %w", err)
	}
	if err := os.MkdirAll(merged, 0o755); err != nil {
		return err
	}
	if err := copyPNGs(defaults, merged); err != nil {
		return fmt.Errorf("копирование дефолтных иконок: %w", err)
	}
	if err := copyPNGs(scenarioIconsDir(cfg, scenario), merged); err != nil {
		return fmt.Errorf("копирование иконок сценария: %w", err)
	}
	if err := writeBuildMarker(merged, wantMarker); err != nil {
		return fmt.Errorf("запись маркера merged: %w", err)
	}
	return nil
}

func copyPNGs(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".png") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func List(cfg config.Config) ([]Icon, error) {
	scenario, err := CurrentScenario(cfg)
	if err != nil {
		return nil, err
	}
	if err := EnsureReady(cfg); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(mergedDir(cfg, scenario))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sizes := map[string]int64{}
	var out []Icon
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".png") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		sizes[name] = info.Size()
		out = append(out, Icon{Name: name, Size: info.Size()})
	}

	if aliases, err := customIconAliases(cfg); err == nil {
		for name, target := range aliases {
			if _, hasOwn := sizes[name]; hasOwn {
				continue
			}
			if size, ok := sizes[target]; ok {
				out = append(out, Icon{Name: name, Size: size})
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func Read(cfg config.Config, name string) ([]byte, error) {
	scenario, err := CurrentScenario(cfg)
	if err != nil {
		return nil, err
	}
	if err := EnsureReady(cfg); err != nil {
		return nil, err
	}
	path, err := gamefiles.SafeJoin(mergedDir(cfg, scenario), name+".png")
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err == nil {
		return data, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}

	aliases, aerr := customIconAliases(cfg)
	if aerr != nil {
		return nil, err
	}
	target, ok := aliases[name]
	if !ok {
		return nil, err
	}
	targetPath, perr := gamefiles.SafeJoin(mergedDir(cfg, scenario), target+".png")
	if perr != nil {
		return nil, err
	}
	return os.ReadFile(targetPath)
}
