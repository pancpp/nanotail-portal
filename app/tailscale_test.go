package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/pancpp/nanotail-portal/app/graph"
	"github.com/pancpp/nanotail-portal/database"
	"github.com/pancpp/nanotail-portal/tailscale"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

const credentialQuery = `query { tailscaleClient { clientId hasClientSecret createTime updateTime } }`
const credentialMutation = `mutation Save($credential: TailscaleCredential!) { setTailscaleCredential(credential: $credential) }`

func setupTailscaleApp(t *testing.T) (*echo.Echo, *database.User) {
	t.Helper()
	e, user := setupLoginApp(t)
	if _, err := database.DB().NewCreateTable().Model((*database.TailscaleClient)(nil)).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	return e, user
}

func credentialRequest(t *testing.T, e *echo.Echo, user *database.User, query string, credential any) (map[string]json.RawMessage, gqlerror.List) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": map[string]any{"credential": credential}})
	if err != nil {
		t.Fatal(err)
	}
	w := appRequest(e, http.MethodPost, "/api/v1/query", string(body), testAuthorization(t, user.PID))
	var response struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors gqlerror.List              `json:"errors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK && len(response.Errors) == 0 {
		t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
	}
	return response.Data, response.Errors
}

func TestTailscaleCredentialLifecycle(t *testing.T) {
	e, user := setupTailscaleApp(t)
	data, failures := credentialRequest(t, e, user, credentialQuery, nil)
	if len(failures) != 0 || string(data["tailscaleClient"]) != "null" {
		t.Fatalf("unconfigured credentials: %s %v", data, failures)
	}

	data, failures = credentialRequest(t, e, user, credentialMutation, map[string]string{"clientId": " test-client ", "clientSecret": " test-secret "})
	if len(failures) != 0 || string(data["setTailscaleCredential"]) != "true" {
		t.Fatalf("save: %s %v", data, failures)
	}
	stored := &database.TailscaleClient{PID: 1}
	if err := database.DB().NewSelect().Model(stored).WherePK().Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if stored.ClientID != "test-client" || stored.ClientSecret != "test-secret" || stored.CreateTime.IsZero() {
		t.Fatalf("credentials not saved correctly: ID %q", stored.ClientID)
	}
	created := stored.CreateTime

	data, failures = credentialRequest(t, e, user, credentialQuery, nil)
	var metadata struct {
		ClientID        string
		HasClientSecret bool
	}
	if err := json.Unmarshal(data["tailscaleClient"], &metadata); err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 || metadata.ClientID != "test-client" || !metadata.HasClientSecret || strings.Contains(string(data["tailscaleClient"]), "test-secret") {
		t.Fatal("metadata is missing or exposes the saved secret")
	}
	_, failures = credentialRequest(t, e, user, `query { tailscaleClient { clientSecret } }`, nil)
	if len(failures) == 0 {
		t.Fatal("saved secret can be queried")
	}

	// Keeping the same ID may retain the saved secret; changing IDs may not.
	_, failures = credentialRequest(t, e, user, credentialMutation, map[string]string{"clientId": "test-client"})
	if len(failures) != 0 {
		t.Fatalf("keep secret: %v", failures)
	}
	_, failures = credentialRequest(t, e, user, credentialMutation, map[string]string{"clientId": "another-client"})
	if len(failures) != 1 || failures[0].Message != graph.ErrInvalidCredential.Error() {
		t.Fatalf("changed ID retained a mismatched secret: %v", failures)
	}

	stored.UpdateTime = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	stored.ApiToken, stored.ApiTokenId, stored.AuthKey, stored.AuthKeyId = "old-token", "token-id", "old-key", "key-id"
	if _, err := database.DB().NewUpdate().Model(stored).WherePK().Column("update_time", "api_token", "api_token_id", "auth_key", "auth_key_id").Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, failures = credentialRequest(t, e, user, credentialMutation, map[string]string{"clientId": "replacement-client", "clientSecret": "replacement-secret"})
	if len(failures) != 0 {
		t.Fatalf("replace: %v", failures)
	}
	updated := &database.TailscaleClient{PID: 1}
	if err := database.DB().NewSelect().Model(updated).WherePK().Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if updated.ClientID != "replacement-client" || updated.ClientSecret != "replacement-secret" || !updated.CreateTime.Equal(created) || !updated.UpdateTime.After(stored.UpdateTime) {
		t.Fatal("replacement did not preserve creation time or update credentials")
	}
	if updated.ApiToken != "" || updated.ApiTokenId != "" || updated.AuthKey != "" || updated.AuthKeyId != "" {
		t.Fatal("replacement retained derived credentials")
	}
	if count, err := database.DB().NewSelect().Model((*database.TailscaleClient)(nil)).Count(t.Context()); err != nil || count != 1 {
		t.Fatalf("expected singleton credentials: %d %v", count, err)
	}

	for range 2 {
		data, failures = credentialRequest(t, e, user, `mutation { clearTailscaleCredential }`, nil)
		if len(failures) != 0 || string(data["clearTailscaleCredential"]) != "true" {
			t.Fatalf("clear: %s %v", data, failures)
		}
	}
	data, failures = credentialRequest(t, e, user, credentialQuery, nil)
	if len(failures) != 0 || string(data["tailscaleClient"]) != "null" {
		t.Fatal("credentials remain after removal")
	}
}

func TestTailscaleCredentialRejectsInvalidInput(t *testing.T) {
	e, user := setupTailscaleApp(t)
	for _, credential := range []map[string]string{
		{"clientId": "", "clientSecret": "test-secret"},
		{"clientId": "client"},
		{"clientId": "client", "clientSecret": " "},
		{"clientId": "client id", "clientSecret": "test-secret"},
		{"clientId": "client", "clientSecret": "test\nsecret"},
		{"clientId": strings.Repeat("x", 513), "clientSecret": "test-secret"},
		{"clientId": "client", "clientSecret": strings.Repeat("x", 4097)},
	} {
		_, failures := credentialRequest(t, e, user, credentialMutation, credential)
		if len(failures) != 1 || failures[0].Message != graph.ErrInvalidCredential.Error() {
			t.Fatalf("expected validation error, got %v", failures)
		}
	}
	if count, err := database.DB().NewSelect().Model((*database.TailscaleClient)(nil)).Count(t.Context()); err != nil || count != 0 {
		t.Fatal("invalid input persisted credentials")
	}
}

func TestTailscaleCredentialAuthorizationAndDatabaseFailure(t *testing.T) {
	e, user := setupTailscaleApp(t)
	user.Role = "user"
	if _, err := database.DB().NewUpdate().Model(user).WherePK().Column("role").Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{credentialQuery, credentialMutation, `mutation { clearTailscaleCredential }`} {
		_, failures := credentialRequest(t, e, user, query, map[string]string{"clientId": "client", "clientSecret": "secret"})
		if len(failures) != 1 || failures[0].Message != graph.ErrTailscaleAdmin.Error() {
			t.Fatalf("non-admin access: %v", failures)
		}
	}
	user.Role = "admin"
	if _, err := database.DB().NewUpdate().Model(user).WherePK().Column("role").Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB().NewDropTable().Model((*database.TailscaleClient)(nil)).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{credentialQuery, credentialMutation, `mutation { clearTailscaleCredential }`} {
		_, failures := credentialRequest(t, e, user, query, map[string]string{"clientId": "client", "clientSecret": "secret"})
		if len(failures) != 1 || failures[0].Message != "Internal Server Error" {
			t.Fatalf("database details not masked: %v", failures)
		}
	}
}

type statusReaderFunc func(context.Context) (tailscale.Status, error)

func (f statusReaderFunc) Status(ctx context.Context) (tailscale.Status, error) { return f(ctx) }

func TestTailscaleStatusDistinguishesLoginFromConnectivity(t *testing.T) {
	for _, tt := range []struct {
		state                         string
		online, connected, needsLogin bool
	}{
		{"NeedsLogin", false, false, true}, {"Running", true, true, false},
		{"Running", false, false, false}, {"Stopped", false, false, false},
		{"Starting", false, false, false}, {"NeedsMachineAuth", false, false, false},
	} {
		t.Run(tt.state, func(t *testing.T) {
			r := &graph.Resolver{Tailscale: statusReaderFunc(func(ctx context.Context) (tailscale.Status, error) {
				if ctx != t.Context() {
					t.Fatal("request context was not propagated")
				}
				return tailscale.Status{BackendState: tt.state, Self: &tailscale.Peer{Online: tt.online}, IPs: []string{}, AuthURL: "private-auth-url"}, nil
			})}
			status, err := r.Query().TailscaleStatus(t.Context())
			if err != nil || status.Connected != tt.connected || status.NeedsLogin != tt.needsLogin {
				t.Fatalf("status: %+v %v", status, err)
			}
			body, err := json.Marshal(status)
			if err != nil || strings.Contains(string(body), "private-auth-url") {
				t.Fatal("status exposed authentication data")
			}
		})
	}
	r := &graph.Resolver{Tailscale: statusReaderFunc(func(context.Context) (tailscale.Status, error) {
		return tailscale.Status{}, errors.New("private daemon details")
	})}
	if status, err := r.Query().TailscaleStatus(t.Context()); status != nil || !errors.Is(err, graph.ErrTailscaleStatus) {
		t.Fatalf("unavailable daemon should not be NeedsLogin: %+v %v", status, err)
	}
}
