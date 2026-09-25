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
	"github.com/pancpp/nanotail-portal/app/auth"
	"github.com/pancpp/nanotail-portal/app/graph"
	"github.com/pancpp/nanotail-portal/database"
	"github.com/pancpp/nanotail-portal/device"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

type deviceConfiguratorFunc func(context.Context, device.IPConfig) error

func (f deviceConfiguratorFunc) SetIP(ctx context.Context, input device.IPConfig) error {
	return f(ctx, input)
}

func TestSetDeviceIPHTTPAuthentication(t *testing.T) {
	e := newTestApp(t)
	for _, token := range []string{"", "Bearer invalid"} {
		w := appRequest(e, http.MethodPost, "/api/v1/query", `{"query":"mutation { setDeviceIP(deviceIP: {type: \"DHCP\", ip: \"\", gateway: \"\", dns: []}) }"}`, token)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated mutation returned %d: %s", w.Code, w.Body.String())
		}
	}
}

func TestSetDeviceIPGraphQL(t *testing.T) {
	db := setupAuthDatabase(t, t.Context())
	admin := &database.User{Username: "admin", Role: "admin"}
	regular := &database.User{Username: "user", Role: "user"}
	for _, user := range []*database.User{admin, regular} {
		if _, err := db.NewInsert().Model(user).Exec(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	static := map[string]any{"type": " STATIC ", "ip": " 192.0.2.20/24 ", "gateway": " 192.0.2.1 ", "dns": []string{"1.1.1.1", "1.1.1.1"}}
	dhcp := map[string]any{"type": "DHCP", "ip": "", "gateway": "", "dns": []string{}}
	for _, tt := range []struct {
		name                string
		pid                 int64
		input               any
		configErr           error
		missingConfigurator bool
		wantMessage         string
		wantCalls           int
		wantConfig          device.IPConfig
	}{
		{name: "static", pid: admin.PID, input: static, wantCalls: 1, wantConfig: device.IPConfig{Type: "static", IP: "192.0.2.20/24", Gateway: "192.0.2.1", DNS: []string{"1.1.1.1"}}},
		{name: "DHCP", pid: admin.PID, input: dhcp, wantCalls: 1, wantConfig: device.IPConfig{Type: "DHCP", DNS: []string{}}},
		{name: "null input", pid: admin.PID, wantMessage: device.ErrInvalidIP.Error()},
		{name: "invalid input", pid: admin.PID, input: map[string]any{"type": "static", "ip": "192.0.2.20;reboot", "gateway": "", "dns": []string{}}, wantMessage: device.ErrInvalidIP.Error()},
		{name: "not admin", pid: regular.PID, input: dhcp, wantMessage: graph.ErrDeviceAdmin.Error()},
		{name: "deleted user", pid: 99999, input: dhcp, wantMessage: auth.ErrUnauthorized.Error()},
		{name: "no auth context", input: dhcp, wantMessage: auth.ErrUnauthorized.Error()},
		{name: "missing configurator", pid: admin.PID, input: dhcp, missingConfigurator: true, wantMessage: device.ErrConfigUnavailable.Error()},
		{name: "busy", pid: admin.PID, input: dhcp, configErr: device.ErrConfigBusy, wantMessage: device.ErrConfigBusy.Error(), wantCalls: 1},
		{name: "apply failed", pid: admin.PID, input: dhcp, configErr: device.ErrConfigApply, wantMessage: device.ErrConfigApply.Error(), wantCalls: 1},
		{name: "recovery failed", pid: admin.PID, input: dhcp, configErr: device.ErrConfigRecovery, wantMessage: device.ErrConfigRecovery.Error(), wantCalls: 1},
		{name: "internal error masked", pid: admin.PID, input: dhcp, configErr: errors.New("private OS diagnostic"), wantMessage: "Internal Server Error", wantCalls: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			var gotConfig device.IPConfig
			r := &graph.Resolver{}
			if !tt.missingConfigurator {
				r.DeviceConfig = deviceConfiguratorFunc(func(ctx context.Context, config device.IPConfig) error {
					calls++
					gotConfig = config
					if ctx.Value(graph.QUERY_CONTEXT_KEY).(*graph.ContextValue).UserPID != tt.pid {
						t.Fatal("lost request context")
					}
					return tt.configErr
				})
			}
			server := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: r}))
			server.AddTransport(transport.POST{})
			server.SetErrorPresenter(presentGraphQLError)
			body, err := json.Marshal(map[string]any{
				"query":     `mutation SetDeviceIP($deviceIP: DeviceIP) { setDeviceIP(deviceIP: $deviceIP) }`,
				"variables": map[string]any{"deviceIP": tt.input},
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
				if response.Data != nil || len(response.Errors) != 1 || !strings.HasPrefix(response.Errors[0].Message, tt.wantMessage) {
					t.Fatalf("unexpected error: %s", w.Body.String())
				}
				if strings.Contains(w.Body.String(), "private OS diagnostic") {
					t.Fatal("internal details leaked")
				}
			} else if len(response.Errors) != 0 || string(response.Data["setDeviceIP"]) != "true" || !reflect.DeepEqual(gotConfig, tt.wantConfig) {
				t.Fatalf("unexpected result: %s, config %+v", w.Body.String(), gotConfig)
			}
		})
	}
}
