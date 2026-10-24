package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"

	"elb/ui/internal/daemonclient"
)

type warpPlayerTarget struct {
	EntityId  int                `json:"entityId"`
	SteamId   string             `json:"steamId"`
	Name      string             `json:"name"`
	Online    bool               `json:"online"`
	Playfield string             `json:"playfield"`
	Pos       *daemonclient.Vec3 `json:"pos,omitempty"`
	Rot       *daemonclient.Vec3 `json:"rot,omitempty"`
}

type warpStructureTarget struct {
	Id            int               `json:"id"`
	Name          string            `json:"name"`
	TypeName      string            `json:"typeName"`
	Playfield     string            `json:"playfield"`
	Owner         string            `json:"owner,omitempty"`
	OwnerEntityId int               `json:"ownerEntityId,omitempty"`
	Pos           daemonclient.Vec3 `json:"pos"`
	Rot           daemonclient.Vec3 `json:"rot"`
}

type warpTargets struct {
	Playfields []string              `json:"playfields"`
	Players    []warpPlayerTarget    `json:"players"`
	Structures []warpStructureTarget `json:"structures"`
}

func (s *Server) handleActionWarpTargets(w http.ResponseWriter, r *http.Request) {
	players, err := s.daemon.Players()
	if err != nil {
		log.Printf("warp targets: players: %v", err)
	}
	structures, err := s.daemon.Structures()
	if err != nil {
		log.Printf("warp targets: structures: %v", err)
	}

	playfieldSet := map[string]bool{}

	out := warpTargets{}
	for _, p := range players {
		if p.Playfield != "" {
			playfieldSet[p.Playfield] = true
		}
		out.Players = append(out.Players, warpPlayerTarget{
			EntityId: p.EntityId, SteamId: p.SteamId, Name: p.Name, Online: p.Online,
			Playfield: p.Playfield, Pos: p.Pos, Rot: p.Rot,
		})
	}
	for _, st := range structures {
		if st.Playfield != "" {
			playfieldSet[st.Playfield] = true
		}
		owner := st.OwnerName
		if owner == "" {
			owner = st.FactionTag
		}
		if owner == "" {
			owner = st.FactionGroupStr
		}
		var ownerEntityId int
		if st.FactionGroup == daemonclient.FactionGroupPlayer {
			ownerEntityId = st.FactionId
		}
		out.Structures = append(out.Structures, warpStructureTarget{
			Id: st.ID, Name: st.Name, TypeName: st.TypeName, Playfield: st.Playfield,
			Owner: owner, OwnerEntityId: ownerEntityId, Pos: st.Pos, Rot: st.Rot,
		})
	}
	for pf := range playfieldSet {
		out.Playfields = append(out.Playfields, pf)
	}
	sort.Strings(out.Playfields)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (s *Server) handleActionPlayerTeleport(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	entityId, err := strconv.Atoi(r.FormValue("entityId"))
	if err != nil {
		writeToast(w, "Некорректный id игрока")
		return
	}
	steamId := r.FormValue("steamId")
	playfield := r.FormValue("playfield")

	x, errX := strconv.ParseFloat(r.FormValue("x"), 64)
	y, errY := strconv.ParseFloat(r.FormValue("y"), 64)
	z, errZ := strconv.ParseFloat(r.FormValue("z"), 64)
	if errX != nil || errY != nil || errZ != nil {
		writeToast(w, "Некорректные координаты")
		return
	}

	var rot *daemonclient.Vec3
	if rawRotX := r.FormValue("rotX"); rawRotX != "" {
		rotX, errRX := strconv.ParseFloat(rawRotX, 64)
		rotY, errRY := strconv.ParseFloat(r.FormValue("rotY"), 64)
		rotZ, errRZ := strconv.ParseFloat(r.FormValue("rotZ"), 64)
		if errRX == nil && errRY == nil && errRZ == nil {
			rot = &daemonclient.Vec3{X: rotX, Y: rotY, Z: rotZ}
		}
	}

	if err := s.daemon.Teleport(entityId, steamId, playfield, daemonclient.Vec3{X: x, Y: y, Z: z}, rot); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось телепортировать: %v", err))
		return
	}
	writeToast(w, "Телепортация выполнена (для offline-игрока — применится при следующем входе)")
}
