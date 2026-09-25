package tailscale

import (
	"context"
	"crypto/rand"
	"errors"
	"net/url"
	"strings"
	"time"
)

var (
	ErrKeyRenewalUnavailable  = errors.New("Unable to read node key renewal status. Check that tailscaled is running and the portal can access it")
	ErrKeyRenewalUnconfigured = errors.New("Tailscale is not ready for sign-in. Check the device status and try again")
	ErrKeyRenewalDisabled     = errors.New("Node key renewal is disabled because key expiry is disabled for this device")
	ErrKeyRenewalStart        = errors.New("Unable to confirm that renewal started. It may already be in progress. Reconnect over the LAN and check renewal status before retrying")
	ErrKeyRenewalURL          = errors.New("Tailscale returned an unsupported sign-in URL. Complete reauthentication directly on the device")
	ErrKeyRenewalChanged      = errors.New("The renewal request has changed or expired. Check renewal status before retrying")
	ErrKeyRenewalStarted      = errors.New("Sign-in has already started and cannot be cancelled here. Check renewal status to continue")
)

type KeyRenewal struct {
	State     string
	AuthURL   string
	CanRenew  bool
	AttemptID string
}

type keyRenewalAttempt struct {
	previousKey  string
	initialLogin bool
	id           string
	cancelled    bool
	startedAt    time.Time
}

func nodeKeyExpiryDisabled(status Status) bool {
	return status.HaveNodeKey && status.Self != nil && status.Self.KeyExpiry == nil
}

func canStartLogin(status Status) bool {
	if nodeKeyExpiryDisabled(status) {
		return false
	}
	// A fresh or logged-out device has no node key yet. Interactive login is
	// still available, but must only begin after an explicit Sign in action.
	if status.BackendState == "NeedsLogin" {
		return true
	}
	if status.Self == nil || status.Self.PublicKey == "" {
		return false
	}
	switch status.BackendState {
	case "Running", "Stopped", "Starting":
		return true
	default:
		return false
	}
}

// The portal supports Tailscale-hosted interactive login. Never turn an
// arbitrary daemon-provided URL into a clickable link (or include it in errors).
func validRenewalURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Host == "login.tailscale.com" &&
		u.User == nil && u.Fragment == "" && strings.HasPrefix(u.Path, "/a/") && len(u.Path) > 3 &&
		!strings.ContainsAny(value, "\\ \t\r\n")
}

// renewalStatus requires the mutation lock. COMPLETE requires key rotation;
// SIGNED_IN confirms login without asserting rotation. Neither is inferred
// merely from a successful command.
func (c *Client) renewalStatus(status Status) (KeyRenewal, error) {
	attempt := c.keyRenewal
	canRenew := canStartLogin(status)
	if status.AuthURL != "" {
		if !validRenewalURL(status.AuthURL) {
			return KeyRenewal{}, ErrKeyRenewalURL
		}
		canRenew = !nodeKeyExpiryDisabled(status) // An existing login link cannot bypass disabled renewal.
	}
	if attempt == nil {
		if status.BackendState == "NeedsMachineAuth" {
			return KeyRenewal{State: "AWAITING_APPROVAL"}, nil
		}
		// Do not expose or start a sign-in flow until the user chooses Sign in.
		return KeyRenewal{State: "IDLE", CanRenew: canRenew}, nil
	}
	if attempt.cancelled {
		return KeyRenewal{State: "CANCELLED", CanRenew: canRenew, AttemptID: attempt.id}, nil
	}
	if attempt.startedAt.IsZero() {
		return KeyRenewal{State: "READY", AttemptID: attempt.id}, nil
	}
	if status.AuthURL != "" {
		return KeyRenewal{State: "AWAITING_LOGIN", AuthURL: status.AuthURL, AttemptID: attempt.id}, nil
	}
	if status.BackendState == "NeedsMachineAuth" {
		return KeyRenewal{State: "AWAITING_APPROVAL", AttemptID: attempt.id}, nil
	}
	if status.BackendState == "Running" && status.HaveNodeKey && status.Self != nil && status.Self.PublicKey != "" &&
		(status.Self.KeyExpiry == nil || status.Self.KeyExpiry.After(time.Now())) {
		if attempt.initialLogin || attempt.previousKey == "" {
			// First login or an externally-started flow: verify sign-in, but do
			// not claim that a previous key was rotated without its baseline.
			return KeyRenewal{State: "SIGNED_IN", CanRenew: canRenew, AttemptID: attempt.id}, nil
		}
		if status.Self.PublicKey != attempt.previousKey {
			return KeyRenewal{State: "COMPLETE", CanRenew: canRenew, AttemptID: attempt.id}, nil
		}
	}
	// A stalled/uncertain start can be retried explicitly after a fresh read.
	// Suppress concurrent requests while the control server prepares its URL.
	return KeyRenewal{State: "STARTING", CanRenew: canRenew && time.Since(attempt.startedAt) >= 30*time.Second, AttemptID: attempt.id}, nil
}

