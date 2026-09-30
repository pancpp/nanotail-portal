package device

import (
	"context"
	"errors"
	"log"
	"regexp"
	"strings"
	"time"
)

var (
	ErrInvalidHostname  = errors.New("Enter a hostname of 1–63 letters, digits, or hyphens, starting and ending with a letter or digit.")
	ErrReservedHostname = errors.New("Choose a hostname other than localhost or localhost6.")
	ErrHostnameBusy     = errors.New("A device configuration change is already in progress; check device status before retrying.")
	ErrHostnameApply    = errors.New("Hostname change could not be confirmed. Check device status, NetworkManager, and the portal's permissions before retrying.")
	hostnamePattern     = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
)

func ValidateHostname(input string) (string, error) {
	hostname := strings.TrimSpace(input)
	if !hostnamePattern.MatchString(hostname) {
		return "", ErrInvalidHostname
	}
	hostname = strings.ToLower(hostname)
	if hostname == "localhost" || hostname == "localhost6" {
		return "", ErrReservedHostname
	}
	return hostname, nil
}

// SetHostname persists the system hostname through NetworkManager. It shares
// the LAN configuration lock and never restarts the connection or Tailscale.
func (c *Configurator) SetHostname(ctx context.Context, input string) error {
	hostname, err := ValidateHostname(input)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !c.mu.TryLock() {
		return ErrHostnameBusy
	}
	defer c.mu.Unlock()

	// Finish the bounded write/readback even if the browser loses name resolution.
	applyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if _, err := c.runNM(applyCtx, "--wait", "10", "general", "hostname", hostname); err != nil {
		log.Printf("set device hostname: %v", err)
		return ErrHostnameApply
	}
	saved, err := c.runNM(applyCtx, "--terse", "general", "hostname")
	if err != nil {
		log.Printf("read back device hostname: %v", err)
		return ErrHostnameApply
	}
	if applyCtx.Err() != nil || strings.TrimSpace(string(saved)) != hostname {
		return ErrHostnameApply
	}
	return nil
}
