package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/fsowner"
	"elb/daemon/internal/gamefiles"
	"elb/daemon/internal/remotestore"
	"elb/daemon/internal/safego"
	"elb/daemon/internal/serverctl"
	"elb/daemon/internal/settings"
	"elb/daemon/internal/sysinfo"
)

type Info struct {
	Name       string   `json:"name"`
	Path       string   `json:"path"`
	Subvolumes []string `json:"subvolumes"`

	Archiving        bool  `json:"archiving,omitempty"`
	HasArchive       bool  `json:"hasArchive,omitempty"`
	ArchiveSizeBytes int64 `json:"archiveSizeBytes,omitempty"`

	SizeBytes int64 `json:"sizeBytes,omitempty"`
}

type subvolEntry struct {
	RelPath string `json:"rel_path"`
	Dir     string `json:"dir"`
}

const metaFile = "meta.json"

func Create(cfg config.Config) (Info, error) {
	mount := filepath.Dir(cfg.DataRoot)
	dataRel := filepath.Base(cfg.DataRoot)

	all, err := listSubvolumes(mount)
	if err != nil {
		return Info{}, fmt.Errorf("list subvolumes: %w", err)
	}

	var targets []string
	for _, p := range all {
		if p == dataRel || strings.HasPrefix(p, dataRel+"/") || p == "wine" {
			targets = append(targets, p)
		}
	}
	if len(targets) == 0 {
		return Info{}, fmt.Errorf("под %s не найдено ни одного subvolume — точки бэкапа не подготовлены?", mount)
	}

	name := time.Now().Format("20060102_1504")
	snapDir := filepath.Join(cfg.BackupsDir, name)
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		return Info{}, fmt.Errorf("mkdir snapshot dir: %w", err)
	}

	var entries []subvolEntry
	for i, rel := range targets {
		dir := fmt.Sprintf("%d_%s", i, strings.ReplaceAll(rel, "/", "_"))
		src := filepath.Join(mount, rel)
		dst := filepath.Join(snapDir, dir)
		if out, err := exec.Command("btrfs", "subvolume", "snapshot", "-r", src, dst).CombinedOutput(); err != nil {

			return infoFromEntries(cfg, name, snapDir, entries),
				fmt.Errorf("snapshot %s: %w: %s", rel, err, out)
		}
		entries = append(entries, subvolEntry{RelPath: rel, Dir: dir})
	}

	if err := copyFile(cfg.DedicatedYAML, filepath.Join(snapDir, filepath.Base(cfg.DedicatedYAML))); err != nil && !os.IsNotExist(err) {
		log.Printf("snapshot: copy %s: %v", cfg.DedicatedYAML, err)
	}

	if err := writeMeta(snapDir, entries); err != nil {
		return infoFromEntries(cfg, name, snapDir, entries), fmt.Errorf("write meta: %w", err)
	}

	go refreshOneSize(cfg, name)

	return infoFromEntries(cfg, name, snapDir, entries), nil
}

func List(cfg config.Config) ([]Info, error) {
	dirEntries, err := os.ReadDir(cfg.BackupsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read backups dir: %w", err)
	}
	var infos []Info
	for _, e := range dirEntries {
		if !e.IsDir() {
			continue
		}
		snapPath := filepath.Join(cfg.BackupsDir, e.Name())
		entries, err := readMeta(snapPath)
		if err != nil {
			continue
		}
		infos = append(infos, infoFromEntries(cfg, e.Name(), snapPath, entries))
	}
	return infos, nil
}

const sizeCacheTTL = 30 * time.Minute

var (
	sizeCacheMu sync.RWMutex
	sizeCache   = map[string]int64{} // имя снапшота -> последний известный эксклюзивный размер
)

func CachedSizeBytes(name string) int64 {
	sizeCacheMu.RLock()
	defer sizeCacheMu.RUnlock()
	return sizeCache[name]
}

func RunSizePoller(cfg config.Config) {
	ticker := time.NewTicker(sizeCacheTTL)
	defer ticker.Stop()
	for {
		safego.Tick("snapshot.RunSizePoller", func() { refreshAllSizes(cfg) })
		<-ticker.C
	}
}

func refreshAllSizes(cfg config.Config) {
	infos, err := List(cfg)
	if err != nil {
		log.Printf("snapshot: size poll: snapshot list: %v", err)
		return
	}
	fresh := make(map[string]int64, len(infos))
	for _, info := range infos {
		if sz, err := SnapshotSizeBytes(cfg, info.Name); err == nil {
			fresh[info.Name] = sz
		} else {
			log.Printf("snapshot: size poll for %s: %v", info.Name, err)
		}
	}
	sizeCacheMu.Lock()
	sizeCache = fresh
	sizeCacheMu.Unlock()
}

func refreshOneSize(cfg config.Config, name string) {
	sz, err := SnapshotSizeBytes(cfg, name)
	if err != nil {
		return
	}
	sizeCacheMu.Lock()
	sizeCache[name] = sz
	sizeCacheMu.Unlock()
}

func forgetSize(name string) {
	sizeCacheMu.Lock()
	delete(sizeCache, name)
	sizeCacheMu.Unlock()
}

