package scenariocache

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/settings"
)

type Info struct {
	Active    bool      `json:"active"`
	BuiltAt   time.Time `json:"builtAt,omitzero"`
	SizeBytes int64     `json:"sizeBytes,omitempty"`
	URL       string    `json:"url,omitempty"`
}

func ComputeURL(publicURLBase, fingerprint string) string {
	return strings.TrimRight(publicURLBase, "/") + "/" + ZipName + "?v=" + fingerprint
}

func ReadInfo(cfg config.Config, set settings.ScenarioCache) Info {
	if set.Mode == "" || set.Mode == "off" {
		return Info{}
	}
	info := Info{Active: true}
	destZip := filepath.Join(cfg.ScenarioCacheDir, ZipName)
	stat, err := os.Stat(destZip)
	if err != nil {
		return info
	}
	info.BuiltAt = stat.ModTime()
	info.SizeBytes = stat.Size()
	if fp, ferr := ReadStampedFingerprint(destZip); ferr == nil && set.PublicURLBase != "" {
		info.URL = ComputeURL(set.PublicURLBase, fp)
	}
	return info
}
