package snapshot

import (
	"path/filepath"
	"testing"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/settings"
)

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func mkEntries(t *testing.T, names []string, layout string) []RetentionEntry {
	t.Helper()
	var entries []RetentionEntry
	for _, n := range names {
		tm, err := time.ParseInLocation(layout, n, time.Local)
		if err != nil {
			t.Fatalf("bad fixture time %q: %v", n, err)
		}
		entries = append(entries, RetentionEntry{Name: n, T: tm})
	}
	return entries
}

func keptNames(entries []RetentionEntry, keep map[string]bool) []string {
	var out []string
	for _, e := range entries {
		if keep[e.Name] {
			out = append(out, e.Name)
		}
	}
	return out
}

func assertKept(t *testing.T, entries []RetentionEntry, r settings.Retention, want ...string) {
	t.Helper()
	keep := SelectRetentionKeep(entries, r)
	got := keptNames(entries, keep)
	if len(got) != len(want) {
		t.Fatalf("kept %v, want %v", got, want)
	}
	wantSet := make(map[string]bool, len(want))
	for _, w := range want {
		wantSet[w] = true
	}
	for _, g := range got {
		if !wantSet[g] {
			t.Fatalf("kept %v, want %v (unexpected %q)", got, want, g)
		}
	}
}

func TestSelectRetentionKeepOff(t *testing.T) {
	entries := mkEntries(t, []string{"20260101_1200", "20260102_1200"}, "20060102_1504")
	keep := SelectRetentionKeep(entries, settings.Retention{})
	if len(keep) != 0 {
		t.Fatalf("Off() retention should keep nothing, got %v", keep)
	}
}

func TestSelectRetentionKeepLast(t *testing.T) {
	names := []string{"20260101_1200", "20260102_1200", "20260103_1200", "20260104_1200"}
	entries := mkEntries(t, names, "20060102_1504")

	assertKept(t, entries, settings.Retention{Last: 2}, "20260103_1200", "20260104_1200")
}

func TestSelectRetentionKeepDaily(t *testing.T) {

	names := []string{
		"20260101_0800", "20260101_2000",
		"20260102_1200",
		"20260103_1200",
	}
	entries := mkEntries(t, names, "20060102_1504")
	assertKept(t, entries, settings.Retention{Daily: 2}, "20260102_1200", "20260103_1200")
}

func TestSelectRetentionKeepWeeklyMonthlyYearly(t *testing.T) {

	names := []string{
		"20260105_1200",
		"20260112_1200",
		"20260119_1200",
		"20260126_1200",
		"20260202_1200",
	}
	entries := mkEntries(t, names, "20060102_1504")

	assertKept(t, entries, settings.Retention{Weekly: 3}, "20260119_1200", "20260126_1200", "20260202_1200")
}

func TestSelectRetentionKeepUnion(t *testing.T) {

	names := []string{
		"20260101_1200",
		"20260102_1200",
		"20260103_1200",
	}
	entries := mkEntries(t, names, "20060102_1504")

	assertKept(t, entries, settings.Retention{Daily: 1, Last: 2}, "20260102_1200", "20260103_1200")
}

func TestSelectRetentionKeepMonthlyBoundary(t *testing.T) {

	names := []string{"20260131_1200", "20260201_1200"}
	entries := mkEntries(t, names, "20060102_1504")
	assertKept(t, entries, settings.Retention{Monthly: 2}, "20260131_1200", "20260201_1200")
}

func TestApplyRetentionOffIsNoop(t *testing.T) {

	cfg := config.Config{BackupsDir: "/nonexistent/elb-retention-test-dir"}
	off := settings.Retention{}
	deleted, err := ApplyRetention(cfg, off)
	if err != nil || deleted != nil {
		t.Fatalf("ApplyRetention with Off() retention should be a pure no-op, got deleted=%v err=%v", deleted, err)
	}
	deleted, err = ApplyDiskRetention("/nonexistent/elb-retention-test-dir", off)
	if err != nil || deleted != nil {
		t.Fatalf("ApplyDiskRetention with Off() retention should be a pure no-op, got deleted=%v err=%v", deleted, err)
	}
}