func SnapshotSizeBytes(cfg config.Config, name string) (int64, error) {
	snapDir := filepath.Join(cfg.BackupsDir, name)
	entries, err := readMeta(snapDir)
	if err != nil {
		return 0, err
	}
	if len(entries) == 0 {
		return 0, nil
	}
	args := []string{"filesystem", "du", "-s", "--raw"}
	for _, e := range entries {
		args = append(args, filepath.Join(snapDir, e.Dir))
	}
	out, err := exec.Command("btrfs", args...).Output()
	if err != nil {
		return 0, fmt.Errorf("btrfs filesystem du: %w", err)
	}
	return parseExclusiveDuTotal(out), nil
}

func parseExclusiveDuTotal(out []byte) int64 {
	var total int64
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] == "Total" {
			continue
		}
		if excl, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
			total += excl
		}
	}
	return total
}

type RetentionEntry struct {
	Name string
	T    time.Time
}

func SelectRetentionKeep(entries []RetentionEntry, r settings.Retention) map[string]bool {
	sorted := append([]RetentionEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].T.After(sorted[j].T) })

	keep := make(map[string]bool)
	for i := 0; i < r.Last && i < len(sorted); i++ {
		keep[sorted[i].Name] = true
	}
	keepByPeriod(sorted, keep, r.Hourly, func(t time.Time) string { return t.Format("2006-01-02T15") })
	keepByPeriod(sorted, keep, r.Daily, func(t time.Time) string { return t.Format("2006-01-02") })
	keepByPeriod(sorted, keep, r.Weekly, func(t time.Time) string {
		y, w := t.ISOWeek()
		return fmt.Sprintf("%d-W%02d", y, w)
	})
	keepByPeriod(sorted, keep, r.Monthly, func(t time.Time) string { return t.Format("2006-01") })
	keepByPeriod(sorted, keep, r.Yearly, func(t time.Time) string { return t.Format("2006") })
	return keep
}

func keepByPeriod(sorted []RetentionEntry, keep map[string]bool, n int, bucketKey func(time.Time) string) {
	if n <= 0 {
		return
	}
	seen := make(map[string]bool)
	for _, e := range sorted {
		k := bucketKey(e.T)
		if seen[k] {
			continue
		}
		if len(seen) >= n {
			break
		}
		seen[k] = true
		keep[e.Name] = true
	}
}

func ApplyRetention(cfg config.Config, r settings.Retention) (deleted []string, err error) {
	if r.Off() {
		return nil, nil
	}
	infos, err := List(cfg)
	if err != nil {
		return nil, err
	}
	var entries []RetentionEntry
	for _, info := range infos {
		t, perr := time.ParseInLocation("20060102_1504", info.Name, time.Local)
		if perr != nil {
			continue
		}
		entries = append(entries, RetentionEntry{info.Name, t})
	}

	keep := SelectRetentionKeep(entries, r)
	for _, e := range entries {
		if keep[e.Name] {
			continue
		}

		if isBackupBusy(cfg, e.Name) {
			continue
		}
		if err := Delete(cfg, e.Name); err != nil {
			log.Printf("snapshot: retention: could not delete %s: %v", e.Name, err)
			continue
		}
		deleted = append(deleted, e.Name)
	}
	return deleted, nil
}

func ApplyDiskRetention(dir string, r settings.Retention) (deleted []string, err error) {
	if r.Off() {
		return nil, nil
	}
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var entries []RetentionEntry
	for _, e := range dirEntries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tar.zst") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".tar.zst")
		t, perr := time.ParseInLocation("20060102_1504", base, time.Local)
		if perr != nil {
			continue
		}
		entries = append(entries, RetentionEntry{e.Name(), t})
	}

	keep := SelectRetentionKeep(entries, r)
	for _, e := range entries {
		if keep[e.Name] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name)); err != nil {
			log.Printf("snapshot: disk retention %s: could not delete %s: %v", dir, e.Name, err)
			continue
		}
		deleted = append(deleted, e.Name)
	}
	return deleted, nil
}

func ApplyRemoteRetention(d settings.BackupDestination) (deleted []string, err error) {
	if d.Retention.Off() {
		return nil, nil
	}
	store, err := remotestore.New(d)
	if err != nil {
		return nil, err
	}
	remoteEntries, err := store.List()
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	var entries []RetentionEntry
	for _, e := range remoteEntries {
		if !strings.HasSuffix(e.Name, ".tar.zst") {
			continue
		}
		base := strings.TrimSuffix(e.Name, ".tar.zst")
		t, perr := time.ParseInLocation("20060102_1504", base, time.Local)
		if perr != nil {
			continue
		}
		entries = append(entries, RetentionEntry{base, t})
	}

	keep := SelectRetentionKeep(entries, d.Retention)
	for _, e := range entries {
		if keep[e.Name] {
			continue
		}
		if err := store.Delete(e.Name + ".tar.zst"); err != nil {
			log.Printf("snapshot: remote destination retention: could not delete %s: %v", e.Name, err)
			continue
		}
		_ = store.Delete(e.Name + ".manifest.json")
		deleted = append(deleted, e.Name)
	}
	return deleted, nil
}

func validName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("некорректное имя снапшота: %q", name)
	}
	return nil
}

