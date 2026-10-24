package config

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func testConfig(t *testing.T, yaml string) (Config, string) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dedicated.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return Config{DedicatedYAML: path}, path
}

const sampleYAML = "### Dedicated server settings\r\n" +
	"\r\n" +
	"ServerConfig:\r\n" +
	"    Srv_Port: 30000\r\n" +
	"    Srv_Name: ELB Test\r\n" +
	"    Srv_Password: 13666829\r\n" +
	"    # Only use with public servers (not for Local Coop)\r\n" +
	"    # Srv_MaxPlayers: 8\r\n" +
	"\r\n" +
	"    ### Server description shown in Server Browser\r\n" +
	"    # Srv_Description: \"Server info, can contain [b]bold[/b] text\"\r\n" +
	"\r\n" +
	"    Srv_Public: false\r\n" +
	"\r\n" +
	"GameConfig:\r\n" +
	"    GameName: FreshTest                       # Determines name of the save game\r\n"

func TestServerField_ReadActive(t *testing.T) {
	cfg, _ := testConfig(t, sampleYAML)
	v, ok, err := cfg.ServerField("Srv_Name")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || v != "ELB Test" {
		t.Fatalf("got %q ok=%v, want %q ok=true", v, ok, "ELB Test")
	}
}

func TestServerField_ReadCommentedIsNotOk(t *testing.T) {
	cfg, _ := testConfig(t, sampleYAML)
	_, ok, err := cfg.ServerField("Srv_MaxPlayers")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("закомментированное поле не должно читаться как активное")
	}
}

func TestSetServerField_ReplaceActive(t *testing.T) {
	cfg, path := testConfig(t, sampleYAML)
	if err := cfg.SetServerField("Srv_Name", "New Name"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, `Srv_Name: "New Name"`) {
		t.Fatalf("не нашли новое значение в файле:\n%s", content)
	}

	if !strings.Contains(content, "Srv_Port: 30000") || !strings.Contains(content, "GameName: FreshTest") {
		t.Fatalf("соседние строки повреждены:\n%s", content)
	}

	v, ok, err := cfg.ServerField("Srv_Name")
	if err != nil || !ok || v != "New Name" {
		t.Fatalf("round-trip не совпал: v=%q ok=%v err=%v", v, ok, err)
	}
}

func TestSetServerField_UncommentField(t *testing.T) {
	cfg, path := testConfig(t, sampleYAML)
	if err := cfg.SetServerField("Srv_MaxPlayers", "12"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, "Srv_MaxPlayers: 12") {
		t.Fatalf("не нашли раскомментированное значение:\n%s", content)
	}
	if strings.Contains(content, "# Srv_MaxPlayers: 8") {
		t.Fatalf("старая закомментированная строка должна быть заменена, а не продублирована:\n%s", content)
	}

	if strings.Contains(content, `Srv_MaxPlayers: "12"`) {
		t.Fatalf("числовое поле не должно оборачиваться в кавычки:\n%s", content)
	}
}

func TestSetServerField_EmptyPasswordCommentsLine(t *testing.T) {
	cfg, path := testConfig(t, sampleYAML)
	if err := cfg.SetServerField("Srv_Password", ""); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, "#Srv_Password:") {
		t.Fatalf("строка должна быть закомментирована:\n%s", content)
	}
	if strings.Contains(content, `Srv_Password: ""`) || strings.Contains(content, "Srv_Password: 13666829") {
		t.Fatalf("активная строка с паролем (пустым или старым) не должна остаться:\n%s", content)
	}

	v, ok, err := cfg.ServerField("Srv_Password")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("закомментированный пароль не должен читаться как активный: v=%q", v)
	}

	if err := cfg.SetServerField("Srv_Password", ""); err != nil {
		t.Fatal(err)
	}
	data2, _ := os.ReadFile(path)
	if strings.Count(string(data2), "Srv_Password") != 1 {
		t.Fatalf("строка Srv_Password не должна дублироваться при повторном вызове:\n%s", string(data2))
	}
}

