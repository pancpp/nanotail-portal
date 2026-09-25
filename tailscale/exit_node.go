package tailscale

import (
	"context"
	"errors"
	"log"
	"slices"
	"time"
)

const exitNodeCheckInterval = 10 * time.Second

func hasExitNodeRoutes(prefs Config) bool {
	return slices.Contains(prefs.exitRoutes, "0.0.0.0/0") && slices.Contains(prefs.exitRoutes, "::/0")
}

func exitNodeConfigured(prefs Config) bool {
	return hasExitNodeRoutes(prefs) && prefs.ExitNode == "" && prefs.ExitNodeID == "" && !prefs.ExitNodeAllowLANAccess
}

// EnsureExitNode reconciles the appliance's fixed role. It never logs in,
// starts a paused daemon, changes OS forwarding, or replaces subnet routes.
// The same lock as user mutations prevents races with logout and route changes.
func (c *Client) EnsureExitNode(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	select {
	case c.mutations <- struct{}{}:
		defer func() { <-c.mutations }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	status, err := c.Status(ctx)
	if err != nil {
		return ErrRoutingUnavailable
	}
	if !status.HaveNodeKey {
		return nil
	}
	switch status.BackendState {
	case "Running", "Stopped", "Starting":
	default:
		return nil // Wait for sign-in/approval; never resume it ourselves.
	}
	prefs, err := c.Config(ctx)
	if err != nil {
		return ErrRoutingUnavailable
	}
	if exitNodeConfigured(prefs) {
		return nil
	}
	address, allowLAN, advertise := "", false, true
	args, err := (ConfigUpdate{ExitNode: &address, ExitNodeAllowLANAccess: &allowLAN, AdvertiseExitNode: &advertise}).args()
	if err != nil {
		return ErrRoutingApply
	}
	if _, err := c.run(ctx, args...); err != nil {
		return ErrRoutingApply
	}
	prefs, err = c.Config(ctx)
	if err != nil || !exitNodeConfigured(prefs) {
		return ErrRoutingApply
	}
	return nil
}

// MaintainRouting checks at startup and after daemon/sign-in recovery. Each
// retry reads fresh state first, so an uncertain write is not blindly repeated.
func (c *Client) MaintainRouting(ctx context.Context) {
	maintainExitNode(ctx, func(ctx context.Context) error {
		return errors.Join(c.EnsureExitNode(ctx), c.EnsureSubnetDefaults(ctx))
	}, func(err error) {
		log.Printf("(routing) unable to ensure advertisements: %v", err)
	})
}

func maintainExitNode(ctx context.Context, ensure func(context.Context) error, report func(error)) {
	ticker := time.NewTicker(exitNodeCheckInterval)
	defer ticker.Stop()
	lastError := ""
	for ctx.Err() == nil {
		err := ensure(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if err.Error() != lastError {
				report(err)
			}
			lastError = err.Error()
		} else {
			lastError = ""
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
