package tailscale

import (
	"context"
	"errors"
	"net/netip"
	"strings"
)

var (
	ErrRoutingUnavailable = errors.New("Unable to read routing settings. Check that tailscaled is running and the portal can access it")
	ErrExitNodeInvalid    = errors.New("Select an online, approved exit node from this tailnet, or choose the local gateway")
	ErrRoutingStopped     = errors.New("Connect this device to Tailscale before selecting an exit node")
	ErrRoutingAdvertised  = errors.New("This device advertises itself as an exit node and cannot use another exit node at the same time")
	ErrRoutingApply       = errors.New("Unable to confirm the routing change. It may already have applied. Check connectivity, daemon permissions, and current routing settings before retrying")
)

// Routing exposes only the preferences needed by the exit-node UI, never keys.
type Routing struct {
	BackendState      string
	ExitNodeID        string
	ExitNodeIP        string
	AllowLANAccess    bool
	AdvertiseExitNode bool
	ExitNodes         []Peer
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
	result := Routing{
		BackendState: status.BackendState, ExitNodeID: prefs.ExitNodeID, ExitNodeIP: prefs.ExitNode,
		AllowLANAccess: prefs.ExitNodeAllowLANAccess, AdvertiseExitNode: prefs.AdvertiseExitNode,
		ExitNodes: []Peer{},
	}
	for _, peer := range status.Peers {
		if peer.ExitNodeOption && peer.ID != "" {
			result.ExitNodes = append(result.ExitNodes, peer)
		}
		// Older daemons may store an address instead of the stable node ID.
		if result.ExitNodeID == "" && prefs.ExitNode != "" && peerHasIP(peer, prefs.ExitNode) {
			result.ExitNodeID = peer.ID
		}
	}
	return result, nil
}

// SetExitNode serializes validation, the partial update, and readback with other
// portal mutations. It never starts Tailscale or changes unrelated preferences.
func (c *Client) SetExitNode(ctx context.Context, id string, allowLAN bool) error {
	if len(id) > 256 || strings.TrimSpace(id) != id || (id == "" && allowLAN) {
		return ErrExitNodeInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	select {
	case c.mutations <- struct{}{}:
		defer func() { <-c.mutations }()
	case <-ctx.Done():
		return ErrRoutingUnavailable
	}
	address := ""
	var selected Peer
	if id != "" {
		routing, err := c.Routing(ctx)
		if err != nil {
			return err
		}
		if routing.BackendState != "Running" {
			return ErrRoutingStopped
		}
		if routing.AdvertiseExitNode {
			return ErrRoutingAdvertised
		}
		for _, peer := range routing.ExitNodes {
			if peer.ID != id || !peer.Online {
				continue
			}
			selected = peer
			for _, candidate := range peer.IPs {
				ip, err := netip.ParseAddr(candidate)
				if err == nil && ip.IsGlobalUnicast() && ip.Zone() == "" {
					address = ip.String()
					break
				}
			}
			break
		}
		if address == "" {
			return ErrExitNodeInvalid
		}
	}
	args, err := (ConfigUpdate{ExitNode: &address, ExitNodeAllowLANAccess: &allowLAN}).args()
	if err != nil {
		return ErrExitNodeInvalid
	}
	// We already hold the mutation lock; do not call UpdateConfig here.
	if _, err := c.run(ctx, args...); err != nil {
		return ErrRoutingApply
	}
	prefs, err := c.Config(ctx)
	if err != nil || prefs.ExitNodeAllowLANAccess != allowLAN {
		return ErrRoutingApply
	}
	if id == "" {
		if prefs.ExitNodeID != "" || prefs.ExitNode != "" {
			return ErrRoutingApply
		}
	} else if prefs.ExitNodeID != id && !(prefs.ExitNodeID == "" && peerHasIP(selected, prefs.ExitNode)) {
		return ErrRoutingApply
	}
	return nil
}

func peerHasIP(peer Peer, address string) bool {
	ip, err := netip.ParseAddr(address)
	if err != nil {
		return false
	}
	for _, candidate := range peer.IPs {
		other, err := netip.ParseAddr(candidate)
		if err == nil && ip == other {
			return true
		}
	}
	return false
}
