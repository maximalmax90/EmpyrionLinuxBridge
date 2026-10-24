package steamjob

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/fsowner"
	"elb/daemon/internal/gameoptions"
	"elb/daemon/internal/safego"
	"elb/daemon/internal/settings"
)

const defaultWorkshopAppID = "383120"

type WorkshopItemInfo struct {
	PublishedFileID string   `json:"publishedFileId"`
	Title           string   `json:"title"`
	FileSize        int64    `json:"fileSize"`
	TimeUpdated     int64    `json:"timeUpdated"`
	Tags            []string `json:"tags"`
}

func LookupWorkshopItem(publishedFileID string) (*WorkshopItemInfo, error) {
	form := url.Values{}
	form.Set("itemcount", "1")
	form.Set("publishedfileids[0]", publishedFileID)
	resp, err := http.PostForm("https://api.steampowered.com/ISteamRemoteStorage/GetPublishedFileDetails/v1/", form)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Steam Web API: %s", resp.Status)
	}

	var parsed struct {
		Response struct {
			PublishedFileDetails []struct {
				Result      int    `json:"result"`
				Title       string `json:"title"`
				FileSize    string `json:"file_size"`
				TimeUpdated int64  `json:"time_updated"`
				Tags        []struct {
					Tag string `json:"tag"`
				} `json:"tags"`
			} `json:"publishedfiledetails"`
		} `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("разбор ответа Steam Web API: %w", err)
	}
	if len(parsed.Response.PublishedFileDetails) == 0 {
		return nil, fmt.Errorf("пустой ответ Steam Web API")
	}
	d := parsed.Response.PublishedFileDetails[0]
	if d.Result != 1 {
		return nil, fmt.Errorf("предмет мастерской не найден (result=%d) — проверьте ID", d.Result)
	}
	size, _ := strconv.ParseInt(d.FileSize, 10, 64)
	info := &WorkshopItemInfo{
		PublishedFileID: publishedFileID,
		Title:           d.Title,
		FileSize:        size,
		TimeUpdated:     d.TimeUpdated,
	}
	for _, t := range d.Tags {
		info.Tags = append(info.Tags, t.Tag)
	}
	return info, nil
}

func InstallWorkshopItem(cfg config.Config, publishedFileID string) (bool, error) {
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		return false, err
	}
	if s.Steam.Username == "" {
		return false, fmt.Errorf("сначала выполните вход в Steam")
	}
	appID := s.Steam.WorkshopAppID
	if appID == "" {
		appID = defaultWorkshopAppID
	}
	if _, ok := TryBegin("install_workshop"); !ok {
		return false, nil
	}
	safego.Go("steamjob.InstallWorkshopItem", func() {

		err := runSteamcmdAsDataOwner(cfg, []string{
			"+force_install_dir", cfg.DataRoot,
			"+login", s.Steam.Username,
			"+workshop_download_item", appID, publishedFileID, "validate",
			"+quit",
		})
		if err != nil {
			msg := lastOutputLine()
			if msg == "" {
				msg = err.Error()
			}
			setState("failed", msg)
			return
		}
		kind, name, title, timeUpdated, placeErr := placeWorkshopItem(cfg, appID, publishedFileID)
		if placeErr != nil {
			setState("failed", placeErr.Error())
			return
		}
		if saveErr := recordInstalledItem(cfg, publishedFileID, title, kind, name, timeUpdated); saveErr != nil {
			appendOutput(fmt.Sprintf("ВНИМАНИЕ: установлено, но не удалось сохранить в настройки: %v", saveErr))
		}
		setProgress(100)

		setState("success", kind+":"+name)
	})
	return true, nil
}

func placeWorkshopItem(cfg config.Config, appID, publishedFileID string) (kind, name, title string, timeUpdated int64, err error) {
	srcDir := filepath.Join(cfg.DataRoot, "steamapps", "workshop", "content", appID, publishedFileID)
	if _, statErr := os.Stat(srcDir); statErr != nil {
		return "", "", "", 0, fmt.Errorf("скачанный контент не найден в %s: %w", srcDir, statErr)
	}

	title = publishedFileID
	info, lookupErr := LookupWorkshopItem(publishedFileID)
	if lookupErr == nil && info != nil {
		if info.Title != "" {
			title = info.Title
		}
		timeUpdated = info.TimeUpdated
	}

	isScenario := looksLikeScenario(srcDir)
	if lookupErr == nil && info != nil {
		hasScenarioTag := false
		for _, t := range info.Tags {
			if strings.EqualFold(t, "Scenario") {
				hasScenarioTag = true
				break
			}
		}
		if hasScenarioTag != isScenario {
			appendOutput(fmt.Sprintf("предупреждение: тег мастерской (Scenario=%v) не совпадает со структурным признаком (%v) — используется структурный", hasScenarioTag, isScenario))
		}
	}

	name = sanitizeDirName(title)
	if name == "" {
		name = publishedFileID
	}

	var destDir string
	if isScenario {
		kind = "scenario"
		destDir = filepath.Join(cfg.DataRoot, "Content", "Scenarios", name)
	} else {
		kind = "mod"
		destDir = filepath.Join(cfg.DataRoot, "Content", "Mods", name)
	}

	var oldGameOptions map[string]string
	var gameOptionsMode string
	if isScenario {
		gameOptionsMode = gameoptions.ActiveMode(cfg)
		optionsPath := filepath.Join(destDir, "gameoptions.yaml")
		if snap, snapErr := gameoptions.SnapshotActiveBlockFields(optionsPath, gameOptionsMode); snapErr != nil {
			appendOutput(fmt.Sprintf("предупреждение: не удалось снять текущие значения gameoptions.yaml перед обновлением: %v", snapErr))
		} else {
			oldGameOptions = snap
		}

		log.Printf("steamjob: placeWorkshopItem: gameoptions.yaml snapshot BEFORE copy (%s, mode=%s): %d fields", destDir, gameOptionsMode, len(oldGameOptions))
	}

	if err := copyDirRecursive(srcDir, destDir); err != nil {
		return "", "", "", 0, fmt.Errorf("копирование в %s: %w", destDir, err)
	}

	if len(oldGameOptions) > 0 {
		optionsPath := filepath.Join(destDir, "gameoptions.yaml")
		if err := gameoptions.MergeActiveBlockFields(optionsPath, gameOptionsMode, oldGameOptions); err != nil {
			appendOutput(fmt.Sprintf("предупреждение: не удалось перенести прежние значения gameoptions.yaml после обновления: %v", err))
			log.Printf("steamjob: placeWorkshopItem: MergeActiveBlockFields FAILED: %v", err)
		} else {
			log.Printf("steamjob: placeWorkshopItem: carried %d fields back over the freshly downloaded gameoptions.yaml", len(oldGameOptions))
		}
	}

	if chownErr := fsowner.FixTree(cfg, destDir); chownErr != nil {
		appendOutput(fmt.Sprintf("предупреждение: не удалось выставить владельца файлов в %s: %v", destDir, chownErr))
	}
	return kind, name, title, timeUpdated, nil
}

func looksLikeScenario(dir string) bool {
	for _, marker := range []string{"description.txt", "preview.jpg", "Playfields"} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

func sanitizeDirName(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == ' ', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func copyDirRecursive(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}

func ListInstalledItems(cfg config.Config) ([]settings.InstalledWorkshopItem, error) {
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		return nil, err
	}
	return s.Steam.InstalledItems, nil
}

func ItemTargetDirBase(cfg config.Config, kind string) (string, error) {
	switch kind {
	case "scenario":
		return filepath.Join(cfg.DataRoot, "Content", "Scenarios"), nil
	case "mod":
		return filepath.Join(cfg.DataRoot, "Content", "Mods"), nil
	default:
		return "", fmt.Errorf("unknown installed item type: %q", kind)
	}
}

func ItemTargetPath(cfg config.Config, kind, targetDir string) string {
	base, err := ItemTargetDirBase(cfg, kind)
	if err != nil {
		return targetDir
	}
	return filepath.Join(base, targetDir)
}

const diskUsageCacheTTL = 60 * time.Second

var (
	diskUsageCacheMu sync.Mutex
	diskUsageCache   = map[string]diskUsageCacheEntry{} // key: абсолютный target-путь
)

type diskUsageCacheEntry struct {
	size int64
	at   time.Time
}

func ItemDiskUsage(cfg config.Config, kind, targetDir string) (int64, error) {
	base, err := ItemTargetDirBase(cfg, kind)
	if err != nil {
		return 0, err
	}
	target := filepath.Join(base, targetDir)

	diskUsageCacheMu.Lock()
	if entry, ok := diskUsageCache[target]; ok && time.Since(entry.at) < diskUsageCacheTTL {
		diskUsageCacheMu.Unlock()
		return entry.size, nil
	}
	diskUsageCacheMu.Unlock()

	var total int64
	err = filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("size calculation %s: %w", target, err)
	}

	diskUsageCacheMu.Lock()
	diskUsageCache[target] = diskUsageCacheEntry{size: total, at: time.Now()}
	diskUsageCacheMu.Unlock()

	return total, nil
}

type WorkshopUpdateStatus struct {
	PublishedFileID   string `json:"publishedFileId"`
	LocalTimeUpdated  int64  `json:"localTimeUpdated"`
	RemoteTimeUpdated int64  `json:"remoteTimeUpdated"`
	UpdateAvailable   bool   `json:"updateAvailable"`
}

func CheckInstalledItemUpdate(cfg config.Config, publishedFileID string) (*WorkshopUpdateStatus, error) {
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		return nil, err
	}
	var local int64
	found := false
	for _, it := range s.Steam.InstalledItems {
		if it.PublishedFileID == publishedFileID {
			local = it.TimeUpdated
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("предмет %s не найден среди установленных", publishedFileID)
	}
	info, err := LookupWorkshopItem(publishedFileID)
	if err != nil {
		return nil, err
	}
	return &WorkshopUpdateStatus{
		PublishedFileID:   publishedFileID,
		LocalTimeUpdated:  local,
		RemoteTimeUpdated: info.TimeUpdated,
		UpdateAvailable:   info.TimeUpdated > local,
	}, nil
}

func RemoveInstalledItem(cfg config.Config, publishedFileID string) error {
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		return err
	}
	idx := -1
	for i, it := range s.Steam.InstalledItems {
		if it.PublishedFileID == publishedFileID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("предмет %s не найден среди установленных", publishedFileID)
	}
	item := s.Steam.InstalledItems[idx]
	baseDir, err := ItemTargetDirBase(cfg, item.Kind)
	if err != nil {
		return err
	}
	target := filepath.Join(baseDir, item.TargetDir)
	if !strings.HasPrefix(target, baseDir+string(filepath.Separator)) {
		return fmt.Errorf("недопустимый путь установленного предмета: %q", item.TargetDir)
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("удаление %s: %w", target, err)
	}
	s.Steam.InstalledItems = append(s.Steam.InstalledItems[:idx], s.Steam.InstalledItems[idx+1:]...)

	s.PruneBackupExcludeName(item.Kind, item.TargetDir)
	return settings.Save(cfg.SettingsFile, s)
}

func ResolveInstalledItemArchive(cfg config.Config, publishedFileID string) (baseDir, targetDir, filename string, err error) {
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		return "", "", "", err
	}
	for _, it := range s.Steam.InstalledItems {
		if it.PublishedFileID != publishedFileID {
			continue
		}
		base, err := ItemTargetDirBase(cfg, it.Kind)
		if err != nil {
			return "", "", "", err
		}
		target := filepath.Join(base, it.TargetDir)
		if !strings.HasPrefix(target, base+string(filepath.Separator)) {
			return "", "", "", fmt.Errorf("недопустимый путь установленного предмета: %q", it.TargetDir)
		}
		if _, err := os.Stat(target); err != nil {
			return "", "", "", fmt.Errorf("каталог предмета не найден: %w", err)
		}
		return base, it.TargetDir, it.TargetDir + ".tar.zst", nil
	}
	return "", "", "", fmt.Errorf("предмет %s не найден среди установленных", publishedFileID)
}

func StreamItemArchive(baseDir, targetDir string, w io.Writer) error {
	cmd := exec.Command("tar", "--zstd", "-cf", "-", "-C", baseDir, targetDir)
	cmd.Stdout = w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("archiving %s: %w", targetDir, err)
	}
	return nil
}

func recordInstalledItem(cfg config.Config, publishedFileID, title, kind, targetDir string, timeUpdated int64) error {
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		return err
	}
	item := settings.InstalledWorkshopItem{
		PublishedFileID: publishedFileID,
		Title:           title,
		Kind:            kind,
		TargetDir:       targetDir,
		TimeUpdated:     timeUpdated,
	}
	replaced := false
	for i, existing := range s.Steam.InstalledItems {
		if existing.PublishedFileID == publishedFileID {
			s.Steam.InstalledItems[i] = item
			replaced = true
			break
		}
	}
	if !replaced {
		s.Steam.InstalledItems = append(s.Steam.InstalledItems, item)
	}
	return settings.Save(cfg.SettingsFile, s)
}
