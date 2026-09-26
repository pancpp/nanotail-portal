package tailscale

import (
	"context"
	"errors"
	"net/netip"
	"slices"
)

var (
	ErrRoutingUnavailable  = errors.New("Unable to read routing settings. Check that tailscaled is running and the portal can access it")
	ErrSubnetRoutesInvalid = errors.New("Enter up to 64 unique, canonical IPv4 or IPv6 subnet CIDRs, such as 192.168.1.0/24. Default, loopback, link-local, multicast, and Tailscale address ranges are not subnet routes")
	ErrRoutingStopped      = errors.New("Connect this device to Tailscale before advertising an exit node or subnet routes")
	ErrRoutingApply        = errors.New("Unable to confirm the routing change. It may already have applied. Check connectivity, daemon permissions, and current routing settings before retrying")
)

// Routing describes this device's advertisements, not approval or reachability.
type Routing struct {
	BackendState          string
	AdvertiseExitNode     bool
	SubnetRoutes          []string
	SubnetDefaultsPending bool
	UsingExitNode         bool
	SNATEnabled           bool
	Health                []string
	Approval              RouteApproval
	PeerRelayPort         *uint16
}

func (c *Client) Routing(ctx context.Context) (Routing, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	prefs, err := c.Config(ctx)
	if err != nil {
		return Routing{}, ErrRoutingUnavailable
	}
	status, err := c.Status(ctx)
	if err != nil {
		return Routing{}, ErrRoutingUnavailable
	}
	pending, err := c.subnetDefaultsPending(ctx)
	if err != nil {
		return Routing{}, err
	}
	return Routing{
		BackendState: status.BackendState, AdvertiseExitNode: hasExitNodeRoutes(prefs),
		SubnetRoutes: nonNil(prefs.AdvertiseRoutes), UsingExitNode: prefs.ExitNodeID != "" || prefs.ExitNode != "",
		SNATEnabled: prefs.SNATEnabled, Health: nonNil(status.Health),
		SubnetDefaultsPending: pending && len(prefs.AdvertiseRoutes) == 0,
		Approval:              c.routeApprovalStatus(ctx, status, prefs),
		PeerRelayPort:         prefs.RelayServerPort,
	}, nil
}

// ValidateSubnetRoutes normalizes spelling/order but refuses host bits, default
// routes, duplicates, and special ranges. Routes are never passed through a shell.
func ValidateSubnetRoutes(routes []string) ([]string, error) {
	if len(routes) > 64 {
		return nil, ErrSubnetRoutesInvalid
	}
	result := make([]string, 0, len(routes))
	seen := make(map[netip.Prefix]bool)
	for _, value := range routes {
		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix.Bits() == 0 || prefix != prefix.Masked() ||
			prefix.Addr().Is4In6() || !prefix.Addr().IsGlobalUnicast() || seen[prefix] {
			return nil, ErrSubnetRoutesInvalid
		}
		for _, reserved := range []string{"0.0.0.0/8", "240.0.0.0/4", "100.64.0.0/10", "fd7a:115c:a1e0::/48", "127.0.0.0/8", "169.254.0.0/16", "224.0.0.0/4", "fe80::/10", "ff00::/8", "::ffff:0:0/96"} {
			if prefix.Overlaps(netip.MustParsePrefix(reserved)) {
				return nil, ErrSubnetRoutesInvalid
			}
		}
		seen[prefix] = true
		result = append(result, prefix.String())
	}
	slices.Sort(result)
	return result, nil
}

// SetRouting replaces subnet advertisements, always advertises an exit node, and clears
// legacy exit-node use in the same command. It does not start Tailscale, change
// DNS/SNAT/firewall preferences, or configure the host's IP forwarding.
func (c *Client) SetRouting(ctx context.Context, routes []string) error {
	routes, err := ValidateSubnetRoutes(routes)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	select {
	case c.mutations <- struct{}{}:
		defer func() { <-c.mutations }()
	case <-ctx.Done():
		return ErrRoutingUnavailable
	}
	// Subnet withdrawal remains possible while stopped. The mandatory exit-node
	// preference stays enabled; this does not start or reconnect Tailscale.
	if len(routes) > 0 {
		status, err := c.Status(ctx)
		if err != nil {
			return ErrRoutingUnavailable
		}
		if status.BackendState != "Running" {
			return ErrRoutingStopped
		}
	}
	if err := c.claimSubnetChoice(ctx); err != nil {
		return err
	}
	return c.applyRouting(ctx, routes)
}

// applyRouting requires the mutation lock and already-validated subnet routes.
func (c *Client) applyRouting(ctx context.Context, routes []string) error {
	address, allowLAN, advertiseExitNode := "", false, true
	args, err := (ConfigUpdate{
		ExitNode: &address, ExitNodeAllowLANAccess: &allowLAN,
		AdvertiseExitNode: &advertiseExitNode, AdvertiseRoutes: &routes,
	}).args()
	if err != nil {
		return ErrSubnetRoutesInvalid
	}
	if _, err := c.run(ctx, args...); err != nil {
		return ErrRoutingApply
	}
	prefs, err := c.Config(ctx)
	if err != nil {
		return ErrRoutingApply
	}
	actual, err := ValidateSubnetRoutes(prefs.AdvertiseRoutes)
	if err != nil || !slices.Equal(actual, routes) || !exitNodeConfigured(prefs) {
		return ErrRoutingApply
	}
	return nil
}
