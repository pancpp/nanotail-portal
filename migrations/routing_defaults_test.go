package migrations

import (
	"github.com/pancpp/nanotail-portal/database"
	"testing"
)

func TestSubnetDefaultsMarkerMigrationAndRestart(t *testing.T) {
	db := startupDB(t)
	if err := Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := database.NewRoutingDefaultsStore(db)
	if done, err := store.Configured(t.Context()); err != nil || done {
		t.Fatalf("fresh defaults=%v err=%v", done, err)
	}
	for range 2 {
		if err := store.MarkConfigured(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := database.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	store = database.NewRoutingDefaultsStore(database.DB())
	if done, err := store.Configured(t.Context()); err != nil || !done {
		t.Fatalf("saved choice lost on restart: %v %v", done, err)
	}
	count, err := database.DB().NewSelect().Model((*database.RoutingDefaults)(nil)).Count(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("markers=%d err=%v", count, err)
	}
}
