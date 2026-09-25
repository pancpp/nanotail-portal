package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestEnsureExitNode(t *testing.T) {
	for _, tc := range []struct {
		name, state, before, after string
		noKey, skip, noWrite       bool
		failAt                     string
		want                       error
	}{
		{name: "enable", before: `{"WantRunning":true}`},
		{name: "already enabled", before: `{"WantRunning":true,"AdvertiseRoutes":["::/0","0.0.0.0/0","10.0.0.0/8"]}`, noWrite: true},
		{name: "only IPv4", before: `{"WantRunning":true,"AdvertiseRoutes":["0.0.0.0/0"]}`},
		{name: "only IPv6", before: `{"WantRunning":true,"AdvertiseRoutes":["::/0"]}`},
		{name: "clear selected exit", before: `{"WantRunning":true,"ExitNodeID":"old","ExitNodeIP":"100.64.0.9","ExitNodeAllowLANAccess":true,"AdvertiseRoutes":["0.0.0.0/0","::/0"]}`},
		{name: "preserve custom preferences", before: `{"WantRunning":true,"CorpDNS":false,"NoSNAT":true,"AdvertiseRoutes":["10.20.0.0/16","fd00:1234::/64"]}`},
		{name: "paused stays paused", state: "Stopped", before: `{"WantRunning":false}`},
		{name: "starting", state: "Starting", before: `{"WantRunning":true}`},
		{name: "needs login", state: "NeedsLogin", skip: true},
		{name: "needs approval", state: "NeedsMachineAuth", skip: true},
		{name: "no node key", noKey: true, skip: true},
		{name: "daemon down", failAt: "status", want: ErrRoutingUnavailable},
		{name: "cannot read prefs", failAt: "prefs", want: ErrRoutingUnavailable},
		{name: "cannot write", before: `{"WantRunning":true}`, failAt: "set", want: ErrRoutingApply},
		{name: "cannot readback", before: `{"WantRunning":true}`, failAt: "readback", want: ErrRoutingApply},
		{name: "readback missing default", before: `{"WantRunning":true}`, after: `{"WantRunning":true,"AdvertiseRoutes":["0.0.0.0/0"]}`, want: ErrRoutingApply},
		{name: "readback retains selection", before: `{"WantRunning":true}`, after: `{"WantRunning":true,"ExitNodeID":"old","AdvertiseRoutes":["0.0.0.0/0","::/0"]}`, want: ErrRoutingApply},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var writes, reads int
			client := NewClient("fake-tailscale", "/run/test.sock", time.Second, runnerFunc(func(_ context.Context, binary string, args ...string) ([]byte, error) {
				if binary != "fake-tailscale" || args[0] != "--socket=/run/test.sock" {
					t.Fatalf("%s %v", binary, args)
				}
				args = args[1:]
				switch args[0] {
				case "status":
					if tc.failAt == "status" {
						return nil, errors.New("private diagnostic")
					}
					state := tc.state
					if state == "" {
						state = "Running"
					}
					return json.Marshal(map[string]any{"BackendState": state, "HaveNodeKey": !tc.noKey})
				case "debug":
					if tc.skip {
						t.Fatal("read prefs before authenticated")
					}
					reads++
					if tc.failAt == "prefs" || (reads > 1 && tc.failAt == "readback") {
						return nil, errors.New("private diagnostic")
					}
					if reads == 1 {
						return []byte(tc.before), nil
					}
					if tc.after != "" {
						return []byte(tc.after), nil
					}
					return []byte(`{"WantRunning":false,"AdvertiseRoutes":["0.0.0.0/0","::/0","10.20.0.0/16","fd00:1234::/64"]}`), nil
				case "set":
					writes++
					// No --advertise-routes, --snat-subnet-routes, --accept-dns, or up:
					// tailscale set must preserve every preference outside the fixed role.
					want := []string{"set", "--exit-node-allow-lan-access=false", "--advertise-exit-node=true", "--exit-node="}
					if !reflect.DeepEqual(args, want) {
						t.Fatalf("args=%v want=%v", args, want)
					}
					if tc.failAt == "set" {
						return nil, errors.New("private diagnostic")
					}
					return nil, nil
				default:
					t.Fatalf("unexpected command %v", args)
					return nil, nil
				}
			}))
			err := client.EnsureExitNode(t.Context())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want=%v", err, tc.want)
			}
			wantWrites := 1
			if tc.skip || tc.noWrite || tc.failAt == "status" || tc.failAt == "prefs" {
				wantWrites = 0
			}
			if writes != wantWrites {
				t.Fatalf("writes=%d want=%d", writes, wantWrites)
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal("leaked diagnostics")
			}
			if len(client.mutations) != 0 {
				t.Fatal("mutation lock leaked")
			}
		})
	}
}

