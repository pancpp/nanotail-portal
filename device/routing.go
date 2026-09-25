package device

import (
	"context"
	"net/netip"
	"slices"
	"strings"
)

// RoutingStatus is read-only host readiness. A nil forwarding value means
// unknown, not disabled. LAN detection does not depend on NetworkManager/CPU data.
type RoutingStatus struct {
	LANInterface                   string
	DefaultSubnetRoutes            []string
	LANWarning                     string
	IPv4Forwarding, IPv6Forwarding *bool
}

func (r *Reader) RoutingStatus(ctx context.Context) (RoutingStatus, error) {
	if err := ctx.Err(); err != nil {
		return RoutingStatus{}, err
	}
	result := RoutingStatus{LANInterface: "eth0", DefaultSubnetRoutes: []string{}}
	_, addrs, err := r.interfaceInfo(result.LANInterface)
	if err != nil {
		result.LANWarning = "Unable to detect the local LAN on eth0. Enter subnet routes manually."
	} else {
		for _, cidr := range interfaceIPs(addrs) {
			prefix, _ := netip.ParsePrefix(cidr)
			if !prefix.Addr().IsGlobalUnicast() || prefix.Addr().Is4In6() || prefix.Bits() == 0 {
				continue
			}
			prefix = prefix.Masked()
			if prefix.Overlaps(netip.MustParsePrefix("100.64.0.0/10")) || prefix.Overlaps(netip.MustParsePrefix("fd7a:115c:a1e0::/48")) {
				continue
			}
			result.DefaultSubnetRoutes = append(result.DefaultSubnetRoutes, prefix.String())
		}
		slices.Sort(result.DefaultSubnetRoutes)
		result.DefaultSubnetRoutes = slices.Compact(result.DefaultSubnetRoutes)
		if len(result.DefaultSubnetRoutes) == 0 {
			result.LANWarning = "No usable LAN subnet was detected on eth0. Enter subnet routes manually."
		}
	}
	result.IPv4Forwarding = r.forwarding("/proc/sys/net/ipv4/ip_forward")
	result.IPv6Forwarding = r.forwarding("/proc/sys/net/ipv6/conf/all/forwarding")
	return result, ctx.Err()
}

func (r *Reader) forwarding(path string) *bool {
	data, err := r.readFile(path)
	if err != nil {
		return nil
	}
	switch strings.TrimSpace(string(data)) {
	case "1":
		value := true
		return &value
	case "0":
		value := false
		return &value
	default:
		return nil
	}
}
