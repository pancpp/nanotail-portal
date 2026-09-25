package migrations

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"syscall"

	"github.com/pancpp/nanotail-portal/database"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

var (
	Migrations = migrate.NewMigrations()
)

func Init(ctx context.Context) error {
	db := database.DB()
	if db == nil {
		return errors.New("database must be opened before running migrations")
	}
	lock, err := lockDatabase(ctx, db)
	if err != nil {
		return fmt.Errorf("lock database migrations: %w", err)
	}
	defer lock.Close()

	// Failed migrations must remain pending so the next startup can retry.
	migrator := migrate.NewMigrator(db, Migrations, migrate.WithMarkAppliedOnSuccess(true))
	// Init only creates missing migration bookkeeping tables.
	if err := migrator.Init(ctx); err != nil {
		return fmt.Errorf("initialize migration metadata: %w", err)
	}

	group, err := migrator.Migrate(ctx)
	if err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	if group.IsZero() {
		log.Println("(migrations) no migrations to run (database is up to date)")
	} else {
		log.Println("(migrations) migrated to", group)
	}

	return nil
}

// The portal uses a local, file-backed SQLite database on Linux. Lock its actual
// file so separate portal processes cannot both select and apply pending work.
// Unlike a persistent database lock row, flock is released by the OS on crash
// or exit; automatic startup never needs a removed CLI command to unlock it.
func lockDatabase(ctx context.Context, db *bun.DB) (*os.File, error) {
	var path string
	if err := db.NewRaw("SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(ctx, &path); err != nil {
		return nil, err
	}
	if path == "" {
		return nil, errors.New("automatic migrations require a file-backed SQLite database")
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.New("another portal process is applying database migrations")
		}
		return nil, err
	}
	return file, nil
}
