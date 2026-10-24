package identity

import "testing"

func TestRunPollerWithParams_StopsOnFirstSuccess(t *testing.T) {
	calls := 0
	runPollerWithParams(0, 10, func() bool {
		calls++
		return calls == 1
	})
	if calls != 1 {
		t.Errorf("calls = %d, хочу 1 (должен остановиться на первом же успехе)", calls)
	}
}

func TestRunPollerWithParams_RetriesUntilSuccess(t *testing.T) {
	calls := 0
	runPollerWithParams(0, 10, func() bool {
		calls++
		return calls == 3
	})
	if calls != 3 {
		t.Errorf("calls = %d, хочу 3 (должен повторять до успеха)", calls)
	}
}

func TestRunPollerWithParams_GivesUpAfterMaxAttempts(t *testing.T) {
	calls := 0
	runPollerWithParams(0, 5, func() bool {
		calls++
		return false
	})
	if calls != 5 {
		t.Errorf("calls = %d, хочу 5 (должен остановиться после maxAttempts неудач)", calls)
	}
}