func Restore(cfg config.Config, name string) error {
	if err := validName(name); err != nil {
		return err
	}
	snapDir := filepath.Join(cfg.BackupsDir, name)
	entries, err := readMeta(snapDir)
	if err != nil {
		return fmt.Errorf("read meta for %s: %w", name, err)
	}

	mount := filepath.Dir(cfg.DataRoot)

	deleteOrder := append([]subvolEntry(nil), entries...)
	sort.Slice(deleteOrder, func(i, j int) bool {
		return depth(deleteOrder[i].RelPath) > depth(deleteOrder[j].RelPath)
	})
	createOrder := append([]subvolEntry(nil), entries...)
	sort.Slice(createOrder, func(i, j int) bool {
		return depth(createOrder[i].RelPath) < depth(createOrder[j].RelPath)
	})

	for _, e := range deleteOrder {
		live := filepath.Join(mount, e.RelPath)
		if _, err := os.Stat(live); err != nil {
			continue
		}

		if out, err := exec.Command("btrfs", "subvolume", "delete", live).CombinedOutput(); err != nil {
			if rmErr := os.RemoveAll(live); rmErr != nil {
				return fmt.Errorf("restore прервано: удаление текущего %s: btrfs: %v (%s); fallback RemoveAll: %w", live, err, out, rmErr)
			}
		}
	}

	var restored []string
	for _, e := range createOrder {
		live := filepath.Join(mount, e.RelPath)
		backup := filepath.Join(snapDir, e.Dir)

		os.Remove(live)
		if out, err := exec.Command("btrfs", "subvolume", "snapshot", backup, live).CombinedOutput(); err != nil {
			return fmt.Errorf("restore прервано после %v: восстановление %s: %w: %s", restored, live, err, out)
		}
		restored = append(restored, e.RelPath)
	}

	backupYAML := filepath.Join(snapDir, filepath.Base(cfg.DedicatedYAML))
	if _, err := os.Stat(backupYAML); err == nil {
		if err := copyFile(backupYAML, cfg.DedicatedYAML); err != nil {
			return fmt.Errorf("restore %s: %w", cfg.DedicatedYAML, err)
		}
	}

	return serverctl.Start(cfg.SystemdUnit)
}

func Delete(cfg config.Config, name string) error {
	if err := validName(name); err != nil {
		return err
	}

	if isBackupBusy(cfg, name) {
		return fmt.Errorf("snapshot %s is busy with an active backup (archiving/copying not finished yet) — try again later", name)
	}
	snapDir := filepath.Join(cfg.BackupsDir, name)
	entries, err := readMeta(snapDir)
	if err != nil {
		return fmt.Errorf("read meta for %s: %w", name, err)
	}

	os.Remove(backupBusyMarkerPath(cfg, name))
	os.Remove(filepath.Join(cfg.BackupsDir, name+".tar.zst.tmp"))
	os.Remove(manifestPath(cfg, name))

	sort.Slice(entries, func(i, j int) bool { return depth(entries[i].RelPath) > depth(entries[j].RelPath) })
	for _, e := range entries {
		dir := filepath.Join(snapDir, e.Dir)
		if out, err := exec.Command("btrfs", "subvolume", "delete", dir).CombinedOutput(); err != nil {
			return fmt.Errorf("removing subvolume %s: %w: %s", dir, err, out)
		}
	}
	forgetSize(name)
	return os.RemoveAll(snapDir)
}

func DeleteAllSnapshots(cfg config.Config) (skipped []string, err error) {
	infos, err := List(cfg)
	if err != nil {
		return nil, err
	}
	for _, info := range infos {
		if isBackupBusy(cfg, info.Name) {
			skipped = append(skipped, info.Name)
			continue
		}
		if err := Delete(cfg, info.Name); err != nil {
			return skipped, fmt.Errorf("удаление %s: %w", info.Name, err)
		}
	}
	return skipped, nil
}

