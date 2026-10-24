package sensors

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	defaultHwmonRoot = "/sys/class/hwmon"
	defaultCPURoot   = "/sys/devices/system/cpu"
)

type Temp struct {
	Chip  string
	Label string
	C     float64
}

func ReadTemps() ([]Temp, error) { return ReadTempsFrom(defaultHwmonRoot) }

func ReadTempsFrom(hwmonRoot string) ([]Temp, error) {
	entries, err := os.ReadDir(hwmonRoot)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	type hwmonDir struct {
		dir  string
		name string
	}
	var dirs []hwmonDir
	nameCount := map[string]int{}
	for _, e := range entries {
		if !e.IsDir() && e.Type()&os.ModeSymlink == 0 {
			continue
		}
		dir := filepath.Join(hwmonRoot, e.Name())
		name := strings.TrimSpace(readFileOrEmpty(filepath.Join(dir, "name")))
		if name == "" {
			name = e.Name()
		}
		dirs = append(dirs, hwmonDir{dir: dir, name: name})
		nameCount[name]++
	}

	seen := map[string]int{}
	var out []Temp
	for _, d := range dirs {
		chip := d.name
		if nameCount[d.name] > 1 {
			seen[d.name]++
			chip = fmt.Sprintf("%s %d", d.name, seen[d.name])
		}
		out = append(out, tempsInChip(d.dir, chip)...)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Chip != out[j].Chip {
			return out[i].Chip < out[j].Chip
		}
		return out[i].Label < out[j].Label
	})
	return out, nil
}

func tempsInChip(dir, chip string) []Temp {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Temp
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "temp") || !strings.HasSuffix(name, "_input") {
			continue
		}
		raw := strings.TrimSpace(readFileOrEmpty(filepath.Join(dir, name)))
		milli, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			continue
		}
		base := strings.TrimSuffix(name, "_input")
		label := strings.TrimSpace(readFileOrEmpty(filepath.Join(dir, base+"_label")))
		if label == "" {
			label = base
		}
		out = append(out, Temp{Chip: chip, Label: label, C: float64(milli) / 1000.0})
	}
	return out
}

func ReadGovernor() (string, error) { return ReadGovernorFrom(defaultCPURoot) }

func ReadGovernorFrom(cpuRoot string) (string, error) {
	raw := readFileOrEmpty(filepath.Join(cpuRoot, "cpu0", "cpufreq", "scaling_governor"))
	return strings.TrimSpace(raw), nil
}

func readFileOrEmpty(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}
