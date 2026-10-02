package app

import (
	"context"
	"errors"
	"testing"

	"github.com/pancpp/nanotail-portal/auth"
	"github.com/pancpp/nanotail-portal/database"
	"github.com/uptrace/bun"
	"golang.org/x/crypto/bcrypt"
)

// The database package owns a global connection, so these tests run serially.
func setupAuthDatabase(t *testing.T, ctx context.Context) *bun.DB {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("NANOTAIL_DATA_DIR", dir)
	if err := database.Init(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	db := database.DB()
	if _, err := db.NewCreateTable().Model((*database.User)(nil)).Exec(database.Context()); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestAuthenticateWithUsernamePassword(t *testing.T) {
	db := setupAuthDatabase(t, t.Context())
	hash, err := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	emptyHash, err := bcrypt.GenerateFromPassword(nil, bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	otherHash, err := bcrypt.GenerateFromPassword([]byte("different-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	users := []database.User{
		{Username: "admin", Passwd: string(hash), Role: "admin"},
		{Username: "literal_%", Passwd: string(hash), Role: "user"},
		{Username: "invalid-hash", Passwd: "not-a-bcrypt-hash", Role: "user"},
		{Username: "", Passwd: string(hash), Role: "user"},
		{Username: "empty-password", Passwd: string(emptyHash), Role: "user"},
		{Username: "other", Passwd: string(otherHash), Role: "user"},
	}
	for i := range users {
		if _, err := db.NewInsert().Model(&users[i]).Exec(database.Context()); err != nil {
			t.Fatal(err)
		}
	}

	for _, tt := range []struct {
		name     string
		username string
		password string
		want     *database.User
	}{
		{"default credentials", "admin", "admin", &users[0]},
		{"case insensitive username", "AdMiN", "admin", &users[0]},
		{"literal wildcard characters", "literal_%", "admin", &users[1]},
		{"case insensitive literal wildcard characters", "LiTeRaL_%", "admin", &users[1]},
		{"another account", "other", "different-password", &users[5]},
		{"unknown username", "missing", "admin", nil},
		{"username prefix does not match", "adm", "admin", nil},
		{"wrong password", "admin", "wrong", nil},
		{"password from another account", "other", "admin", nil},
		{"case sensitive password", "admin", "ADMIN", nil},
		{"percent is not a wildcard", "%", "admin", nil},
		{"underscore is not a wildcard", "ad_in", "admin", nil},
		{"SQL input is literal", "' OR 1=1 --", "admin", nil},
		{"invalid password hash", "invalid-hash", "admin", nil},
		{"empty username", "", "admin", nil},
		{"empty password", "empty-password", "", nil},
		{"empty credentials", "", "", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := auth.AuthenticateWithUsernamePassword(tt.username, tt.password)
			if tt.want == nil {
				if !errors.Is(err, auth.ErrUnauthorized) || got != nil {
					t.Fatalf("got (%v, %v), want (nil, auth.ErrUnauthorized)", got, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got == nil || got.PID != tt.want.PID || got.Username != tt.want.Username || got.Role != tt.want.Role {
				t.Fatalf("got %v, want user %q with PID %d and role %q", got, tt.want.Username, tt.want.PID, tt.want.Role)
			}
		})
	}
}

func TestAuthenticateWithUsernamePasswordDatabaseError(t *testing.T) {
	db := setupAuthDatabase(t, t.Context())
	// A missing table is a database failure, not a credentials mismatch.
	if _, err := db.NewDropTable().Model((*database.User)(nil)).Exec(database.Context()); err != nil {
		t.Fatal(err)
	}
	user, err := auth.AuthenticateWithUsernamePassword("admin", "admin")
	if user != nil || err == nil || errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("got (%v, %v), want (nil, database error)", user, err)
	}

	// Empty credentials must be rejected before attempting the failing lookup.
	for _, tt := range []struct {
		name     string
		username string
		password string
	}{
		{"empty username", "", "admin"},
		{"empty password", "admin", ""},
		{"empty credentials", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			user, err := auth.AuthenticateWithUsernamePassword(tt.username, tt.password)
			if user != nil || !errors.Is(err, auth.ErrUnauthorized) {
				t.Fatalf("got (%v, %v), want (nil, auth.ErrUnauthorized)", user, err)
			}
		})
	}
}

func TestAuthenticateWithUsernamePasswordCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	setupAuthDatabase(t, ctx)
	cancel()

	user, err := auth.AuthenticateWithUsernamePassword("admin", "admin")
	if user != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("got (%v, %v), want (nil, context.Canceled)", user, err)
	}
}

func TestAuthenticateWithUserPIDPassword(t *testing.T) {
	db := setupAuthDatabase(t, t.Context())
	users := []database.User{
		{Username: "admin", Role: "admin"},
		{Username: "other", Role: "user"},
		{Username: "invalid-hash", Passwd: "not-a-bcrypt-hash", Role: "user"},
		{Username: "deleted", Role: "user"},
	}
	for i, password := range []string{"admin", "different-password", "", "admin"} {
		if password != "" {
			hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
			if err != nil {
				t.Fatal(err)
			}
			users[i].Passwd = string(hash)
		}
		if _, err := db.NewInsert().Model(&users[i]).Exec(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.NewDelete().Model(&users[3]).WherePK().Exec(t.Context()); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name     string
		pid      int64
		password string
		want     error
	}{
		{"matching admin", users[0].PID, "admin", nil},
		{"matching non-admin", users[1].PID, "different-password", nil},
		{"wrong password", users[0].PID, "wrong", auth.ErrUnauthorized},
		{"case sensitive password", users[0].PID, "ADMIN", auth.ErrUnauthorized},
		{"another account's password", users[1].PID, "admin", auth.ErrUnauthorized},
		{"empty password", users[0].PID, "", auth.ErrUnauthorized},
		{"invalid hash", users[2].PID, "admin", auth.ErrUnauthorized},
		{"deleted user", users[3].PID, "admin", auth.ErrUnauthorized},
		{"missing user", users[3].PID + 100, "admin", auth.ErrUnauthorized},
		{"zero PID", 0, "admin", auth.ErrUnauthorized},
		{"negative PID", -1, "admin", auth.ErrUnauthorized},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := auth.AuthenticateWithUserPIDPassword(tt.pid, tt.password); !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}
}

func TestAuthenticateWithUserPIDPasswordDatabaseError(t *testing.T) {
	db := setupAuthDatabase(t, t.Context())
	if _, err := db.NewDropTable().Model((*database.User)(nil)).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := auth.AuthenticateWithUserPIDPassword(1, "admin"); err == nil || errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("got %v, want a database error rather than a credentials mismatch", err)
	}
}

func TestAuthenticateWithUserPIDPasswordCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	setupAuthDatabase(t, ctx)
	cancel()

	if err := auth.AuthenticateWithUserPIDPassword(1, "admin"); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}
