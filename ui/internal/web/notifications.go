package web

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
)

const notificationsDropdownLimit = 30

func (s *Server) handleActionNotifications(w http.ResponseWriter, r *http.Request) {
	list, err := s.daemon.Notifications(notificationsDropdownLimit)
	if err != nil {
		log.Printf("notifications: list: %v", err)
	}
	if err := s.execTemplate(w, r, "notif_dropdown_inner", list); err != nil {
		log.Printf("notifications: render: %v", err)
	}
}

func (s *Server) handleActionNotificationsReadAll(w http.ResponseWriter, r *http.Request) {
	if err := s.daemon.MarkAllNotificationsRead(); err != nil {
		log.Printf("notifications: read-all: %v", err)
	}
	s.handleActionNotifications(w, r)
}

func (s *Server) handleActionNotificationsClear(w http.ResponseWriter, r *http.Request) {
	if err := s.daemon.ClearAllNotifications(); err != nil {
		log.Printf("notifications: clear: %v", err)
	}
	s.handleActionNotifications(w, r)
}

func (s *Server) handleActionNotificationRead(w http.ResponseWriter, r *http.Request) {

	if id, err := strconv.ParseInt(r.PathValue("id"), 10, 64); err != nil {
		log.Printf("notifications: invalid id: %v", err)
	} else if err := s.daemon.MarkNotificationRead(id); err != nil {
		log.Printf("notifications: read %d: %v", id, err)
	}
	s.handleActionNotifications(w, r)
}

func (s *Server) handleActionNotificationsUnreadCount(w http.ResponseWriter, r *http.Request) {
	n, err := s.daemon.NotificationsUnreadCount()
	if err != nil {
		log.Printf("notifications: unread count: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{"count": n})
}
