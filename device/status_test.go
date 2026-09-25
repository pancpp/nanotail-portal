package device

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

const firstStat = "cpu 100 10 20 800 40 5 5 20 80 10\ncpu0 1 2 3 4\nbtime 1700000000\n"
const secondStat = "cpu 130 10 40 840 50 5 5 20 1000 1000\nbtime 1700000000\n"

func testReader(t *testing.T) *Reader {
	t.Helper()
	r := NewReader()
	r.sampleInterval = 0
	r.hostname = func() (string, error) { return "nanotail", nil }
	r.runNM = func(_ context.Context, args ...string) ([]byte, error) {
		if args[len(args)-1] == "eth0" {
			return []byte(nmDeviceFixture), nil
		}
		return []byte("auto\nauto\n"), nil
	}
	r.interfaceInfo = func(name string) (net.HardwareAddr, []net.Addr, error) {
		if name != "eth0" {
			t.Fatalf("interface = %q, want eth0", name)
		}
		return net.HardwareAddr{2, 0, 0, 0, 0, 1}, []net.Addr{
			&net.IPNet{IP: net.ParseIP("192.0.2.2"), Mask: net.CIDRMask(24, 32)},
			&net.IPNet{IP: net.ParseIP("fd00::2"), Mask: net.CIDRMask(64, 128)},
			&net.IPNet{IP: net.ParseIP("fe80::2"), Mask: net.CIDRMask(64, 128)},
		}, nil
	}
	statReads := 0
	r.readFile = func(path string) ([]byte, error) {
		switch path {
		case "/proc/stat":
			statReads++
			if statReads == 1 {
				return []byte(firstStat), nil
			}
			return []byte(secondStat), nil
		case "/proc/meminfo":
			return []byte("MemTotal: 1000 kB\nMemFree: 100 kB\nMemAvailable: 750 kB\n"), nil
		case "/proc/uptime":
			return []byte("90061.99 300000.00\n"), nil
		default:
			t.Fatalf("unexpected file read: %s", path)
			return nil, nil
		}
	}
	return r
}

