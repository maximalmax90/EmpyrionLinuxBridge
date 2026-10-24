package icons

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"elb/daemon/internal/config"
)

var (
	// Item Id/Block Id и Name всегда на ОДНОЙ строке в начале блока (тот же
	// формат, что уже использован в daemon/internal/itemstacks).
	nameRe   = regexp.MustCompile(`(?:Item|Block) Id:\s*\d+,\s*Name:\s*([^,}#\r\n]+)`)
	customRe = regexp.MustCompile(`CustomIcon:\s*([^,}#\r\n]+)`)
)

func itemsConfigPath(cfg config.Config) string {
	if scenario, err := CurrentScenario(cfg); err == nil {
		p := filepath.Join(cfg.DataRoot, "Content", "Scenarios", scenario, "Content", "Configuration", "ItemsConfig.ecf")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join(cfg.DataRoot, "Content", "Configuration", "ItemsConfig.ecf")
}

func blocksConfigPath(cfg config.Config) string {
	if scenario, err := CurrentScenario(cfg); err == nil {
		p := filepath.Join(cfg.DataRoot, "Content", "Scenarios", scenario, "Content", "Configuration", "BlocksConfig.ecf")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join(cfg.DataRoot, "Content", "Configuration", "BlocksConfig.ecf")
}

func parseCustomIcons(path string, out map[string]string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	currentName := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if m := nameRe.FindStringSubmatch(line); m != nil {
			currentName = strings.TrimSpace(m[1])
			continue
		}
		if currentName == "" {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if _, ok := out[currentName]; ok {
			continue
		}
		if m := customRe.FindStringSubmatch(line); m != nil {
			if v := strings.TrimSpace(m[1]); v != "" {
				out[currentName] = v
			}
		}
	}
	return sc.Err()
}

var (
	aliasMu        sync.Mutex
	aliasItemsPath string
	aliasBlockPath string
	aliasItemsMod  time.Time
	aliasBlockMod  time.Time
	aliasCache     map[string]string
)

func customIconAliases(cfg config.Config) (map[string]string, error) {
	itemsPath := itemsConfigPath(cfg)
	blocksPath := blocksConfigPath(cfg)

	itemsInfo, err := os.Stat(itemsPath)
	if err != nil {
		return nil, err
	}
	blocksInfo, err := os.Stat(blocksPath)
	if err != nil {
		return nil, err
	}

	aliasMu.Lock()
	defer aliasMu.Unlock()
	if aliasCache != nil && aliasItemsPath == itemsPath && aliasBlockPath == blocksPath &&
		aliasItemsMod.Equal(itemsInfo.ModTime()) && aliasBlockMod.Equal(blocksInfo.ModTime()) {
		return aliasCache, nil
	}

	out := map[string]string{}
	if err := parseCustomIcons(itemsPath, out); err != nil {
		return nil, err
	}
	if err := parseCustomIcons(blocksPath, out); err != nil {
		return nil, err
	}
	aliasItemsPath, aliasBlockPath = itemsPath, blocksPath
	aliasItemsMod, aliasBlockMod = itemsInfo.ModTime(), blocksInfo.ModTime()
	aliasCache = out
	return out, nil
}
