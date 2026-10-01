package migrations

import (
	"context"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/pancpp/nanotail-portal/conf"
	"github.com/pancpp/nanotail-portal/database"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
	"golang.org/x/crypto/bcrypt"
)

// The application owns a global database, so each test uses a separate working
// directory and runs serially, just like the authentication integration tests.
func startupDB(t *testing.T) *bun.DB {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("NANOTAIL_DATA_DIR", dir)
	if err := database.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database.DB()
}

func assertCurrent(t *testing.T, db *bun.DB) {
	t.Helper()
	ms, err := migrate.NewMigrator(db, Migrations).MigrationsWithStatus(t.Context())
	if err != nil || len(ms.Unapplied()) != 0 {
		t.Fatalf("pending migrations=%v, err=%v", ms.Unapplied(), err)
	}
	count, err := db.NewSelect().Table("bun_migrations").Count(t.Context())
	if err != nil || count != len(Migrations.Sorted()) {
		t.Fatalf("migration records=%d, err=%v", count, err)
	}
}

func TestStartupInitializesAndRestarts(t *testing.T) {
	db := startupDB(t)
	if err := Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertCurrent(t, db)
	admin := database.User{}
	if err := db.NewSelect().Model(&admin).Where("username = ?", "admin").Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if admin.Role != "admin" || bcrypt.CompareHashAndPassword([]byte(admin.Passwd), []byte("admin")) != nil {
		t.Fatal("initial administrator was not created correctly")
	}
	const changedPassword = "existing-password-hash"
	if _, err := db.NewUpdate().Model(&admin).Set("passwd = ?", changedPassword).WherePK().Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	credential := &database.TailscaleClient{ClientID: "saved-client", ClientSecret: "saved-secret"}
	if _, err := db.NewInsert().Model(credential).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := database.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	db = database.DB()
	if err := Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertCurrent(t, db)
	if err := db.NewSelect().Model(&admin).WherePK().Scan(t.Context()); err != nil || admin.Passwd != changedPassword {
		t.Fatalf("restart changed the administrator: %v", err)
	}
	if err := db.NewSelect().Model(credential).WherePK().Scan(t.Context()); err != nil || credential.ClientSecret != "saved-secret" {
		t.Fatalf("restart changed stored credentials: %v", err)
	}
	count, err := db.NewSelect().Model((*database.User)(nil)).Count(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("users=%d, err=%v", count, err)
	}
}

func TestStartupUpgradesExistingDatabase(t *testing.T) {
	db := startupDB(t)
	previous := migrate.NewMigrations()
	for _, migration := range Migrations.Sorted()[:3] {
		previous.Add(migration)
	}
	migrator := migrate.NewMigrator(db, previous)
	if err := migrator.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	hour := &database.NetworkActivityHour{HourStart: 3600, RxBytes: "123", TxBytes: "456", ObservedSeconds: 3600}
	if _, err := db.NewInsert().Model(hour).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := Init(t.Context()); err != nil {
			t.Fatal(err)
		}
		assertCurrent(t, db)
		totals := database.NetworkActivityTotals{ID: 1}
		if err := db.NewSelect().Model(&totals).WherePK().Scan(t.Context()); err != nil || totals.TotalRxBytes != "123" || totals.TotalTxBytes != "456" {
			t.Fatalf("upgrade lost or double-counted saved traffic: %+v, err=%v", totals, err)
		}
	}
}

func TestStartupRetriesFailedMigration(t *testing.T) {
	db := startupDB(t)
	if _, err := db.NewCreateTable().Model((*database.User)(nil)).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER fail_seed BEFORE INSERT ON users BEGIN SELECT RAISE(ABORT, 'seed failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := Init(t.Context()); err == nil || !strings.Contains(err.Error(), "seed failed") {
		t.Fatalf("migration failure was not reported: %v", err)
	}
	count, err := db.NewSelect().Table("bun_migrations").Count(t.Context())
	if err != nil || count != 0 {
		t.Fatalf("failed migration was marked applied: count=%d, err=%v", count, err)
	}
	if _, err := db.ExecContext(t.Context(), `DROP TRIGGER fail_seed`); err != nil {
		t.Fatal(err)
	}
	if err := Init(t.Context()); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	assertCurrent(t, db)
}

func TestStartupRetriesAfterMigrationRecordFailure(t *testing.T) {
	db := startupDB(t)
	if err := migrate.NewMigrator(db, Migrations).Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER fail_record BEFORE INSERT ON bun_migrations BEGIN SELECT RAISE(ABORT, 'record failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := Init(t.Context()); err == nil || !strings.Contains(err.Error(), "record failed") {
		t.Fatalf("record failure was not reported: %v", err)
	}
	// Simulate the completed seed surviving a failure to record its migration.
	// Re-running it must preserve an existing account instead of failing on its
	// unique username or resetting its password to the default.
	if _, err := db.NewCreateTable().Model((*database.User)(nil)).IfNotExists().Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	admin := &database.User{Username: "admin", Passwd: "keep-this-password", Role: "admin"}
	if _, err := db.NewInsert().Model(admin).On("CONFLICT (username) DO UPDATE").Set("passwd = EXCLUDED.passwd").Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `DROP TRIGGER fail_record`); err != nil {
		t.Fatal(err)
	}
	if err := Init(t.Context()); err != nil {
		t.Fatalf("retry after completed seed failed: %v", err)
	}
	if err := db.NewSelect().Model(admin).Where("username = ?", "admin").Scan(t.Context()); err != nil || admin.Passwd != "keep-this-password" {
		t.Fatalf("retry replaced the administrator password: %v", err)
	}
	assertCurrent(t, db)
}

func TestStartupRejectsConcurrentMigrator(t *testing.T) {
	db := startupDB(t)
	lock, err := os.OpenFile(conf.DatabasePath(), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if err := Init(t.Context()); err == nil {
		t.Fatal("startup ran migrations while another process held the migration lock")
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Init(t.Context()); err != nil {
		t.Fatalf("startup failed after the other migrator released its lock: %v", err)
	}
	assertCurrent(t, db)
}

func TestStartupCancelledThenRetried(t *testing.T) {
	db := startupDB(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Init(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled startup returned %v", err)
	}
	if err := Init(t.Context()); err != nil {
		t.Fatalf("retry after cancellation failed: %v", err)
	}
	assertCurrent(t, db)
}
