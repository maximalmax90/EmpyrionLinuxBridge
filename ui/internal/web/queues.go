package web

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
)

func (s *Server) handleQueuesPage(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)

	items, err := s.daemon.Queues()
	if err != nil {
		log.Printf("queues: %v", err)
	}

	data := map[string]any{
		"Username": u.Username,
		"Items":    items,
	}
	for k, v := range s.statusBarData(u) {
		data[k] = v
	}
	if err := s.execTemplate(w, r, "queues.html", data); err != nil {
		log.Printf("queues: render: %v", err)
	}
}

func (s *Server) handleActionQueuesCancel(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeToast(w, "Некорректный id")
		return
	}
	if err := s.daemon.CancelQueueItem(kind, id); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось отменить заявку: %v", err))
		return
	}
	writeToast(w, "Заявка отменена")
}
