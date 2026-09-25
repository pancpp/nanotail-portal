package graph

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/pancpp/nanotail-portal/device"
)

type trafficReaderFunc func(context.Context) (device.TrafficSample, error)

func (f trafficReaderFunc) Sample(ctx context.Context) (device.TrafficSample, error) { return f(ctx) }

func TestNetworkActivityGraphQL(t *testing.T) {
	for _, mode := range []string{"success", "failed", "missing"} {
		t.Run(mode, func(t *testing.T) {
			r := &Resolver{}
			if mode != "missing" {
				r.Traffic = trafficReaderFunc(func(ctx context.Context) (device.TrafficSample, error) {
					if ctx.Value("traffic-test") != "request" {
						t.Fatal("request context not propagated")
					}
					if mode == "failed" {
						return device.TrafficSample{}, errors.New("private OS error")
					}
					return device.TrafficSample{InterfaceName: "tailscale0", RxBytes: math.MaxUint64, TxBytes: 0, SampledAt: time.Unix(1700000000, 0).UTC(), CounterEpoch: "boot:3"}, nil
				})
			}
			srv := handler.New(NewExecutableSchema(Config{Resolvers: r}))
			srv.AddTransport(transport.POST{})
			ctx := context.WithValue(t.Context(), "traffic-test", "request")
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/query", strings.NewReader(`{"query":"query { networkActivity { interfaceName rxBytes txBytes sampledAt counterEpoch } }"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			var payload struct {
				Data   map[string]map[string]string
				Errors []struct{ Message string }
			}
			if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusOK {
				t.Fatalf("unexpected status: %d %s", w.Code, w.Body.String())
			}
			if mode != "success" {
				if payload.Data != nil || len(payload.Errors) != 1 || payload.Errors[0].Message != ErrNetworkActivity.Error() || strings.Contains(w.Body.String(), "private OS") {
					t.Fatalf("unsafe error: %s", w.Body.String())
				}
				return
			}
			if len(payload.Errors) != 0 {
				t.Fatalf("unexpected errors: %s", w.Body.String())
			}
			for key, want := range map[string]string{"interfaceName": "tailscale0", "rxBytes": "18446744073709551615", "txBytes": "0", "sampledAt": "2023-11-14T22:13:20Z", "counterEpoch": "boot:3"} {
				if got := payload.Data["networkActivity"][key]; got != want {
					t.Errorf("%s=%s, want %s", key, got, want)
				}
			}
		})
	}
}