func TestSetServerField_NonEmptyPasswordStillActive(t *testing.T) {
	cfg, path := testConfig(t, sampleYAML)
	if err := cfg.SetServerField("Srv_Password", "newpass"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, `Srv_Password: "newpass"`) {
		t.Fatalf("не нашли новый пароль в файле:\n%s", content)
	}
	v, ok, err := cfg.ServerField("Srv_Password")
	if err != nil || !ok || v != "newpass" {
		t.Fatalf("round-trip не совпал: v=%q ok=%v err=%v", v, ok, err)
	}
}

func TestSetServerField_QuotedDescriptionWithColon(t *testing.T) {
	cfg, path := testConfig(t, sampleYAML)
	if err := cfg.SetServerField("Srv_Description", "Welcome: read the rules"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, `Srv_Description: "Welcome: read the rules"`) {
		t.Fatalf("значение с ':' должно быть в кавычках целиком:\n%s", content)
	}
	v, ok, err := cfg.ServerField("Srv_Description")
	if err != nil || !ok || v != "Welcome: read the rules" {
		t.Fatalf("round-trip не совпал: v=%q ok=%v err=%v", v, ok, err)
	}
}

func TestSetServerField_InsertMissingField(t *testing.T) {
	yamlNoDescription := strings.ReplaceAll(sampleYAML, "    # Srv_Description: \"Server info, can contain [b]bold[/b] text\"\r\n", "")
	cfg, path := testConfig(t, yamlNoDescription)
	if err := cfg.SetServerField("Srv_Description", "Hello"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, `Srv_Description: "Hello"`) {
		t.Fatalf("новое поле не вставлено:\n%s", content)
	}
	if strings.Count(content, "ServerConfig:") != 1 {
		t.Fatalf("секция ServerConfig задублировалась:\n%s", content)
	}
}

func TestSetServerField_RejectsUnknownField(t *testing.T) {
	cfg, _ := testConfig(t, sampleYAML)
	if err := cfg.SetServerField("Tel_Pwd", "hacked"); err == nil {
		t.Fatal("поле вне белого списка не должно приниматься")
	}
}

func TestGameField_ReadActiveWithInlineComment(t *testing.T) {
	yaml := sampleYAML + "    CustomScenario: RE2      # Enable custom scenario. Name of custom scenario from folder Content\\Scenarios\r\n"
	cfg, _ := testConfig(t, yaml)
	v, ok, err := cfg.GameField("CustomScenario")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || v != "RE2" {
		t.Fatalf("got %q ok=%v, want %q ok=true (инлайн-комментарий должен отрезаться)", v, ok, "RE2")
	}
}

func TestClearGameField_CommentsActiveLine(t *testing.T) {
	yaml := sampleYAML + "    SharedDataURL: _http://host:8899/x.zip?v=1\r\n"
	cfg, path := testConfig(t, yaml)
	if err := cfg.ClearGameField("SharedDataURL"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, "#SharedDataURL:") {
		t.Fatalf("строка должна быть закомментирована:\n%s", content)
	}
	if strings.Contains(content, "SharedDataURL: _http://host:8899/x.zip?v=1") {
		t.Fatalf("активная строка не должна остаться:\n%s", content)
	}
	v, ok, err := cfg.GameField("SharedDataURL")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("закомментированное поле не должно читаться как активное: v=%q", v)
	}

	if err := cfg.ClearGameField("SharedDataURL"); err != nil {
		t.Fatal(err)
	}
	data2, _ := os.ReadFile(path)
	if strings.Count(string(data2), "SharedDataURL") != 1 {
		t.Fatalf("строка не должна дублироваться при повторном вызове:\n%s", string(data2))
	}
}

func TestClearGameField_NoOpWhenFieldAbsent(t *testing.T) {
	cfg, _ := testConfig(t, sampleYAML)
	if err := cfg.ClearGameField("SharedDataURL"); err != nil {
		t.Fatal(err)
	}
}

