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

type connectionMock struct {
	value       tailscale.Connection
	err         error
	calls       int
	enabled     bool
	logoutCalls int
}

func (m *connectionMock) Connection(context.Context) (tailscale.Connection, error) {
	return m.value, m.err
}
func (m *connectionMock) SetEnabled(_ context.Context, enabled bool) error {
	m.calls++
	m.enabled = enabled
	return m.err
}

func (m *connectionMock) Logout(context.Context) error {
	m.logoutCalls++
	return m.err
}

func connectionGraphQL(t *testing.T, connector graph.TailscaleConnector, pid int64, query string, variables any) map[string]json.RawMessage {
	t.Helper()
	server := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{Connection: connector}}))
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
		t.Fatalf("HTTP %d %s", w.Code, w.Body.String())
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestConnectionHTTPAuthentication(t *testing.T) {
	e := newTestApp(t)
	for _, token := range []string{"", "Bearer invalid"} {
		for _, query := range []string{`{ tailscaleConnection { enabled backendState canEnable } }`, `mutation { setTailscaleEnabled(enabled:false) }`, `mutation { logoutTailscale }`} {
			body, _ := json.Marshal(map[string]string{"query": query})
			w := appRequest(e, http.MethodPost, "/api/v1/query", string(body), token)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("HTTP %d %s", w.Code, w.Body.String())
			}
		}
	}
}

func TestSetTailscaleEnabledGraphQL(t *testing.T) {
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
		enabled, missing bool
		err              error
		want             string
		calls            int
	}{
		{name: "enable", pid: admin.PID, enabled: true, calls: 1}, {name: "disable", pid: admin.PID, calls: 1},
		{name: "regular", pid: regular.PID, want: graph.ErrConnectionAdmin.Error()},
		{name: "missing identity", want: auth.ErrUnauthorized.Error()}, {name: "deleted identity", pid: 99999, want: auth.ErrUnauthorized.Error()},
		{name: "no backend", pid: admin.PID, missing: true, want: tailscale.ErrConnectionUnavailable.Error()},
		{name: "needs enrollment", pid: admin.PID, err: tailscale.ErrConnectionLogin, want: tailscale.ErrConnectionLogin.Error(), calls: 1},
		{name: "read failed", pid: admin.PID, err: tailscale.ErrConnectionUnavailable, want: tailscale.ErrConnectionUnavailable.Error(), calls: 1},
		{name: "unknown outcome", pid: admin.PID, err: tailscale.ErrConnectionApply, want: tailscale.ErrConnectionApply.Error(), calls: 1},
		{name: "masked diagnostic", pid: admin.PID, err: errors.New("secret daemon output"), want: "Internal Server Error", calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := &connectionMock{err: tc.err}
			var connector graph.TailscaleConnector = mock
			if tc.missing {
				connector = nil
			}
			result := connectionGraphQL(t, connector, tc.pid, `mutation SetTailscaleEnabled($enabled:Boolean!) { setTailscaleEnabled(enabled:$enabled) }`, map[string]any{"enabled": tc.enabled})
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
			} else if len(result["errors"]) != 0 || string(result["data"]) != `{"setTailscaleEnabled":true}` || mock.enabled != tc.enabled {
				t.Fatalf("result=%s mock=%+v", result, mock)
			}
		})
	}
}

func TestConnectionGraphQLRead(t *testing.T) {
	query := `{ tailscaleConnection { enabled backendState canEnable } }`
	mock := &connectionMock{value: tailscale.Connection{Enabled: false, BackendState: "Stopped", CanEnable: true}}
	result := connectionGraphQL(t, mock, 1, query, nil)
	if len(result["errors"]) != 0 || string(result["data"]) != `{"tailscaleConnection":{"enabled":false,"backendState":"Stopped","canEnable":true}}` {
		t.Fatalf("result=%s", result)
	}
	mock.err = errors.New("private preferences")
	for _, connector := range []graph.TailscaleConnector{mock, nil} {
		result = connectionGraphQL(t, connector, 1, query, nil)
		if !strings.Contains(string(result["errors"]), tailscale.ErrConnectionUnavailable.Error()) || strings.Contains(string(result["errors"]), "private") {
			t.Fatalf("result=%s", result)
		}
	}
}
