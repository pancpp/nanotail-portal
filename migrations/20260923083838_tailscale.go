package migrations

import (
	"context"
	"fmt"

	"github.com/pancpp/nanotail-portal/database"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		fmt.Print(" [up migration] ")
		if _, err := db.NewCreateTable().Model((*database.TailscaleClient)(nil)).IfNotExists().Exec(ctx); err != nil {
			return err
		}
		return nil
	}, func(ctx context.Context, db *bun.DB) error {
		fmt.Print(" [do not support roll back for safety purpose] ")
		return nil
	})
}
