package hub

import (
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const writeTimeout = 5 * time.Second

var upgrader = websocket.Upgrader{

	CheckOrigin: func(r *http.Request) bool { return true },
}

type Hub struct {
	mu      sync.Mutex
	clients map[*websocket.Conn]string
}

func New() *Hub {
	return &Hub{clients: map[*websocket.Conn]string{}}
}

func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request, locale string) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("hub: upgrade: %v", err)
		return
	}

	h.mu.Lock()
	h.clients[conn] = locale
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.clients, conn)
		h.mu.Unlock()
		conn.Close()
	}()

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (h *Hub) ClientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

func (h *Hub) Broadcast(variants map[string][]byte, defaultLocale string) {
	type target struct {
		conn   *websocket.Conn
		locale string
	}
	h.mu.Lock()
	targets := make([]target, 0, len(h.clients))
	for conn, locale := range h.clients {
		targets = append(targets, target{conn, locale})
	}
	h.mu.Unlock()

	var failed []*websocket.Conn
	for _, t := range targets {
		html, ok := variants[t.locale]
		if !ok {
			html = variants[defaultLocale]
		}
		t.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		if err := t.conn.WriteMessage(websocket.TextMessage, html); err != nil {
			t.conn.Close()
			failed = append(failed, t.conn)
		}
	}

	if len(failed) > 0 {
		h.mu.Lock()
		for _, conn := range failed {
			delete(h.clients, conn)
		}
		h.mu.Unlock()
	}
}
