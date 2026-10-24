package scheduler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"elb/daemon/internal/chatlog"
	"elb/daemon/internal/config"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/notify"
	"elb/daemon/internal/playfieldwipe"
	"elb/daemon/internal/safego"
	"elb/daemon/internal/scriptqueue"
	"elb/daemon/internal/serverctl"
	"elb/daemon/internal/settings"
	"elb/daemon/internal/snapshot"
	"elb/daemon/internal/telnet"

	_ "modernc.org/sqlite"
)

type ActionType string

const (
	ActionRestartServer  ActionType = "restart_server"
	ActionStartServer    ActionType = "start_server"
	ActionStopServer     ActionType = "stop_server"
	ActionBroadcastChat  ActionType = "broadcast_chat"
	ActionConsoleCommand ActionType = "console_command"
	// ActionBackup — архивирует УЖЕ СУЩЕСТВУЮЩИЙ снапшот (см.
	// Params.SnapshotName, пусто = самый свежий) и копирует архив во ВСЕ
	// настроенные доп. хранилища (см. snapshot.RunBackup) — САМ снапшот не
	// создаёт. Top-level задача (бэкапить существующий снапшот по своему
	// расписанию, сервер не трогает вовсе). Отдельно, ВСТРОЕННО (не как
	// Subtask) в ActionRestartServer/ActionStopServer с MakeSnapshot=true —
	// см. Params.BackupEnabled/BackupDeferred/BackupDeferredTime и
	// runWithSnapshot (2026-09-11, редизайн: раньше это была подзадача
	// ActionBackup с Subtask.OffsetMinutes==0, признано избыточной сущностью
	// по прямой просьбе пользователя — "отдельных подзадач не будет").
	ActionBackup ActionType = "backup"
	// ActionWipePlayfield — чистка игровых данных (2026-09-08, по прямой
	// просьбе пользователя: "вайпы поверхностей/POI") — см.
	// internal/playfieldwipe за точным механизмом (файловая операция,
	// движок вайпает сам при следующей загрузке плейфилда).
	ActionWipePlayfield ActionType = "wipe_playfield"
	// ActionResetPlayfield — Reset/Recreate/RecreateDefect (2026-09-11, по
	// итогам разбора vendor/ewa PlayfieldController.cs) — режим в
	// Params.ResetMode, см. internal/playfieldwipe. Гораздо разрушительнее
	// ActionWipePlayfield (физическое удаление директорий, не маркер).
	ActionResetPlayfield ActionType = "reset_playfield"
	// ActionWipeOldUnusedPlayfields — автопоиск заброшенных плейфилдов
	// (Params.Days) без построек игроков → Reset (2026-09-11).
	ActionWipeOldUnusedPlayfields ActionType = "wipe_old_unused_playfields"
	// ActionBanner — модальный баннер (мод: /message/all или /message/player,
	// оба эндпоинта уже существовали — доработок мода не потребовалось,
	// 2026-09-11, по прямой просьбе пользователя: таргетированное
	// уведомление админу при неуспехе родительской задачи).
	ActionBanner ActionType = "banner"
	// ActionRunScript — "запуск скрипта" (2026-09-16, прямая просьба
	// пользователя: "как в чат-боте, в определённых сценариях может
	// пригодиться", см. daemon/internal/scriptqueue.ScriptsDir за
	// фиксированным источником .sh-файлов). В отличие от чат-бота
	// (мод под wine не умеет исполнять процессы хоста — нужна файловая
	// очередь+обратный вызов) планировщик УЖЕ часть демона и вызывает
	// scriptqueue.Run НАПРЯМУЮ, без очереди (см. runScriptAction). У
	// планировщика нет естественного адресата (не отвечает конкретному
	// игроку) — куда девать stdout решает Params.ScriptOutputMode: "log"
	// (по умолчанию, только в лог демона), "banner" (всем, см.
	// Params.BannerPriority — то же поле, что у ActionBanner), "chat"
	// (глобальный чат, channel="server").
	ActionRunScript ActionType = "run_script"
)

type RepeatType string

const (
	RepeatManual   RepeatType = "manual"   // только вручную (кнопка "запустить сейчас")
	RepeatInterval RepeatType = "interval" // каждые N минут от последнего запуска
	RepeatDaily    RepeatType = "daily"    // каждый день в TimeOfDay
	RepeatWeekday  RepeatType = "weekday"  // раз в неделю, в Weekday+TimeOfDay
	RepeatMonthly  RepeatType = "monthly"  // раз в месяц, в MonthDay+TimeOfDay
	RepeatOnce     RepeatType = "once"     // один раз в конкретный момент, потом self-deactivate
	// RepeatIntervalWeekday/RepeatIntervalMonthDay — 2026-09-20, прямая
	// просьба пользователя ("в пятницу у нас вайп, хочу повторять
	// сообщение каждые N минут именно в пятницу"): та же арифметика "каждые
	// N минут", что RepeatInterval, но действует ТОЛЬКО в дни, подходящие
	// под Weekday/MonthDay — весь такой день целиком, кратно суткам (00:00,
	// 00:00+N, 00:00+2N...), в остальные дни — тишина. TimeOfDay НЕ
	// используется этими двумя типами (см. computeNextRun/
	// nextIntervalOnWeekday/nextIntervalOnMonthDay).
	RepeatIntervalWeekday  RepeatType = "interval_weekday"
	RepeatIntervalMonthDay RepeatType = "interval_monthday"
)

type Params struct {
	Text    string `json:"text,omitempty"`
	Command string `json:"command,omitempty"`

	SnapshotName string `json:"snapshotName,omitempty"`

	LowPriority bool `json:"lowPriority,omitempty"`

	MakeSnapshot bool `json:"makeSnapshot,omitempty"`

	BackupEnabled bool `json:"backupEnabled,omitempty"`

	BackupDeferred bool `json:"backupDeferred,omitempty"`

	BackupDeferredTime string `json:"backupDeferredTime,omitempty"`

	WipePlayfields []string `json:"wipePlayfields,omitempty"`
	WipeTokens     []string `json:"wipeTokens,omitempty"`

	WarnMinutes int `json:"warnMinutes,omitempty"`

	Channel       string `json:"channel,omitempty"`
	FactionID     string `json:"factionId,omitempty"`
	RecipientName string `json:"recipientName,omitempty"`

	BannerText          string `json:"bannerText,omitempty"`
	BannerPriority      int    `json:"bannerPriority,omitempty"`
	BannerRecipientName string `json:"bannerRecipientName,omitempty"`

	ResetMode          string `json:"resetMode,omitempty"`
	ResetIfEmpty       bool   `json:"resetIfEmpty,omitempty"`
	ResetScanAllDefect bool   `json:"resetScanAllDefect,omitempty"`

	Days int `json:"days,omitempty"`

	ScriptName           string `json:"scriptName,omitempty"`
	ScriptTimeoutSeconds int    `json:"scriptTimeoutSeconds,omitempty"`
	ScriptOutputMode     string `json:"scriptOutputMode,omitempty"`
}

