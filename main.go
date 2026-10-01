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
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer cancel()
	if err := conf.PrepareDataDir(); err != nil {
		return err
	}
	files, err := factoryreset.Open(conf.DataDir())
	if err != nil {
		return err
	}
	defer files.Close()
	lock, err := files.LockInstance()
	if err != nil {
		return err
	}
	defer lock.Close()
	// config
	if err := conf.Init(); err != nil {
		return err
	}

	// logger
	if err := logger.Init(); err != nil {
		return err
	}
	defer logger.Close()
	// No log writes occur before pending cleanup: the lazy file writer has not
	// opened a file or started compression. Reload defaults after clearing config.
	resumed, err := files.Resume()
	if err != nil {
		return fmt.Errorf("factory reset recovery: %w", err)
	}
	if resumed {
		if err := logger.Close(); err != nil {
			return err
		}
		if err := conf.Init(); err != nil {
			return err
		}
		if err := logger.Init(); err != nil {
			return err
		}
		log.Print("[factory-reset] data cleanup complete; initializing fresh portal")
	}

	log.Println("Hello, nanotail portal!")

	executable, err := os.Executable()
	if err != nil {
		return err
	}
	reset := factoryreset.NewController(func() error {
		if err := files.ValidatePaths(conf.ConfigFile(), conf.DatabasePath(), conf.LogDir()); err != nil {
			return err
		}
		info, err := os.Stat(executable)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
			return fmt.Errorf("portal executable is not available for restart")
		}
		logs, err := filepath.Abs(conf.LogDir())
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
	if ctx.Err() != nil {
		return nil
	}

	if err := database.Init(ctx); err != nil {
		return err
	}
	defer database.Close()
	if err := migrations.Init(ctx); err != nil {
		return err
	}
	// Reset recovery has already deleted the old key, if requested. Load (or
	// durably generate) the signing key before constructing JWT middleware.
	if err := auth.Init(); err != nil {
		return err
	}
	for {
		runtime, err := app.Start(ctx, reset)
		if err != nil {
			return err
		}
		// Startup, migrations and HTTP listener are ready: reset is complete.
		log.Printf("[factory-reset] portal ready; no reset in progress")
		resetRequested := false
		select {
		case <-ctx.Done():
		case err = <-runtime.Errors():
		case <-reset.Requests():
			resetRequested = true
			log.Printf("[factory-reset] starting reset")
		}
		if resetRequested {
			log.Printf("[factory-reset] stopping portal services and draining requests")
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
		log.Printf("[factory-reset] portal services stopped; logging out of Tailscale")
		// Logout first: a daemon/permission failure must not erase local data.
		if err := client.Logout(context.Background()); err != nil {
			log.Printf("[factory-reset] aborted before clearing any files: %v", err)
			reset.RetryAllowed()
			continue
		}
		return restartForReset(files, executable)
	}
}

func restartForReset(files *factoryreset.Files, executable string) error {
	log.Print("[factory-reset] Tailscale logout complete; preparing data cleanup")
	if err := database.Close(); err != nil {
		return err
	}
	if err := files.Prepare(); err != nil {
		return fmt.Errorf("prepare factory reset: %w", err)
	}
	log.Print("[factory-reset] reset intent saved; restarting process for data cleanup")
	if err := logger.Close(); err != nil {
		return err
	}
	// Re-exec eliminates background logger compression workers before cleanup.
	return syscall.Exec(executable, os.Args, os.Environ())
}
