package device

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

const nmActivePath = "/org/freedesktop/NetworkManager/ActiveConnection/7"
const nmDeviceFixture = "GENERAL.CON-PATH:" + nmActivePath + "\n" +
	"IP4.GATEWAY:192.0.2.1\nIP4.DNS[1]:192.0.2.53\n" +
	"IP6.GATEWAY:fe80::1\nIP6.DNS[1]:2001:db8::53\n"

func TestInterfaceIPs(t *testing.T) {
	got := interfaceIPs([]net.Addr{
		&net.IPNet{IP: net.ParseIP("fd00::1"), Mask: net.CIDRMask(64, 128)},
		&net.IPNet{IP: net.ParseIP("fe80::1234"), Mask: net.CIDRMask(64, 128)},
		&net.IPNet{IP: net.ParseIP("192.0.2.123"), Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.IP{192, 0, 2, 123}, Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.ParseIP("192.0.2.123"), Mask: net.CIDRMask(32, 32)},
		&net.IPNet{IP: net.ParseIP("::ffff:198.51.100.2"), Mask: net.CIDRMask(120, 128)},
		&net.IPNet{IP: net.ParseIP("2001:db8::2"), Mask: net.CIDRMask(128, 128)},
		&net.IPNet{IP: net.ParseIP("192.0.2.1"), Mask: net.IPMask{255, 0, 255, 0}},
		&net.IPNet{IP: net.ParseIP("fd00::2"), Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.ParseIP("192.0.2.1")}, &net.IPNet{Mask: net.CIDRMask(24, 32)},
		&net.IPAddr{IP: net.ParseIP("192.0.2.1")},
		nil, (*net.IPNet)(nil), (*net.IPAddr)(nil), &net.IPAddr{}, &net.UnixAddr{},
	})
	want := []string{"192.0.2.123/24", "192.0.2.123/32", "198.51.100.2/24", "2001:db8::2/128", "fd00::1/64", "fe80::1234/64"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("IPs = %v, want %v", got, want)
	}
}

func TestSelectInterfaceIPs(t *testing.T) {
	for _, tt := range []struct {
		name   string
		cidrs  []string
		v4, v6 string
	}{
		{"absent", nil, "", ""},
		{"link local fallback", []string{"fe80::2/64", "169.254.1.2/16"}, "169.254.1.2/16", "fe80::2/64"},
		{"prefer unicast", []string{"fe80::2/64", "169.254.1.2/16", "fd00::2/64", "192.0.2.2/24"}, "192.0.2.2/24", "fd00::2/64"},
		{"stable selection", []string{"192.0.2.9/24", "2001:db8::9/64", "192.0.2.2/24", "2001:db8::2/64"}, "192.0.2.2/24", "2001:db8::2/64"},
		{"no loopback or multicast", []string{"127.0.0.1/8", "::1/128", "::/128", "ff02::1/64", "224.0.0.1/4"}, "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var addrs []net.Addr
			for _, cidr := range tt.cidrs {
				ip, network, err := net.ParseCIDR(cidr)
				if err != nil {
					t.Fatal(err)
				}
				network.IP = ip
				addrs = append(addrs, network)
			}
			v4, v6 := selectInterfaceIPs(addrs)
			if v4 != tt.v4 || v6 != tt.v6 {
				t.Fatalf("got %q, %q; want %q, %q", v4, v6, tt.v4, tt.v6)
			}
		})
	}
}

func TestParseNMDevice(t *testing.T) {
	status, path, err := parseNMDevice([]byte(nmDeviceFixture + "IP4.DNS[2]:192.0.2.53\nIP6.DNS[2]:fe80::53%eth0\n"))
	want := networkStatus{ipv4Method: "unknown", ipv6Method: "unknown", gateway: "192.0.2.1", gateway6: "fe80::1",
		dns: []string{"192.0.2.53", "2001:db8::53", "fe80::53%eth0"}}
	if err != nil || path != nmActivePath || !reflect.DeepEqual(status, want) {
		t.Fatalf("unexpected status: %+v %q %v", status, path, err)
	}
	for _, data := range []string{"GENERAL.CON-PATH:\n", "GENERAL.CON-PATH:--\nIP4.GATEWAY:--\nIP6.GATEWAY:\nIP4.DNS:\n"} {
		status, path, err := parseNMDevice([]byte(data))
		if err != nil || path != "" || !reflect.DeepEqual(status, networkStatus{ipv4Method: "unknown", ipv6Method: "unknown", dns: []string{}}) {
			t.Fatalf("disconnected device: %+v %q %v", status, path, err)
		}
	}
	for _, data := range []string{"", "invalid", "IP4.GATEWAY:192.0.2.1\n", "GENERAL.CON-PATH:invalid\n",
		nmDeviceFixture + "IP4.GATEWAY:fe80::1\n", nmDeviceFixture + "IP6.GATEWAY:192.0.2.1\n",
		nmDeviceFixture + "IP4.DNS[2]:invalid\n", nmDeviceFixture + "IP6.DNS[2]:::\n",
	} {
		if _, _, err := parseNMDevice([]byte(data)); err == nil {
			t.Errorf("accepted invalid NM data: %q", data)
		}
	}
}