func TestReaderStatus(t *testing.T) {
	got, err := testReader(t).Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := Status{
		Hostname: "nanotail", LANIP: "192.0.2.2/24", LANIPv6: "fd00::2/64", LANIPType: "DHCP", LANIPv6Type: "auto",
		Gateway: "192.0.2.1", Gateway6: "fe80::1", DNS: []string{"192.0.2.53", "2001:db8::53"}, EthAddr: "02:00:00:00:00:01",
		CPULoad: 50, Memory: 25, LastRestart: time.Unix(1700000000, 0).UTC(), Uptime: 90061, Health: "healthy",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestReaderFailures(t *testing.T) {
	failure := errors.New("test OS failure")
	t.Run("hostname", func(t *testing.T) {
		r := testReader(t)
		r.hostname = func() (string, error) { return "", failure }
		if _, err := r.Status(t.Context()); !errors.Is(err, failure) {
			t.Fatalf("hostname error lost: %v", err)
		}
	})
	t.Run("missing interface", func(t *testing.T) {
		r := testReader(t)
		r.interfaceInfo = func(string) (net.HardwareAddr, []net.Addr, error) { return nil, nil, failure }
		if _, err := r.Status(t.Context()); !errors.Is(err, failure) {
			t.Fatalf("interface error lost: %v", err)
		}
	})
	t.Run("missing MAC", func(t *testing.T) {
		r := testReader(t)
		r.interfaceInfo = func(string) (net.HardwareAddr, []net.Addr, error) { return nil, nil, nil }
		if _, err := r.Status(t.Context()); err == nil {
			t.Fatal("missing MAC accepted")
		}
	})
	for _, path := range []string{"/proc/stat", "/proc/meminfo", "/proc/uptime"} {
		t.Run(path, func(t *testing.T) {
			for _, malformed := range []bool{false, true} {
				r := testReader(t)
				read := r.readFile
				r.readFile = func(name string) ([]byte, error) {
					if name == path {
						if malformed {
							return []byte("invalid"), nil
						}
						return nil, failure
					}
					return read(name)
				}
				status, err := r.Status(t.Context())
				if err == nil || (!malformed && !errors.Is(err, failure)) || status.Health != "" {
					t.Fatalf("failed metrics reported as success: %+v %v", status, err)
				}
			}
		})
	}
	t.Run("second CPU read", func(t *testing.T) {
		r := testReader(t)
		read := r.readFile
		calls := 0
		r.readFile = func(name string) ([]byte, error) {
			calls++
			if calls == 2 {
				return nil, failure
			}
			return read(name)
		}
		if _, err := r.Status(t.Context()); !errors.Is(err, failure) {
			t.Fatalf("second CPU read failure lost: %v", err)
		}
	})
}

func TestReaderCancellation(t *testing.T) {
	for _, beforeRead := range []bool{true, false} {
		r := testReader(t)
		r.sampleInterval = time.Hour
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		if beforeRead {
			cancel()
			r.hostname = func() (string, error) { t.Fatal("read after cancellation"); return "", nil }
		} else {
			read := r.readFile
			r.readFile = func(name string) ([]byte, error) { cancel(); return read(name) }
		}
		if _, err := r.Status(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation not propagated: %v", err)
		}
	}
}

func TestParseStat(t *testing.T) {
	cpu, boot, err := parseStat([]byte(firstStat))
	if err != nil || cpu != (cpuTimes{100, 10, 20, 800, 40, 5, 5, 20}) || boot.Unix() != 1700000000 {
		t.Fatalf("incorrect CPU or boot time: %v %v %v", cpu, boot, err)
	}
	for _, data := range []string{
		"", "cpu0 1 2 3 4\nbtime 1", "cpu 1 2 3\nbtime 1", "cpu 1 2 3 4",
		"cpu 1 2 invalid 4\nbtime 1", "cpu 1 2 -3 4\nbtime 1",
		"cpu 1 2 3 4\nbtime", "cpu 1 2 3 4\nbtime 0", "cpu 1 2 3 4\nbtime invalid",
	} {
		if _, _, err := parseStat([]byte(data)); err == nil {
			t.Errorf("accepted invalid stat data: %q", data)
		}
	}
}

func TestCPUPercent(t *testing.T) {
	for _, tt := range []struct {
		name          string
		before, after cpuTimes
		want          int32
		wantError     bool
	}{
		{"idle", cpuTimes{}, cpuTimes{3: 100}, 0, false},
		{"busy", cpuTimes{}, cpuTimes{100}, 100, false},
		{"rounding", cpuTimes{}, cpuTimes{1, 0, 0, 2}, 33, false},
		{"iowait is idle", cpuTimes{}, cpuTimes{1, 0, 0, 0, 3}, 25, false},
		{"decreasing iowait", cpuTimes{4: 20}, cpuTimes{10, 0, 0, 10, 19}, 50, false},
		{"counter reset", cpuTimes{100}, cpuTimes{1}, 0, true},
		{"no progress", cpuTimes{100}, cpuTimes{100}, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cpuPercent(tt.before, tt.after)
			if (err != nil) != tt.wantError || got != tt.want {
				t.Fatalf("got %d, %v; want %d, error=%v", got, err, tt.want, tt.wantError)
			}
		})
	}
}

func TestMemoryPercent(t *testing.T) {
	for _, tt := range []struct{ available, want int }{{0, 100}, {1000, 0}, {667, 33}, {5, 100}} {
		got, err := memoryPercent(fmt.Appendf(nil, "MemTotal: 1000 kB\nMemAvailable: %d kB\n", tt.available))
		if err != nil || int(got) != tt.want {
			t.Fatalf("available=%d: got %d, %v; want %d", tt.available, got, err, tt.want)
		}
	}
	for _, data := range []string{
		"", "MemTotal: 1000 kB", "MemAvailable: 500 kB", "MemTotal: 0 kB\nMemAvailable: 0 kB",
		"MemTotal: 100 kB\nMemAvailable: 101 kB", "MemTotal: -1 kB\nMemAvailable: 0 kB",
		"MemTotal: NaN kB\nMemAvailable: 0 kB", "MemTotal: 100 MB\nMemAvailable: 0 kB",
	} {
		if _, err := memoryPercent([]byte(data)); err == nil {
			t.Errorf("accepted invalid memory data: %q", data)
		}
	}
}

func TestParseUptime(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  int64
	}{{"0.00 0.00", 0}, {"90061.99 300000.00", 90061}, {"5000000000.50 1.00", 5000000000}} {
		got, err := parseUptime([]byte(tt.input))
		if err != nil || got != tt.want {
			t.Fatalf("uptime %q: got %d, %v", tt.input, got, err)
		}
	}
	for _, input := range []string{"", "invalid", "NaN", "Inf", "-1", "9223372036854775808"} {
		if _, err := parseUptime([]byte(input)); err == nil {
			t.Errorf("accepted invalid uptime: %q", input)
		}
	}
}

func TestReaderConcurrentRequests(t *testing.T) {
	r := testReader(t)
	read := r.readFile
	var samples atomic.Uint64
	r.readFile = func(path string) ([]byte, error) {
		if path == "/proc/stat" {
			n := samples.Add(1)
			return fmt.Appendf(nil, "cpu %d 0 0 %d 0 0 0 0\nbtime 1700000000\n", 3*n, 7*n), nil
		}
		return read(path)
	}
	results := make(chan error, 8)
	for range 8 {
		go func() {
			status, err := r.Status(t.Context())
			if err == nil && status.CPULoad != 30 {
				err = fmt.Errorf("CPU load = %d, want 30", status.CPULoad)
			}
			results <- err
		}()
	}
	for range 8 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
}
