package tailscale

import (
	"context"
	"errors"
)

var (
	ErrRoutingPersistence        = errors.New("Unable to save the subnet routing choice. No routing command was sent; check the portal database before retrying")
	ErrSubnetDefaultsUnavailable = errors.New("Local LAN subnet defaults are unavailable. Waiting for usable LAN addresses, or configure subnet routes manually")
)

// SubnetDefaultsStore distinguishes an untouched installation from a user's
// explicit empty route list. A marker is durable across portal restarts.
type SubnetDefaultsStore interface {
	Configured(context.Context) (bool, error)
	MarkConfigured(context.Context) error
}

// WithSubnetDefaults installs the default policy before the client is shared.
func (c *Client) WithSubnetDefaults(store SubnetDefaultsStore, detect func(context.Context) ([]string, error)) *Client {
	c.subnetDefaults, c.detectSubnets = store, detect
	return c
}

func (c *Client) subnetDefaultsPending(ctx context.Context) (bool, error) {
	if c.subnetDefaults == nil {
		return false, nil
	}
	configured, err := c.subnetDefaults.Configured(ctx)
	if err != nil {
		return false, ErrRoutingUnavailable
	}
	return !configured, nil
}

// claimSubnetChoice is called under the mutation lock before an explicit write.
// Even an uncertain/failed command must not allow a background default to undo
// the user's intent. Invalid inputs never reach this point.
func (c *Client) claimSubnetChoice(ctx context.Context) error {
	if c.subnetDefaults != nil {
		if err := c.subnetDefaults.MarkConfigured(ctx); err != nil {
			return ErrRoutingPersistence
		}
	}
	return nil
}

// EnsureSubnetDefaults advertises the local LAN once, without a browser visit.
// Existing/custom routes are adopted unchanged. Missing LAN or Tailscale is
// retried later, but a saved choice (including off) is never auto-enabled again.
func (c *Client) EnsureSubnetDefaults(ctx context.Context) error {
	if c.subnetDefaults == nil {
		return nil
	}
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
	pending, err := c.subnetDefaultsPending(ctx)
	if err != nil || !pending {
		return err
	}
	status, err := c.Status(ctx)
	if err != nil {
		return ErrRoutingUnavailable
	}
	if !status.HaveNodeKey || status.BackendState != "Running" {
		return nil
	}
	prefs, err := c.Config(ctx)
	if err != nil {
		return ErrRoutingUnavailable
	}
	if len(prefs.AdvertiseRoutes) > 0 {
		// Also handles a successful CLI write whose reply or marker write was lost.
		return c.claimSubnetChoice(ctx)
	}
	if c.detectSubnets == nil {
		return ErrSubnetDefaultsUnavailable
	}
	routes, err := c.detectSubnets(ctx)
	if err != nil {
		return ErrSubnetDefaultsUnavailable
	}
	routes, err = ValidateSubnetRoutes(routes)
	if err != nil || len(routes) == 0 {
		return ErrSubnetDefaultsUnavailable
	}
	if err := c.applyRouting(ctx, routes); err != nil {
		return err
	}
	// Do not mark before a default write: failed first-time setup must retry.
	if err := c.claimSubnetChoice(ctx); err != nil {
		return ErrRoutingApply // CLI already applied; next check adopts its readback.
	}
	return nil
}
