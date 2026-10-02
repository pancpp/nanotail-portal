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
	"github.com/pancpp/nanotail-portal/tailscale"
)

type renewalMock struct {
	err       error
	calls     int
	mutation  bool
	attemptID string
}

func (m *renewalMock) KeyRenewal(context.Context) (tailscale.KeyRenewal, error) {
	m.calls++
	return tailscale.KeyRenewal{State: "AWAITING_LOGIN", AuthURL: "https://login.tailscale.com/a/test", AttemptID: "attempt-1"}, m.err
}
func (m *renewalMock) RenewNodeKey(ctx context.Context) (tailscale.KeyRenewal, error) {
	m.mutation = true
	m.calls++
	return tailscale.KeyRenewal{State: "READY", AttemptID: "attempt-1"}, m.err
}

func (m *renewalMock) BeginNodeKeyRenewal(_ context.Context, id string) (tailscale.KeyRenewal, error) {
	m.calls++
	m.mutation = true
	m.attemptID = id
	return tailscale.KeyRenewal{State: "STARTING", AttemptID: id}, m.err
}
func (m *renewalMock) CancelNodeKeyRenewal(_ context.Context, id string) (tailscale.KeyRenewal, error) {
	m.calls++
	m.mutation = true
	m.attemptID = id
	return tailscale.KeyRenewal{State: "CANCELLED", AttemptID: id}, m.err
}

func TestNodeKeyRenewalGraphQL(t *testing.T) {
	db := setupAuthDatabase(t, t.Context())
	admin, regular := &database.User{Username: "admin", Role: "admin"}, &database.User{Username: "user", Role: "user"}
	for _, user := range []*database.User{admin, regular} {
		if _, err := db.NewInsert().Model(user).Exec(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	for _, operation := range []struct {
		field, state     string
		mutation, withID bool
	}{
		{"tailscaleKeyRenewal", "AWAITING_LOGIN", false, false}, {"renewTailscaleNodeKey", "READY", true, false},
		{"beginTailscaleNodeKeyRenewal", "STARTING", true, true}, {"cancelTailscaleNodeKeyRenewal", "CANCELLED", true, true},
	} {
		field, mutation, prefix := operation.field, operation.mutation, "query"
		if mutation {
			prefix = "mutation"
		}
		args := ""
		if operation.withID {
			args = `(attemptID: "attempt-1")`
		}
		for _, tc := range []struct {
			name    string
			pid     int64
			missing bool
			err     error
			want    string
			calls   int
		}{
			{name: "admin", pid: admin.PID, calls: 1},
			{name: "regular", pid: regular.PID, want: graph.ErrKeyRenewalAdmin.Error()},
			{name: "missing identity", want: auth.ErrUnauthorized.Error()},
			{name: "deleted identity", pid: 99999, want: auth.ErrUnauthorized.Error()},
			{name: "missing backend", pid: admin.PID, missing: true, want: tailscale.ErrKeyRenewalUnavailable.Error()},
			{name: "unavailable", pid: admin.PID, err: tailscale.ErrKeyRenewalUnavailable, want: tailscale.ErrKeyRenewalUnavailable.Error(), calls: 1},
			{name: "no key", pid: admin.PID, err: tailscale.ErrKeyRenewalUnconfigured, want: tailscale.ErrKeyRenewalUnconfigured.Error(), calls: 1},
			{name: "expiry disabled", pid: admin.PID, err: tailscale.ErrKeyRenewalDisabled, want: tailscale.ErrKeyRenewalDisabled.Error(), calls: 1},
			{name: "unknown outcome", pid: admin.PID, err: tailscale.ErrKeyRenewalStart, want: tailscale.ErrKeyRenewalStart.Error(), calls: 1},
			{name: "unsafe URL", pid: admin.PID, err: tailscale.ErrKeyRenewalURL, want: tailscale.ErrKeyRenewalURL.Error(), calls: 1},
			{name: "stale ID", pid: admin.PID, err: tailscale.ErrKeyRenewalChanged, want: tailscale.ErrKeyRenewalChanged.Error(), calls: 1},
			{name: "already started", pid: admin.PID, err: tailscale.ErrKeyRenewalStarted, want: tailscale.ErrKeyRenewalStarted.Error(), calls: 1},
			{name: "internal failure", pid: admin.PID, err: errors.New("private diagnostic"), want: "Internal Server Error", calls: 1},
		} {
			t.Run(field+"/"+tc.name, func(t *testing.T) {
				mock := &renewalMock{err: tc.err}
				var renewer graph.TailscaleKeyRenewer = mock
				if tc.missing {
					renewer = nil
				}
				server := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{KeyRenewer: renewer}}))
				server.AddTransport(transport.POST{})
				server.SetErrorPresenter(presentGraphQLError)
				body, _ := json.Marshal(map[string]string{"query": prefix + " { " + field + args + " { state authURL canRenew attemptID } }"})
				ctx := t.Context()
				if tc.pid != 0 {
					ctx = context.WithValue(ctx, graph.QUERY_CONTEXT_KEY, &graph.ContextValue{UserPID: tc.pid})
				}
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/query", strings.NewReader(string(body)))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				server.ServeHTTP(w, req)
				var result struct {
					Data   map[string]tailscale.KeyRenewal
					Errors []struct{ Message string }
				}
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if w.Code != http.StatusOK || mock.calls != tc.calls {
					t.Fatalf("HTTP %d calls=%d: %s", w.Code, mock.calls, w.Body.String())
				}
				if tc.want != "" {
					if len(result.Errors) != 1 || result.Errors[0].Message != tc.want || result.Data != nil || strings.Contains(w.Body.String(), "/a/test") {
						t.Fatalf("result=%s", w.Body.String())
					}
				} else {
					wantURL := ""
					if !mutation {
						wantURL = "https://login.tailscale.com/a/test"
					}
					if len(result.Errors) != 0 || result.Data[field].State != operation.state || result.Data[field].AuthURL != wantURL || result.Data[field].AttemptID != "attempt-1" || mock.mutation != mutation || (operation.withID && mock.attemptID != "attempt-1") {
						t.Fatalf("result=%s", w.Body.String())
					}
				}
			})
		}
	}
}

