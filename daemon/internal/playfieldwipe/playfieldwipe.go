package playfieldwipe

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/structuresdb"
)

type Token string

const (
	TokenPOI     Token = "poi"
	TokenDeposit Token = "deposit"
	TokenTerrain Token = "terrain"
	TokenPlayer  Token = "player"
)

var AllTokens = []Token{TokenPOI, TokenDeposit, TokenTerrain, TokenPlayer}

func ValidToken(s string) bool {
	for _, t := range AllTokens {
		if string(t) == s {
			return true
		}
	}
	return false
}

func playfieldsDir(cfg config.Config) (string, error) {
	name, err := cfg.GameName()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg.DataRoot, "Saves", "Games", name, "Playfields"), nil
}

func List(cfg config.Config) ([]string, error) {
	dir, err := playfieldsDir(cfg)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {

			return []string{}, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func Queue(cfg config.Config, playfields []string, tokens []Token) error {
	if len(tokens) == 0 || len(playfields) == 0 {
		return nil
	}
	dir, err := playfieldsDir(cfg)
	if err != nil {
		return err
	}
	for _, pf := range playfields {
		pfDir := filepath.Join(dir, pf)
		if _, err := os.Stat(pfDir); err != nil {
			continue
		}
		if err := queueOne(pfDir, tokens); err != nil {
			return err
		}
	}
	return nil
}

func queueOne(pfDir string, tokens []Token) error {
	path := filepath.Join(pfDir, "wipeinfo.txt")
	existing := map[string]bool{}
	if data, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				existing[line] = true
			}
		}
	}
	for _, t := range tokens {
		existing[string(t)] = true
	}
	merged := make([]string, 0, len(existing))
	for t := range existing {
		merged = append(merged, t)
	}
	sort.Strings(merged)
	return os.WriteFile(path, []byte(strings.Join(merged, "\n")+"\n"), 0o644)
}

func templatesDir(cfg config.Config) (string, error) {
	name, err := cfg.GameName()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg.DataRoot, "Saves", "Games", name, "Templates"), nil
}

func cachePlayfieldsDir(cfg config.Config) (string, error) {
	name, err := cfg.GameName()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg.DataRoot, "Saves", "Cache", name, "Playfields"), nil
}

func removeDir(path string) {
	_ = os.RemoveAll(path)
}

func Reset(cfg config.Config, playfields []string) error {
	saveDir, err := playfieldsDir(cfg)
	if err != nil {
		return err
	}
	cacheDir, err := cachePlayfieldsDir(cfg)
	if err != nil {
		return err
	}
	for _, p := range playfields {
		removeDir(filepath.Join(saveDir, p))
		removeDir(filepath.Join(cacheDir, p))
	}
	return nil
}

func ResetIfEmpty(cfg config.Config, playfields []string) error {
	safe, err := filterNoPlayerStuff(cfg, playfields)
	if err != nil {
		return err
	}
	return Reset(cfg, safe)
}

func Recreate(cfg config.Config, playfields []string) error {
	saveDir, err := playfieldsDir(cfg)
	if err != nil {
		return err
	}
	tmplDir, err := templatesDir(cfg)
	if err != nil {
		return err
	}
	cacheDir, err := cachePlayfieldsDir(cfg)
	if err != nil {
		return err
	}
	for _, p := range playfields {
		removeDir(filepath.Join(saveDir, p))
		removeDir(filepath.Join(tmplDir, p))
		removeDir(filepath.Join(cacheDir, p))
	}
	return nil
}

func RecreateDefect(cfg config.Config, playfields []string, scanAll bool) error {
	tmplDir, err := templatesDir(cfg)
	if err != nil {
		return err
	}
	if scanAll {
		entries, err := os.ReadDir(tmplDir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		playfields = playfields[:0]
		for _, e := range entries {
			if e.IsDir() {
				playfields = append(playfields, e.Name())
			}
		}
	}

	saveDir, err := playfieldsDir(cfg)
	if err != nil {
		return err
	}
	cacheDir, err := cachePlayfieldsDir(cfg)
	if err != nil {
		return err
	}
	for _, p := range playfields {
		if _, err := os.Stat(filepath.Join(tmplDir, p, "playfield.yaml")); err == nil {
			continue
		}
		removeDir(filepath.Join(tmplDir, p))
		removeDir(filepath.Join(saveDir, p))
		removeDir(filepath.Join(cacheDir, p))
	}
	return nil
}

func WipeOldUnused(cfg config.Config, days int) error {
	saveDir, err := playfieldsDir(cfg)
	if err != nil {
		return err
	}
	cacheDir, err := cachePlayfieldsDir(cfg)
	if err != nil {
		return err
	}
	deleteBefore := time.Now().AddDate(0, 0, -days)

	entries, err := os.ReadDir(saveDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var candidates []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if filesOlderThan(filepath.Join(saveDir, e.Name()), deleteBefore) {
			candidates = append(candidates, e.Name())
		}
	}
	safe, err := filterNoPlayerStuff(cfg, candidates)
	if err != nil {
		return err
	}
	for _, p := range safe {
		removeDir(filepath.Join(saveDir, p))
		removeDir(filepath.Join(cacheDir, p))
	}

	existing := map[string]bool{}
	if entries, err := os.ReadDir(saveDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				existing[e.Name()] = true
			}
		}
	}
	if cacheEntries, err := os.ReadDir(cacheDir); err == nil {
		for _, e := range cacheEntries {
			if e.IsDir() && !existing[e.Name()] {
				removeDir(filepath.Join(cacheDir, e.Name()))
			}
		}
	}
	return nil
}

func filesOlderThan(dir string, before time.Time) bool {
	found := false
	newest := time.Time{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		found = true
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	if err != nil {
		return false
	}
	if !found {
		return true
	}
	return newest.Before(before)
}

func filterNoPlayerStuff(cfg config.Config, playfields []string) ([]string, error) {
	if len(playfields) == 0 {
		return nil, nil
	}
	structures, err := structuresdb.All(cfg)
	if err != nil {
		return nil, err
	}
	occupied := map[string]bool{}
	for _, s := range structures {
		if s.FactionGroup == structuresdb.FactionGroupFaction || s.FactionGroup == structuresdb.FactionGroupPlayer {
			occupied[s.Playfield] = true
		}
	}
	var safe []string
	for _, p := range playfields {
		if !occupied[p] {
			safe = append(safe, p)
		}
	}
	return safe, nil
}
