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

func (s *Server) handleStructuresPage(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	data := s.structuresData(u)
	for k, v := range s.statusBarData(u) {
		data[k] = v
	}
	if err := s.execTemplate(w, r, "structures.html", data); err != nil {
		log.Printf("structures: render: %v", err)
	}
}

func (s *Server) handleActionStructuresFragment(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	if err := s.execTemplate(w, r, "structures_fragment", s.structuresData(u)); err != nil {
		log.Printf("structures fragment: render: %v", err)
	}
}

func (s *Server) structuresData(u *userInfo) map[string]any {
	structures, err := s.daemon.Structures()
	if err != nil {
		log.Printf("structures: %v", err)
	}
	players, err := s.daemon.Players()
	if err != nil {
		log.Printf("structures: players: %v", err)
	}
	factions, err := s.daemon.Factions()
	if err != nil {
		log.Printf("structures: factions: %v", err)
	}
	historyCounts, err := s.daemon.HistoryCounts("structure")
	if err != nil {
		log.Printf("structures: history counts: %v", err)
	}
	pendingIDs, err := s.daemon.StructurePendingIDs()
	if err != nil {
		log.Printf("structures: pending ids: %v", err)
	}

	ownerNames := make(map[int]string, len(players))
	for _, p := range players {
		ownerNames[p.EntityId] = p.Name
	}
	factionLabel := make(map[int]string, len(factions))
	for _, f := range factions {
		label := f.Abbreviation
		if label == "" {
			label = f.Name
		}
		factionLabel[f.ID] = label
	}

	playfieldSet := map[string]bool{}
	typeSet := map[string]bool{}
	factionSet := map[string]bool{}
	ownerSet := map[string]bool{}

	factionAllSet := map[string]bool{}
	for _, label := range factionLabel {
		if label != "" {
			factionAllSet[label] = true
		}
	}

	for i := range structures {
		st := &structures[i]
		st.HistoryCount = historyCounts[strconv.Itoa(st.ID)]
		st.Pending = pendingIDs[st.ID]
		if st.Playfield != "" {
			playfieldSet[st.Playfield] = true
		}
		if st.TypeName != "" {
			typeSet[st.TypeName] = true
		}
		switch st.FactionGroup {
		case daemonclient.FactionGroupFaction:
			st.FactionTag = factionLabel[st.FactionId]
			if st.FactionTag != "" {
				factionSet[st.FactionTag] = true
			}
		case daemonclient.FactionGroupPlayer:
			st.OwnerName = ownerNames[st.FactionId]
			if st.OwnerName != "" {
				ownerSet[st.OwnerName] = true
			}
		}
	}

	return map[string]any{
		"Username":          u.Username,
		"Structures":        structures,
		"PlayfieldOptions":  sortedKeysOf(playfieldSet),
		"TypeOptions":       sortedKeysOf(typeSet),
		"FactionOptions":    sortedKeysOf(factionSet),
		"FactionAllOptions": sortedKeysOf(factionAllSet),
		"OwnerOptions":      sortedKeysOf(ownerSet),
	}
}