func TestNodeKeyRenewalHTTPAuthentication(t *testing.T) {
	e := newTestApp(t)
	for _, token := range []string{"", "Bearer invalid"} {
		for _, query := range []string{`{ tailscaleKeyRenewal { state authURL canRenew } }`, `mutation { renewTailscaleNodeKey { state authURL canRenew } }`, `mutation { beginTailscaleNodeKeyRenewal(attemptID:"attempt-1") { state } }`, `mutation { cancelTailscaleNodeKeyRenewal(attemptID:"attempt-1") { state } }`} {
			body, _ := json.Marshal(map[string]string{"query": query})
			w := appRequest(e, http.MethodPost, "/api/v1/query", string(body), token)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("HTTP %d %s", w.Code, w.Body.String())
			}
		}
	}
}

func TestGeneralStatusProtectsRenewalURL(t *testing.T) {
	db := setupAuthDatabase(t, t.Context())
	admin, regular := &database.User{Username: "admin", Role: "admin"}, &database.User{Username: "user", Role: "user"}
	for _, user := range []*database.User{admin, regular} {
		if _, err := db.NewInsert().Model(user).Exec(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	const link = "https://login.tailscale.com/a/test"
	r := &graph.Resolver{Tailscale: statusReaderFunc(func(context.Context) (tailscale.Status, error) {
		return tailscale.Status{BackendState: "NeedsLogin", AuthURL: link}, nil
	})}
	for _, pid := range []int64{admin.PID, regular.PID, 99999, 0} {
		ctx := context.WithValue(t.Context(), graph.QUERY_CONTEXT_KEY, &graph.ContextValue{UserPID: pid})
		status, err := r.Query().TailscaleStatus(ctx)
		want := ""
		if pid == admin.PID {
			want = link
		}
		if err != nil || status.AuthURL != want {
			t.Fatalf("pid=%d status=%+v err=%v", pid, status, err)
		}
	}
}
