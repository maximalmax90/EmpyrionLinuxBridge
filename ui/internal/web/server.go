package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"elb/ui/internal/daemonclient"
	"elb/ui/internal/hub"
	"elb/ui/internal/i18n"
	"elb/ui/internal/iconsync"
	"elb/ui/internal/logbuf"
	"elb/ui/internal/store"
)

type serverStateInfo struct {
	Class string
	Label string
}

func serverStateL(lang string, h daemonclient.Health, manualType string, daemonUnreachable bool) serverStateInfo {
	switch {
	case daemonUnreachable:
		return serverStateInfo{"state-unreachable", i18n.T(lang, "state.daemonUnreachable")}

	case h.ScenarioCache.InProgress():
		return serverStateInfo{"state-caching", i18n.T(lang, "state.cachingScenario")}
	case manualType == "stop":
		return serverStateInfo{"state-stopping", i18n.T(lang, "state.stopping")}
	case manualType == "restart":
		return serverStateInfo{"state-restarting", i18n.T(lang, "state.restarting")}
	case h.Hung:
		return serverStateInfo{"state-hung", i18n.T(lang, "state.hung")}
	case h.Active && h.TelnetOK:
		return serverStateInfo{"state-online", i18n.T(lang, "state.online")}
	case h.Active && !h.TelnetOK:
		return serverStateInfo{"state-starting", i18n.T(lang, "state.starting")}
	default:
		return serverStateInfo{"state-stopped", i18n.T(lang, "state.stopped")}
	}
}

func channelLabelL(lang, ch string) string {
	key := "channel." + ch
	if v := i18n.T(lang, key); v != key {
		return v
	}
	return ch
}

func chatBannerPriorityLabelL(lang, priority string) string {
	key := "composer.priority." + priority
	if v := i18n.T(lang, key); v != key {
		return v
	}
	return priority
}

func t(key string) string { return i18n.T(i18n.Default, key) }

func availableLocales() []string { return i18n.Languages() }

