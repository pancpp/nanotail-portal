package graph

import (
	"context"
	"errors"

	"github.com/pancpp/nanotail-portal/app/graph/model"
	"github.com/pancpp/nanotail-portal/tailscale"
)

var ErrRoutingAdmin = errors.New("Only portal administrators can change routing settings")

func (r *queryResolver) tailscaleRouting(ctx context.Context) (*model.TailscaleRouting, error) {
	if r.Routing == nil {
		return nil, tailscale.ErrRoutingUnavailable
	}
	routing, err := r.Routing.Routing(ctx)
	if err != nil {
		return nil, tailscale.ErrRoutingUnavailable
	}
	result := &model.TailscaleRouting{
		BackendState: routing.BackendState, AdvertiseExitNode: routing.AdvertiseExitNode,
		SubnetRoutes: append([]string{}, routing.SubnetRoutes...), UsingExitNode: routing.UsingExitNode,
		SubnetDefaultsPending: routing.SubnetDefaultsPending,
		RouteApprovalState:    routing.Approval.State, RouteApprovalMessage: routing.Approval.Message,
		SnatEnabled: routing.SNATEnabled, Health: append([]string{}, routing.Health...),
		LanInterface: "eth0", DefaultSubnetRoutes: []string{},
		LanWarning: "Unable to detect the local LAN. Enter subnet routes manually.",
	}
	if r.RoutingHost != nil {
		if host, err := r.RoutingHost.RoutingStatus(ctx); err == nil {
			result.LanInterface, result.LanWarning = host.LANInterface, host.LANWarning
			result.DefaultSubnetRoutes = append([]string{}, host.DefaultSubnetRoutes...)
			result.Ipv4Forwarding, result.Ipv6Forwarding = host.IPv4Forwarding, host.IPv6Forwarding
		}
	}
	return result, nil
}

func (r *mutationResolver) setRouting(ctx context.Context, input model.RoutingInput) (bool, error) {
	if err := requireAdmin(ctx, ErrRoutingAdmin); err != nil {
		return false, err
	}
	if r.Routing == nil {
		return false, tailscale.ErrRoutingUnavailable
	}
	err := r.Routing.SetRouting(ctx, input.SubnetRoutes)
	return err == nil, err
}