func DeleteFromDisk(destDir, name string) error {
	if err := validName(name); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(destDir, name+".tar.zst")); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(filepath.Join(destDir, name+".manifest.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func ClearDisk(destDir string) error {
	entries, err := os.ReadDir(destDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tar.zst") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".tar.zst")
		if err := DeleteFromDisk(destDir, name); err != nil {
			return fmt.Errorf("удаление %s: %w", name, err)
		}
	}
	return nil
}

func DeleteFromRemote(d settings.BackupDestination, name string) error {
	if err := validName(name); err != nil {
		return err
	}
	store, err := remotestore.New(d)
	if err != nil {
		return err
	}
	if err := store.Delete(name + ".tar.zst"); err != nil {
		return err
	}
	return store.Delete(name + ".manifest.json")
}

func ClearRemote(d settings.BackupDestination) error {
	store, err := remotestore.New(d)
	if err != nil {
		return err
	}
	entries, err := store.List()
	if err != nil {
		return fmt.Errorf("список файлов: %w", err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name, ".tar.zst") {
			continue
		}
		name := strings.TrimSuffix(e.Name, ".tar.zst")
		if err := DeleteFromRemote(d, name); err != nil {
			return fmt.Errorf("удаление %s: %w", name, err)
		}
	}
	return nil
}

const (
	archiveFile  = "archive.tar.zst"
	manifestFile = "manifest.json"
	// backupBusyMarker — раньше назывался ".archiving" и жил только на время
	// самого tar (см. историю StartArchive) — обобщён (2026-09-11,
	// переработка хранилищ) на ВЕСЬ backup-цикл: архивация + копирование во
	// ВСЕ настроенные доп. хранилища, см. MarkBackupBusy. Имя файла на диске
	// изменено сознательно — старый ".archiving_<name>" от предыдущей
	// версии демона всё равно был бы только на время короткого tar-шага и
	// не пережил бы рестарт демона, отдельной миграции не требуется.
	backupBusyMarker = ".backup_busy"

	// backupBusyStaleAfter — маркер busy считается зависшим (демон упал или
	// был перезапущен посреди backup-цикла, best-effort os.Remove в defer
	// не выполнился) и перестаёт блокировать удаление/ретеншн после этого
	// времени — иначе один прерванный процесс мог бы НАВСЕГДА защитить
	// снапшот от очистки. Даже очень большой снапшот (500GB) на throttled
	// 30 MiB/s (см. archiveIOLimitBytesPerSec ниже) архивируется меньше чем
	// за 5 часов — 6 часов оставляют разумный запас.
	backupBusyStaleAfter = 6 * time.Hour

	// archiveIOLimitBytesPerSec — потолок скорости I/O для ФОНОВОГО (lowPriority)
	// бэкапа целиком — и архивации (cgroup v2 io.max через systemd-run, см.
	// runTarArchive), и заливки на удалённое хранилище (rate-limited reader,
	// см. remotestore.Put/CopyArchiveToRemote). 30 MiB/s — компромисс:
	// достаточно медленно, чтобы live-серверу оставалась пропускная
	// способность диска, достаточно быстро, чтобы упаковка/заливка не
	// растягивались на часы.
	//
	// Живая находка 2026-09-08 (архивация): ionice НЕ РАБОТАЕТ на хостах с
	// I/O-планировщиком "none"/"mq-deadline" (только cfq/bfq его понимают)
	// — при реальном прогоне архивации 21GB снапшота EmpyrionDedicated.exe
	// ушёл в T-state (тот же баг, что в wine_ptrace_frozen_server_2026-09-06),
	// пришлось перезапускать сервер вручную. cgroup io.max работает
	// независимо от планировщика ядра.
	//
	// Живая находка 2026-09-21 (заливка): описанный выше throttling покрывал
	// ТОЛЬКО архивацию — реальный повторный T-state фриз на emptest.home
	// совпал по времени НЕ с ней (архивация штатно throttled и короткая), а
	// с последующей заливкой архива на FTP (график ioWait — скачок до
	// 25-27% ровно в это окно), которая раньше шла вообще без лимита.
	// Заливка — чистый Go network I/O, не отдельный процесс — cgroup/
	// systemd-run сюда не применить, отсюда отдельный rate-limited reader.
	archiveIOLimitBytesPerSec int64 = 30 * 1024 * 1024
)

func blockDeviceFor(path string) (string, error) {
	out, err := exec.Command("findmnt", "-no", "SOURCE", "--target", path).Output()
	if err != nil {
		return "", err
	}
	device := strings.TrimSpace(string(out))
	if device == "" {
		return "", fmt.Errorf("findmnt did not return a device for %s", path)
	}
	return device, nil
}

func runTarArchive(cfg config.Config, name, snapDir string, throttled bool) error {
	archivePath := filepath.Join(snapDir, archiveFile)
	tmpPath := filepath.Join(cfg.BackupsDir, name+".tar.zst.tmp")

	entries, err := readMeta(snapDir)
	if err != nil {
		return fmt.Errorf("meta.json: %w", err)
	}
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	excludes := archiveExcludes(entries, filepath.Base(cfg.DataRoot), s.Backup.Exclude)

	if err := writeManifest(snapDir, manifestPath(cfg, name), excludes); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}

	tarArgs := append(append([]string{"--zstd", "-cf", tmpPath, "-C", snapDir}, tarExcludeArgs(excludes)...), ".")

	var cmd *exec.Cmd
	if !throttled {
		cmd = exec.Command("nice", append([]string{"-n", "19", "tar"}, tarArgs...)...)
	} else if device, err := blockDeviceFor(cfg.BackupsDir); err == nil {

		limitStr := strconv.FormatInt(archiveIOLimitBytesPerSec, 10)
		args := append([]string{"--scope", "--collect", "--quiet",
			"-p", "IOReadBandwidthMax=" + device + " " + limitStr,
			"-p", "IOWriteBandwidthMax=" + device + " " + limitStr,
			"--", "nice", "-n", "19", "tar"}, tarArgs...)
		cmd = exec.Command("systemd-run", args...)
	} else {
		log.Printf("snapshot: could not determine the block device for I/O throttling (%v) — archiving WITHOUT a limit", err)
		cmd = exec.Command("nice", append([]string{"-n", "19", "tar"}, tarArgs...)...)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("%w: %s", err, out)
	}
	if err := os.Rename(tmpPath, archivePath); err != nil {
		return fmt.Errorf("rename to final name: %w", err)
	}
	return nil
}

func archiveExcludes(entries []subvolEntry, dataRel string, exclude settings.BackupExclude) []string {
	dirFor := func(rel string) (string, bool) {
		for _, e := range entries {
			if e.RelPath == rel {
				return e.Dir, true
			}
		}
		return "", false
	}
	var out []string
	if exclude.Elb {
		if elbDir, ok := dirFor(dataRel + "/elb"); ok {
			out = append(out, elbDir)
		}
	}
	if savesDir, ok := dirFor(dataRel + "/Saves"); ok {
		for _, name := range exclude.Saves {
			out = append(out, filepath.Join(savesDir, name))
		}
	}
	if scenariosDir, ok := dirFor(dataRel + "/Content/Scenarios"); ok {
		for _, name := range exclude.Scenarios {
			out = append(out, filepath.Join(scenariosDir, name))
		}
	}
	if modsDir, ok := dirFor(dataRel + "/Content/Mods"); ok {
		for _, name := range exclude.Mods {
			out = append(out, filepath.Join(modsDir, name))
		}
	}
	if exclude.Wine {
		if wineDir, ok := dirFor("wine"); ok {
			out = append(out, wineDir)
		}
	}
	return out
}

