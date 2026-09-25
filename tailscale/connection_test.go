package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestConnectionRead(t *testing.T) {
	for _, tc := range []struct {
		state                      string
		enabled, key, expired, can bool
	}{
		{"Running", true, true, false, true}, {"Stopped", false, true, false, true},
		{"Starting", true, true, false, true}, {"NeedsLogin", true, true, false, false},
		{"NeedsMachineAuth", true, true, false, false}, {"NoState", false, true, false, false},
		{"Stopped", false, false, false, false}, {"Stopped", false, true, true, false},
	} {
		t.Run(tc.state, func(t *testing.T) {
			client := NewClient("tailscale", "", time.Second, runnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if reflect.DeepEqual(args, []string{"debug", "prefs"}) {
					return json.Marshal(map[string]any{"WantRunning": tc.enabled, "Persist": map[string]string{"PrivateNodeKey": "secret"}})
				}
				if !reflect.DeepEqual(args, []string{"status", "--json"}) {
					t.Fatalf("unexpected command %v", args)
				}
				status := map[string]any{"BackendState": tc.state, "HaveNodeKey": tc.key}
				if tc.expired {
					status["Self"] = map[string]any{"KeyExpiry": "2020-01-01T00:00:00Z"}
				}
				return json.Marshal(status)
			}))
			value, err := client.Connection(t.Context())
			if err != nil || value.Enabled != tc.enabled || value.BackendState != tc.state || value.CanEnable != tc.can {
				t.Fatalf("connection=%+v err=%v", value, err)
			}
		})
	}
	client := NewClient("tailscale", "", time.Second, runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("private daemon diagnostic")
	}))
	if _, err := client.Connection(t.Context()); !errors.Is(err, ErrConnectionUnavailable) {
		t.Fatal(err)
	}
}

func TestSetEnabled(t *testing.T) {
	for _, tc := range []struct {
		name                                                       string
		initial, enabled                                           bool
		state                                                      string
		noKey, expired, badPrefs, badStatus, badReadback, mismatch bool
		writeErr, wantErr                                          error
		command                                                    string
	}{
		{name: "resume", enabled: true, command: "up"},
		{name: "stop", initial: true, command: "down", badStatus: true}, // Stopping must work without status.
		{name: "already on", initial: true, enabled: true},
		{name: "already off"},
		{name: "no key", enabled: true, noKey: true, wantErr: ErrConnectionLogin},
		{name: "needs login", enabled: true, state: "NeedsLogin", wantErr: ErrConnectionLogin},
		{name: "needs approval", enabled: true, state: "NeedsMachineAuth", wantErr: ErrConnectionLogin},
		{name: "unknown state", enabled: true, state: "NoState", wantErr: ErrConnectionLogin},
		{name: "expired key", enabled: true, expired: true, wantErr: ErrConnectionLogin},
		{name: "already enabled but logged out", initial: true, enabled: true, state: "NeedsLogin", wantErr: ErrConnectionLogin},
		{name: "prefs unavailable", enabled: true, badPrefs: true, wantErr: ErrConnectionUnavailable},
		{name: "status unavailable", enabled: true, badStatus: true, wantErr: ErrConnectionUnavailable},
		{name: "up failure masked", enabled: true, command: "up", writeErr: errors.New("secret output"), wantErr: ErrConnectionApply},
		{name: "down failure masked", initial: true, command: "down", writeErr: errors.New("secret output"), wantErr: ErrConnectionApply},
		{name: "failed readback", enabled: true, command: "up", badReadback: true, wantErr: ErrConnectionApply},
		{name: "mismatched readback", enabled: true, command: "up", mismatch: true, wantErr: ErrConnectionApply},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var writes [][]string
			client := NewClient("/usr/bin/tailscale", "/run/test.sock", time.Second, runnerFunc(func(ctx context.Context, binary string, args ...string) ([]byte, error) {
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("missing timeout")
				}
				if binary != "/usr/bin/tailscale" || args[0] != "--socket=/run/test.sock" {
					t.Fatalf("bad command %s %v", binary, args)
				}
				args = args[1:]
				switch args[0] {
				case "debug":
					if tc.badPrefs || (len(writes) > 0 && tc.badReadback) {
						return []byte(`{}`), nil
					}
					want := tc.initial
					if len(writes) > 0 && !tc.mismatch {
						want = tc.enabled
					}
					return json.Marshal(map[string]any{"WantRunning": want, "ExitNodeID": "keep-exit", "CorpDNS": true, "AdvertiseRoutes": []string{"192.0.2.0/24"}})
				case "status":
					if !tc.enabled {
						t.Fatal("stop must not depend on status")
					}
					if tc.badStatus {
						return nil, errors.New("private status diagnostic")
					}
					state := tc.state
					if state == "" {
						state = "Stopped"
					}
					status := map[string]any{"BackendState": state, "HaveNodeKey": !tc.noKey}
					if tc.expired {
						status["Self"] = map[string]any{"KeyExpiry": "2020-01-01T00:00:00Z"}
					}
					return json.Marshal(status)
				case "up", "down":
					writes = append(writes, args)
					return nil, tc.writeErr
				default:
					t.Fatalf("unexpected mutation %v", args)
					return nil, nil
				}
			}))
			err := client.SetEnabled(t.Context(), tc.enabled)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v want %v", err, tc.wantErr)
			}
			if tc.command == "" {
				if len(writes) != 0 {
					t.Fatalf("unexpected writes %v", writes)
				}
			} else if !reflect.DeepEqual(writes, [][]string{{tc.command}}) {
				t.Fatalf("must only run bare up/down: %v", writes)
			}
			if len(client.mutations) != 0 {
				t.Fatal("mutation lock leaked")
			}
		})
	}
}

func TestConnectionMutationLockAndCancellation(t *testing.T) {
	client := NewClient("tailscale", "", 20*time.Millisecond, runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("must not run while another mutation holds the lock")
		return nil, nil
	}))
	client.mutations <- struct{}{}
	if err := client.SetEnabled(t.Context(), false); !errors.Is(err, ErrConnectionUnavailable) {
		t.Fatal(err)
	}
	<-client.mutations
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := client.SetEnabled(ctx, false); !errors.Is(err, ErrConnectionUnavailable) {
		t.Fatal(err)
	}
	client = NewClient("tailscale", "", 20*time.Millisecond, runnerFunc(func(ctx context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "debug" {
			return []byte(`{"WantRunning":true}`), nil
		}
		if !reflect.DeepEqual(args, []string{"down"}) {
			t.Fatal(args)
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	if err := client.SetEnabled(t.Context(), false); !errors.Is(err, ErrConnectionApply) {
		t.Fatal(err)
	}
	if len(client.mutations) != 0 {
		t.Fatal("mutation lock leaked")
	}
}
