package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/pancpp/fairnet-portal/app"
	"github.com/pancpp/fairnet-portal/conf"
	"github.com/pancpp/fairnet-portal/logger"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// config
	if err := conf.Init(ctx); err != nil {
		log.Fatal(err)
	}

	// logger
	if err := logger.Init(); err != nil {
		log.Fatal(err)
	}

	version, buildTime, gitHash, buildNumber := conf.GetVersion()
	log.Println("Hello, fainet portal!")
	log.Println("###############################################")
	log.Println("Version:", version)
	log.Println("Githash:", gitHash)
	log.Println("BuildTime:", buildTime)
	log.Println("BuildNumber:", buildNumber)
	log.Println("###############################################")

	// app
	if err := app.Init(ctx); err != nil {
		log.Fatal(err)
	}

	// wait for keyboard interruption
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	<-sigChan

	// say goodbye
	log.Println("Goodbye, faient portal!")
}