type Task struct {
	ID              int64      `json:"id"`
	Active          bool       `json:"active"`
	ActionType      ActionType `json:"actionType"`
	Params          Params     `json:"params"`
	Repeat          RepeatType `json:"repeat"`
	IntervalMinutes int        `json:"intervalMinutes,omitempty"`
	TimeOfDay       string     `json:"timeOfDay,omitempty"`
	Weekday         int        `json:"weekday,omitempty"`
	MonthDay        int        `json:"monthDay,omitempty"`
	OnceAt          string     `json:"onceAt,omitempty"`
	NextRun         string     `json:"nextRun,omitempty"`
	CreatedAt       string     `json:"createdAt,omitempty"`
	UpdatedAt       string     `json:"updatedAt,omitempty"`
}

type Run struct {
	ID         int64      `json:"id"`
	TaskID     int64      `json:"taskId"`
	SubtaskID  int64      `json:"subtaskId,omitempty"`
	ActionType ActionType `json:"actionType"`
	Params     Params     `json:"params"`
	RanAt      string     `json:"ranAt"`
	Success    bool       `json:"success"`
	Message    string     `json:"message"`
}

type RunCondition string

const (
	RunAlways    RunCondition = "always"
	RunOnSuccess RunCondition = "on_success"
	RunOnFailure RunCondition = "on_failure"
)

type Subtask struct {
	ID            int64        `json:"id"`
	TaskID        int64        `json:"taskId"`
	Active        bool         `json:"active"`
	ActionType    ActionType   `json:"actionType"`
	Params        Params       `json:"params"`
	OffsetMinutes int          `json:"offsetMinutes"`
	RunCondition  RunCondition `json:"runCondition"`
	NextRun       string       `json:"nextRun,omitempty"`
	CreatedAt     string       `json:"createdAt,omitempty"`
	UpdatedAt     string       `json:"updatedAt,omitempty"`
}

type pendingBackup struct {
	ID           int64
	TaskID       int64
	SnapshotName string
	DueAt        string
	LowPriority  bool
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS scheduled_tasks (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	active INTEGER NOT NULL DEFAULT 1,
	action_type TEXT NOT NULL,
	params_json TEXT NOT NULL DEFAULT '{}',
	repeat_type TEXT NOT NULL,
	interval_minutes INTEGER NOT NULL DEFAULT 0,
	time_of_day TEXT NOT NULL DEFAULT '',
	weekday INTEGER NOT NULL DEFAULT 0,
	month_day INTEGER NOT NULL DEFAULT 0,
	once_at TEXT NOT NULL DEFAULT '',
	next_run TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS scheduler_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id INTEGER NOT NULL,
	task_name TEXT NOT NULL,
	ran_at TEXT NOT NULL,
	success INTEGER NOT NULL,
	message TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS scheduler_subtasks (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id INTEGER NOT NULL,
	active INTEGER NOT NULL DEFAULT 1,
	action_type TEXT NOT NULL,
	params_json TEXT NOT NULL DEFAULT '{}',
	offset_minutes INTEGER NOT NULL DEFAULT 0,
	run_condition TEXT NOT NULL DEFAULT 'always',
	next_run TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS scheduler_pending_backups (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id INTEGER NOT NULL,
	snapshot_name TEXT NOT NULL,
	due_at TEXT NOT NULL,
	low_priority INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);
`

func migrateAddColumn(db *sql.DB, table, ddl string) error {
	_, err := db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + ddl)
	if err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("migrate %s add column: %w", table, err)
	}
	return nil
}

var (
	dbMu     sync.Mutex
	dbByPath = map[string]*sql.DB{}
)

func getDB(cfg config.Config) (*sql.DB, error) {
	dbMu.Lock()
	defer dbMu.Unlock()

	if db, ok := dbByPath[cfg.SchedulerDBFile]; ok {
		return db, nil
	}
	if err := os.MkdirAll(filepath.Dir(cfg.SchedulerDBFile), 0o777); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(cfg.SchedulerDBFile), err)
	}
	db, err := sql.Open("sqlite", cfg.SchedulerDBFile)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", cfg.SchedulerDBFile, err)
	}

	if _, err := db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		return nil, fmt.Errorf("pragma busy_timeout: %w", err)
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		return nil, fmt.Errorf("pragma journal_mode: %w", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	if err := migrateAddColumn(db, "scheduler_runs", `action_type TEXT NOT NULL DEFAULT ''`); err != nil {
		return nil, err
	}
	if err := migrateAddColumn(db, "scheduler_runs", `params_json TEXT NOT NULL DEFAULT '{}'`); err != nil {
		return nil, err
	}
	if err := migrateAddColumn(db, "scheduler_runs", `subtask_id INTEGER NOT NULL DEFAULT 0`); err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	dbByPath[cfg.SchedulerDBFile] = db
	return db, nil
}

func scanTask(row interface{ Scan(...any) error }) (Task, error) {
	var t Task
	var active int
	var paramsJSON string
	if err := row.Scan(&t.ID, &active, &t.ActionType, &paramsJSON, &t.Repeat,
		&t.IntervalMinutes, &t.TimeOfDay, &t.Weekday, &t.MonthDay, &t.OnceAt, &t.NextRun,
		&t.CreatedAt, &t.UpdatedAt); err != nil {
		return Task{}, err
	}
	t.Active = active != 0
	_ = json.Unmarshal([]byte(paramsJSON), &t.Params)
	return t, nil
}

const selectCols = `id, active, action_type, params_json, repeat_type, interval_minutes, time_of_day, weekday, month_day, once_at, next_run, created_at, updated_at`

func List(cfg config.Config) ([]Task, error) {
	db, err := getDB(cfg)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT ` + selectCols + ` FROM scheduled_tasks ORDER BY next_run = '', next_run, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func Get(cfg config.Config, id int64) (Task, error) {
	db, err := getDB(cfg)
	if err != nil {
		return Task{}, err
	}
	row := db.QueryRow(`SELECT `+selectCols+` FROM scheduled_tasks WHERE id = ?`, id)
	return scanTask(row)
}

func Save(cfg config.Config, t Task) (Task, error) {
	db, err := getDB(cfg)
	if err != nil {
		return Task{}, err
	}
	paramsJSON, err := json.Marshal(t.Params)
	if err != nil {
		return Task{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	t.NextRun = computeNextRun(t, time.Now())

	if t.ID == 0 {

		res, err := db.Exec(`INSERT INTO scheduled_tasks
			(name, active, action_type, params_json, repeat_type, interval_minutes, time_of_day, weekday, month_day, once_at, next_run, created_at, updated_at)
			VALUES ('', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			boolToInt(t.Active), t.ActionType, string(paramsJSON), t.Repeat,
			t.IntervalMinutes, t.TimeOfDay, t.Weekday, t.MonthDay, t.OnceAt, t.NextRun, now, now)
		if err != nil {
			return Task{}, err
		}
		t.ID, _ = res.LastInsertId()
		t.CreatedAt, t.UpdatedAt = now, now
		return t, nil
	}

	_, err = db.Exec(`UPDATE scheduled_tasks SET
			active=?, action_type=?, params_json=?, repeat_type=?, interval_minutes=?,
			time_of_day=?, weekday=?, month_day=?, once_at=?, next_run=?, updated_at=?
		WHERE id=?`,
		boolToInt(t.Active), t.ActionType, string(paramsJSON), t.Repeat,
		t.IntervalMinutes, t.TimeOfDay, t.Weekday, t.MonthDay, t.OnceAt, t.NextRun, now, t.ID)
	if err != nil {
		return Task{}, err
	}
	t.UpdatedAt = now

	if err := resyncSubtaskNextRuns(cfg, t); err != nil {
		log.Printf("scheduler: recompute next_run for subtasks of task %d: %v", t.ID, err)
	}
	return t, nil
}

func updateTaskNextRun(cfg config.Config, taskID int64, nextRun string) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.Exec(`UPDATE scheduled_tasks SET next_run=?, updated_at=? WHERE id=?`, nextRun, now, taskID)
	return err
}

