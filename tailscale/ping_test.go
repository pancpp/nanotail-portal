package tailscale

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestPing(t *testing.T) {
	for _, tc := range []struct {
		name, output    string
		runErr, wantErr error
		want            time.Duration
	}{
		{name: "direct", output: "pong from laptop (100.64.0.2) via TSMP in 23ms\n", want: 23 * time.Millisecond},
		{name: "zero", output: "pong from laptop (100.64.0.2) via TSMP in 0s\n"},
		{name: "seconds", output: "pong from laptop (100.64.0.2) via TSMP in 1.25s\n", want: 1250 * time.Millisecond},
		{name: "ipv6", output: "pong from laptop (fd7a:115c:a1e0::2) via TSMP in 500µs\n", want: 500 * time.Microsecond},
		{name: "timeout", output: "ping timed out", runErr: errors.New("private detail"), wantErr: ErrUnavailable},
		{name: "malformed", output: "pong from laptop (100.64.0.2) via TSMP in unknown\n", wantErr: ErrInvalidOutput},
		{name: "negative", output: "pong from laptop (100.64.0.2) via TSMP in -1ms\n", wantErr: ErrInvalidOutput},
		{name: "not a pong", output: "100.64.0.2 is local Tailscale IP\n", wantErr: ErrInvalidOutput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := NewClient("/usr/bin/tailscale", "/run/test.sock", 15*time.Second, runnerFunc(func(ctx context.Context, binary string, args ...string) ([]byte, error) {
				calls++
				wantArgs := []string{"--socket=/run/test.sock", "ping", "--tsmp", "--c=1", "--timeout=2s", "100.64.0.2"}
				if binary != "/usr/bin/tailscale" || !reflect.DeepEqual(args, wantArgs) {
					t.Errorf("command: %s %v", binary, args)
				}
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 3*time.Second {
					t.Error("missing bounded probe deadline")
				}
				return []byte(tc.output), tc.runErr
			}))
			got, err := client.Ping(t.Context(), "100.64.0.2")
			if got != tc.want || !errors.Is(err, tc.wantErr) || calls != 1 {
				t.Fatalf("Ping = %v, %v (%d calls), want %v, %v", got, err, calls, tc.want, tc.wantErr)
			}
		})
	}
}

func TestPingRejectsInvalidTargetsAndCanceledRequests(t *testing.T) {
	client := NewClient("tailscale", "", time.Second, runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		t.Error("unexpected command")
		return nil, nil
	}))
	for _, address := range []string{"", "--help", "host.example", "100.64.0.1; echo secret", "fe80::1%eth0"} {
		if _, err := client.Ping(t.Context(), address); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("accepted invalid address %q: %v", address, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := client.Ping(ctx, "100.64.0.1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request: %v", err)
	}
}

func TestPingBoundsConcurrencyAndCancelsQueuedWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		client := NewClient("tailscale", "", 15*time.Second, runnerFunc(func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
			calls.Add(1)
			<-ctx.Done()
			return nil, ctx.Err()
		}))
		ctx, cancel := context.WithCancel(t.Context())
		var wg sync.WaitGroup
		for range 12 {
			wg.Go(func() {
				if _, err := client.Ping(ctx, "100.64.0.2"); !errors.Is(err, context.Canceled) {
					t.Errorf("cancellation: %v", err)
				}
			})
		}
		synctest.Wait()
		if calls.Load() != 4 {
			t.Fatalf("started %d probes, want 4", calls.Load())
		}
		cancel()
		wg.Wait()
		if calls.Load() != 4 {
			t.Fatalf("canceled queued work started probes: %d", calls.Load())
		}
	})
}

func TestPingDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := NewClient("tailscale", "", 15*time.Second, runnerFunc(func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}))
		started := time.Now()
		if _, err := client.Ping(t.Context(), "100.64.0.2"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("deadline: %v", err)
		}
		if elapsed := time.Since(started); elapsed != 3*time.Second {
			t.Fatalf("probe lasted %v", elapsed)
		}
	})
}
