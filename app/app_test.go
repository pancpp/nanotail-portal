package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v5"
	"github.com/pancpp/nanotail-portal/app/auth"
	"github.com/pancpp/nanotail-portal/database"
	"github.com/pancpp/nanotail-portal/webui"
	"golang.org/x/crypto/bcrypt"
)

func newTestApp(t *testing.T) *echo.Echo {
	t.Helper()
	// Exercise the same routing setup as Init without starting its background server.
	e := echo.New()
	if err := webui.Init(e); err != nil {
		t.Fatal(err)
	}
	if err := initAPIs(e); err != nil {
		t.Fatal(err)
	}
	return e
}

func setupLoginApp(t *testing.T) (*echo.Echo, *database.User) {
	t.Helper()
	db := setupAuthDatabase(t, t.Context())
	hash, err := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	user := &database.User{Username: "admin", Passwd: string(hash), Role: "admin"}
	if _, err := db.NewInsert().Model(user).Exec(database.Context()); err != nil {
		t.Fatal(err)
	}
	return newTestApp(t), user
}

func appRequest(e *echo.Echo, method, path, body, authorization string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if method == http.MethodPost {
		r.Header.Set("Content-Type", "application/json")
	}
	if authorization != "" {
		r.Header.Set("Authorization", authorization)
	}
	e.ServeHTTP(w, r)
	return w
}