func TestSetGameField_TwoCommentedBoilerplateExamples_OnlyActivatesOne(t *testing.T) {
	yaml := sampleYAML +
		"\r\n    # Google Drive example\r\n" +
		"    # SharedDataURL: _https://old.example/a.zip?v=1\r\n" +
		"\r\n    # Example for a \"download URL\" - notice the leading underscore character\r\n" +
		"    # SharedDataURL: _https://old.example/a.zip?v=1\r\n"
	cfg, path := testConfig(t, yaml)
	if err := cfg.SetGameField("SharedDataURL", "_https://new.example/b.zip?v=2"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	content := string(data)
	activeCount := strings.Count(content, "SharedDataURL: _https://new.example/b.zip?v=2")
	if activeCount != 1 {
		t.Fatalf("got %d активных строк, want ровно 1:\n%s", activeCount, content)
	}

	staleCount := strings.Count(content, "# SharedDataURL: _https://old.example/a.zip?v=1")
	if staleCount != 1 {
		t.Fatalf("got %d нетронутых закомментированных строк, want 1:\n%s", staleCount, content)
	}
}

func TestSetServerField_DollarInValueDoesNotBreakReplacement(t *testing.T) {
	cfg, path := testConfig(t, sampleYAML)
	if err := cfg.SetServerField("Srv_Password", "p$1$ss"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	content := string(data)

	if !strings.Contains(content, `Srv_Password: "p$1$ss"`) {
		t.Fatalf("значение с '$' повреждено при замене:\n%s", content)
	}
	v, ok, err := cfg.ServerField("Srv_Password")
	if err != nil || !ok || v != "p$1$ss" {
		t.Fatalf("round-trip не совпал: v=%q ok=%v err=%v", v, ok, err)
	}
}

func TestSnapshotActiveFields_CapturesAllSectionsIgnoresCommentsAndHeaders(t *testing.T) {
	_, path := testConfig(t, sampleYAML)
	got, err := SnapshotActiveFields(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Srv_Port":     "30000",
		"Srv_Name":     "ELB Test",
		"Srv_Password": "13666829",
		"Srv_Public":   "false",
		"GameName":     "FreshTest",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("snapshot[%q] = %q, want %q", k, got[k], v)
		}
	}
	for _, absent := range []string{"Srv_MaxPlayers", "Srv_Description", "ServerConfig", "GameConfig"} {
		if _, ok := got[absent]; ok {
			t.Errorf("snapshot не должен содержать %q (закомментировано или заголовок секции)", absent)
		}
	}
}

const postUpdateYAML = "### Dedicated server settings\r\n" +
	"\r\n" +
	"ServerConfig:\r\n" +
	"    Srv_Port: 30000\r\n" +
	"    Srv_Name: My Server\r\n" +
	"    # Srv_Password: Abc # Only use with public servers (not for Local Coop)\r\n" +
	"    # Srv_MaxPlayers: 8\r\n" +
	"    Srv_Public: true\r\n" +
	"    Srv_NewFeature: enabled                  # добавлено в новой версии игры\r\n" +
	"\r\n" +
	"GameConfig:\r\n" +
	"    GameName: DediGame                       # Determines name of the save game\r\n"

func TestMergeActiveFields_RestoresOldLeavesNewFieldUntouched(t *testing.T) {
	oldSnapshot, err := SnapshotActiveFields(mustWriteTemp(t, sampleYAML))
	if err != nil {
		t.Fatal(err)
	}
	path := mustWriteTemp(t, postUpdateYAML)
	if err := MergeActiveFields(path, oldSnapshot); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	content := string(data)

	cfg := Config{DedicatedYAML: path}
	if v, ok, _ := cfg.readField(map[string]bool{"Srv_Name": true}, "Srv_Name"); !ok || v != "ELB Test" {
		t.Errorf("Srv_Name после merge = %q ok=%v, want %q", v, ok, "ELB Test")
	}
	if v, ok, _ := cfg.readField(map[string]bool{"Srv_Password": true}, "Srv_Password"); !ok || v != "13666829" {
		t.Errorf("Srv_Password после merge = %q ok=%v, want %q (должен раскомментироваться)", v, ok, "13666829")
	}
	if v, ok, _ := cfg.readField(map[string]bool{"Srv_Public": true}, "Srv_Public"); !ok || v != "false" {
		t.Errorf("Srv_Public после merge = %q ok=%v, want %q", v, ok, "false")
	}
	if v, ok, _ := cfg.readField(map[string]bool{"GameName": true}, "GameName"); !ok || v != "FreshTest" {
		t.Errorf("GameName после merge = %q ok=%v, want %q", v, ok, "FreshTest")
	}
	if !strings.Contains(content, "Srv_NewFeature: enabled") {
		t.Fatalf("новое поле (не было в старом снимке) не должно трогаться:\n%s", content)
	}
}

func mustWriteTemp(t *testing.T, yaml string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dedicated.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadOrInitFileConfig_CreatesNewWithDefaultListenAddr(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.json")
	fc, err := loadOrInitFileConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if fc.ListenAddr != ":8090" {
		t.Fatalf("ListenAddr = %q, want %q", fc.ListenAddr, ":8090")
	}
	if len(fc.Tokens) != 1 || fc.Tokens[0] == "" {
		t.Fatalf("ожидался ровно один сгенерированный токен, получили %v", fc.Tokens)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"listen_addr": ":8090"`) {
		t.Fatalf("listen_addr не записан в файл:\n%s", data)
	}
}

func TestLoadOrInitFileConfig_MigratesLegacyFileWithoutListenAddr(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.json")
	legacy := `{"tls_cert_file":"/etc/elb/tls.crt","tls_key_file":"/etc/elb/tls.key","tokens":["abc123"]}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	fc, err := loadOrInitFileConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if fc.ListenAddr != ":8090" {
		t.Fatalf("ListenAddr после миграции = %q, want %q (дефолт)", fc.ListenAddr, ":8090")
	}
	if fc.Tokens[0] != "abc123" {
		t.Fatalf("токен потерян при миграции: %v", fc.Tokens)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"listen_addr": ":8090"`) {
		t.Fatalf("миграция не дописала listen_addr в файл на диске:\n%s", data)
	}
}

func TestLoadOrInitFileConfig_MigrationPrefersLegacyEnvVar(t *testing.T) {
	t.Setenv(legacyListenAddrEnv, "127.0.0.1:9999")
	path := filepath.Join(t.TempDir(), "daemon.json")
	legacy := `{"tls_cert_file":"/etc/elb/tls.crt","tls_key_file":"/etc/elb/tls.key","tokens":["abc123"]}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	fc, err := loadOrInitFileConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if fc.ListenAddr != "127.0.0.1:9999" {
		t.Fatalf("миграция должна подхватить %s=%q, получили ListenAddr=%q",
			legacyListenAddrEnv, "127.0.0.1:9999", fc.ListenAddr)
	}
}

func TestLoadOrInitFileConfig_KeepsExplicitListenAddr(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.json")
	explicit := `{"listen_addr":"0.0.0.0:8443","tls_cert_file":"/etc/elb/tls.crt","tls_key_file":"/etc/elb/tls.key","tokens":["abc123"]}`
	if err := os.WriteFile(path, []byte(explicit), 0o600); err != nil {
		t.Fatal(err)
	}

	fc, err := loadOrInitFileConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if fc.ListenAddr != "0.0.0.0:8443" {
		t.Fatalf("ListenAddr = %q, значение из файла не должно перезаписываться", fc.ListenAddr)
	}
}

func TestEnsureSaveDirOwnership_CreatesAndDoesNotTouchExistingAncestor(t *testing.T) {
	root := t.TempDir()
	games := filepath.Join(root, "Saves", "Games", "LinuxGame")
	if err := os.MkdirAll(games, 0o755); err != nil {
		t.Fatal(err)
	}
	beforeSt, err := os.Stat(games)
	if err != nil {
		t.Fatal(err)
	}
	beforeSys, ok := beforeSt.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("syscall.Stat_t недоступен на этой платформе")
	}

	target := filepath.Join(games, "Mods", "ElbMod")
	if err := ensureSaveDirOwnership(target); err != nil {
		t.Fatalf("ensureSaveDirOwnership: %v", err)
	}

	for _, p := range []string{filepath.Join(games, "Mods"), target} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatalf("каталог не создан: %s: %v", p, err)
		}
		if !st.IsDir() {
			t.Fatalf("%s — не каталог", p)
		}
	}

	afterSt, err := os.Stat(games)
	if err != nil {
		t.Fatal(err)
	}
	afterSys, ok := afterSt.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("syscall.Stat_t недоступен на этой платформе")
	}
	if beforeSys.Uid != afterSys.Uid || beforeSys.Gid != afterSys.Gid {
		t.Fatalf("уже существовавший родитель %s сменил владельца: было uid=%d gid=%d, стало uid=%d gid=%d",
			games, beforeSys.Uid, beforeSys.Gid, afterSys.Uid, afterSys.Gid)
	}

	if err := ensureSaveDirOwnership(target); err != nil {
		t.Fatalf("повторный вызов на существующем дереве: %v", err)
	}
}