func CloneTask(cfg config.Config, id int64) (Task, error) {
	orig, err := Get(cfg, id)
	if err != nil {
		return Task{}, fmt.Errorf("задача %d: %w", id, err)
	}
	orig.ID = 0
	orig.Active = false
	clone, err := Save(cfg, orig)
	if err != nil {
		return Task{}, err
	}

	subtasks, err := SubtasksForTask(cfg, id)
	if err != nil {
		return Task{}, fmt.Errorf("подзадачи исходной задачи %d: %w", id, err)
	}
	for _, st := range subtasks {
		st.ID = 0
		st.TaskID = clone.ID
		if _, err := SaveSubtask(cfg, st); err != nil {
			return Task{}, fmt.Errorf("клонирование подзадачи: %w", err)
		}
	}
	return clone, nil
}

func Delete(cfg config.Config, id int64) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM scheduler_subtasks WHERE task_id = ?`, id); err != nil {
		return err
	}
	_, err = db.Exec(`DELETE FROM scheduled_tasks WHERE id = ?`, id)
	return err
}

func Runs(cfg config.Config, limit int) ([]Run, error) {
	db, err := getDB(cfg)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.Query(`SELECT id, task_id, subtask_id, action_type, params_json, ran_at, success, message FROM scheduler_runs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		var success int
		var paramsJSON string
		if err := rows.Scan(&r.ID, &r.TaskID, &r.SubtaskID, &r.ActionType, &paramsJSON, &r.RanAt, &success, &r.Message); err != nil {
			return nil, err
		}
		r.Success = success != 0
		_ = json.Unmarshal([]byte(paramsJSON), &r.Params)
		out = append(out, r)
	}
	return out, rows.Err()
}

func RunsBetween(cfg config.Config, from, to string, limit int) ([]Run, error) {
	db, err := getDB(cfg)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 1000
	}
	query := `SELECT id, task_id, subtask_id, action_type, params_json, ran_at, success, message FROM scheduler_runs WHERE 1=1`
	var args []any
	if from != "" {
		query += ` AND ran_at >= ?`
		args = append(args, from)
	}
	if to != "" {
		query += ` AND ran_at <= ?`
		args = append(args, to)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		var success int
		var paramsJSON string
		if err := rows.Scan(&r.ID, &r.TaskID, &r.SubtaskID, &r.ActionType, &paramsJSON, &r.RanAt, &success, &r.Message); err != nil {
			return nil, err
		}
		r.Success = success != 0
		_ = json.Unmarshal([]byte(paramsJSON), &r.Params)
		out = append(out, r)
	}
	return out, rows.Err()
}

