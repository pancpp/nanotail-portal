package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/errcode"
	"github.com/pancpp/nanotail-portal/app/auth"
	"github.com/pancpp/nanotail-portal/app/graph"
	"github.com/pancpp/nanotail-portal/device"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func TestGraphQLErrorPresenter(t *testing.T) {
	ctx := context.Background()
	if got := presentGraphQLError(ctx, nil); got != nil {
		t.Fatalf("nil error became %v", got)
	}
	for _, expected := range []error{auth.ErrUnauthorized, graph.ErrInvalidPassword, graph.ErrInvalidCredential, graph.ErrTailscaleAdmin, graph.ErrTailscaleStatus, graph.ErrDeviceStatus,
		graph.ErrNetworkActivity, graph.ErrNetworkActivityHistory, graph.ErrDeviceAdmin, device.ErrInvalidIP, device.ErrConfigBusy, device.ErrConfigUnavailable, device.ErrConfigApply, device.ErrConfigRecovery} {
		wrapped := &gqlerror.Error{Err: fmt.Errorf("resolver: %w", expected), Message: expected.Error()}
		for _, err := range []error{expected, fmt.Errorf("resolver: %w", expected), wrapped, fmt.Errorf("execution: %w", wrapped)} {
			got := presentGraphQLError(ctx, err)
			if got == nil || !errors.Is(got, expected) || !strings.Contains(got.Message, expected.Error()) {
				t.Fatalf("expected error %v was masked: %v", err, got)
			}
		}
	}
	for _, code := range []string{errcode.ParseFailed, errcode.ValidationFailed, "PERSISTED_QUERY_NOT_FOUND"} {
		err := &gqlerror.Error{Message: "Invalid GraphQL input", Extensions: map[string]any{"code": code}}
		if got := presentGraphQLError(ctx, err); got.Message != err.Message || got.Extensions["code"] != code {
			t.Fatalf("protocol error was masked: %v", got)
		}
	}

	internal := &gqlerror.Error{
		Err:        errors.New("private database details"),
		Message:    "private database details",
		Path:       ast.Path{ast.PathName("changePassword")},
		Locations:  []gqlerror.Location{{Line: 1, Column: 10}},
		Extensions: map[string]any{"code": "DATABASE_ERROR", "debug": "private database details"},
	}
	for _, tt := range []struct {
		name string
		err  error
	}{
		{"plain error", internal.Err},
		{"wrapped error", fmt.Errorf("resolver: %w", internal.Err)},
		{"GraphQL error", internal},
		{"wrapped GraphQL error", fmt.Errorf("execution: %w", internal)},
		{"matching auth message without sentinel", errors.New(auth.ErrUnauthorized.Error())},
		{"matching validation message without sentinel", errors.New(graph.ErrInvalidPassword.Error())},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := presentGraphQLError(ctx, tt.err)
			if got == nil || got.Message != "Internal Server Error" || got.Err != nil || len(got.Extensions) != 0 {
				t.Fatalf("internal error was not sanitized: %#v", got)
			}
			payload, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(payload), "private database details") || strings.Contains(string(payload), "DATABASE_ERROR") {
				t.Fatalf("internal details leaked in the serialized error: %s", payload)
			}
		})
	}
	got := presentGraphQLError(ctx, internal)
	if !reflect.DeepEqual(got.Path, internal.Path) || !reflect.DeepEqual(got.Locations, internal.Locations) {
		t.Fatal("sanitizing the error lost its field location")
	}
	if internal.Message != "private database details" || internal.Err == nil ||
		internal.Extensions["code"] != "DATABASE_ERROR" || internal.Extensions["debug"] != "private database details" {
		t.Fatal("presenter mutated the original error")
	}
}

func TestDeviceStatusRequiresAuthentication(t *testing.T) {
	e := newTestApp(t)
	for _, authorization := range []string{"", "Bearer invalid"} {
		w := appRequest(e, http.MethodPost, "/api/v1/query", `{"query":"query { deviceStatus { hostname lanIPType lanIP gateway dns lanIPv6Type lanIPv6 gateway6 ethAddr cpuload memory lastRestart uptime health } }"}`, authorization)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated device status returned %d: %s", w.Code, w.Body.String())
		}
	}
}

func TestNetworkActivityRequiresAuthentication(t *testing.T) {
	e := newTestApp(t)
	for _, authorization := range []string{"", "Bearer invalid"} {
		w := appRequest(e, http.MethodPost, "/api/v1/query", `{"query":"query { networkActivity { interfaceName rxBytes txBytes sampledAt counterEpoch } }"}`, authorization)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated traffic query returned %d: %s", w.Code, w.Body.String())
		}
	}
}

func TestGraphQLErrorPresenterLogsInternalErrors(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })

	presentGraphQLError(t.Context(), auth.ErrUnauthorized)
	presentGraphQLError(t.Context(), graph.ErrInvalidPassword)
	presentGraphQLError(t.Context(), &gqlerror.Error{
		Message: "invalid input", Extensions: map[string]any{"code": errcode.ValidationFailed},
	})
	if output.Len() != 0 {
		t.Fatalf("expected client errors were logged as internal failures: %s", output.String())
	}
	presentGraphQLError(t.Context(), errors.New("private database details"))
	if !strings.Contains(output.String(), "private database details") {
		t.Fatalf("internal details were not preserved in server logs: %s", output.String())
	}
}

func TestGraphQLRequestErrorsRemainReadable(t *testing.T) {
	e := newTestApp(t)
	authorization := testAuthorization(t, 42)
	for _, tt := range []struct {
		name, body, code string
	}{
		{"parse error", `{"query":"query {"}`, errcode.ParseFailed},
		{"validation error", `{"query":"query { missingField }"}`, errcode.ValidationFailed},
		{"persisted query cache miss", `{"extensions":{"persistedQuery":{"version":1,"sha256Hash":"missing"}}}`, "PERSISTED_QUERY_NOT_FOUND"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := appRequest(e, http.MethodPost, "/api/v1/query", tt.body, authorization)
			var response struct {
				Errors []*gqlerror.Error `json:"errors"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Errors) != 1 || response.Errors[0].Extensions["code"] != tt.code ||
				response.Errors[0].Message == "Internal Server Error" {
				t.Fatalf("expected readable %s error, got %d %s", tt.code, w.Code, w.Body.String())
			}
		})
	}
}

func TestGraphQLPasswordErrorsRemainReadable(t *testing.T) {
	e, user := setupLoginApp(t)
	for _, tt := range []struct {
		current, next, want string
	}{
		{"wrong", "new-password", auth.ErrUnauthorized.Error()},
		{"admin", "short", graph.ErrInvalidPassword.Error()},
	} {
		w := appRequest(e, http.MethodPost, "/api/v1/query",
			passwordChangeBody(t, tt.current, tt.next), testAuthorization(t, user.PID))
		assertPasswordChangeResponse(t, w, false)
		var response struct {
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusOK || len(response.Errors) != 1 || response.Errors[0].Message != tt.want {
			t.Fatalf("expected retryable GraphQL error %q, got %d %s", tt.want, w.Code, w.Body.String())
		}
	}
}