func isSteamId64(id string) bool {
	if len(id) != 17 {
		return false
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func gb(bytes uint64) string {
	return strconv.FormatFloat(float64(bytes)/(1024*1024*1024), 'f', 1, 64)
}

func humanSize(bytes int64) string {
	f := float64(bytes)
	switch {
	case bytes >= 1<<30:
		return strconv.FormatFloat(f/(1<<30), 'f', 1, 64) + " GB"
	case bytes >= 1<<20:
		return strconv.FormatFloat(f/(1<<20), 'f', 1, 64) + " MB"
	case bytes >= 1<<10:
		return strconv.FormatFloat(f/(1<<10), 'f', 0, 64) + " KB"
	default:
		return strconv.FormatInt(bytes, 10) + " B"
	}
}

func pctOf(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	p := float64(used) / float64(total) * 100
	if p > 100 {
		p = 100
	}
	return p
}

func intPct(used, total int) float64 {
	if total <= 0 {
		return 0
	}
	p := float64(used) / float64(total) * 100
	if p > 100 {
		p = 100
	}
	return p
}

const UIVersion = "0.1.44"

const rulesDefaultText = "<color=yellow>Правила сервера</color>\n\n<color=red>Запрещено:</color>\nЧитерство\nИспользование багов и эксплойтов.\nСпам в чате.\nМат, оскорбления, расизм в чате, никах, названии построек и фракции.\n\nНарушение правил - от предупреждения до перманентного бана.\nУважайте себя и других игроков.\n\n<color=green>Разрешено:</color>\nПолучать удовольствие и наслаждаться игрой!"

func gaugeClass(percent float64, warnPct, critPct int) string {
	switch {
	case percent >= float64(critPct):
		return "gauge-danger"
	case percent >= float64(warnPct):
		return "gauge-warn"
	default:
		return "gauge-ok"
	}
}

//go:embed templates static
var embedded embed.FS

const sessionCookie = "elb_session"

var funcMap = template.FuncMap{

	"cmdEnabled": func(enabled map[string]bool, name string) bool {
		v, ok := enabled[name]
		return !ok || v
	},

	"logHangsEnabled": func(v *bool) bool { return v == nil || *v },

	"cmdDesc": func(desc map[string]string, name, def string) string {
		if v, ok := desc[name]; ok && v != "" {
			return v
		}
		return def
	},

	"rulesDefaultText": func() string { return rulesDefaultText },

	"minutesOf": func(seconds int) int { return seconds / 60 },

	"channelLabel":        func(ch string) string { return channelLabelL(i18n.Default, ch) },
	"bannerPriorityLabel": func(p string) string { return chatBannerPriorityLabelL(i18n.Default, p) },
	"serverState": func(h daemonclient.Health, manualType string, daemonUnreachable bool) serverStateInfo {
		return serverStateL(i18n.Default, h, manualType, daemonUnreachable)
	},

	"contains": func(list []string, s string) bool {
		for _, v := range list {
			if v == s {
				return true
			}
		}
		return false
	},

	"dict": func(pairs ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i+1 < len(pairs); i += 2 {
			if k, ok := pairs[i].(string); ok {
				m[k] = pairs[i+1]
			}
		}
		return m
	},
	"localeName": i18n.LanguageName,

	"countAdmins": func(users []store.User) int {
		n := 0
		for _, u := range users {
			if u.Role == store.RoleAdmin {
				n++
			}
		}
		return n
	},
	"gb": gb,

	"gbSize":    func(bytes int64) string { return gb(uint64(bytes)) },
	"humanSize": humanSize,

	"inc":        func(i int) int { return i + 1 },
	"pctOf":      pctOf,
	"intPct":     intPct,
	"gaugeClass": gaugeClass,
	"t":          t,

	"isSteamId64": isSteamId64,

	"chWidth": func(maxStr string, unitLen int) int {
		return 2*len(maxStr) + 1 + unitLen
	},

	"mountLabel": func(mountpoint string) string {
		trimmed := strings.TrimRight(mountpoint, "/")
		if trimmed == "" {
			return "/"
		}
		if idx := strings.LastIndex(trimmed, "/"); idx >= 0 {
			return trimmed[idx+1:]
		}
		return trimmed
	},

	"hoursOf": func(seconds float64) float64 { return seconds / 3600 },
	"onlineCount": func(players []daemonclient.Player) int {
		n := 0
		for _, p := range players {
			if p.Online {
				n++
			}
		}
		return n
	},

	"actionLabel":    func(actionType string) string { return actionLabelL(i18n.Default, actionType) },
	"join":           func(items []string, sep string) string { return strings.Join(items, sep) },
	"wipeTokenLabel": func(token string) string { return wipeTokenLabelL(i18n.Default, token) },

	"scheduleSummary": func(t daemonclient.Task) string { return scheduleSummaryL(i18n.Default, t, time.Local) },
	"taskDescription": func(actionType string, p daemonclient.TaskParams, playfieldSystems map[string]string) template.HTML {
		return taskDescriptionHTML(i18n.Default, actionType, p, playfieldSystems)
	},
	"subtaskDescription": func(s daemonclient.Subtask) string {
		return subtaskDescriptionL(i18n.Default, s, nil)
	},
}

func actionLabelL(lang, actionType string) string {
	key := "scheduler.actionType." + actionType
	if v := i18n.T(lang, key); v != key {
		return v
	}
	return actionType
}

func wipeTokenLabelL(lang, token string) string {
	key := "scheduler.wipeToken." + token
	if v := i18n.T(lang, key); v != key {
		return v
	}
	return token
}

func resetModeLabelL(lang, mode string) string {
	if mode == "" {
		mode = "reset"
	}
	key := "scheduler.resetMode." + mode
	if v := i18n.T(lang, key); v != key {
		return v
	}
	return mode
}

func bannerPriorityLabelL(lang string, prio int) string {
	key := "scheduler.form.bannerPriority.alarm"
	switch prio {
	case 2:
		key = "scheduler.form.bannerPriority.info"
	case 1:
		key = "scheduler.form.bannerPriority.message"
	}
	return i18n.T(lang, key)
}

func warnMinutesSuffixL(lang string, minutes int) string {
	if minutes <= 0 {
		return ""
	}
	return " — " + fmt.Sprintf(i18n.T(lang, "scheduler.warnMinutesSuffix"), minutes)
}

type playfieldGroupLine struct {
	System string
	Detail string
}

func groupPlayfieldsBySystem(playfields []string, systems map[string]string) []playfieldGroupLine {
	type baseGroup struct {
		base     string
		suffixes []string
	}
	systemOrder := []string{}
	baseOrderBySystem := map[string][]string{}
	basesBySystem := map[string]map[string]*baseGroup{}
	seenSystem := map[string]bool{}

	for _, p := range playfields {
		sys := systems[p]
		if !seenSystem[sys] {
			seenSystem[sys] = true
			systemOrder = append(systemOrder, sys)
			basesBySystem[sys] = map[string]*baseGroup{}
		}
		base, suffix := splitPlayfieldSuffix(p)
		g, ok := basesBySystem[sys][base]
		if !ok {
			g = &baseGroup{base: base}
			basesBySystem[sys][base] = g
			baseOrderBySystem[sys] = append(baseOrderBySystem[sys], base)
		}
		if suffix != "" {
			g.suffixes = append(g.suffixes, suffix)
		}
	}

	lines := make([]playfieldGroupLine, 0, len(systemOrder))
	for _, sys := range systemOrder {
		baseParts := make([]string, 0, len(baseOrderBySystem[sys]))
		for _, base := range baseOrderBySystem[sys] {
			g := basesBySystem[sys][base]
			if len(g.suffixes) == 0 {
				baseParts = append(baseParts, base)
			} else {
				baseParts = append(baseParts, base+" (+ "+strings.Join(g.suffixes, ", ")+")")
			}
		}
		lines = append(lines, playfieldGroupLine{System: sys, Detail: strings.Join(baseParts, ", ")})
	}
	return lines
}

func groupedPlayfieldsLabel(playfields []string, systems map[string]string) string {
	if len(playfields) == 0 {
		return ""
	}
	if len(systems) == 0 {
		return strings.Join(playfields, ", ")
	}
	lines := groupPlayfieldsBySystem(playfields, systems)
	out := make([]string, len(lines))
	for i, l := range lines {
		if l.System == "" {
			out[i] = l.Detail
		} else {
			out[i] = l.System + ": " + l.Detail
		}
	}
	return strings.Join(out, "\n")
}

func groupedPlayfieldsLabelHTML(playfields []string, systems map[string]string) template.HTML {
	if len(playfields) == 0 {
		return ""
	}
	if len(systems) == 0 {
		return htmlEscape(strings.Join(playfields, ", "))
	}
	lines := groupPlayfieldsBySystem(playfields, systems)
	out := make([]string, len(lines))
	for i, l := range lines {
		detail := template.HTMLEscapeString(l.Detail)
		if l.System == "" {
			out[i] = detail
		} else {
			out[i] = "<strong>" + template.HTMLEscapeString(l.System) + "</strong>: " + detail
		}
	}
	return template.HTML(strings.Join(out, "\n"))
}

func htmlEscape(s string) template.HTML { return template.HTML(template.HTMLEscapeString(s)) }

var sectorSuffixRe = regexp.MustCompile(`^(.+) Sector$`)
var moonSuffixRe = regexp.MustCompile(`^(.+) Moon( \d+)?$`)

func splitPlayfieldSuffix(name string) (base, suffix string) {
	rest := name
	sectorPart := ""
	if m := sectorSuffixRe.FindStringSubmatch(rest); m != nil {
		rest = m[1]
		sectorPart = "Sector"
	}
	moonPart := ""
	if m := moonSuffixRe.FindStringSubmatch(rest); m != nil {
		rest = m[1]
		moonPart = "Moon" + m[2]
	}
	var parts []string
	if moonPart != "" {
		parts = append(parts, moonPart)
	}
	if sectorPart != "" {
		parts = append(parts, sectorPart)
	}
	return rest, strings.Join(parts, " ")
}

func taskDescriptionL(lang, actionType string, p daemonclient.TaskParams, playfieldSystems map[string]string) string {
	label := actionLabelL(lang, actionType)
	switch actionType {
	case "broadcast_chat":
		if p.Text == "" {
			break
		}
		target := channelLabelL(lang, "server")
		if p.RecipientName != "" {
			target = p.RecipientName
		} else if p.Channel != "" {
			target = channelLabelL(lang, p.Channel)
		}
		return label + " (" + target + "): " + p.Text
	case "console_command":
		if p.Command != "" {
			return label + ": " + p.Command
		}
	case "backup":
		text := label
		if p.SnapshotName != "" {
			text += ": " + p.SnapshotName
		}
		if p.LowPriority {
			text += " (" + i18n.T(lang, "scheduler.form.lowPriority") + ")"
		}
		return text + warnMinutesSuffixL(lang, p.WarnMinutes)
	case "restart_server", "stop_server":
		text := label
		if p.MakeSnapshot {
			text += " + " + i18n.T(lang, "scheduler.label.snapshot")
		}

		if p.BackupEnabled {
			if p.BackupDeferred && p.BackupDeferredTime != "" {
				text += fmt.Sprintf(" + %s (%s)", i18n.T(lang, "scheduler.actionType.backup"), p.BackupDeferredTime)
			} else {
				text += " + " + i18n.T(lang, "scheduler.actionType.backup")
			}
		}
		return text + warnMinutesSuffixL(lang, p.WarnMinutes)
	case "wipe_playfield":
		tokens := make([]string, len(p.WipeTokens))
		for i, t := range p.WipeTokens {
			tokens[i] = wipeTokenLabelL(lang, t)
		}
		return label + ": " + strings.Join(tokens, ", ") + " —\n" + groupedPlayfieldsLabel(p.WipePlayfields, playfieldSystems)
	case "reset_playfield":
		sep := ":\n"
		targets := groupedPlayfieldsLabel(p.WipePlayfields, playfieldSystems)
		if p.ResetMode == "recreateDefect" && p.ResetScanAllDefect {
			sep = ": "
			targets = i18n.T(lang, "scheduler.resetMode.scanAll")
		}
		return label + " (" + resetModeLabelL(lang, p.ResetMode) + ")" + sep + targets
	case "wipe_old_unused_playfields":
		days := p.Days
		if days <= 0 {
			days = 14
		}
		return fmt.Sprintf("%s (%d %s)", label, days, i18n.T(lang, "scheduler.days"))
	case "banner":
		if p.BannerText == "" {
			break
		}
		target := i18n.T(lang, "scheduler.banner.all")
		if p.BannerRecipientName != "" {
			target = p.BannerRecipientName
		}
		return label + " (" + target + ", " + bannerPriorityLabelL(lang, p.BannerPriority) + "): " + p.BannerText
	case "run_script":
		if p.ScriptName == "" {
			break
		}
		return label + " (" + p.ScriptName + " → " + scriptOutputModeLabelL(lang, p.ScriptOutputMode) + ")"
	}
	return label
}

func taskDescriptionHTML(lang, actionType string, p daemonclient.TaskParams, playfieldSystems map[string]string) template.HTML {
	switch actionType {
	case "wipe_playfield":
		label := actionLabelL(lang, actionType)
		tokens := make([]string, len(p.WipeTokens))
		for i, t := range p.WipeTokens {
			tokens[i] = wipeTokenLabelL(lang, t)
		}
		prefix := htmlEscape(label + ": " + strings.Join(tokens, ", ") + " —\n")
		return prefix + groupedPlayfieldsLabelHTML(p.WipePlayfields, playfieldSystems)
	case "reset_playfield":
		if p.ResetMode == "recreateDefect" && p.ResetScanAllDefect {
			return htmlEscape(taskDescriptionL(lang, actionType, p, playfieldSystems))
		}
		label := actionLabelL(lang, actionType)
		prefix := htmlEscape(label + " (" + resetModeLabelL(lang, p.ResetMode) + "):\n")
		return prefix + groupedPlayfieldsLabelHTML(p.WipePlayfields, playfieldSystems)
	default:
		return htmlEscape(taskDescriptionL(lang, actionType, p, playfieldSystems))
	}
}

func scriptOutputModeLabelL(lang, mode string) string {
	key := "scheduler.form.scriptOutputMode.log"
	switch mode {
	case "banner":
		key = "scheduler.form.scriptOutputMode.banner"
	case "chat":
		key = "scheduler.form.scriptOutputMode.chat"
	}
	return i18n.T(lang, key)
}

func subtaskOffsetLabelL(lang string, s daemonclient.Subtask) string {
	sign := "+"
	minutes := s.OffsetMinutes
	if minutes < 0 {
		sign = "-"
		minutes = -minutes
	}
	offset := fmt.Sprintf("%s%d %s", sign, minutes, i18n.T(lang, "scheduler.subtask.minutes"))
	if s.RunCondition == "on_success" {
		offset += " (" + i18n.T(lang, "scheduler.subtask.onSuccess") + ")"
	} else if s.RunCondition == "on_failure" {
		offset += " (" + i18n.T(lang, "scheduler.subtask.onFailure") + ")"
	}
	return offset
}

func subtaskDescriptionL(lang string, s daemonclient.Subtask, playfieldSystems map[string]string) string {
	return subtaskOffsetLabelL(lang, s) + ": " + taskDescriptionL(lang, s.ActionType, s.Params, playfieldSystems)
}

func subtaskDescriptionHTML(lang string, s daemonclient.Subtask, playfieldSystems map[string]string) template.HTML {
	return htmlEscape(subtaskOffsetLabelL(lang, s)+": ") + taskDescriptionHTML(lang, s.ActionType, s.Params, playfieldSystems)
}

func weekdayNameL(lang string, wd int) string {
	if wd < 0 || wd > 6 {
		wd = 0
	}
	return i18n.T(lang, fmt.Sprintf("scheduler.weekday.%d", wd))
}

func scheduleSummaryL(lang string, t daemonclient.Task, loc *time.Location) string {
	switch t.Repeat {
	case "manual":
		return i18n.T(lang, "scheduler.schedule.manual")
	case "interval":
		return fmt.Sprintf(i18n.T(lang, "scheduler.schedule.everyNMin"), t.IntervalMinutes)
	case "daily":
		return fmt.Sprintf(i18n.T(lang, "scheduler.schedule.dailyAt"), t.TimeOfDay)
	case "weekday":
		return weekdayNameL(lang, t.Weekday) + ", " + t.TimeOfDay
	case "monthly":
		return fmt.Sprintf(i18n.T(lang, "scheduler.schedule.monthlyAt"), t.MonthDay, t.TimeOfDay)

	case "interval_weekday":
		return fmt.Sprintf(i18n.T(lang, "scheduler.schedule.everyNMinWeekday"), t.IntervalMinutes, weekdayNameL(lang, t.Weekday))
	case "interval_monthday":
		return fmt.Sprintf(i18n.T(lang, "scheduler.schedule.everyNMinMonthDay"), t.IntervalMinutes, t.MonthDay)
	case "once":
		tm, err := time.Parse(time.RFC3339, t.OnceAt)
		if err != nil {
			return i18n.T(lang, "scheduler.schedule.once")
		}
		return tm.In(loc).Format("02.01.2006 15:04")
	default:
		return t.Repeat
	}
}

type Server struct {
	store    *store.Store
	daemon   *daemonclient.Client
	hub      *hub.Hub
	tmpl     *template.Template
	byLocale map[string]*template.Template
	iconSync *iconsync.Syncer
	uiLog    *logbuf.Buffer

	trustProxyHeaders bool

	scenarioCacheLimiter *ipRateLimiter

	scenarioCachePanelListenAddr string

	listenPort int

	daemonLoc atomic.Pointer[time.Location]

	senders atomic.Pointer[senderInfo]
}

type senderInfo struct {
	roles map[string]string
	vip   map[string]bool
}

func (s *Server) senderClass(steamId string) string {
	if steamId == "" {
		return "server"
	}
	info := s.senders.Load()
	if info == nil {
		return ""
	}
	if info.roles[steamId] == "admin" {
		return "admin"
	}
	if info.vip[steamId] {
		return "vip"
	}
	return ""
}

func (s *Server) senderClassMap() map[string]string {
	out := map[string]string{}
	info := s.senders.Load()
	if info == nil {
		return out
	}
	for steamId, role := range info.roles {
		if role == "admin" {
			out[steamId] = "admin"
		}
	}
	for steamId := range info.vip {
		if _, has := out[steamId]; !has {
			out[steamId] = "vip"
		}
	}
	return out
}

func (s *Server) refreshSenderInfo() {
	info := &senderInfo{roles: map[string]string{}, vip: map[string]bool{}}
	if players, err := s.daemon.Players(); err != nil {
		log.Printf("senderInfo: players: %v", err)
	} else {
		for _, p := range players {
			if p.Role != "" {
				info.roles[p.SteamId] = p.Role
			}
		}
	}
	if cfg, err := s.daemon.Config(); err != nil {
		log.Printf("senderInfo: config: %v", err)
	} else {
		for _, id := range vipListSplit(cfg.Server.PlayerLoginVipNames.Value) {
			info.vip[id] = true
		}
	}
	s.senders.Store(info)
}

func (s *Server) runSenderInfoRefresher() {
	for {
		time.Sleep(60 * time.Second)
		s.refreshSenderInfo()
	}
}

func (s *Server) daemonLocation() *time.Location {
	if loc := s.daemonLoc.Load(); loc != nil {
		return loc
	}
	return time.Local
}

func (s *Server) refreshDaemonLocation() {
	for {
		if loc, err := s.daemon.ServerLocation(); err != nil {
			log.Printf("daemonLocation: refresh failed (keeping previous value/panel local time): %v", err)
		} else {
			s.daemonLoc.Store(loc)
		}
		time.Sleep(15 * time.Minute)
	}
}

func New(st *store.Store, daemon *daemonclient.Client, h *hub.Hub, syncer *iconsync.Syncer, uiLog *logbuf.Buffer, trustProxyHeaders bool, scenarioCachePanelListenAddr string, listenPort int) (*Server, error) {

	srv := &Server{store: st, daemon: daemon, hub: h, iconSync: syncer, uiLog: uiLog, trustProxyHeaders: trustProxyHeaders,
		scenarioCacheLimiter: newIPRateLimiter(20, time.Minute), scenarioCachePanelListenAddr: scenarioCachePanelListenAddr, listenPort: listenPort}

	base, err := template.New("root").Funcs(funcMap).Funcs(template.FuncMap{
		"playerColumns": st.PlayerColumns,
		"nowLocal": func() string {
			if t, err := daemon.ServerTime(); err == nil {
				return t
			}
			return time.Now().Format("02.01.2006 15:04:05")
		},
		"dateOf": func(ts string) string {
			t, err := time.Parse(time.RFC3339Nano, ts)
			if err != nil {
				return ""
			}
			return t.In(srv.daemonLocation()).Format("02.01.2006")
		},
		"timeOf": func(ts string) string {
			t, err := time.Parse(time.RFC3339Nano, ts)
			if err != nil {
				return ""
			}
			return t.In(srv.daemonLocation()).Format("15:04:05")
		},

		"localTime": func(t time.Time, layout string) string {
			return t.In(srv.daemonLocation()).Format(layout)
		},
		"senderClass": srv.senderClass,
	}).ParseFS(embedded, "templates/*.html")
	if err != nil {
		return nil, err
	}

	byLocale := map[string]*template.Template{}
	for _, lang := range i18n.Languages() {
		clone, err := base.Clone()
		if err != nil {
			return nil, fmt.Errorf("clone templates for %q: %w", lang, err)
		}
		byLocale[lang] = clone.Funcs(template.FuncMap{
			"t":                   func(key string) string { return i18n.T(lang, key) },
			"channelLabel":        func(ch string) string { return channelLabelL(lang, ch) },
			"bannerPriorityLabel": func(p string) string { return chatBannerPriorityLabelL(lang, p) },
			"serverState": func(h daemonclient.Health, manualType string, daemonUnreachable bool) serverStateInfo {
				return serverStateL(lang, h, manualType, daemonUnreachable)
			},
			"actionLabel":    func(actionType string) string { return actionLabelL(lang, actionType) },
			"wipeTokenLabel": func(token string) string { return wipeTokenLabelL(lang, token) },
			"scheduleSummary": func(t daemonclient.Task) string {
				return scheduleSummaryL(lang, t, srv.daemonLocation())
			},
			"subtaskDescription": func(s daemonclient.Subtask) string {
				return subtaskDescriptionL(lang, s, nil)
			},
			"taskDescription": func(actionType string, p daemonclient.TaskParams, playfieldSystems map[string]string) template.HTML {
				return taskDescriptionHTML(lang, actionType, p, playfieldSystems)
			},
		})
	}

	srv.tmpl = byLocale[i18n.Default]
	srv.byLocale = byLocale
	go srv.refreshDaemonLocation()
	srv.refreshSenderInfo()
	go srv.runSenderInfoRefresher()
	return srv, nil
}

func (s *Server) Templates() *template.Template { return s.tmpl }

func (s *Server) TemplatesByLocale() map[string]*template.Template { return s.byLocale }

func (s *Server) tmplForLocale(lang string) *template.Template {
	if t, ok := s.byLocale[lang]; ok {
		return t
	}
	return s.tmpl
}

func (s *Server) execTemplate(w http.ResponseWriter, r *http.Request, name string, data any) error {
	lang := i18n.Default
	if u := userFromCtx(r); u != nil {
		lang = u.Locale
	}
	return s.tmplForLocale(lang).ExecuteTemplate(w, name, data)
}

func (s *Server) ScenarioCacheRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /scenario-cache/"+scenarioCacheZipName, s.handleScenarioCacheFile)
	return mux
}

func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()

	staticFS, _ := fs.Sub(embedded, "static")
	staticHandler := http.StripPrefix("/static/", http.FileServer(http.FS(staticFS)))
	mux.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		staticHandler.ServeHTTP(w, r)
	}))

	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLoginSubmit)
	mux.HandleFunc("GET /logout", s.handleLogout)

	if s.scenarioCachePanelListenAddr == "" {
		mux.HandleFunc("GET /scenario-cache/"+scenarioCacheZipName, s.handleScenarioCacheFile)
	}

	mux.Handle("GET /change-password", s.requireSession(http.HandlerFunc(s.handleChangePasswordPage)))
	mux.Handle("POST /change-password", s.requireSession(http.HandlerFunc(s.handleChangePasswordSubmit)))

	mux.Handle("GET /{$}", s.requireFullAccess(http.HandlerFunc(s.handleDashboard)))
	mux.Handle("GET /ws", s.requireFullAccess(http.HandlerFunc(s.handleWS)))
	mux.Handle("POST /action/settings/scenario-cache", s.requireFullAccess(http.HandlerFunc(s.handleActionSettingsScenarioCache)))
	mux.Handle("POST /action/scenario-cache/test", s.requireFullAccess(http.HandlerFunc(s.handleActionScenarioCacheTest)))
	mux.Handle("GET /icon/{name...}", s.requireFullAccess(http.HandlerFunc(s.handleIcon)))
	mux.Handle("GET /action/player", s.requireFullAccess(http.HandlerFunc(s.handleActionPlayer)))
	mux.Handle("GET /action/logs/daemon", s.requireFullAccess(http.HandlerFunc(s.handleActionLogsDaemon)))
	mux.Handle("GET /action/logs/webui", s.requireFullAccess(http.HandlerFunc(s.handleActionLogsWebui)))
	mux.Handle("GET /action/logs/server", s.requireFullAccess(http.HandlerFunc(s.handleActionLogsServer)))
	mux.Handle("GET /action/logs/scheduler", s.requireFullAccess(http.HandlerFunc(s.handleActionLogsScheduler)))
	mux.Handle("GET /action/logs/audit", s.requireFullAccess(http.HandlerFunc(s.handleActionLogsAudit)))
	mux.Handle("POST /action/kick", s.requireFullAccess(http.HandlerFunc(s.handleActionKick)))
	mux.Handle("POST /action/message", s.requireFullAccess(http.HandlerFunc(s.handleActionMessage)))

	mux.Handle("POST /action/player/setinfo", s.requireFullAccess(http.HandlerFunc(s.handleActionPlayerSetInfo)))
	mux.Handle("POST /action/player/setrole", s.requireFullAccess(http.HandlerFunc(s.handleActionPlayerSetRole)))
	mux.Handle("POST /action/player/ban", s.requireFullAccess(http.HandlerFunc(s.handleActionPlayerBan)))
	mux.Handle("POST /action/player/unban", s.requireFullAccess(http.HandlerFunc(s.handleActionPlayerUnban)))
	mux.Handle("POST /action/player/mute", s.requireFullAccess(http.HandlerFunc(s.handleActionPlayerMute)))
	mux.Handle("POST /action/player/unmute", s.requireFullAccess(http.HandlerFunc(s.handleActionPlayerUnmute)))
	mux.Handle("POST /action/player/addcredits", s.requireFullAccess(http.HandlerFunc(s.handleActionPlayerAddCredits)))
	mux.Handle("POST /action/player/factory-finish", s.requireFullAccess(http.HandlerFunc(s.handleActionPlayerFactoryFinish)))
	mux.Handle("POST /action/player/inventory", s.requireFullAccess(http.HandlerFunc(s.handleActionPlayerSetInventory)))
	mux.Handle("GET /action/player/backpack-history", s.requireFullAccess(http.HandlerFunc(s.handleActionPlayerBackpackHistory)))
	mux.Handle("POST /action/player/backpack-restore", s.requireFullAccess(http.HandlerFunc(s.handleActionPlayerBackpackRestore)))

	mux.Handle("POST /action/player/vip", s.requireAdmin(http.HandlerFunc(s.handleActionPlayerVip)))

	mux.Handle("GET /action/warp/targets", s.requireFullAccess(http.HandlerFunc(s.handleActionWarpTargets)))
	mux.Handle("POST /action/player/teleport", s.requireFullAccess(http.HandlerFunc(s.handleActionPlayerTeleport)))

	mux.Handle("POST /action/history/around", s.requireFullAccess(http.HandlerFunc(s.handleActionHistoryAround)))
	mux.Handle("GET /action/history/names", s.requireFullAccess(http.HandlerFunc(s.handleActionHistoryNames)))

	mux.Handle("POST /action/console", s.requireAdmin(http.HandlerFunc(s.handleActionConsole)))

	mux.Handle("POST /action/player/remoteex", s.requireAdmin(http.HandlerFunc(s.handleActionPlayerRemoteEx)))
	mux.Handle("GET /action/player/pda-chapters", s.requireAdmin(http.HandlerFunc(s.handleActionPdaChapters)))

	mux.Handle("POST /action/admin-players/add", s.requireAdmin(http.HandlerFunc(s.handleAdminPlayerAdd)))
	mux.Handle("POST /action/admin-players/remove", s.requireAdmin(http.HandlerFunc(s.handleAdminPlayerRemove)))

	mux.Handle("POST /action/moderator-players/add", s.requireAdmin(http.HandlerFunc(s.handleModeratorPlayerAdd)))
	mux.Handle("POST /action/moderator-players/remove", s.requireAdmin(http.HandlerFunc(s.handleModeratorPlayerRemove)))

	mux.Handle("GET /action/server-time", s.requireFullAccess(http.HandlerFunc(s.handleActionServerTime)))
	mux.Handle("GET /action/notifications", s.requireFullAccess(http.HandlerFunc(s.handleActionNotifications)))
	mux.Handle("GET /action/notifications/unread-count", s.requireFullAccess(http.HandlerFunc(s.handleActionNotificationsUnreadCount)))
	mux.Handle("POST /action/notifications/read-all", s.requireFullAccess(http.HandlerFunc(s.handleActionNotificationsReadAll)))
	mux.Handle("POST /action/notifications/clear", s.requireFullAccess(http.HandlerFunc(s.handleActionNotificationsClear)))
	mux.Handle("POST /action/notifications/{id}/read", s.requireFullAccess(http.HandlerFunc(s.handleActionNotificationRead)))

	mux.Handle("POST /action/profile/preferences", s.requireFullAccess(http.HandlerFunc(s.handleActionProfilePreferences)))

	mux.Handle("GET /scheduler", s.requireAdmin(http.HandlerFunc(s.handleSchedulerPage)))
	mux.Handle("POST /action/scheduler/enabled", s.requireAdmin(http.HandlerFunc(s.handleActionSchedulerToggle)))
	mux.Handle("POST /action/scheduler/tasks", s.requireAdmin(http.HandlerFunc(s.handleActionSchedulerCreate)))
	mux.Handle("PUT /action/scheduler/tasks/{id}", s.requireAdmin(http.HandlerFunc(s.handleActionSchedulerUpdate)))
	mux.Handle("DELETE /action/scheduler/tasks/{id}", s.requireAdmin(http.HandlerFunc(s.handleActionSchedulerDelete)))
	mux.Handle("POST /action/scheduler/tasks/{id}/run", s.requireAdmin(http.HandlerFunc(s.handleActionSchedulerRun)))
	mux.Handle("POST /action/scheduler/tasks/{id}/clone", s.requireAdmin(http.HandlerFunc(s.handleActionSchedulerClone)))
	mux.Handle("POST /action/scheduler/tasks/{id}/subtasks", s.requireAdmin(http.HandlerFunc(s.handleActionSchedulerSubtaskCreate)))
	mux.Handle("PUT /action/scheduler/subtasks/{id}", s.requireAdmin(http.HandlerFunc(s.handleActionSchedulerSubtaskUpdate)))
	mux.Handle("DELETE /action/scheduler/subtasks/{id}", s.requireAdmin(http.HandlerFunc(s.handleActionSchedulerSubtaskDelete)))
	mux.Handle("POST /action/scheduler/subtasks/{id}/run", s.requireAdmin(http.HandlerFunc(s.handleActionSchedulerSubtaskRun)))

	mux.Handle("GET /backups", s.requireAdmin(http.HandlerFunc(s.handleBackupsPage)))
	mux.Handle("GET /action/backups/tables", s.requireAdmin(http.HandlerFunc(s.handleActionBackupsTables)))
	mux.Handle("POST /action/backups/run", s.requireAdmin(http.HandlerFunc(s.handleActionBackupsRun)))
	mux.Handle("POST /action/backups/{name}/restore", s.requireAdmin(http.HandlerFunc(s.handleActionBackupsRestore)))

	mux.Handle("DELETE /action/backups", s.requireAdmin(http.HandlerFunc(s.handleActionBackupsDelete)))
	mux.Handle("GET /action/backups/download", s.requireAdmin(http.HandlerFunc(s.handleActionBackupsDownload)))
	mux.Handle("GET /action/backups/browse", s.requireAdmin(http.HandlerFunc(s.handleActionBackupsBrowse)))
	mux.Handle("POST /action/backups/restore-files", s.requireAdmin(http.HandlerFunc(s.handleActionBackupsRestoreFiles)))

	mux.Handle("GET /settings", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.ChatbotBasic || p.PanelBasic })(http.HandlerFunc(s.handleSettingsPage)))
	mux.Handle("POST /action/settings/server", s.requireAdmin(http.HandlerFunc(s.handleActionSettingsServer)))
	mux.Handle("POST /action/settings/gameoptions", s.requireAdmin(http.HandlerFunc(s.handleActionSettingsGameOptions)))
	mux.Handle("POST /action/settings/chatbot", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.ChatbotBasic })(http.HandlerFunc(s.handleActionSettingsChatbot)))
	mux.Handle("POST /action/settings/panel", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.PanelBasic })(http.HandlerFunc(s.handleActionSettingsPanel)))
	mux.Handle("POST /action/settings/backup", s.requireAdmin(http.HandlerFunc(s.handleActionSettingsBackup)))
	mux.Handle("POST /action/settings/backup/clear", s.requireAdmin(http.HandlerFunc(s.handleActionSettingsBackupClear)))
	mux.Handle("POST /action/settings/backup/test", s.requireAdmin(http.HandlerFunc(s.handleActionSettingsBackupTest)))

	mux.Handle("GET /queues", s.requireAdmin(http.HandlerFunc(s.handleQueuesPage)))
	mux.Handle("GET /monitoring", s.requireAdmin(http.HandlerFunc(s.handleMonitoringPage)))
	mux.Handle("GET /monitoring/data", s.requireAdmin(http.HandlerFunc(s.handleMonitoringData)))
	mux.Handle("POST /action/queues/{kind}/{id}/cancel", s.requireAdmin(http.HandlerFunc(s.handleActionQueuesCancel)))

	mux.Handle("GET /maintenance", s.requireAdmin(http.HandlerFunc(s.handleMaintenancePage)))
	mux.Handle("POST /action/server/start", s.requireAdmin(http.HandlerFunc(s.handleActionServerStart)))
	mux.Handle("POST /action/server/stop", s.requireAdmin(http.HandlerFunc(s.handleActionServerStop)))
	mux.Handle("POST /action/server/restart", s.requireAdmin(http.HandlerFunc(s.handleActionServerRestart)))

	mux.Handle("POST /action/maintenance/update", s.requireAdmin(http.HandlerFunc(s.handleActionMaintenanceUpdate)))
	mux.Handle("POST /action/maintenance/wipe-local-db", s.requireAdmin(http.HandlerFunc(s.handleActionMaintenanceWipeLocalDB)))
	mux.Handle("GET /action/maintenance/duplicate-players", s.requireAdmin(http.HandlerFunc(s.handleActionMaintenanceDuplicatePlayers)))
	mux.Handle("POST /action/maintenance/duplicate-players/cleanup", s.requireAdmin(http.HandlerFunc(s.handleActionMaintenanceCleanupDuplicatePlayers)))
	mux.Handle("GET /action/maintenance/steamjob", s.requireAdmin(http.HandlerFunc(s.handleActionSteamJobStatus)))
	mux.Handle("POST /action/maintenance/steam-login", s.requireAdmin(http.HandlerFunc(s.handleActionSteamLogin)))
	mux.Handle("POST /action/maintenance/steam-code", s.requireAdmin(http.HandlerFunc(s.handleActionSteamCode)))
	mux.Handle("POST /action/maintenance/steam-logout", s.requireAdmin(http.HandlerFunc(s.handleActionSteamLogout)))
	mux.Handle("GET /action/maintenance/steam-workshop-lookup", s.requireAdmin(http.HandlerFunc(s.handleActionSteamWorkshopLookup)))
	mux.Handle("POST /action/maintenance/steam-workshop-install", s.requireAdmin(http.HandlerFunc(s.handleActionSteamWorkshopInstall)))
	mux.Handle("GET /action/maintenance/scenarios-refresh", s.requireAdmin(http.HandlerFunc(s.handleActionScenariosRefresh)))
	mux.Handle("GET /action/maintenance/steam-workshop-installed", s.requireAdmin(http.HandlerFunc(s.handleActionSteamWorkshopInstalled)))
	mux.Handle("POST /action/maintenance/steam-workshop-check", s.requireAdmin(http.HandlerFunc(s.handleActionSteamWorkshopCheck)))
	mux.Handle("POST /action/maintenance/steam-workshop-remove", s.requireAdmin(http.HandlerFunc(s.handleActionSteamWorkshopRemove)))
	mux.Handle("GET /action/maintenance/steam-workshop-download", s.requireAdmin(http.HandlerFunc(s.handleActionSteamWorkshopDownload)))
	mux.Handle("GET /action/maintenance/server-update-check", s.requireAdmin(http.HandlerFunc(s.handleActionServerUpdateCheck)))
	mux.Handle("POST /action/maintenance/scenario", s.requireAdmin(http.HandlerFunc(s.handleActionMaintenanceScenario)))
	mux.Handle("GET /action/maintenance/files/{name...}", s.requireAdmin(http.HandlerFunc(s.handleActionMaintenanceFileRead)))
	mux.Handle("POST /action/maintenance/files/{name...}", s.requireAdmin(http.HandlerFunc(s.handleActionMaintenanceFileWrite)))
	mux.Handle("GET /action/maintenance/config-files", s.requireAdmin(http.HandlerFunc(s.handleActionConfigFiles)))

	mux.Handle("GET /structures", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.Structures })(http.HandlerFunc(s.handleStructuresPage)))
	mux.Handle("GET /history", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.History })(http.HandlerFunc(s.handleHistoryPage)))

	mux.Handle("GET /chat-history", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.ChatHistory })(http.HandlerFunc(s.handleChatHistoryPage)))
	mux.Handle("POST /action/chat/history", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.ChatHistory })(http.HandlerFunc(s.handleActionChatHistory)))
	mux.Handle("GET /action/chat/sender-classes", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.ChatHistory })(http.HandlerFunc(s.handleActionChatSenderClasses)))
	mux.Handle("GET /action/structures/fragment", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.Structures })(http.HandlerFunc(s.handleActionStructuresFragment)))
	mux.Handle("POST /action/structures/{id}/touch", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.Structures })(http.HandlerFunc(s.handleActionStructureTouch)))
	mux.Handle("POST /action/structures/{id}/repair", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.Structures })(http.HandlerFunc(s.handleActionStructureRepair)))
	mux.Handle("POST /action/structures/{id}/destroy", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.Structures })(http.HandlerFunc(s.handleActionStructureDestroy)))
	mux.Handle("POST /action/structures/{id}/faction", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.Structures })(http.HandlerFunc(s.handleActionStructureFaction)))
	mux.Handle("POST /action/structures/{id}/refuel", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.Structures })(http.HandlerFunc(s.handleActionStructureRefuel)))
	mux.Handle("POST /action/structures/{id}/move", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.Structures })(http.HandlerFunc(s.handleActionStructureMove)))
	mux.Handle("POST /action/structures/{id}/restore", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.Structures })(http.HandlerFunc(s.handleActionStructureRestore)))
	mux.Handle("GET /action/structures/{id}/backups", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.Structures })(http.HandlerFunc(s.handleStructureBackupsList)))
	mux.Handle("POST /action/structures/{id}/backup-now", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.Structures })(http.HandlerFunc(s.handleActionStructureBackupNow)))
	mux.Handle("GET /action/structures/{id}/blocks-breakdown", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.Structures })(http.HandlerFunc(s.handleStructureBlocksBreakdown)))

	mux.Handle("GET /journal", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.Journal })(http.HandlerFunc(s.handleJournalPage)))
	mux.Handle("GET /action/journal/logs/daemon", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.Journal })(http.HandlerFunc(s.handleActionJournalDaemon)))
	mux.Handle("GET /action/journal/logs/webui", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.Journal })(http.HandlerFunc(s.handleActionJournalWebui)))
	mux.Handle("GET /action/journal/logs/server", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.Journal })(http.HandlerFunc(s.handleActionJournalServer)))
	mux.Handle("GET /action/journal/logs/scheduler", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.Journal })(http.HandlerFunc(s.handleActionJournalScheduler)))
	mux.Handle("GET /action/journal/logs/audit", s.requirePermission(func(p store.ModeratorPermissions) bool { return p.Journal })(http.HandlerFunc(s.handleActionJournalAudit)))

	mux.Handle("GET /accounts", s.requireAdmin(http.HandlerFunc(s.handleAccountsPage)))
	mux.Handle("POST /action/accounts", s.requireAdmin(http.HandlerFunc(s.handleActionAccountsCreate)))
	mux.Handle("DELETE /action/accounts/{username}", s.requireAdmin(http.HandlerFunc(s.handleActionAccountsDelete)))
	mux.Handle("POST /action/accounts/{username}/role", s.requireAdmin(http.HandlerFunc(s.handleActionAccountsSetRole)))
	mux.Handle("POST /action/accounts/moderator-permissions", s.requireAdmin(http.HandlerFunc(s.handleActionAccountsModeratorPermissions)))

	return mux
}