func TestEnsureModQueueDirOwnership_CreatesAllThreeWithDataRootOwner(t *testing.T) {
	root := t.TempDir()
	elbHostDir := filepath.Join(root, "elb")
	if err := os.MkdirAll(elbHostDir, 0o755); err != nil {
		t.Fatal(err)
	}

	dataRootSt, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	dataRootSys, ok := dataRootSt.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("syscall.Stat_t недоступен на этой платформе")
	}

	if err := ensureModQueueDirOwnership(root, elbHostDir); err != nil {
		t.Fatalf("ensureModQueueDirOwnership: %v", err)
	}

	for _, name := range modQueueDirNames {
		dir := filepath.Join(elbHostDir, name)
		st, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("каталог не создан: %s: %v", dir, err)
		}
		if !st.IsDir() {
			t.Fatalf("%s — не каталог", dir)
		}
		sys, ok := st.Sys().(*syscall.Stat_t)
		if !ok {
			t.Fatal("syscall.Stat_t недоступен на этой платформе")
		}
		if sys.Uid != dataRootSys.Uid || sys.Gid != dataRootSys.Gid {
			t.Fatalf("%s: владелец uid=%d gid=%d, ожидался владелец DataRoot uid=%d gid=%d",
				dir, sys.Uid, sys.Gid, dataRootSys.Uid, dataRootSys.Gid)
		}
	}

	if err := ensureModQueueDirOwnership(root, elbHostDir); err != nil {
		t.Fatalf("повторный вызов: %v", err)
	}
}

