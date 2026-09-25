package migrations

import (
	"context"
	"github.com/pancpp/nanotail-portal/database"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		_, err := db.NewCreateTable().Model((*database.RoutingDefaults)(nil)).IfNotExists().Exec(ctx)
		return err
	}, func(context.Context, *bun.DB) error { return nil })
}
