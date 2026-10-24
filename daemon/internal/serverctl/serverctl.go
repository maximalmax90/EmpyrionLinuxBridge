package serverctl

import (
	"os/exec"
	"strings"
	"sync"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/scenariocache"
	"elb/daemon/internal/telnet"
)

type Status struct {
	Active          bool   `json:"active"`
	TelnetOK        bool   `json:"telnet_ok"`
	Version         string `json:"version,omitempty"`
	Mode            string `json:"mode,omitempty"`
	Name            string `json:"name,omitempty"`
	Seed            string `json:"seed,omitempty"`
	StartInProgress bool   `json:"startInProgress"`

	ScenarioCache scenariocache.Status `json:"scenarioCache,omitzero"`
}

var (
	lastActionMu sync.Mutex
	lastActionAt time.Time
)

func markAction() {
	lastActionMu.Lock()
	lastActionAt = time.Now()
	lastActionMu.Unlock()
}

func TimeSinceLastAction() time.Duration {
	lastActionMu.Lock()
	defer lastActionMu.Unlock()
	if lastActionAt.IsZero() {
		return time.Duration(1<<63 - 1)
	}
	return time.Since(lastActionAt)
}

type ManualAction struct {
	Type     string    `json:"type"`
	Since    time.Time `json:"since"`
	Deadline time.Time `json:"deadline,omitempty"`
}

var (
	manualMu      sync.Mutex
	manualPending *ManualAction
)

func TryBeginManualAction(actionType string, warnMinutes int) bool {
	manualMu.Lock()
	defer manualMu.Unlock()
	if manualPending != nil {
		return false
	}
	deadline := time.Now()
	if warnMinutes > 0 {
		deadline = deadline.Add(time.Duration(warnMinutes) * time.Minute)
	}
	manualPending = &ManualAction{Type: actionType, Since: time.Now(), Deadline: deadline}
	return true
}

func EndManualAction() {
	manualMu.Lock()
	manualPending = nil
	manualMu.Unlock()
}

func ManualStatus() *ManualAction {
	manualMu.Lock()
	defer manualMu.Unlock()
	return manualPending
}

func Start(unit string) error {
	markAction()
	beginStart()
	return exec.Command("systemctl", "start", unit).Run()
}
func Stop(unit string) error { markAction(); return exec.Command("systemctl", "stop", unit).Run() }
func Restart(unit string) error {
	markAction()
	return exec.Command("systemctl", "restart", unit).Run()
}

func IsActive(unit string) bool {
	out, err := exec.Command("systemctl", "is-active", unit).Output()
	return err == nil && strings.TrimSpace(string(out)) == "active"
}

func IsStopped(cfg config.Config) bool {
	if IsActive(cfg.SystemdUnit) {
		return false
	}
	startMu.Lock()
	inProgress := startInProgress
	startMu.Unlock()
	if inProgress {
		return false
	}
	if ManualStatus() != nil {
		return false
	}
	return true
}

var (
	startMu         sync.Mutex
	startInProgress bool
	startBeganAt    time.Time
)

const startInProgressTimeout = 5 * time.Minute

func beginStart() {
	startMu.Lock()
	startInProgress = true
	startBeganAt = time.Now()
	startMu.Unlock()
}

func noteStartStatus(active bool) {
	startMu.Lock()
	defer startMu.Unlock()
	if !startInProgress {
		return
	}
	if active || time.Since(startBeganAt) > startInProgressTimeout {
		startInProgress = false
	}
}

func GetStatus(cfg config.Config) Status {
	active := IsActive(cfg.SystemdUnit)
	noteStartStatus(active)
	st := Status{Active: active}
	startMu.Lock()
	st.StartInProgress = startInProgress
	startMu.Unlock()
	if sc, err := scenariocache.ReadStatus(cfg); err == nil {
		st.ScenarioCache = sc
	}
	if !st.Active {
		return st
	}
	addr, pwd, err := cfg.TelnetCreds()
	if err != nil {
		return st
	}
	out, err := telnet.New(addr, pwd).Command("", 5*time.Second)
	if err != nil {
		return st
	}
	st.TelnetOK = true
	parseLoginBanner(out, &st)
	return st
}

func parseLoginBanner(banner string, st *Status) {
	for _, line := range strings.Split(banner, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "= Version:"):
			st.Version = strings.TrimSpace(strings.TrimPrefix(line, "= Version:"))
		case strings.HasPrefix(line, "= Mode:"):
			st.Mode = strings.TrimSpace(strings.TrimPrefix(line, "= Mode:"))
		case strings.HasPrefix(line, "= Name:"):
			st.Name = strings.TrimSpace(strings.TrimPrefix(line, "= Name:"))
		case strings.HasPrefix(line, "= Game seed:"):
			st.Seed = strings.TrimSpace(strings.TrimPrefix(line, "= Game seed:"))
		}
	}
}