func TestEnsureModQueueDirOwnership_IdempotentOnAlreadyCorrectDir(t *testing.T) {
	root := t.TempDir()
	elbHostDir := filepath.Join(root, "elb")
	existing := filepath.Join(elbHostDir, "restart-queue")
	if err := os.MkdirAll(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	beforeSt, err := os.Stat(existing)
	if err != nil {
		t.Fatal(err)
	}

	if err := ensureModQueueDirOwnership(root, elbHostDir); err != nil {
		t.Fatalf("ensureModQueueDirOwnership: %v", err)
	}

	afterSt, err := os.Stat(existing)
	if err != nil {
		t.Fatal(err)
	}
	if afterSt.Mode() != beforeSt.Mode() {
		t.Fatalf("уже существовавший каталог %s изменил права: было %v, стало %v", existing, beforeSt.Mode(), afterSt.Mode())
	}
}

func TestEnsureModQueueDirOwnership_FixesMismatchedOwnerOnExistingDir(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("нужен root, чтобы реально создать каталог с чужим владельцем")
	}
	root := t.TempDir()
	elbHostDir := filepath.Join(root, "elb")
	existing := filepath.Join(elbHostDir, "restart-queue")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}

	const otherUID, otherGID = 65534, 65534
	if err := os.Chown(existing, otherUID, otherGID); err != nil {
		t.Fatal(err)
	}

	if err := ensureModQueueDirOwnership(root, elbHostDir); err != nil {
		t.Fatalf("ensureModQueueDirOwnership: %v", err)
	}

	dataRootSt, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	dataRootSys := dataRootSt.Sys().(*syscall.Stat_t)

	st, err := os.Stat(existing)
	if err != nil {
		t.Fatal(err)
	}
	sys := st.Sys().(*syscall.Stat_t)
	if sys.Uid != dataRootSys.Uid || sys.Gid != dataRootSys.Gid {
		t.Fatalf("владелец не исправлен: uid=%d gid=%d, ожидался владелец DataRoot uid=%d gid=%d",
			sys.Uid, sys.Gid, dataRootSys.Uid, dataRootSys.Gid)
	}
}