func tarExcludeArgs(excludes []string) []string {
	args := make([]string, len(excludes))
	for i, e := range excludes {
		args[i] = "--exclude=./" + e
	}
	return args
}

func excludedRaw(rel string, excludes []string) bool {
	for _, e := range excludes {
		if rel == e || strings.HasPrefix(rel, e+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func manifestPath(cfg config.Config, name string) string {
	return filepath.Join(cfg.BackupsDir, name+".manifest.json")
}

type ManifestEntry struct {
	Path  string `json:"path"`
	IsDir bool   `json:"isDir,omitempty"`
	Size  int64  `json:"size,omitempty"`
}

type manifestDoc struct {
	Subvolumes []subvolEntry   `json:"subvolumes"`
	Entries    []ManifestEntry `json:"entries"`
}

func translateSubvolPath(subvols []subvolEntry, path string, toClean bool) string {
	for _, s := range subvols {
		from, to := s.Dir, s.RelPath
		if !toClean {
			from, to = s.RelPath, s.Dir
		}
		if path == from {
			return to
		}
		if strings.HasPrefix(path, from+"/") {
			return to + strings.TrimPrefix(path, from)
		}
	}
	return ""
}

func writeManifest(snapDir, outPath string, excludes []string) error {
	subvols, err := readMeta(snapDir)
	if err != nil {
		return fmt.Errorf("read meta: %w", err)
	}
	var entries []ManifestEntry
	err = filepath.WalkDir(snapDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(snapDir, p)
		if err != nil || rel == "." || rel == metaFile {
			return nil
		}
		if excludedRaw(rel, excludes) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		clean := translateSubvolPath(subvols, filepath.ToSlash(rel), true)
		if clean == "" {
			return nil
		}
		e := ManifestEntry{Path: clean, IsDir: d.IsDir()}
		if !d.IsDir() {
			info, ierr := d.Info()
			if ierr != nil {
				return ierr
			}
			e.Size = info.Size()
		}
		entries = append(entries, e)
		return nil
	})
	if err != nil {
		return err
	}
	data, err := json.Marshal(manifestDoc{Subvolumes: subvols, Entries: entries})
	if err != nil {
		return err
	}
	return os.WriteFile(outPath, data, 0o644)
}

func readManifest(path string) (manifestDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return manifestDoc{}, err
	}
	var doc manifestDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return manifestDoc{}, err
	}
	return doc, nil
}

func backupBusyMarkerPath(cfg config.Config, name string) string {
	return filepath.Join(cfg.BackupsDir, backupBusyMarker+"_"+name)
}

func MarkBackupBusy(cfg config.Config, name string) error {
	return os.WriteFile(backupBusyMarkerPath(cfg, name), nil, 0o644)
}

func ClearBackupBusy(cfg config.Config, name string) {
	os.Remove(backupBusyMarkerPath(cfg, name))
}

func isBackupBusy(cfg config.Config, name string) bool {
	fi, err := os.Stat(backupBusyMarkerPath(cfg, name))
	if err != nil {
		return false
	}
	return time.Since(fi.ModTime()) < backupBusyStaleAfter
}

func ArchivePath(cfg config.Config, name string) (string, error) {
	if err := validName(name); err != nil {
		return "", err
	}
	p := filepath.Join(cfg.BackupsDir, name, archiveFile)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("архив для %s не найден (ещё не создан или ещё в процессе): %w", name, err)
	}
	return p, nil
}

func ArchivePathOrCreate(cfg config.Config, name string) (string, error) {
	if err := validName(name); err != nil {
		return "", err
	}
	snapDir := filepath.Join(cfg.BackupsDir, name)
	p := filepath.Join(snapDir, archiveFile)
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	if _, err := os.Stat(snapDir); err != nil {
		return "", fmt.Errorf("снапшот %s не найден: %w", name, err)
	}
	if isBackupBusy(cfg, name) {
		return "", fmt.Errorf("снапшот %s сейчас архивируется другим процессом, повторите скачивание чуть позже", name)
	}
	if err := MarkBackupBusy(cfg, name); err != nil {
		return "", err
	}
	defer ClearBackupBusy(cfg, name)
	if err := runTarArchive(cfg, name, snapDir, true); err != nil {
		return "", fmt.Errorf("архивация %s: %w", name, err)
	}
	return p, nil
}

func depth(relPath string) int { return strings.Count(relPath, "/") }

func infoFromEntries(cfg config.Config, name, path string, entries []subvolEntry) Info {
	rels := make([]string, 0, len(entries))
	for _, e := range entries {
		rels = append(rels, e.RelPath)
	}
	info := Info{Name: name, Path: path, Subvolumes: rels}
	if isBackupBusy(cfg, name) {
		info.Archiving = true
	}
	if fi, err := os.Stat(filepath.Join(path, archiveFile)); err == nil {
		info.HasArchive = true
		info.ArchiveSizeBytes = fi.Size()
	}
	return info
}

func writeMeta(snapDir string, entries []subvolEntry) error {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(snapDir, metaFile), data, 0o644)
}

