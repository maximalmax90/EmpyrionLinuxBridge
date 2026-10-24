package serverlog

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Category string

const (
	CategoryChat      Category = "chat"      // "CHAT ..." — уже разобрано в docs/research.md
	CategoryMod       Category = "mod"       // {ElbMod}/{:ElbMod} — наш собственный debug-вывод, вперемешку с логом движка
	CategoryError     Category = "error"     // -ERR-/-EXC-
	CategoryWarning   Category = "warning"   // -WRN-
	CategoryPlayer    Category = "player"    // коннект/дисконнект (движок надёжно логирует только дисконнект, см. память)
	CategoryPlayfield Category = "playfield" // загрузка/выгрузка плейфилда движком (не путать с CategoryMod — наше эхо тех же событий содержит "ElbMod" и уходит в mod)
	CategorySave      Category = "save"
	CategoryNoise     Category = "noise" // основная масса -LOG- строк: Eos/Steam/Telnet-сетевой шум, тики — "мухи"
	CategoryRaw       Category = "raw"   // без стандартного префикса вовсе
)

type Severity string

const (
	SeverityNone     Severity = ""
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

type Line struct {
	Text     string    `json:"text"`
	Category Category  `json:"category"`
	Severity Severity  `json:"severity,omitempty"`
	Time     time.Time `json:"time"`
}

var prefixRe = regexp.MustCompile(`^(\d{2})-(\d{2}):\d{2}:\d{2}\.\d{3} (\d{2})_(\d{2}) -(\w+)- (.*)$`)

var playerLineRe = regexp.MustCompile(`\[CM] Player .* disconnected|disconnected from \S+,`)

func Classify(line string) Category {
	cat, _, _, _ := classify(line, time.Now())
	return cat
}

func classify(line string, now time.Time) (cat Category, sev Severity, t time.Time, displayText string) {
	m := prefixRe.FindStringSubmatch(line)
	if m == nil {
		return CategoryRaw, SeverityNone, time.Time{}, line
	}
	day, _ := strconv.Atoi(m[1])
	hour, _ := strconv.Atoi(m[3])
	minute, _ := strconv.Atoi(m[4])
	level, msg := m[5], m[6]
	t = resolveLineTime(day, hour, minute, now)
	displayText = fmt.Sprintf("-%s- %s", level, msg)

	sev = SeverityNone
	switch level {
	case "ERR", "EXC":
		sev = SeverityCritical
	case "WRN":
		sev = SeverityWarning
	}

	switch {
	case strings.HasPrefix(msg, "CHAT "):
		cat = CategoryChat
	case strings.Contains(msg, "ElbMod"):
		cat = CategoryMod
	case level == "ERR" || level == "EXC":
		cat = CategoryError
	case level == "WRN":
		cat = CategoryWarning
	case playerLineRe.MatchString(msg):
		cat = CategoryPlayer
	case strings.Contains(strings.ToLower(msg), "playfield"):
		cat = CategoryPlayfield
	case strings.HasPrefix(msg, "Save game"):
		cat = CategorySave
	default:
		cat = CategoryNoise
	}
	return cat, sev, t, displayText
}

func resolveLineTime(day, hour, minute int, now time.Time) time.Time {
	year, month, _ := now.Date()
	t := time.Date(year, month, day, hour, minute, 0, 0, time.Local)
	if t.Month() != month || t.After(now.Add(12*time.Hour)) {
		t = time.Date(year, month, 1, hour, minute, 0, 0, time.Local).AddDate(0, -1, 0)
		t = time.Date(t.Year(), t.Month(), day, hour, minute, 0, 0, time.Local)
	}
	return t
}

func ParseLines(text string) []Line {
	now := time.Now()
	rawLines := strings.Split(text, "\n")
	out := make([]Line, 0, len(rawLines))
	for _, l := range rawLines {
		l = strings.TrimRight(l, "\r")
		if l == "" {
			continue
		}
		cat, sev, t, displayText := classify(l, now)
		out = append(out, Line{Text: displayText, Category: cat, Severity: sev, Time: t})
	}
	return out
}
