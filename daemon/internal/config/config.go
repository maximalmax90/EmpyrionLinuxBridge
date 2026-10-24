package config

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

type fileConfig struct {
	ListenAddr  string   `json:"listen_addr"`
	TLSCertFile string   `json:"tls_cert_file"`
	TLSKeyFile  string   `json:"tls_key_file"`
	Tokens      []string `json:"tokens"`
}

type Config struct {
	ListenAddr    string
	DedicatedYAML string
	SystemdUnit   string
	ModAPI        string
	DataRoot      string
	BackupsDir    string
	SteamcmdBin   string
	SteamAppID    string

	PlayerHistoryDir string

	BackpackDBFile      string
	BackpackPollSeconds int
	SettingsFile        string

	ChatDBFile      string
	ChatPollSeconds int

	PresenceDBFile      string
	PresencePollSeconds int

	PlayerQueueDBFile      string
	PlayerQueuePollSeconds int

	StructureQueueDBFile      string
	StructureQueuePollSeconds int

	StructureBackupDBFile string

	PlayerStatsDBFile      string
	PlayerStatsPollSeconds int

	IconExtractPython string

	SchedulerDBFile string

	HistoryDBFile      string
	HistoryPollSeconds int

	NotifyDBFile string

	MutedFile string

	MetricsDBFile string

	AuditDBFile string

	ScenarioCacheDir string

	ScenarioCacheListenAddr string

	TLSCertFile string
	TLSKeyFile  string
	Tokens      map[string]bool
}

