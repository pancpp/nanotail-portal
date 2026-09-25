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
		BackendState: routing.BackendState, ExitNodeID: routing.ExitNodeID, ExitNodeIP: routing.ExitNodeIP,
		AllowLANAccess: routing.AllowLANAccess, AdvertiseExitNode: routing.AdvertiseExitNode,
		ExitNodes: make([]*model.TailscalePeer, len(routing.ExitNodes)),
	}
	for i := range routing.ExitNodes {
		result.ExitNodes[i] = tailscalePeer(&routing.ExitNodes[i])
	}
	return result, nil
}

func (r *mutationResolver) setExitNode(ctx context.Context, input model.ExitNodeInput) (bool, error) {
	if err := requireAdmin(ctx, ErrRoutingAdmin); err != nil {
		return false, err
	}
	if r.Routing == nil {
		return false, tailscale.ErrRoutingUnavailable
	}
	err := r.Routing.SetExitNode(ctx, input.ExitNodeID, input.AllowLANAccess)
	return err == nil, err
}
