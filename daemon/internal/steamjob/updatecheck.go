package steamjob

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"elb/daemon/internal/config"
)

type ServerUpdateStatus struct {
	LocalBuildID    string `json:"localBuildId"`
	RemoteBuildID   string `json:"remoteBuildId"`
	UpdateAvailable bool   `json:"updateAvailable"`
}

var acfBuildIDRe = regexp.MustCompile(`"buildid"\s+"(\d+)"`)

func localBuildID(cfg config.Config) (string, error) {
	path := filepath.Join(cfg.DataRoot, "steamapps", fmt.Sprintf("appmanifest_%s.acf", cfg.SteamAppID))
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("local buildid: %w", err)
	}
	m := acfBuildIDRe.FindSubmatch(data)
	if m == nil {
		return "", fmt.Errorf("buildid not found in %s", path)
	}
	return string(m[1]), nil
}

const remoteBuildIDTimeout = 75 * time.Second

func remoteBuildID(cfg config.Config) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), remoteBuildIDTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, cfg.SteamcmdBin,
		"+login", "anonymous",
		"+app_info_update", "1",
		"+app_info_print", cfg.SteamAppID,
		"+quit",
	)
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return "", err
	}
	defer devNull.Close()
	cmd.Stdin = devNull
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("app_info_print: %w", err)
	}
	text := string(out)
	idx := strings.Index(text, `"public"`)
	if idx < 0 {
		return "", fmt.Errorf("\"public\" section not found in app_info_print output")
	}
	m := acfBuildIDRe.FindStringSubmatch(text[idx:])
	if m == nil {
		return "", fmt.Errorf("buildid not found after \"public\"")
	}
	return m[1], nil
}

func CheckServerUpdate(cfg config.Config) (*ServerUpdateStatus, error) {
	local, err := localBuildID(cfg)
	if err != nil {
		return nil, err
	}
	remote, err := remoteBuildID(cfg)
	if err != nil {
		return nil, err
	}
	return &ServerUpdateStatus{
		LocalBuildID:    local,
		RemoteBuildID:   remote,
		UpdateAvailable: local != remote,
	}, nil
}
