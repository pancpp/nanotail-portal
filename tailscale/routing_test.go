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

func TestRoutingRead(t *testing.T) {
	client := NewClient("tailscale", "", time.Second, runnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "debug prefs":
			return []byte(`{"WantRunning":true,"ExitNodeID":"legacy","NoSNAT":true,"AdvertiseRoutes":["0.0.0.0/0","::/0","192.168.2.0/24"],"Persist":{"PrivateNodeKey":"secret"}}`), nil
		case "status --json":
			return []byte(`{"BackendState":"Running","Health":["example warning"]}`), nil
		default:
			t.Fatalf("unexpected command %v", args)
			return nil, nil
		}
	}))
	value, err := client.Routing(t.Context())
	if err != nil || !value.UsingExitNode || !value.AdvertiseExitNode || value.SNATEnabled ||
		!reflect.DeepEqual(value.SubnetRoutes, []string{"192.168.2.0/24"}) || len(value.Health) != 1 {
		t.Fatalf("routing=%+v err=%v", value, err)
	}
	encoded, _ := json.Marshal(value)
	if strings.Contains(string(encoded), "secret") {
		t.Fatal("leaked private preferences")
	}
}

func TestValidateSubnetRoutes(t *testing.T) {
	got, err := ValidateSubnetRoutes([]string{"FD00:1234::/64", "192.168.1.0/24", "10.0.0.2/32"})
	if err != nil || !reflect.DeepEqual(got, []string{"10.0.0.2/32", "192.168.1.0/24", "fd00:1234::/64"}) {
		t.Fatalf("%v, %v", got, err)
	}
	for _, routes := range [][]string{
		{"192.168.1.1/24"}, {"::/0"}, {"0.0.0.0/0"}, {"bad"}, {"--reset"}, {"$(reboot)"},
		{"192.168.1.0/24\n--reset"}, {"192.168.1.0/24", "192.168.1.0/24"},
		{"FD00::/64", "fd00::/64"}, {"::ffff:192.168.1.0/120"}, {"fe80::/64"}, {"ff00::/8"},
		{"127.0.0.0/8"}, {"169.254.0.0/16"}, {"224.0.0.0/4"}, {"0.1.0.0/16"}, {"240.0.0.0/4"},
		{"100.64.0.0/10"}, {"100.0.0.0/8"}, {"fd7a:115c:a1e0::/64"}, make([]string, 65),
	} {
		if _, err := ValidateSubnetRoutes(routes); !errors.Is(err, ErrSubnetRoutesInvalid) {
			t.Errorf("accepted %v: %v", routes, err)
		}
	}
}

