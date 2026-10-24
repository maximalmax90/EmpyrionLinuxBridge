package web

import (
	"fmt"
	"net/http"
	"strings"
)

func vipListSplit(raw string) []string {
	var out []string
	for _, id := range strings.Split(raw, ",") {
		id = strings.TrimSpace(id)
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

func vipListContains(raw, steamID string) bool {
	for _, id := range vipListSplit(raw) {
		if id == steamID {
			return true
		}
	}
	return false
}

func (s *Server) handleActionPlayerVip(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	steamID := r.FormValue("steamId")
	if steamID == "" {
		writeToast(w, "Не указан игрок")
		return
	}
	want := r.FormValue("vip") == "on"

	cfg, err := s.daemon.Config()
	if err != nil {
		writeToast(w, fmt.Sprintf("Не удалось прочитать конфигурацию сервера: %v", err))
		return
	}
	ids := vipListSplit(cfg.Server.PlayerLoginVipNames.Value)
	has := false
	idx := -1
	for i, id := range ids {
		if id == steamID {
			has = true
			idx = i
			break
		}
	}

	switch {
	case want && !has:
		ids = append(ids, steamID)
	case !want && has:
		ids = append(ids[:idx], ids[idx+1:]...)
	default:

		writeToast(w, "VIP-статус уже соответствует выбранному")
		return
	}

	if err := s.daemon.SetPlayerLoginVipNames(strings.Join(ids, ",")); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось сохранить список VIP: %v", err))
		return
	}

	writeToast(w, "VIP-статус изменён — вступит в силу после перезапуска сервера")
}
