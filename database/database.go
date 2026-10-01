package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"github.com/pancpp/nanotail-portal/conf"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/sqliteshim"
)

var (
	gDB     *bun.DB
	gCtx    context.Context
	gCancel context.CancelFunc
)

func Init(ctx context.Context) error {
	dsn := fmt.Sprintf("file:%s?cache=shared&mode=rwc", conf.DatabasePath())
	sqldb, err := sql.Open(sqliteshim.ShimName, dsn)
	if err != nil {
		log.Println("(database) open sqlite failed, err:", err)
		return err
	}

	// Create Bun database instance
	db := bun.NewDB(sqldb, sqlitedialect.New())

	// Add query debugging (optional)
	// db.AddQueryHook(bundebug.NewQueryHook(bundebug.WithVerbose(true)))

	// Test connection
	if err := db.PingContext(ctx); err != nil {
		log.Println("(database) test connection failed, err:", err)
		return err
	}
	log.Println("SQLite connected")

	gDB = db
	gCtx, gCancel = context.WithCancel(ctx)

	return nil
}

func Close() error {
	if gCancel != nil {
		gCancel()
	}
	if gDB != nil {
		return gDB.Close()
	}
	return nil
}

func DB() *bun.DB {
	return gDB
}

func Context() context.Context {
	return gCtx
}
