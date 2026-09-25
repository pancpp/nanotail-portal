package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type memorySubnetDefaults struct {
	configured        bool
	readErr, writeErr error
	marks             int
}

func (s *memorySubnetDefaults) Configured(context.Context) (bool, error) {
	return s.configured, s.readErr
}
func (s *memorySubnetDefaults) MarkConfigured(context.Context) error {
	s.marks++
	if s.writeErr != nil {
		return s.writeErr
	}
	s.configured = true
	return nil
}

type defaultSubnetDaemon struct {
	state    string
	noKey    bool
	routes   []string
	writes   [][]string
	commands int
	failAt   string
}

func (d *defaultSubnetDaemon) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	d.commands++
	switch args[0] {
	case "status":
		if d.failAt == "status" {
			return nil, errors.New("private status failure")
		}
		state := d.state
		if state == "" {
			state = "Running"
		}
		return json.Marshal(map[string]any{"BackendState": state, "HaveNodeKey": !d.noKey})
	case "debug":
		if d.failAt == "prefs" || (d.failAt == "readback" && len(d.writes) > 0) {
			return nil, errors.New("private prefs failure")
		}
		return json.Marshal(map[string]any{"WantRunning": d.state != "Stopped", "AdvertiseRoutes": append([]string{"0.0.0.0/0", "::/0"}, d.routes...)})
	case "set":
		d.writes = append(d.writes, append([]string{}, args...))
		if d.failAt == "set" {
			return nil, errors.New("private command failure")
		}
		for _, arg := range args[1:] {
			if value, ok := strings.CutPrefix(arg, "--advertise-routes="); ok {
				d.routes = nil
				if value != "" {
					d.routes = strings.Split(value, ",")
				}
			}
		}
		if d.failAt == "uncertain" {
			return nil, errors.New("lost response after successful write")
		}
		return nil, nil
	default:
		return nil, errors.New("unexpected command")
	}
}

func defaultSubnetClient(store SubnetDefaultsStore, d *defaultSubnetDaemon, detect func(context.Context) ([]string, error)) *Client {
	return NewClient("fake", "", time.Second, d).WithSubnetDefaults(store, detect)
}

func TestSubnetDefaultsInitializeAndPersist(t *testing.T) {
	store := &memorySubnetDefaults{}
	daemon := &defaultSubnetDaemon{}
	detect := func(context.Context) ([]string, error) { return []string{"fd00::/64", "192.168.42.0/24"}, nil }
	client := defaultSubnetClient(store, daemon, detect)
	value, err := client.Routing(t.Context())
	if err != nil || !value.SubnetDefaultsPending || len(value.SubnetRoutes) != 0 {
		t.Fatalf("%+v %v", value, err)
	}
	if err := client.EnsureSubnetDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := []string{"set", "--exit-node-allow-lan-access=false", "--advertise-exit-node=true", "--exit-node=", "--advertise-routes=192.168.42.0/24,fd00::/64"}
	if len(daemon.writes) != 1 || !reflect.DeepEqual(daemon.writes[0], want) || !store.configured {
		t.Fatalf("writes=%v configured=%v", daemon.writes, store.configured)
	}
	value, err = client.Routing(t.Context())
	if err != nil || value.SubnetDefaultsPending || len(value.SubnetRoutes) != 2 {
		t.Fatalf("%+v %v", value, err)
	}
	// A new client/process and a changed LAN must not rewrite the saved choice.
	restarted := defaultSubnetClient(store, daemon, func(context.Context) ([]string, error) { t.Fatal("redetected initialized LAN"); return nil, nil })
	commands := daemon.commands
	if err := restarted.EnsureSubnetDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	if daemon.commands != commands {
		t.Fatal("initialized defaults reached CLI")
	}
	// Explicit off is durable, not mistaken for a fresh installation.
	if err := restarted.SetRouting(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	restarted = defaultSubnetClient(store, daemon, detect)
	if err := restarted.EnsureSubnetDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(daemon.writes) != 2 || len(daemon.routes) != 0 {
		t.Fatal("explicit off was re-enabled")
	}
}

func TestSubnetDefaultsPreserveExistingRoutes(t *testing.T) {
	store := &memorySubnetDefaults{}
	daemon := &defaultSubnetDaemon{routes: []string{"10.20.0.0/16"}}
	client := defaultSubnetClient(store, daemon, func(context.Context) ([]string, error) { t.Fatal("detected LAN over existing routes"); return nil, nil })
	if err := client.EnsureSubnetDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !store.configured || len(daemon.writes) != 0 || daemon.routes[0] != "10.20.0.0/16" {
		t.Fatal("existing routes replaced")
	}
}

func TestSubnetDefaultsWaitForReadiness(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		noKey       bool
		routes      []string
		detectErr   error
		want        error
	}{
		{name: "logged out", state: "NeedsLogin"},
		{name: "paused", state: "Stopped"},
		{name: "approval", state: "NeedsMachineAuth"},
		{name: "starting", state: "Starting"},
		{name: "no key", noKey: true},
		{name: "no LAN", want: ErrSubnetDefaultsUnavailable},
		{name: "bad LAN", routes: []string{"192.168.1.9/24"}, want: ErrSubnetDefaultsUnavailable},
		{name: "detector failed", detectErr: errors.New("private LAN error"), want: ErrSubnetDefaultsUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &memorySubnetDefaults{}
			daemon := &defaultSubnetDaemon{state: tc.state, noKey: tc.noKey}
			client := defaultSubnetClient(store, daemon, func(context.Context) ([]string, error) {
				if tc.state != "" || tc.noKey {
					t.Fatal("detected LAN before ready")
				}
				return tc.routes, tc.detectErr
			})
			if err := client.EnsureSubnetDefaults(t.Context()); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if len(daemon.writes) != 0 || store.configured {
				t.Fatal("waiting marked defaults as configured")
			}
			daemon.state = "Running"
			daemon.noKey = false
			client = defaultSubnetClient(store, daemon, func(context.Context) ([]string, error) { return []string{"192.168.42.0/24"}, nil })
			if err := client.EnsureSubnetDefaults(t.Context()); err != nil {
				t.Fatal(err)
			}
			if !store.configured || len(daemon.writes) != 1 {
				t.Fatal("recovery did not apply defaults")
			}
		})
	}
}