func sortedKeysOf(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func structureIDFromPath(r *http.Request) (int, error) {
	return strconv.Atoi(r.PathValue("id"))
}

func (s *Server) handleActionStructureTouch(w http.ResponseWriter, r *http.Request) {
	id, err := structureIDFromPath(r)
	if err != nil {
		writeToast(w, "Некорректный id структуры")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	playfield := r.FormValue("playfield")
	queued, err := s.daemon.StructureTouch(id, playfield)
	if err != nil {
		writeToast(w, fmt.Sprintf("Не удалось защитить структуру от вайпа: %v", err))
		return
	}
	if queued {
		writeToast(w, "Плейфилд сейчас не загружен — структура будет защищена от вайпа, когда он загрузится")
		return
	}
	writeToast(w, "Структура защищена от вайпа")
}

func (s *Server) handleActionStructureRepair(w http.ResponseWriter, r *http.Request) {
	id, err := structureIDFromPath(r)
	if err != nil {
		writeToast(w, "Некорректный id структуры")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	playfield := r.FormValue("playfield")
	queued, err := s.daemon.StructureRepair(id, playfield)
	if err != nil {
		writeToast(w, fmt.Sprintf("Не удалось восстановить структуру: %v", err))
		return
	}
	if queued {
		writeToast(w, "Плейфилд сейчас не загружен — структура будет восстановлена, когда он загрузится")
		return
	}
	writeToast(w, "Структура отправлена на восстановление")
}

func (s *Server) handleActionStructureDestroy(w http.ResponseWriter, r *http.Request) {
	id, err := structureIDFromPath(r)
	if err != nil {
		writeToast(w, "Некорректный id структуры")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	playfield := r.FormValue("playfield")
	if err := s.daemon.StructureDestroy(id, playfield); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось удалить структуру: %v", err))
		return
	}
	writeToast(w, "Структура удалена")
}

func (s *Server) handleActionStructureBackupNow(w http.ResponseWriter, r *http.Request) {
	id, err := structureIDFromPath(r)
	if err != nil {
		writeToast(w, "Некорректный id структуры")
		return
	}
	captured, err := s.daemon.StructureBackupNow(id)
	if err != nil {
		writeToast(w, fmt.Sprintf("Не удалось сделать снимок: %v", err))
		return
	}
	if captured {
		writeToast(w, "Снимок структуры сделан")
	} else {
		writeToast(w, "Снимок запрошен, но подтверждение не пришло за 9с — проверьте в истории чуть позже")
	}
}

func (s *Server) handleActionStructureFaction(w http.ResponseWriter, r *http.Request) {
	id, err := structureIDFromPath(r)
	if err != nil {
		writeToast(w, "Некорректный id структуры")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	abbrev := r.FormValue("abbrev")
	if err := s.daemon.StructureSetFaction(id, abbrev); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось сменить фракцию структуры: %v", err))
		return
	}
	writeToast(w, "Фракция структуры изменена")
}

func parseOptionalPercent(r *http.Request, field string) (*float64, error) {
	raw := r.FormValue(field)
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", field, err)
	}
	return &v, nil
}

func (s *Server) handleActionStructureRefuel(w http.ResponseWriter, r *http.Request) {
	id, err := structureIDFromPath(r)
	if err != nil {
		writeToast(w, "Некорректный id структуры")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	playfield := r.FormValue("playfield")
	fuel, err := parseOptionalPercent(r, "fuelPercent")
	if err != nil {
		writeToast(w, "Некорректное значение топлива")
		return
	}
	oxygen, err := parseOptionalPercent(r, "oxygenPercent")
	if err != nil {
		writeToast(w, "Некорректное значение кислорода")
		return
	}
	pentaxid, err := parseOptionalPercent(r, "pentaxidPercent")
	if err != nil {
		writeToast(w, "Некорректное значение пентаксида")
		return
	}
	if fuel == nil && oxygen == nil && pentaxid == nil {
		writeToast(w, "Укажите хотя бы одно значение для заправки")
		return
	}
	if err := s.daemon.StructureRefuel(id, playfield, fuel, oxygen, pentaxid); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось поставить заявку на заправку: %v", err))
		return
	}
	writeToast(w, "Заявка на заправку структуры принята")
}

func (s *Server) handleActionStructureMove(w http.ResponseWriter, r *http.Request) {
	id, err := structureIDFromPath(r)
	if err != nil {
		writeToast(w, "Некорректный id структуры")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	playfield := r.FormValue("playfield")
	x, errX := strconv.ParseFloat(r.FormValue("x"), 64)
	y, errY := strconv.ParseFloat(r.FormValue("y"), 64)
	z, errZ := strconv.ParseFloat(r.FormValue("z"), 64)
	if errX != nil || errY != nil || errZ != nil {
		writeToast(w, "Некорректные координаты")
		return
	}
	queued, err := s.daemon.StructureMove(id, playfield, x, y, z)
	if err != nil {
		writeToast(w, fmt.Sprintf("Не удалось переместить структуру: %v", err))
		return
	}
	if queued {
		writeToast(w, "Плейфилд сейчас не загружен — структура будет перемещена, когда он загрузится")
		return
	}
	writeToast(w, "Структура перемещена")
}

func (s *Server) handleActionStructureRestore(w http.ResponseWriter, r *http.Request) {
	id, err := structureIDFromPath(r)
	if err != nil {
		writeToast(w, "Некорректный id структуры")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	mode := r.FormValue("mode")
	if mode != "all" && mode != "containers" && mode != "structure" {
		mode = "all"
	}
	snapshotId, _ := strconv.Atoi(r.FormValue("snapshotId"))
	positionPlayerId, _ := strconv.Atoi(r.FormValue("positionPlayerId"))
	res, err := s.daemon.StructureRestore(id, snapshotId, mode, positionPlayerId)
	if err != nil {
		writeToast(w, fmt.Sprintf("Не удалось восстановить структуру: %v", err))
		return
	}
	switch res.Branch {
	case "respawn":
		writeToast(w, fmt.Sprintf("Структура пересоздана из снимка (новый id %d)%s", res.NewID, restoreContainersSuffix(mode)))
	case "containers":
		writeToast(w, "Содержимое контейнеров восстановлено")
	default:
		writeToast(w, "Восстановление выполнено")
	}
}

func (s *Server) handleStructureBackupsList(w http.ResponseWriter, r *http.Request) {
	id, err := structureIDFromPath(r)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	list, err := s.daemon.StructureBackups(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

func (s *Server) handleStructureBlocksBreakdown(w http.ResponseWriter, r *http.Request) {
	id, err := structureIDFromPath(r)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	lang := ""
	if u := userFromCtx(r); u != nil {
		lang = u.Locale
	}
	breakdown, err := s.daemon.StructureBlocksBreakdown(id, lang)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(breakdown)
}

func restoreContainersSuffix(mode string) string {
	if mode == "all" {
		return ", содержимое контейнеров восстановлено"
	}
	return ""
}