func Load() Config {
	cfg := Config{
		DedicatedYAML:       envOr("ELB_DEDICATED_YAML", "/server/EMP/dedicated.yaml"),
		SystemdUnit:         envOr("ELB_SYSTEMD_UNIT", "empyrion.service"),
		ModAPI:              envOr("ELB_MOD_API", "http://127.0.0.1:31914"),
		DataRoot:            envOr("ELB_DATA_ROOT", "/server/EMP"),
		BackupsDir:          envOr("ELB_BACKUPS_DIR", "/server/backups"),
		SteamcmdBin:         envOr("ELB_STEAMCMD_BIN", "/home/empyrion/steamcmd/steamcmd.sh"),
		SteamAppID:          envOr("ELB_STEAM_APPID", "530870"),
		BackpackPollSeconds: envOrInt("ELB_BACKPACK_POLL_SECONDS", 30),
		ChatPollSeconds:     envOrInt("ELB_CHAT_POLL_SECONDS", 2),
		PresencePollSeconds: envOrInt("ELB_PRESENCE_POLL_SECONDS", 10),
		IconExtractPython:   envOr("ELB_ICON_EXTRACT_PYTHON", "/opt/elb/iconextract-venv/bin/python3"),
		HistoryPollSeconds:  envOrInt("ELB_HISTORY_POLL_SECONDS", 30),
	}
	cfg.PlayerQueuePollSeconds = envOrInt("ELB_PLAYER_QUEUE_POLL_SECONDS", 10)
	cfg.StructureQueuePollSeconds = envOrInt("ELB_STRUCTURE_QUEUE_POLL_SECONDS", 10)
	cfg.PlayerStatsPollSeconds = envOrInt("ELB_PLAYER_STATS_POLL_SECONDS", 10)

	elbHostDir := filepath.Join(cfg.DataRoot, "elb")
	elbSaveDir := elbHostDir
	if name, err := cfg.GameName(); err == nil && name != "" {
		elbSaveDir = filepath.Join(cfg.DataRoot, "Saves", "Games", name, "Mods", "ElbMod")
	} else {

		log.Printf("config: GameName unavailable (%v) — save data temporarily in %s", err, elbSaveDir)
	}

	if err := ensureSaveDirOwnership(elbSaveDir); err != nil {
		log.Printf("config: could not prepare %s: %v", elbSaveDir, err)
	}

	if err := ensureModQueueDirOwnership(cfg.DataRoot, elbHostDir); err != nil {
		log.Printf("config: could not prepare mod queue dirs under %s: %v", elbHostDir, err)
	}

	cfg.PlayerHistoryDir = envOr("ELB_PLAYER_HISTORY_DIR", filepath.Join(elbSaveDir, "players"))
	cfg.BackpackDBFile = envOr("ELB_BACKPACK_DB_FILE", filepath.Join(elbSaveDir, "backpack.db"))
	cfg.SettingsFile = envOr("ELB_SETTINGS_FILE", filepath.Join(elbHostDir, "settings.json"))
	cfg.ChatDBFile = envOr("ELB_CHAT_DB_FILE", filepath.Join(elbSaveDir, "chat.db"))
	cfg.PresenceDBFile = envOr("ELB_PRESENCE_DB_FILE", filepath.Join(elbSaveDir, "presence.db"))
	cfg.PlayerQueueDBFile = envOr("ELB_PLAYER_QUEUE_DB_FILE", filepath.Join(elbSaveDir, "playerqueue.db"))
	cfg.StructureQueueDBFile = envOr("ELB_STRUCTURE_QUEUE_DB_FILE", filepath.Join(elbSaveDir, "structurequeue.db"))
	cfg.PlayerStatsDBFile = envOr("ELB_PLAYER_STATS_DB_FILE", filepath.Join(elbSaveDir, "playerstats.db"))
	cfg.SchedulerDBFile = envOr("ELB_SCHEDULER_DB_FILE", filepath.Join(elbHostDir, "scheduler.db"))
	cfg.HistoryDBFile = envOr("ELB_HISTORY_DB_FILE", filepath.Join(elbSaveDir, "history.db"))
	cfg.NotifyDBFile = envOr("ELB_NOTIFY_DB_FILE", filepath.Join(elbSaveDir, "notify.db"))
	cfg.MutedFile = envOr("ELB_MUTED_FILE", filepath.Join(elbHostDir, "mutes.json"))
	cfg.MetricsDBFile = envOr("ELB_METRICS_DB_FILE", filepath.Join(elbHostDir, "metrics.db"))
	cfg.StructureBackupDBFile = envOr("ELB_STRUCTURE_BACKUP_DB_FILE", filepath.Join(elbSaveDir, "structurebackup.db"))
	cfg.AuditDBFile = envOr("ELB_AUDIT_DB_FILE", filepath.Join(elbSaveDir, "audit.db"))
	cfg.ScenarioCacheDir = envOr("ELB_SCENARIO_CACHE_DIR", filepath.Join(elbHostDir, "scenario-cache"))
	if err := os.MkdirAll(cfg.ScenarioCacheDir, 0o755); err != nil {
		log.Printf("config: could not prepare %s: %v", cfg.ScenarioCacheDir, err)
	}
	cfg.ScenarioCacheListenAddr = envOr("ELB_SCENARIO_CACHE_LISTEN_ADDR", ":8899")

	fc, err := loadOrInitFileConfig(envOr("ELB_CONFIG", "/etc/elb/daemon.json"))
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cfg.ListenAddr = fc.ListenAddr
	cfg.TLSCertFile = fc.TLSCertFile
	cfg.TLSKeyFile = fc.TLSKeyFile
	cfg.Tokens = make(map[string]bool, len(fc.Tokens))
	for _, t := range fc.Tokens {
		cfg.Tokens[t] = true
	}
	return cfg
}