func readMeta(snapDir string) ([]subvolEntry, error) {
	data, err := os.ReadFile(filepath.Join(snapDir, metaFile))
	if err != nil {
		return nil, err
	}
	var entries []subvolEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmpPath := dst + ".tmp"
	out, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, dst)
}

func CopyArchiveToDisk(cfg config.Config, name string, destDir string) error {
	src, err := ArchivePath(cfg, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", destDir, err)
	}
	if err := copyFile(src, filepath.Join(destDir, name+".tar.zst")); err != nil {
		return fmt.Errorf("copying to %s: %w", destDir, err)
	}
	if srcManifest := manifestPath(cfg, name); fileExists(srcManifest) {
		if err := copyFile(srcManifest, filepath.Join(destDir, name+".manifest.json")); err != nil {
			log.Printf("snapshot: copying manifest %s to %s: %v", name, destDir, err)
		}
	}
	return nil
}

func CopyArchiveToRemote(cfg config.Config, name string, d settings.BackupDestination, lowPriority bool) error {
	src, err := ArchivePath(cfg, name)
	if err != nil {
		return err
	}
	store, err := remotestore.New(d)
	if err != nil {
		return err
	}
	var throttle int64
	if lowPriority {
		throttle = archiveIOLimitBytesPerSec
	}
	if err := store.Put(src, name+".tar.zst", throttle); err != nil {
		return fmt.Errorf("uploading to remote storage: %w", err)
	}
	if srcManifest := manifestPath(cfg, name); fileExists(srcManifest) {
		if err := store.Put(srcManifest, name+".manifest.json", throttle); err != nil {
			log.Printf("snapshot: uploading manifest %s to the remote destination: %v", name, err)
		}
	}
	return nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func CreateAndRetain(cfg config.Config, primary settings.BackupDestination) (Info, error) {
	info, err := Create(cfg)
	if err != nil {
		return Info{}, err
	}
	if _, err := ApplyRetention(cfg, primary.Retention); err != nil {
		log.Printf("snapshot: retention: %v", err)
	}
	return info, nil
}

var ErrNoBackupDestination = errors.New("no additional backup storage destination configured")

func DestinationConfigured(d settings.BackupDestination) bool {
	switch d.Type {
	case "disk":
		return d.DiskPath != ""
	case "ftp":
		return d.FTPHost != ""
	case "s3":
		return d.S3Bucket != ""
	default:
		return false
	}
}

func RunBackup(cfg config.Config, destinations [2]settings.BackupDestination, name string, lowPriority bool) error {
	if err := validName(name); err != nil {
		return err
	}
	hasDestination := false
	for _, d := range destinations {
		if DestinationConfigured(d) {
			hasDestination = true
			break
		}
	}
	if !hasDestination {
		return ErrNoBackupDestination
	}
	snapDir := filepath.Join(cfg.BackupsDir, name)
	if _, err := os.Stat(snapDir); err != nil {
		return fmt.Errorf("snapshot %s not found: %w", name, err)
	}
	if err := MarkBackupBusy(cfg, name); err != nil {
		return err
	}
	defer ClearBackupBusy(cfg, name)

	if !fileExists(filepath.Join(snapDir, archiveFile)) {
		if err := runTarArchive(cfg, name, snapDir, lowPriority); err != nil {
			return fmt.Errorf("archiving: %w", err)
		}
	}

	var copyErr error
	for _, d := range destinations {
		switch d.Type {
		case "disk":
			if err := CopyArchiveToDisk(cfg, name, d.DiskPath); err != nil {
				copyErr = fmt.Errorf("copying to %s: %w", d.DiskPath, err)
				continue
			}
			if _, err := ApplyDiskRetention(d.DiskPath, d.Retention); err != nil {
				log.Printf("snapshot: disk retention %s: %v", d.DiskPath, err)
			}
		case "ftp", "s3":
			if err := CopyArchiveToRemote(cfg, name, d, lowPriority); err != nil {
				copyErr = fmt.Errorf("copying to remote storage: %w", err)
				continue
			}
			if _, err := ApplyRemoteRetention(d); err != nil {
				log.Printf("snapshot: remote destination retention: %v", err)
			}
		}
	}
	return copyErr
}

type BrowseEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"isDir,omitempty"`
	Size  int64  `json:"size,omitempty"`
}

func resolveSnapshotDir(snapDir string, subvols []subvolEntry, subPath string) (string, error) {
	var best *subvolEntry
	for i := range subvols {
		rel := subvols[i].RelPath
		if subPath == rel || strings.HasPrefix(subPath, rel+"/") {
			if best == nil || len(rel) > len(best.RelPath) {
				best = &subvols[i]
			}
		}
	}
	if best == nil {
		return "", fmt.Errorf("путь %q не найден в снапшоте", subPath)
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(subPath, best.RelPath), "/")
	base := filepath.Join(snapDir, best.Dir)
	if rest == "" {
		return base, nil
	}
	return gamefiles.SafeJoin(base, rest)
}

func BrowseSnapshot(cfg config.Config, name, subPath string) ([]BrowseEntry, error) {
	if err := validName(name); err != nil {
		return nil, err
	}
	snapDir := filepath.Join(cfg.BackupsDir, name)
	subvols, err := readMeta(snapDir)
	if err != nil {
		return nil, fmt.Errorf("read meta for %s: %w", name, err)
	}
	subPath = strings.Trim(subPath, "/")
	if subPath == "" {
		out := make([]BrowseEntry, 0, len(subvols))
		for _, s := range subvols {
			out = append(out, BrowseEntry{Name: s.RelPath, IsDir: true})
		}
		return out, nil
	}
	dir, err := resolveSnapshotDir(snapDir, subvols, subPath)
	if err != nil {
		return nil, err
	}
	return readDirEntries(dir)
}

func readDirEntries(dir string) ([]BrowseEntry, error) {
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]BrowseEntry, 0, len(dirEntries))
	for _, de := range dirEntries {
		be := BrowseEntry{Name: de.Name(), IsDir: de.IsDir()}
		if !de.IsDir() {
			if fi, err := de.Info(); err == nil {
				be.Size = fi.Size()
			}
		}
		out = append(out, be)
	}
	return out, nil
}

func copyDirOverwrite(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyFile(p, target)
	})
}

func RestoreFromSnapshot(cfg config.Config, name string, paths []string) error {
	if len(paths) == 0 {
		return Restore(cfg, name)
	}
	if err := validName(name); err != nil {
		return err
	}
	snapDir := filepath.Join(cfg.BackupsDir, name)
	subvols, err := readMeta(snapDir)
	if err != nil {
		return fmt.Errorf("read meta for %s: %w", name, err)
	}
	mount := filepath.Dir(cfg.DataRoot)
	for _, raw := range paths {
		p := strings.Trim(raw, "/")
		src, err := resolveSnapshotDir(snapDir, subvols, p)
		if err != nil {
			return err
		}
		dst, err := gamefiles.SafeJoin(mount, p)
		if err != nil {
			return err
		}
		fi, err := os.Stat(src)
		if err != nil {
			return fmt.Errorf("путь %q: %w", p, err)
		}
		if fi.IsDir() {
			if err := copyDirOverwrite(src, dst); err != nil {
				return fmt.Errorf("восстановление %q: %w", p, err)
			}
			fixRestoredOwnership(cfg, dst)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("восстановление %q: %w", p, err)
		}
		fixRestoredOwnership(cfg, dst)
	}
	return nil
}

func fixRestoredOwnership(cfg config.Config, path string) {
	if err := fsowner.FixTree(cfg, path); err != nil {
		log.Printf("snapshot: could not set owner on %s after restore: %v", path, err)
	}
}

func BrowseArchive(destDir, name, subPath string) ([]BrowseEntry, error) {
	if err := validName(name); err != nil {
		return nil, err
	}
	doc, err := readManifest(filepath.Join(destDir, name+".manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("манифест %s: %w", name, err)
	}
	return groupManifestEntries(doc, subPath), nil
}

func groupManifestEntries(doc manifestDoc, subPath string) []BrowseEntry {
	subPath = strings.Trim(subPath, "/")
	seen := make(map[string]BrowseEntry)
	for _, e := range doc.Entries {
		rel := e.Path
		if subPath != "" {
			if rel == subPath || !strings.HasPrefix(rel, subPath+"/") {
				continue
			}
			rel = strings.TrimPrefix(rel, subPath+"/")
		}
		if rel == "" {
			continue
		}
		seg, isLeaf := rel, true
		if idx := strings.Index(rel, "/"); idx >= 0 {
			seg, isLeaf = rel[:idx], false
		}
		be := BrowseEntry{Name: seg, IsDir: !isLeaf || e.IsDir}
		if isLeaf && !e.IsDir {
			be.Size = e.Size
		}
		seen[seg] = be
	}
	out := make([]BrowseEntry, 0, len(seen))
	for _, v := range seen {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func BrowseRemoteArchive(d settings.BackupDestination, name, subPath string) ([]BrowseEntry, error) {
	if err := validName(name); err != nil {
		return nil, err
	}
	store, err := remotestore.New(d)
	if err != nil {
		return nil, err
	}
	rc, err := store.Open(name + ".manifest.json")
	if err != nil {
		return nil, fmt.Errorf("манифест %s: %w", name, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("манифест %s: %w", name, err)
	}
	var doc manifestDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("манифест %s: %w", name, err)
	}
	return groupManifestEntries(doc, subPath), nil
}

func RestoreFromArchive(cfg config.Config, destDir, name string, paths []string) error {
	if err := validName(name); err != nil {
		return err
	}
	archivePath := filepath.Join(destDir, name+".tar.zst")
	if _, err := os.Stat(archivePath); err != nil {
		return fmt.Errorf("архив %s не найден в %s: %w", name, destDir, err)
	}
	doc, err := readManifest(filepath.Join(destDir, name+".manifest.json"))
	if err != nil {
		return fmt.Errorf("манифест %s: %w", name, err)
	}
	if len(paths) == 0 {
		for _, s := range doc.Subvolumes {
			paths = append(paths, s.RelPath)
		}
	}

	tmpDir, err := os.MkdirTemp(cfg.BackupsDir, "restore_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	mount := filepath.Dir(cfg.DataRoot)
	for _, raw := range paths {
		clean := strings.Trim(raw, "/")
		rawPath := translateSubvolPath(doc.Subvolumes, clean, false)
		if rawPath == "" {
			return fmt.Errorf("путь %q не найден в манифесте %s", clean, name)
		}

		if out, err := exec.Command("tar", "--zstd", "-xf", archivePath, "-C", tmpDir, "./"+rawPath).CombinedOutput(); err != nil {
			return fmt.Errorf("извлечение %q: %w: %s", clean, err, out)
		}
		src := filepath.Join(tmpDir, rawPath)
		dst, err := gamefiles.SafeJoin(mount, clean)
		if err != nil {
			return err
		}
		fi, err := os.Stat(src)
		if err != nil {
			return fmt.Errorf("после извлечения %q не найдено: %w", clean, err)
		}
		if fi.IsDir() {
			if err := copyDirOverwrite(src, dst); err != nil {
				return fmt.Errorf("восстановление %q: %w", clean, err)
			}
			fixRestoredOwnership(cfg, dst)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("восстановление %q: %w", clean, err)
		}
		fixRestoredOwnership(cfg, dst)
	}
	return nil
}

func RestoreFromRemote(cfg config.Config, d settings.BackupDestination, name string, paths []string) error {
	if err := validName(name); err != nil {
		return err
	}
	store, err := remotestore.New(d)
	if err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp(cfg.BackupsDir, "restore_dl_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	if err := store.Get(name+".tar.zst", filepath.Join(tmpDir, name+".tar.zst")); err != nil {
		return fmt.Errorf("скачивание архива %s: %w", name, err)
	}
	if err := store.Get(name+".manifest.json", filepath.Join(tmpDir, name+".manifest.json")); err != nil {
		return fmt.Errorf("скачивание манифеста %s: %w", name, err)
	}
	return RestoreFromArchive(cfg, tmpDir, name, paths)
}

type Stats struct {
	Unsupported bool `json:"unsupported,omitempty"`

	UsedBytes  uint64 `json:"usedBytes,omitempty"`
	TotalBytes uint64 `json:"totalBytes,omitempty"`

	BackupsBytes int64 `json:"backupsBytes,omitempty"`
	BackupsCount int   `json:"backupsCount,omitempty"`

	Entries []BackupEntry `json:"entries,omitempty"`
}

type BackupEntry struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"sizeBytes"`
}

