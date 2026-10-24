package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const Default = "ru"

//go:embed locales/*.json
var embedded embed.FS

var catalog = map[string]map[string]string{}

func init() {
	entries, err := embedded.ReadDir("locales")
	if err != nil {
		panic(fmt.Sprintf("i18n: embedded locales: %v", err))
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := embedded.ReadFile("locales/" + e.Name())
		if err != nil {
			panic(fmt.Sprintf("i18n: read %s: %v", e.Name(), err))
		}
		var m map[string]string
		if err := json.Unmarshal(data, &m); err != nil {
			panic(fmt.Sprintf("i18n: parse %s: %v", e.Name(), err))
		}
		lang := strings.TrimSuffix(e.Name(), ".json")
		catalog[lang] = m
	}
}

func LoadExternalDir(dir string) error {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("i18n: read %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return fmt.Errorf("i18n: read %s: %w", e.Name(), err)
		}
		var patch map[string]string
		if err := json.Unmarshal(data, &patch); err != nil {
			return fmt.Errorf("i18n: parse %s: %w", e.Name(), err)
		}
		lang := strings.TrimSuffix(e.Name(), ".json")
		m, ok := catalog[lang]
		if !ok {
			m = map[string]string{}
			catalog[lang] = m
		}
		for k, v := range patch {
			m[k] = v
		}
	}
	return nil
}

func T(lang, key string) string {
	if m, ok := catalog[lang]; ok {
		if v, ok := m[key]; ok {
			return v
		}
	}
	if lang != Default {
		if v, ok := catalog[Default][key]; ok {
			return v
		}
	}
	return key
}

func Languages() []string {
	out := make([]string, 0, len(catalog))
	for lang := range catalog {
		out = append(out, lang)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i] == Default {
			return true
		}
		if out[j] == Default {
			return false
		}
		return out[i] < out[j]
	})
	return out
}

func LanguageName(lang string) string {
	if m, ok := catalog[lang]; ok {
		if v, ok := m["_meta.name"]; ok {
			return v
		}
	}
	return lang
}
