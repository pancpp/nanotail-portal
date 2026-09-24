package graph

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/pancpp/nanotail-portal/app/auth"
	"github.com/pancpp/nanotail-portal/app/graph/model"
	"github.com/pancpp/nanotail-portal/database"
	"github.com/uptrace/bun"
)

var (
	ErrInvalidCredential = errors.New("Enter a client ID and secret; changing the client ID requires a new secret")
	ErrTailscaleAdmin    = errors.New("Only portal administrators can manage Tailscale credentials")
	ErrTailscaleStatus   = errors.New("Unable to read Tailscale status. Check that tailscaled is running and the portal can access it")
)

func requireTailscaleAdmin(ctx context.Context) error {
	value := queryContextValue(ctx)
	if value == nil || value.UserPID <= 0 {
		return auth.ErrUnauthorized
	}
	user := &database.User{PID: value.UserPID}
	if err := database.DB().NewSelect().Model(user).WherePK().Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return auth.ErrUnauthorized
		}
		return err
	}
	if user.Role != "admin" {
		return ErrTailscaleAdmin
	}
	return nil
}

func (r *mutationResolver) setTailscaleCredential(ctx context.Context, credential model.TailscaleCredential) (bool, error) {
	if err := requireTailscaleAdmin(ctx); err != nil {
		return false, err
	}
	id := strings.TrimSpace(credential.ClientID)
	secret := ""
	if credential.ClientSecret != nil {
		secret = strings.TrimSpace(*credential.ClientSecret)
	}
	if id == "" || len(id) > 512 || len(secret) > 4096 ||
		strings.ContainsFunc(id+secret, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return false, ErrInvalidCredential
	}
	err := database.DB().RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// A portal manages one device-wide OAuth client. Never return its secret.
		if secret == "" {
			saved := &database.TailscaleClient{PID: 1}
			if err := tx.NewSelect().Model(saved).WherePK().Scan(ctx); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrInvalidCredential
				}
				return err
			}
			if saved.ClientID != id || saved.ClientSecret == "" {
				return ErrInvalidCredential
			}
			secret = saved.ClientSecret
		}
		client := &database.TailscaleClient{PID: 1, ClientID: id, ClientSecret: secret, UpdateTime: time.Now().UTC()}
		_, err := tx.NewInsert().Model(client).On("CONFLICT (pid) DO UPDATE").
			Set("client_id = EXCLUDED.client_id").Set("client_secret = EXCLUDED.client_secret").
			Set("update_time = EXCLUDED.update_time").
			Set("api_token_id = ''").Set("api_token = ''").Set("auth_key_id = ''").Set("auth_key = ''").Exec(ctx)
		return err
	})
	return err == nil, err
}

func (r *queryResolver) tailscaleClient(ctx context.Context) (*model.TailscaleClient, error) {
	if err := requireTailscaleAdmin(ctx); err != nil {
		return nil, err
	}
	client := &database.TailscaleClient{PID: 1}
	if err := database.DB().NewSelect().Model(client).WherePK().Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &model.TailscaleClient{
		ID: int(client.PID), ClientID: client.ClientID, HasClientSecret: client.ClientSecret != "",
		CreateTime: client.CreateTime, UpdateTime: client.UpdateTime,
	}, nil
}

func (r *mutationResolver) clearTailscaleCredential(ctx context.Context) (bool, error) {
	if err := requireTailscaleAdmin(ctx); err != nil {
		return false, err
	}
	_, err := database.DB().NewDelete().Model((*database.TailscaleClient)(nil)).Where("pid = ?", 1).Exec(ctx)
	return err == nil, err
}

func (r *queryResolver) tailscaleStatus(ctx context.Context) (*model.TailscaleStatus, error) {
	if r.Tailscale == nil {
		return nil, ErrTailscaleStatus
	}
	status, err := r.Tailscale.Status(ctx)
	if err != nil {
		return nil, ErrTailscaleStatus
	}
	return &model.TailscaleStatus{
		BackendState: status.BackendState,
		Connected:    status.BackendState == "Running" && status.Self != nil && status.Self.Online,
		NeedsLogin:   status.BackendState == "NeedsLogin",
		Tailnet:      status.Tailnet, Ips: status.IPs,
	}, nil
}
