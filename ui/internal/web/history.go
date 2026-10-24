package web

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"elb/ui/internal/daemonclient"
)

func (s *Server) handleHistoryPage(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	data := map[string]any{"Username": u.Username}
	for k, v := range s.statusBarData(u) {
		data[k] = v
	}
	if err := s.execTemplate(w, r, "history.html", data); err != nil {
		log.Printf("history page: render: %v", err)
	}
}

func (s *Server) handleActionHistoryAround(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind             string  `json:"kind"`
		Key              string  `json:"key"`
		EventKind        string  `json:"eventKind"`
		Playfield        string  `json:"playfield"`
		From             string  `json:"from"`
		To               string  `json:"to"`
		Distance         float64 `json:"distance"`
		HideFirst        bool    `json:"hideFirst"`
		HideOnlyPosition bool    `json:"hideOnlyPosition"`
		Limit            int     `json:"limit"`
		Offset           int     `json:"offset"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	result, err := s.daemon.HistoryAround(body.Kind, body.Key, body.EventKind, body.Playfield, body.From, body.To, body.Distance, body.HideFirst, body.HideOnlyPosition, body.Limit, body.Offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleActionHistoryNames(w http.ResponseWriter, r *http.Request) {
	factions, err := s.daemon.Factions()
	if err != nil {
		log.Printf("history names: factions: %v", err)
	}
	players, err := s.daemon.Players()
	if err != nil {
		log.Printf("history names: players: %v", err)
	}
	structures, err := s.daemon.Structures()
	if err != nil {
		log.Printf("history names: structures: %v", err)
	}
	playfieldSystem, err := s.daemon.PlayfieldSystems()
	if err != nil {
		log.Printf("history names: playfield systems: %v", err)
	}

	factionLabel := make(map[int]string, len(factions))
	factionNames := make(map[string]string, len(factions))
	for _, f := range factions {
		label := f.Abbreviation
		if label == "" {
			label = f.Name
		}
		factionLabel[f.ID] = label
		factionNames[strconv.Itoa(f.ID)] = label
	}

	playerNames := make(map[string]string, len(players))
	playerFaction := make(map[string]string, len(players))

	steamIdByName := make(map[string]string, len(players))
	for _, p := range players {
		playerNames[strconv.Itoa(p.EntityId)] = p.Name
		if p.FactionTag != "" {
			playerFaction[p.SteamId] = p.FactionTag
		}
		if p.SteamId != "" {
			steamIdByName[p.Name] = p.SteamId
		}
	}

	structureFaction := make(map[string]string, len(structures))
	for _, st := range structures {
		var label string
		switch st.FactionGroup {
		case daemonclient.FactionGroupFaction:
			label = factionLabel[st.FactionId]
		case daemonclient.FactionGroupPlayer:
			label = playerNames[strconv.Itoa(st.FactionId)]
		}
		if label != "" {
			structureFaction[strconv.Itoa(st.ID)] = label
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"factionNames":     factionNames,
		"playerNames":      playerNames,
		"playerFaction":    playerFaction,
		"structureFaction": structureFaction,
		"playfieldSystem":  playfieldSystem,
		"steamIdByName":    steamIdByName,
	})
}
