package itemlocalization

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/icons"
	"elb/daemon/internal/itemstacks"
)

func csvPath(cfg config.Config) string {
	if scenario, err := icons.CurrentScenario(cfg); err == nil {
		p := filepath.Join(cfg.DataRoot, "Content", "Scenarios", scenario, "Extras", "Localization.csv")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join(cfg.DataRoot, "Content", "Extras", "Localization.csv")
}

func LangColumn(lang string) string {
	switch lang {
	case "ru":
		return "Russian"
	case "en":
		return "English"
	case "de":
		return "Deutsch"
	case "fr":
		return "Français"
	case "it":
		return "Italiano"
	case "es":
		return "Spanish"
	case "pl":
		return "Polish"
	case "ja":
		return "Japanese"
	case "ko":
		return "Korean"
	case "tr":
		return "Turkish"
	case "el":
		return "Greek"
	case "nl":
		return "Dutch"
	case "vi":
		return "Vietnamese"
	case "zh":
		return "Chinese (simplified)"
	default:
		return "English"
	}
}

type parsedCSV struct {
	header map[string]int
	rows   map[string][]string
}

func loadCSV(path string) (*parsedCSV, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)

	r.LazyQuotes = true
	r.FieldsPerRecord = -1

	headerRow, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("заголовок: %w", err)
	}
	header := map[string]int{}
	for i, col := range headerRow {
		header[strings.TrimSpace(col)] = i
	}
	if _, ok := header["English"]; !ok {
		return nil, fmt.Errorf("колонка English не найдена в заголовке")
	}

	rows := map[string][]string{}
	for {
		row, err := r.Read()
		if err != nil {
			break
		}
		if len(row) == 0 || row[0] == "" {
			continue
		}
		rows[row[0]] = row
	}
	return &parsedCSV{header: header, rows: rows}, nil
}

var (
	mu        sync.Mutex
	cachePath string
	cacheMod  time.Time
	cacheCSV  *parsedCSV
)

func ensureCached(cfg config.Config) (*parsedCSV, error) {
	path := csvPath(cfg)
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}

	mu.Lock()
	defer mu.Unlock()
	if cacheCSV != nil && cachePath == path && cacheMod.Equal(info.ModTime()) {
		return cacheCSV, nil
	}

	data, err := loadCSV(path)
	if err != nil {
		return nil, fmt.Errorf("разбор %s: %w", path, err)
	}
	cachePath, cacheMod, cacheCSV = path, info.ModTime(), data
	return data, nil
}

func resolveLang(data *parsedCSV, lang string) map[string]string {
	col := LangColumn(lang)
	idx, hasCol := data.header[col]
	enIdx := data.header["English"]

	out := map[string]string{}
	for key, row := range data.rows {
		text := ""
		if hasCol && idx < len(row) {
			text = strings.TrimSpace(row[idx])
		}
		if text == "" && enIdx < len(row) {
			text = strings.TrimSpace(row[enIdx])
		}
		if text != "" {
			out[key] = text
		}
	}
	return out
}

func DisplayNames(cfg config.Config, lang string) (map[string]string, error) {
	data, err := ensureCached(cfg)
	if err != nil {
		return nil, err
	}
	return resolveLang(data, lang), nil
}

func Descriptions(cfg config.Config, lang string) (map[string]string, error) {
	infoKeys, err := itemstacks.InfoKeys(cfg)
	if err != nil {
		return nil, err
	}
	texts, err := DisplayNames(cfg, lang)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for name, key := range infoKeys {
		if text, ok := texts[key]; ok && text != "" {
			out[name] = text
		}
	}
	return out, nil
}
