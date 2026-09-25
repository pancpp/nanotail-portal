package app

import (
	"context"
	"database/sql"
	"errors"

	"github.com/pancpp/nanotail-portal/database"
	"github.com/pancpp/nanotail-portal/tailscale"
)

func loadRoutingCredentials(ctx context.Context) (tailscale.OAuthCredentials, error) {
	saved := &database.TailscaleClient{PID: 1}
	err := database.DB().NewSelect().Model(saved).Column("client_id", "client_secret").WherePK().Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return tailscale.OAuthCredentials{}, nil
	}
	if err != nil {
		return tailscale.OAuthCredentials{}, err
	}
	return tailscale.OAuthCredentials{ClientID: saved.ClientID, ClientSecret: saved.ClientSecret}, nil
}