// KeyRenewal is read-only. It also recovers an existing login link after a lost
// HTTP response or a portal restart; without a baseline it never claims renewal.
func (c *Client) KeyRenewal(ctx context.Context) (KeyRenewal, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	select {
	case c.mutations <- struct{}{}:
		defer func() { <-c.mutations }()
	case <-ctx.Done():
		return KeyRenewal{}, ErrKeyRenewalUnavailable
	}
	if ctx.Err() != nil {
		return KeyRenewal{}, ErrKeyRenewalUnavailable
	}
	if attempt := c.keyRenewal; attempt != nil && attempt.startedAt.IsZero() && !attempt.cancelled {
		return KeyRenewal{State: "READY", AttemptID: attempt.id}, nil
	}
	status, err := c.Status(ctx)
	if err != nil {
		if attempt := c.keyRenewal; attempt != nil && attempt.cancelled {
			return KeyRenewal{State: "CANCELLED", AttemptID: attempt.id}, nil
		}
		return KeyRenewal{}, ErrKeyRenewalUnavailable
	}
	return c.renewalStatus(status)
}

// RenewNodeKey prepares first-time sign-in or renewal only. Closing it can cancel without
// changing tailscaled, because StartLoginInteractive is deferred until Sign in.
func (c *Client) RenewNodeKey(ctx context.Context) (KeyRenewal, error) {
	return c.updateKeyRenewal(ctx, "prepare", "")
}

func (c *Client) BeginNodeKeyRenewal(ctx context.Context, attemptID string) (KeyRenewal, error) {
	return c.updateKeyRenewal(ctx, "begin", attemptID)
}

func (c *Client) CancelNodeKeyRenewal(ctx context.Context, attemptID string) (KeyRenewal, error) {
	return c.updateKeyRenewal(ctx, "cancel", attemptID)
}

func (c *Client) updateKeyRenewal(ctx context.Context, action, attemptID string) (KeyRenewal, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	select {
	case c.mutations <- struct{}{}:
		defer func() { <-c.mutations }()
	case <-ctx.Done():
		return KeyRenewal{}, ErrKeyRenewalUnavailable
	}
	if ctx.Err() != nil {
		return KeyRenewal{}, ErrKeyRenewalUnavailable
	}
	attempt := c.keyRenewal
	if action != "prepare" {
		if attemptID == "" || attempt == nil || attempt.id != attemptID {
			return KeyRenewal{}, ErrKeyRenewalChanged
		}
		if action == "cancel" {
			if !attempt.startedAt.IsZero() {
				return KeyRenewal{}, ErrKeyRenewalStarted
			}
			// Cancellation is idempotent and works even when tailscaled is down.
			attempt.cancelled = true
			return KeyRenewal{State: "CANCELLED", AttemptID: attempt.id}, nil
		}
		if attempt.cancelled {
			return KeyRenewal{}, ErrKeyRenewalChanged
		}
	}
	status, err := c.Status(ctx)
	if err != nil {
		return KeyRenewal{}, ErrKeyRenewalUnavailable
	}
	value, err := c.renewalStatus(status)
	if err != nil {
		return KeyRenewal{}, err
	}
	if action == "prepare" {
		if nodeKeyExpiryDisabled(status) {
			return KeyRenewal{}, ErrKeyRenewalDisabled
		}
		if value.State == "READY" || value.State == "STARTING" || value.State == "AWAITING_LOGIN" || value.State == "AWAITING_APPROVAL" {
			return value, nil
		}
		if !value.CanRenew {
			return KeyRenewal{}, ErrKeyRenewalUnconfigured
		}
		attempt = &keyRenewalAttempt{id: rand.Text(), initialLogin: !status.HaveNodeKey}
		if status.Self != nil {
			attempt.previousKey = status.Self.PublicKey
		}
		c.keyRenewal = attempt
		return KeyRenewal{State: "READY", AttemptID: attempt.id}, nil
	}
	if value.State != "READY" && !(value.State == "STARTING" && value.CanRenew) {
		return value, nil
	}
	currentKey := ""
	if status.Self != nil {
		currentKey = status.Self.PublicKey
	}
	if attempt.startedAt.IsZero() && currentKey != attempt.previousKey {
		return KeyRenewal{}, ErrKeyRenewalChanged
	}
	// Recheck fresh status: expiry may have been disabled after preparation.
	if nodeKeyExpiryDisabled(status) {
		return KeyRenewal{}, ErrKeyRenewalDisabled
	}
	if !canStartLogin(status) && status.AuthURL == "" {
		return KeyRenewal{}, ErrKeyRenewalUnconfigured
	}
	if ctx.Err() != nil {
		return KeyRenewal{}, ErrKeyRenewalUnavailable
	}
	// Commit before the write: an interrupted response must not permit a later
	// Cancel to claim it stopped a daemon login that may already be running.
	attempt.startedAt = time.Now()
	if status.AuthURL != "" {
		return c.renewalStatus(status)
	}
	// This is the StartLoginInteractive operation used by up --force-reauth.
	// Unlike up, it returns immediately and never replaces saved preferences.
	// No auth key, OAuth credential, logout, --reset, or shell interpolation.
	if _, err := c.run(ctx, "debug", "localapi", "POST", "/localapi/v0/login-interactive"); err != nil {
		return KeyRenewal{}, ErrKeyRenewalStart
	}
	return KeyRenewal{State: "STARTING", AttemptID: attempt.id}, nil
}