func TestAddressMethod(t *testing.T) {
	for _, tt := range []struct{ method, v4, v6 string }{
		{"manual", "static", "static"}, {"auto", "DHCP", "auto"}, {"dhcp", "DHCP", "DHCP"},
		{"disabled", "disabled", "disabled"}, {"ignore", "ignore", "ignore"},
		{"link-local", "link-local", "link-local"}, {"shared", "shared", "shared"},
		{"future", "unknown", "unknown"}, {"", "unknown", "unknown"},
	} {
		if got := addressMethod(tt.method, false); got != tt.v4 {
			t.Errorf("IPv4 %q = %q", tt.method, got)
		}
		if got := addressMethod(tt.method, true); got != tt.v6 {
			t.Errorf("IPv6 %q = %q", tt.method, got)
		}
	}
}

func TestReadNetwork(t *testing.T) {
	r := testReader(t)
	calls := 0
	r.runNM = func(ctx context.Context, args ...string) ([]byte, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 3*time.Second {
			t.Fatal("missing command timeout")
		}
		calls++
		if calls == 1 {
			want := []string{"--colors", "no", "--escape", "no", "--terse", "--mode", "multiline", "--fields",
				"GENERAL.CON-PATH,IP4.GATEWAY,IP4.DNS,IP6.GATEWAY,IP6.DNS", "device", "show", "eth0"}
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("device command: %v", args)
			}
			return []byte(nmDeviceFixture), nil
		}
		want := []string{"--colors", "no", "--escape", "no", "--get-values", "ipv4.method,ipv6.method", "connection", "show", "apath", nmActivePath}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("profile command: %v", args)
		}
		return []byte("manual\ndhcp\n"), nil
	}
	status, err := r.Status(t.Context())
	if err != nil || status.LANIPType != "static" || status.LANIPv6Type != "DHCP" || calls != 2 {
		t.Fatalf("status: %+v %v", status, err)
	}
}

func TestReadNetworkNoActiveProfile(t *testing.T) {
	r := testReader(t)
	calls := 0
	r.runNM = func(context.Context, ...string) ([]byte, error) { calls++; return []byte("GENERAL.CON-PATH:--\n"), nil }
	status, err := r.Status(t.Context())
	if err != nil || calls != 1 || status.LANIPType != "unknown" || status.LANIPv6Type != "unknown" ||
		status.Gateway != "" || status.Gateway6 != "" || status.DNS == nil || len(status.DNS) != 0 {
		t.Fatalf("status: %+v %v", status, err)
	}
}

func TestReadNetworkFailures(t *testing.T) {
	failure := errors.New("private nmcli failure")
	for _, failedCall := range []int{1, 2} {
		for _, malformed := range []bool{false, true} {
			r := testReader(t)
			calls := 0
			r.runNM = func(context.Context, ...string) ([]byte, error) {
				calls++
				if calls == failedCall {
					if malformed {
						return []byte("invalid"), nil
					}
					return nil, failure
				}
				return []byte(nmDeviceFixture), nil
			}
			status, err := r.Status(t.Context())
			if err == nil || status.Health != "" || (!malformed && !errors.Is(err, failure)) {
				t.Fatalf("failure hidden: %+v %v", status, err)
			}
		}
	}
}

func TestReadNetworkCancellation(t *testing.T) {
	r := testReader(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r.runNM = func(ctx context.Context, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "device show") {
			cancel()
		}
		return nil, errors.New("killed")
	}
	if _, err := r.Status(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
