package hub

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestBroadcast_PerLocaleRouting(t *testing.T) {
	h := New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		locale := r.URL.Query().Get("locale")
		h.ServeWS(w, r, locale)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	dial := func(locale string) *websocket.Conn {
		conn, _, err := websocket.DefaultDialer.Dial(wsURL+"?locale="+locale, nil)
		if err != nil {
			t.Fatalf("dial %s: %v", locale, err)
		}
		return conn
	}

	connRu := dial("ru")
	defer connRu.Close()
	connEn := dial("en")
	defer connEn.Close()
	connUnknown := dial("fr")
	defer connUnknown.Close()

	waitForClients(t, h, 3)

	variants := map[string][]byte{
		"ru": []byte("<div>рус</div>"),
		"en": []byte("<div>eng</div>"),
	}
	h.Broadcast(variants, "ru")

	assertMessage(t, connRu, "<div>рус</div>")
	assertMessage(t, connEn, "<div>eng</div>")
	assertMessage(t, connUnknown, "<div>рус</div>")
}

func waitForClients(t *testing.T, h *Hub, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.ClientCount() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d registered clients", want)
}

func assertMessage(t *testing.T, conn *websocket.Conn, want string) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != want {
		t.Fatalf("got %q, want %q", data, want)
	}
}
