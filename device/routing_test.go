package device

import (
	"context"
	"errors"
	"net"
	"os"
	"reflect"
	"testing"
)

func TestRoutingHostReadiness(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cidrs   []string
		want    []string
		missing bool
	}{
		{name: "LAN masking and dedup", cidrs: []string{"192.168.42.8/24", "192.168.42.9/24", "fd00:1234::7/64", "fe80::1/64", "100.64.0.2/32", "127.0.0.1/8"}, want: []string{"192.168.42.0/24", "fd00:1234::/64"}},
		{name: "no LAN", want: []string{}},
		{name: "missing interface", missing: true, want: []string{}},
		{name: "never default route", cidrs: []string{"192.168.42.8/0"}, want: []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &Reader{
				interfaceInfo: func(name string) (net.HardwareAddr, []net.Addr, error) {
					if name != "eth0" {
						t.Fatal(name)
					}
					if tc.missing {
						return nil, nil, errors.New("private error")
					}
					var addrs []net.Addr
					for _, cidr := range tc.cidrs {
						ip, network, _ := net.ParseCIDR(cidr)
						network.IP = ip
						addrs = append(addrs, network)
					}
					return nil, addrs, nil
				},
				readFile: func(path string) ([]byte, error) {
					switch path {
					case "/proc/sys/net/ipv4/ip_forward":
						return []byte("1\n"), nil
					case "/proc/sys/net/ipv6/conf/all/forwarding":
						return []byte("0\n"), nil
					default:
						t.Fatal(path)
						return nil, nil
					}
				},
			}
			got, err := reader.RoutingStatus(t.Context())
			if err != nil || !reflect.DeepEqual(got.DefaultSubnetRoutes, tc.want) || got.LANInterface != "eth0" {
				t.Fatalf("%+v %v", got, err)
			}
			if got.IPv4Forwarding == nil || !*got.IPv4Forwarding || got.IPv6Forwarding == nil || *got.IPv6Forwarding {
				t.Fatalf("forwarding %+v", got)
			}
			if (got.LANWarning != "") != (len(tc.want) == 0) {
				t.Fatal(got.LANWarning)
			}
		})
	}
}

func TestForwardingUnknownAndCancellation(t *testing.T) {
	for _, value := range []string{"", "2", "invalid"} {
		reader := &Reader{readFile: func(string) ([]byte, error) { return []byte(value), nil }}
		if got := reader.forwarding("fake"); got != nil {
			t.Fatal(got)
		}
	}
	reader := &Reader{readFile: func(string) ([]byte, error) { return nil, os.ErrPermission }}
	if got := reader.forwarding("fake"); got != nil {
		t.Fatal(got)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := reader.RoutingStatus(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
