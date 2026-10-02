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
	"github.com/pancpp/nanotail-portal/app/graph"
	"github.com/pancpp/nanotail-portal/auth"
	"github.com/pancpp/nanotail-portal/database"
	"github.com/pancpp/nanotail-portal/device"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

type hostnameConfiguratorFunc func(context.Context, string) error

func (f hostnameConfiguratorFunc) SetHostname(ctx context.Context, hostname string) error {
	return f(ctx, hostname)
}

func TestSetDeviceHostnameHTTPAuthentication(t *testing.T) {
	e := newTestApp(t)
	for _, token := range []string{"", "Bearer invalid"} {
		w := appRequest(e, http.MethodPost, "/api/v1/query", `{"query":"mutation { setDeviceHostname(hostname: \"nanotail-2\") }"}`, token)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d: %s", w.Code, w.Body.String())
		}
	}
}

func TestSetDeviceHostnameGraphQL(t *testing.T) {
	db := setupAuthDatabase(t, t.Context())
	admin := &database.User{Username: "admin", Role: "admin"}
	regular := &database.User{Username: "user", Role: "user"}
	for _, user := range []*database.User{admin, regular} {
		if _, err := db.NewInsert().Model(user).Exec(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	for _, tt := range []struct {
		name        string
		pid         int64
		hostname    string
		configErr   error
		missing     bool
		wantMessage string
		wantCalls   int
	}{
		{name: "admin", pid: admin.PID, hostname: " NanoTail-2 ", wantCalls: 1},
		{name: "invalid", pid: admin.PID, hostname: "--help", wantMessage: device.ErrInvalidHostname.Error()},
		{name: "reserved", pid: admin.PID, hostname: "localhost", wantMessage: device.ErrReservedHostname.Error()},
		{name: "empty", pid: admin.PID, wantMessage: device.ErrInvalidHostname.Error()},
		{name: "regular user", pid: regular.PID, hostname: "nanotail-2", wantMessage: graph.ErrHostnameAdmin.Error()},
		{name: "deleted user", pid: 99999, hostname: "nanotail-2", wantMessage: auth.ErrUnauthorized.Error()},
		{name: "no auth context", hostname: "nanotail-2", wantMessage: auth.ErrUnauthorized.Error()},
		{name: "unavailable", pid: admin.PID, hostname: "nanotail-2", missing: true, wantMessage: device.ErrHostnameApply.Error()},
		{name: "busy", pid: admin.PID, hostname: "nanotail-2", configErr: device.ErrHostnameBusy, wantMessage: device.ErrHostnameBusy.Error(), wantCalls: 1},
		{name: "apply failed", pid: admin.PID, hostname: "nanotail-2", configErr: device.ErrHostnameApply, wantMessage: device.ErrHostnameApply.Error(), wantCalls: 1},
		{name: "internal details masked", pid: admin.PID, hostname: "nanotail-2", configErr: errors.New("private OS details"), wantMessage: "Internal Server Error", wantCalls: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			r := &graph.Resolver{}
			if !tt.missing {
				r.DeviceHostname = hostnameConfiguratorFunc(func(ctx context.Context, hostname string) error {
					calls++
					if hostname != "nanotail-2" || ctx.Value(graph.QUERY_CONTEXT_KEY).(*graph.ContextValue).UserPID != tt.pid {
						t.Fatalf("unexpected hostname or context: %q", hostname)
					}
					return tt.configErr
				})
			}
			server := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: r}))
			server.AddTransport(transport.POST{})
			server.SetErrorPresenter(presentGraphQLError)
			body, err := json.Marshal(map[string]any{
				"query":     `mutation SetDeviceHostname($hostname: String!) { setDeviceHostname(hostname: $hostname) }`,
				"variables": map[string]any{"hostname": tt.hostname},
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			if tt.pid != 0 {
				ctx = context.WithValue(ctx, graph.QUERY_CONTEXT_KEY, &graph.ContextValue{UserPID: tt.pid})
			}
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/query", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			server.ServeHTTP(w, req)
			var response struct {
				Data   map[string]json.RawMessage
				Errors gqlerror.List
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusOK || calls != tt.wantCalls {
				t.Fatalf("status=%d calls=%d: %s", w.Code, calls, w.Body.String())
			}
			if tt.wantMessage != "" {
				if response.Data != nil || len(response.Errors) != 1 || response.Errors[0].Message != tt.wantMessage {
					t.Fatalf("unexpected error: %s", w.Body.String())
				}
			} else if len(response.Errors) != 0 || string(response.Data["setDeviceHostname"]) != "true" {
				t.Fatalf("unexpected result: %s", w.Body.String())
			}
		})
	}
}
