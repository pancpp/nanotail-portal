package graph

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/pancpp/nanotail-portal/tailscale"
)

type latencyRunner struct{ probes atomic.Int32 }

func (r *latencyRunner) Run(ctx context.Context, _ string, args ...string) ([]byte, error) {
	if args[0] == "status" {
		return []byte(`{"BackendState":"Running","Peer":{
			"a":{"ID":"a","Online":true,"TailscaleIPs":["100.64.0.1"]},
			"b":{"ID":"b","Online":false,"TailscaleIPs":["100.64.0.2"]},
			"c":{"ID":"c","Online":true,"TailscaleIPs":["100.64.0.3"]},
			"d":{"ID":"d","Online":true,"TailscaleIPs":[]},
			"e":{"ID":"e","Online":true,"TailscaleIPs":["--help"]}
		}}`), nil
	}
	r.probes.Add(1)
	if ctx.Value(latencyContextKey{}) != "request" {
		return nil, errors.New("missing request context")
	}
	switch args[len(args)-1] {
	case "100.64.0.1":
		return []byte("pong from laptop (100.64.0.1) via TSMP in 12ms\n"), nil
	case "100.64.0.3":
		return nil, errors.New("private daemon error")
	default:
		panic("probed an offline or invalid peer")
	}
}

type latencyContextKey struct{}

func TestPeerLatencyIsOnlyMeasuredWhenSelected(t *testing.T) {
	runner := &latencyRunner{}
	client := tailscale.NewClient("tailscale", "", time.Second, runner)
	server := handler.New(NewExecutableSchema(Config{Resolvers: &Resolver{Tailscale: client, PeerPinger: client}}))
	server.AddTransport(transport.POST{})
	query := func(fields string) []map[string]any {
		t.Helper()
		req := httptest.NewRequestWithContext(context.WithValue(t.Context(), latencyContextKey{}, "request"), http.MethodPost, "/query",
			strings.NewReader(`{"query":"{ tailscaleStatus { peers { `+fields+` } } }"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)
		var payload struct {
			Data struct {
				TailscaleStatus struct{ Peers []map[string]any }
			}
			Errors []any
		}
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil || w.Code != http.StatusOK || len(payload.Errors) != 0 {
			t.Fatalf("query failed: %s (%v)", w.Body.String(), err)
		}
		return payload.Data.TailscaleStatus.Peers
	}
	query("id online")
	query("id latencyMs @skip(if: true)")
	if runner.probes.Load() != 0 {
		t.Fatal("ordinary status requests must not probe peers")
	}
	for iteration := int32(1); iteration <= 2; iteration++ {
		peers := query("id latencyMs")
		if len(peers) != 5 || peers[0]["latencyMs"] != float64(12) {
			t.Fatalf("latency response: %+v", peers)
		}
		for _, peer := range peers[1:] {
			if latency, exists := peer["latencyMs"]; !exists || latency != nil {
				t.Fatalf("unavailable latency must be null: %+v", peer)
			}
		}
		if runner.probes.Load() != iteration*2 {
			t.Fatalf("expected one fresh probe per online peer, got %d", runner.probes.Load())
		}
	}
}
