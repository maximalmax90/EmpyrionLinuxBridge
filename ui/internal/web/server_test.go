package web

import (
	"html/template"
	"testing"
	"time"

	"elb/ui/internal/daemonclient"
)

func TestSplitPlayfieldSuffix(t *testing.T) {
	cases := []struct {
		name       string
		wantBase   string
		wantSuffix string
	}{
		{"Akua", "Akua", ""},
		{"Akua Moon", "Akua", "Moon"},
		{"Tallodar Moon 1", "Tallodar", "Moon 1"},
		{"Akua Sector", "Akua", "Sector"},
		{"Alpha I Moon Sector", "Alpha I", "Moon Sector"},
		{"Alpha I Moon 1 Sector", "Alpha I", "Moon 1 Sector"},
		{"Trade Station Alpha", "Trade Station Alpha", ""},
		{"Alpha Asteroid Field I", "Alpha Asteroid Field I", ""},
	}
	for _, c := range cases {
		base, suffix := splitPlayfieldSuffix(c.name)
		if base != c.wantBase || suffix != c.wantSuffix {
			t.Errorf("splitPlayfieldSuffix(%q) = (%q, %q), want (%q, %q)", c.name, base, suffix, c.wantBase, c.wantSuffix)
		}
	}
}

func TestGroupedPlayfieldsLabel(t *testing.T) {
	playfields := []string{"Sys1", "Sys1 Sector", "Sys1 Moon", "Sys1 Moon Sector", "Sys1 Moon 1", "Sys1 Moon 1 Sector"}
	systems := map[string]string{
		"Sys1":               "Alpha",
		"Sys1 Sector":        "Alpha",
		"Sys1 Moon":          "Alpha",
		"Sys1 Moon Sector":   "Alpha",
		"Sys1 Moon 1":        "Alpha",
		"Sys1 Moon 1 Sector": "Alpha",
	}
	want := "Alpha: Sys1 (+ Sector, Moon, Moon Sector, Moon 1, Moon 1 Sector)"
	got := groupedPlayfieldsLabel(playfields, systems)
	if got != want {
		t.Errorf("groupedPlayfieldsLabel() = %q, want %q", got, want)
	}
}

func TestGroupedPlayfieldsLabel_MultiSystem(t *testing.T) {
	systems := map[string]string{
		"Akua": "Alpha", "Akua Moon": "Alpha",
		"Tallodar": "Alpha", "Tallodar Moon": "Alpha", "Tallodar Moon 1": "Alpha",
		"Ningues": "Beta", "Ningues Moon": "Beta",
	}
	playfields := []string{"Akua", "Akua Moon", "Tallodar", "Tallodar Moon", "Tallodar Moon 1", "Ningues", "Ningues Moon"}
	want := "Alpha: Akua (+ Moon), Tallodar (+ Moon, Moon 1)\nBeta: Ningues (+ Moon)"
	got := groupedPlayfieldsLabel(playfields, systems)
	if got != want {
		t.Errorf("groupedPlayfieldsLabel() = %q, want %q", got, want)
	}
}

func TestGroupedPlayfieldsLabel_NoSystems(t *testing.T) {
	got := groupedPlayfieldsLabel([]string{"Akua", "Tallodar"}, nil)
	want := "Akua, Tallodar"
	if got != want {
		t.Errorf("groupedPlayfieldsLabel() = %q, want %q", got, want)
	}
}

func TestGroupedPlayfieldsLabelHTML(t *testing.T) {
	systems := map[string]string{
		"Akua": "Alpha", "Akua Moon": "Alpha",
		"<script>": "A & B",
	}
	playfields := []string{"Akua", "Akua Moon", "<script>"}
	want := template.HTML("<strong>Alpha</strong>: Akua (+ Moon)\n<strong>A &amp; B</strong>: &lt;script&gt;")
	got := groupedPlayfieldsLabelHTML(playfields, systems)
	if got != want {
		t.Errorf("groupedPlayfieldsLabelHTML() = %q, want %q", got, want)
	}
}

func TestGroupedPlayfieldsLabelHTML_NoSystems(t *testing.T) {
	got := groupedPlayfieldsLabelHTML([]string{"Akua", "Tallodar"}, nil)
	want := template.HTML("Akua, Tallodar")
	if got != want {
		t.Errorf("groupedPlayfieldsLabelHTML() = %q, want %q", got, want)
	}
}

func TestScheduleSummaryLOnce_UsesGivenLocation(t *testing.T) {
	irkt := time.FixedZone("IRKT", 8*60*60)
	task := daemonclient.Task{Repeat: "once", OnceAt: "2026-09-20T01:30:00Z"}
	got := scheduleSummaryL("ru", task, irkt)
	want := "20.09.2026 09:30"
	if got != want {
		t.Errorf("scheduleSummaryL(once) = %q, want %q", got, want)
	}
}

func TestFormatNextRunL_UsesGivenLocation(t *testing.T) {
	irkt := time.FixedZone("IRKT", 8*60*60)
	got := formatNextRunL("2026-09-20T01:30:00Z", irkt)
	want := "20.09.2026 09:30:00"
	if got != want {
		t.Errorf("formatNextRunL() = %q, want %q", got, want)
	}
	if got := formatNextRunL("", irkt); got != "—" {
		t.Errorf("formatNextRunL(\"\") = %q, want %q", got, "—")
	}
}

func TestServerStateL_DaemonUnreachableTakesPriority(t *testing.T) {
	h := daemonclient.Health{Active: true, TelnetOK: true}
	got := serverStateL("ru", h, "stop", true)
	if got.Class != "state-unreachable" {
		t.Fatalf("Class = %q, want %q (daemonUnreachable должен перебивать и Health, и manualType)", got.Class, "state-unreachable")
	}

	got = serverStateL("ru", h, "stop", false)
	if got.Class != "state-stopping" {
		t.Fatalf("Class = %q, want %q", got.Class, "state-stopping")
	}
}

func TestServerStateL_ScenarioCacheInProgressTakesPriority(t *testing.T) {
	h := daemonclient.Health{Active: true, TelnetOK: true, ScenarioCache: daemonclient.ScenarioCacheStatus{Phase: "packaging"}}
	got := serverStateL("ru", h, "restart", false)
	if got.Class != "state-caching" {
		t.Fatalf("Class = %q, want %q (manualType=restart)", got.Class, "state-caching")
	}
	got = serverStateL("ru", h, "", false)
	if got.Class != "state-caching" {
		t.Fatalf("Class = %q, want %q (manualType уже сброшен, Health.Active=true)", got.Class, "state-caching")
	}

	h.ScenarioCache.Phase = "done"
	got = serverStateL("ru", h, "", false)
	if got.Class != "state-online" {
		t.Fatalf("Class = %q, want %q (фаза done не должна считаться InProgress)", got.Class, "state-online")
	}
}
