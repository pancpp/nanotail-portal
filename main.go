package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pancpp/nanotail-portal/app"
	"github.com/pancpp/nanotail-portal/app/auth"
	"github.com/pancpp/nanotail-portal/conf"
	"github.com/pancpp/nanotail-portal/database"
	"github.com/pancpp/nanotail-portal/factoryreset"
	"github.com/pancpp/nanotail-portal/logger"
	"github.com/pancpp/nanotail-portal/migrations"
	"github.com/pancpp/nanotail-portal/tailscale"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer cancel()
	files, err := factoryreset.Open(".")
	if err != nil {
		return err
	}
	defer files.Close()
	lock, err := files.LockInstance()
	if err != nil {
		return err
	}
	defer lock.Close()
	// Complete an interrupted reset before opening config, database, or log files.
	if err := files.Resume(); err != nil {
		return fmt.Errorf("factory reset recovery: %w", err)
	}

	// config
	if err := conf.Init(); err != nil {
		return err
	}

	// logger
	if err := logger.Init(); err != nil {
		return err
	}
	defer logger.Close()

	log.Println("Hello, nanotail portal!")

	// database
	if err := database.Init(ctx); err != nil {
		return err
	}
	defer database.Close()

	// db migrations
	if err := migrations.Init(ctx); err != nil {
		return err
	}
	// Reset recovery has already deleted the old key, if requested. Load (or
	// durably generate) the signing key before constructing JWT middleware.
	if err := auth.Init(); err != nil {
		return err
	}

	executable, err := os.Executable()
	if err != nil {
		return err
	}
	reset := factoryreset.NewController(func() error {
		if err := files.ValidatePaths(conf.ConfigFile(), conf.GetString("database"), conf.GetString("log_dir")); err != nil {
			return err
		}
		info, err := os.Stat(executable)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
			return fmt.Errorf("portal executable is not available for restart")
		}
		logs, err := filepath.Abs(conf.GetString("log_dir"))
		if err != nil {
			return err
		}
		realExecutable, err := filepath.EvalSymlinks(executable)
		if err != nil {
			return err
		}
		realLogs, err := filepath.EvalSymlinks(logs)
		if err != nil {
			return err
		}
		if strings.HasPrefix(realExecutable, realLogs+string(os.PathSeparator)) {
			return fmt.Errorf("portal executable cannot be inside the logs folder")
		}
		return nil
	})
	timeout, err := time.ParseDuration(conf.GetString("tailscale_timeout"))
	if err != nil || timeout <= 0 {
		timeout = 15 * time.Second
	}
	client := tailscale.NewClient(conf.GetString("tailscale_binary"), conf.GetString("tailscale_socket"), timeout, nil)
	for {
		runtime, err := app.Start(ctx, reset)
		if err != nil {
			return err
		}
		resetRequested := false
		select {
		case <-ctx.Done():
		case err = <-runtime.Errors():
		case <-reset.Requests():
			resetRequested = true
		}
		shutdownCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		shutdownErr := runtime.Shutdown(shutdownCtx)
		stop()
		if shutdownErr != nil {
			return fmt.Errorf("stop portal before shutdown: %w", shutdownErr)
		}
		if !resetRequested {
			return err
		}
		// Logout first: a daemon/permission failure must not erase local data.
		if err := client.Logout(context.Background()); err != nil {
			log.Printf("(factory reset) aborted before clearing any files: %v", err)
			reset.RetryAllowed()
			continue
		}
		if err := database.Close(); err != nil {
			return err
		}
		if err := logger.Close(); err != nil {
			return err
		}
		if err := files.Prepare(); err != nil {
			return fmt.Errorf("prepare factory reset: %w", err)
		}
		// Re-exec also eliminates background logger compression workers. The
		// recovery path clears all state, then normal startup creates a fresh DB.
		return syscall.Exec(executable, os.Args, os.Environ())
	}
}
