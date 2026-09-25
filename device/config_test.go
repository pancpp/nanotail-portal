package device

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func staticIP() IPConfig {
	return IPConfig{Type: "static", IP: "192.0.2.20/24", Gateway: "192.0.2.1", DNS: []string{"192.0.2.53", "1.1.1.1"}}
}

func TestValidateIPConfig(t *testing.T) {
	for _, tt := range []struct {
		name        string
		input, want IPConfig
	}{
		{"static", staticIP(), staticIP()},
		{"normalization", IPConfig{" STATIC ", " 192.0.2.20/24 ", " 192.0.2.1 ", []string{" 1.1.1.1 ", "1.1.1.1"}}, IPConfig{"static", "192.0.2.20/24", "192.0.2.1", []string{"1.1.1.1"}}},
		{"DHCP", IPConfig{Type: " dhcp "}, IPConfig{Type: "DHCP", DNS: []string{}}},
		{"optional gateway and DNS", IPConfig{Type: "static", IP: "192.0.2.20/24"}, IPConfig{Type: "static", IP: "192.0.2.20/24", DNS: []string{}}},
		{"point to point", IPConfig{Type: "static", IP: "192.0.2.0/31", Gateway: "192.0.2.1"}, IPConfig{Type: "static", IP: "192.0.2.0/31", Gateway: "192.0.2.1", DNS: []string{}}},
		{"host prefix", IPConfig{Type: "static", IP: "192.0.2.20/32"}, IPConfig{Type: "static", IP: "192.0.2.20/32", DNS: []string{}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateIPConfig(tt.input)
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
	invalid := []IPConfig{{}, {Type: "auto"}, {Type: "DHCP", IP: "192.0.2.20/24"}, {Type: "DHCP", Gateway: "192.0.2.1"}, {Type: "DHCP", DNS: []string{"1.1.1.1"}}}
	for _, ip := range []string{"", "192.0.2.20", "192.0.2.20/0", "192.0.2.20/33", "192.0.2.0/24", "192.0.2.255/24", "127.0.0.1/8", "0.1.2.3/24", "224.0.0.1/24", "255.255.255.255/32", "::ffff:192.0.2.20/120", "fd00::2/64", "192.0.2.20/24;reboot", "192.000.2.20/24"} {
		input := staticIP()
		input.IP = ip
		invalid = append(invalid, input)
	}
	for _, gateway := range []string{"192.0.3.1", "192.0.2.20", "192.0.2.0", "192.0.2.255", "::1", "192.0.2.1/24", "192.0.2.1;reboot"} {
		input := staticIP()
		input.Gateway = gateway
		invalid = append(invalid, input)
	}
	for _, dns := range [][]string{{""}, {"::1"}, {"127.0.0.1"}, {"224.0.0.1"}, {"1.1.1.1;reboot"}, {"1.1.1.1", "1.1.1.1", "1.1.1.1", "1.1.1.1", "1.1.1.1", "1.1.1.1", "1.1.1.1", "1.1.1.1", "1.1.1.1"}} {
		input := staticIP()
		input.DNS = dns
		invalid = append(invalid, input)
	}
	for _, input := range invalid {
		c := &Configurator{runNM: func(context.Context, ...string) ([]byte, error) {
			t.Fatal("invalid input reached nmcli")
			return nil, nil
		}}
		if err := c.SetIP(t.Context(), input); !errors.Is(err, ErrInvalidIP) {
			t.Errorf("accepted %+v: %v", input, err)
		}
	}
}

const testProfileUUID = "12345678-1234-1234-1234-123456789abc"
const testActivePath = "/org/freedesktop/NetworkManager/ActiveConnection/2"
const testProfile = "connection.uuid:" + testProfileUUID + "\nipv4.method:manual\nipv4.addresses:192.0.2.2/24,192.0.2.3/24\nipv4.gateway:192.0.2.1\nipv4.dns:192.0.2.53,1.0.0.1\nipv4.ignore-auto-dns:yes\n"

func profileReadResult(call int) []byte {
	if call == 0 {
		return []byte("GENERAL.CON-PATH:" + testActivePath + "\n")
	}
	return []byte(testProfile)
}

func TestSetIPCommands(t *testing.T) {
	for _, config := range []IPConfig{staticIP(), {Type: "DHCP"}} {
		t.Run(config.Type, func(t *testing.T) {
			var calls [][]string
			c := &Configurator{runNM: func(ctx context.Context, args ...string) ([]byte, error) {
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("unbounded nmcli command")
				}
				calls = append(calls, args)
				if len(calls) <= 2 {
					return profileReadResult(len(calls) - 1), nil
				}
				return nil, nil
			}}
			if err := c.SetIP(t.Context(), config); err != nil {
				t.Fatal(err)
			}
			values := []string{"manual", config.IP, config.Gateway, strings.Join(config.DNS, ","), "yes"}
			if config.Type == "DHCP" {
				values = []string{"auto", "", "", "", "no"}
			}
			want := [][]string{
				{"--colors", "no", "--escape", "no", "--terse", "--mode", "multiline", "--fields", "GENERAL.CON-PATH", "device", "show", "eth0"},
				{"--colors", "no", "--escape", "no", "--terse", "--mode", "multiline", "--fields", "connection.uuid,ipv4.method,ipv4.addresses,ipv4.gateway,ipv4.dns,ipv4.ignore-auto-dns", "connection", "show", "apath", testActivePath},
				{"--wait", "15", "connection", "modify", "uuid", testProfileUUID,
					"ipv4.method", values[0], "ipv4.addresses", values[1], "ipv4.gateway", values[2],
					"ipv4.dns", values[3], "ipv4.ignore-auto-dns", values[4]},
				{"--wait", "15", "device", "reapply", "eth0"},
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("commands = %q, want %q", calls, want)
			}
			for _, args := range calls {
				for _, arg := range args {
					if strings.HasPrefix(arg, "ipv6.") {
						t.Fatalf("IPv6 property was modified: %s", arg)
					}
				}
			}
		})
	}
}

func TestSetIPReadFailuresDoNotWrite(t *testing.T) {
	for _, tt := range []struct {
		name   string
		failAt int
		output string
		err    error
	}{
		{"nmcli unavailable", 0, "", errors.New("private OS details")},
		{"no active profile", 0, "GENERAL.CON-PATH:--\n", nil},
		{"invalid active path", 0, "GENERAL.CON-PATH:not-a-path\n", nil},
		{"profile read failure", 1, "", errors.New("permission denied")},
		{"missing properties", 1, "connection.uuid:" + testProfileUUID, nil},
		{"invalid UUID", 1, strings.Replace(testProfile, testProfileUUID, "--malicious", 1), nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			c := &Configurator{runNM: func(_ context.Context, args ...string) ([]byte, error) {
				call := calls
				calls++
				if call > tt.failAt {
					t.Fatalf("unexpected command after read failure: %v", args)
				}
				if call == tt.failAt {
					return []byte(tt.output), tt.err
				}
				return profileReadResult(call), nil
			}}
			if err := c.SetIP(t.Context(), staticIP()); !errors.Is(err, ErrConfigUnavailable) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestSetIPRecovery(t *testing.T) {
	for _, tt := range []struct {
		name                          string
		applyFailure, recoveryFailure int
		want                          error
	}{
		{"modify failure", 2, -1, ErrConfigApply},
		{"reapply failure", 3, -1, ErrConfigApply},
		{"profile recovery failure", 3, 4, ErrConfigRecovery},
		{"link recovery failure", 3, 5, ErrConfigRecovery},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls [][]string
			c := &Configurator{runNM: func(ctx context.Context, args ...string) ([]byte, error) {
				call := len(calls)
				calls = append(calls, args)
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("unbounded recovery command")
				}
				if call < 2 {
					return profileReadResult(call), nil
				}
				if call == tt.applyFailure || call == tt.recoveryFailure {
					return nil, errors.New("private nmcli diagnostic")
				}
				return nil, nil
			}}
			if err := c.SetIP(t.Context(), staticIP()); !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
			_, previous, _ := parseIPProfile([]byte(testProfile))
			if !reflect.DeepEqual(calls[tt.applyFailure+1], modifyIPArgs(testProfileUUID, previous)) {
				t.Fatalf("old profile not restored exactly: %v", calls)
			}
			wantCount := tt.applyFailure + 3
			if tt.recoveryFailure >= 0 {
				wantCount = tt.recoveryFailure + 1
			}
			if len(calls) != wantCount {
				t.Fatalf("got %d calls, want %d", len(calls), wantCount)
			}
		})
	}
}

func TestSetIPCancellation(t *testing.T) {
	for _, cancelAt := range []int{-1, 1, 2, 3} {
		ctx, cancel := context.WithCancel(t.Context())
		calls := 0
		if cancelAt == -1 {
			cancel()
		}
		c := &Configurator{runNM: func(commandCtx context.Context, args ...string) ([]byte, error) {
			call := calls
			calls++
			if call == cancelAt {
				cancel()
			}
			if call < 2 {
				return profileReadResult(call), nil
			}
			if commandCtx.Err() != nil {
				t.Fatal("request cancellation interrupted a write")
			}
			return nil, nil
		}}
		err := c.SetIP(ctx, staticIP())
		cancel()
		if cancelAt < 2 {
			if !errors.Is(err, context.Canceled) || calls > 2 {
				t.Fatalf("canceled read wrote settings: %d %v", calls, err)
			}
		} else if err != nil || calls != 4 {
			t.Fatalf("write did not finish after disconnect: %d %v", calls, err)
		}
	}
}

func TestSetIPRejectsConcurrentChanges(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	calls := 0
	c := &Configurator{runNM: func(_ context.Context, _ ...string) ([]byte, error) {
		call := calls
		calls++
		if call == 0 {
			close(started)
			<-release
		}
		if call < 2 {
			return profileReadResult(call), nil
		}
		return nil, nil
	}}
	result := make(chan error, 1)
	go func() { result <- c.SetIP(t.Context(), staticIP()) }()
	<-started
	err := c.SetIP(t.Context(), staticIP())
	close(release)
	if !errors.Is(err, ErrConfigBusy) {
		t.Errorf("concurrent change returned %v", err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatalf("concurrent command leaked through: %d", calls)
	}
}

func TestSetIPRecoverySurvivesDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	c := &Configurator{runNM: func(commandCtx context.Context, _ ...string) ([]byte, error) {
		call := calls
		calls++
		if call < 2 {
			return profileReadResult(call), nil
		}
		if call == 3 {
			cancel()
			return nil, errors.New("apply failed after browser disconnected")
		}
		if commandCtx.Err() != nil {
			t.Fatal("browser disconnection interrupted recovery")
		}
		if _, ok := commandCtx.Deadline(); !ok {
			t.Fatal("recovery has no timeout")
		}
		return nil, nil
	}}
	if err := c.SetIP(ctx, staticIP()); !errors.Is(err, ErrConfigApply) || calls != 6 {
		t.Fatalf("recovery after disconnect: %v, %d commands", err, calls)
	}
}

func TestParseIPProfileEmptyValues(t *testing.T) {
	data := "connection.uuid:" + testProfileUUID + "\nipv4.method:auto\nipv4.addresses:--\nipv4.gateway:\nipv4.dns:\nipv4.ignore-auto-dns:no\n"
	uuid, values, err := parseIPProfile([]byte(data))
	if err != nil || uuid != testProfileUUID || !reflect.DeepEqual(values, []string{"auto", "", "", "", "no"}) {
		t.Fatalf("got %q %v %v", uuid, values, err)
	}
}
