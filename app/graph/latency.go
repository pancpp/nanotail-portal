package graph

import (
	"context"
	"net/netip"
	"time"

	"github.com/pancpp/nanotail-portal/app/graph/model"
)

func (r *tailscalePeerResolver) peerLatency(ctx context.Context, peer *model.TailscalePeer) (*float64, error) {
	if r.PeerPinger == nil || !peer.Online {
		return nil, nil
	}
	// Targets come only from the daemon's peer list, never a caller-supplied host.
	for _, address := range peer.TailscaleIPs {
		if ip, err := netip.ParseAddr(address); err != nil || ip.Zone() != "" {
			continue
		}
		latency, err := r.PeerPinger.Ping(ctx, address)
		if err != nil {
			// One unreachable peer must not hide the rest of the table.
			return nil, nil
		}
		ms := float64(latency) / float64(time.Millisecond)
		return &ms, nil
	}
	return nil, nil
}