func TestEnsureExitNodeUncertainWriteReadsBeforeRetry(t *testing.T) {
	writes := 0
	client := NewClient("fake", "", time.Second, runnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch args[0] {
		case "status":
			return []byte(`{"BackendState":"Running","HaveNodeKey":true}`), nil
		case "debug":
			if writes > 0 {
				return []byte(`{"WantRunning":true,"AdvertiseRoutes":["0.0.0.0/0","::/0","192.168.1.0/24"]}`), nil
			}
			return []byte(`{"WantRunning":true}`), nil
		case "set":
			writes++
			return nil, errors.New("connection lost after apply")
		default:
			t.Fatal(args)
			return nil, nil
		}
	}))
	if err := client.EnsureExitNode(t.Context()); !errors.Is(err, ErrRoutingApply) {
		t.Fatal(err)
	}
	if err := client.EnsureExitNode(t.Context()); err != nil {
		t.Fatal(err)
	}
	if writes != 1 {
		t.Fatalf("uncertain write blindly repeated: %d", writes)
	}
}

func TestEnsureExitNodeMutationLockAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := NewClient("fake", "", time.Second, runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
			t.Fatal("command ran after cancellation or while mutation lock held")
			return nil, nil
		}))
		client.mutations <- struct{}{}
		if err := client.EnsureExitNode(t.Context()); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		<-client.mutations
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := client.EnsureExitNode(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if len(client.mutations) != 0 {
			t.Fatal("mutation lock leaked")
		}
	})
}

func TestMaintainExitNodeLifecycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls, reports atomic.Int32
		var fail atomic.Bool
		fail.Store(true)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan struct{})
		go func() {
			defer close(done)
			maintainExitNode(ctx, func(context.Context) error {
				calls.Add(1)
				if fail.Load() {
					return ErrRoutingUnavailable
				}
				return nil
			}, func(error) { reports.Add(1) })
		}()
		synctest.Wait()
		if calls.Load() != 1 || reports.Load() != 1 {
			t.Fatal("missing immediate startup check")
		}
		time.Sleep(2 * exitNodeCheckInterval)
		synctest.Wait()
		if calls.Load() != 3 || reports.Load() != 1 {
			t.Fatalf("calls=%d reports=%d", calls.Load(), reports.Load())
		}
		fail.Store(false)
		time.Sleep(exitNodeCheckInterval)
		synctest.Wait()
		fail.Store(true)
		time.Sleep(exitNodeCheckInterval)
		synctest.Wait()
		if reports.Load() != 2 {
			t.Fatal("new failure after recovery not logged")
		}
		cancel()
		<-done
		before := calls.Load()
		time.Sleep(2 * exitNodeCheckInterval)
		if calls.Load() != before {
			t.Fatal("worker ran after shutdown")
		}
	})
}

func TestUpdateConfigCannotDisableExitNodeRole(t *testing.T) {
	client := NewClient("fake", "", time.Second, runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("role-changing input reached CLI")
		return nil, nil
	}))
	for _, input := range []string{
		`{"advertise_exit_node":false}`,
		`{"exit_node":"100.64.0.9"}`,
		`{"exit_node_allow_lan_access":true}`,
	} {
		var update ConfigUpdate
		if err := json.Unmarshal([]byte(input), &update); err != nil {
			t.Fatal(err)
		}
		if err := client.UpdateConfig(t.Context(), update); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%s: %v", input, err)
		}
	}
}
