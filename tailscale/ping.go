package tailscale

import (
	"context"
	"net/netip"
	"strings"
	"time"
)

// Ping measures one WireGuard round trip only when explicitly requested. It
// shares a concurrency limit across requests and never schedules background work.
func (c *Client) Ping(ctx context.Context, address string) (time.Duration, error) {
	ip, err := netip.ParseAddr(address)
	if err != nil || ip.Zone() != "" {
		return 0, ErrInvalidConfig
	}
	requestCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	select {
	case c.pings <- struct{}{}:
		defer func() { <-c.pings }()
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	// Parent cancellation may still be propagating to queued child contexts.
	if err := requestCtx.Err(); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	// Allow a little command overhead beyond the CLI's two-second probe deadline.
	ctx, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	data, err := c.run(ctx, "ping", "--tsmp", "--c=1", "--timeout=2s", ip.String())
	if err != nil {
		return 0, err
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if !strings.HasPrefix(line, "pong from ") {
			continue
		}
		_, duration, ok := strings.Cut(line, " in ")
		if !ok {
			continue
		}
		latency, err := time.ParseDuration(strings.TrimSpace(duration))
		if err == nil && latency >= 0 {
			return latency, nil
		}
	}
	return 0, ErrInvalidOutput
}
