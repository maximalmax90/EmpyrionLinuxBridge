package modclient

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDedupeBySteamID(t *testing.T) {
	t.Run("keeps unique players untouched", func(t *testing.T) {
		in := []Player{
			{EntityId: 1, SteamId: "a"},
			{EntityId: 2, SteamId: "b"},
		}
		out := dedupeBySteamID(in)
		if len(out) != 2 {
			t.Fatalf("len = %d, want 2", len(out))
		}
	})

	t.Run("prefers higher entityId when both offline", func(t *testing.T) {
		in := []Player{
			{EntityId: 2080, SteamId: "s", Online: false},
			{EntityId: 6008, SteamId: "s", Online: false},
		}
		out := dedupeBySteamID(in)
		if len(out) != 1 || out[0].EntityId != 6008 {
			t.Fatalf("got %+v, want single entry with EntityId=6008", out)
		}
	})

	t.Run("prefers higher entityId regardless of input order", func(t *testing.T) {
		in := []Player{
			{EntityId: 6008, SteamId: "s", Online: false},
			{EntityId: 2080, SteamId: "s", Online: false},
		}
		out := dedupeBySteamID(in)
		if len(out) != 1 || out[0].EntityId != 6008 {
			t.Fatalf("got %+v, want single entry with EntityId=6008", out)
		}
	})

	t.Run("online wins even with lower entityId", func(t *testing.T) {
		in := []Player{
			{EntityId: 6008, SteamId: "s", Online: false},
			{EntityId: 2080, SteamId: "s", Online: true},
		}
		out := dedupeBySteamID(in)
		if len(out) != 1 || out[0].EntityId != 2080 || !out[0].Online {
			t.Fatalf("got %+v, want single online entry with EntityId=2080", out)
		}
	})

	t.Run("empty steamId entries pass through without deduping", func(t *testing.T) {
		in := []Player{
			{EntityId: 1, SteamId: ""},
			{EntityId: 2, SteamId: ""},
		}
		out := dedupeBySteamID(in)
		if len(out) != 2 {
			t.Fatalf("len = %d, want 2 (no dedup without steamId)", len(out))
		}
	})
}

func TestPlayerInventory_IOErrorMapsToErrInventoryBusy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"game error: IOError"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.PlayerInventory(1)
	if !errors.Is(err, ErrInventoryBusy) {
		t.Fatalf("err = %v, want errors.Is(err, ErrInventoryBusy)", err)
	}
}

func TestPlayerInventory_OtherErrorsNotMappedToErrInventoryBusy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"game error: PlayerIdNotFound"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.PlayerInventory(1)
	if err == nil {
		t.Fatal("ожидалась ошибка")
	}
	if errors.Is(err, ErrInventoryBusy) {
		t.Fatalf("err = %v, НЕ должна распознаваться как ErrInventoryBusy", err)
	}
}

func TestPostMessageWithRetry_SuccessPassesThroughWithoutRetry(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.PostMessageWithRetry("/chat", map[string]any{"text": "hi"}); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if hits != 1 {
		t.Fatalf("hits = %d, want 1 (успех не должен повторяться)", hits)
	}
}

func TestPostMessageWithRetry_EOFIsRetried(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Fatalf("hijack: %v", err)
			}
			conn.Close()
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.PostMessageWithRetry("/message/all", map[string]any{"text": "hi"}); err != nil {
		t.Fatalf("err = %v, want nil (повтор после EOF должен был пройти)", err)
	}
	if hits != 2 {
		t.Fatalf("hits = %d, want 2 (EOF на первой попытке должен запустить ровно один повтор)", hits)
	}
}

func TestGetWithRetry_EOFIsRetried(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Fatalf("hijack: %v", err)
			}
			conn.Close()
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	resp, err := getWithRetry(srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("err = %v, want nil (повтор после EOF должен был пройти)", err)
	}
	resp.Body.Close()
	if hits != 2 {
		t.Fatalf("hits = %d, want 2 (EOF на первой попытке должен запустить ровно один повтор)", hits)
	}
}

func TestGetWithRetry_NonRetryableErrorNotRetried(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	resp, err := getWithRetry(srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("err = %v, want nil (500 — не сетевая ошибка, getWithRetry её не видит, статус разбирает вызывающий)", err)
	}
	resp.Body.Close()
	if hits != 1 {
		t.Fatalf("hits = %d, want 1 (ответ со статусом 500 — не повод для повтора на этом уровне)", hits)
	}
}

func TestPostMessageWithRetry_NonTimeoutErrorNotRetried(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.PostMessageWithRetry("/chat", map[string]any{"text": "hi"}); err == nil {
		t.Fatal("ожидалась ошибка")
	}
	if hits != 1 {
		t.Fatalf("hits = %d, want 1 (не-таймаут ошибка не должна приводить к повтору)", hits)
	}
}