func DestinationStats(cfg config.Config, d settings.BackupDestination) (Stats, error) {
	switch d.Type {
	case "snapshot":
		used, total, err := sysinfo.DiskUsage(cfg.BackupsDir)
		if err != nil {
			return Stats{}, err
		}
		infos, err := List(cfg)
		if err != nil {
			return Stats{}, err
		}

		var bytes int64
		for _, info := range infos {
			bytes += CachedSizeBytes(info.Name)
		}
		return Stats{UsedBytes: used, TotalBytes: total, BackupsBytes: bytes, BackupsCount: len(infos)}, nil
	case "disk":
		if d.DiskPath == "" {
			return Stats{Unsupported: true}, nil
		}
		used, total, err := sysinfo.DiskUsage(d.DiskPath)
		if err != nil {

			return Stats{Unsupported: true}, nil
		}
		var bytes int64
		var backupEntries []BackupEntry
		if dirEntries, err := os.ReadDir(d.DiskPath); err == nil {

			manifests := map[string]bool{}
			for _, e := range dirEntries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".manifest.json") {
					manifests[strings.TrimSuffix(e.Name(), ".manifest.json")] = true
				}
			}
			for _, e := range dirEntries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".tar.zst") {
					continue
				}
				base := strings.TrimSuffix(e.Name(), ".tar.zst")
				if !manifests[base] {
					continue
				}
				if fi, err := e.Info(); err == nil {
					bytes += fi.Size()
					backupEntries = append(backupEntries, BackupEntry{
						Name:      base,
						SizeBytes: fi.Size(),
					})
				}
			}
		}
		sort.Slice(backupEntries, func(i, j int) bool { return backupEntries[i].Name < backupEntries[j].Name })
		return Stats{UsedBytes: used, TotalBytes: total, BackupsBytes: bytes, BackupsCount: len(backupEntries), Entries: backupEntries}, nil
	case "ftp", "s3":

		store, err := remotestore.New(d)
		if err != nil {
			return Stats{Unsupported: true}, nil
		}
		remoteEntries, err := store.List()
		if err != nil {

			return Stats{Unsupported: true}, nil
		}

		remoteManifests := map[string]bool{}
		for _, e := range remoteEntries {
			if strings.HasSuffix(e.Name, ".manifest.json") {
				remoteManifests[strings.TrimSuffix(e.Name, ".manifest.json")] = true
			}
		}
		var bytes int64
		var backupEntries []BackupEntry
		for _, e := range remoteEntries {
			if !strings.HasSuffix(e.Name, ".tar.zst") {
				continue
			}
			base := strings.TrimSuffix(e.Name, ".tar.zst")
			if !remoteManifests[base] {
				continue
			}
			bytes += e.SizeBytes
			backupEntries = append(backupEntries, BackupEntry{
				Name:      base,
				SizeBytes: e.SizeBytes,
			})
		}
		sort.Slice(backupEntries, func(i, j int) bool { return backupEntries[i].Name < backupEntries[j].Name })
		return Stats{BackupsBytes: bytes, BackupsCount: len(backupEntries), Entries: backupEntries}, nil
	default:
		return Stats{Unsupported: true}, nil
	}
}

func listSubvolumes(mount string) ([]string, error) {
	out, err := exec.Command("btrfs", "subvolume", "list", mount).Output()
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		idx := strings.Index(line, " path ")
		if idx == -1 {
			continue
		}
		paths = append(paths, strings.TrimSpace(line[idx+len(" path "):]))
	}
	return paths, nil
}
