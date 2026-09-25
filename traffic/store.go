// Package traffic collects hourly VPN traffic independently of browser polling.
package traffic

import (
	"context"
	"errors"
	"time"

	"github.com/pancpp/nanotail-portal/database"
	"github.com/uptrace/bun"
)

type History struct {
	WindowStart, WindowEnd time.Time
	Hours                  []database.NetworkActivityHour
	Totals                 database.NetworkActivityTotals
}

type Store struct{ db *bun.DB }

func NewStore(db *bun.DB) *Store { return &Store{db: db} }

// Save writes and prunes together, once at each hourly boundary. A primary key
// prevents duplicate buckets across retries or a backward wall-clock change.
func (s *Store) Save(ctx context.Context, hours []database.NetworkActivityHour, end time.Time) error {
	if s.db == nil {
		return errors.New("network activity database unavailable")
	}
	endUnix := end.UTC().Truncate(time.Hour).Unix()
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Take SQLite's writer lock before the read/modify/write sequence. A
		// competing process either waits or fails atomically, never loses totals.
		locked, err := tx.NewUpdate().Model((*database.NetworkActivityTotals)(nil)).Set("id = id").Where("id = 1").Exec(ctx)
		if err != nil {
			return err
		}
		if n, err := locked.RowsAffected(); err != nil || n != 1 {
			return errors.New("missing network activity totals; apply migrations")
		}
		totals := database.NetworkActivityTotals{ID: 1}
		if err := tx.NewSelect().Model(&totals).WherePK().Scan(ctx); err != nil {
			return err
		}
		if endUnix <= totals.WindowEnd {
			return nil
		}
		for _, hour := range hours {
			// The watermark also rejects replays after their rows were pruned.
			if hour.HourStart < totals.WindowEnd {
				continue
			}
			if hour.HourStart >= endUnix {
				return errors.New("cannot save an unfinished traffic hour")
			}
			result, err := tx.NewInsert().Model(&hour).On("CONFLICT (hour_start) DO NOTHING").Exec(ctx)
			if err != nil {
				return err
			}
			inserted, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if inserted == 1 {
				if err := totals.AddHour(hour); err != nil {
					return err
				}
			}
		}
		if _, err := tx.NewDelete().Model((*database.NetworkActivityHour)(nil)).Where("hour_start < ?", endUnix-86400).Exec(ctx); err != nil {
			return err
		}
		retained := []database.NetworkActivityHour{}
		if err := tx.NewSelect().Model(&retained).Where("hour_start >= ? AND hour_start < ?", endUnix-86400, endUnix).Order("hour_start ASC").Scan(ctx); err != nil {
			return err
		}
		if err := totals.SetWindow(retained, endUnix); err != nil {
			return err
		}
		_, err = tx.NewUpdate().Model(&totals).WherePK().Exec(ctx)
		return err
	})
}

func (s *Store) History(ctx context.Context, now time.Time) (History, error) {
	end := now.UTC().Truncate(time.Hour)
	result := History{WindowStart: end.Add(-24 * time.Hour), WindowEnd: end, Hours: []database.NetworkActivityHour{}}
	if s.db == nil {
		return History{}, errors.New("network activity database unavailable")
	}
	// Read one consistent saved snapshot, even if an hourly save/prune races
	// this request. The period stays anchored to that save until the next one.
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		result.Totals.ID = 1
		if err := tx.NewSelect().Model(&result.Totals).WherePK().Scan(ctx); err != nil {
			return err
		}
		if result.Totals.WindowEnd != 0 {
			result.WindowEnd = time.Unix(result.Totals.WindowEnd, 0).UTC()
			result.WindowStart = result.WindowEnd.Add(-24 * time.Hour)
		}
		return tx.NewSelect().Model(&result.Hours).
			Where("hour_start >= ? AND hour_start < ?", result.WindowStart.Unix(), result.WindowEnd.Unix()).
			Order("hour_start ASC").Limit(24).Scan(ctx)
	})
	return result, err
}
