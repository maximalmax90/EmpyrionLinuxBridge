package gameoptions

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"elb/daemon/internal/config"
)

type Kind string

const (
	KindInt    Kind = "int"
	KindBool   Kind = "bool"
	KindEnum   Kind = "enum"
	KindString Kind = "string"
)

type FieldDef struct {
	Name        string
	Kind        Kind
	Enum        []string
	Group       string
	Description string
	Default     string
}

var Fields = []FieldDef{
	{Name: "DecayTime", Kind: KindInt, Group: "Мир и структуры", Description: "Часы до удаления построек без ядра/<10 блоков без посещения (0=выкл)", Default: "24"},
	{Name: "WipeTime", Kind: KindInt, Group: "Мир и структуры", Description: "Часы до удаления ЛЮБЫХ построек без посещения (0=выкл)", Default: "0"},
	{Name: "ProtectTime", Kind: KindInt, Group: "Мир и структуры", Description: "Часы офлайн-защиты структур", Default: "48"},
	{Name: "ProtectDelay", Kind: KindInt, Group: "Мир и структуры", Description: "Секунд задержки до включения офлайн-защиты", Default: "300"},
	{Name: "MaxStructures", Kind: KindInt, Group: "Мир и структуры", Description: "Макс. построек на playfield (лимит 255)", Default: "200"},
	{Name: "RegeneratePOIs", Kind: KindBool, Group: "Мир и структуры", Description: "Регенерация POI", Default: "false"},
	{Name: "AutoMinerDepletion", Kind: KindBool, Group: "Мир и структуры", Description: "Истощение месторождений автомайнерами", Default: "true"},
	{Name: "MaxSpawnedEnemies", Kind: KindInt, Group: "Мир и структуры", Description: "Макс. NPC вокруг игрока", Default: "50"},
	{Name: "ForcePvP", Kind: KindBool, Group: "Мир и структуры", Description: "Принудительный PvP на всех playfield", Default: "false"},
	{Name: "GroundedStructureSpawn", Kind: KindBool, Group: "Мир и структуры", Description: "Чертежи спавнятся только на базе/крупном корабле", Default: "true"},
	{Name: "TurretUndergroundCheck", Kind: KindBool, Group: "Мир и структуры", Description: "Отключать турели под землёй/водой", Default: "false"},
	{Name: "EnableDecoKnockDown", Kind: KindBool, Group: "Мир и структуры", Description: "Разрушаемость декораций", Default: "true"},
	{Name: "IsManagedScenario", Kind: KindBool, Group: "Мир и структуры", Description: "Сценарий со статистикой/таймерами (managed)", Default: "false"},
	{Name: "OriginFactionStart", Kind: KindBool, Group: "Мир и структуры", Description: "Стартовая фракция по Origin (для кооп-игр)", Default: "false"},
	{Name: "ThrustersNeedOpenSpace", Kind: KindBool, Group: "Мир и структуры", Description: "Двигателю нужно открытое пространство сзади", Default: "false"},
	{Name: "UndergroundBpSpawn", Kind: KindEnum, Enum: []string{"Always", "PvE", "Never"}, Group: "Мир и структуры", Description: "Разрешение на спавн чертежей под землёй", Default: "Always"},
	{Name: "OriginDefault", Kind: KindString, Group: "Мир и структуры", Description: "Имя Origin по умолчанию", Default: "Neutral"},
	{Name: "OriginAccessOthers", Kind: KindBool, Group: "Мир и структуры", Description: "Разрешить альянсы с другими Origin", Default: "true"},
	{Name: "OriginAutoAlliance", Kind: KindBool, Group: "Мир и структуры", Description: "Авто-альянс фракций одного Origin", Default: "true"},
	{Name: "FriendlyFireInPvP", Kind: KindBool, Group: "Мир и структуры", Description: "Урон союзным структурам даже в PvP", Default: "true"},
	{Name: "DespawnEscapePod", Kind: KindBool, Group: "Мир и структуры", Description: "Спасательная капсула исчезает после использования", Default: "false"},
	{Name: "FOWTransparency", Kind: KindInt, Group: "Мир и структуры", Description: "Прозрачность тумана войны, 0-100", Default: "0"},
	{Name: "DSLUnloadSecs", Kind: KindInt, Group: "Мир и структуры", Description: "Задержка выгрузки структур без игроков рядом (0=выкл)", Default: "30"},
	{Name: "DSLDistancePlanet", Kind: KindInt, Group: "Мир и структуры", Description: "Дистанция выгрузки на планете (мин. 600)", Default: "1200"},
	{Name: "DSLDistanceSpace", Kind: KindInt, Group: "Мир и структуры", Description: "Дистанция выгрузки в космосе (мин. 1500)", Default: "3000"},

	{Name: "AntiGriefDistancePvE", Kind: KindInt, Group: "Анти-грифинг", Description: "Радиус (м) вокруг базы, где нельзя строить чужим, PvE (0=выкл)", Default: "30"},
	{Name: "AntiGriefDistancePvP", Kind: KindInt, Group: "Анти-грифинг", Description: "То же для PvP-плейфилдов", Default: "300"},
	{Name: "AntiGriefOresDistance", Kind: KindInt, Group: "Анти-грифинг", Description: "Радиус (м) вокруг месторождений, где нельзя строить", Default: "30"},
	{Name: "AntiGriefOresZone", Kind: KindEnum, Enum: []string{"All", "PvP", "PvE"}, Group: "Анти-грифинг", Description: "Где действует защита месторождений", Default: "PvE"},

	{Name: "EnableTrading", Kind: KindEnum, Enum: []string{"None", "GlobalVirtual", "LocalPlayerToPlayer", "LocalPlayerToSystem", "Local", "GlobalPlayerToPlayer", "Player2Player", "GlobalPlayerToSystem", "Player2System", "Global", "All", "GlobalEverywhere"}, Group: "Экономика/крафт", Description: "Режим торговли", Default: "All"},
	{Name: "EnableMaxBlockCount", Kind: KindBool, Group: "Экономика/крафт", Description: "Лимит на кол-во блоков (напр. оружия)", Default: "true"},
	{Name: "EnableVolumeWeight", Kind: KindBool, Group: "Экономика/крафт", Description: "Лимит объёма/веса", Default: "false"},
	{Name: "EnableCPUPoints", Kind: KindBool, Group: "Экономика/крафт", Description: "Лимит CPU Points (экспериментально)", Default: "false"},
	{Name: "EnableUnlockCheckInBPF", Kind: KindBool, Group: "Экономика/крафт", Description: "Проверка разблокировки устройств в фабрике чертежей", Default: "true"},
	{Name: "DiffConstrCraftTime", Kind: KindEnum, Enum: []string{"Faster", "Normal", "Slower", "Instant"}, Group: "Экономика/крафт", Description: "Скорость конструктора", Default: "Normal"},
	{Name: "DiffBpProdTime", Kind: KindEnum, Enum: []string{"Faster", "Normal", "Slower", "Instant"}, Group: "Экономика/крафт", Description: "Скорость фабрики чертежей/ремонт-блоков", Default: "Normal"},

	{Name: "DiffEscapePodContent", Kind: KindEnum, Enum: []string{"Easy", "Medium", "Hard"}, Group: "Сложность: игрок", Description: "Стартовое снаряжение", Default: "Medium"},
	{Name: "DiffPlayerProgression", Kind: KindEnum, Enum: []string{"Faster", "Normal", "Slower"}, Group: "Сложность: игрок", Description: "Скорость получения опыта", Default: "Normal"},
	{Name: "DiffDegradationSpeed", Kind: KindEnum, Enum: []string{"Low", "Normal", "High", "Off"}, Group: "Сложность: игрок", Description: "Износ оружия/инструментов", Default: "Normal"},
	{Name: "DiffPlayerBackpackDrop", Kind: KindEnum, Enum: []string{"DropNothing", "DropBagOnly", "DropEverything"}, Group: "Сложность: игрок", Description: "Дроп при смерти", Default: "DropEverything"},
	{Name: "DiffSpawnLocal", Kind: KindEnum, Enum: []string{"Always", "PvP", "PvE", "Never"}, Group: "Сложность: игрок", Description: "Локальный респавн на месте смерти", Default: "Always"},

	{Name: "DiffFoodConsumption", Kind: KindEnum, Enum: []string{"Low", "Normal", "High", "Off"}, Group: "Сложность: жизнеобеспечение", Description: "Скорость расхода еды", Default: "Normal"},
	{Name: "DiffOxygenConsumption", Kind: KindEnum, Enum: []string{"Low", "Normal", "High", "Off"}, Group: "Сложность: жизнеобеспечение", Description: "Скорость расхода кислорода", Default: "Normal"},
	{Name: "DiffRadiationTemperature", Kind: KindEnum, Enum: []string{"Low", "Normal", "High", "Off"}, Group: "Сложность: жизнеобеспечение", Description: "Влияние радиации/температуры", Default: "Normal"},

	{Name: "DiffAmountOfOre", Kind: KindEnum, Enum: []string{"Rich", "Normal", "Poor"}, Group: "Сложность: ресурсы", Description: "Кол-во руды в месторождении", Default: "Normal"},
	{Name: "DiffNumberOfDeposits", Kind: KindEnum, Enum: []string{"Plenty", "Normal", "Few"}, Group: "Сложность: ресурсы", Description: "Кол-во месторождений на планету", Default: "Normal"},

	{Name: "DiffAttackStrength", Kind: KindEnum, Enum: []string{"Easy", "Medium", "Hard"}, Group: "Сложность: враги", Description: "Сила атак врагов", Default: "Medium"},
	{Name: "DiffDronePresence", Kind: KindEnum, Enum: []string{"Low", "Normal", "High", "Off"}, Group: "Сложность: враги", Description: "Кол-во дронов на планете", Default: "Normal"},
	{Name: "DiffDroneBaseAttack", Kind: KindEnum, Enum: []string{"Low", "Normal", "High", "Off"}, Group: "Сложность: враги", Description: "Частота атак дронов на базу", Default: "Normal"},
	{Name: "DiffEnemySpawnRate", Kind: KindEnum, Enum: []string{"Low", "Normal", "High", "Off"}, Group: "Сложность: враги", Description: "Частота спавна врагов", Default: "Normal"},
}