func TestArchiveExcludesDefaults(t *testing.T) {

	entries := []subvolEntry{
		{RelPath: "wine", Dir: "1_wine"},
		{RelPath: "EMP/elb", Dir: "0_EMP_elb"},
		{RelPath: "EMP/Content/Scenarios", Dir: "2_EMP_Content_Scenarios"},
		{RelPath: "EMP/Content/Mods", Dir: "3_EMP_Content_Mods"},
		{RelPath: "EMP/Saves", Dir: "4_EMP_Saves"},
	}

	got := archiveExcludes(entries, "EMP", settings.BackupExclude{})
	if len(got) != 0 {
		t.Fatalf("default excludes = %v, want none (nothing unconditional in the new layout)", got)
	}
}

func TestArchiveExcludesFullOptIn(t *testing.T) {
	entries := []subvolEntry{
		{RelPath: "wine", Dir: "1_wine"},
		{RelPath: "EMP/elb", Dir: "0_EMP_elb"},
		{RelPath: "EMP/Content/Scenarios", Dir: "2_EMP_Content_Scenarios"},
		{RelPath: "EMP/Content/Mods", Dir: "3_EMP_Content_Mods"},
		{RelPath: "EMP/Saves", Dir: "4_EMP_Saves"},
	}
	got := archiveExcludes(entries, "EMP", settings.BackupExclude{
		Elb:       true,
		Wine:      true,
		Saves:     []string{"Cache"},
		Mods:      []string{"DemoMod"},
		Scenarios: []string{"Tutorial"},
	})
	mustContain := []string{
		"0_EMP_elb",
		"1_wine",
		filepath.Join("4_EMP_Saves", "Cache"),
		filepath.Join("3_EMP_Content_Mods", "DemoMod"),
		filepath.Join("2_EMP_Content_Scenarios", "Tutorial"),
	}
	for _, w := range mustContain {
		if !containsStr(got, w) {
			t.Fatalf("full-opt-in excludes missing %q, got %v", w, got)
		}
	}
}

func TestArchiveExcludesMissingSubvolume(t *testing.T) {

	entries := []subvolEntry{{RelPath: "EMP/elb", Dir: "0_EMP_elb"}}
	got := archiveExcludes(entries, "EMP", settings.BackupExclude{Wine: true, Elb: true})
	if containsStr(got, "1_wine") {
		t.Fatalf("should not reference wine dir when subvolume absent, got %v", got)
	}
	if !containsStr(got, "0_EMP_elb") {
		t.Fatalf("elb exclude should still apply when its subvolume is present, got %v", got)
	}
}

func TestArchiveExcludesScenariosUnconditional(t *testing.T) {

	entries := []subvolEntry{
		{RelPath: "EMP/Content/Scenarios", Dir: "2_EMP_Content_Scenarios"},
	}
	got := archiveExcludes(entries, "EMP", settings.BackupExclude{Scenarios: []string{"Tutorial"}})
	if !containsStr(got, filepath.Join("2_EMP_Content_Scenarios", "Tutorial")) {
		t.Fatalf("scenarios exclude must apply unconditionally, got %v", got)
	}
}

func TestExcludedRaw(t *testing.T) {
	excludes := []string{filepath.Join("0_EMP", "Logs")}
	if !excludedRaw(filepath.Join("0_EMP", "Logs"), excludes) {
		t.Fatal("exact match should be excluded")
	}
	if !excludedRaw(filepath.Join("0_EMP", "Logs", "5150", "Dedicated.log"), excludes) {
		t.Fatal("nested path should be excluded")
	}
	if excludedRaw(filepath.Join("0_EMP", "LogsOther"), excludes) {
		t.Fatal("sibling with shared name prefix should NOT be excluded")
	}
	if excludedRaw(filepath.Join("0_EMP", "Saves"), excludes) {
		t.Fatal("unrelated path should not be excluded")
	}
}
