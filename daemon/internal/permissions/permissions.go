package permissions

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/safego"
	"elb/daemon/internal/settings"
	"elb/daemon/internal/telnet"
	"elb/daemon/internal/watchdog"
)

const telnetTimeout = 15 * time.Second

type Info struct {
	Roles map[string]string

	Banned map[string]string
}

func Fetch(cfg config.Config) (Info, error) {
	addr, pwd, err := cfg.TelnetCreds()
	if err != nil {
		return Info{}, err
	}
	tn := telnet.New(addr, pwd)

	outs, err := tn.Commands([]string{"list permissions", "list banned"}, telnetTimeout)
	if err != nil {
		return Info{}, err
	}
	return Info{Roles: parseRoles(outs[0]), Banned: parseBanned(outs[1])}, nil
}

var roleHeaderRe = regexp.MustCompile(`^Players with '(\w+)' permission:`)

func parseRoles(text string) map[string]string {
	roles := map[string]string{}
	role := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if m := roleHeaderRe.FindStringSubmatch(line); m != nil {
			role = strings.ToLower(m[1])
			continue
		}
		if line == "" || role == "" || strings.HasPrefix(line, "Thread ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		roles[fields[0]] = role
	}
	return roles
}

var bannedLineRe = regexp.MustCompile(`^(\S+)\s*-\s*(.+)$`)

func parseBanned(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, "Currently banned") || strings.HasPrefix(line, "Id ") || strings.HasPrefix(line, "Thread ") {
			continue
		}
		if m := bannedLineRe.FindStringSubmatch(line); m != nil {
			out[m[1]] = strings.TrimSpace(m[2])
		}
	}
	return out
}

var (
	cacheMu   sync.Mutex
	cacheAt   time.Time
	cacheData Info
)

func FetchCached(cfg config.Config, ttl time.Duration) (Info, error) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cacheData.Roles != nil && time.Since(cacheAt) < ttl {
		return cacheData, nil
	}
	info, err := Fetch(cfg)
	if err != nil {
		return Info{}, err
	}
	cacheData = info
	cacheAt = time.Now()
	return info, nil
}

const pollInterval = 15 * time.Second

var (
	bgCacheMu sync.RWMutex
	bgCache   Info // нулевое значение (nil-карты) до первого успешного опроса
)

func Cached() Info {
	bgCacheMu.RLock()
	defer bgCacheMu.RUnlock()
	return bgCache
}

func Refresh(cfg config.Config) (Info, error) {
	info, err := Fetch(cfg)
	if err != nil {
		return Info{}, err
	}
	bgCacheMu.Lock()
	bgCache = info
	bgCacheMu.Unlock()
	return info, nil
}

func RunPoller(cfg config.Config, mod *modclient.Client) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if !watchdog.CachedHealth().Ready() {
			<-ticker.C
			continue
		}
		safego.Tick("permissions.RunPoller", func() {
			info, err := Fetch(cfg)
			if err != nil {
				log.Printf("permissions: poll: %v", err)
			} else {
				info = ensureAdminPlayersElevated(cfg, info, mod)
				info = ensureModeratorPlayersElevated(cfg, info, mod)
				bgCacheMu.Lock()
				bgCache = info
				bgCacheMu.Unlock()
			}
		})
		<-ticker.C
	}
}

func onlineSteamIDs(mod *modclient.Client) map[string]bool {
	out := map[string]bool{}
	for _, p := range mod.CachedPlayers() {
		if p.Online {
			out[p.SteamId] = true
		}
	}
	return out
}

func filterOnline(ids []string, online map[string]bool) []string {
	var out []string
	for _, id := range ids {
		if online[id] {
			out = append(out, id)
		}
	}
	return out
}

func idsToElevate(ids []string, roles map[string]string, role string) []string {
	var out []string
	for _, id := range ids {
		if roles[id] != role {
			out = append(out, id)
		}
	}
	return out
}

const elevateRetryBackoff = 5 * time.Minute

