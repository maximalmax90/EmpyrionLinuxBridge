package web

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"time"

	"elb/ui/internal/daemonclient"
	"elb/ui/internal/store"
)

func TestTemplatesParse(t *testing.T) {

	_, err := template.New("root").Funcs(funcMap).Funcs(template.FuncMap{
		"playerColumns": func() any { return nil },
		"nowLocal":      func() string { return "" },
		"dateOf":        func(string) string { return "" },
		"timeOf":        func(string) string { return "" },
		"senderClass":   func(string) string { return "" },
		"localTime":     func(time.Time, string) string { return "" },
	}).ParseFS(embedded, "templates/*.html")
	if err != nil {
		t.Fatalf("template parse: %v", err)
	}
}

func TestSidebarNavBlockedGroup_ExecutesAndBlocks(t *testing.T) {
	tmpl, err := template.New("root").Funcs(funcMap).Funcs(template.FuncMap{
		"playerColumns": func() any { return nil },
		"nowLocal":      func() string { return "" },
		"dateOf":        func(string) string { return "" },
		"timeOf":        func(string) string { return "" },
		"senderClass":   func(string) string { return "" },
		"localTime":     func(time.Time, string) string { return "" },
	}).ParseFS(embedded, "templates/*.html")
	if err != nil {
		t.Fatalf("template parse: %v", err)
	}

	data := map[string]any{
		"Page": "overview", "IsAdmin": true, "Perms": store.ModeratorPermissions{},
		"DaemonVersion": "", "UIVersion": "", "SysInfo": daemonclient.SysInfo{},
		"ActiveMounts": []daemonclient.MountInfo{}, "WarningPercent": 0, "CriticalPercent": 0,
		"Playfields": daemonclient.Playfields{},
	}

	data["DaemonUnreachable"] = false
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "sidebar_nav", data); err != nil {
		t.Fatalf("execute sidebar_nav (reachable): %v", err)
	}
	html := buf.String()
	if strings.Contains(html, `id="sidebar-nav-blocked-group" class="daemon-blocked"`) {
		t.Fatal("daemon доступен — блокирующая группа не должна быть daemon-blocked")
	}

	data["DaemonUnreachable"] = true
	buf.Reset()
	if err := tmpl.ExecuteTemplate(&buf, "sidebar_nav", data); err != nil {
		t.Fatalf("execute sidebar_nav (unreachable): %v", err)
	}
	html = buf.String()
	if !strings.Contains(html, `id="sidebar-nav-blocked-group" class="daemon-blocked" inert`) {
		t.Fatalf("демон недоступен — ожидали inert+daemon-blocked на #sidebar-nav-blocked-group, получили:\n%s", html)
	}

	if !strings.Contains(html, `<a href="/" class="active"`) {
		t.Fatal("ссылка на Обзор должна остаться активной ссылкой даже при недоступном демоне")
	}
	if !strings.Contains(html, `<a href="/accounts" class=""`) {
		t.Fatal("ссылка на Аккаунты должна остаться активной ссылкой даже при недоступном демоне")
	}
}

func TestServerState_UsedFromTemplate_Executes(t *testing.T) {
	tmpl, err := template.New("root").Funcs(funcMap).Funcs(template.FuncMap{
		"playerColumns": func() any { return nil },
		"nowLocal":      func() string { return "" },
		"dateOf":        func(string) string { return "" },
		"timeOf":        func(string) string { return "" },
		"senderClass":   func(string) string { return "" },
		"localTime":     func(time.Time, string) string { return "" },
	}).ParseFS(embedded, "templates/*.html")
	if err != nil {
		t.Fatalf("template parse: %v", err)
	}
	data := map[string]any{
		"Health":            daemonclient.Health{Active: true, TelnetOK: true},
		"ManualAction":      daemonclient.ManualAction{},
		"DaemonUnreachable": true,
		"Identity":          daemonclient.Identity{},
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "statusbar_fragment", data); err != nil {
		t.Fatalf("execute statusbar_fragment: %v", err)
	}
	if !strings.Contains(buf.String(), "state-unreachable") {
		t.Fatalf("ожидали state-unreachable в бейдже при DaemonUnreachable=true, получили:\n%s", buf.String())
	}
}
