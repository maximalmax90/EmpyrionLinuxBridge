package poller

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"elb/ui/internal/hub"

	"github.com/gorilla/websocket"
)

func TestRenderAndBroadcast_PerLocaleAndSkipUnchanged(t *testing.T) {
	tmplRu := template.Must(template.New("root").Parse(`{{define "frag"}}<div>{{.}} (ru)</div>{{end}}`))
	tmplEn := template.Must(template.New("root").Parse(`{{define "frag"}}<div>{{.}} (en)</div>{{end}}`))

	h := hub.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeWS(w, r, r.URL.Query().Get("locale"))
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

	deadline := time.Now().Add(2 * time.Second)
	for h.ClientCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	p := &Poller{
		hub: h,
		tmplByLocale: map[string]*template.Template{
			"ru": tmplRu,
			"en": tmplEn,
		},
		lastHTML: map[string][]byte{},
	}

	p.renderAndBroadcast("frag", "frag", "X")
	readAndExpect(t, connRu, "<div>X (ru)</div>")
	readAndExpect(t, connEn, "<div>X (en)</div>")

	p.renderAndBroadcast("frag", "frag", "X")
	p.renderAndBroadcast("frag", "frag", "Y")
	readAndExpect(t, connRu, "<div>Y (ru)</div>")
	readAndExpect(t, connEn, "<div>Y (en)</div>")
}

func readAndExpect(t *testing.T, conn *websocket.Conn, want string) {
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
