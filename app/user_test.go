package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pancpp/nanotail-portal/database"
	"golang.org/x/crypto/bcrypt"
)

func passwordChangeBody(t *testing.T, current, next string) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{"current_password": current, "new_password": next})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func testAuthorization(t *testing.T, pid int64) string {
	t.Helper()
	token, err := createJwtToken(pid)
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
			w := appRequest(e, http.MethodPost, "/api/change-password",
				passwordChangeBody(t, "admin", tt.password), testAuthorization(t, user.PID))
			if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
				t.Fatalf("got %d %s, want 204 with no body", w.Code, w.Body.String())
			}

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
	validBody := passwordChangeBody(t, "admin", "new-password")
	for _, tt := range []struct {
		name        string
		body        string
		contentType string
		code        int
	}{
		{"malformed JSON", `{`, "application/json", http.StatusBadRequest},
		{"empty body", ``, "application/json", http.StatusBadRequest},
		{"missing fields", `{}`, "application/json", http.StatusBadRequest},
		{"null body", `null`, "application/json", http.StatusBadRequest},
		{"missing current password", `{"new_password":"new-password"}`, "application/json", http.StatusBadRequest},
		{"missing new password", `{"current_password":"admin"}`, "application/json", http.StatusBadRequest},
		{"wrong field type", `{"current_password":42,"new_password":"new-password"}`, "application/json", http.StatusBadRequest},
		{"short password", passwordChangeBody(t, "admin", "1234567"), "application/json", http.StatusBadRequest},
		{"long password", passwordChangeBody(t, "admin", strings.Repeat("p", 73)), "application/json", http.StatusBadRequest},
		{"multibyte byte limit", passwordChangeBody(t, "admin", strings.Repeat("界", 25)), "application/json", http.StatusBadRequest},
		{"incorrect current password", passwordChangeBody(t, "wrong", "new-password"), "application/json", http.StatusUnauthorized},
		{"current password is case sensitive", passwordChangeBody(t, "ADMIN", "new-password"), "application/json", http.StatusUnauthorized},
		{"cannot select another user", `{"current_password":"admin","new_password":"new-password","pid":42}`, "application/json", http.StatusBadRequest},
		{"extra JSON value", validBody + `{}`, "application/json", http.StatusBadRequest},
		{"oversized body", passwordChangeBody(t, "admin", strings.Repeat("p", 65<<10)), "application/json", http.StatusRequestEntityTooLarge},
		{"unsupported content type", validBody, "text/plain", http.StatusUnsupportedMediaType},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/change-password", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", tt.contentType)
			r.Header.Set("Authorization", authorization)
			w := httptest.NewRecorder()
			e.ServeHTTP(w, r)
			if w.Code != tt.code {
				t.Fatalf("got %d, want %d: %s", w.Code, tt.code, w.Body.String())
			}
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
		w := appRequest(e, http.MethodPost, "/api/change-password",
			passwordChangeBody(t, "admin", "new-password"), testAuthorization(t, pid))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("user %d: got %d, want 401: %s", pid, w.Code, w.Body.String())
		}
	}
}

func TestChangePasswordDatabaseFailures(t *testing.T) {
	for _, tt := range []struct {
		name  string
		query string
		code  int
	}{
		{"read failure", "DROP TABLE users", http.StatusInternalServerError},
		{"write failure", "CREATE TRIGGER reject_password BEFORE UPDATE OF passwd ON users BEGIN SELECT RAISE(ABORT, 'test write failure'); END", http.StatusInternalServerError},
		{"no row updated", "CREATE TRIGGER ignore_password BEFORE UPDATE OF passwd ON users BEGIN SELECT RAISE(IGNORE); END", http.StatusUnauthorized},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e, user := setupLoginApp(t)
			if _, err := database.DB().ExecContext(t.Context(), tt.query); err != nil {
				t.Fatal(err)
			}
			w := appRequest(e, http.MethodPost, "/api/change-password",
				passwordChangeBody(t, "admin", "new-password"), testAuthorization(t, user.PID))
			if w.Code != tt.code {
				t.Fatalf("got %d, want %d: %s", w.Code, tt.code, w.Body.String())
			}
			if tt.code == http.StatusInternalServerError {
				var response struct {
					Message string `json:"message"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if response.Message != http.StatusText(http.StatusInternalServerError) {
					t.Fatalf("database details leaked to client: %q", response.Message)
				}
			}
			if tt.name != "read failure" {
				unchanged := &database.User{PID: user.PID}
				if err := database.DB().NewSelect().Model(unchanged).WherePK().Scan(t.Context()); err != nil {
					t.Fatal(err)
				}
				if unchanged.Passwd != user.Passwd || !unchanged.UpdateTime.Equal(user.UpdateTime) {
					t.Fatal("failed update modified the password or timestamp")
				}
			}
		})
	}
}

func TestChangePasswordHonorsRequestCancellation(t *testing.T) {
	e, user := setupLoginApp(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := httptest.NewRequest(http.MethodPost, "/api/change-password",
		strings.NewReader(passwordChangeBody(t, "admin", "new-password"))).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", testAuthorization(t, user.PID))
	cancel()
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("canceled request: got %d, want 500", w.Code)
	}
	unchanged := &database.User{PID: user.PID}
	if err := database.DB().NewSelect().Model(unchanged).WherePK().Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if unchanged.Passwd != user.Passwd || !unchanged.UpdateTime.Equal(user.UpdateTime) {
		t.Fatal("canceled request modified the stored password or timestamp")
	}
}
