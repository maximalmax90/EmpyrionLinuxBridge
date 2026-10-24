package presencenotify

import (
	"fmt"
	"log"
	"sync"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/safego"
	"elb/daemon/internal/settings"
	"elb/daemon/internal/watchdog"
)

const pollInterval = 5 * time.Second

var (
	stateMu sync.Mutex
	// inGame — тот же смысл, что internal/welcome.inGame (steamId -> был ли
	// "в игре" на прошлом тике), но своя копия состояния — независимый
	// пакет, не переиспользует чужую переменную (разная цель отслеживания:
	// welcome нужны только входы, этому пакету — оба направления).
	inGame = map[string]bool{}
	primed bool
)

func RunPoller(cfg config.Config, mod *modclient.Client) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if !watchdog.CachedHealth().Ready() {
			<-ticker.C
			continue
		}
		safego.Tick("presencenotify.RunPoller", func() {
			tick(cfg, mod)
		})
		<-ticker.C
	}
}

func tick(cfg config.Config, mod *modclient.Client) {
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		log.Printf("presencenotify: settings: %v", err)
		return
	}
	if !s.Chatbot.NotifyPlayerPresence {
		return
	}
	players := mod.CachedPlayers()

	stateMu.Lock()
	joined, left, next, first := detectTransitions(inGame, players, primed)
	inGame = next
	primed = true
	stateMu.Unlock()

	if first || (len(joined) == 0 && len(left) == 0) {
		return
	}

	recipients := recipientSteamIds(s)
	if len(recipients) == 0 {
		return
	}
	senderName := s.Chatbot.ServerSenderName
	if senderName == "" {
		senderName = "Сервер"
	}

	for _, p := range joined {
		notify(mod, players, recipients, senderName, p, true)
	}
	for _, p := range left {
		notify(mod, players, recipients, senderName, p, false)
	}
}

func detectTransitions(prev map[string]bool, players []modclient.Player, primed bool) (joined, left []modclient.Player, next map[string]bool, wasFirst bool) {
	next = make(map[string]bool, len(players))
	for _, p := range players {
		if p.SteamId == "" {
			continue
		}
		nowInGame := p.Online && p.Playfield != ""
		if primed {
			was := prev[p.SteamId]
			switch {
			case nowInGame && !was:
				joined = append(joined, p)
			case !nowInGame && was:
				left = append(left, p)
			}
		}
		next[p.SteamId] = nowInGame
	}
	return joined, left, next, !primed
}

func recipientSteamIds(s settings.Config) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range append(append([]string{}, s.Chatbot.AdminSteamIds...), s.Chatbot.ModeratorSteamIds...) {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func notify(mod *modclient.Client, players []modclient.Player, recipients []string, senderName string, subject modclient.Player, joined bool) {
	text := formatMessage(subject, joined)
	for _, recipientID := range recipients {
		if recipientID == subject.SteamId {
			continue
		}
		recipient, ok := findOnlinePlayer(players, recipientID)
		if !ok {
			continue
		}
		if _, err := mod.PostMessageWithRetry("/chat", map[string]any{"text": text, "recipientSteamId": recipient.SteamId, "asSteamId": "__server__", "senderName": senderName}); err != nil {
			log.Printf("presencenotify: send to %s (%s): %v", recipient.Name, recipient.SteamId, err)
		}
	}
}

func formatMessage(p modclient.Player, joined bool) string {
	status := "оффлайн"
	statusColor := "ff3333"
	if joined {
		status = "онлайн"
		statusColor = "33cc33"
	}
	return fmt.Sprintf("[c][ffffff]Игрок [b][ffcc00]%s[/b][ffffff] [%s]%s[/c]", p.Name, statusColor, status)
}

func findOnlinePlayer(players []modclient.Player, steamID string) (modclient.Player, bool) {
	for _, p := range players {
		if p.SteamId == steamID && p.Online {
			return p, true
		}
	}
	return modclient.Player{}, false
}
