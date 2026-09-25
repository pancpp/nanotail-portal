package device

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidIP         = errors.New("Invalid LAN IPv4 settings")
	ErrConfigBusy        = errors.New("A LAN configuration change is already in progress; check device status before retrying")
	ErrConfigUnavailable = errors.New("Unable to read the active eth0 profile; check NetworkManager and the portal's permissions")
	ErrConfigApply       = errors.New("LAN configuration could not be applied; the previous settings were restored")
	ErrConfigRecovery    = errors.New("LAN configuration failed and recovery could not be confirmed; check the device locally before retrying")
)

type IPConfig struct {
	Type, IP, Gateway string
	DNS               []string
}

func ValidateIPConfig(input IPConfig) (IPConfig, error) {
	invalid := func(message string) (IPConfig, error) { return IPConfig{}, fmt.Errorf("%w: %s", ErrInvalidIP, message) }
	input.Type = strings.ToLower(strings.TrimSpace(input.Type))
	input.IP, input.Gateway = strings.TrimSpace(input.IP), strings.TrimSpace(input.Gateway)
	if input.Type == "dhcp" {
		if input.IP != "" || input.Gateway != "" || len(input.DNS) != 0 {
			return invalid("DHCP requires empty address, gateway, and DNS fields")
		}
		return IPConfig{Type: "DHCP", DNS: []string{}}, nil
	}
	if input.Type != "static" {
		return invalid("select static or DHCP")
	}
	prefix, err := netip.ParsePrefix(input.IP)
	if err != nil || !usableIPv4(prefix.Addr()) || prefix.Bits() < 1 || prefix.Bits() > 32 {
		return invalid("enter a unicast IPv4 address with a /1–/32 CIDR prefix")
	}
	if subnetEndpoint(prefix.Addr(), prefix) {
		return invalid("the address cannot be the subnet's network or broadcast address")
	}
	input.IP = prefix.String()
	if input.Gateway != "" {
		gateway, err := netip.ParseAddr(input.Gateway)
		if err != nil || !usableIPv4(gateway) || gateway == prefix.Addr() || !prefix.Contains(gateway) || subnetEndpoint(gateway, prefix) {
			return invalid("the gateway must be a different usable IPv4 address in the same subnet")
		}
		input.Gateway = gateway.String()
	}
	if len(input.DNS) > 8 {
		return invalid("enter at most eight IPv4 DNS servers")
	}
	dns := make([]string, 0, len(input.DNS))
	for _, value := range input.DNS {
		addr, err := netip.ParseAddr(strings.TrimSpace(value))
		if err != nil || !usableIPv4(addr) {
			return invalid("DNS servers must be unicast IPv4 addresses")
		}
		if !slices.Contains(dns, addr.String()) {
			dns = append(dns, addr.String())
		}
	}
	input.DNS = dns
	return input, nil
}

func usableIPv4(addr netip.Addr) bool {
	if !addr.Is4() || addr.IsLoopback() || addr.IsUnspecified() {
		return false
	}
	bytes := addr.As4()
	return bytes[0] != 0 && bytes[0] < 224
}

func subnetEndpoint(addr netip.Addr, prefix netip.Prefix) bool {
	if prefix.Bits() >= 31 {
		return false
	}
	base, ip := prefix.Masked().Addr().As4(), addr.As4()
	network := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
	value := uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
	broadcast := network | (^uint32(0) >> prefix.Bits())
	return value == network || value == broadcast
}

type Configurator struct {
	mu    sync.Mutex
	runNM func(context.Context, ...string) ([]byte, error)
}

func NewConfigurator() *Configurator { return &Configurator{runNM: runNetworkManager} }

var profileUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var ipv4Properties = []string{"ipv4.method", "ipv4.addresses", "ipv4.gateway", "ipv4.dns", "ipv4.ignore-auto-dns"}

func (c *Configurator) SetIP(ctx context.Context, input IPConfig) error {
	config, err := ValidateIPConfig(input)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !c.mu.TryLock() {
		return ErrConfigBusy
	}
	defer c.mu.Unlock()
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	data, err := c.runNM(readCtx, "--colors", "no", "--escape", "no", "--terse", "--mode", "multiline", "--fields", "GENERAL.CON-PATH", "device", "show", "eth0")
	if err != nil {
		return configReadError(err)
	}
	_, activePath, err := parseNMDevice(data)
	if err != nil || activePath == "" {
		return ErrConfigUnavailable
	}
	fields := "connection.uuid," + strings.Join(ipv4Properties, ",")
	data, err = c.runNM(readCtx, "--colors", "no", "--escape", "no", "--terse", "--mode", "multiline", "--fields", fields, "connection", "show", "apath", activePath)
	if err != nil {
		return configReadError(err)
	}
	uuid, previous, err := parseIPProfile(data)
	if err != nil {
		return configReadError(err)
	}
	if err := readCtx.Err(); err != nil {
		return err
	}
	values := []string{"manual", config.IP, config.Gateway, strings.Join(config.DNS, ","), "yes"}
	if config.Type == "DHCP" {
		values = []string{"auto", "", "", "", "no"}
	}

	// Once writes begin, finish or recover even if changing the address closes
	// the HTTP connection. Every subprocess still has a bounded lifetime.
	applyCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer stop()
	_, err = c.runNM(applyCtx, modifyIPArgs(uuid, values)...)
	if err == nil {
		_, err = c.runNM(applyCtx, "--wait", "15", "device", "reapply", "eth0")
	}
	if err == nil && applyCtx.Err() == nil {
		return nil
	}
	log.Printf("(device) LAN configuration apply failed: %v", err)
	rollbackCtx, finish := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer finish()
	if _, err := c.runNM(rollbackCtx, modifyIPArgs(uuid, previous)...); err != nil {
		log.Printf("(device) LAN profile recovery failed: %v", err)
		return ErrConfigRecovery
	}
	if _, err := c.runNM(rollbackCtx, "--wait", "15", "device", "reapply", "eth0"); err != nil || rollbackCtx.Err() != nil {
		log.Printf("(device) LAN link recovery failed: %v", err)
		return ErrConfigRecovery
	}
	return ErrConfigApply
}

func configReadError(err error) error {
	log.Printf("(device) unable to read LAN profile: %v", err)
	return ErrConfigUnavailable
}

func modifyIPArgs(uuid string, values []string) []string {
	args := []string{"--wait", "15", "connection", "modify", "uuid", uuid}
	for i, property := range ipv4Properties {
		args = append(args, property, values[i])
	}
	return args
}

func parseIPProfile(data []byte) (string, []string, error) {
	fields := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return "", nil, errors.New("invalid NetworkManager profile")
		}
		value = strings.TrimSpace(value)
		if value == "--" {
			value = ""
		}
		fields[key] = value
	}
	uuid := fields["connection.uuid"]
	if !profileUUID.MatchString(uuid) {
		return "", nil, errors.New("invalid NetworkManager profile UUID")
	}
	previous := make([]string, 0, len(ipv4Properties))
	for _, key := range ipv4Properties {
		value, ok := fields[key]
		if !ok {
			return "", nil, errors.New("incomplete NetworkManager IPv4 profile")
		}
		previous = append(previous, value)
	}
	return uuid, previous, nil
}
