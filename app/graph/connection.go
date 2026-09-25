package graph

import (
	"context"
	"errors"

	"github.com/pancpp/nanotail-portal/app/graph/model"
	"github.com/pancpp/nanotail-portal/tailscale"
)

var ErrConnectionAdmin = errors.New("Only portal administrators can change the tailnet connection")

func (r *queryResolver) tailscaleConnection(ctx context.Context) (*model.TailscaleConnection, error) {
	if r.Connection == nil {
		return nil, tailscale.ErrConnectionUnavailable
	}
	value, err := r.Connection.Connection(ctx)
	if err != nil {
		return nil, tailscale.ErrConnectionUnavailable
	}
	return &model.TailscaleConnection{Enabled: value.Enabled, BackendState: value.BackendState, CanEnable: value.CanEnable}, nil
}

func (r *mutationResolver) setTailscaleEnabled(ctx context.Context, enabled bool) (bool, error) {
	if err := requireAdmin(ctx, ErrConnectionAdmin); err != nil {
		return false, err
	}
	if r.Connection == nil {
		return false, tailscale.ErrConnectionUnavailable
	}
	err := r.Connection.SetEnabled(ctx, enabled)
	return err == nil, err
}
