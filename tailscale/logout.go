package tailscale

import (
	"context"
	"errors"
)

var ErrLogoutApply = errors.New("Unable to confirm Tailscale logout. It may already have applied. Reconnect over the LAN and reload connection settings before retrying")

// Logout signs out the current device through the CLI, serialized with other
// device mutations. It does not remove credentials stored by the portal.
func (c *Client) Logout(ctx context.Context) error {
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
	// Invalidate prepared and running attempts before the write: even a lost
	// response must not leave an old attempt or sign-in link available to resume.
	c.keyRenewal = nil
	if _, err := c.run(ctx, "logout"); err != nil {
		return ErrLogoutApply
	}
	status, err := c.Status(ctx)
	if err != nil || status.HaveNodeKey || status.BackendState != "NeedsLogin" || status.AuthURL != "" {
		return ErrLogoutApply
	}
	return nil
}
