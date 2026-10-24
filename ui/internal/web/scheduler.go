package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"elb/ui/internal/daemonclient"
)

func (s *Server) statusBarData(u *userInfo) map[string]any {

	var (
		health        daemonclient.Health
		sysInfo       daemonclient.SysInfo
		ident         daemonclient.Identity
		playfields    daemonclient.Playfields
		notifications []daemonclient.Notification
		unreadCount   int
		mounts        []daemonclient.MountInfo
		cfg           daemonclient.AppConfig
		manual        daemonclient.ManualAction
	)
	var wg sync.WaitGroup
	run := func(f func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := f(); err != nil {
				log.Printf("statusbar: %v", err)
			}
		}()
	}
	run(func() (err error) { health, err = s.daemon.Health(); return })
	run(func() (err error) { sysInfo, err = s.daemon.SysInfo(); return })
	run(func() (err error) { ident, err = s.daemon.Identity(); return })
	run(func() (err error) { playfields, err = s.daemon.Playfields(); return })

	run(func() (err error) { notifications, err = s.daemon.Notifications(20); return })
	run(func() (err error) { unreadCount, err = s.daemon.NotificationsUnreadCount(); return })

	run(func() (err error) { mounts, err = s.daemon.Mounts(); return })
	run(func() (err error) { cfg, err = s.daemon.Config(); return })

	run(func() (err error) { manual, err = s.daemon.ManualActionStatus(); return })
	wg.Wait()

	itemRenderConfig := s.store.ItemRenderConfig()
	itemRenderConfigJSON, _ := json.Marshal(itemRenderConfig)

	return map[string]any{
		"Health":       health,
		"SysInfo":      sysInfo,
		"Identity":     ident,
		"Playfields":   playfields,
		"ManualAction": manual,
		"ActiveMounts": daemonclient.FilterMounts(mounts, cfg.Monitoring.EnabledMounts),

		"WarningPercent":  cfg.Monitoring.WarningPercent,
		"CriticalPercent": cfg.Monitoring.CriticalPercent,

		"DaemonUnreachable": s.daemon.Unreachable(),

		"SchedulerPaused": cfg.Scheduler.Paused,

		"Notifications": notifications,
		"UnreadCount":   unreadCount,

		"Username":  u.Username,
		"Role":      u.Role,
		"IsAdmin":   u.IsAdmin(),
		"Theme":     u.Theme,
		"Locale":    u.Locale,
		"Languages": availableLocales(),

		"Perms": s.store.ModeratorPermissions(),

		"ChatColors": s.store.ChatColors(),

		"ItemRenderConfig":     itemRenderConfig,
		"ItemRenderConfigJSON": template.JS(itemRenderConfigJSON),

		"DaemonVersion": ident.DaemonVersion,
		"UIVersion":     UIVersion,
	}
}

func (s *Server) handleActionServerTime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	t, err := s.daemon.ServerTime()
	if err != nil {
		t = time.Now().Format("02.01.2006 15:04:05")
	}
	fmt.Fprint(w, t)
}

func (s *Server) handleSchedulerPage(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)

	tasks, err := s.daemon.Tasks()
	if err != nil {
		log.Printf("scheduler: tasks: %v", err)
	}

	destinations, err := s.daemon.BackupDestinations()
	if err != nil {
		log.Printf("scheduler: destinations: %v", err)
	}
	hasSecondaryDestination := len(destinations) > 1 && destinations[1].Configured

	factions, err := s.daemon.Factions()
	if err != nil {
		log.Printf("scheduler: factions: %v", err)
	}

	scripts, err := s.daemon.Scripts()
	if err != nil {
		log.Printf("scheduler: scripts: %v", err)
	}

	wipeablePlayfields, err := s.daemon.WipeablePlayfields()
	if err != nil {
		log.Printf("scheduler: wipeable playfields: %v", err)
	}
	if wipeablePlayfields == nil {

		wipeablePlayfields = []string{}
	}

	playfieldSystems, err := s.daemon.PlayfieldSystems()
	if err != nil {
		log.Printf("scheduler: playfield systems: %v", err)
	}
	wipeableSystemsSet := make(map[string]bool)
	for _, p := range wipeablePlayfields {
		if sys, ok := playfieldSystems[p]; ok && sys != "" {
			wipeableSystemsSet[sys] = true
		}
	}
	wipeableSystems := make([]string, 0, len(wipeableSystemsSet))
	for sys := range wipeableSystemsSet {
		wipeableSystems = append(wipeableSystems, sys)
	}
	sort.Strings(wipeableSystems)

	allSystemsSet := make(map[string]bool, len(playfieldSystems))
	allPlayfields := make([]string, 0, len(playfieldSystems))
	for p, sys := range playfieldSystems {
		if sys == "" {
			continue
		}
		allSystemsSet[sys] = true
		allPlayfields = append(allPlayfields, p)
	}
	allSystems := make([]string, 0, len(allSystemsSet))
	for sys := range allSystemsSet {
		allSystems = append(allSystems, sys)
	}
	sort.Strings(allSystems)
	sort.Strings(allPlayfields)

	playfieldSystemsJSON, _ := json.Marshal(playfieldSystems)
	playfieldsJSON, _ := json.Marshal(wipeablePlayfields)
	allPlayfieldsJSON, _ := json.Marshal(allPlayfields)

	allSubtasks := make(map[int64][]subtaskView, len(tasks))
	for _, t := range tasks {
		subtasks, err := s.daemon.Subtasks(t.ID)
		if err != nil {
			log.Printf("scheduler: subtasks of task %d: %v", t.ID, err)
			continue
		}
		views := make([]subtaskView, len(subtasks))
		for i, st := range subtasks {
			views[i] = subtaskView{
				ID:            st.ID,
				Active:        st.Active,
				ActionType:    st.ActionType,
				Params:        st.Params,
				OffsetMinutes: st.OffsetMinutes,
				RunCondition:  st.RunCondition,
				Description:   subtaskDescriptionHTML(u.Locale, st, playfieldSystems),
				NextRun:       formatNextRunL(st.NextRun, s.daemonLocation()),
			}
		}
		allSubtasks[t.ID] = views
	}
	allSubtasksJSON, _ := json.Marshal(allSubtasks)

	data := map[string]any{
		"Username":                u.Username,
		"Tasks":                   tasks,
		"HasSecondaryDestination": hasSecondaryDestination,
		"Factions":                factions,
		"Scripts":                 scripts,
		"WipeableSystems":         wipeableSystems,
		"AllSystems":              allSystems,
		"WipeablePlayfieldsJSON":  template.JS(playfieldsJSON),
		"AllPlayfieldsJSON":       template.JS(allPlayfieldsJSON),
		"PlayfieldSystemsJSON":    template.JS(playfieldSystemsJSON),

		"PlayfieldSystems": playfieldSystems,
		"AllSubtasksJSON":  template.JS(allSubtasksJSON),

		"AllSubtasks": allSubtasks,
	}
	for k, v := range s.statusBarData(u) {
		data[k] = v
	}
	if err := s.execTemplate(w, r, "scheduler.html", data); err != nil {
		log.Printf("scheduler: render: %v", err)
	}
}

