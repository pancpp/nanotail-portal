package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/pancpp/nanotail-portal/app/auth"
	"github.com/pancpp/nanotail-portal/app/graph"
	"github.com/pancpp/nanotail-portal/database"
	"github.com/pancpp/nanotail-portal/tailscale"
)

type peerRelayMock struct {
	calls, port int
	enabled     bool
	err         error
}

func (m *peerRelayMock) SetPeerRelay(_ context.Context, enabled bool, port int) error {
	m.calls++
	m.enabled = enabled
	m.port = port
	return m.err
}

func TestPeerRelayMutation(t *testing.T) {
	db := setupAuthDatabase(t, t.Context())
	admin, regular := &database.User{Username: "admin", Role: "admin"}, &database.User{Username: "user", Role: "user"}
	for _, user := range []*database.User{admin, regular} {
		if _, err := db.NewInsert().Model(user).Exec(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name             string
		pid              int64
		input            string
		port             int
		enabled, missing bool
		err              error
		want             string
		calls            int
	}{
		{name: "default", pid: admin.PID, input: "enabled:true", port: 40001, enabled: true, calls: 1},
		{name: "custom", pid: admin.PID, input: "enabled:true,port:45678", port: 45678, enabled: true, calls: 1},
		{name: "disable", pid: admin.PID, input: "enabled:false", port: 40001, calls: 1},
		{name: "regular", pid: regular.PID, want: graph.ErrPeerRelayAdmin.Error()},
		{name: "no identity", want: auth.ErrUnauthorized.Error()},
		{name: "deleted", pid: 99999, want: auth.ErrUnauthorized.Error()},
		{name: "no backend", pid: admin.PID, missing: true, want: tailscale.ErrPeerRelayUnavailable.Error()},
		{name: "invalid port", pid: admin.PID, err: tailscale.ErrPeerRelayPort, want: tailscale.ErrPeerRelayPort.Error(), calls: 1},
		{name: "unknown outcome", pid: admin.PID, err: tailscale.ErrPeerRelayApply, want: tailscale.ErrPeerRelayApply.Error(), calls: 1},
		{name: "masked", pid: admin.PID, err: errors.New("private daemon output"), want: "Internal Server Error", calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := &peerRelayMock{err: tc.err}
			resolver := &graph.Resolver{PeerRelay: mock}
			if tc.missing {
				resolver.PeerRelay = nil
			}
			srv := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: resolver}))
			srv.AddTransport(transport.POST{})
			srv.SetErrorPresenter(presentGraphQLError)
			input := tc.input
			if input == "" {
				input = "enabled:true"
			}
			body, _ := json.Marshal(map[string]string{"query": "mutation { setPeerRelay(input:{" + input + "}) }"})
			ctx := context.WithValue(t.Context(), graph.QUERY_CONTEXT_KEY, &graph.ContextValue{UserPID: tc.pid})
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/query", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			var result struct {
				Data   json.RawMessage
				Errors []struct{ Message string }
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if mock.calls != tc.calls {
				t.Fatalf("calls=%d want %d", mock.calls, tc.calls)
			}
			if tc.want != "" {
				if len(result.Errors) != 1 || result.Errors[0].Message != tc.want {
					t.Fatalf("%s", w.Body.String())
				}
			} else if len(result.Errors) != 0 || string(result.Data) != `{"setPeerRelay":true}` || mock.enabled != tc.enabled || mock.port != tc.port {
				t.Fatalf("%s %+v", w.Body.String(), mock)
			}
		})
	}
}

func TestPeerRelayRoutingMapping(t *testing.T) {
	for _, port := range []*uint16{nil, new(uint16(0)), new(uint16(40001))} {
		mock := &routingMock{value: tailscale.Routing{BackendState: "Running", PeerRelayPort: port}}
		result := routingGraphQL(t, mock, 1, `{tailscaleRouting{peerRelayEnabled peerRelayPort}}`, nil)
		var data struct {
			TailscaleRouting struct {
				PeerRelayEnabled bool
				PeerRelayPort    *uint16
			}
		}
		if err := json.Unmarshal(result["data"], &data); err != nil {
			t.Fatal(err)
		}
		got := data.TailscaleRouting
		if got.PeerRelayEnabled != (port != nil) || (got.PeerRelayPort == nil) != (port == nil) || (port != nil && *got.PeerRelayPort != *port) {
			t.Fatalf("mapping: %s", result["data"])
		}
	}
}

func TestPeerRelayHTTPAuthentication(t *testing.T) {
	e := newTestApp(t)
	for _, token := range []string{"", "Bearer invalid"} {
		w := appRequest(e, http.MethodPost, "/api/v1/query", `{"query":"mutation {setPeerRelay(input:{enabled:true})}"}`, token)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
		}
	}
}
