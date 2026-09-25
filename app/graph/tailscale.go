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
	"github.com/pancpp/nanotail-portal/tailscale"
	"github.com/uptrace/bun"
)

var (
	ErrInvalidCredential = errors.New("Enter a client ID and secret; changing the client ID requires a new secret")
	ErrTailscaleAdmin    = errors.New("Only portal administrators can manage Tailscale credentials")
	ErrTailscaleStatus   = errors.New("Unable to read Tailscale status. Check that tailscaled is running and the portal can access it")
)

func requireTailscaleAdmin(ctx context.Context) error {
	return requireAdmin(ctx, ErrTailscaleAdmin)
}

func requireAdmin(ctx context.Context, forbidden error) error {
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
		return forbidden
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
	result := &model.TailscaleStatus{
		Version: status.Version, Tun: status.TUN, BackendState: status.BackendState,
		HaveNodeKey: status.HaveNodeKey, AuthURL: status.AuthURL,
		TailscaleIPs: status.IPs, Health: status.Health, MagicDNSSuffix: status.MagicDNSSuffix,
		CertDomains: status.CertDomains, Self: tailscalePeer(status.Self),
		Peers: make([]*model.TailscalePeer, len(status.Peers)),
	}
	if tailnet := status.CurrentTailnet; tailnet != nil {
		result.CurrentTailnet = &model.Tailnet{
			Name: tailnet.Name, MagicDNSSuffix: tailnet.MagicDNSSuffix, MagicDNSEnabled: tailnet.MagicDNSEnabled,
		}
	}
	if status.ExtraRecords != nil {
		result.ExtraRecords = make([]*model.TailscaleDNSRecord, len(status.ExtraRecords))
		for i, record := range status.ExtraRecords {
			result.ExtraRecords[i] = &model.TailscaleDNSRecord{Name: record.Name, Type: record.Type, Value: record.Value}
		}
	}
	if version := status.ClientVersion; version != nil {
		result.ClientVersion = &model.TailscaleClientVersion{
			RunningLatest: version.RunningLatest, LatestVersion: version.LatestVersion,
			UrgentSecurityUpdate: version.UrgentSecurityUpdate, Notify: version.Notify,
			NotifyURL: version.NotifyURL, NotifyText: version.NotifyText,
		}
	}
	for i := range status.Peers {
		result.Peers[i] = tailscalePeer(&status.Peers[i])
	}
	return result, nil
}

func tailscalePeer(peer *tailscale.Peer) *model.TailscalePeer {
	if peer == nil {
		return nil
	}
	return &model.TailscalePeer{
		ID: peer.ID, NodeID: int(peer.NodeID), PublicKey: peer.PublicKey,
		HostName: peer.Hostname, DNSName: peer.DNSName, Os: peer.OS, UserID: int(peer.UserID),
		TailscaleIPs: peer.IPs, AllowedIPs: peer.AllowedIPs, Tags: peer.Tags, Addrs: peer.Addrs,
		CurAddr: peer.CurAddr, Relay: peer.Relay, PeerRelay: peer.PeerRelay,
		RxBytes: int(peer.RxBytes), TxBytes: int(peer.TxBytes), Created: peer.Created,
		LastWrite: peer.LastWrite, LastSeen: peer.LastSeen, LastHandshake: peer.LastHandshake,
		Online: peer.Online, ExitNode: peer.ExitNode, ExitNodeOption: peer.ExitNodeOption, Active: peer.Active,
		PeerAPIURL: peer.PeerAPIURL, TaildropTarget: peer.TaildropTarget, NoFileSharingReason: peer.NoFileSharingReason,
		CapMap: peer.CapMap, InNetworkMap: peer.InNetworkMap, InMagicSock: peer.InMagicSock, InEngine: peer.InEngine,
		KeyExpiry: peer.KeyExpiry,
	}
}
