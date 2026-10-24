package steamjob

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/fsowner"
	"elb/daemon/internal/safego"
)

const maxOutputLines = 200

type Job struct {
	Kind      string    `json:"kind"`
	State     string    `json:"state"`
	Message   string    `json:"message,omitempty"`
	Progress  float64   `json:"progress"`
	Output    []string  `json:"output"`
	StartedAt time.Time `json:"startedAt"`
}

var (
	mu      sync.Mutex
	current *Job
)

func TryBegin(kind string) (*Job, bool) {
	mu.Lock()
	defer mu.Unlock()
	if current != nil && (current.State == "running" || current.State == "awaiting_code") {
		return nil, false
	}
	current = &Job{Kind: kind, State: "running", Progress: -1, StartedAt: time.Now()}
	return current, true
}

func Current() *Job {
	mu.Lock()
	defer mu.Unlock()
	if current == nil {
		return nil
	}
	cp := *current
	cp.Output = append([]string(nil), current.Output...)
	return &cp
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func appendOutput(line string) {
	line = ansiRe.ReplaceAllString(line, "")
	mu.Lock()
	defer mu.Unlock()
	if current == nil {
		return
	}
	current.Output = append(current.Output, line)
	if len(current.Output) > maxOutputLines {
		current.Output = current.Output[len(current.Output)-maxOutputLines:]
	}
}

func setProgress(p float64) {
	mu.Lock()
	defer mu.Unlock()
	if current != nil {
		current.Progress = p
	}
}

func setState(state, message string) {
	mu.Lock()
	defer mu.Unlock()
	if current != nil {
		current.State = state
		current.Message = message
	}
}

var errorMarkerRe = regexp.MustCompile(`ERROR|FAILED|Invalid Password|No subscription`)

func lastOutputLine() string {
	mu.Lock()
	defer mu.Unlock()
	if current == nil {
		return ""
	}
	for i := len(current.Output) - 1; i >= 0; i-- {
		if errorMarkerRe.MatchString(current.Output[i]) {
			return current.Output[i]
		}
	}
	for i := len(current.Output) - 1; i >= 0; i-- {
		if strings.TrimSpace(current.Output[i]) != "" {
			return current.Output[i]
		}
	}
	return ""
}

var progressRe = regexp.MustCompile(`progress:\s*([\d.]+)`)

func dataOwnerCredential(cfg config.Config) (*syscall.Credential, []string, error) {
	uid, gid, err := fsowner.ReferenceOwner(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("владелец %s: %w", cfg.DataRoot, err)
	}
	u, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		return nil, nil, fmt.Errorf("поиск пользователя uid=%d (владелец %s): %w", uid, cfg.DataRoot, err)
	}
	cred := &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
	env := append(os.Environ(), "HOME="+u.HomeDir, "USER="+u.Username, "LOGNAME="+u.Username)
	return cred, env, nil
}

func runSteamcmdAsDataOwner(cfg config.Config, args []string) error {
	cred, env, err := dataOwnerCredential(cfg)
	if err != nil {
		return err
	}
	cmd := exec.Command(cfg.SteamcmdBin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: cred}
	cmd.Env = env
	return runSteamcmdCmd(cmd)
}

func runSteamcmdCmd(cmd *exec.Cmd) error {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return fmt.Errorf("открыть %s: %w", os.DevNull, err)
	}
	defer devNull.Close()
	cmd.Stdin = devNull

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("запуск steamcmd: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		appendOutput(line)
		if m := progressRe.FindStringSubmatch(line); m != nil {
			if p, err := strconv.ParseFloat(m[1], 64); err == nil {
				setProgress(p)
			}
		}
	}
	return cmd.Wait()
}

func RunWithCachedSession(cfg config.Config, kind, username string, args ...string) bool {
	if _, ok := TryBegin(kind); !ok {
		return false
	}
	full := append([]string{"+login", username}, args...)
	full = append(full, "+quit")
	safego.Go("steamjob.RunWithCachedSession", func() {
		err := runSteamcmdAsDataOwner(cfg, full)
		if err != nil {
			msg := lastOutputLine()
			if msg == "" {
				msg = err.Error()
			}
			setState("failed", msg)
			return
		}
		setState("success", "")
	})
	return true
}

func UpdateServer(cfg config.Config) bool {
	if _, ok := TryBegin("update_server"); !ok {
		return false
	}
	safego.Go("steamjob.UpdateServer", func() {

		fieldSnapshot, snapshotErr := config.SnapshotActiveFields(cfg.DedicatedYAML)
		if snapshotErr != nil {
			appendOutput(fmt.Sprintf("предупреждение: не удалось снять снимок %s перед обновлением: %v", cfg.DedicatedYAML, snapshotErr))
		}
		defer func() {
			if len(fieldSnapshot) == 0 {
				return
			}
			if err := config.MergeActiveFields(cfg.DedicatedYAML, fieldSnapshot); err != nil {
				appendOutput(fmt.Sprintf("ВНИМАНИЕ: не удалось перенести настройки в %s после обновления: %v", cfg.DedicatedYAML, err))
			}
		}()

		defer reassertCrashHandlersDisabled(cfg)

		args := []string{
			"+force_install_dir", cfg.DataRoot,
			"+login", "anonymous",
			"+app_update", cfg.SteamAppID, "validate",
			"+quit",
		}
		err := runSteamcmdAsDataOwner(cfg, args)

		if err != nil {
			appendOutput("--- предыдущая попытка не удалась, повторяю ещё раз (частый транзиентный сбой steamcmd) ---")
			err = runSteamcmdAsDataOwner(cfg, args)
		}
		if err != nil {
			msg := lastOutputLine()
			if msg == "" {
				msg = err.Error()
			}
			setState("failed", msg)
			return
		}

		if chownErr := fsowner.FixTree(cfg, cfg.DataRoot, "elb", "steamapps"); chownErr != nil {
			appendOutput(fmt.Sprintf("предупреждение: не удалось выставить владельца после обновления: %v", chownErr))
		}
		setProgress(100)
		setState("success", "")
	})
	return true
}

var crashHandlerRelPaths = []string{
	filepath.Join("DedicatedServer", "UnityCrashHandler64.exe"),
	filepath.Join("PlayfieldServer", "UnityCrashHandler64.exe"),
}

func reassertCrashHandlersDisabled(cfg config.Config) {
	for _, rel := range crashHandlerRelPaths {
		path := filepath.Join(cfg.DataRoot, rel)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if err := os.Remove(path); err != nil {
			appendOutput(fmt.Sprintf("ВНИМАНИЕ: не удалось удалить %s: %v", path, err))
		}
	}
}