func TestLoginReturnsUsableJWT(t *testing.T) {
	e, user := setupLoginApp(t)
	for _, username := range []string{"admin", "AdMiN"} {
		t.Run(username, func(t *testing.T) {
			body, err := json.Marshal(map[string]string{"username": username, "password": "admin"})
			if err != nil {
				t.Fatal(err)
			}
			before := time.Now().Truncate(time.Second)
			// Login is public, even when a client sends a stale or invalid bearer token.
			w := appRequest(e, http.MethodPost, "/api/login", string(body), "Bearer invalid")
			if w.Code != http.StatusOK {
				t.Fatalf("login: got %d, want 200: %s", w.Code, w.Body.String())
			}
			var login struct {
				Token string `json:"token"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &login); err != nil {
				t.Fatal(err)
			}
			claims := new(auth.Claims)
			token, err := jwt.ParseWithClaims(login.Token, claims, func(*jwt.Token) (any, error) {
				return auth.GetJwtSignKey(), nil
			}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
			if err != nil {
				t.Fatalf("invalid login token: %v", err)
			}
			if !token.Valid || claims.UserPID != user.PID {
				t.Fatalf("login token does not identify user %d", user.PID)
			}
			if claims.IssuedAt == nil || claims.ExpiresAt == nil {
				t.Fatal("login token is missing its issued-at or expiration claim")
			}
			if claims.IssuedAt.Time.Before(before) || claims.IssuedAt.Time.After(time.Now()) {
				t.Fatalf("unexpected token issue time: %v", claims.IssuedAt.Time)
			}
			if ttl := claims.ExpiresAt.Sub(claims.IssuedAt.Time); ttl != auth.JWT_EXP_LEN {
				t.Fatalf("token lifetime = %v, want %v", ttl, auth.JWT_EXP_LEN)
			}
			w = appRequest(e, http.MethodPost, "/api/v1/query", `{"query":"query { __typename }"}`, "Bearer "+login.Token)
			// A valid token reaches the authenticated GraphQL endpoint.
			if w.Code != http.StatusOK {
				t.Fatalf("authenticated request: got %d, want 200: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestLoginRejectsInvalidRequests(t *testing.T) {
	e, _ := setupLoginApp(t)
	for _, tt := range []struct {
		name string
		body string
		code int
	}{
		{"malformed JSON", `{"username":`, http.StatusBadRequest},
		{"wrong field type", `{"username":42,"password":"admin"}`, http.StatusBadRequest},
		{"empty body", ``, http.StatusUnauthorized},
		{"missing credentials", `{}`, http.StatusUnauthorized},
		{"missing username", `{"password":"admin"}`, http.StatusUnauthorized},
		{"missing password", `{"username":"admin"}`, http.StatusUnauthorized},
		{"unknown user", `{"username":"missing","password":"admin"}`, http.StatusUnauthorized},
		{"wrong password", `{"username":"admin","password":"wrong"}`, http.StatusUnauthorized},
		{"password case differs", `{"username":"admin","password":"ADMIN"}`, http.StatusUnauthorized},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := appRequest(e, http.MethodPost, "/api/login", tt.body, "")
			if w.Code != tt.code {
				t.Fatalf("got %d, want %d: %s", w.Code, tt.code, w.Body.String())
			}
			var response map[string]json.RawMessage
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if _, ok := response["token"]; ok {
				t.Fatal("failed login returned a token")
			}
		})
	}
}

func TestLoginDatabaseError(t *testing.T) {
	db := setupAuthDatabase(t, t.Context())
	if _, err := db.NewDropTable().Model((*database.User)(nil)).Exec(database.Context()); err != nil {
		t.Fatal(err)
	}
	w := appRequest(newTestApp(t), http.MethodPost, "/api/login", `{"username":"admin","password":"admin"}`, "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want 500: %s", w.Code, w.Body.String())
	}
	var response struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Message != http.StatusText(http.StatusInternalServerError) {
		t.Fatalf("database error was not hidden from the client: %q", response.Message)
	}
}

func TestProtectedAPI(t *testing.T) {
	e := newTestApp(t)
	sign := func(method jwt.SigningMethod, key []byte, expires time.Time) string {
		t.Helper()
		token := jwt.NewWithClaims(method, &auth.Claims{
			UserPID: 42,
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(expires),
			},
		})
		signed, err := token.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return signed
	}
	future := time.Now().Add(time.Hour)
	valid := sign(jwt.SigningMethodHS256, auth.GetJwtSignKey(), future)
	for _, tt := range []struct {
		name          string
		authorization string
		code          int
	}{
		{"missing token", "", http.StatusUnauthorized},
		{"malformed token", "Bearer invalid", http.StatusUnauthorized},
		{"wrong authorization scheme", "Basic " + valid, http.StatusUnauthorized},
		{"invalid signature", "Bearer " + sign(jwt.SigningMethodHS256, []byte("incorrect-test-signing-key"), future), http.StatusUnauthorized},
		{"expired token", "Bearer " + sign(jwt.SigningMethodHS256, auth.GetJwtSignKey(), time.Now().Add(-time.Hour)), http.StatusUnauthorized},
		{"wrong algorithm", "Bearer " + sign(jwt.SigningMethodHS384, auth.GetJwtSignKey(), future), http.StatusUnauthorized},
		{"valid token reaches GraphQL", "Bearer " + valid, http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := appRequest(e, http.MethodPost, "/api/v1/query", `{"query":"query { __typename }"}`, tt.authorization)
			if w.Code != tt.code {
				t.Fatalf("got %d, want %d: %s", w.Code, tt.code, w.Body.String())
			}
		})
	}
}

func TestEmbeddedWebUI(t *testing.T) {
	// Static files must be served from the embedded build, not the working directory.
	t.Chdir(t.TempDir())
	e := newTestApp(t)
	w := appRequest(e, http.MethodGet, "/", "", "")
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("WebUI: status %d, content type %q", w.Code, w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Body.String(), `id="root"`) {
		t.Fatal("WebUI is missing the React root element")
	}
	// Discover hashed asset names from the embedded HTML so rebuilds need no test edits.
	assets := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`).FindAllStringSubmatch(w.Body.String(), -1)
	if len(assets) == 0 {
		t.Fatal("WebUI does not reference any built assets")
	}
	for _, asset := range assets {
		t.Run(asset[1], func(t *testing.T) {
			w := appRequest(e, http.MethodGet, asset[1], "", "")
			if w.Code != http.StatusOK || w.Body.Len() == 0 {
				t.Fatalf("asset: status %d, body length %d", w.Code, w.Body.Len())
			}
			if strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
				t.Fatal("asset request returned HTML")
			}
		})
	}
	w = appRequest(e, http.MethodGet, "/assets/missing.js", "", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing asset: got %d, want 404", w.Code)
	}
}
