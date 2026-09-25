package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func routingStatus(state string, online, approved bool, ips []string) []byte {
	data, _ := json.Marshal(map[string]any{"BackendState": state, "Peer": map[string]any{
		"peer":     map[string]any{"ID": "exit-1", "HostName": "exit-one", "Online": online, "ExitNodeOption": approved, "TailscaleIPs": ips},
		"ordinary": map[string]any{"ID": "ordinary", "Online": true},
	}})
	return data
}

func TestRoutingRead(t *testing.T) {
	for _, tc := range []struct{ name, prefs, wantID string }{
		{"local", `{"WantRunning":true}`, ""},
		{"selected by ID", `{"WantRunning":true,"ExitNodeID":"exit-1","ExitNodeAllowLANAccess":true}`, "exit-1"},
		{"legacy IP", `{"WantRunning":true,"ExitNodeIP":"100.64.0.2"}`, "exit-1"},
		{"missing peer", `{"WantRunning":true,"ExitNodeID":"gone"}`, "gone"},
		{"advertising", `{"WantRunning":true,"AdvertiseRoutes":["0.0.0.0/0","::/0"],"Persist":{"PrivateNodeKey":"secret"}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := NewClient("tailscale", "", time.Second, runnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
				switch strings.Join(args, " ") {
				case "debug prefs":
					return []byte(tc.prefs), nil
				case "status --json":
					return routingStatus("Running", false, true, []string{"100.64.0.2"}), nil
				default:
					t.Fatalf("unexpected command %v", args)
					return nil, nil
				}
			}))
			value, err := client.Routing(t.Context())
			if err != nil || value.ExitNodeID != tc.wantID || value.BackendState != "Running" || len(value.ExitNodes) != 1 || value.ExitNodes[0].Online {
				t.Fatalf("routing=%+v err=%v", value, err)
			}
			if value.AdvertiseExitNode != (tc.name == "advertising") || value.AllowLANAccess != (tc.name == "selected by ID") {
				t.Fatalf("lost preferences: %+v", value)
			}
			encoded, _ := json.Marshal(value)
			if strings.Contains(string(encoded), "secret") {
				t.Fatal("leaked private preferences")
			}
		})
	}
}

func TestSetExitNode(t *testing.T) {
	for _, tc := range []struct {
		name, id, state, initial, after string
		allow, offline, unapproved      bool
		ips                             []string
		setErr, wantErr                 error
		wantWrite                       bool
	}{
		{name: "select", id: "exit-1", allow: true, after: `{"WantRunning":true,"ExitNodeID":"exit-1","ExitNodeAllowLANAccess":true}`, wantWrite: true},
		{name: "block LAN", id: "exit-1", after: `{"WantRunning":true,"ExitNodeID":"exit-1"}`, wantWrite: true},
		{name: "IPv6", id: "exit-1", ips: []string{"fd7a:115c:a1e0::2"}, after: `{"WantRunning":true,"ExitNodeIP":"fd7a:115c:a1e0::2"}`, wantWrite: true},
		{name: "legacy IP readback", id: "exit-1", after: `{"WantRunning":true,"ExitNodeIP":"100.64.0.2"}`, wantWrite: true},
		{name: "clear missing exit even when stopped", after: `{"WantRunning":false}`, wantWrite: true},
		{name: "clear with LAN access rejected", allow: true, wantErr: ErrExitNodeInvalid},
		{name: "unknown", id: "missing", wantErr: ErrExitNodeInvalid},
		{name: "injection", id: "--hostname=bad;reboot", wantErr: ErrExitNodeInvalid},
		{name: "whitespace", id: " exit-1", wantErr: ErrExitNodeInvalid},
		{name: "too long", id: strings.Repeat("x", 257), wantErr: ErrExitNodeInvalid},
		{name: "offline", id: "exit-1", offline: true, wantErr: ErrExitNodeInvalid},
		{name: "unapproved", id: "exit-1", unapproved: true, wantErr: ErrExitNodeInvalid},
		{name: "no address", id: "exit-1", ips: []string{}, wantErr: ErrExitNodeInvalid},
		{name: "invalid address", id: "exit-1", ips: []string{"--bad", "::", "ff02::1", "fe80::1%eth0"}, wantErr: ErrExitNodeInvalid},
		{name: "stopped", id: "exit-1", state: "Stopped", wantErr: ErrRoutingStopped},
		{name: "logged out", id: "exit-1", state: "NeedsLogin", wantErr: ErrRoutingStopped},
		{name: "advertising", id: "exit-1", initial: `{"WantRunning":true,"AdvertiseRoutes":["0.0.0.0/0"]}`, wantErr: ErrRoutingAdvertised},
		{name: "bad preferences", id: "exit-1", initial: `{}`, wantErr: ErrRoutingUnavailable},
		{name: "bad status", id: "exit-1", state: "invalid", wantErr: ErrRoutingUnavailable},
		{name: "CLI failure", id: "exit-1", setErr: errors.New("secret daemon diagnostic"), wantErr: ErrRoutingApply, wantWrite: true},
		{name: "readback failed", id: "exit-1", after: `{}`, wantErr: ErrRoutingApply, wantWrite: true},
		{name: "wrong ID", id: "exit-1", after: `{"WantRunning":true,"ExitNodeID":"other"}`, wantErr: ErrRoutingApply, wantWrite: true},
		{name: "ID overrides legacy IP", id: "exit-1", after: `{"WantRunning":true,"ExitNodeID":"other","ExitNodeIP":"100.64.0.2"}`, wantErr: ErrRoutingApply, wantWrite: true},
		{name: "LAN mismatch", id: "exit-1", allow: true, after: `{"WantRunning":true,"ExitNodeID":"exit-1"}`, wantErr: ErrRoutingApply, wantWrite: true},
		{name: "clear failed", after: `{"WantRunning":true,"ExitNodeID":"old"}`, wantErr: ErrRoutingApply, wantWrite: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := tc.state
			if state == "" {
				state = "Running"
			}
			initial := tc.initial
			if initial == "" {
				initial = `{"WantRunning":true}`
			}
			ips := tc.ips
			if ips == nil {
				ips = []string{"100.64.0.2"}
			}
			var writes [][]string
			client := NewClient("/usr/bin/tailscale", "/run/custom.sock", time.Second, runnerFunc(func(_ context.Context, binary string, args ...string) ([]byte, error) {
				if binary != "/usr/bin/tailscale" || args[0] != "--socket=/run/custom.sock" {
					t.Fatalf("command %s %v", binary, args)
				}
				args = args[1:]
				switch args[0] {
				case "debug":
					if len(writes) > 0 {
						return []byte(tc.after), nil
					}
					return []byte(initial), nil
				case "status":
					if tc.id == "" {
						t.Fatal("clearing must not depend on status or an available peer")
					}
					if state == "invalid" {
						return nil, errors.New("private status error")
					}
					return routingStatus(state, !tc.offline, !tc.unapproved, ips), nil
				case "set":
					writes = append(writes, args)
					return nil, tc.setErr
				default:
					t.Fatalf("unexpected command %v", args)
					return nil, nil
				}
			}))
			err := client.SetExitNode(t.Context(), tc.id, tc.allow)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want=%v", err, tc.wantErr)
			}
			if (len(writes) == 1) != tc.wantWrite {
				t.Fatalf("writes=%v", writes)
			}
			if tc.wantWrite {
				address := ""
				if tc.id != "" {
					address = ips[0]
				}
				allow := "false"
				if tc.allow {
					allow = "true"
				}
				want := []string{"set", "--exit-node-allow-lan-access=" + allow, "--exit-node=" + address}
				if !reflect.DeepEqual(writes[0], want) {
					t.Fatalf("args=%v want=%v", writes[0], want)
				}
			}
		})
	}
}

func TestSetExitNodeMutationLockAndCancellation(t *testing.T) {
	client := NewClient("tailscale", "", 20*time.Millisecond, runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("must not run commands while another mutation holds the lock")
		return nil, nil
	}))
	client.mutations <- struct{}{}
	if err := client.SetExitNode(t.Context(), "", false); !errors.Is(err, ErrRoutingUnavailable) {
		t.Fatal(err)
	}
	<-client.mutations
	client = NewClient("tailscale", "", 20*time.Millisecond, runnerFunc(func(ctx context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] != "set" {
			t.Fatal(args)
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	if err := client.SetExitNode(t.Context(), "", false); !errors.Is(err, ErrRoutingApply) {
		t.Fatal(err)
	}
	if len(client.mutations) != 0 {
		t.Fatal("mutation lock leaked")
	}
}
