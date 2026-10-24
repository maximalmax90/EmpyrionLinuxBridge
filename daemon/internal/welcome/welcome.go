package welcome

import (
	"log"
	"math/rand"
	"sync"
	"time"

	"elb/daemon/internal/chatlog"
	"elb/daemon/internal/config"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/safego"
	"elb/daemon/internal/settings"
	"elb/daemon/internal/watchdog"
)

const pollInterval = 5 * time.Second

const (
	welcomeDelayMin = 10 * time.Second
	welcomeDelayMax = 15 * time.Second
)

var (
	stateMu sync.Mutex
	// inGame — steamId -> был ли игрок "в игре" (online && playfield != "")
	// на ПРЕДЫДУЩЕМ тике. Отсутствие в карте = ещё не видели вообще.
	inGame = map[string]bool{}
	// primed — false до первого тика: на первом тике только заполняем карту
	// текущим состоянием, приветствия НЕ шлём — иначе каждый рестарт демона
	// (например, после деплоя) приветствовал бы заново ВСЕХ уже играющих
	// игроков, застав их уже "в игре" без видимого перехода. Тот же приём,
	// что IsFirst у internal/history.Tick (см. её package doc).
	primed bool
	// pending — steamId -> оставшиеся неотправленные сообщения ТЕКУЩЕЙ
	// серии приветствия этого игрока (см. settings.WelcomeMessage.Messages,
	// до 5 штук). Заполняется ЦЕЛИКОМ в момент обнаружения перехода в игру
	// (новый вход — новая серия, старая отбрасывается, см. tick ниже),
	// разбирается на каждом тике отдельно от detectJoins — если игрок успел
	// выйти ДО наступления времени конкретного сообщения, это сообщение
	// просто не шлётся (см. duePending).
	pending = map[string][]pendingSend{}
)

type pendingSend struct {
	at  time.Time
	msg settings.WelcomeMessageItem
}

func RunPoller(cfg config.Config, mod *modclient.Client) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if !watchdog.CachedHealth().Ready() {
			<-ticker.C
			continue
		}
		safego.Tick("welcome.RunPoller", func() {
			tick(cfg, mod)
		})
		<-ticker.C
	}
}

func tick(cfg config.Config, mod *modclient.Client) {
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		log.Printf("welcome: settings: %v", err)
		return
	}
	wm := s.Chatbot.WelcomeMessage
	if !wm.Enabled || len(wm.Messages) == 0 {
		return
	}
	players := mod.CachedPlayers()
	now := time.Now()

	stateMu.Lock()
	toWelcome, next, first := detectJoins(inGame, players, primed)
	inGame = next
	primed = true
	for _, p := range toWelcome {
		base := now.Add(welcomeDelayMin + time.Duration(rand.Int63n(int64(welcomeDelayMax-welcomeDelayMin))))
		entries := make([]pendingSend, 0, len(wm.Messages))
		for _, m := range wm.Messages {
			entries = append(entries, pendingSend{at: base.Add(time.Duration(m.DelaySeconds) * time.Second), msg: m})
		}
		pending[p.SteamId] = entries
	}
	due := duePending(pending, now)
	stateMu.Unlock()

	if first || len(due) == 0 {
		return
	}
	senderName := s.Chatbot.ServerSenderName
	if senderName == "" {
		senderName = "Сервер"
	}
	for _, d := range due {
		p, ok := findPlayer(players, d.steamID)
		if !ok || !(p.Online && p.Playfield != "") {

			continue
		}
		send(cfg, mod, d.msg, senderName, p)
	}
}

type dueSend struct {
	steamID string
	msg     settings.WelcomeMessageItem
}

func duePending(pending map[string][]pendingSend, now time.Time) []dueSend {
	var due []dueSend
	for steamID, entries := range pending {
		var remaining []pendingSend
		for _, e := range entries {
			if !now.Before(e.at) {
				due = append(due, dueSend{steamID: steamID, msg: e.msg})
			} else {
				remaining = append(remaining, e)
			}
		}
		if len(remaining) == 0 {
			delete(pending, steamID)
		} else {
			pending[steamID] = remaining
		}
	}
	return due
}

func findPlayer(players []modclient.Player, steamID string) (modclient.Player, bool) {
	for _, p := range players {
		if p.SteamId == steamID {
			return p, true
		}
	}
	return modclient.Player{}, false
}

func detectJoins(prev map[string]bool, players []modclient.Player, primed bool) (toWelcome []modclient.Player, next map[string]bool, wasFirst bool) {
	next = make(map[string]bool, len(players))
	for _, p := range players {
		if p.SteamId == "" {
			continue
		}
		nowInGame := p.Online && p.Playfield != ""
		if nowInGame && !prev[p.SteamId] && primed {
			toWelcome = append(toWelcome, p)
		}
		next[p.SteamId] = nowInGame
	}
	return toWelcome, next, !primed
}

func send(cfg config.Config, mod *modclient.Client, msg settings.WelcomeMessageItem, senderName string, p modclient.Player) {
	var err error
	if msg.ActionType == settings.ActionBanner {
		_, err = mod.PostMessageWithRetry("/message/player", map[string]any{"playerId": p.EntityId, "text": msg.Text, "prio": msg.BannerPriority})
		if err == nil {

			if logErr := chatlog.LogBanner(cfg, senderName, msg.Text, p.Name, chatlog.PriorityName(msg.BannerPriority)); logErr != nil {
				log.Printf("welcome: log banner: %v", logErr)
			}
		}
	} else {

		_, err = mod.PostMessageWithRetry("/chat", map[string]any{"text": msg.Text, "recipientSteamId": p.SteamId, "asSteamId": "__server__", "senderName": senderName})
	}
	if err != nil {
		log.Printf("welcome: send to %s (%s): %v", p.Name, p.SteamId, err)
		return
	}
	log.Printf("welcome: sent to %s (%s)", p.Name, p.SteamId)
}
