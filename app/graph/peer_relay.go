package graph

import (
	"context"
	"errors"

	"github.com/pancpp/nanotail-portal/app/graph/model"
	"github.com/pancpp/nanotail-portal/tailscale"
)

var ErrPeerRelayAdmin = errors.New("Only portal administrators can change peer relay settings")

func (r *mutationResolver) setPeerRelay(ctx context.Context, input model.PeerRelayInput) (bool, error) {
	if err := requireAdmin(ctx, ErrPeerRelayAdmin); err != nil {
		return false, err
	}
	if r.PeerRelay == nil {
		return false, tailscale.ErrPeerRelayUnavailable
	}
	err := r.PeerRelay.SetPeerRelay(ctx, input.Enabled, int(input.Port))
	return err == nil, err
}