type Field struct {
	Name        string   `json:"name"`
	Kind        Kind     `json:"kind"`
	Enum        []string `json:"enum,omitempty"`
	Group       string   `json:"group"`
	Description string   `json:"description"`
	Value       string   `json:"value"`
	Set         bool     `json:"set"`
}

type Snapshot struct {
	ValidFor []string `json:"validFor"`
	Found    bool     `json:"found"`
	Fields   []Field  `json:"fields"`
}

func Path(cfg config.Config) (string, error) {
	scenario, ok, err := cfg.GameField("CustomScenario")
	if err != nil {
		return "", err
	}
	if !ok || scenario == "" {
		return "", fmt.Errorf("сценарий не выбран (CustomScenario)")
	}
	path := filepath.Join(cfg.DataRoot, "Content", "Scenarios", scenario, "gameoptions.yaml")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("gameoptions.yaml не найден для сценария %q: %w", scenario, err)
	}
	return path, nil
}

func SavePath(cfg config.Config) (path string, ok bool) {
	name, err := cfg.GameName()
	if err != nil || name == "" {
		return "", false
	}
	path = filepath.Join(cfg.DataRoot, "Saves", "Games", name, "gameoptions.yaml")
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	return path, true
}

func ActiveMode(cfg config.Config) string {
	mode, ok, err := cfg.GameField("Mode")
	if err != nil || !ok || mode == "" {
		return "Survival"
	}
	return mode
}

