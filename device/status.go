// Package device reads Linux system metrics and NetworkManager's eth0 status.
package device

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type Status struct {
	Hostname    string
	LANIPType   string
	LANIP       string
	Gateway     string
	DNS         []string
	LANIPv6Type string
	LANIPv6     string
	Gateway6    string
	EthAddr     string
	CPULoad     int32
	Memory      int32
	LastRestart time.Time
	Uptime      int64
	Health      string
}

// Reader samples each request independently, so concurrent callers do not share
// CPU counters. The private dependencies also allow tests without host access.
type Reader struct {
	readFile       func(string) ([]byte, error)
	hostname       func() (string, error)
	interfaceInfo  func(string) (net.HardwareAddr, []net.Addr, error)
	runNM          func(context.Context, ...string) ([]byte, error)
	sampleInterval time.Duration
}

func NewReader() *Reader {
	return &Reader{
		readFile: os.ReadFile, hostname: os.Hostname, sampleInterval: 200 * time.Millisecond, runNM: runNetworkManager,
		interfaceInfo: func(name string) (net.HardwareAddr, []net.Addr, error) {
			iface, err := net.InterfaceByName(name)
			if err != nil {
				return nil, nil, err
			}
			addrs, err := iface.Addrs()
			return iface.HardwareAddr, addrs, err
		},
	}
}

func (r *Reader) Status(ctx context.Context) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	hostname, err := r.hostname()
	if err != nil {
		return Status{}, fmt.Errorf("read hostname: %w", err)
	}
	mac, addrs, err := r.interfaceInfo("eth0")
	if err != nil {
		return Status{}, fmt.Errorf("read eth0: %w", err)
	}
	if len(mac) == 0 {
		return Status{}, errors.New("eth0 has no MAC address")
	}
	before, _, err := r.readStat()
	if err != nil {
		return Status{}, err
	}
	timer := time.NewTimer(r.sampleInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return Status{}, ctx.Err()
	case <-timer.C:
	}
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	after, boot, err := r.readStat()
	if err != nil {
		return Status{}, err
	}
	load, err := cpuPercent(before, after)
	if err != nil {
		return Status{}, err
	}
	meminfo, err := r.readFile("/proc/meminfo")
	if err != nil {
		return Status{}, fmt.Errorf("read memory: %w", err)
	}
	memory, err := memoryPercent(meminfo)
	if err != nil {
		return Status{}, err
	}
	uptimeData, err := r.readFile("/proc/uptime")
	if err != nil {
		return Status{}, fmt.Errorf("read uptime: %w", err)
	}
	uptime, err := parseUptime(uptimeData)
	if err != nil {
		return Status{}, err
	}
	network, err := r.readNetwork(ctx)
	if err != nil {
		return Status{}, err
	}
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	lanIP, lanIPv6 := selectInterfaceIPs(addrs)
	return Status{
		Hostname: hostname, LANIP: lanIP, LANIPv6: lanIPv6, EthAddr: mac.String(),
		LANIPType: network.ipv4Method, LANIPv6Type: network.ipv6Method,
		Gateway: network.gateway, Gateway6: network.gateway6, DNS: network.dns,
		CPULoad: load, Memory: memory, LastRestart: boot, Uptime: uptime,
		Health: "healthy", // Placeholder; no system-health checks are performed yet.
	}, nil
}

// The first eight counters include user, nice, system, idle, iowait, irq,
// softirq, and steal. Guest times are already counted in user/nice.
type cpuTimes [8]uint64

func (r *Reader) readStat() (cpuTimes, time.Time, error) {
	data, err := r.readFile("/proc/stat")
	if err != nil {
		return cpuTimes{}, time.Time{}, fmt.Errorf("read CPU statistics: %w", err)
	}
	return parseStat(data)
}

func parseStat(data []byte) (cpuTimes, time.Time, error) {
	var cpu cpuTimes
	var boot time.Time
	foundCPU := false
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "cpu":
			if len(fields) < 5 {
				return cpu, boot, errors.New("incomplete CPU counters")
			}
			for i := 0; i < len(cpu) && i+1 < len(fields); i++ {
				value, err := strconv.ParseUint(fields[i+1], 10, 64)
				if err != nil {
					return cpu, boot, errors.New("invalid CPU counter")
				}
				cpu[i] = value
			}
			foundCPU = true
		case "btime":
			if len(fields) != 2 {
				return cpu, boot, errors.New("invalid boot time")
			}
			seconds, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil || seconds <= 0 {
				return cpu, boot, errors.New("invalid boot time")
			}
			boot = time.Unix(seconds, 0).UTC()
		}
		if foundCPU && !boot.IsZero() {
			return cpu, boot, nil
		}
	}
	return cpu, boot, errors.New("missing CPU counters or boot time")
}

func cpuPercent(before, after cpuTimes) (int32, error) {
	var total, busy float64
	for i := range before {
		if after[i] < before[i] {
			// Linux documents that iowait can decrease. Ignore a negative delta.
			if i == 4 {
				continue
			}
			return 0, errors.New("CPU counters decreased during sampling")
		}
		delta := float64(after[i] - before[i])
		total += delta
		if i != 3 && i != 4 {
			busy += delta
		}
	}
	if total == 0 {
		return 0, errors.New("no CPU time elapsed during sampling")
	}
	return int32(math.Round(100 * busy / total)), nil
}

func memoryPercent(data []byte) (int32, error) {
	var total, available uint64
	var haveTotal, haveAvailable bool
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || (fields[0] != "MemTotal:" && fields[0] != "MemAvailable:") {
			continue
		}
		if len(fields) != 3 || fields[2] != "kB" {
			return 0, errors.New("invalid memory statistics")
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, errors.New("invalid memory value")
		}
		if fields[0] == "MemTotal:" {
			total, haveTotal = value, true
		} else {
			available, haveAvailable = value, true
		}
	}
	if !haveTotal || !haveAvailable || total == 0 || available > total {
		return 0, errors.New("missing or inconsistent memory statistics")
	}
	return int32(math.Round(100 * float64(total-available) / float64(total))), nil
}

func parseUptime(data []byte) (int64, error) {
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, errors.New("missing uptime")
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds >= float64(math.MaxInt64) {
		return 0, errors.New("invalid uptime")
	}
	return int64(seconds), nil
}
