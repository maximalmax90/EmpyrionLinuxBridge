package itemstacks

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/icons"
)

var (
	itemIDNameRe = regexp.MustCompile(`Item Id:\s*\d+,\s*Name:\s*([^,}#\r\n]+)`)
	stackSizeRe  = regexp.MustCompile(`StackSize:\s*(\d+)`)
	categoryRe   = regexp.MustCompile(`Category:\s*([^,}#]+)`)

	ammoTypeRe = regexp.MustCompile(`AmmoType:\s*([^,}#\r\n]+)`)

	itemRefRe = regexp.MustCompile(`,\s*Ref:\s*([^,}#\r\n]+)`)

	infoKeyRe = regexp.MustCompile(`Info:\s*([^,}#\r\n]+)`)
)

func configPath(cfg config.Config) string {
	if scenario, err := icons.CurrentScenario(cfg); err == nil {
		p := filepath.Join(cfg.DataRoot, "Content", "Scenarios", scenario, "Content", "Configuration", "ItemsConfig.ecf")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join(cfg.DataRoot, "Content", "Configuration", "ItemsConfig.ecf")
}

func parse(path string) (stackSizes map[string]int, categories map[string]string, ammoTypes map[string]string, infoKeys map[string]string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	defer f.Close()

	stackSizes = map[string]int{}
	categories = map[string]string{}
	ammoTypes = map[string]string{}
	infoKeys = map[string]string{}
	refs := map[string]string{}
	currentName := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if m := itemIDNameRe.FindStringSubmatch(line); m != nil {
			currentName = strings.TrimSpace(m[1])
			if rm := itemRefRe.FindStringSubmatch(line); rm != nil {
				refs[currentName] = strings.TrimSpace(rm[1])
			}
			continue
		}
		if currentName == "" {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if _, ok := stackSizes[currentName]; !ok {
			if m := stackSizeRe.FindStringSubmatch(line); m != nil {
				if size, err := strconv.Atoi(m[1]); err == nil {
					stackSizes[currentName] = size
				}
			}
		}
		if _, ok := categories[currentName]; !ok {
			if m := categoryRe.FindStringSubmatch(line); m != nil {
				if cat := strings.TrimSpace(m[1]); cat != "" {
					categories[currentName] = cat
				}
			}
		}
		if _, ok := ammoTypes[currentName]; !ok {
			if m := ammoTypeRe.FindStringSubmatch(line); m != nil {
				if ammo := strings.TrimSpace(m[1]); ammo != "" {
					ammoTypes[currentName] = ammo
				}
			}
		}
		if _, ok := infoKeys[currentName]; !ok {
			if m := infoKeyRe.FindStringSubmatch(line); m != nil {
				if key := strings.TrimSpace(m[1]); key != "" {
					infoKeys[currentName] = key
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, nil, nil, nil, err
	}

	for name := range refs {
		if _, ok := ammoTypes[name]; ok {
			continue
		}
		seen := map[string]bool{name: true}
		for parent := refs[name]; parent != "" && !seen[parent]; parent = refs[parent] {
			seen[parent] = true
			if ammo, ok := ammoTypes[parent]; ok {
				ammoTypes[name] = ammo
				break
			}
		}
	}

	return stackSizes, categories, ammoTypes, infoKeys, nil
}

var (
	mu             sync.Mutex
	cachePath      string
	cacheMod       time.Time
	cacheStackSize map[string]int
	cacheCategory  map[string]string
	cacheAmmoType  map[string]string
	cacheInfoKey   map[string]string
)

func ensureCached(cfg config.Config) error {
	path := configPath(cfg)
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	mu.Lock()
	defer mu.Unlock()
	if cacheStackSize != nil && cachePath == path && cacheMod.Equal(info.ModTime()) {
		return nil
	}

	stackSizes, categories, ammoTypes, infoKeys, err := parse(path)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	cachePath, cacheMod, cacheStackSize, cacheCategory, cacheAmmoType, cacheInfoKey = path, info.ModTime(), stackSizes, categories, ammoTypes, infoKeys
	return nil
}

func All(cfg config.Config) (map[string]int, error) {
	if err := ensureCached(cfg); err != nil {
		return nil, err
	}
	mu.Lock()
	defer mu.Unlock()
	return cacheStackSize, nil
}

func Categories(cfg config.Config) (map[string]string, error) {
	if err := ensureCached(cfg); err != nil {
		return nil, err
	}
	mu.Lock()
	defer mu.Unlock()
	return cacheCategory, nil
}

func AmmoTypes(cfg config.Config) (map[string]string, error) {
	if err := ensureCached(cfg); err != nil {
		return nil, err
	}
	mu.Lock()
	defer mu.Unlock()
	return cacheAmmoType, nil
}

func InfoKeys(cfg config.Config) (map[string]string, error) {
	if err := ensureCached(cfg); err != nil {
		return nil, err
	}
	mu.Lock()
	defer mu.Unlock()
	return cacheInfoKey, nil
}
