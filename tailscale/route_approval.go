package tailscale

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"sync"
	"time"
)

const (
	approvalDisabled = "DISABLED"
	approvalPending  = "PENDING"
	approvalApproved = "APPROVED"
	approvalError    = "ERROR"
)

var approvalDeviceID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

type RouteApproval struct{ State, Message string }

type routeApproval struct {
	load func(context.Context) (OAuthCredentials, error)
	api  *oauthRoutes
	// Only the published snapshot is shared with read-only queries.
	mu          sync.RWMutex
	fingerprint [32]byte
	result      RouteApproval
	nextCheck   time.Time
}

// WithRouteApproval is configured before sharing the client.
func (c *Client) WithRouteApproval(load func(context.Context) (OAuthCredentials, error), client *http.Client) *Client {
	c.approval = &routeApproval{load: load, api: newOAuthRoutes(client)}
	return c
}

func approvalTarget(credential OAuthCredentials, status Status, prefs Config) (string, []string, [32]byte, error) {
	if status.BackendState != "Running" || !status.HaveNodeKey || status.Self == nil || !approvalDeviceID.MatchString(status.Self.ID) {
		return "", nil, [32]byte{}, errApprovalPending
	}
	routes, err := ValidateSubnetRoutes(prefs.AdvertiseRoutes)
	if err != nil {
		return "", nil, [32]byte{}, errApprovalPending
	}
	if hasExitNodeRoutes(prefs) {
		routes = append(routes, "0.0.0.0/0", "::/0")
	}
	if len(routes) == 0 || prefs.ExitNode != "" || prefs.ExitNodeID != "" {
		return "", nil, [32]byte{}, errApprovalPending
	}
	slices.Sort(routes)
	data, _ := json.Marshal([]any{credential, status.Self.ID, routes})
	return status.Self.ID, routes, sha256.Sum256(data), nil
}

func disabledApproval() RouteApproval {
	return RouteApproval{approvalDisabled, "No OAuth credentials saved. Use Tailscale admin approval or tailnet auto-approvers."}
}

// routeApprovalStatus is local/read-only. Browsing settings never sends cloud writes.
func (c *Client) routeApprovalStatus(ctx context.Context, status Status, prefs Config) RouteApproval {
	a := c.approval
	if a == nil {
		return disabledApproval()
	}
	credential, err := a.load(ctx)
	if err != nil {
		return RouteApproval{approvalError, "Unable to read saved OAuth credentials. Check the portal database."}
	}
	if credential.ClientID == "" || credential.ClientSecret == "" {
		return disabledApproval()
	}
	_, _, fingerprint, err := approvalTarget(credential, status, prefs)
	if err != nil {
		return RouteApproval{approvalPending, "Waiting for Tailscale to be connected and advertise this device's routes."}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.fingerprint == fingerprint && a.result.State != "" {
		if a.result.State == approvalApproved && time.Now().After(a.nextCheck.Add(exitNodeCheckInterval)) {
			return RouteApproval{approvalPending, "Waiting to refresh the last confirmed Tailscale route approval."}
		}
		return a.result
	}
	return RouteApproval{approvalPending, "OAuth approval is queued for this device's exit node and advertised subnet routes."}
}

func (a *routeApproval) publish(fingerprint [32]byte, result RouteApproval, delay time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.fingerprint, a.result, a.nextCheck = fingerprint, result, time.Now().Add(delay)
}

// EnsureRouteApproval runs after default advertisements, independently of browsers.
// Changed credentials, device identity, or routes bypass the retry delay. Unchanged
// failures wait a minute; successful approvals are rechecked every five minutes.
func (c *Client) EnsureRouteApproval(ctx context.Context) error {
	a := c.approval
	if a == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
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
	credential, err := a.load(ctx)
	if err != nil {
		a.api.forget()
		return errors.New("Unable to read saved OAuth credentials for route approval")
	}
	if credential.ClientID == "" || credential.ClientSecret == "" {
		a.api.forget()
		a.publish([32]byte{}, disabledApproval(), 0)
		return nil
	}
	status, err := c.Status(ctx)
	if err != nil {
		return ErrRoutingUnavailable
	}
	prefs, err := c.Config(ctx)
	if err != nil {
		return ErrRoutingUnavailable
	}
	deviceID, routes, fingerprint, err := approvalTarget(credential, status, prefs)
	if err != nil {
		a.publish([32]byte{}, RouteApproval{approvalPending, "Waiting for Tailscale to be connected and advertise this device's routes."}, 0)
		return nil
	}
	a.mu.RLock()
	skip := fingerprint == a.fingerprint && time.Now().Before(a.nextCheck)
	a.mu.RUnlock()
	if skip {
		return nil
	}
	a.publish(fingerprint, RouteApproval{approvalPending, "Checking and approving this device's advertisements with Tailscale."}, 0)
	// The shared mutation lock serializes portal changes. Also re-read before the
	// cloud write to detect daemon/credential changes made outside this process.
	current := func() bool {
		latestCredential, err := a.load(ctx)
		if err != nil {
			return false
		}
		latestStatus, err := c.Status(ctx)
		if err != nil {
			return false
		}
		latestPrefs, err := c.Config(ctx)
		if err != nil {
			return false
		}
		_, _, latest, err := approvalTarget(latestCredential, latestStatus, latestPrefs)
		return err == nil && latest == fingerprint
	}
	err = a.api.approve(ctx, credential, deviceID, routes, current)
	result, delay := RouteApproval{approvalApproved, "Tailscale confirmed approval for this device's exit node and current subnet advertisements. Access rules and client settings still apply."}, 5*time.Minute
	if err != nil {
		result, delay = RouteApproval{approvalError, err.Error()}, time.Minute
		if errors.Is(err, errApprovalPending) {
			result.State = approvalPending
		}
	}
	a.publish(fingerprint, result, delay)
	return err
}

// UpdateOAuthCredentials serializes saving/removing credentials with cloud writes.
// Once removal succeeds no operation can use its cached token. No cloud revocation
// occurs, and successful route approvals are left intact.
func (c *Client) UpdateOAuthCredentials(ctx context.Context, update func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
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
	if err := update(ctx); err != nil {
		return err
	}
	if c.approval != nil {
		c.approval.api.forget()
		c.approval.publish([32]byte{}, RouteApproval{}, 0)
	}
	if c.relayPolicy != nil {
		c.relayPolicy.api.forget()
	}
	return nil
}
