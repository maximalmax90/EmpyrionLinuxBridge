package watchdog

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"elb/daemon/internal/config"
)

func HangDiagnosticsDir(cfg config.Config) string {
	return filepath.Join(cfg.DataRoot, "elb-diagnostics")
}

func writeDiagnostics(cfg config.Config, badSince time.Time) {
	dir := HangDiagnosticsDir(cfg)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("watchdog: diagnostics: mkdir %s: %v", dir, err)
		return
	}
	path := filepath.Join(dir, badSince.Format("20060102-150405")+".txt")

	var sb strings.Builder
	fmt.Fprintf(&sb, "ELB watchdog — снимок зависания\nОбнаружено: %s\n\n", badSince.Format(time.RFC3339))

	pid, err := findGamePID()
	if err != nil {
		fmt.Fprintf(&sb, "PID EmpyrionDedicated.exe: не найден (%v)\n", err)
	} else {
		fmt.Fprintf(&sb, "PID EmpyrionDedicated.exe: %d\n\n--- /proc/%d/status ---\n", pid, pid)
		if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid)); err == nil {
			sb.Write(data)
		} else {
			fmt.Fprintf(&sb, "(не прочитать: %v)\n", err)
		}

		sb.WriteString("\n--- потоки (tid  comm  state  wchan) ---\n")
		appendThreadStates(&sb, pid)

		sb.WriteString("\n--- cgroup ---\n")
		appendCgroupState(&sb, pid)
	}

	sb.WriteString("\n--- systemctl status " + cfg.SystemdUnit + " ---\n")
	appendCommandOutput(&sb, "systemctl", "status", cfg.SystemdUnit, "--no-pager", "-n", "0")

	sb.WriteString("\n--- journalctl (последние 5 минут, все юниты) ---\n")
	appendCommandOutput(&sb, "journalctl", "--since", "-5min", "--no-pager")

	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		log.Printf("watchdog: diagnostics: write %s: %v", path, err)
		return
	}
	log.Printf("watchdog: hang diagnostics snapshot written to %s", path)
}

const hangDiagnosticsRetentionDays = 14

func CleanHangDiagnosticsOnce(cfg config.Config) error {
	dir := HangDiagnosticsDir(cfg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	cutoff := time.Now().Add(-hangDiagnosticsRetentionDays * 24 * time.Hour)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if err := os.Remove(p); err != nil {
			log.Printf("watchdog: diagnostics: removing stale snapshot %s: %v", p, err)
			continue
		}
		log.Printf("watchdog: diagnostics: removed stale snapshot %s (older than %d days)", p, hangDiagnosticsRetentionDays)
	}
	return nil
}

func findGamePID() (int, error) {
	out, err := exec.Command("pgrep", "-f", "EmpyrionDedicated.exe").Output()
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0, fmt.Errorf("pgrep: пусто")
	}
	return strconv.Atoi(fields[0])
}

func appendThreadStates(sb *strings.Builder, pid int) {
	entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/task", pid))
	if err != nil {
		fmt.Fprintf(sb, "(не прочитать task/: %v)\n", err)
		return
	}
	type row struct{ tid, comm, state, wchan string }
	var rows []row
	for _, e := range entries {
		tid := e.Name()
		statRaw, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%s/stat", pid, tid))
		comm, state := "?", "?"
		if err == nil {

			s := string(statRaw)
			open, close := strings.Index(s, "("), strings.LastIndex(s, ")")
			if open >= 0 && close > open {
				comm = s[open+1 : close]
				rest := strings.Fields(s[close+1:])
				if len(rest) > 0 {
					state = rest[0]
				}
			}
		}
		wchanRaw, _ := os.ReadFile(fmt.Sprintf("/proc/%d/task/%s/wchan", pid, tid))
		rows = append(rows, row{tid, comm, state, string(wchanRaw)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].state < rows[j].state })
	for _, r := range rows {
		fmt.Fprintf(sb, "%-8s %-24s %-3s %s\n", r.tid, r.comm, r.state, r.wchan)
	}
}

func appendCgroupState(sb *strings.Builder, pid int) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil {
		fmt.Fprintf(sb, "(не прочитать cgroup: %v)\n", err)
		return
	}
	sb.Write(data)
	line := strings.TrimSpace(string(data))
	parts := strings.SplitN(line, ":", 3)
	if len(parts) != 3 {
		return
	}
	freezePath := filepath.Join("/sys/fs/cgroup", parts[2], "cgroup.freeze")
	if fr, err := os.ReadFile(freezePath); err == nil {
		fmt.Fprintf(sb, "cgroup.freeze: %s", fr)
	}
	eventsPath := filepath.Join("/sys/fs/cgroup", parts[2], "cgroup.events")
	if ev, err := os.ReadFile(eventsPath); err == nil {
		fmt.Fprintf(sb, "cgroup.events:\n%s", ev)
	}
}

func appendCommandOutput(sb *strings.Builder, name string, args ...string) {
	out, err := exec.Command(name, args...).CombinedOutput()
	sb.Write(out)
	if err != nil {
		fmt.Fprintf(sb, "(команда завершилась с ошибкой: %v)\n", err)
	}
}