func TestSubnetDefaultsErrorsAndFreshReadRetry(t *testing.T) {
	for _, stage := range []string{"store read", "status", "prefs", "set", "uncertain", "readback", "store write"} {
		t.Run(stage, func(t *testing.T) {
			store := &memorySubnetDefaults{}
			if stage == "store read" {
				store.readErr = errors.New("private database error")
			}
			if stage == "store write" {
				store.writeErr = errors.New("private database error")
			}
			daemon := &defaultSubnetDaemon{failAt: stage}
			client := defaultSubnetClient(store, daemon, func(context.Context) ([]string, error) { return []string{"192.168.42.0/24"}, nil })
			err := client.EnsureSubnetDefaults(t.Context())
			if err == nil || strings.Contains(err.Error(), "private") || store.configured {
				t.Fatalf("err=%v configured=%v", err, store.configured)
			}
			if stage == "store read" && daemon.commands != 0 {
				t.Fatal("store read failure reached CLI")
			}
			writesBefore := len(daemon.writes)
			store.readErr = nil
			store.writeErr = nil
			daemon.failAt = ""
			if err := client.EnsureSubnetDefaults(t.Context()); err != nil {
				t.Fatal(err)
			}
			if !store.configured {
				t.Fatal("retry did not record setup")
			}
			if (stage == "uncertain" || stage == "readback" || stage == "store write") && len(daemon.writes) != writesBefore {
				t.Fatal("already-applied default write was repeated")
			}
		})
	}
}

func TestExplicitSubnetChoiceWinsBeforeAutomaticSetup(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, failWrite := range []bool{false, true} {
			store := &memorySubnetDefaults{}
			daemon := &defaultSubnetDaemon{}
			if failWrite {
				daemon.failAt = "set"
			}
			client := defaultSubnetClient(store, daemon, func(context.Context) ([]string, error) { t.Fatal("explicit choice lost"); return nil, nil })
			var err error
			if legacy {
				empty := []string{}
				err = client.UpdateConfig(t.Context(), ConfigUpdate{AdvertiseRoutes: &empty})
			} else {
				err = client.SetRouting(t.Context(), nil)
			}
			if (err != nil) != failWrite {
				t.Fatalf("unexpected explicit write: %v", err)
			}
			if !store.configured {
				t.Fatal("explicit off was not persisted before command")
			}
			daemon.failAt = ""
			if err := client.EnsureSubnetDefaults(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(daemon.writes) != 1 || len(daemon.routes) != 0 {
				t.Fatal("default overrode explicit off")
			}
		}
	}
}

func TestExplicitSubnetChoiceStorageFailureDoesNotRunCLI(t *testing.T) {
	store := &memorySubnetDefaults{writeErr: errors.New("private database error")}
	daemon := &defaultSubnetDaemon{}
	client := defaultSubnetClient(store, daemon, nil)
	if err := client.SetRouting(t.Context(), nil); !errors.Is(err, ErrRoutingPersistence) {
		t.Fatal(err)
	}
	if daemon.commands != 0 {
		t.Fatal("database failure reached CLI")
	}
	if err := client.SetRouting(t.Context(), []string{"invalid"}); !errors.Is(err, ErrSubnetRoutesInvalid) {
		t.Fatal(err)
	}
	if store.marks != 1 {
		t.Fatal("invalid route wrote the marker")
	}
}

func TestSubnetDefaultsUseMutationLockAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &memorySubnetDefaults{}
		daemon := &defaultSubnetDaemon{}
		client := defaultSubnetClient(store, daemon, nil)
		client.mutations <- struct{}{}
		if err := client.EnsureSubnetDefaults(t.Context()); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		<-client.mutations
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := client.EnsureSubnetDefaults(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if daemon.commands != 0 || store.marks != 0 || len(client.mutations) != 0 {
			t.Fatal("cancelled work changed state")
		}
	})
}

func TestMaintainRoutingInitializesSubnetDefaults(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &memorySubnetDefaults{}
		daemon := &defaultSubnetDaemon{}
		client := defaultSubnetClient(store, daemon, func(context.Context) ([]string, error) { return []string{"192.168.42.0/24"}, nil })
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan struct{})
		go func() { defer close(done); client.MaintainRouting(ctx) }()
		synctest.Wait()
		cancel()
		<-done
		if !store.configured || len(daemon.writes) != 1 || !reflect.DeepEqual(daemon.routes, []string{"192.168.42.0/24"}) {
			t.Fatal("startup worker did not apply LAN defaults")
		}
	})
}
