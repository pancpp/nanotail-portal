package device

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

func interfaceIPs(addrs []net.Addr) []string {
	ips := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet == nil {
			continue // Do not invent a prefix for addresses without a mask.
		}
		if prefix, err := netip.ParsePrefix(ipNet.String()); err == nil {
			ips = append(ips, prefix.String()) // Preserve host bits, not just the subnet.
		}
	}
	slices.Sort(ips)
	return slices.Compact(ips)
}

// Scalar schema fields require one address per family. Prefer global unicast
// (including private/ULA addresses) over link-local, then lexical CIDR order.
func selectInterfaceIPs(addrs []net.Addr) (ipv4, ipv6 string) {
	var selected4, selected6 netip.Prefix
	for _, cidr := range interfaceIPs(addrs) {
		prefix, _ := netip.ParsePrefix(cidr)
		addr := prefix.Addr()
		if !addr.IsGlobalUnicast() && !addr.IsLinkLocalUnicast() {
			continue
		}
		selected := &selected6
		if addr.Is4() {
			selected = &selected4
		}
		if !selected.IsValid() || (selected.Addr().IsLinkLocalUnicast() && addr.IsGlobalUnicast()) {
			*selected = prefix
		}
	}
	if selected4.IsValid() {
		ipv4 = selected4.String()
	}
	if selected6.IsValid() {
		ipv6 = selected6.String()
	}
	return
}

type networkStatus struct {
	ipv4Method, ipv6Method string
	gateway, gateway6      string
	dns                    []string
}

func runNetworkManager(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "nmcli", args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	cmd.WaitDelay = time.Second
	return cmd.Output()
}

// Only read-only fields from eth0 and its active connection profile are queried.
// No shell, secrets, privileged operations, or network configuration changes.
func (r *Reader) readNetwork(ctx context.Context) (networkStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	data, err := r.runNM(ctx, "--colors", "no", "--escape", "no", "--terse", "--mode", "multiline",
		"--fields", "GENERAL.CON-PATH,IP4.GATEWAY,IP4.DNS,IP6.GATEWAY,IP6.DNS", "device", "show", "eth0")
	if ctx.Err() != nil {
		return networkStatus{}, ctx.Err()
	}
	if err != nil {
		return networkStatus{}, fmt.Errorf("read NetworkManager eth0 status: %w", err)
	}
	status, activePath, err := parseNMDevice(data)
	if err != nil {
		return networkStatus{}, err
	}
	if activePath == "" {
		return status, nil
	}
	data, err = r.runNM(ctx, "--colors", "no", "--escape", "no", "--get-values", "ipv4.method,ipv6.method",
		"connection", "show", "apath", activePath)
	if ctx.Err() != nil {
		return networkStatus{}, ctx.Err()
	}
	if err != nil {
		return networkStatus{}, fmt.Errorf("read NetworkManager address modes: %w", err)
	}
	methods := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(methods) != 2 {
		return networkStatus{}, errors.New("invalid NetworkManager address modes")
	}
	status.ipv4Method = addressMethod(strings.TrimSpace(methods[0]), false)
	status.ipv6Method = addressMethod(strings.TrimSpace(methods[1]), true)
	return status, nil
}

func addressMethod(method string, ipv6 bool) string {
	switch method {
	case "manual":
		return "static"
	case "auto":
		if ipv6 {
			return "auto"
		} // SLAAC and/or DHCPv6, not necessarily DHCP.
		return "DHCP"
	case "dhcp":
		return "DHCP"
	case "disabled", "ignore", "link-local", "shared":
		return method
	default:
		return "unknown"
	}
}

func parseNMDevice(data []byte) (networkStatus, string, error) {
	status := networkStatus{ipv4Method: "unknown", ipv6Method: "unknown", dns: []string{}}
	var activePath string
	foundPath := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":") // IPv6 colons belong to the value.
		if !ok {
			return networkStatus{}, "", errors.New("invalid NetworkManager device status")
		}
		value = strings.TrimSpace(value)
		if value == "--" {
			value = ""
		}
		switch {
		case key == "GENERAL.CON-PATH":
			foundPath, activePath = true, value
			if value != "" {
				id, ok := strings.CutPrefix(value, "/org/freedesktop/NetworkManager/ActiveConnection/")
				if _, err := strconv.ParseUint(id, 10, 64); !ok || err != nil {
					return networkStatus{}, "", errors.New("invalid NetworkManager active connection path")
				}
			}
		case key == "IP4.GATEWAY" || key == "IP6.GATEWAY":
			if value == "" {
				continue
			}
			addr, err := netip.ParseAddr(value)
			if err != nil || addr.Is4() != (key == "IP4.GATEWAY") {
				return networkStatus{}, "", errors.New("invalid NetworkManager gateway")
			}
			if addr.IsUnspecified() {
				continue
			}
			if addr.Is4() {
				status.gateway = addr.String()
			} else {
				status.gateway6 = addr.String()
			}
		case key == "IP4.DNS" || key == "IP6.DNS" || strings.HasPrefix(key, "IP4.DNS[") || strings.HasPrefix(key, "IP6.DNS["):
			if value == "" {
				continue
			}
			addr, err := netip.ParseAddr(value)
			if err != nil || addr.IsUnspecified() {
				return networkStatus{}, "", errors.New("invalid NetworkManager DNS address")
			}
			if !slices.Contains(status.dns, addr.String()) {
				status.dns = append(status.dns, addr.String())
			}
		}
	}
	if !foundPath {
		return networkStatus{}, "", errors.New("missing NetworkManager connection status")
	}
	return status, activePath, nil
}
