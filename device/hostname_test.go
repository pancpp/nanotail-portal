package device

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestValidateHostname(t *testing.T) {
	for input, want := range map[string]string{"nanotail": "nanotail", " NanoTail-2 ": "nanotail-2", "a": "a", "1-device": "1-device", strings.Repeat("a", 63): strings.Repeat("a", 63)} {
		got, err := ValidateHostname(input)
		if err != nil || got != want {
			t.Fatalf("ValidateHostname(%q) = %q, %v", input, got, err)
		}
	}
	for _, input := range []string{"", " ", "--help", "-device", "device-", "device.local", "device_name", "two names", "a\nb", "a;b", "$(reboot)", "设备", strings.Repeat("a", 64)} {
		if _, err := ValidateHostname(input); !errors.Is(err, ErrInvalidHostname) {
			t.Fatalf("accepted invalid hostname %q: %v", input, err)
		}
	}
	for _, input := range []string{"localhost", " LOCALHOST6 "} {
		if _, err := ValidateHostname(input); !errors.Is(err, ErrReservedHostname) {
			t.Fatalf("accepted reserved hostname %q: %v", input, err)
		}
	}
}

func TestSetHostname(t *testing.T) {
	for _, tt := range []struct {
		name              string
		writeErr, readErr error
		saved             string
		wantErr           bool
	}{
		{name: "persisted", saved: "nanotail-2\n"},
		{name: "write rejected", writeErr: errors.New("permission denied"), wantErr: true},
		{name: "read failed", readErr: errors.New("nmcli unavailable"), wantErr: true},
		{name: "readback differs", saved: "old-name", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var calls [][]string
			c := &Configurator{runNM: func(commandCtx context.Context, args ...string) ([]byte, error) {
				calls = append(calls, args)
				deadline, ok := commandCtx.Deadline()
				if !ok || time.Until(deadline) > 15*time.Second || commandCtx.Err() != nil {
					t.Fatal("command must have a bounded lifetime and finish after browser cancellation")
				}
				if len(calls) == 1 {
					cancel()
					return nil, tt.writeErr
				}
				return []byte(tt.saved), tt.readErr
			}}
			err := c.SetHostname(ctx, " NanoTail-2 ")
			if (err != nil) != tt.wantErr || err != nil && !errors.Is(err, ErrHostnameApply) {
				t.Fatalf("SetHostname error = %v", err)
			}
			want := [][]string{{"--wait", "10", "general", "hostname", "nanotail-2"}}
			if tt.writeErr == nil {
				want = append(want, []string{"--terse", "general", "hostname"})
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("commands = %v, want %v", calls, want)
			}
		})
	}
}

func TestSetHostnameRejectsBeforeCommands(t *testing.T) {
	c := &Configurator{runNM: func(context.Context, ...string) ([]byte, error) {
		t.Fatal("unexpected command")
		return nil, nil
	}}
	if err := c.SetHostname(t.Context(), "--help"); !errors.Is(err, ErrInvalidHostname) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := c.SetHostname(ctx, "nanotail"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.SetHostname(t.Context(), "nanotail"); !errors.Is(err, ErrHostnameBusy) {
		t.Fatal(err)
	}
}

func TestHostnameChangeBlocksConcurrentLANAndHostnameWrites(t *testing.T) {
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	c := &Configurator{runNM: func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "--wait" {
			close(started)
			<-release
		}
		return []byte("nanotail"), nil
	}}
	go func() { done <- c.SetHostname(t.Context(), "nanotail") }()
	<-started
	hostErr := c.SetHostname(t.Context(), "another-name")
	ipErr := c.SetIP(t.Context(), IPConfig{Type: "DHCP"})
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !errors.Is(hostErr, ErrHostnameBusy) || !errors.Is(ipErr, ErrConfigBusy) {
		t.Fatalf("concurrent writes: hostname=%v LAN=%v", hostErr, ipErr)
	}
}
