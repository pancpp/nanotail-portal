package migrations

import (
	"context"

	"github.com/pancpp/nanotail-portal/database"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			if _, err := tx.NewCreateTable().Model((*database.NetworkActivityTotals)(nil)).IfNotExists().Exec(ctx); err != nil {
				return err
			}
			hours := []database.NetworkActivityHour{}
			if err := tx.NewSelect().Model(&hours).Order("hour_start ASC").Scan(ctx); err != nil {
				return err
			}
			totals, err := database.SeedNetworkActivityTotals(hours)
			if err != nil {
				return err
			}
			_, err = tx.NewInsert().Model(&totals).On("CONFLICT (id) DO NOTHING").Exec(ctx)
			return err
		})
	}, func(context.Context, *bun.DB) error { return nil })
}
