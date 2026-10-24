package serverctl

import (
	"testing"
	"time"
)

func resetStartState() {
	startMu.Lock()
	startInProgress = false
	startBeganAt = time.Time{}
	startMu.Unlock()
}

func startFlagLocked() bool {
	startMu.Lock()
	defer startMu.Unlock()
	return startInProgress
}

func TestStartInProgress_ClearsOnceActive(t *testing.T) {
	resetStartState()
	beginStart()
	if !startFlagLocked() {
		t.Fatal("beginStart должен сразу выставить startInProgress")
	}
	noteStartStatus(false)
	if !startFlagLocked() {
		t.Fatal("noteStartStatus(false) не должен снимать флаг — сервер ещё не Active")
	}
	noteStartStatus(true)
	if startFlagLocked() {
		t.Fatal("noteStartStatus(true) должен снять флаг — сервер стал Active")
	}
}

func TestStartInProgress_TimeoutSafetyNet(t *testing.T) {
	resetStartState()
	beginStart()
	startMu.Lock()
	startBeganAt = time.Now().Add(-startInProgressTimeout - time.Second)
	startMu.Unlock()
	noteStartStatus(false)
	if startFlagLocked() {
		t.Fatal("safety-net таймаут должен снять флаг, даже если сервер не стал Active")
	}
}

func TestStartInProgress_NoopWhenNotStarted(t *testing.T) {
	resetStartState()
	noteStartStatus(true)
	if startFlagLocked() {
		t.Fatal("noteStartStatus без предшествующего beginStart не должен выставлять флаг")
	}
}
