package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/pancpp/nanotail-portal/app"
	"github.com/pancpp/nanotail-portal/conf"
	"github.com/pancpp/nanotail-portal/database"
	"github.com/pancpp/nanotail-portal/logger"
	"github.com/pancpp/nanotail-portal/migrations"
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

	log.Println("Hello, nanotail portal!")
	defer log.Println("Goodbye, nanotail portal!")

	// database
	if err := database.Init(ctx); err != nil {
		log.Fatal(err)
	}

	// db migrations
	if err := migrations.Init(ctx); err != nil {
		log.Fatal(err)
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