func decodeTask(r *http.Request) (daemonclient.Task, error) {
	var t daemonclient.Task
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		return daemonclient.Task{}, fmt.Errorf("некорректный запрос: %w", err)
	}
	return t, nil
}

func (s *Server) handleActionSchedulerCreate(w http.ResponseWriter, r *http.Request) {
	t, err := decodeTask(r)
	if err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	if _, err := s.daemon.CreateTask(t); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось создать задачу: %v", err))
		return
	}
	writeToast(w, "Задача создана")
}

func (s *Server) handleActionSchedulerUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeToast(w, "Некорректный id задачи")
		return
	}
	t, err := decodeTask(r)
	if err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	t.ID = id
	if _, err := s.daemon.UpdateTask(t); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось сохранить задачу: %v", err))
		return
	}
	writeToast(w, "Задача сохранена")
}

func (s *Server) handleActionSchedulerDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeToast(w, "Некорректный id задачи")
		return
	}
	if err := s.daemon.DeleteTask(id); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось удалить задачу: %v", err))
		return
	}
	writeToast(w, "Задача удалена")
}

func (s *Server) handleActionSchedulerClone(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeToast(w, "Некорректный id задачи")
		return
	}
	clone, err := s.daemon.CloneTask(id)
	if err != nil {
		writeToast(w, fmt.Sprintf("Не удалось клонировать задачу: %v", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"id": clone.ID})
}

func (s *Server) handleActionSchedulerRun(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeToast(w, "Некорректный id задачи")
		return
	}
	if err := s.daemon.RunTaskNow(id); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось запустить: %v", err))
		return
	}
	writeToast(w, "Запущено")
}

func (s *Server) handleActionSchedulerToggle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Paused bool `json:"paused"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	if err := s.daemon.SetSchedulerPaused(body.Paused); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось сохранить: %v", err))
		return
	}
	if body.Paused {
		writeToast(w, "Планировщик приостановлен")
	} else {
		writeToast(w, "Планировщик снова работает")
	}
}

type subtaskView struct {
	ID            int64                   `json:"id"`
	Active        bool                    `json:"active"`
	ActionType    string                  `json:"actionType"`
	Params        daemonclient.TaskParams `json:"params"`
	OffsetMinutes int                     `json:"offsetMinutes"`
	RunCondition  string                  `json:"runCondition"`
	Description   template.HTML           `json:"description"`
	NextRun       string                  `json:"nextRun"`
}

func formatNextRunL(ts string, loc *time.Location) string {
	if ts == "" {
		return "—"
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return "—"
	}
	return t.In(loc).Format("02.01.2006 15:04:05")
}

func decodeSubtask(r *http.Request) (daemonclient.Subtask, error) {
	var s daemonclient.Subtask
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		return daemonclient.Subtask{}, fmt.Errorf("некорректный запрос: %w", err)
	}
	return s, nil
}

func (s *Server) handleActionSchedulerSubtaskCreate(w http.ResponseWriter, r *http.Request) {
	taskID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeToast(w, "Некорректный id подзадачи")
		return
	}
	st, err := decodeSubtask(r)
	if err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	st.TaskID = taskID
	if _, err := s.daemon.CreateSubtask(st); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось создать подзадачу: %v", err))
		return
	}
	writeToast(w, "Подзадача создана")
}

func (s *Server) handleActionSchedulerSubtaskUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeToast(w, "Некорректный id подзадачи")
		return
	}
	st, err := decodeSubtask(r)
	if err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	st.ID = id
	if _, err := s.daemon.UpdateSubtask(st); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось сохранить подзадачу: %v", err))
		return
	}
	writeToast(w, "Подзадача сохранена")
}

func (s *Server) handleActionSchedulerSubtaskDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeToast(w, "Некорректный id подзадачи")
		return
	}
	if err := s.daemon.DeleteSubtask(id); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось удалить подзадачу: %v", err))
		return
	}
	writeToast(w, "Подзадача удалена")
}

func (s *Server) handleActionSchedulerSubtaskRun(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeToast(w, "Некорректный id подзадачи")
		return
	}
	if err := s.daemon.RunSubtaskNow(id); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось запустить: %v", err))
		return
	}
	writeToast(w, "Запущено")
}
