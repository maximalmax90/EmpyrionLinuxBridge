package pdachapters

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/icons"
	"elb/daemon/internal/itemlocalization"
)

type Chapter struct {
	Number int    `json:"number"`
	Key    string `json:"key"`
	Title  string `json:"title"`
}

var chapterTitleRe = regexp.MustCompile(`^  - ChapterTitle:\s*(\S+)\s*$`)

func pdaDir(cfg config.Config) (string, bool) {
	scenario, err := icons.CurrentScenario(cfg)
	if err != nil || scenario == "" {
		return "", false
	}
	dir := filepath.Join(cfg.DataRoot, "Content", "Scenarios", scenario, "Extras", "PDA")
	if _, err := os.Stat(filepath.Join(dir, "PDA.yaml")); err != nil {
		return "", false
	}
	return dir, true
}

func chapterKeys(yamlPath string) ([]string, error) {
	f, err := os.Open(yamlPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var keys []string
	sc := bufio.NewScanner(f)

	for sc.Scan() {
		if m := chapterTitleRe.FindStringSubmatch(sc.Text()); m != nil {
			keys = append(keys, m[1])
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}

func csvTitles(path, lang string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.LazyQuotes = true
	r.FieldsPerRecord = -1

	headerRow, err := r.Read()
	if err != nil {
		return nil
	}
	header := map[string]int{}
	for i, col := range headerRow {
		header[strings.TrimSpace(col)] = i
	}
	enIdx, hasEn := header["English"]
	if !hasEn {
		return nil
	}
	col := itemlocalization.LangColumn(lang)
	idx, hasCol := header[col]

	out := map[string]string{}
	for {
		row, err := r.Read()
		if err != nil {
			break
		}
		if len(row) == 0 || row[0] == "" {
			continue
		}
		text := ""
		if hasCol && idx < len(row) {
			text = strings.TrimSpace(row[idx])
		}
		if text == "" && enIdx < len(row) {
			text = strings.TrimSpace(row[enIdx])
		}
		if text != "" {
			out[row[0]] = text
		}
	}
	return out
}

var bbcodeTagRe = regexp.MustCompile(`\[[^\[\]]{1,20}\]`)

func stripBBCode(s string) string {
	s = strings.ReplaceAll(s, `\n`, " ")
	return strings.TrimSpace(bbcodeTagRe.ReplaceAllString(s, ""))
}

type cacheEntry struct {
	dir  string
	mod  time.Time
	list []Chapter
}

var (
	mu    sync.Mutex
	cache = map[string]cacheEntry{} // ключ — lang, т.к. Title зависит от языка
)

func List(cfg config.Config, lang string) ([]Chapter, error) {
	dir, ok := pdaDir(cfg)
	if !ok {
		return nil, nil
	}
	yamlPath := filepath.Join(dir, "PDA.yaml")
	info, err := os.Stat(yamlPath)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", yamlPath, err)
	}

	mu.Lock()
	if e, ok := cache[lang]; ok && e.dir == dir && e.mod.Equal(info.ModTime()) {
		mu.Unlock()
		return e.list, nil
	}
	mu.Unlock()

	keys, err := chapterKeys(yamlPath)
	if err != nil {
		return nil, fmt.Errorf("разбор %s: %w", yamlPath, err)
	}
	pdaTitles := csvTitles(filepath.Join(dir, "PDA.csv"), lang)

	locTitles, _ := itemlocalization.DisplayNames(cfg, lang)

	list := make([]Chapter, 0, len(keys))
	for i, key := range keys {
		title := pdaTitles[key]
		if title == "" {
			title = locTitles[key]
		}
		if title == "" {
			title = key
		}
		list = append(list, Chapter{Number: i + 1, Key: key, Title: stripBBCode(title)})
	}

	mu.Lock()
	cache[lang] = cacheEntry{dir: dir, mod: info.ModTime(), list: list}
	mu.Unlock()
	return list, nil
}
