package watchdog

import (
	"log"
	"os"
	"os/exec"
)

const (
	ruleFile    = "/etc/audit/rules.d/elb-signal-watch.rules"
	ruleContent = "-a always,exit -F arch=b64 -S kill -S tgkill -S tkill -k elb_signal_watch\n"
)

func SyncAuditRule(enabled bool) {
	if _, err := exec.LookPath("augenrules"); err != nil {
		log.Printf("watchdog: auditd (augenrules) not found — elb_signal_watch rule skipped (see installer/README.ru.md for setup)")
		return
	}

	_, statErr := os.Stat(ruleFile)
	exists := statErr == nil

	if enabled == exists {
		return
	}

	if enabled {
		if err := os.MkdirAll("/etc/audit/rules.d", 0o750); err != nil {
			log.Printf("watchdog: auditd: mkdir /etc/audit/rules.d: %v", err)
			return
		}
		if err := os.WriteFile(ruleFile, []byte(ruleContent), 0o640); err != nil {
			log.Printf("watchdog: auditd: write %s: %v", ruleFile, err)
			return
		}
	} else {
		if err := os.Remove(ruleFile); err != nil && !os.IsNotExist(err) {
			log.Printf("watchdog: auditd: remove %s: %v", ruleFile, err)
			return
		}
	}

	if out, err := exec.Command("augenrules", "--load").CombinedOutput(); err != nil {
		log.Printf("watchdog: auditd: augenrules --load: %v\n%s", err, out)
		return
	}
	log.Printf("watchdog: auditd: elb_signal_watch rule %s", map[bool]string{true: "enabled", false: "disabled"}[enabled])
}
