package gameoptions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"elb/daemon/internal/config"
)

func setupTestScenario(t *testing.T, mode string) config.Config {
	t.Helper()
	dir := t.TempDir()
	scenarioDir := filepath.Join(dir, "Content", "Scenarios", "Test")
	if err := os.MkdirAll(scenarioDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("testdata/akua.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scenarioDir, "gameoptions.yaml"), fixture, 0o644); err != nil {
		t.Fatal(err)
	}
	dedicated := "ServerConfig:\n    Srv_Name: Test\nGameConfig:\n    CustomScenario: Test\n    Mode: " + mode + "\n"
	dedicatedPath := filepath.Join(dir, "dedicated.yaml")
	if err := os.WriteFile(dedicatedPath, []byte(dedicated), 0o644); err != nil {
		t.Fatal(err)
	}
	return config.Config{DataRoot: dir, DedicatedYAML: dedicatedPath}
}

func mustWriteTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gameoptions.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func fieldByName(fields []Field, name string) (Field, bool) {
	for _, f := range fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

func TestRead_FindsMPSurvivalBlock(t *testing.T) {
	cfg := setupTestScenario(t, "Survival")
	snap, err := Read(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Found {
		t.Fatal("ожидали найденный блок")
	}
	if strings.Join(snap.ValidFor, ",") != "MP,Survival" {
		t.Fatalf("ValidFor = %v, ожидали [MP Survival]", snap.ValidFor)
	}

	regen, ok := fieldByName(snap.Fields, "RegeneratePOIs")
	if !ok || !regen.Set || regen.Value != "true" {
		t.Fatalf("RegeneratePOIs = %+v, ожидали Set=true Value=\"true\" (нормализовано из \"True\")", regen)
	}
	decay, ok := fieldByName(snap.Fields, "DecayTime")
	if !ok || decay.Set {
		t.Fatalf("DecayTime = %+v, ожидали Set=false (закомментировано в MP-блоке)", decay)
	}
}

func TestRead_NoMatchingBlock(t *testing.T) {

	cfg := setupTestScenario(t, "Creative")
	snap, err := Read(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Found {
		t.Fatalf("не ожидали найденного блока для MP+Creative, получили ValidFor=%v", snap.ValidFor)
	}
	for _, f := range snap.Fields {
		if f.Set {
			t.Fatalf("поле %s не должно быть Set при Found=false", f.Name)
		}
	}
}

func TestSetFields_UpdatesActiveFieldInCorrectBlock(t *testing.T) {
	cfg := setupTestScenario(t, "Survival")
	if err := SetFields(cfg, map[string]string{"RegeneratePOIs": "false"}); err != nil {
		t.Fatal(err)
	}
	snap, err := Read(cfg)
	if err != nil {
		t.Fatal(err)
	}
	regen, _ := fieldByName(snap.Fields, "RegeneratePOIs")
	if regen.Value != "false" {
		t.Fatalf("RegeneratePOIs = %q после SetFields, ожидали \"false\"", regen.Value)
	}

	data, err := os.ReadFile(filepath.Join(cfg.DataRoot, "Content", "Scenarios", "Test", "gameoptions.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# RegeneratePOIs: False") {
		t.Fatal("SetFields не должен был трогать закомментированный RegeneratePOIs в SP-блоке")
	}
}

func TestSetFields_UncommentsFieldInTargetBlock(t *testing.T) {
	cfg := setupTestScenario(t, "Survival")
	if err := SetFields(cfg, map[string]string{"DecayTime": "99"}); err != nil {
		t.Fatal(err)
	}
	snap, err := Read(cfg)
	if err != nil {
		t.Fatal(err)
	}
	decay, _ := fieldByName(snap.Fields, "DecayTime")
	if !decay.Set || decay.Value != "99" {
		t.Fatalf("DecayTime = %+v после SetFields, ожидали Set=true Value=99", decay)
	}

	data, err := os.ReadFile(filepath.Join(cfg.DataRoot, "Content", "Scenarios", "Test", "gameoptions.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(data), "\r\n    DecayTime: 0\r\n") {
		t.Fatal("SetFields не должен был трогать DecayTime в SP-блоке")
	}
}

func TestSetFields_RejectsInvalidEnum(t *testing.T) {
	cfg := setupTestScenario(t, "Survival")
	err := SetFields(cfg, map[string]string{"EnableTrading": "NotAValue"})
	if err == nil {
		t.Fatal("ожидали ошибку валидации enum")
	}
}

const twoBlockFixture = `Options:
  - ValidFor: [ SP, Survival ]
    SharedField: sp-value
    OnlyInSP: 1

  - ValidFor: [ MP, Survival ]
    SharedField: mp-value
    OnlyInMP: 2
`

func TestSnapshotActiveBlockFields_ScopedToActiveBlockOnly(t *testing.T) {
	path := mustWriteTemp(t, twoBlockFixture)
	snap, err := SnapshotActiveBlockFields(path, "Survival")
	if err != nil {
		t.Fatal(err)
	}
	if snap["SharedField"] != "mp-value" {
		t.Fatalf("SharedField = %q, ожидали mp-value (из активного MP-блока, не SP)", snap["SharedField"])
	}
	if snap["OnlyInMP"] != "2" {
		t.Fatalf("OnlyInMP = %q, ожидали 2", snap["OnlyInMP"])
	}
	if _, ok := snap["OnlyInSP"]; ok {
		t.Fatal("OnlyInSP не должен попасть в снимок — принадлежит SP-блоку, не активному MP")
	}
}

func TestSnapshotActiveBlockFields_MissingFileReturnsNilNoError(t *testing.T) {
	snap, err := SnapshotActiveBlockFields(filepath.Join(t.TempDir(), "does-not-exist.yaml"), "Survival")
	if err != nil {
		t.Fatalf("ожидали nil-ошибку для отсутствующего файла (свежая установка сценария), получили %v", err)
	}
	if snap != nil {
		t.Fatalf("ожидали nil snapshot, получили %v", snap)
	}
}

const twoBlockFixtureAfterUpdate = `Options:
  - ValidFor: [ SP, Survival ]
    SharedField: sp-value-v2
    OnlyInSP: 1

  - ValidFor: [ MP, Survival ]
    SharedField: mp-value-v2-default
    OnlyInMP: 99
    NewField: added-in-new-version
`

func TestMergeActiveBlockFields_RestoresOldLeavesNewFieldAndOtherBlockUntouched(t *testing.T) {
	oldSnapshot, err := SnapshotActiveBlockFields(mustWriteTemp(t, twoBlockFixture), "Survival")
	if err != nil {
		t.Fatal(err)
	}
	path := mustWriteTemp(t, twoBlockFixtureAfterUpdate)
	if err := MergeActiveBlockFields(path, "Survival", oldSnapshot); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, "SharedField: mp-value\n") {
		t.Errorf("MP-блок: SharedField не восстановлен из снимка, содержимое:\n%s", content)
	}
	if !strings.Contains(content, "OnlyInMP: 2\n") {
		t.Errorf("MP-блок: OnlyInMP не восстановлен из снимка, содержимое:\n%s", content)
	}
	if !strings.Contains(content, "NewField: added-in-new-version") {
		t.Errorf("NewField (нет в старом снимке) не должен был тронуться, содержимое:\n%s", content)
	}
	if !strings.Contains(content, "SharedField: sp-value-v2") {
		t.Errorf("SP-блок не должен был тронуться — ожидали sp-value-v2 из нового файла, содержимое:\n%s", content)
	}
	if strings.Contains(content, "SharedField: mp-value\n    OnlyInSP") {
		t.Fatal("значение из MP-снимка просочилось в SP-блок — коллизия одноимённых полей между блоками")
	}
}

func TestSetFields_CreatesBlockWhenNoneMatches(t *testing.T) {
	cfg := setupTestScenario(t, "Creative")
	if err := SetFields(cfg, map[string]string{"RegeneratePOIs": "true"}); err != nil {
		t.Fatal(err)
	}
	snap, err := Read(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Found {
		t.Fatal("после SetFields блок должен быть найден")
	}
	if strings.Join(snap.ValidFor, ",") != "MP,Creative" {
		t.Fatalf("ValidFor нового блока = %v, ожидали [MP Creative]", snap.ValidFor)
	}
	regen, _ := fieldByName(snap.Fields, "RegeneratePOIs")
	if regen.Value != "true" {
		t.Fatalf("RegeneratePOIs = %q, ожидали true", regen.Value)
	}
}