func LastRun(cfg config.Config, taskID int64) (Run, bool, error) {
	db, err := getDB(cfg)
	if err != nil {
		return Run{}, false, err
	}
	row := db.QueryRow(`SELECT id, task_id, subtask_id, action_type, params_json, ran_at, success, message
		FROM scheduler_runs WHERE task_id = ? AND subtask_id = 0 ORDER BY id DESC LIMIT 1`, taskID)
	var r Run
	var success int
	var paramsJSON string
	if err := row.Scan(&r.ID, &r.TaskID, &r.SubtaskID, &r.ActionType, &paramsJSON, &r.RanAt, &success, &r.Message); err != nil {
		if err == sql.ErrNoRows {
			return Run{}, false, nil
		}
		return Run{}, false, err
	}
	r.Success = success != 0
	_ = json.Unmarshal([]byte(paramsJSON), &r.Params)
	return r, true, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func computeNextRun(t Task, now time.Time) string {
	if !t.Active {
		return ""
	}
	switch t.Repeat {
	case RepeatManual:
		return ""
	case RepeatInterval:
		if t.IntervalMinutes <= 0 {
			return ""
		}
		return now.Add(time.Duration(t.IntervalMinutes) * time.Minute).Format(time.RFC3339)
	case RepeatDaily:
		return nextTimeOfDay(now, t.TimeOfDay).Format(time.RFC3339)
	case RepeatWeekday:
		return nextWeekdayAt(now, time.Weekday(t.Weekday), t.TimeOfDay).Format(time.RFC3339)
	case RepeatMonthly:
		return nextMonthDayAt(now, t.MonthDay, t.TimeOfDay).Format(time.RFC3339)
	case RepeatIntervalWeekday:
		if t.IntervalMinutes <= 0 {
			return ""
		}
		return nextIntervalOnWeekday(now, t.IntervalMinutes, time.Weekday(t.Weekday)).Format(time.RFC3339)
	case RepeatIntervalMonthDay:
		if t.IntervalMinutes <= 0 {
			return ""
		}
		return nextIntervalOnMonthDay(now, t.IntervalMinutes, t.MonthDay).Format(time.RFC3339)
	case RepeatOnce:
		if t.OnceAt == "" {
			return ""
		}
		if at, err := time.Parse(time.RFC3339, t.OnceAt); err == nil {
			return at.Format(time.RFC3339)
		}
		return ""
	default:
		return ""
	}
}

func computeSubtaskNextRun(parent Task, offsetMinutes int, referenceNow time.Time) string {
	offset := time.Duration(offsetMinutes) * time.Minute
	anchor := computeNextRun(parent, referenceNow.Add(-offset))
	if anchor == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, anchor)
	if err != nil {
		return ""
	}
	return t.Add(offset).Format(time.RFC3339)
}

func parseTimeOfDay(s string) (hh, mm, ss int, ok bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 && len(parts) != 3 {
		return 0, 0, 0, false
	}
	h, errH := strconv.Atoi(parts[0])
	m, errM := strconv.Atoi(parts[1])
	if errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, 0, false
	}
	if len(parts) == 2 {
		return h, m, 0, true
	}
	sec, errS := strconv.Atoi(parts[2])
	if errS != nil || sec < 0 || sec > 59 {
		return 0, 0, 0, false
	}
	return h, m, sec, true
}

func nextTimeOfDay(now time.Time, timeOfDay string) time.Time {
	h, m, s, ok := parseTimeOfDay(timeOfDay)
	if !ok {
		h, m, s = 0, 0, 0
	}
	candidate := time.Date(now.Year(), now.Month(), now.Day(), h, m, s, 0, now.Location())
	if !candidate.After(now) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate
}

func nextWeekdayAt(now time.Time, weekday time.Weekday, timeOfDay string) time.Time {
	h, m, s, ok := parseTimeOfDay(timeOfDay)
	if !ok {
		h, m, s = 0, 0, 0
	}
	candidate := time.Date(now.Year(), now.Month(), now.Day(), h, m, s, 0, now.Location())
	daysUntil := (int(weekday) - int(now.Weekday()) + 7) % 7
	candidate = candidate.AddDate(0, 0, daysUntil)
	if !candidate.After(now) {
		candidate = candidate.AddDate(0, 0, 7)
	}
	return candidate
}

func nextMonthDayAt(now time.Time, monthDay int, timeOfDay string) time.Time {
	h, m, s, ok := parseTimeOfDay(timeOfDay)
	if !ok {
		h, m, s = 0, 0, 0
	}
	if monthDay <= 0 {
		monthDay = 1
	}
	clampDay := func(year int, month time.Month, day int) time.Time {
		firstOfNext := time.Date(year, month+1, 1, 0, 0, 0, 0, now.Location())
		lastDay := firstOfNext.AddDate(0, 0, -1).Day()
		if day > lastDay {
			day = lastDay
		}
		return time.Date(year, month, day, h, m, s, 0, now.Location())
	}
	candidate := clampDay(now.Year(), now.Month(), monthDay)
	if !candidate.After(now) {
		next := now.AddDate(0, 1, 0)
		candidate = clampDay(next.Year(), next.Month(), monthDay)
	}
	return candidate
}

func nextIntervalOnWeekday(now time.Time, intervalMinutes int, weekday time.Weekday) time.Time {
	candidate := now.Add(time.Duration(intervalMinutes) * time.Minute)
	if candidate.Weekday() == weekday {
		return candidate
	}
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for i := 0; i < 8; i++ {
		day = day.AddDate(0, 0, 1)
		if day.Weekday() == weekday && day.After(now) {
			return day
		}
	}
	return candidate
}

func nextIntervalOnMonthDay(now time.Time, intervalMinutes int, monthDay int) time.Time {
	if monthDay <= 0 {
		monthDay = 1
	}
	candidate := now.Add(time.Duration(intervalMinutes) * time.Minute)
	if candidate.Day() == monthDay {
		return candidate
	}
	clampDay := func(year int, month time.Month) time.Time {
		firstOfNext := time.Date(year, month+1, 1, 0, 0, 0, 0, now.Location())
		lastDay := firstOfNext.AddDate(0, 0, -1).Day()
		d := monthDay
		if d > lastDay {
			d = lastDay
		}
		return time.Date(year, month, d, 0, 0, 0, 0, now.Location())
	}
	day := clampDay(now.Year(), now.Month())
	for !day.After(now) {
		next := day.AddDate(0, 1, 0)
		day = clampDay(next.Year(), next.Month())
	}
	return day
}

const subtaskSelectCols = `id, task_id, active, action_type, params_json, offset_minutes, run_condition, next_run, created_at, updated_at`

func scanSubtask(row interface{ Scan(...any) error }) (Subtask, error) {
	var s Subtask
	var active int
	var paramsJSON string
	if err := row.Scan(&s.ID, &s.TaskID, &active, &s.ActionType, &paramsJSON, &s.OffsetMinutes,
		&s.RunCondition, &s.NextRun, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return Subtask{}, err
	}
	s.Active = active != 0
	_ = json.Unmarshal([]byte(paramsJSON), &s.Params)
	return s, nil
}

