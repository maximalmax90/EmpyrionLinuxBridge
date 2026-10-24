package web

import (
	"encoding/json"
	"log"
	"net/http"

	"elb/ui/internal/daemonclient"
)

func (s *Server) handleChatHistoryPage(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	data := map[string]any{"Username": u.Username}
	for k, v := range s.statusBarData(u) {
		data[k] = v
	}
	if err := s.execTemplate(w, r, "chathistory.html", data); err != nil {
		log.Printf("chat history page: render: %v", err)
	}
}

func (s *Server) handleActionChatHistory(w http.ResponseWriter, r *http.Request) {
	var body struct {
		From    string `json:"from"`
		To      string `json:"to"`
		SteamId string `json:"steamId"`
		Sender  string `json:"sender"`
		Channel string `json:"channel"`
		Target  string `json:"target"`
		Limit   int    `json:"limit"`
		Offset  int    `json:"offset"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	result, err := s.daemon.ChatHistory(daemonclient.ChatHistoryFilter{
		From: body.From, To: body.To,
		SteamId: body.SteamId, Sender: body.Sender,
		Channel: body.Channel, Target: body.Target,
		Limit: body.Limit, Offset: body.Offset,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleActionChatSenderClasses(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.senderClassMap())
}
