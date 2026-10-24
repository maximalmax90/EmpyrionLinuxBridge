package permissions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

var (
	mutedMu   sync.Mutex
	mutedPath string
	muted     map[string]time.Time
)

func InitMuted(path string) {
	mutedMu.Lock()
	defer mutedMu.Unlock()
	mutedPath = path
	muted = loadMutedFile(path)
}

func loadMutedFile(path string) map[string]time.Time {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]time.Time{}
	}
	out := map[string]time.Time{}
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]time.Time{}
	}
	return out
}

func saveMutedFileLocked() error {
	if mutedPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(mutedPath), 0o777); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(mutedPath), err)
	}
	data, err := json.MarshalIndent(muted, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(mutedPath, data, 0o666)
}

func SetMuted(steamID string, until time.Time) error {
	mutedMu.Lock()
	defer mutedMu.Unlock()
	if muted == nil {
		muted = map[string]time.Time{}
	}
	muted[steamID] = until
	return saveMutedFileLocked()
}

func ClearMuted(steamID string) error {
	mutedMu.Lock()
	defer mutedMu.Unlock()
	delete(muted, steamID)
	return saveMutedFileLocked()
}

func MutedUntil(steamID string) (time.Time, bool) {
	mutedMu.Lock()
	defer mutedMu.Unlock()
	until, ok := muted[steamID]
	if !ok || !until.After(time.Now()) {
		return time.Time{}, false
	}
	return until, true
}

func DurationFromEngineString(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("некорректная длительность %q", s)
	}
	unit := s[len(s)-1]
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n < 0 {
		return 0, fmt.Errorf("некорректная длительность %q", s)
	}
	switch unit {
	case 'h':
		return time.Duration(n) * time.Hour, nil
	case 'd':
		return time.Duration(n) * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("неподдерживаемая единица длительности %q", s)
	}
}
