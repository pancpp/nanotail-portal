package database

import (
	"context"
	"errors"

	"github.com/uptrace/bun"
)

// RoutingDefaults records that the initial subnet choice has been made.
// Route values remain in tailscaled. Factory reset clears this marker with SQLite.
type RoutingDefaults struct {
	bun.BaseModel `bun:"table:routing_defaults"`
	ID            int `bun:"id,pk"`
}

type RoutingDefaultsStore struct{ db *bun.DB }

func NewRoutingDefaultsStore(db *bun.DB) *RoutingDefaultsStore { return &RoutingDefaultsStore{db: db} }

func (s *RoutingDefaultsStore) Configured(ctx context.Context) (bool, error) {
	if s.db == nil {
		return false, errors.New("routing defaults database unavailable")
	}
	return s.db.NewSelect().Model((*RoutingDefaults)(nil)).Where("id = ?", 1).Exists(ctx)
}

func (s *RoutingDefaultsStore) MarkConfigured(ctx context.Context) error {
	if s.db == nil {
		return errors.New("routing defaults database unavailable")
	}
	_, err := s.db.NewInsert().Model(&RoutingDefaults{ID: 1}).On("CONFLICT (id) DO NOTHING").Exec(ctx)
	return err
}
