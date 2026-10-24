package gamefiles

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime/multipart"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/fsowner"
	"elb/daemon/internal/settings"
)

func SafeJoin(base, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("пустое имя")
	}
	cleanBase := filepath.Clean(base)
	full := filepath.Join(cleanBase, filepath.Clean(string(filepath.Separator)+name))
	if full != cleanBase && !strings.HasPrefix(full, cleanBase+string(filepath.Separator)) {
		return "", fmt.Errorf("недопустимый путь: %q", name)
	}
	return full, nil
}

type LogFile struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
}

func logsDir(cfg config.Config) string {
	root := filepath.Join(cfg.DataRoot, "Logs")
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) == 0 {
		return root
	}
	best, bestTime := entries[0].Name(), int64(0)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if t := info.ModTime().Unix(); t > bestTime {
			best, bestTime = e.Name(), t
		}
	}
	return filepath.Join(root, best)
}

func ListLogs(cfg config.Config) ([]LogFile, error) {
	entries, err := os.ReadDir(logsDir(cfg))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []LogFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, LogFile{Name: e.Name(), Size: info.Size(), ModTime: info.ModTime().Format("2006-01-02T15:04:05Z07:00")})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime > out[j].ModTime })
	return out, nil
}

func ReadLog(cfg config.Config, name string, maxBytes int64) (string, error) {
	if maxBytes <= 0 {
		maxBytes = 256 * 1024
	}
	path, err := SafeJoin(logsDir(cfg), name)
	if err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := info.Size()
	offset := int64(0)
	if size > maxBytes {
		offset = size - maxBytes
	}
	if _, err := f.Seek(offset, 0); err != nil {
		return "", err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

type ScenarioConfigFile struct {
	Path        string    `json:"path"`
	DisplayPath string    `json:"displayPath"`
	Ext         string    `json:"ext"`
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"modTime"`
}

func scenarioConfigRoots(cfg config.Config) (scenarioDir string, roots []string) {
	scenario, ok, _ := cfg.GameField("CustomScenario")
	if !ok || scenario == "" {
		return "", nil
	}
	scenarioDir = filepath.Join(cfg.DataRoot, "Content", "Scenarios", scenario)
	roots = []string{
		filepath.Join(scenarioDir, "Extras"),
		filepath.Join(scenarioDir, "Content", "Configuration"),
	}
	return scenarioDir, roots
}

func scenarioConfigExtraFiles() []string {
	return []string{
		filepath.Join("SharedData", "Content", "Extras", "LoadingScreenshots", "LoadingScreenConfig.yaml"),
	}
}

func ListScenarioConfigFiles(cfg config.Config) ([]ScenarioConfigFile, error) {
	scenarioDir, roots := scenarioConfigRoots(cfg)
	if scenarioDir == "" {
		return nil, nil
	}
	var files []ScenarioConfigFile
	add := func(path string, info fs.FileInfo) error {
		rel, err := filepath.Rel(cfg.DataRoot, path)
		if err != nil {
			return err
		}
		disp, err := filepath.Rel(scenarioDir, path)
		if err != nil {
			return err
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
		files = append(files, ScenarioConfigFile{Path: rel, DisplayPath: disp, Ext: ext, Size: info.Size(), ModTime: info.ModTime()})
		return nil
	}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			return add(path, info)
		})
		if err != nil {
			return nil, err
		}
	}
	for _, rel := range scenarioConfigExtraFiles() {
		path := filepath.Join(scenarioDir, rel)
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if err := add(path, info); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func isAllowedConfigFile(cfg config.Config, name string) bool {
	if files, err := ListScenarioConfigFiles(cfg); err == nil {
		for _, f := range files {
			if f.Path == name {
				return true
			}
		}
	}
	return false
}

func ReadConfigFile(cfg config.Config, name string) (string, error) {
	if !isAllowedConfigFile(cfg, name) {
		return "", fmt.Errorf("файл %q не в списке разрешённых для чтения/записи", name)
	}
	path, err := SafeJoin(cfg.DataRoot, name)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func WriteConfigFile(cfg config.Config, name, content string) error {
	if !isAllowedConfigFile(cfg, name) {
		return fmt.Errorf("файл %q не в списке разрешённых для чтения/записи", name)
	}
	path, err := SafeJoin(cfg.DataRoot, name)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}

	if err := fsowner.FixTree(cfg, path); err != nil {
		log.Printf("gamefiles: could not set owner on %s: %v", path, err)
	}
	return nil
}

type ModInfo struct {
	Name    string `json:"name"`
	HasInfo bool   `json:"hasInfo"`
}

func modsDir(cfg config.Config) string { return filepath.Join(cfg.DataRoot, "Content", "Mods") }

func ListMods(cfg config.Config) ([]ModInfo, error) {
	entries, err := os.ReadDir(modsDir(cfg))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []ModInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		infoPath := filepath.Join(modsDir(cfg), e.Name(), e.Name()+"_Info.yaml")
		_, statErr := os.Stat(infoPath)
		out = append(out, ModInfo{Name: e.Name(), HasInfo: statErr == nil})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func InstallModZip(cfg config.Config, file multipart.File, size int64) (string, error) {
	tmp, err := os.CreateTemp("", "elb-mod-*.zip")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := io.Copy(tmp, file); err != nil {
		return "", fmt.Errorf("сохранение временного файла: %w", err)
	}

	zr, err := zip.OpenReader(tmp.Name())
	if err != nil {
		return "", fmt.Errorf("это не валидный zip: %w", err)
	}
	defer zr.Close()

	dest := modsDir(cfg)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}

	var topDirs = map[string]bool{}
	for _, f := range zr.File {
		target, err := SafeJoin(dest, f.Name)
		if err != nil {
			return "", fmt.Errorf("подозрительный путь в архиве: %w", err)
		}
		if first := strings.SplitN(filepath.ToSlash(f.Name), "/", 2)[0]; first != "" {
			topDirs[first] = true
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			rc.Close()
			return "", err
		}
		_, copyErr := io.Copy(out, rc)
		rc.Close()
		out.Close()
		if copyErr != nil {
			return "", copyErr
		}
	}

	if err := fsowner.FixTree(cfg, dest); err != nil {
		log.Printf("gamefiles: could not set owner on %s: %v", dest, err)
	}

	if len(topDirs) == 1 {
		for name := range topDirs {
			return name, nil
		}
	}
	return "", nil
}

func RemoveMod(cfg config.Config, name string) error {
	if name == "" || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("недопустимое имя мода: %q", name)
	}
	path, err := SafeJoin(modsDir(cfg), name)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%q — не каталог мода", name)
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	pruneBackupExclude(cfg, "mod", name)
	return nil
}

func pruneBackupExclude(cfg config.Config, kind, name string) {
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		log.Printf("gamefiles: could not load settings for backup exclude cleanup: %v", err)
		return
	}
	if !s.PruneBackupExcludeName(kind, name) {
		return
	}
	if err := settings.Save(cfg.SettingsFile, s); err != nil {
		log.Printf("gamefiles: could not save settings after backup exclude cleanup: %v", err)
	}
}

func scenariosDir(cfg config.Config) string {
	return filepath.Join(cfg.DataRoot, "Content", "Scenarios")
}

func ListScenarios(cfg config.Config) ([]string, error) {
	entries, err := os.ReadDir(scenariosDir(cfg))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

func SelectScenario(cfg config.Config, name string) error {
	scenarios, err := ListScenarios(cfg)
	if err != nil {
		return err
	}
	found := false
	for _, s := range scenarios {
		if s == name {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("сценарий %q не найден в %s", name, scenariosDir(cfg))
	}
	return cfg.SetGameField("CustomScenario", name)
}

func ListSaves(cfg config.Config) ([]string, error) {
	dir := filepath.Join(cfg.DataRoot, "Saves")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}
