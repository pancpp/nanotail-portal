package device

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// TrafficSample counts IP traffic through the VPN interface, not eth0 traffic
// or encrypted transport overhead. Counters belong to this interface lifetime.
type TrafficSample struct {
	InterfaceName    string
	RxBytes, TxBytes uint64
	SampledAt        time.Time
	CounterEpoch     string
}

type TrafficReader struct {
	readFile func(string) ([]byte, error)
	now      func() time.Time
}

func NewTrafficReader() *TrafficReader {
	return &TrafficReader{readFile: os.ReadFile, now: time.Now}
}

func (r *TrafficReader) Sample(ctx context.Context) (TrafficSample, error) {
	read := func(path string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		data, err := r.readFile(path)
		return strings.TrimSpace(string(data)), err
	}
	const root = "/sys/class/net/tailscale0/"
	index, err := read(root + "ifindex")
	if err != nil {
		return TrafficSample{}, err
	}
	if value, err := parseTrafficCounter(index); err != nil || value == 0 {
		return TrafficSample{}, errors.New("invalid tailscale0 interface index")
	}
	boot, err := read("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return TrafficSample{}, err
	}
	if !profileUUID.MatchString(boot) {
		return TrafficSample{}, errors.New("invalid boot ID")
	}
	counters := [2]uint64{}
	for i, name := range []string{"rx_bytes", "tx_bytes"} {
		value, err := read(root + "statistics/" + name)
		if err != nil {
			return TrafficSample{}, err
		}
		counters[i], err = parseTrafficCounter(value)
		if err != nil {
			return TrafficSample{}, fmt.Errorf("invalid tailscale0 %s: %w", name, err)
		}
	}
	// Do not combine counters from two interfaces if tailscaled recreated it
	// between reads. Consumers also use the epoch to reset their rate baseline.
	after, err := read(root + "ifindex")
	if err != nil {
		return TrafficSample{}, err
	}
	if after != index {
		return TrafficSample{}, errors.New("tailscale0 changed while reading counters")
	}
	if err := ctx.Err(); err != nil {
		return TrafficSample{}, err
	}
	return TrafficSample{
		InterfaceName: "tailscale0", RxBytes: counters[0], TxBytes: counters[1],
		SampledAt: r.now().UTC(), CounterEpoch: boot + ":" + index,
	}, nil
}

func parseTrafficCounter(value string) (uint64, error) {
	if value == "" || strings.ContainsFunc(value, func(r rune) bool { return r < '0' || r > '9' }) {
		return 0, errors.New("expected an unsigned decimal counter")
	}
	return strconv.ParseUint(value, 10, 64)
}
