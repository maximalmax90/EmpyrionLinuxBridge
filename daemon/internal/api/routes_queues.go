package api

import (
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/hardwipe"
	"elb/daemon/internal/logbuf"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/playerqueue"
	"elb/daemon/internal/restartqueue"
	"elb/daemon/internal/scriptqueue"
	"elb/daemon/internal/structurequeue"
	"elb/daemon/internal/structuresdb"
)

type queueItem struct {
	Kind       string `json:"kind"`
	ID         int64  `json:"id,omitempty"`
	Label      string `json:"label"`
	Target     string `json:"target"`
	TargetName string `json:"targetName,omitempty"`
	Detail     string `json:"detail,omitempty"`
	QueuedAt   string `json:"queuedAt"`
	WaitingFor string `json:"waitingFor"`

	Cancelable bool `json:"cancelable,omitempty"`
}

func registerQueueRoutes(mux *http.ServeMux, cfg config.Config, mod *modclient.Client, logBuf *logbuf.Buffer) {
	mux.HandleFunc("GET /api/queues", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, collectQueueItems(cfg, mod))
	})

	mux.HandleFunc("DELETE /api/queues/{kind}/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeErr(w, fmt.Errorf("некорректный id"))
			return
		}
		switch r.PathValue("kind") {
		case "playerEdit":
			err = playerqueue.DeleteByID(cfg, id)
		case "structureEdit":
			err = structurequeue.DeleteByID(cfg, id)
		default:
			writeErr(w, fmt.Errorf("неизвестный тип заявки"))
			return
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
}

func collectQueueItems(cfg config.Config, mod *modclient.Client) []queueItem {
	playerNames := map[string]string{}
	for _, p := range mod.CachedPlayers() {
		if p.SteamId != "" {
			playerNames[p.SteamId] = p.Name
		}
	}
	structureNames := map[int]string{}
	for _, s := range structuresdb.CachedAll() {
		structureNames[s.ID] = s.Name
	}

	var out []queueItem

	if items, err := playerqueue.ListAllPending(cfg); err == nil {
		for _, it := range items {
			out = append(out, queueItem{
				Kind:       "playerEdit",
				ID:         it.ID,
				Cancelable: true,
				Label:      playerActionLabel(playerqueue.FriendlyGroupName(it.ModPath)),
				Target:     it.SteamID,
				TargetName: playerNames[it.SteamID],
				QueuedAt:   it.CreatedAt.UTC().Format(time.RFC3339),
				WaitingFor: "ждёт входа игрока",
			})
		}
	} else {
		log.Printf("queues: playerqueue: %v", err)
	}

	if items, err := structurequeue.ListAllPending(cfg); err == nil {
		for _, it := range items {
			out = append(out, queueItem{
				Kind:       "structureEdit",
				ID:         it.ID,
				Cancelable: true,
				Label:      structureActionLabel(it.ModPath),
				Target:     it.Playfield,
				TargetName: structureNames[it.StructureID],
				QueuedAt:   it.CreatedAt.UTC().Format(time.RFC3339),
				WaitingFor: "ждёт загрузки поля",
			})
		}
	} else {
		log.Printf("queues: structurequeue: %v", err)
	}

	if items, err := hardwipe.ListQueued(cfg); err == nil {
		for _, it := range items {
			out = append(out, queueItem{
				Kind:       "hardwipeIncoming",
				Label:      "Жёсткий вайп",
				Target:     it.SteamID,
				TargetName: playerNames[it.SteamID],
				QueuedAt:   it.QueuedAt.UTC().Format(time.RFC3339),
				WaitingFor: "ждёт ответа сервера",
			})
		}
	} else {
		log.Printf("queues: hardwipe (pending): %v", err)
	}

	if items, err := hardwipe.ListPendingDestroys(cfg); err == nil {
		for _, it := range items {
			out = append(out, queueItem{
				Kind:       "hardwipePendingDestroy",
				Label:      "Снос структур (жёсткий вайп)",
				Target:     it.Playfield,
				Detail:     joinIntsShort(it.IDs),
				QueuedAt:   it.QueuedAt.UTC().Format(time.RFC3339),
				WaitingFor: "ждёт загрузки поля",
			})
		}
	} else {
		log.Printf("queues: hardwipe (teardown): %v", err)
	}

	if items, err := scriptqueue.ListQueued(cfg); err == nil {
		for _, it := range items {
			out = append(out, queueItem{
				Kind:       "script",
				Label:      "Скрипт",
				Target:     it.SteamID,
				TargetName: firstNonEmpty(playerNames[it.SteamID], it.PlayerName),
				Detail:     it.ScriptName,
				QueuedAt:   it.QueuedAt.UTC().Format(time.RFC3339),
				WaitingFor: "ждёт ответа сервера",
			})
		}
	} else {
		log.Printf("queues: scriptqueue: %v", err)
	}

	if items, err := restartqueue.ListQueued(cfg); err == nil {
		for _, it := range items {
			out = append(out, queueItem{
				Kind:       "restart",
				Label:      restartActionLabel(it.Action),
				Target:     it.SteamID,
				TargetName: firstNonEmpty(playerNames[it.SteamID], it.PlayerName),
				QueuedAt:   it.QueuedAt.UTC().Format(time.RFC3339),
				WaitingFor: "ждёт ответа сервера",
			})
		}
	} else {
		log.Printf("queues: restartqueue: %v", err)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].QueuedAt < out[j].QueuedAt })
	return out
}

func playerActionLabel(group string) string {
	switch group {
	case "inventory":
		return "Инвентарь"
	case "setinfo":
		return "Статы/фракция"
	case "addcredits":
		return "Кредиты"
	case "teleport":
		return "Телепортация"
	default:
		return group
	}
}

func structureActionLabel(modPath string) string {
	switch modPath {
	case "/structures/touch":
		return "Защита от вайпа"
	case "/structures/repair":
		return "Восстановление"
	case "/structures/destroy":
		return "Снос"
	default:
		return modPath
	}
}

func restartActionLabel(action string) string {
	if action == "stop" {
		return "Остановка сервера"
	}
	return "Перезапуск сервера"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func joinIntsShort(ids []int) string {
	const max = 5
	s := ""
	for i, id := range ids {
		if i >= max {
			s += ", …"
			break
		}
		if i > 0 {
			s += ", "
		}
		s += strconv.Itoa(id)
	}
	return s
}
