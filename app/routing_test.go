package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/pancpp/nanotail-portal/app/graph"
	"github.com/pancpp/nanotail-portal/auth"
	"github.com/pancpp/nanotail-portal/database"
	"github.com/pancpp/nanotail-portal/device"
	"github.com/pancpp/nanotail-portal/tailscale"
)

type routingMock struct {
	value  tailscale.Routing
	err    error
	calls  int
	routes []string
}

func (m *routingMock) Routing(context.Context) (tailscale.Routing, error) { return m.value, m.err }
func (m *routingMock) SetRouting(_ context.Context, routes []string) error {
	m.calls++
	m.routes = routes
	return m.err
}

func routingGraphQL(t *testing.T, router graph.TailscaleRouter, pid int64, query string, variables any, hosts ...graph.RoutingHostReader) map[string]json.RawMessage {
	t.Helper()
	var host graph.RoutingHostReader = hostRoutingMock{}
	if len(hosts) > 0 {
		host = hosts[0]
	}
	server := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{Routing: router, RoutingHost: host}}))
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
	if w.Code != http.StatusOK && w.Code != http.StatusUnprocessableEntity {
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
		for _, query := range []string{`{ tailscaleRouting { advertiseExitNode subnetRoutes } }`, `mutation { setRouting(input: {subnetRoutes:[]}) }`} {
			body, _ := json.Marshal(map[string]string{"query": query})
			w := appRequest(e, http.MethodPost, "/api/v1/query", string(body), token)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
			}
		}
	}
}

type hostRoutingMock struct{}

type unavailableRoutingHost struct{}

func (unavailableRoutingHost) RoutingStatus(context.Context) (device.RoutingStatus, error) {
	return device.RoutingStatus{}, errors.New("private host details")
}

func (hostRoutingMock) RoutingStatus(context.Context) (device.RoutingStatus, error) {
	enabled := true
	return device.RoutingStatus{LANInterface: "eth0", DefaultSubnetRoutes: []string{"192.168.42.0/24"}, IPv4Forwarding: &enabled, IPv6Forwarding: &enabled}, nil
}

func TestSetRoutingGraphQL(t *testing.T) {
	db := setupAuthDatabase(t, t.Context())
	admin := &database.User{Username: "admin", Role: "admin"}
	regular := &database.User{Username: "user", Role: "user"}
	for _, user := range []*database.User{admin, regular} {
		if _, err := db.NewInsert().Model(user).Exec(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name    string
		pid     int64
		missing bool
		err     error
		want    string
		calls   int
	}{
		{name: "advertise", pid: admin.PID, calls: 1},
		{name: "regular user", pid: regular.PID, want: graph.ErrRoutingAdmin.Error()},
		{name: "no identity", want: auth.ErrUnauthorized.Error()},
		{name: "deleted user", pid: 99999, want: auth.ErrUnauthorized.Error()},
		{name: "no backend", pid: admin.PID, missing: true, want: tailscale.ErrRoutingUnavailable.Error()},
		{name: "invalid routes", pid: admin.PID, err: tailscale.ErrSubnetRoutesInvalid, want: tailscale.ErrSubnetRoutesInvalid.Error(), calls: 1},
		{name: "stopped", pid: admin.PID, err: tailscale.ErrRoutingStopped, want: tailscale.ErrRoutingStopped.Error(), calls: 1},
		{name: "read failure", pid: admin.PID, err: tailscale.ErrRoutingUnavailable, want: tailscale.ErrRoutingUnavailable.Error(), calls: 1},
		{name: "unknown outcome", pid: admin.PID, err: tailscale.ErrRoutingApply, want: tailscale.ErrRoutingApply.Error(), calls: 1},
		{name: "choice storage failure", pid: admin.PID, err: tailscale.ErrRoutingPersistence, want: tailscale.ErrRoutingPersistence.Error(), calls: 1},
		{name: "internal error", pid: admin.PID, err: errors.New("secret"), want: "Internal Server Error", calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := &routingMock{err: tc.err}
			var router graph.TailscaleRouter = mock
			if tc.missing {
				router = nil
			}
			routes := []string{"192.168.42.0/24"}
			result := routingGraphQL(t, router, tc.pid, `mutation SetRouting($input: RoutingInput!) {setRouting(input:$input)}`, map[string]any{"input": map[string]any{"subnetRoutes": routes}})
			if mock.calls != tc.calls {
				t.Fatalf("calls %d want %d", mock.calls, tc.calls)
			}
			if tc.want != "" {
				var errs []struct{ Message string }
				if err := json.Unmarshal(result["errors"], &errs); err != nil {
					t.Fatal(err)
				}
				if len(errs) != 1 || errs[0].Message != tc.want || string(result["data"]) != "null" {
					t.Fatalf("%s", result)
				}
			} else if len(result["errors"]) != 0 || string(result["data"]) != `{"setRouting":true}` || !reflect.DeepEqual(mock.routes, routes) {
				t.Fatalf("%s %+v", result, mock)
			}
		})
	}
}

func TestRoutingGraphQLRead(t *testing.T) {
	query := `{tailscaleRouting{backendState advertiseExitNode subnetRoutes subnetDefaultsPending usingExitNode snatEnabled health lanInterface defaultSubnetRoutes lanWarning ipv4Forwarding ipv6Forwarding routeApprovalState routeApprovalMessage}}`
	mock := &routingMock{value: tailscale.Routing{BackendState: "Running", AdvertiseExitNode: true, SubnetRoutes: []string{"192.168.42.0/24"}, SNATEnabled: true}}
	mock.value.Approval = tailscale.RouteApproval{State: "APPROVED", Message: "Tailscale confirmed approval"}
	result := routingGraphQL(t, mock, 1, query, nil)
	if !strings.Contains(string(result["data"]), `"routeApprovalState":"APPROVED"`) || !strings.Contains(string(result["data"]), `"routeApprovalMessage":"Tailscale confirmed approval"`) {
		t.Fatalf("approval metadata missing: %s", result)
	}
	for _, want := range []string{`"advertiseExitNode":true`, `"defaultSubnetRoutes":["192.168.42.0/24"]`, `"ipv4Forwarding":true`, `"health":[]`} {
		if len(result["errors"]) != 0 || !strings.Contains(string(result["data"]), want) {
			t.Fatalf("%s", result)
		}
	}
	mock.value.SubnetRoutes = nil
	mock.value.SubnetDefaultsPending = true
	result = routingGraphQL(t, mock, 1, query, nil)
	if !strings.Contains(string(result["data"]), `"subnetRoutes":[]`) {
		t.Fatalf("%s", result)
	}
	if !strings.Contains(string(result["data"]), `"subnetDefaultsPending":true`) {
		t.Fatalf("%s", result)
	}
	mock.err = errors.New("secret prefs")
	for _, router := range []graph.TailscaleRouter{mock, nil} {
		result = routingGraphQL(t, router, 1, query, nil)
		if !strings.Contains(string(result["errors"]), tailscale.ErrRoutingUnavailable.Error()) || strings.Contains(string(result["errors"]), "secret") {
			t.Fatalf("%s", result)
		}
	}
}

func TestRoutingCannotDisableExitNode(t *testing.T) {
	mock := &routingMock{}
	result := routingGraphQL(t, mock, 1, `mutation {setRouting(input:{advertiseExitNode:false,subnetRoutes:[]})}`, nil)
	if len(result["errors"]) == 0 || mock.calls != 0 {
		t.Fatalf("exit-node disable reached backend: %s", result)
	}
}
