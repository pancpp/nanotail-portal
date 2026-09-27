package tailscale

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestSetPeerRelay(t *testing.T) {
	for _, tc := range []struct {
		name, before, after, argument string
		enabled                       bool
		port                          int
		readErr, setErr, wantErr      error
		write                         bool
	}{
		{name: "enable default", enabled: true, port: DefaultPeerRelayPort, before: `{"WantRunning":true}`, after: `{"WantRunning":true,"RelayServerPort":40001}`, argument: "--relay-server-port=40001", write: true},
		{name: "custom port", enabled: true, port: 65535, before: `{"WantRunning":true,"RelayServerPort":40001}`, after: `{"WantRunning":true,"RelayServerPort":65535}`, argument: "--relay-server-port=65535", write: true},
		{name: "save while stopped", enabled: true, port: 40001, before: `{"WantRunning":false}`, after: `{"WantRunning":false,"RelayServerPort":40001}`, argument: "--relay-server-port=40001", write: true},
		{name: "disable", port: 40001, before: `{"WantRunning":true,"RelayServerPort":40001}`, after: `{"WantRunning":true}`, argument: "--relay-server-port=", write: true},
		{name: "disable automatic", port: 40001, before: `{"WantRunning":true,"RelayServerPort":0}`, after: `{"WantRunning":true,"RelayServerPort":null}`, argument: "--relay-server-port=", write: true},
		{name: "already enabled", enabled: true, port: 40001, before: `{"WantRunning":true,"RelayServerPort":40001}`},
		{name: "already disabled", port: 40001, before: `{"WantRunning":true}`},
		{name: "read fails", port: 40001, readErr: errors.New("private preferences"), wantErr: ErrPeerRelayUnavailable},
		{name: "write fails", enabled: true, port: 40001, before: `{"WantRunning":true}`, setErr: errors.New("private CLI diagnostic"), wantErr: ErrPeerRelayApply, write: true, argument: "--relay-server-port=40001"},
		{name: "readback malformed", enabled: true, port: 40001, before: `{"WantRunning":true}`, after: `{}`, wantErr: ErrPeerRelayApply, write: true, argument: "--relay-server-port=40001"},
		{name: "readback different", enabled: true, port: 40001, before: `{"WantRunning":true}`, after: `{"WantRunning":true,"RelayServerPort":40002}`, wantErr: ErrPeerRelayApply, write: true, argument: "--relay-server-port=40001"},
		{name: "zero is not disabled", port: 40001, before: `{"WantRunning":true,"RelayServerPort":40001}`, after: `{"WantRunning":true,"RelayServerPort":0}`, wantErr: ErrPeerRelayApply, write: true, argument: "--relay-server-port="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads, writes := 0, 0
			client := NewClient("/usr/bin/tailscale", "/run/relay.sock", time.Second, runnerFunc(func(ctx context.Context, binary string, args ...string) ([]byte, error) {
				if binary != "/usr/bin/tailscale" || args[0] != "--socket=/run/relay.sock" {
					t.Fatalf("unexpected command %s %v", binary, args)
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Error("missing deadline")
				}
				args = args[1:]
				if reflect.DeepEqual(args, []string{"status", "--json"}) {
					return []byte(relayTestStatus), nil
				}
				if reflect.DeepEqual(args, []string{"debug", "prefs"}) {
					reads++
					if reads == 1 {
						return []byte(tc.before), tc.readErr
					}
					return []byte(tc.after), nil
				}
				writes++
				if !reflect.DeepEqual(args, []string{"set", tc.argument}) {
					t.Fatalf("must change only relay preference: %v", args)
				}
				return nil, tc.setErr
			})).WithPeerRelayPolicy(relayTestCredentials, (&relayPolicyFixture{policy: relayTestPolicy}).httpClient(t))
			err := client.SetPeerRelay(t.Context(), tc.enabled, tc.port)
			if !errors.Is(err, tc.wantErr) || (writes == 1) != tc.write || writes > 1 {
				t.Fatalf("err=%v writes=%d", err, writes)
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal("leaked CLI output")
			}
		})
	}
}

func TestPeerRelayReadPreferences(t *testing.T) {
	for _, value := range []string{"null", "0", "40001", "65535"} {
		t.Run(value, func(t *testing.T) {
			client := NewClient("tailscale", "", time.Second, runnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if args[0] == "status" {
					return []byte(`{"BackendState":"Running"}`), nil
				}
				if !reflect.DeepEqual(args, []string{"debug", "prefs"}) {
					t.Fatalf("read performed mutation: %v", args)
				}
				return []byte(`{"WantRunning":true,"RelayServerPort":` + value + `,"Persist":{"PrivateNodeKey":"secret"}}`), nil
			}))
			routing, err := client.Routing(t.Context())
			if err != nil || (routing.PeerRelayPort == nil) != (value == "null") {
				t.Fatalf("%+v %v", routing, err)
			}
			if value == "0" && *routing.PeerRelayPort != 0 {
				t.Fatal("automatic port lost")
			}
			if value == "40001" && *routing.PeerRelayPort != 40001 {
				t.Fatal("port lost")
			}
		})
	}
}

func TestPeerRelayInvalidPortsAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := NewClient("tailscale", "", time.Second, runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
			t.Error("unexpected command")
			return nil, nil
		}))
		for _, port := range []int{-1, 0, 65536} {
			if err := client.SetPeerRelay(t.Context(), true, port); !errors.Is(err, ErrPeerRelayPort) {
				t.Fatalf("port %d: %v", port, err)
			}
		}
		client.mutations <- struct{}{}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- client.SetPeerRelay(ctx, true, 40001) }()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, ErrPeerRelayUnavailable) {
			t.Fatal(err)
		}
		<-client.mutations
		if err := client.SetPeerRelay(ctx, true, 40001); !errors.Is(err, ErrPeerRelayUnavailable) {
			t.Fatal(err)
		}
	})
}
