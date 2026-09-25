package device

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

const trafficBoot = "12345678-1234-1234-1234-123456789abc"

func trafficFiles() map[string]string {
	return map[string]string{
		"/sys/class/net/tailscale0/ifindex":             "3\n",
		"/sys/class/net/tailscale0/statistics/rx_bytes": "18446744073709551615\n",
		"/sys/class/net/tailscale0/statistics/tx_bytes": "0\n",
		"/proc/sys/kernel/random/boot_id":               trafficBoot + "\n",
	}
}

func testTrafficReader(t *testing.T, files map[string]string) *TrafficReader {
	t.Helper()
	return &TrafficReader{
		readFile: func(path string) ([]byte, error) {
			value, ok := files[path]
			if !ok {
				t.Fatalf("unexpected read: %s", path)
			}
			return []byte(value), nil
		},
		now: func() time.Time { return time.Unix(1700000000, 123000000) },
	}
}

func TestTrafficSample(t *testing.T) {
	got, err := testTrafficReader(t, trafficFiles()).Sample(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := TrafficSample{InterfaceName: "tailscale0", RxBytes: math.MaxUint64, TxBytes: 0, SampledAt: time.Unix(1700000000, 123000000).UTC(), CounterEpoch: trafficBoot + ":3"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestTrafficFailures(t *testing.T) {
	for path := range trafficFiles() {
		t.Run(path, func(t *testing.T) {
			r := testTrafficReader(t, trafficFiles())
			read := r.readFile
			r.readFile = func(p string) ([]byte, error) {
				if p == path {
					return nil, errors.New("private system error")
				}
				return read(p)
			}
			if got, err := r.Sample(t.Context()); err == nil || got != (TrafficSample{}) {
				t.Fatalf("failure returned partial counters: %+v %v", got, err)
			}
		})
	}
	for path := range trafficFiles() {
		for _, value := range []string{"", "-1", "+1", "1.2", "1e3", "1 2", "18446744073709551616"} {
			files := trafficFiles()
			files[path] = value
			if _, err := testTrafficReader(t, files).Sample(t.Context()); err == nil {
				t.Errorf("accepted invalid %s: %q", path, value)
			}
		}
	}
	files := trafficFiles()
	files["/sys/class/net/tailscale0/ifindex"] = "0"
	if _, err := testTrafficReader(t, files).Sample(t.Context()); err == nil {
		t.Error("accepted zero interface index")
	}
}

func TestTrafficInterfaceReplacement(t *testing.T) {
	r := testTrafficReader(t, trafficFiles())
	read, indices := r.readFile, 0
	r.readFile = func(path string) ([]byte, error) {
		if strings.HasSuffix(path, "/ifindex") {
			indices++
			if indices == 2 {
				return []byte("4"), nil
			}
		}
		return read(path)
	}
	if _, err := r.Sample(t.Context()); err == nil {
		t.Fatal("mixed interface lifetimes")
	}
}

func TestTrafficCancellation(t *testing.T) {
	for _, duringRead := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		r := testTrafficReader(t, trafficFiles())
		read := r.readFile
		r.readFile = func(path string) ([]byte, error) {
			if !duringRead {
				t.Fatal("read after cancellation")
			}
			cancel()
			return read(path)
		}
		if !duringRead {
			cancel()
		}
		_, err := r.Sample(ctx)
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	}
}
