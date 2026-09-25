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

type routingMock struct {
	value tailscale.Routing
	err   error
	calls int
	id    string
	allow bool
}

func (m *routingMock) Routing(context.Context) (tailscale.Routing, error) { return m.value, m.err }
func (m *routingMock) SetExitNode(_ context.Context, id string, allow bool) error {
	m.calls++
	m.id = id
	m.allow = allow
	return m.err
}

func routingGraphQL(t *testing.T, router graph.TailscaleRouter, pid int64, query string, variables any) map[string]json.RawMessage {
	t.Helper()
	server := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{Routing: router}}))
	server.AddTransport(transport.POST{})
	server.SetErrorPresenter(presentGraphQLError)
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if pid != 0 {
		ctx = context.WithValue(ctx, graph.QUERY_CONTEXT_KEY, &graph.ContextValue{UserPID: pid})
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/query", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRoutingHTTPAuthentication(t *testing.T) {
	e := newTestApp(t)
	for _, token := range []string{"", "Bearer invalid"} {
		for _, query := range []string{`{ tailscaleRouting { exitNodeID } }`, `mutation { setExitNode(input: {exitNodeID:"", allowLANAccess:false}) }`} {
			body, _ := json.Marshal(map[string]string{"query": query})
			w := appRequest(e, http.MethodPost, "/api/v1/query", string(body), token)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
			}
		}
	}
}

func TestSetExitNodeGraphQL(t *testing.T) {
	db := setupAuthDatabase(t, t.Context())
	admin := &database.User{Username: "admin", Role: "admin"}
	regular := &database.User{Username: "user", Role: "user"}
	for _, user := range []*database.User{admin, regular} {
		if _, err := db.NewInsert().Model(user).Exec(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name           string
		pid            int64
		id             string
		allow, missing bool
		err            error
		want           string
		calls          int
	}{
		{name: "select", pid: admin.PID, id: "exit-1", allow: true, calls: 1},
		{name: "clear", pid: admin.PID, calls: 1},
		{name: "regular user", pid: regular.PID, want: graph.ErrRoutingAdmin.Error()},
		{name: "no identity", want: auth.ErrUnauthorized.Error()},
		{name: "deleted identity", pid: 99999, want: auth.ErrUnauthorized.Error()},
		{name: "no backend", pid: admin.PID, missing: true, want: tailscale.ErrRoutingUnavailable.Error()},
		{name: "bad exit node", pid: admin.PID, err: tailscale.ErrExitNodeInvalid, want: tailscale.ErrExitNodeInvalid.Error(), calls: 1},
		{name: "stopped", pid: admin.PID, err: tailscale.ErrRoutingStopped, want: tailscale.ErrRoutingStopped.Error(), calls: 1},
		{name: "advertising", pid: admin.PID, err: tailscale.ErrRoutingAdvertised, want: tailscale.ErrRoutingAdvertised.Error(), calls: 1},
		{name: "read failure", pid: admin.PID, err: tailscale.ErrRoutingUnavailable, want: tailscale.ErrRoutingUnavailable.Error(), calls: 1},
		{name: "unknown write outcome", pid: admin.PID, err: tailscale.ErrRoutingApply, want: tailscale.ErrRoutingApply.Error(), calls: 1},
		{name: "internal details masked", pid: admin.PID, err: errors.New("private node key"), want: "Internal Server Error", calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := &routingMock{err: tc.err}
			var router graph.TailscaleRouter = mock
			if tc.missing {
				router = nil
			}
			result := routingGraphQL(t, router, tc.pid, `mutation SetExitNode($input: ExitNodeInput!) { setExitNode(input:$input) }`, map[string]any{
				"input": map[string]any{"exitNodeID": tc.id, "allowLANAccess": tc.allow},
			})
			if mock.calls != tc.calls {
				t.Fatalf("calls=%d want=%d", mock.calls, tc.calls)
			}
			if tc.want != "" {
				var errs []struct{ Message string }
				if err := json.Unmarshal(result["errors"], &errs); err != nil {
					t.Fatal(err)
				}
				if len(errs) != 1 || errs[0].Message != tc.want || string(result["data"]) != "null" {
					t.Fatalf("result=%s", result)
				}
			} else if len(result["errors"]) != 0 || string(result["data"]) != `{"setExitNode":true}` || mock.id != tc.id || mock.allow != tc.allow {
				t.Fatalf("result=%s mock=%+v", result, mock)
			}
		})
	}
}

func TestRoutingGraphQLRead(t *testing.T) {
	query := `{ tailscaleRouting { backendState exitNodeID exitNodeIP allowLANAccess advertiseExitNode exitNodes { id hostName dnsName os tailscaleIPs online } } }`
	mock := &routingMock{value: tailscale.Routing{BackendState: "Running", ExitNodeID: "exit-1", AllowLANAccess: true,
		ExitNodes: []tailscale.Peer{{ID: "exit-1", Hostname: "exit-one", IPs: []string{"100.64.0.2"}, Online: true}},
	}}
	result := routingGraphQL(t, mock, 1, query, nil)
	if len(result["errors"]) != 0 || !strings.Contains(string(result["data"]), `"allowLANAccess":true`) || !strings.Contains(string(result["data"]), `"id":"exit-1"`) {
		t.Fatalf("result=%s", result)
	}
	mock.value.ExitNodes = nil
	result = routingGraphQL(t, mock, 1, query, nil)
	if len(result["errors"]) != 0 || !strings.Contains(string(result["data"]), `"exitNodes":[]`) {
		t.Fatalf("result=%s", result)
	}
	mock.err = errors.New("secret preferences")
	for _, router := range []graph.TailscaleRouter{mock, nil} {
		result = routingGraphQL(t, router, 1, query, nil)
		if !strings.Contains(string(result["errors"]), tailscale.ErrRoutingUnavailable.Error()) || strings.Contains(string(result["errors"]), "secret") {
			t.Fatalf("result=%s", result)
		}
	}
}
