package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoad_CommandsEnabledAndCustomCommands(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	c := Default()
	c.Chatbot.CommandsEnabled = map[string]bool{"get": false, "destroy": true}
	c.Chatbot.CustomCommands = []CustomCommand{
		{Name: "rules", Text: "Правила сервера: ...", Enabled: true},
		{Name: "discord", Text: "discord.gg/xxx", Enabled: false},
	}

	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if loaded.Chatbot.CommandsEnabled["get"] != false || loaded.Chatbot.CommandsEnabled["destroy"] != true {
		t.Fatalf("CommandsEnabled не совпал после round-trip: %#v", loaded.Chatbot.CommandsEnabled)
	}
	if len(loaded.Chatbot.CustomCommands) != 2 {
		t.Fatalf("ожидали 2 CustomCommands, получили %d", len(loaded.Chatbot.CustomCommands))
	}
	if loaded.Chatbot.CustomCommands[0].Name != "rules" || loaded.Chatbot.CustomCommands[0].Text != "Правила сервера: ..." {
		t.Fatalf("первая custom-команда повреждена: %#v", loaded.Chatbot.CustomCommands[0])
	}
	if loaded.Chatbot.CustomCommands[1].Enabled != false {
		t.Fatalf("вторая custom-команда должна остаться Enabled=false: %#v", loaded.Chatbot.CustomCommands[1])
	}
}

func TestLoad_MissingNewFieldsDefaultsToZeroValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	old := `{"chatbot":{"wipeCooldownHours":24,"vehicleTeleportCooldownMinutes":10,"locale":"ru","hardWipe":false}}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Chatbot.CommandsEnabled != nil {
		t.Fatalf("ожидали nil CommandsEnabled для старого файла, получили %#v", loaded.Chatbot.CommandsEnabled)
	}
	if loaded.Chatbot.CustomCommands != nil {
		t.Fatalf("ожидали nil CustomCommands для старого файла, получили %#v", loaded.Chatbot.CustomCommands)
	}
}

func TestLoad_SchedulerPausedDefaultsToFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	old := `{"chatbot":{"wipeCooldownHours":24}}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Scheduler.Paused {
		t.Fatal("Scheduler.Paused должен быть false для файла без ключа scheduler (иначе апгрейд молча ставит планировщик на паузу)")
	}
}

func TestSaveLoad_SchedulerPaused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	c := Default()
	c.Scheduler.Paused = true
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Scheduler.Paused {
		t.Fatal("Scheduler.Paused=true не пережил round-trip Save/Load")
	}
}