type block struct {
	indent   string
	validFor []string
	start    int
	end      int
}

var validForRe = regexp.MustCompile(`(?m)^([ \t]*)-\s*ValidFor:\s*(?:\[\s*([^\]]*?)\s*\])?\s*\r?$`)

func findBlocks(lines []string) []block {
	var blocks []block
	for i, line := range lines {
		m := validForRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		var tokens []string
		if m[2] != "" {
			for _, t := range strings.Split(m[2], ",") {
				if t = strings.TrimSpace(t); t != "" {
					tokens = append(tokens, t)
				}
			}
		}
		blocks = append(blocks, block{indent: m[1], validFor: tokens, start: i + 1})
	}
	for i := range blocks {
		if i+1 < len(blocks) {
			blocks[i].end = blocks[i+1].start - 1
		} else {
			blocks[i].end = len(lines)
		}
	}
	return blocks
}

func blockMatches(tokens []string, mode string) bool {
	for _, t := range tokens {
		switch t {
		case "SP":
			return false
		case "MP":

		case "Survival", "Creative":
			if t != mode {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func findActiveBlock(lines []string, mode string) (block, bool) {
	for _, b := range findBlocks(lines) {
		if blockMatches(b.validFor, mode) {
			return b, true
		}
	}
	return block{}, false
}

var (
	fieldReMu           sync.Mutex
	fieldActiveReCache  = map[string]*regexp.Regexp{}
	fieldCommentReCache = map[string]*regexp.Regexp{}
)

func fieldActiveRe(indent, field string) *regexp.Regexp {
	key := indent + "\x00" + field
	fieldReMu.Lock()
	defer fieldReMu.Unlock()
	if re, ok := fieldActiveReCache[key]; ok {
		return re
	}
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(indent) + regexp.QuoteMeta(field) + `:[ \t]*(.*?)[ \t]*\r?$`)
	fieldActiveReCache[key] = re
	return re
}

func fieldCommentedRe(indent, field string) *regexp.Regexp {
	key := indent + "\x00" + field
	fieldReMu.Lock()
	defer fieldReMu.Unlock()
	if re, ok := fieldCommentReCache[key]; ok {
		return re
	}
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(indent) + `#[ \t]*` + regexp.QuoteMeta(field) + `:[ \t]*(.*?)[ \t]*\r?$`)
	fieldCommentReCache[key] = re
	return re
}

func stripInlineComment(raw string) string {
	if strings.HasPrefix(raw, `"`) {
		return raw
	}
	if i := strings.Index(raw, "#"); i >= 0 {
		return strings.TrimRight(raw[:i], " \t")
	}
	return raw
}

func unquote(s string) string {
	if len(s) >= 2 && strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		s = s[1 : len(s)-1]
		s = strings.ReplaceAll(s, `\"`, `"`)
		s = strings.ReplaceAll(s, `\\`, `\`)
	}
	return s
}

func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func escapeReplacementDollar(s string) string {
	return strings.ReplaceAll(s, "$", "$$")
}

func Read(cfg config.Config) (Snapshot, error) {
	path, err := Path(cfg)
	if err != nil {
		return Snapshot{}, err
	}

	if savePath, ok := SavePath(cfg); ok {
		path = savePath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read gameoptions.yaml: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	mode := ActiveMode(cfg)
	b, found := findActiveBlock(lines, mode)

	snap := Snapshot{Found: found, ValidFor: b.validFor}
	fieldIndent := b.indent + "  "
	blockText := ""
	if found {
		blockText = strings.Join(lines[b.start:b.end], "\n")
	}
	for _, def := range Fields {
		f := Field{Name: def.Name, Kind: def.Kind, Enum: def.Enum, Group: def.Group, Description: def.Description}
		if found {
			if m := fieldActiveRe(fieldIndent, def.Name).FindStringSubmatch(blockText); m != nil {
				f.Value = unquote(stripInlineComment(strings.TrimSpace(m[1])))

				if def.Kind == KindBool {
					f.Value = strings.ToLower(f.Value)
				}
				f.Set = true
			}
		}
		if !f.Set {
			f.Value = def.Default
		}
		snap.Fields = append(snap.Fields, f)
	}
	return snap, nil
}

var (
	intRe    = regexp.MustCompile(`^-?\d+$`)
	stringRe = regexp.MustCompile(`^[\p{L}0-9 _\-]{1,64}$`)
)

func renderValue(def FieldDef, raw string) (string, error) {
	switch def.Kind {
	case KindInt:
		if !intRe.MatchString(raw) {
			return "", fmt.Errorf("ожидалось целое число, получено %q", raw)
		}
		return raw, nil
	case KindBool:
		switch strings.ToLower(raw) {
		case "true":
			return "true", nil
		case "false":
			return "false", nil
		default:
			return "", fmt.Errorf("ожидалось true/false, получено %q", raw)
		}
	case KindEnum:
		for _, v := range def.Enum {
			if v == raw {
				return raw, nil
			}
		}
		return "", fmt.Errorf("недопустимое значение %q (разрешено: %s)", raw, strings.Join(def.Enum, ", "))
	case KindString:
		if !stringRe.MatchString(raw) {
			return "", fmt.Errorf("недопустимое значение %q", raw)
		}
		return quote(raw), nil
	default:
		return "", fmt.Errorf("неизвестный тип поля")
	}
}

func SetFields(cfg config.Config, values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	defsByName := make(map[string]FieldDef, len(Fields))
	for _, f := range Fields {
		defsByName[f.Name] = f
	}
	mode := ActiveMode(cfg)

	scenarioPath, err := Path(cfg)
	if err != nil {
		return err
	}
	if err := setFieldsAtPath(scenarioPath, mode, values, defsByName); err != nil {
		return err
	}

	log.Printf("gameoptions: SetFields: written to scenario file %s (%d fields: %s)", scenarioPath, len(values), fieldNames(values))

	if savePath, ok := SavePath(cfg); ok {
		if err := setFieldsAtPath(savePath, mode, values, defsByName); err != nil {
			return fmt.Errorf("active save copy: %w", err)
		}
		log.Printf("gameoptions: SetFields: also written to save copy %s", savePath)
	} else {
		log.Printf("gameoptions: SetFields: no active save copy yet (GameName=%q) — written to scenario file only, the engine will copy it when the save is created", firstGameName(cfg))
	}
	return nil
}

func fieldNames(values map[string]string) string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func firstGameName(cfg config.Config) string {
	name, _ := cfg.GameName()
	return name
}

func setFieldsAtPath(path, mode string, values map[string]string, defsByName map[string]FieldDef) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read gameoptions.yaml: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	b, found := findActiveBlock(lines, mode)

	if !found {
		header := fmt.Sprintf("  - ValidFor: [ MP, %s ]", mode)
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, header)
		b = block{indent: "  ", start: len(lines), end: len(lines)}
	}

	fieldIndent := b.indent + "  "
	blockLines := append([]string{}, lines[b.start:b.end]...)
	blockText := strings.Join(blockLines, "\n")

	for name, raw := range values {
		def, known := defsByName[name]
		if !known {
			return fmt.Errorf("неизвестное поле gameoptions: %s", name)
		}
		rendered, err := renderValue(def, raw)
		if err != nil {
			return fmt.Errorf("поле %s: %w", name, err)
		}
		replacement := fieldIndent + name + ": " + escapeReplacementDollar(rendered)
		active := fieldActiveRe(fieldIndent, name)
		commented := fieldCommentedRe(fieldIndent, name)
		switch {
		case active.MatchString(blockText):
			blockText = active.ReplaceAllString(blockText, replacement)
		case commented.MatchString(blockText):
			blockText = commented.ReplaceAllString(blockText, replacement)
		case blockText == "":
			blockText = fieldIndent + name + ": " + rendered
		default:
			blockText = fieldIndent + name + ": " + rendered + "\n" + blockText
		}
	}

	newLines := append([]string{}, lines[:b.start]...)
	newLines = append(newLines, strings.Split(blockText, "\n")...)
	newLines = append(newLines, lines[b.end:]...)

	return os.WriteFile(path, []byte(strings.Join(newLines, "\n")), 0o644)
}

var mergeableFieldRe = regexp.MustCompile(`(?m)^[ \t]*([A-Za-z_][A-Za-z0-9_]*):[ \t]*(.*?)[ \t]*\r?$`)

func SnapshotActiveBlockFields(path, mode string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read gameoptions.yaml: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	b, found := findActiveBlock(lines, mode)
	if !found {
		return nil, nil
	}
	blockText := strings.Join(lines[b.start:b.end], "\n")
	out := map[string]string{}
	for _, m := range mergeableFieldRe.FindAllStringSubmatch(blockText, -1) {
		val := stripInlineComment(m[2])
		if val == "" {
			continue
		}
		out[m[1]] = val
	}
	return out, nil
}

func MergeActiveBlockFields(path, mode string, snapshot map[string]string) error {
	if len(snapshot) == 0 {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read gameoptions.yaml: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	b, found := findActiveBlock(lines, mode)
	if !found {
		return nil
	}
	blockText := strings.Join(lines[b.start:b.end], "\n")
	for field, value := range snapshot {
		replacement := escapeReplacementDollar(field + ": " + value)
		activeRe := regexp.MustCompile(`(?m)^([ \t]*)` + regexp.QuoteMeta(field) + `:[ \t]*.*?[ \t]*\r?$`)
		commentedRe := regexp.MustCompile(`(?m)^([ \t]*)#[ \t]*` + regexp.QuoteMeta(field) + `:.*$`)
		switch {
		case activeRe.MatchString(blockText):
			blockText = activeRe.ReplaceAllString(blockText, "${1}"+replacement)
		case commentedRe.MatchString(blockText):
			blockText = commentedRe.ReplaceAllString(blockText, "${1}"+replacement)
		}
	}
	newLines := append([]string{}, lines[:b.start]...)
	newLines = append(newLines, strings.Split(blockText, "\n")...)
	newLines = append(newLines, lines[b.end:]...)
	return os.WriteFile(path, []byte(strings.Join(newLines, "\n")), 0o644)
}
