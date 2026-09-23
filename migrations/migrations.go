package migrations

import (
	"fmt"

	"github.com/pancpp/fairnet-portal/conf"
	"github.com/pancpp/fairnet-portal/database"
	"github.com/uptrace/bun/migrate"
)

var (
	Migrations = migrate.NewMigrations()
)

func Migrate(cmd int, arg string) error {
	db := database.DB()
	ctx := database.Context()
	migrator := migrate.NewMigrator(db, Migrations)
	if migrator == nil {
		return ErrInvalidMigrator
	}

	switch cmd {
	case conf.DB_INIT:
		err := migrator.Init(ctx)
		if err != nil {
			return err
		}
		fmt.Println("db migration initialized successfully")

	case conf.DB_MIGRATE:
		group, err := migrator.Migrate(ctx)
		if err != nil {
			return err
		}

		if group.IsZero() {
			fmt.Println("no migrations to run (database is up to date)")
		} else {
			fmt.Printf("migrated to %s\n", group)
		}

	case conf.DB_STATUS:
		ms, err := migrator.MigrationsWithStatus(ctx)
		if err != nil {
			return err
		}

		fmt.Printf("migrations: %s\n", ms)
		fmt.Printf("unapplied migrations: %s\n", ms.Unapplied())
		fmt.Printf("last migration group: %s\n", ms.LastGroup())

	case conf.DB_CREATE:
		mf, err := migrator.CreateGoMigration(ctx, arg)
		if err != nil {
			return err
		}
		fmt.Printf("created migration %s (%s)\n", mf.Name, mf.Path)
	}

	return nil
}
