package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/pancpp/fairnet-portal/app"
	"github.com/pancpp/fairnet-portal/conf"
	"github.com/pancpp/fairnet-portal/database"
	"github.com/pancpp/fairnet-portal/logger"
	"github.com/pancpp/fairnet-portal/migrations"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// config
	if err := conf.Init(); err != nil {
		log.Fatal(err)
	}

	// logger
	if err := logger.Init(); err != nil {
		log.Fatal(err)
	}

	log.Println("Hello, fainet portal!")
	defer log.Println("Goodbye, fairnet portal!")

	// database
	if err := database.Init(ctx); err != nil {
		log.Fatal(err)
	}

	// db migrations
	dbCmd, dbArg := conf.DbCmdArg()
	if dbCmd != conf.DB_UNKNOWN {
		if err := migrations.Migrate(dbCmd, dbArg); err != nil {
			log.Fatal(err)
		}
		return
	}

	// app
	if err := app.Init(ctx); err != nil {
		log.Fatal(err)
	}

	// wait for keyboard interruption
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	<-sigChan
}
