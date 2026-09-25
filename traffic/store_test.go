package traffic

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"github.com/pancpp/nanotail-portal/database"
	"github.com/pancpp/nanotail-portal/migrations"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/sqliteshim"
	"github.com/uptrace/bun/migrate"
)

func testDB(t *testing.T, path, mode string) *bun.DB {
	t.Helper()
	sqlDB, err := sql.Open(sqliteshim.ShimName, "file:"+path+"?mode="+mode)
	if err != nil {
		t.Fatal(err)
	}
	db := bun.NewDB(sqlDB, sqlitedialect.New())
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func createTotals(t *testing.T, db *bun.DB) {
	t.Helper()
	if _, err := db.NewCreateTable().Model((*database.NetworkActivityTotals)(nil)).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	totals := database.EmptyNetworkActivityTotals()
	if _, err := db.NewInsert().Model(&totals).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryMigrationRetentionAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite3")
	db := testDB(t, path, "rwc")
	migrator := migrate.NewMigrator(db, migrations.Migrations)
	if err := migrator.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	rows := []database.NetworkActivityHour{}
	for hour := 30; hour > 0; hour-- {
		rows = append(rows, database.NetworkActivityHour{HourStart: baseHour.Add(-time.Duration(hour) * time.Hour).Unix(), RxBytes: "36893488147419103230", TxBytes: "17", ObservedSeconds: 3600})
	}
	if err := store.Save(t.Context(), rows, baseHour); err != nil {
		t.Fatal(err)
	}
	count, err := db.NewSelect().Model((*database.NetworkActivityHour)(nil)).Count(t.Context())
	if err != nil || count != 24 {
		t.Fatalf("retention count %d: %v", count, err)
	}
	// Duplicate writes cannot overwrite a completed hour after a restart/rewind.
	duplicate := rows[len(rows)-1]
	duplicate.RxBytes = "999"
	if err := store.Save(t.Context(), []database.NetworkActivityHour{duplicate}, baseHour); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// Reading persisted history works from a new, read-only connection.
	reopened := NewStore(testDB(t, path, "ro"))
	history, err := reopened.History(t.Context(), baseHour.Add(42*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !history.WindowEnd.Equal(baseHour) || !history.WindowStart.Equal(baseHour.Add(-24*time.Hour)) || len(history.Hours) != 24 {
		t.Fatalf("history %+v", history)
	}
	for i, hour := range history.Hours {
		if hour.HourStart != baseHour.Add(time.Duration(i-24)*time.Hour).Unix() || hour.RxBytes != "36893488147419103230" || hour.TxBytes != "17" {
			t.Fatalf("corrupt persisted hour %+v", hour)
		}
	}
	perHour, _ := new(big.Int).SetString("36893488147419103230", 10)
	wantTotal := new(big.Int).Mul(perHour, big.NewInt(30)).String()
	if history.Totals.TotalRxBytes != wantTotal || history.Totals.TotalTxBytes != "510" || history.Totals.TotalObservedSeconds != 30*3600 {
		t.Fatalf("all-time totals lost pruned hours or counted duplicates: %+v", history.Totals)
	}
	// Reads never recompute or write the cached 24-hour snapshot. Its explicit
	// window remains anchored to the last successful save during downtime.
	history, err = reopened.History(t.Context(), baseHour.Add(48*time.Hour))
	if err != nil || len(history.Hours) != 24 || !history.WindowEnd.Equal(baseHour) || history.Totals.TotalRxBytes != wantTotal {
		t.Fatalf("stale history %+v %v", history, err)
	}
}

func TestHistorySaveIsAtomic(t *testing.T) {
	db := testDB(t, filepath.Join(t.TempDir(), "atomic.sqlite3"), "rwc")
	if _, err := db.NewCreateTable().Model((*database.NetworkActivityHour)(nil)).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	createTotals(t, db)
	old := []database.NetworkActivityHour{{HourStart: baseHour.Add(-25 * time.Hour).Unix(), RxBytes: "1", TxBytes: "2"}}
	if _, err := db.NewInsert().Model(&old).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER reject_prune BEFORE DELETE ON network_activity_hours BEGIN SELECT RAISE(ABORT, 'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	fresh := []database.NetworkActivityHour{{HourStart: baseHour.Add(-time.Hour).Unix(), RxBytes: "3", TxBytes: "4", ObservedSeconds: 120}}
	if err := store.Save(t.Context(), fresh, baseHour); err == nil {
		t.Fatal("expected transaction failure")
	}
	count, err := db.NewSelect().Model((*database.NetworkActivityHour)(nil)).Where("hour_start = ?", fresh[0].HourStart).Count(t.Context())
	if err != nil || count != 0 {
		t.Fatalf("partial transaction: %d %v", count, err)
	}
	totals := database.NetworkActivityTotals{ID: 1}
	if err := db.NewSelect().Model(&totals).WherePK().Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if totals.WindowEnd != 0 || totals.TotalRxBytes != "0" || totals.RxBytes24h != "0" {
		t.Fatal("failed transaction advanced totals")
	}
}

func TestHistoryMissingMigrationAndCancelledReads(t *testing.T) {
	db := testDB(t, filepath.Join(t.TempDir(), "missing.sqlite3"), "rwc")
	for _, store := range []*Store{NewStore(db), NewStore(nil)} {
		if _, err := store.History(t.Context(), baseHour); err == nil {
			t.Fatal("expected missing database/schema error")
		}
		if err := store.Save(t.Context(), nil, baseHour); err == nil {
			t.Fatal("expected missing database/schema error")
		}
	}
	if _, err := db.NewCreateTable().Model((*database.NetworkActivityHour)(nil)).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewStore(db).History(ctx, baseHour); !errors.Is(err, context.Canceled) {
		t.Fatalf("read cancellation lost: %v", err)
	}
	if err := NewStore(db).Save(ctx, nil, baseHour); !errors.Is(err, context.Canceled) {
		t.Fatalf("write cancellation lost: %v", err)
	}
}

func TestRecorderDatabaseIntegrationAcrossRestart(t *testing.T) {
	db := testDB(t, filepath.Join(t.TempDir(), "recorder.sqlite3"), "rwc")
	if _, err := db.NewCreateTable().Model((*database.NetworkActivityHour)(nil)).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	createTotals(t, db)
	r := NewRecorder(nil, store)
	for minute := 0; minute <= 60; minute++ {
		record(t, r, baseHour.Add(time.Duration(minute)*time.Minute), uint64(minute*100), uint64(minute*25), "epoch")
	}
	// A new process keeps completed history, but establishes a fresh baseline
	// rather than charging the interface's lifetime total to the new hour.
	r = NewRecorder(nil, store)
	record(t, r, baseHour.Add(90*time.Minute), 1000000, 1000000, "epoch")
	record(t, r, baseHour.Add(91*time.Minute), 1000100, 1000025, "epoch")
	record(t, r, baseHour.Add(120*time.Minute), 1009999, 1009999, "epoch")
	history, err := store.History(t.Context(), baseHour.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Hours) != 2 || history.Hours[0].RxBytes != "6000" || history.Hours[0].ObservedSeconds != 3600 ||
		history.Hours[1].RxBytes != "100" || history.Hours[1].TxBytes != "25" || history.Hours[1].ObservedSeconds != 60 {
		t.Fatalf("restart lost or invented hourly history: %+v", history)
	}
	if history.Totals.TotalRxBytes != "6100" || history.Totals.TotalTxBytes != "1525" || history.Totals.RxBytes24h != "6100" {
		t.Fatalf("restart lost total traffic: %+v", history.Totals)
	}
}

func TestHourlyTotalsSurvivePruningAndRejectReplayedHours(t *testing.T) {
	db := testDB(t, filepath.Join(t.TempDir(), "totals.sqlite3"), "rwc")
	if _, err := db.NewCreateTable().Model((*database.NetworkActivityHour)(nil)).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	createTotals(t, db)
	store := NewStore(db)
	for i := 0; i < 48; i++ {
		at := baseHour.Add(time.Duration(i) * time.Hour)
		row := database.NetworkActivityHour{HourStart: at.Unix(), RxBytes: "100", TxBytes: "50", ObservedSeconds: 3600}
		if err := store.Save(t.Context(), []database.NetworkActivityHour{row, row}, at.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	history, err := store.History(t.Context(), baseHour.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Hours) != 24 || history.Totals.RxBytes24h != "2400" || history.Totals.TxBytes24h != "1200" ||
		history.Totals.TotalRxBytes != "4800" || history.Totals.TotalTxBytes != "2400" || history.Totals.TotalObservedSeconds != 48*3600 ||
		history.Totals.RecordedSince == nil || *history.Totals.RecordedSince != baseHour.Unix() {
		t.Fatalf("bad hourly totals: %+v", history.Totals)
	}
	replay := database.NetworkActivityHour{HourStart: baseHour.Unix(), RxBytes: "999999", TxBytes: "999999", ObservedSeconds: 3600}
	if err := store.Save(t.Context(), []database.NetworkActivityHour{replay}, baseHour.Add(49*time.Hour)); err != nil {
		t.Fatal(err)
	}
	history, err = store.History(t.Context(), baseHour.Add(49*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if history.Totals.TotalRxBytes != "4800" || history.Totals.RxBytes24h != "2300" {
		t.Fatalf("replayed pruned hour: %+v", history.Totals)
	}
	if err := store.Save(t.Context(), []database.NetworkActivityHour{replay}, baseHour.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(t.Context(), nil, baseHour.Add(80*time.Hour)); err != nil {
		t.Fatal(err)
	}
	history, err = store.History(t.Context(), baseHour.Add(80*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Hours) != 0 || history.Totals.RxBytes24h != "0" || history.Totals.ObservedSeconds24h != 0 || history.Totals.TotalRxBytes != "4800" {
		t.Fatalf("aged totals: %+v", history)
	}
}

func TestTotalsMigrationSeedsExistingHoursOnce(t *testing.T) {
	db := testDB(t, filepath.Join(t.TempDir(), "upgrade.sqlite3"), "rwc")
	if _, err := db.NewCreateTable().Model((*database.NetworkActivityHour)(nil)).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	hours := []database.NetworkActivityHour{
		{HourStart: baseHour.Add(-30 * time.Hour).Unix(), RxBytes: "5", TxBytes: "7", ObservedSeconds: 120},
		{HourStart: baseHour.Add(-time.Hour).Unix(), RxBytes: "11", TxBytes: "13", ObservedSeconds: 3600},
	}
	if _, err := db.NewInsert().Model(&hours).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	migrator := migrate.NewMigrator(db, migrations.Migrations)
	if err := migrator.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	history, err := store.History(t.Context(), baseHour)
	if err != nil {
		t.Fatal(err)
	}
	if history.Totals.TotalRxBytes != "16" || history.Totals.TotalTxBytes != "20" || history.Totals.RxBytes24h != "11" || history.Totals.TxBytes24h != "13" {
		t.Fatalf("migration did not preserve history: %+v", history.Totals)
	}
	newHour := database.NetworkActivityHour{HourStart: baseHour.Unix(), RxBytes: "17", TxBytes: "19", ObservedSeconds: 60}
	if err := store.Save(t.Context(), []database.NetworkActivityHour{newHour}, baseHour.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	history, err = store.History(t.Context(), baseHour.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if history.Totals.TotalRxBytes != "33" || history.Totals.RxBytes24h != "28" {
		t.Fatalf("migration repeated accounting: %+v", history.Totals)
	}
}

func TestInvalidHourDoesNotAdvanceTotals(t *testing.T) {
	db := testDB(t, filepath.Join(t.TempDir(), "invalid.sqlite3"), "rwc")
	if _, err := db.NewCreateTable().Model((*database.NetworkActivityHour)(nil)).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	createTotals(t, db)
	for _, row := range []database.NetworkActivityHour{
		{HourStart: baseHour.Unix(), RxBytes: "-1", TxBytes: "0", ObservedSeconds: 1},
		{HourStart: baseHour.Unix(), RxBytes: "1", TxBytes: "1", ObservedSeconds: 0},
		{HourStart: baseHour.Unix(), RxBytes: "1", TxBytes: "1", ObservedSeconds: 3601},
		{HourStart: baseHour.Unix() + 1, RxBytes: "1", TxBytes: "1", ObservedSeconds: 1},
	} {
		if err := NewStore(db).Save(t.Context(), []database.NetworkActivityHour{row}, baseHour.Add(time.Hour)); err == nil {
			t.Fatalf("accepted %+v", row)
		}
	}
	history, err := NewStore(db).History(t.Context(), baseHour)
	if err != nil {
		t.Fatal(err)
	}
	if history.Totals.WindowEnd != 0 || history.Totals.TotalRxBytes != "0" || len(history.Hours) != 0 {
		t.Fatal("invalid hours changed saved data")
	}
}