func TestSetRouting(t *testing.T) {
	for _, tc := range []struct {
		name, state, after string
		routes             []string
		setErr, wantErr    error
		write              bool
	}{
		{name: "exit and LAN", routes: []string{"192.168.1.0/24"}, after: `{"WantRunning":true,"AdvertiseRoutes":["192.168.1.0/24","::/0","0.0.0.0/0"]}`, write: true},
		{name: "exit only", after: `{"WantRunning":true,"AdvertiseRoutes":["0.0.0.0/0","::/0"]}`, write: true},
		{name: "subnets retain mandatory exit node", routes: []string{"fd00::/64", "192.168.1.0/24"}, after: `{"WantRunning":true,"AdvertiseRoutes":["0.0.0.0/0","::/0","192.168.1.0/24","fd00::/64"]}`, write: true},
		{name: "withdraw subnets while stopped", state: "Stopped", after: `{"WantRunning":false,"AdvertiseRoutes":["0.0.0.0/0","::/0"]}`, write: true},
		{name: "stopped", routes: []string{"192.168.1.0/24"}, state: "Stopped", wantErr: ErrRoutingStopped},
		{name: "needs login", routes: []string{"192.168.1.0/24"}, state: "NeedsLogin", wantErr: ErrRoutingStopped},
		{name: "status failure", routes: []string{"192.168.1.0/24"}, state: "invalid", wantErr: ErrRoutingUnavailable},
		{name: "bad route before any commands", routes: []string{"192.168.1.1/24"}, wantErr: ErrSubnetRoutesInvalid},
		{name: "CLI failure", setErr: errors.New("secret diagnostic"), wantErr: ErrRoutingApply, write: true},
		{name: "bad readback", after: `{}`, wantErr: ErrRoutingApply, write: true},
		{name: "exit mismatch", after: `{"WantRunning":true}`, wantErr: ErrRoutingApply, write: true},
		{name: "half exit routes", after: `{"WantRunning":true,"AdvertiseRoutes":["0.0.0.0/0"]}`, wantErr: ErrRoutingApply, write: true},
		{name: "partial exit missing IPv4", after: `{"WantRunning":true,"AdvertiseRoutes":["::/0"]}`, wantErr: ErrRoutingApply, write: true},
		{name: "subnet mismatch", routes: []string{"192.168.1.0/24"}, after: `{"WantRunning":true,"AdvertiseRoutes":["192.168.2.0/24"]}`, wantErr: ErrRoutingApply, write: true},
		{name: "legacy ID not cleared", after: `{"WantRunning":true,"ExitNodeID":"old"}`, wantErr: ErrRoutingApply, write: true},
		{name: "legacy IP not cleared", after: `{"WantRunning":true,"ExitNodeIP":"100.64.0.1"}`, wantErr: ErrRoutingApply, write: true},
		{name: "legacy LAN not cleared", after: `{"WantRunning":true,"ExitNodeAllowLANAccess":true}`, wantErr: ErrRoutingApply, write: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var writes [][]string
			client := NewClient("/usr/bin/tailscale", "/run/custom.sock", time.Second, runnerFunc(func(_ context.Context, binary string, args ...string) ([]byte, error) {
				if tc.wantErr == ErrSubnetRoutesInvalid {
					t.Fatal("invalid input ran a command")
				}
				if binary != "/usr/bin/tailscale" || args[0] != "--socket=/run/custom.sock" {
					t.Fatalf("%s %v", binary, args)
				}
				args = args[1:]
				switch args[0] {
				case "status":
					if len(tc.routes) == 0 {
						t.Fatal("disabling must not depend on status")
					}
					state := tc.state
					if state == "" {
						state = "Running"
					}
					if state == "invalid" {
						return nil, errors.New("secret status")
					}
					return json.Marshal(map[string]string{"BackendState": state})
				case "set":
					writes = append(writes, args)
					return nil, tc.setErr
				case "debug":
					return []byte(tc.after), nil
				default:
					t.Fatal(args)
					return nil, nil
				}
			}))
			err := client.SetRouting(t.Context(), tc.routes)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want=%v", err, tc.wantErr)
			}
			if (len(writes) == 1) != tc.write {
				t.Fatalf("writes=%v", writes)
			}
			if tc.write {
				routes, _ := ValidateSubnetRoutes(tc.routes)
				want := []string{"set", "--exit-node-allow-lan-access=false", "--advertise-exit-node=true", "--exit-node=", "--advertise-routes=" + strings.Join(routes, ",")}
				if !reflect.DeepEqual(writes[0], want) {
					t.Fatalf("args=%v want=%v", writes[0], want)
				}
			}
		})
	}
}

func TestSetRoutingMutationLockAndCancellation(t *testing.T) {
	client := NewClient("tailscale", "", 20*time.Millisecond, runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("commands while mutation lock held")
		return nil, nil
	}))
	client.mutations <- struct{}{}
	if err := client.SetRouting(t.Context(), nil); !errors.Is(err, ErrRoutingUnavailable) {
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
	if err := client.SetRouting(t.Context(), nil); !errors.Is(err, ErrRoutingApply) {
		t.Fatal(err)
	}
	if len(client.mutations) != 0 {
		t.Fatal("mutation lock leaked")
	}
}
