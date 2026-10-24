package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"elb/daemon/internal/audit"
	"elb/daemon/internal/config"
	"elb/daemon/internal/settings"
)

func TestFilterConsoleNoise_RemoteEx(t *testing.T) {
	raw := "Command 'gm ' sent to player with entity id 6008\n" +
		"{:ElbMod} ElbMod: Game_Event Event_Player_Info seq=2978 pending=True\n"
	got := filterConsoleNoise(raw)
	if got != "" {
		t.Errorf("filterConsoleNoise(remoteex-шум) = %q, хочу пустую строку", got)
	}
}

func TestValidateCustomCommands_RejectsBuiltinName(t *testing.T) {
	err := validateCustomCommands([]settings.CustomCommand{{Name: "wipe", Enabled: true}})
	if err == nil {
		t.Fatal("имя встроенной команды должно быть отклонено")
	}
}

func TestValidateCustomCommands_RejectsDuplicateName(t *testing.T) {
	err := validateCustomCommands([]settings.CustomCommand{
		{Name: "discord", Enabled: true},
		{Name: "Discord", Enabled: true},
	})
	if err == nil {
		t.Fatal("повторяющееся имя (без учёта регистра) должно быть отклонено")
	}
}

func TestValidateCustomCommands_RejectsInvalidChars(t *testing.T) {
	for _, name := range []string{"", "cb:rules", "has space", "тест"} {
		if err := validateCustomCommands([]settings.CustomCommand{{Name: name, Enabled: true}}); err == nil {
			t.Fatalf("ожидали отклонение для %q", name)
		}
	}
}

func TestValidateCustomCommands_UppercaseIsLoweredBeforeCheck(t *testing.T) {
	if err := validateCustomCommands([]settings.CustomCommand{{Name: "Discord", Enabled: true}}); err != nil {
		t.Fatalf("верхний регистр должен приводиться к нижнему перед проверкой: %v", err)
	}
}

func TestValidateCustomCommands_AcceptsValid(t *testing.T) {
	err := validateCustomCommands([]settings.CustomCommand{
		{Name: "discord", Enabled: true},
		{Name: "discord_link-2", Enabled: false},
	})
	if err != nil {
		t.Fatalf("валидные имена не должны отклоняться: %v", err)
	}
}

func TestValidateCustomCommands_RejectsRulesBuiltinName(t *testing.T) {
	err := validateCustomCommands([]settings.CustomCommand{{Name: "rules", Enabled: true}})
	if err == nil {
		t.Fatal("имя встроенной команды rules должно быть отклонено")
	}
}

func testCfgForAuth(t *testing.T) config.Config {
	t.Helper()
	dir := t.TempDir()
	return config.Config{
		Tokens:       map[string]bool{"good-token": true},
		SettingsFile: filepath.Join(dir, "settings.json"),
		AuditDBFile:  filepath.Join(dir, "audit.db"),
	}
}

func TestAuthMiddleware_UnauthorizedNotRecordedWhenDisabled(t *testing.T) {
	cfg := testCfgForAuth(t)
	h := authMiddleware(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest("GET", "/api/players", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	events, err := audit.Recent(cfg, 0)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("audit выключен по умолчанию — событий быть не должно, получили %d", len(events))
	}
}

func TestAuthMiddleware_UnauthorizedRecordedWhenEnabled(t *testing.T) {
	cfg := testCfgForAuth(t)
	if err := settings.Save(cfg.SettingsFile, settings.Config{Audit: settings.Audit{UnauthorizedAPIRequests: true}}); err != nil {
		t.Fatalf("settings.Save: %v", err)
	}
	h := authMiddleware(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest("GET", "/api/players", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	events, err := audit.Recent(cfg, 0)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	if events[0].Category != audit.CategoryUnauthorizedAPIRequests {
		t.Errorf("unexpected category: %s", events[0].Category)
	}

	req2 := httptest.NewRequest("GET", "/api/players", nil)
	req2.Header.Set("Authorization", "Bearer good-token")
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid token, got %d", rr2.Code)
	}
	events, err = audit.Recent(cfg, 0)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("valid request should not add a new audit event, still expected 1, got %d", len(events))
	}
}

func postAuditLogin(t *testing.T, mux *http.ServeMux, user string, success bool) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"user": user, "success": success, "remoteAddr": "1.2.3.4"})
	req := httptest.NewRequest("POST", "/api/audit/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func TestAuditLogin_NotRecordedWhenDisabled(t *testing.T) {
	cfg := testCfgForAuth(t)
	mux := http.NewServeMux()
	registerAuditRoutes(mux, cfg, nil, nil)

	rr := postAuditLogin(t, mux, "admin", true)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 even when disabled (тихий no-op), got %d", rr.Code)
	}

	events, err := audit.Recent(cfg, 0)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("audit выключен — событий быть не должно, получили %d", len(events))
	}
}

func TestAuditLogin_RecordedWhenEnabled(t *testing.T) {
	cfg := testCfgForAuth(t)
	if err := settings.Save(cfg.SettingsFile, settings.Config{Audit: settings.Audit{LoginTracking: true}}); err != nil {
		t.Fatalf("settings.Save: %v", err)
	}
	mux := http.NewServeMux()
	registerAuditRoutes(mux, cfg, nil, nil)

	if rr := postAuditLogin(t, mux, "admin", true); rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if rr := postAuditLogin(t, mux, "attacker", false); rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	events, err := audit.Recent(cfg, 0)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 audit events (OK + FAILED), got %d", len(events))
	}
	for _, ev := range events {
		if ev.Category != audit.CategoryLoginTracking {
			t.Errorf("unexpected category: %s", ev.Category)
		}
	}
}
