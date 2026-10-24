package blockclass

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"elb/daemon/internal/config"
	"elb/daemon/internal/icons"
)

var deviceGroupAllowlist = map[string]bool{
	"Thruster": true, "Shield": true, "Constructor": true,
	"Drill": true, "DrillTurret": true, "RCS": true,
}

func IsDeviceGroup(group string) bool {
	if group == "" {
		return false
	}
	if strings.HasPrefix(group, "cpg") {
		return true
	}
	return deviceGroupAllowlist[group]
}

type rawEntry struct {
	group string
	ref   string
}

var (
	blockHeaderRe = regexp.MustCompile(`^\s*\{\s*Block\s+(?:Id:\s*(\d+),\s*Name:|Name:)\s*([^\s,]+)(?:,\s*Ref:\s*([^\s,]+))?.*$`)
	groupRe       = regexp.MustCompile(`^\s*Group:\s*(\S+?),?\s*$`)
)

func blocksConfigPath(cfg config.Config, scenario string) string {
	return filepath.Join(cfg.DataRoot, "Content", "Scenarios", scenario, "Content", "Configuration", "BlocksConfig.ecf")
}

func Groups(cfg config.Config) (map[string]string, error) {
	scenario, err := icons.CurrentScenario(cfg)
	if err != nil || scenario == "" {
		return nil, nil
	}
	path := blocksConfigPath(cfg, scenario)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	raw := map[string]rawEntry{}
	var current string
	sc := bufio.NewScanner(f)

	for sc.Scan() {
		line := sc.Text()
		if m := blockHeaderRe.FindStringSubmatch(line); m != nil {
			current = m[2]
			e := raw[current]
			if m[3] != "" {
				e.ref = m[3]
			}
			raw[current] = e
			continue
		}
		if current == "" {
			continue
		}
		if m := groupRe.FindStringSubmatch(line); m != nil {
			e := raw[current]
			if e.group == "" {
				e.group = m[1]
				raw[current] = e
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}

	resolved := make(map[string]string, len(raw))
	for name := range raw {
		resolved[name] = resolveGroup(raw, name, 0)
	}
	return resolved, nil
}

func NamesByID(cfg config.Config) (map[int]string, error) {
	scenario, err := icons.CurrentScenario(cfg)
	if err != nil || scenario == "" {
		return nil, nil
	}
	path := blocksConfigPath(cfg, scenario)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	ids := map[int]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		m := blockHeaderRe.FindStringSubmatch(sc.Text())
		if m == nil || m[1] == "" {
			continue
		}
		id, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		ids[id] = m[2]
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return ids, nil
}

func resolveGroup(raw map[string]rawEntry, name string, depth int) string {
	if depth > 10 {
		return ""
	}
	e, ok := raw[name]
	if !ok {
		return ""
	}
	if e.group != "" {
		return e.group
	}
	if e.ref != "" {
		return resolveGroup(raw, e.ref, depth+1)
	}
	return ""
}