func SubtasksForTask(cfg config.Config, taskID int64) ([]Subtask, error) {
	db, err := getDB(cfg)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT `+subtaskSelectCols+` FROM scheduler_subtasks WHERE task_id = ? ORDER BY offset_minutes, id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Subtask
	for rows.Next() {
		s, err := scanSubtask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func allSubtasksByTaskID(cfg config.Config) (map[int64][]Subtask, error) {
	db, err := getDB(cfg)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT ` + subtaskSelectCols + ` FROM scheduler_subtasks ORDER BY task_id, offset_minutes, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]Subtask{}
	for rows.Next() {
		s, err := scanSubtask(rows)
		if err != nil {
			return nil, err
		}
		out[s.TaskID] = append(out[s.TaskID], s)
	}
	return out, rows.Err()
}

func GetSubtask(cfg config.Config, id int64) (Subtask, error) {
	db, err := getDB(cfg)
	if err != nil {
		return Subtask{}, err
	}
	row := db.QueryRow(`SELECT `+subtaskSelectCols+` FROM scheduler_subtasks WHERE id = ?`, id)
	return scanSubtask(row)
}

func SaveSubtask(cfg config.Config, s Subtask) (Subtask, error) {
	parent, err := Get(cfg, s.TaskID)
	if err != nil {
		return Subtask{}, fmt.Errorf("родительская задача %d: %w", s.TaskID, err)
	}
	if parent.Repeat == RepeatManual || parent.Repeat == RepeatInterval ||
		parent.Repeat == RepeatIntervalWeekday || parent.Repeat == RepeatIntervalMonthDay {
		return Subtask{}, fmt.Errorf("подзадачи не поддерживаются для расписания %q (нет фиксированного времени на часах)", parent.Repeat)
	}

	if s.ActionType == ActionBackup && s.OffsetMinutes < 0 {
		return Subtask{}, fmt.Errorf("подзадача бэкапа не может выполняться до родителя (нет ещё снапшота, который бэкапить)")
	}
	if s.OffsetMinutes < 0 {
		s.RunCondition = RunAlways
	}
	if s.RunCondition == "" {
		s.RunCondition = RunAlways
	}

	db, err := getDB(cfg)
	if err != nil {
		return Subtask{}, err
	}
	paramsJSON, err := json.Marshal(s.Params)
	if err != nil {
		return Subtask{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	s.NextRun = computeSubtaskNextRun(parent, s.OffsetMinutes, time.Now())

	if s.ID == 0 {
		res, err := db.Exec(`INSERT INTO scheduler_subtasks
			(task_id, active, action_type, params_json, offset_minutes, run_condition, next_run, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			s.TaskID, boolToInt(s.Active), s.ActionType, string(paramsJSON), s.OffsetMinutes, s.RunCondition, s.NextRun, now, now)
		if err != nil {
			return Subtask{}, err
		}
		s.ID, _ = res.LastInsertId()
		s.CreatedAt, s.UpdatedAt = now, now
		return s, nil
	}

	_, err = db.Exec(`UPDATE scheduler_subtasks SET
			active=?, action_type=?, params_json=?, offset_minutes=?, run_condition=?, next_run=?, updated_at=?
		WHERE id=?`,
		boolToInt(s.Active), s.ActionType, string(paramsJSON), s.OffsetMinutes, s.RunCondition, s.NextRun, now, s.ID)
	if err != nil {
		return Subtask{}, err
	}
	s.UpdatedAt = now
	return s, nil
}

func DeleteSubtask(cfg config.Config, id int64) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	_, err = db.Exec(`DELETE FROM scheduler_subtasks WHERE id = ?`, id)
	return err
}

func resyncSubtaskNextRuns(cfg config.Config, t Task) error {
	subtasks, err := SubtasksForTask(cfg, t.ID)
	if err != nil {
		return err
	}
	if len(subtasks) == 0 {
		return nil
	}
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	now := time.Now()
	updatedAt := now.UTC().Format(time.RFC3339Nano)
	for _, s := range subtasks {
		nextRun := computeSubtaskNextRun(t, s.OffsetMinutes, now)
		if _, err := db.Exec(`UPDATE scheduler_subtasks SET next_run=?, updated_at=? WHERE id=?`, nextRun, updatedAt, s.ID); err != nil {
			return err
		}
	}
	return nil
}

func savePendingBackup(cfg config.Config, taskID int64, snapshotName, dueAt string, lowPriority bool) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO scheduler_pending_backups (task_id, snapshot_name, due_at, low_priority, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		taskID, snapshotName, dueAt, boolToInt(lowPriority), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func duePendingBackups(cfg config.Config, now time.Time) ([]pendingBackup, error) {
	db, err := getDB(cfg)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT id, task_id, snapshot_name, due_at, low_priority FROM scheduler_pending_backups`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pendingBackup
	for rows.Next() {
		var pb pendingBackup
		var lowPriority int
		if err := rows.Scan(&pb.ID, &pb.TaskID, &pb.SnapshotName, &pb.DueAt, &lowPriority); err != nil {
			return nil, err
		}
		pb.LowPriority = lowPriority != 0
		due, err := time.Parse(time.RFC3339, pb.DueAt)
		if err != nil || !due.After(now) {
			out = append(out, pb)
		}
	}
	return out, rows.Err()
}

func deletePendingBackup(cfg config.Config, id int64) error {
	db, err := getDB(cfg)
	if err != nil {
		return err
	}
	_, err = db.Exec(`DELETE FROM scheduler_pending_backups WHERE id = ?`, id)
	return err
}

func allPendingBackupNames(cfg config.Config) (map[string]bool, error) {
	db, err := getDB(cfg)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT DISTINCT snapshot_name FROM scheduler_pending_backups`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

func pollPendingBackups(cfg config.Config, now time.Time) {
	due, err := duePendingBackups(cfg, now)
	if err != nil {
		log.Printf("scheduler: pending backups: %v", err)
		return
	}
	if len(due) > 0 {
		s, err := settings.Load(cfg.SettingsFile)
		if err != nil {
			log.Printf("scheduler: pending backups, settings: %v", err)
			return
		}
		for _, pb := range due {
			runErr := snapshot.RunBackup(cfg, s.Backup.Destinations, pb.SnapshotName, pb.LowPriority)
			recordRun(cfg, pb.TaskID, -1, ActionBackup, Params{SnapshotName: pb.SnapshotName, LowPriority: pb.LowPriority}, runErr)
			snapshot.ClearBackupBusy(cfg, pb.SnapshotName)
			if err := deletePendingBackup(cfg, pb.ID); err != nil {
				log.Printf("scheduler: delete pending backup %d: %v", pb.ID, err)
			}
		}
	}

	names, err := allPendingBackupNames(cfg)
	if err != nil {
		log.Printf("scheduler: touch busy markers for pending backups: %v", err)
		return
	}
	for name := range names {
		if err := snapshot.MarkBackupBusy(cfg, name); err != nil {
			log.Printf("scheduler: touch busy marker %s: %v", name, err)
		}
	}
}

func RunPoller(cfg config.Config, mod *modclient.Client) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		safego.Tick("scheduler.RunPoller", func() {
			if err := PollOnce(cfg, mod); err != nil {
				log.Printf("scheduler: tick: %v", err)
			}
		})
	}
}

func PollOnce(cfg config.Config, mod *modclient.Client) error {

	if s, err := settings.Load(cfg.SettingsFile); err != nil {
		log.Printf("scheduler: could not read settings for Scheduler.Paused: %v", err)
	} else if s.Scheduler.Paused {
		return nil
	}
	tasks, err := List(cfg)
	if err != nil {
		return err
	}

	subtasksByTask, err := allSubtasksByTaskID(cfg)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, task := range tasks {
		if !task.Active {
			continue
		}
		if task.NextRun != "" {
			if nextRun, err := time.Parse(time.RFC3339, task.NextRun); err == nil {

				dueAt := nextRun.Add(-warnMinutesFor(task))
				if !dueAt.After(now) {
					t := task
					runErr := RunTask(cfg, mod, t)
					recordRun(cfg, t.ID, 0, t.ActionType, t.Params, runErr)

					if t.Repeat == RepeatOnce {
						t.Active = false
					}

					t.NextRun = computeNextRun(t, nextRun)
					if err := updateTaskNextRun(cfg, t.ID, t.NextRun); err != nil {
						log.Printf("scheduler: recompute next_run for task %d (%s): %v", t.ID, t.ActionType, err)
					}
				}
			}
		}
		if err := pollSubtasks(cfg, mod, task, subtasksByTask[task.ID], now); err != nil {
			log.Printf("scheduler: subtasks of task %d: %v", task.ID, err)
		}
	}
	pollPendingBackups(cfg, now)
	return nil
}

func pollSubtasks(cfg config.Config, mod *modclient.Client, parent Task, subtasks []Subtask, now time.Time) error {

	var lastRun Run
	var haveLastRun, lastRunLoaded bool
	for _, st := range subtasks {
		if !st.Active || st.NextRun == "" {
			continue
		}
		nextRun, err := time.Parse(time.RFC3339, st.NextRun)
		if err != nil || nextRun.After(now) {
			continue
		}

		skip := false
		if st.RunCondition != RunAlways {
			if !lastRunLoaded {
				var err error
				lastRun, haveLastRun, err = LastRun(cfg, parent.ID)
				if err != nil {
					log.Printf("scheduler: parent %d LastRun: %v", parent.ID, err)
				}
				lastRunLoaded = true
			}
			switch {
			case !haveLastRun:
				skip = true
			case st.RunCondition == RunOnSuccess && !lastRun.Success:
				skip = true
			case st.RunCondition == RunOnFailure && lastRun.Success:
				skip = true
			}
		}

		if skip {
			recordRun(cfg, parent.ID, st.ID, st.ActionType, st.Params, fmt.Errorf("пропущено: условие %q не выполнено", st.RunCondition))
		} else {
			runErr := RunTask(cfg, mod, Task{ActionType: st.ActionType, Params: st.Params})
			recordRun(cfg, parent.ID, st.ID, st.ActionType, st.Params, runErr)
		}

		st.NextRun = computeSubtaskNextRun(parent, st.OffsetMinutes, now)
		if _, err := SaveSubtask(cfg, st); err != nil {
			log.Printf("scheduler: recompute next_run for subtask %d: %v", st.ID, err)
		}
	}
	return nil
}

func recordRun(cfg config.Config, taskID, subtaskID int64, actionType ActionType, params Params, runErr error) {
	db, err := getDB(cfg)
	if err != nil {
		return
	}
	message := "ok"
	if runErr != nil {
		message = runErr.Error()
	}
	paramsJSON, _ := json.Marshal(params)

	if _, err := db.Exec(`INSERT INTO scheduler_runs (task_id, task_name, subtask_id, action_type, params_json, ran_at, success, message) VALUES (?, '', ?, ?, ?, ?, ?, ?)`,
		taskID, subtaskID, actionType, string(paramsJSON), time.Now().UTC().Format(time.RFC3339Nano), boolToInt(runErr == nil), message); err != nil {
		log.Printf("scheduler: history write for task %d (%s): %v", taskID, actionType, err)
	}
	if actionType == ActionBackup {
		notifyBackupFailure(cfg, runErr)
	}
}

func notifyBackupFailure(cfg config.Config, runErr error) {
	if runErr == nil {
		return
	}
	msg := "Бэкап пропущен: дополнительное хранилище не настроено или отключено"
	if !errors.Is(runErr, snapshot.ErrNoBackupDestination) {
		msg = "Бэкап не выполнен: " + runErr.Error()
	}
	if err := notify.Add(cfg, notify.LevelWarning, msg); err != nil {
		log.Printf("scheduler: notify.Add: %v", err)
	}
}

func RunNow(cfg config.Config, mod *modclient.Client, id int64) error {
	t, err := Get(cfg, id)
	if err != nil {
		return err
	}
	runErr := RunTask(cfg, mod, t)
	recordRun(cfg, t.ID, 0, t.ActionType, t.Params, runErr)
	runSubtasksNow(cfg, mod, t, runErr == nil)
	return runErr
}

func runSubtasksNow(cfg config.Config, mod *modclient.Client, parent Task, parentSucceeded bool) {
	subtasks, err := SubtasksForTask(cfg, parent.ID)
	if err != nil {
		log.Printf("scheduler: subtasks of task %d (manual run): %v", parent.ID, err)
		return
	}
	for _, st := range subtasks {
		if !st.Active || st.OffsetMinutes < 0 {
			continue
		}
		skip := false
		switch {
		case st.RunCondition == RunOnSuccess && !parentSucceeded:
			skip = true
		case st.RunCondition == RunOnFailure && parentSucceeded:
			skip = true
		}
		if skip {
			recordRun(cfg, parent.ID, st.ID, st.ActionType, st.Params, fmt.Errorf("пропущено: условие %q не выполнено", st.RunCondition))
			continue
		}
		runErr := RunTask(cfg, mod, Task{ActionType: st.ActionType, Params: st.Params})
		recordRun(cfg, parent.ID, st.ID, st.ActionType, st.Params, runErr)
	}
}

func RunSubtaskNow(cfg config.Config, mod *modclient.Client, id int64) error {
	st, err := GetSubtask(cfg, id)
	if err != nil {
		return err
	}
	runErr := RunTask(cfg, mod, Task{ActionType: st.ActionType, Params: st.Params})
	recordRun(cfg, st.TaskID, st.ID, st.ActionType, st.Params, runErr)
	return runErr
}

var (
	backgroundActionMu       sync.Mutex
	backgroundActionInFlight bool
)

func tryBeginBackgroundAction() bool {
	backgroundActionMu.Lock()
	defer backgroundActionMu.Unlock()
	if backgroundActionInFlight {
		return false
	}
	backgroundActionInFlight = true
	return true
}

func endBackgroundAction() {
	backgroundActionMu.Lock()
	backgroundActionInFlight = false
	backgroundActionMu.Unlock()
}

func RunTask(cfg config.Config, mod *modclient.Client, t Task) error {
	switch t.ActionType {
	case ActionRestartServer:

		if !tryBeginBackgroundAction() {
			log.Printf("scheduler: restart_server skipped — a previous restart/stop is still running (see backgroundActionInFlight)")
			return fmt.Errorf("восстановление/остановка сервера уже выполняется — повторный запуск пропущен")
		}
		go func(t Task) {
			defer endBackgroundAction()
			defer func() {
				if r := recover(); r != nil {
					log.Printf("scheduler: panic in restart_server (background): %v\n%s", r, debug.Stack())
				}
			}()
			var err error
			if t.Params.MakeSnapshot {
				err = runWithSnapshot(cfg, t, true)
			} else {
				WarnBeforeStop(cfg, t.Params.WarnMinutes)
				err = serverctl.Restart(cfg.SystemdUnit)
			}
			if err != nil {
				log.Printf("scheduler: restart_server (background): %v", err)
			}
		}(t)
		return nil
	case ActionStartServer:
		return serverctl.Start(cfg.SystemdUnit)
	case ActionStopServer:

		if !tryBeginBackgroundAction() {
			log.Printf("scheduler: stop_server skipped — a previous restart/stop is still running (see backgroundActionInFlight)")
			return fmt.Errorf("восстановление/остановка сервера уже выполняется — повторный запуск пропущен")
		}
		go func(t Task) {
			defer endBackgroundAction()
			defer func() {
				if r := recover(); r != nil {
					log.Printf("scheduler: panic in stop_server (background): %v\n%s", r, debug.Stack())
				}
			}()
			var err error
			if t.Params.MakeSnapshot {
				err = runWithSnapshot(cfg, t, false)
			} else {
				WarnBeforeStop(cfg, t.Params.WarnMinutes)
				err = serverctl.Stop(cfg.SystemdUnit)
			}
			if err != nil {
				log.Printf("scheduler: stop_server (background): %v", err)
			}
		}(t)
		return nil
	case ActionBroadcastChat:

		senderName := "Сервер"
		if s, err := settings.Load(cfg.SettingsFile); err == nil && s.Chatbot.ServerSenderName != "" {
			senderName = s.Chatbot.ServerSenderName
		}

		if t.Params.RecipientName != "" {
			p, ok, err := findOnlinePlayerByName(mod, t.Params.RecipientName)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("recipientName %q: игрок не найден или оффлайн", t.Params.RecipientName)
			}
			_, err = mod.PostMessageWithRetry("/chat", map[string]any{"text": t.Params.Text, "recipientSteamId": p.SteamId, "asSteamId": "__server__", "senderName": senderName})
			return err
		}
		channel := t.Params.Channel
		if channel == "" {
			channel = "global"
		}
		body := map[string]any{"text": t.Params.Text, "channel": channel, "asSteamId": "__server__", "senderName": senderName}
		if channel == "faction" {
			body["factionId"] = t.Params.FactionID
		}
		_, err := mod.PostMessageWithRetry("/chat", body)
		return err
	case ActionConsoleCommand:
		addr, pwd, err := cfg.TelnetCreds()
		if err != nil {
			return err
		}
		_, err = telnet.New(addr, pwd).Command(t.Params.Command, 15*time.Second)
		return err
	case ActionBackup:
		if t.Params.LowPriority {

			go func(p Params) {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("scheduler: panic in backup (background): %v\n%s", r, debug.Stack())
					}
				}()
				err := runBackupAction(cfg, p)
				if err != nil {
					log.Printf("scheduler: backup (background): %v", err)
				}

				notifyBackupFailure(cfg, err)
			}(t.Params)
			return nil
		}
		return runBackupAction(cfg, t.Params)
	case ActionWipePlayfield:

		tokens := make([]playfieldwipe.Token, 0, len(t.Params.WipeTokens))
		for _, s := range t.Params.WipeTokens {
			if !playfieldwipe.ValidToken(s) {
				continue
			}
			tokens = append(tokens, playfieldwipe.Token(s))
		}
		return playfieldwipe.Queue(cfg, t.Params.WipePlayfields, tokens)
	case ActionResetPlayfield:
		switch t.Params.ResetMode {
		case "recreate":
			return playfieldwipe.Recreate(cfg, t.Params.WipePlayfields)
		case "recreateDefect":
			return playfieldwipe.RecreateDefect(cfg, t.Params.WipePlayfields, t.Params.ResetScanAllDefect)
		default:
			if t.Params.ResetIfEmpty {
				return playfieldwipe.ResetIfEmpty(cfg, t.Params.WipePlayfields)
			}
			return playfieldwipe.Reset(cfg, t.Params.WipePlayfields)
		}
	case ActionWipeOldUnusedPlayfields:
		days := t.Params.Days
		if days <= 0 {
			days = 14
		}
		return playfieldwipe.WipeOldUnused(cfg, days)
	case ActionBanner:

		if t.Params.BannerRecipientName == "" {
			_, err := mod.PostMessageWithRetry("/message/all", map[string]any{"text": t.Params.BannerText, "prio": t.Params.BannerPriority})
			if err == nil {
				logSchedulerBanner(cfg, t.Params.BannerText, "", t.Params.BannerPriority)
			}
			return err
		}
		p, ok, err := findOnlinePlayerByName(mod, t.Params.BannerRecipientName)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("bannerRecipientName %q: игрок не найден или оффлайн", t.Params.BannerRecipientName)
		}
		_, err = mod.PostMessageWithRetry("/message/player", map[string]any{"playerId": p.EntityId, "text": t.Params.BannerText, "prio": t.Params.BannerPriority})
		if err == nil {
			logSchedulerBanner(cfg, t.Params.BannerText, t.Params.BannerRecipientName, t.Params.BannerPriority)
		}
		return err
	case ActionRunScript:
		return runScriptAction(cfg, mod, t.Params)
	default:
		return fmt.Errorf("неизвестный тип действия: %s", t.ActionType)
	}
}

func logSchedulerBanner(cfg config.Config, text, target string, prio int) {
	senderName := "Сервер"
	if s, err := settings.Load(cfg.SettingsFile); err == nil && s.Chatbot.ServerSenderName != "" {
		senderName = s.Chatbot.ServerSenderName
	}
	if err := chatlog.LogBanner(cfg, senderName, text, target, chatlog.PriorityName(prio)); err != nil {
		log.Printf("scheduler: log banner: %v", err)
	}
}

func runScriptAction(cfg config.Config, mod *modclient.Client, p Params) error {
	timeout := time.Duration(p.ScriptTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	out, err := scriptqueue.Run(p.ScriptName, timeout)
	if err != nil {
		return err
	}
	switch p.ScriptOutputMode {
	case "banner":
		_, err = mod.PostMessageWithRetry("/message/all", map[string]any{"text": out, "prio": p.BannerPriority})
		if err == nil {
			logSchedulerBanner(cfg, out, "", p.BannerPriority)
		}
		return err
	case "chat":
		_, err = mod.PostMessageWithRetry("/chat", map[string]any{"text": out, "channel": "server"})
		return err
	default:
		log.Printf("scheduler: run_script %s: %s", p.ScriptName, out)
		return nil
	}
}

func findOnlinePlayerByName(mod *modclient.Client, name string) (modclient.Player, bool, error) {
	for _, p := range mod.CachedPlayers() {
		if strings.EqualFold(p.Name, name) {
			if !p.Online {
				return modclient.Player{}, false, nil
			}
			return p, true, nil
		}
	}
	return modclient.Player{}, false, nil
}

func warnMinutesFor(t Task) time.Duration {
	if (t.ActionType == ActionRestartServer || t.ActionType == ActionStopServer) && t.Params.WarnMinutes > 0 {
		return time.Duration(t.Params.WarnMinutes) * time.Minute
	}
	return 0
}

func WarnBeforeStop(cfg config.Config, warnMinutes int) {
	if warnMinutes <= 0 {
		return
	}
	addr, pwd, err := cfg.TelnetCreds()
	if err != nil {
		return
	}
	if _, err := telnet.New(addr, pwd).Command(fmt.Sprintf("saveandexit %d", warnMinutes), 15*time.Second); err != nil {
		log.Printf("scheduler: WarnBeforeStop (saveandexit %d): %v", warnMinutes, err)
	}
	time.Sleep(time.Duration(warnMinutes) * time.Minute)
}

func primaryDestination(cfg config.Config) settings.BackupDestination {
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		log.Printf("scheduler: settings: %v", err)
		return settings.BackupDestination{Type: "snapshot"}
	}
	return s.Backup.Destinations[0]
}

func runBackupAction(cfg config.Config, p Params) error {
	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	name := p.SnapshotName
	if name == "" {
		name, err = latestSnapshotName(cfg)
		if err != nil {
			return err
		}
	}
	return snapshot.RunBackup(cfg, s.Backup.Destinations, name, p.LowPriority)
}

func latestSnapshotName(cfg config.Config) (string, error) {
	infos, err := snapshot.List(cfg)
	if err != nil {
		return "", err
	}
	if len(infos) == 0 {
		return "", fmt.Errorf("no snapshots exist yet — run restart_server/stop_server with the \"Make a snapshot\" checkbox first")
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	return infos[len(infos)-1].Name, nil
}

func runWithSnapshot(cfg config.Config, t Task, restart bool) error {
	WarnBeforeStop(cfg, t.Params.WarnMinutes)

	st := serverctl.GetStatus(cfg)
	serverLookedHealthy := !(st.Active && !st.TelnetOK)

	if err := serverctl.Stop(cfg.SystemdUnit); err != nil {
		return fmt.Errorf("stopping server before snapshot: %w", err)
	}

	s, err := settings.Load(cfg.SettingsFile)
	if err != nil {
		if restart {
			serverctl.Start(cfg.SystemdUnit)
		}
		return fmt.Errorf("settings: %w", err)
	}

	info, err := snapshot.CreateAndRetain(cfg, s.Backup.Destinations[0])
	if err != nil {
		if restart {
			if startErr := serverctl.Start(cfg.SystemdUnit); startErr != nil {
				return fmt.Errorf("snapshot failed (%v), and failed to start the server back up: %w", err, startErr)
			}
		}
		return fmt.Errorf("snapshot failed: %w", err)
	}

	if t.Params.BackupEnabled {
		switch {
		case !serverLookedHealthy:
			recordRun(cfg, t.ID, -1, ActionBackup, t.Params, fmt.Errorf("пропущено: сервер выглядел зависшим перед остановкой"))
		case t.Params.BackupDeferred:
			if err := snapshot.MarkBackupBusy(cfg, info.Name); err != nil {
				log.Printf("scheduler: busy marker for pending backup %s: %v", info.Name, err)
			}
			due := nextTimeOfDay(time.Now(), t.Params.BackupDeferredTime)
			if err := savePendingBackup(cfg, t.ID, info.Name, due.Format(time.RFC3339), t.Params.LowPriority); err != nil {
				log.Printf("scheduler: register pending backup for task %d: %v", t.ID, err)
			}
		default:
			runErr := snapshot.RunBackup(cfg, s.Backup.Destinations, info.Name, t.Params.LowPriority)
			recordRun(cfg, t.ID, -1, ActionBackup, t.Params, runErr)
			if runErr != nil {
				log.Printf("scheduler: synchronous built-in backup for task %d: %v", t.ID, runErr)
			}
		}
	}

	if !restart {
		return nil
	}
	return serverctl.Start(cfg.SystemdUnit)
}
