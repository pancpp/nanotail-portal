package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pancpp/nanotail-portal/auth"
	"github.com/pancpp/nanotail-portal/database"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"golang.org/x/crypto/bcrypt"
)

func passwordChangeBody(t *testing.T, current, next string) string {
	t.Helper()
	return passwordVariablesBody(t, map[string]any{"oldpassword": current, "newpassword": next})
}

func passwordVariablesBody(t *testing.T, passwords any) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"operationName": "ChangePassword",
		"query":         `mutation ChangePassword($passwords: ChangePassword!) { changePassword(passwords: $passwords) }`,
		"variables":     map[string]any{"passwords": passwords},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func assertPasswordChangeResponse(t *testing.T, w *httptest.ResponseRecorder, success bool) {
	t.Helper()
	var response struct {
		Data *struct {
			ChangePassword bool `json:"changePassword"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid JSON response: %v: %s", err, w.Body.String())
	}
	confirmed := response.Data != nil && response.Data.ChangePassword
	if success {
		if w.Code != http.StatusOK || len(response.Errors) != 0 || !confirmed {
			t.Fatalf("got %d %s, want GraphQL changePassword: true", w.Code, w.Body.String())
		}
	} else if confirmed || (w.Code < 400 && len(response.Errors) == 0) {
		t.Fatalf("got %d %s, want a rejected password change", w.Code, w.Body.String())
	}
}

func testAuthorization(t *testing.T, pid int64) string {
	t.Helper()
	token, err := auth.CreateJwtToken(pid)
	if err != nil {
		t.Fatal(err)
	}
	return "Bearer " + token
}

func TestChangePassword(t *testing.T) {
	for _, tt := range []struct {
		name     string
		password string
		role     string
	}{
		{"minimum length", "new-pass", "admin"},
		{"maximum length", strings.Repeat("p", 72), "admin"},
		{"multibyte password and non-admin user", strings.Repeat("界", 24), "user"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e, user := setupLoginApp(t)
			db := database.DB()
			user.Role = tt.role
			user.UpdateTime = time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
			if _, err := db.NewUpdate().Model(user).Column("role", "update_time").WherePK().Exec(t.Context()); err != nil {
				t.Fatal(err)
			}
			other := &database.User{Username: "other", Passwd: user.Passwd, Role: "user"}
			if _, err := db.NewInsert().Model(other).Exec(t.Context()); err != nil {
				t.Fatal(err)
			}
			w := appRequest(e, http.MethodPost, "/api/v1/query",
				passwordChangeBody(t, "admin", tt.password), testAuthorization(t, user.PID))
			assertPasswordChangeResponse(t, w, true)

			updated := &database.User{PID: user.PID}
			if err := db.NewSelect().Model(updated).WherePK().Scan(t.Context()); err != nil {
				t.Fatal(err)
			}
			if updated.Passwd == user.Passwd || updated.Passwd == tt.password {
				t.Fatal("password was not replaced with a new hash")
			}
			if err := bcrypt.CompareHashAndPassword([]byte(updated.Passwd), []byte(tt.password)); err != nil {
				t.Fatalf("stored password does not match: %v", err)
			}
			if cost, err := bcrypt.Cost([]byte(updated.Passwd)); err != nil || cost != bcrypt.DefaultCost {
				t.Fatalf("bcrypt cost = %d, err = %v, want %d", cost, err, bcrypt.DefaultCost)
			}
			if !updated.UpdateTime.After(user.UpdateTime) {
				t.Fatal("update timestamp did not advance")
			}
			if updated.Username != user.Username || updated.Role != user.Role || !updated.CreateTime.Equal(user.CreateTime) {
				t.Fatal("password change modified unrelated user fields")
			}
			untouched := &database.User{PID: other.PID}
			if err := db.NewSelect().Model(untouched).WherePK().Scan(t.Context()); err != nil {
				t.Fatal(err)
			}
			if untouched.Passwd != other.Passwd || !untouched.UpdateTime.Equal(other.UpdateTime) {
				t.Fatal("password change modified another account")
			}
			for _, login := range []struct {
				password string
				code     int
			}{{"admin", http.StatusUnauthorized}, {tt.password, http.StatusOK}} {
				body, err := json.Marshal(map[string]string{"username": user.Username, "password": login.password})
				if err != nil {
					t.Fatal(err)
				}
				w := appRequest(e, http.MethodPost, "/api/login", string(body), "")
				if w.Code != login.code {
					t.Fatalf("login after password change: got %d, want %d", w.Code, login.code)
				}
			}
		})
	}
}

func TestChangePasswordRejectsInvalidRequests(t *testing.T) {
	e, user := setupLoginApp(t)
	authorization := testAuthorization(t, user.PID)
	for _, tt := range []struct {
		name string
		body string
	}{
		{"malformed JSON", `{`},
		{"empty body", ``},
		{"missing operation", `{}`},
		{"null body", `null`},
		{"missing current password", passwordVariablesBody(t, map[string]any{"newpassword": "new-password"})},
		{"missing new password", passwordVariablesBody(t, map[string]any{"oldpassword": "admin"})},
		{"wrong field type", passwordVariablesBody(t, map[string]any{"oldpassword": 42, "newpassword": "new-password"})},
		{"short password", passwordChangeBody(t, "admin", "1234567")},
		{"long password", passwordChangeBody(t, "admin", strings.Repeat("p", 73))},
		{"multibyte byte limit", passwordChangeBody(t, "admin", strings.Repeat("界", 25))},
		{"incorrect current password", passwordChangeBody(t, "wrong", "new-password")},
		{"current password is case sensitive", passwordChangeBody(t, "ADMIN", "new-password")},
		{"cannot select another user", passwordVariablesBody(t, map[string]any{"oldpassword": "admin", "newpassword": "new-password", "pid": 42})},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/query", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", authorization)
			w := httptest.NewRecorder()
			e.ServeHTTP(w, r)
			assertPasswordChangeResponse(t, w, false)
			unchanged := &database.User{PID: user.PID}
			if err := database.DB().NewSelect().Model(unchanged).WherePK().Scan(t.Context()); err != nil {
				t.Fatal(err)
			}
			if unchanged.Passwd != user.Passwd || !unchanged.UpdateTime.Equal(user.UpdateTime) {
				t.Fatal("rejected request modified the stored password or timestamp")
			}
		})
	}
}

func TestChangePasswordRejectsInvalidUser(t *testing.T) {
	e, user := setupLoginApp(t)
	if _, err := database.DB().NewDelete().Model(user).WherePK().Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, pid := range []int64{0, -1, user.PID, user.PID + 100} {
		w := appRequest(e, http.MethodPost, "/api/v1/query",
			passwordChangeBody(t, "admin", "new-password"), testAuthorization(t, pid))
		assertPasswordChangeResponse(t, w, false)
	}
}

func TestChangePasswordDatabaseFailures(t *testing.T) {
	for _, tt := range []struct {
		name    string
		query   string
		message string
	}{
		{"read failure", "DROP TABLE users", "Internal Server Error"},
		{"write failure", "CREATE TRIGGER reject_password BEFORE UPDATE OF passwd ON users BEGIN SELECT RAISE(ABORT, 'test write failure'); END", "Internal Server Error"},
		{"no row updated", "CREATE TRIGGER ignore_password BEFORE UPDATE OF passwd ON users BEGIN SELECT RAISE(IGNORE); END", auth.ErrUnauthorized.Error()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e, user := setupLoginApp(t)
			if _, err := database.DB().ExecContext(t.Context(), tt.query); err != nil {
				t.Fatal(err)
			}
			w := appRequest(e, http.MethodPost, "/api/v1/query",
				passwordChangeBody(t, "admin", "new-password"), testAuthorization(t, user.PID))
			assertPasswordChangeResponse(t, w, false)
			if tt.name != "read failure" {
				unchanged := &database.User{PID: user.PID}
				if err := database.DB().NewSelect().Model(unchanged).WherePK().Scan(t.Context()); err != nil {
					t.Fatal(err)
				}
				if unchanged.Passwd != user.Passwd || !unchanged.UpdateTime.Equal(user.UpdateTime) {
					t.Fatal("failed update modified the password or timestamp")
				}
			}
			var response struct {
				Data   json.RawMessage   `json:"data"`
				Errors []*gqlerror.Error `json:"errors"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusOK || string(response.Data) != "null" || len(response.Errors) != 1 {
				t.Fatalf("expected a GraphQL resolver error with null data, got %d %s", w.Code, w.Body.String())
			}
			failure := response.Errors[0]
			if failure.Message != tt.message || failure.Path.String() != "changePassword" || len(failure.Extensions) != 0 {
				t.Fatalf("unexpected GraphQL error or leaked internal details: %s", w.Body.String())
			}
		})
	}
}

func TestChangePasswordHonorsRequestCancellation(t *testing.T) {
	e, user := setupLoginApp(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/query",
		strings.NewReader(passwordChangeBody(t, "admin", "new-password"))).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", testAuthorization(t, user.PID))
	cancel()
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	// gqlgen may stop before resolving any fields on an already-canceled request.
	if strings.TrimSpace(w.Body.String()) != "null" {
		assertPasswordChangeResponse(t, w, false)
	}
	unchanged := &database.User{PID: user.PID}
	if err := database.DB().NewSelect().Model(unchanged).WherePK().Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if unchanged.Passwd != user.Passwd || !unchanged.UpdateTime.Equal(user.UpdateTime) {
		t.Fatal("canceled request modified the stored password or timestamp")
	}
}
