package sysinfo

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"elb/daemon/internal/safego"
)

type Snapshot struct {
	CPUPercent     float64 `json:"cpuPercent"`
	MemUsedBytes   uint64  `json:"memUsedBytes"`
	MemTotalBytes  uint64  `json:"memTotalBytes"`
	SwapUsedBytes  uint64  `json:"swapUsedBytes"`
	SwapTotalBytes uint64  `json:"swapTotalBytes"`
	DiskUsedBytes  uint64  `json:"diskUsedBytes"`
	DiskTotalBytes uint64  `json:"diskTotalBytes"`

	IOWaitPercent float64 `json:"ioWaitPercent"`
}

var (
	mu                sync.Mutex
	lastCPUPercent    float64
	lastIOWaitPercent float64
)

func RunSampler() {
	prevIdle, prevIowait, prevTotal, err := readCPUStat()
	if err != nil {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		safego.Tick("sysinfo.RunSampler", func() {
			idle, iowait, total, err := readCPUStat()
			if err != nil {
				return
			}
			deltaTotal := total - prevTotal
			if deltaTotal > 0 {
				deltaIdle := idle - prevIdle
				deltaIowait := iowait - prevIowait
				mu.Lock()
				lastCPUPercent = 100 * (1 - float64(deltaIdle)/float64(deltaTotal))
				lastIOWaitPercent = 100 * float64(deltaIowait) / float64(deltaTotal)
				mu.Unlock()
			}
			prevIdle, prevIowait, prevTotal = idle, iowait, total
		})
	}
}

func readCPUStat() (idle, iowait, total uint64, err error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return 0, 0, 0, fmt.Errorf("пустой /proc/stat")
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, 0, fmt.Errorf("неожиданный формат /proc/stat: %q", scanner.Text())
	}
	var vals []uint64
	for _, f := range fields[1:] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return 0, 0, 0, err
		}
		vals = append(vals, v)
		total += v
	}

	if len(vals) > 4 {
		iowait = vals[4]
		idle = vals[3] + iowait
	} else if len(vals) > 3 {
		idle = vals[3]
	}
	return idle, iowait, total, nil
}

func Read(diskPath string) (Snapshot, error) {
	mu.Lock()
	cpu := lastCPUPercent
	iowait := lastIOWaitPercent
	mu.Unlock()

	memUsed, memTotal, swapUsed, swapTotal, err := readMemInfo()
	if err != nil {
		return Snapshot{}, fmt.Errorf("meminfo: %w", err)
	}

	diskUsed, diskTotal, err := DiskUsage(diskPath)
	if err != nil {
		return Snapshot{}, fmt.Errorf("disk %s: %w", diskPath, err)
	}

	return Snapshot{
		CPUPercent:     cpu,
		MemUsedBytes:   memUsed,
		MemTotalBytes:  memTotal,
		SwapUsedBytes:  swapUsed,
		SwapTotalBytes: swapTotal,
		DiskUsedBytes:  diskUsed,
		DiskTotalBytes: diskTotal,
		IOWaitPercent:  iowait,
	}, nil
}

func readMemInfo() (used, total, swapUsed, swapTotal uint64, err error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, 0, 0, err
	}
	defer f.Close()

	var totalKB, availableKB, swapTotalKB, swapFreeKB uint64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		var key string
		var val uint64
		if _, err := fmt.Sscanf(line, "%s %d", &key, &val); err != nil {
			continue
		}
		switch key {
		case "MemTotal:":
			totalKB = val
		case "MemAvailable:":
			availableKB = val
		case "SwapTotal:":
			swapTotalKB = val
		case "SwapFree:":
			swapFreeKB = val
		}
	}
	if totalKB == 0 {
		return 0, 0, 0, 0, fmt.Errorf("MemTotal not found")
	}
	total = totalKB * 1024
	available := availableKB * 1024
	if available > total {
		available = total
	}
	used = total - available

	swapTotal = swapTotalKB * 1024
	swapFree := swapFreeKB * 1024
	if swapFree > swapTotal {
		swapFree = swapTotal
	}
	swapUsed = swapTotal - swapFree
	return used, total, swapUsed, swapTotal, nil
}

type MountInfo struct {
	Mountpoint string `json:"mountpoint"`
	FSType     string `json:"fsType"`
	Device     string `json:"device"`
	UsedBytes  uint64 `json:"usedBytes"`
	TotalBytes uint64 `json:"totalBytes"`
}

var pseudoFSTypes = map[string]bool{
	"proc": true, "sysfs": true, "devtmpfs": true, "tmpfs": true, "devpts": true,
	"cgroup": true, "cgroup2": true, "overlay": true, "squashfs": true, "autofs": true,
	"debugfs": true, "tracefs": true, "securityfs": true, "pstore": true, "efivarfs": true,
	"bpf": true, "configfs": true, "mqueue": true, "fusectl": true, "binfmt_misc": true,
	"hugetlbfs": true, "rpc_pipefs": true, "nsfs": true, "ramfs": true,
}

var pseudoMountPrefixes = []string{"/proc", "/sys", "/dev", "/run"}

func ListMounts() ([]MountInfo, error) {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	seen := map[string]bool{}
	var out []MountInfo
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		device, mountpoint, fsType := fields[0], fields[1], fields[2]
		if pseudoFSTypes[fsType] || seen[mountpoint] {
			continue
		}
		skip := false
		for _, p := range pseudoMountPrefixes {
			if mountpoint == p || strings.HasPrefix(mountpoint, p+"/") {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		seen[mountpoint] = true
		used, total, err := DiskUsage(mountpoint)
		if err != nil {
			continue
		}
		out = append(out, MountInfo{Mountpoint: mountpoint, FSType: fsType, Device: device, UsedBytes: used, TotalBytes: total})
	}
	return out, scanner.Err()
}

func DiskUsage(path string) (used, total uint64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	total = stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	if free > total {
		free = total
	}
	used = total - free
	return used, total, nil
}

type NetIface struct {
	Name    string
	RxBytes uint64
	TxBytes uint64
}

const defaultNetRoot = "/sys/class/net"

func ReadNetIfaces() ([]NetIface, error) { return ReadNetIfacesFrom(defaultNetRoot) }

func ReadNetIfacesFrom(netRoot string) ([]NetIface, error) {
	entries, err := os.ReadDir(netRoot)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []NetIface
	for _, e := range entries {
		name := e.Name()
		if name == "lo" {
			continue
		}
		target, errLink := filepath.EvalSymlinks(filepath.Join(netRoot, name))
		if errLink == nil && strings.Contains(filepath.ToSlash(target), "/devices/virtual/net/") {
			continue
		}
		rx, errRx := readUintFile(filepath.Join(netRoot, name, "statistics", "rx_bytes"))
		tx, errTx := readUintFile(filepath.Join(netRoot, name, "statistics", "tx_bytes"))
		if errRx != nil || errTx != nil {
			continue
		}
		out = append(out, NetIface{Name: name, RxBytes: rx, TxBytes: tx})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func readUintFile(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
}
