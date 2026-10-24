package scenariocache

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/notify"
	"elb/daemon/internal/remotestore"
	"elb/daemon/internal/settings"
)

func isTransientNetworkError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{
		"network is unreachable",
		"no route to host",
		"no such host",
		"temporary failure in name resolution",
		"connection timed out",
		"i/o timeout",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

func withNetworkRetry(attempts int, delay time.Duration, fn func() error) error {
	var err error
	for i := 0; i < attempts; i++ {
		err = fn()
		if err == nil || !isTransientNetworkError(err) {
			return err
		}
		if i < attempts-1 {
			time.Sleep(delay)
		}
	}
	return err
}

const ZipName = "sharedData.zip"

type Phase string

const (
	PhaseIdle      Phase = ""
	PhasePackaging Phase = "packaging"
	PhaseUploading Phase = "uploading"
	PhaseVerifying Phase = "verifying"
	PhaseDone      Phase = "done"
	PhaseFailed    Phase = "failed"
)

type Status struct {
	Phase     Phase     `json:"phase"`
	Message   string    `json:"message,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (s Status) InProgress() bool {
	return s.Phase == PhasePackaging || s.Phase == PhaseUploading || s.Phase == PhaseVerifying
}

func statusPath(cfg config.Config) string {
	return filepath.Join(cfg.ScenarioCacheDir, "status.json")
}

func ReadStatus(cfg config.Config) (Status, error) {
	data, err := os.ReadFile(statusPath(cfg))
	if err != nil {
		if os.IsNotExist(err) {
			return Status{Phase: PhaseIdle}, nil
		}
		return Status{}, err
	}
	var s Status
	if err := json.Unmarshal(data, &s); err != nil {
		return Status{}, err
	}
	return s, nil
}

func writeStatus(cfg config.Config, phase Phase, message string) {
	s := Status{Phase: phase, Message: message, UpdatedAt: time.Now()}
	data, err := json.Marshal(s)
	if err != nil {
		log.Printf("scenariocache: marshaling status: %v", err)
		return
	}
	if err := os.WriteFile(statusPath(cfg), data, 0o644); err != nil {
		log.Printf("scenariocache: writing status: %v", err)
	}
}

func ownDestination(set settings.ScenarioCache) settings.BackupDestination {
	return settings.BackupDestination{
		Type:        "ftp",
		FTPProtocol: set.FTPProtocol,
		FTPHost:     set.FTPHost,
		FTPPort:     set.FTPPort,
		FTPUser:     set.FTPUser,
		FTPPassword: set.FTPPassword,
		FTPKey:      set.FTPKey,
		FTPPath:     set.FTPPath,
	}
}

func verifyDownloadable(url string, wantSize int64) error {
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Head(url)
	if err != nil {
		return fmt.Errorf("verify %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("verify %s: unexpected status %s", url, resp.Status)
	}
	if resp.ContentLength > 0 && resp.ContentLength != wantSize {
		return fmt.Errorf("verify %s: size mismatch (got %d, want %d)", url, resp.ContentLength, wantSize)
	}
	return nil
}

func Prepare(cfg config.Config, set settings.ScenarioCache) {
	if set.Mode == "" || set.Mode == "off" {

		if err := cfg.ClearGameField("SharedDataURL"); err != nil {
			log.Printf("scenariocache: clearing SharedDataURL on Mode=off: %v", err)
		}
		writeStatus(cfg, PhaseIdle, "")
		return
	}
	if set.PublicURLBase == "" {
		fallback(cfg, fmt.Errorf("scenario cache: Mode=%q but PublicURLBase is empty", set.Mode))
		return
	}

	writeStatus(cfg, PhasePackaging, "")
	dir, err := SharedDataDir(cfg)
	if err != nil {
		fallback(cfg, err)
		return
	}
	fp, err := Fingerprint(dir)
	if err != nil {
		fallback(cfg, err)
		return
	}
	destZip := filepath.Join(cfg.ScenarioCacheDir, ZipName)
	if existingFp, rerr := ReadStampedFingerprint(destZip); rerr != nil || existingFp != fp {

		log.Printf("scenariocache: SharedData changed, building new cache (%s)", dir)
		if berr := BuildZip(dir, destZip); berr != nil {
			fallback(cfg, berr)
			return
		}
		if werr := WriteStampedFingerprint(destZip, fp); werr != nil {
			fallback(cfg, werr)
			return
		}
	}

	info, err := os.Stat(destZip)
	if err != nil {
		fallback(cfg, err)
		return
	}

	if set.Mode == "own" {
		writeStatus(cfg, PhaseUploading, "")
		store, serr := remotestore.New(ownDestination(set))
		if serr != nil {
			fallback(cfg, serr)
			return
		}
		if perr := withNetworkRetry(6, 2*time.Second, func() error {
			return store.Put(destZip, ZipName, 0)
		}); perr != nil {
			fallback(cfg, perr)
			return
		}
	}

	writeStatus(cfg, PhaseVerifying, "")
	url := ComputeURL(set.PublicURLBase, fp)
	if verr := withNetworkRetry(6, 2*time.Second, func() error {
		return verifyDownloadable(url, info.Size())
	}); verr != nil {
		fallback(cfg, verr)
		return
	}

	if serr := cfg.SetGameField("SharedDataURL", "_"+url); serr != nil {
		fallback(cfg, serr)
		return
	}
	writeStatus(cfg, PhaseDone, "")
	log.Printf("scenariocache: prepared, SharedDataURL=%s", url)
}

func fallback(cfg config.Config, cause error) {
	if err := cfg.ClearGameField("SharedDataURL"); err != nil {
		log.Printf("scenariocache: fallback: clearing SharedDataURL: %v", err)
	}
	log.Printf("scenariocache: prepare failed, falling back to built-in SharedData transfer: %v", cause)
	if err := notify.Add(cfg, notify.LevelWarning, fmt.Sprintf("Кэш сценария: не удалось подготовить раздачу SharedData, используется встроенная передача движка (%v)", cause)); err != nil {
		log.Printf("scenariocache: fallback: notify: %v", err)
	}
	writeStatus(cfg, PhaseFailed, cause.Error())
}
