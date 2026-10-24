package pdachapters

import (
	"os"
	"path/filepath"
	"testing"

	"elb/daemon/internal/config"
)

const testDedicatedYAML = "ServerConfig:\r\n" +
	"    Srv_Port: 30000\r\n" +
	"\r\n" +
	"GameConfig:\r\n" +
	"    CustomScenario: TestScenario\r\n"

func testCfg(t *testing.T, pdaYAML, pdaCSV, extrasLocCSV string) config.Config {
	t.Helper()
	root := t.TempDir()
	pdaDir := filepath.Join(root, "Content", "Scenarios", "TestScenario", "Extras", "PDA")
	if err := os.MkdirAll(pdaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdaDir, "PDA.yaml"), []byte(pdaYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if pdaCSV != "" {
		if err := os.WriteFile(filepath.Join(pdaDir, "PDA.csv"), []byte(pdaCSV), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	extrasDir := filepath.Join(root, "Content", "Scenarios", "TestScenario", "Extras")
	if extrasLocCSV != "" {
		if err := os.WriteFile(filepath.Join(extrasDir, "Localization.csv"), []byte(extrasLocCSV), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	yamlPath := filepath.Join(root, "dedicated.yaml")
	if err := os.WriteFile(yamlPath, []byte(testDedicatedYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return config.Config{DedicatedYAML: yamlPath, DataRoot: root}
}

const samplePdaYAML = `PDAs:
  - ChapterTitle: pda_first
    Category: SoloMission
  - ChapterTitle: pda_second
    Category: FAQ
  - ChapterTitle: pda_third
    Category: SoloMission
`

const samplePdaCSV = "KEY,English,Russian\n" +
	"pda_first,[b]First Chapter[/b],[b]Первая глава[/b]\n" +
	"pda_second,Second Chapter,\n"

func TestList_NumbersInFileOrderAndResolvesTitles(t *testing.T) {
	cfg := testCfg(t, samplePdaYAML, samplePdaCSV, "")
	got, err := List(cfg, "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d chapters, want 3: %+v", len(got), got)
	}
	want := []Chapter{
		{Number: 1, Key: "pda_first", Title: "First Chapter"},
		{Number: 2, Key: "pda_second", Title: "Second Chapter"},
		{Number: 3, Key: "pda_third", Title: "pda_third"},
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("chapter %d: got %+v, want %+v", i, got[i], w)
		}
	}
}

func TestList_ResolvesRussianColumnWithEnglishFallback(t *testing.T) {
	cfg := testCfg(t, samplePdaYAML, samplePdaCSV, "")
	got, err := List(cfg, "ru")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Title != "Первая глава" {
		t.Fatalf("got %q, want русский перевод", got[0].Title)
	}

	if got[1].Title != "Second Chapter" {
		t.Fatalf("got %q, want fallback на English", got[1].Title)
	}
}

func TestList_FallsBackToExtrasLocalizationCSV(t *testing.T) {

	locCSV := "KEY,English\npda_third,Third Chapter From Localization\n"
	cfg := testCfg(t, samplePdaYAML, samplePdaCSV, locCSV)
	got, err := List(cfg, "en")
	if err != nil {
		t.Fatal(err)
	}
	if got[2].Title != "Third Chapter From Localization" {
		t.Fatalf("got %q, want фолбэк из Extras/Localization.csv", got[2].Title)
	}
}

func TestList_NoPdaCSV_FallsBackToKeyOrLocalization(t *testing.T) {
	cfg := testCfg(t, samplePdaYAML, "", "")
	got, err := List(cfg, "en")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got {
		if c.Title != c.Key {
			t.Fatalf("без PDA.csv и Localization.csv ожидали сырой ключ, got %+v", c)
		}
	}
}

func TestList_NoPdaYaml_ReturnsEmptyNotError(t *testing.T) {
	root := t.TempDir()
	yamlPath := filepath.Join(root, "dedicated.yaml")
	if err := os.WriteFile(yamlPath, []byte(testDedicatedYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DedicatedYAML: yamlPath, DataRoot: root}
	got, err := List(cfg, "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want пустой список без PDA.yaml у сценария", got)
	}
}
