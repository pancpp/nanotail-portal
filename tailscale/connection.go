package tailscale

import (
	"context"
	"errors"
	"time"
)

var (
	ErrConnectionUnavailable = errors.New("Unable to read tailnet connection settings. Check that tailscaled is running and the portal can access it")
	ErrConnectionLogin       = errors.New("Sign in to Tailscale and approve this device before enabling the tailnet. Saved OAuth credentials alone do not enroll the device")
	ErrConnectionApply       = errors.New("Unable to confirm the tailnet connection change. It may already have applied. Reconnect over the LAN and reload settings before retrying")
)

type Connection struct {
	Enabled      bool
	BackendState string
	CanEnable    bool
}

func canEnable(status Status) bool {
	if !status.HaveNodeKey || (status.Self != nil && status.Self.KeyExpiry != nil && !status.Self.KeyExpiry.After(time.Now())) {
		return false
	}
	switch status.BackendState {
	case "Running", "Stopped", "Starting":
		return true
	default:
		return false
	}
}

func (c *Client) Connection(ctx context.Context) (Connection, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	prefs, err := c.Config(ctx)
	if err != nil {
		return Connection{}, ErrConnectionUnavailable
	}
	status, err := c.Status(ctx)
	if err != nil {
		return Connection{}, ErrConnectionUnavailable
	}
	return Connection{Enabled: prefs.WantRunning, BackendState: status.BackendState, CanEnable: canEnable(status)}, nil
}

// SetEnabled only resumes/pauses this device. Never log out, reset preferences,
// start interactive enrollment, or use saved OAuth credentials for enrollment.
func (c *Client) SetEnabled(ctx context.Context, enabled bool) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	select {
	case c.mutations <- struct{}{}:
		defer func() { <-c.mutations }()
	case <-ctx.Done():
		return ErrConnectionUnavailable
	}
	if ctx.Err() != nil {
		return ErrConnectionUnavailable
	}
	prefs, err := c.Config(ctx)
	if err != nil {
		return ErrConnectionUnavailable
	}
	if enabled {
		status, err := c.Status(ctx)
		if err != nil {
			return ErrConnectionUnavailable
		}
		if !canEnable(status) {
			return ErrConnectionLogin
		}
	}
	if prefs.WantRunning == enabled {
		return nil
	}
	command := "down"
	if enabled {
		command = "up"
	}
	// A bare up preserves existing preferences. Do not add --reset, --auth-key,
	// or even --timeout: bound execution with the context, not CLI flags.
	if _, err := c.run(ctx, command); err != nil {
		return ErrConnectionApply
	}
	prefs, err = c.Config(ctx)
	if err != nil || prefs.WantRunning != enabled {
		return ErrConnectionApply
	}
	return nil
}
