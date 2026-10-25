package identity

import (
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"elb/daemon/internal/config"
)

const Version = "0.0.113"

type Identity struct {
	ServerName    string `json:"serverName"`
	Port          string `json:"port,omitempty"`
	LocalIP       string `json:"localIp"`
	ExternalIP    string `json:"externalIp"`
	DaemonVersion string `json:"daemonVersion"`
}

var (
	mu         sync.RWMutex
	externalIP string
)

const (
	externalIPRetryInterval = time.Minute
	externalIPMaxAttempts   = 10
)

func RunPoller() {
	runPollerWithParams(externalIPRetryInterval, externalIPMaxAttempts, fetchExternalIPOnce)
}

func runPollerWithParams(interval time.Duration, maxAttempts int, fetch func() bool) {
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if fetch() {
			return
		}
		if attempt < maxAttempts {
			time.Sleep(interval)
		}
	}
}

func fetchExternalIPOnce() bool {
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return false
	}
	ip := strings.TrimSpace(string(body))
	if net.ParseIP(ip) == nil {
		return false
	}
	mu.Lock()
	externalIP = ip
	mu.Unlock()
	return true
}

func localIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return ""
	}
	return addr.IP.String()
}

func Get(cfg config.Config) Identity {
	name, _, _ := cfg.ServerField("Srv_Name")
	port, _, _ := cfg.ServerField("Srv_Port")
	mu.RLock()
	ext := externalIP
	mu.RUnlock()
	return Identity{
		ServerName:    name,
		Port:          port,
		LocalIP:       localIP(),
		ExternalIP:    ext,
		DaemonVersion: Version,
	}
}