func ensureSaveDirOwnership(path string) error {
	var missing []string
	p := path
	for {
		if _, err := os.Stat(p); err == nil {
			break
		}
		missing = append(missing, p)
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	if len(missing) == 0 {
		return nil
	}
	st, err := os.Stat(p)
	if err != nil {

		return nil
	}
	sysStat, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	uid, gid := int(sysStat.Uid), int(sysStat.Gid)
	for _, d := range missing {
		if err := os.Chown(d, uid, gid); err != nil {
			return err
		}
	}
	return nil
}

var modQueueDirNames = []string{"restart-queue", "script-queue", "structure-commands-pending"}

func ensureModQueueDirOwnership(dataRoot, elbHostDir string) error {
	st, err := os.Stat(dataRoot)
	if err != nil {
		return err
	}
	sysStat, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	uid, gid := int(sysStat.Uid), int(sysStat.Gid)

	var errs []error
	for _, name := range modQueueDirNames {
		dir := filepath.Join(elbHostDir, name)
		info, statErr := os.Stat(dir)
		if statErr != nil {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				errs = append(errs, err)
				continue
			}
			if err := os.Chown(dir, uid, gid); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		dirSys, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (int(dirSys.Uid) == uid && int(dirSys.Gid) == gid) {
			continue
		}
		if err := os.Chown(dir, uid, gid); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	msg := "ensureModQueueDirOwnership:"
	for _, e := range errs {
		msg += " " + e.Error() + ";"
	}
	return fmt.Errorf("%s", msg)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envOrInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

const legacyListenAddrEnv = "ELB_HTTP_ADDR"

func loadOrInitFileConfig(path string) (fileConfig, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		var fc fileConfig
		if err := json.Unmarshal(data, &fc); err != nil {
			return fileConfig{}, fmt.Errorf("parse %s: %w", path, err)
		}
		if len(fc.Tokens) == 0 {
			return fileConfig{}, fmt.Errorf("%s: tokens list is empty — daemon will not start without a token", path)
		}
		if fc.ListenAddr == "" {
			fc.ListenAddr = envOr(legacyListenAddrEnv, ":8090")
			if out, mErr := json.MarshalIndent(fc, "", "  "); mErr == nil {
				if wErr := os.WriteFile(path, out, 0o600); wErr != nil {
					log.Printf("config: could not write listen_addr into %s: %v", path, wErr)
				}
			}
		}
		return fc, nil
	}
	if !os.IsNotExist(err) {
		return fileConfig{}, fmt.Errorf("read %s: %w", path, err)
	}

	token, err := randomToken()
	if err != nil {
		return fileConfig{}, fmt.Errorf("generate token: %w", err)
	}
	fc := fileConfig{
		ListenAddr:  envOr(legacyListenAddrEnv, ":8090"),
		TLSCertFile: "/etc/elb/tls.crt",
		TLSKeyFile:  "/etc/elb/tls.key",
		Tokens:      []string{token},
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fileConfig{}, fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	out, _ := json.MarshalIndent(fc, "", "  ")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fileConfig{}, fmt.Errorf("write %s: %w", path, err)
	}
	log.Printf("config: created new %s with a generated token: %s", path, token)
	log.Printf("config: save the token — it will not be printed again (see %s)", path)
	return fc, nil
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

var (
	telPortRe  = regexp.MustCompile(`Tel_Port:\s*(\S+)`)
	telPwdRe   = regexp.MustCompile(`Tel_Pwd:\s*(\S+)`)
	gameNameRe = regexp.MustCompile(`GameName:\s*(\S+)`)
)

func (c Config) GameName() (string, error) {
	f, err := os.Open(c.DedicatedYAML)
	if err != nil {
		return "", fmt.Errorf("open dedicated.yaml: %w", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if m := gameNameRe.FindStringSubmatch(sc.Text()); m != nil {
			return m[1], nil
		}
	}
	return "", fmt.Errorf("GameName not found in %s", c.DedicatedYAML)
}

func (c Config) BlocksmapPath() (string, error) {
	name, err := c.GameName()
	if err != nil {
		return "", err
	}
	return filepath.Join(c.DataRoot, "Saves", "Games", name, "blocksmap.dat"), nil
}

func (c Config) TelnetCreds() (addr, pwd string, err error) {
	f, err := os.Open(c.DedicatedYAML)
	if err != nil {
		return "", "", fmt.Errorf("open dedicated.yaml: %w", err)
	}
	defer f.Close()

	var port string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if m := telPortRe.FindStringSubmatch(line); m != nil {
			port = m[1]
		}
		if m := telPwdRe.FindStringSubmatch(line); m != nil {
			pwd = m[1]
		}
	}
	if err := sc.Err(); err != nil {
		return "", "", fmt.Errorf("read dedicated.yaml: %w", err)
	}
	if port == "" || pwd == "" {
		return "", "", fmt.Errorf("Tel_Port/Tel_Pwd not found in %s", c.DedicatedYAML)
	}
	return "127.0.0.1:" + port, pwd, nil
}

var ServerFields = map[string]bool{
	"Srv_Name":                  true,
	"Srv_Password":              true,
	"Srv_MaxPlayers":            false,
	"Srv_Description":           true,
	"Srv_Port":                  false,
	"Srv_ReservePlayfields":     false,
	"Srv_Public":                false,
	"Srv_StopPeriod":            false,
	"EACActive":                 false,
	"MaxAllowedSizeClass":       false,
	"AllowedBlueprints":         false,
	"HeartbeatServer":           false,
	"HeartbeatClient":           false,
	"LogFlags":                  false,
	"DisableSteamFamilySharing": false,
	"KickPlayerWithPing":        false,
	"TimeoutBootingPfServer":    false,
	"PlayerLoginParallelCount":  false,
	"PlayerLoginVipNames":       true,
	"EnableDLC":                 false,
}

var GameFields = map[string]bool{
	"CustomScenario": false,
	"Mode":           false,
	"GameName":       false,
	"Seed":           false,

	"SharedDataURL": false,
}

func (c Config) ServerField(field string) (value string, ok bool, err error) {
	return c.readField(ServerFields, field)
}

func (c Config) SetServerField(field, value string) error {
	return c.setField(ServerFields, "ServerConfig:", field, value)
}

func (c Config) GameField(field string) (value string, ok bool, err error) {
	return c.readField(GameFields, field)
}

func (c Config) SetGameField(field, value string) error {
	return c.setField(GameFields, "GameConfig:", field, value)
}

func (c Config) ClearGameField(field string) error {
	if _, known := GameFields[field]; !known {
		return fmt.Errorf("поле %s не в списке разрешённых для чтения/записи", field)
	}
	data, err := os.ReadFile(c.DedicatedYAML)
	if err != nil {
		return fmt.Errorf("read dedicated.yaml: %w", err)
	}
	content := string(data)
	activeRe := serverFieldActiveRe(field)
	if !activeRe.MatchString(content) {
		return nil
	}
	content = activeRe.ReplaceAllString(content, "${1}#"+field+":")
	return os.WriteFile(c.DedicatedYAML, []byte(content), 0o644)
}

func (c Config) readField(fields map[string]bool, field string) (value string, ok bool, err error) {
	if _, known := fields[field]; !known {
		return "", false, fmt.Errorf("поле %s не в списке разрешённых для чтения/записи", field)
	}
	data, err := os.ReadFile(c.DedicatedYAML)
	if err != nil {
		return "", false, fmt.Errorf("read dedicated.yaml: %w", err)
	}
	m := serverFieldActiveRe(field).FindStringSubmatch(string(data))
	if m == nil {
		return "", false, nil
	}
	return unquoteYAMLScalar(stripInlineComment(strings.TrimSpace(m[2]))), true, nil
}

func stripInlineComment(raw string) string {
	if strings.HasPrefix(raw, `"`) {
		return raw
	}
	if i := strings.Index(raw, "#"); i >= 0 {
		return strings.TrimRight(raw[:i], " \t")
	}
	return raw
}

func replaceFirstMatch(re *regexp.Regexp, content, repl string) string {
	loc := re.FindStringSubmatchIndex(content)
	if loc == nil {
		return content
	}
	var buf []byte
	buf = re.ExpandString(buf, repl, content, loc)
	return content[:loc[0]] + string(buf) + content[loc[1]:]
}

func (c Config) setField(fields map[string]bool, section, field, value string) error {
	quote, known := fields[field]
	if !known {
		return fmt.Errorf("поле %s не в списке разрешённых для чтения/записи", field)
	}

	data, err := os.ReadFile(c.DedicatedYAML)
	if err != nil {
		return fmt.Errorf("read dedicated.yaml: %w", err)
	}
	content := string(data)

	activeRe := serverFieldActiveRe(field)
	commentedRe := serverFieldCommentedRe(field)

	if field == "Srv_Password" && value == "" {
		if activeRe.MatchString(content) {
			content = activeRe.ReplaceAllString(content, "${1}#"+field+":")
		}

		return os.WriteFile(c.DedicatedYAML, []byte(content), 0o644)
	}

	rendered := value
	if quote {
		rendered = quoteYAMLScalar(value)
	}
	replacement := escapeReplacementDollar(field + ": " + rendered)

	switch {
	case activeRe.MatchString(content):
		content = replaceFirstMatch(activeRe, content, "${1}"+replacement)
	case commentedRe.MatchString(content):

		content = replaceFirstMatch(commentedRe, content, "${1}"+replacement)
	default:
		headerRe := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(section) + `[ \t]*\r?\n`)
		loc := headerRe.FindStringIndex(content)
		if loc == nil {
			return fmt.Errorf("секция %s не найдена в %s", section, c.DedicatedYAML)
		}
		content = content[:loc[1]] + "    " + field + ": " + rendered + "\n" + content[loc[1]:]
	}

	return os.WriteFile(c.DedicatedYAML, []byte(content), 0o644)
}

var mergeableFieldRe = regexp.MustCompile(`(?m)^[ \t]*([A-Za-z_][A-Za-z0-9_]*):[ \t]*(.*?)[ \t]*\r?$`)

func SnapshotActiveFields(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	out := map[string]string{}
	for _, m := range mergeableFieldRe.FindAllStringSubmatch(string(data), -1) {
		val := stripInlineComment(m[2])
		if val == "" {
			continue
		}
		out[m[1]] = val
	}
	return out, nil
}

func MergeActiveFields(path string, snapshot map[string]string) error {
	if len(snapshot) == 0 {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	content := string(data)
	for field, value := range snapshot {
		replacement := escapeReplacementDollar(field + ": " + value)
		activeRe := regexp.MustCompile(`(?m)^([ \t]*)` + regexp.QuoteMeta(field) + `:[ \t]*.*?[ \t]*\r?$`)
		commentedRe := regexp.MustCompile(`(?m)^([ \t]*)#[ \t]*` + regexp.QuoteMeta(field) + `:.*$`)
		switch {
		case activeRe.MatchString(content):
			content = activeRe.ReplaceAllString(content, "${1}"+replacement)
		case commentedRe.MatchString(content):
			content = commentedRe.ReplaceAllString(content, "${1}"+replacement)
		}
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

var (
	fieldReMu      sync.Mutex
	fieldActiveRe  = map[string]*regexp.Regexp{}
	fieldCommentRe = map[string]*regexp.Regexp{}
)

func serverFieldActiveRe(field string) *regexp.Regexp {
	fieldReMu.Lock()
	defer fieldReMu.Unlock()
	if re, ok := fieldActiveRe[field]; ok {
		return re
	}
	re := regexp.MustCompile(`(?m)^([ \t]*)` + regexp.QuoteMeta(field) + `:[ \t]*(.*?)[ \t]*\r?$`)
	fieldActiveRe[field] = re
	return re
}

func serverFieldCommentedRe(field string) *regexp.Regexp {
	fieldReMu.Lock()
	defer fieldReMu.Unlock()
	if re, ok := fieldCommentRe[field]; ok {
		return re
	}
	re := regexp.MustCompile(`(?m)^([ \t]*)#[ \t]*` + regexp.QuoteMeta(field) + `:.*$`)
	fieldCommentRe[field] = re
	return re
}

func quoteYAMLScalar(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func unquoteYAMLScalar(s string) string {
	if len(s) >= 2 && strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		s = s[1 : len(s)-1]
		s = strings.ReplaceAll(s, `\"`, `"`)
		s = strings.ReplaceAll(s, `\\`, `\`)
	}
	return s
}

func escapeReplacementDollar(s string) string {
	return strings.ReplaceAll(s, "$", "$$")
}
