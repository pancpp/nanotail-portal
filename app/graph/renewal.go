package graph

import (
	"context"
	"errors"

	"github.com/pancpp/nanotail-portal/app/graph/model"
	"github.com/pancpp/nanotail-portal/tailscale"
)

var ErrKeyRenewalAdmin = errors.New("Only portal administrators can sign in to Tailscale, renew the node key, or view its sign-in link")

func keyRenewalModel(value tailscale.KeyRenewal, err error) (*model.TailscaleKeyRenewal, error) {
	if err != nil {
		return nil, err
	}
	return &model.TailscaleKeyRenewal{State: model.KeyRenewalState(value.State), AuthURL: value.AuthURL, CanRenew: value.CanRenew, AttemptID: value.AttemptID}, nil
}

func (r *queryResolver) tailscaleKeyRenewal(ctx context.Context) (*model.TailscaleKeyRenewal, error) {
	if err := requireAdmin(ctx, ErrKeyRenewalAdmin); err != nil {
		return nil, err
	}
	if r.KeyRenewer == nil {
		return nil, tailscale.ErrKeyRenewalUnavailable
	}
	return keyRenewalModel(r.KeyRenewer.KeyRenewal(ctx))
}

func (r *mutationResolver) renewTailscaleNodeKey(ctx context.Context) (*model.TailscaleKeyRenewal, error) {
	if err := requireAdmin(ctx, ErrKeyRenewalAdmin); err != nil {
		return nil, err
	}
	if r.KeyRenewer == nil {
		return nil, tailscale.ErrKeyRenewalUnavailable
	}
	return keyRenewalModel(r.KeyRenewer.RenewNodeKey(ctx))
}

func (r *mutationResolver) beginTailscaleNodeKeyRenewal(ctx context.Context, attemptID string) (*model.TailscaleKeyRenewal, error) {
	if err := requireAdmin(ctx, ErrKeyRenewalAdmin); err != nil {
		return nil, err
	}
	if r.KeyRenewer == nil {
		return nil, tailscale.ErrKeyRenewalUnavailable
	}
	return keyRenewalModel(r.KeyRenewer.BeginNodeKeyRenewal(ctx, attemptID))
}

func (r *mutationResolver) cancelTailscaleNodeKeyRenewal(ctx context.Context, attemptID string) (*model.TailscaleKeyRenewal, error) {
	if err := requireAdmin(ctx, ErrKeyRenewalAdmin); err != nil {
		return nil, err
	}
	if r.KeyRenewer == nil {
		return nil, tailscale.ErrKeyRenewalUnavailable
	}
	return keyRenewalModel(r.KeyRenewer.CancelNodeKeyRenewal(ctx, attemptID))
}