var (
	adminElevateMu      sync.Mutex
	adminElevateAttempt = map[string]time.Time{}

	// moderatorElevateMu/moderatorElevateAttempt — 2026-09-30, прямая
	// просьба пользователя: "Игроки-модераторы" держатся на игровой роли
	// moderator тем же способом, что уже держит "Игроки-админы" на admin
	// (см. ensureRoleElevated ниже — общее ядро для обеих ролей, вынесенное
	// из бывшей ensureAdminPlayersElevated). Отдельная от admin карта
	// попыток — backoff считается независимо по ролям, иначе одновременный
	// "непокорный" ID в обоих списках делил бы один и тот же таймер не по
	// делу (на практике так не бывает — см. ensureModeratorPlayersElevated,
	// admin исключается из целей elevate к moderator — но карта всё равно
	// своя, чтобы не завязывать backoff двух независимых ролей друг на друга).
	moderatorElevateMu      sync.Mutex
	moderatorElevateAttempt = map[string]time.Time{}
)

func ensureAdminPlayersElevated(cfg config.Config, info Info, mod *modclient.Client) Info {
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		log.Printf("permissions: ensureAdminPlayersElevated: settings: %v", err)
		return info
	}
	targets := filterOnline(s.Chatbot.AdminSteamIds, onlineSteamIDs(mod))
	return ensureRoleElevated(cfg, info, targets, "admin", "admin", &adminElevateMu, adminElevateAttempt)
}

func ensureModeratorPlayersElevated(cfg config.Config, info Info, mod *modclient.Client) Info {
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		log.Printf("permissions: ensureModeratorPlayersElevated: settings: %v", err)
		return info
	}
	targets := filterOnline(moderatorElevateTargets(s.Chatbot.AdminSteamIds, s.Chatbot.ModeratorSteamIds), onlineSteamIDs(mod))

	return ensureRoleElevated(cfg, info, targets, "moderator", "mod", &moderatorElevateMu, moderatorElevateAttempt)
}

func moderatorElevateTargets(adminIDs, moderatorIDs []string) []string {
	adminSet := make(map[string]bool, len(adminIDs))
	for _, id := range adminIDs {
		adminSet[id] = true
	}
	var out []string
	for _, id := range moderatorIDs {
		if !adminSet[id] {
			out = append(out, id)
		}
	}
	return out
}

func ensureRoleElevated(cfg config.Config, info Info, targetIDs []string, stateRole, commandRole string, mu *sync.Mutex, lastAttempt map[string]time.Time) Info {
	pending := idsToElevate(targetIDs, info.Roles, stateRole)
	if len(pending) == 0 {
		return info
	}

	now := time.Now()
	mu.Lock()

	targetSet := make(map[string]bool, len(targetIDs))
	for _, id := range targetIDs {
		targetSet[id] = true
	}
	for id := range lastAttempt {
		if !targetSet[id] {
			delete(lastAttempt, id)
		}
	}
	var toElevate []string
	for _, id := range pending {
		if last, ok := lastAttempt[id]; !ok || now.Sub(last) >= elevateRetryBackoff {
			toElevate = append(toElevate, id)
			lastAttempt[id] = now
		}
	}
	mu.Unlock()
	if len(toElevate) == 0 {

		return info
	}

	addr, pwd, err := cfg.TelnetCreds()
	if err != nil {
		log.Printf("permissions: ensureRoleElevated(%s): telnet creds: %v", stateRole, err)
		return info
	}
	cmds := make([]string, len(toElevate))
	for i, id := range toElevate {
		cmds[i] = fmt.Sprintf("setrole %s %s", id, commandRole)
	}
	if _, err := telnet.New(addr, pwd).Commands(cmds, telnetTimeout); err != nil {
		log.Printf("permissions: ensureRoleElevated(%s): setrole batch: %v", stateRole, err)
		return info
	}

	refreshed, err := Fetch(cfg)
	if err != nil {
		log.Printf("permissions: ensureRoleElevated(%s): verify refetch: %v", stateRole, err)
		return info
	}
	stillPending := idsToElevate(toElevate, refreshed.Roles, stateRole)
	applied := len(toElevate) - len(stillPending)
	if applied > 0 {
		log.Printf("permissions: ensureRoleElevated(%s): elevated %d player(s) to %s", stateRole, applied, stateRole)
	}
	if len(stillPending) > 0 {
		log.Printf("permissions: ensureRoleElevated(%s): %d player(s) still not %s after setrole (likely never connected yet) — will retry next tick: %v", stateRole, len(stillPending), stateRole, stillPending)
	}
	return refreshed
}
