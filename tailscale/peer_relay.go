package tailscale

import (
	"context"
	"errors"
	"strconv"
)

const DefaultPeerRelayPort = 40001

var (
	ErrPeerRelayPort        = errors.New("Enter a UDP port from 1 to 65535")
	ErrPeerRelayUnavailable = errors.New("Unable to read peer relay settings. Check that tailscaled is running and the portal can access it")
	ErrPeerRelayApply       = errors.New("Unable to confirm the peer relay change. It may already have applied. Reload peer relay settings before retrying; check Tailscale version and daemon permissions")
)

// SetPeerRelay ensures the device's relay grant before enabling its listener.
// Disabling only changes the listener; existing tailnet grants are preserved.
func (c *Client) SetPeerRelay(ctx context.Context, enabled bool, port int) error {
	if port < 1 || port > 65535 {
		return ErrPeerRelayPort
	}
	requestCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	select {
	case c.mutations <- struct{}{}:
		defer func() { <-c.mutations }()
	case <-ctx.Done():
		return ErrPeerRelayUnavailable
	}
	if requestCtx.Err() != nil || ctx.Err() != nil {
		return ErrPeerRelayUnavailable
	}
	prefs, err := c.Config(ctx)
	if err != nil {
		return ErrPeerRelayUnavailable
	}
	if enabled {
		if err := c.ensurePeerRelayPolicy(ctx); err != nil {
			return err
		}
	}
	matches := func(prefs Config) bool {
		return (!enabled && prefs.RelayServerPort == nil) ||
			(enabled && prefs.RelayServerPort != nil && int(*prefs.RelayServerPort) == port)
	}
	if matches(prefs) {
		return nil
	}
	value := ""
	if enabled {
		value = strconv.Itoa(port)
	}
	// Empty disables the relay; zero would enable a randomly assigned port.
	if _, err := c.run(ctx, "set", "--relay-server-port="+value); err != nil {
		return ErrPeerRelayApply
	}
	prefs, err = c.Config(ctx)
	if err != nil || !matches(prefs) {
		return ErrPeerRelayApply
	}
	return nil
}
