package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/pancpp/nanotail-portal/app"
	"github.com/pancpp/nanotail-portal/conf"
	"github.com/pancpp/nanotail-portal/database"
	"github.com/pancpp/nanotail-portal/factoryreset"
	"github.com/pancpp/nanotail-portal/logger"
	"github.com/pancpp/nanotail-portal/maintenance"
	"github.com/pancpp/nanotail-portal/migrations"
	"github.com/pancpp/nanotail-portal/tailscale"
	"github.com/pancpp/nanotail-portal/upgrade"
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
	if conf.UpgradeRecovery() {
		return upgrade.RunRecovery(ctx)
	}
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
	gate := &maintenance.Gate{}
	keys, err := upgrade.TrustedKeys()
	if err != nil {
		return err
	}
	installer := upgrade.NewInstaller(upgrade.InstallerOptions{
		Executable: executable, Gate: gate,
	}, keys, runtime.GOOS, runtime.GOARCH)
	if err := installer.BeforeStartup(ctx); err != nil {
		return fmt.Errorf("upgrade startup recovery: %w", err)
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
	}, gate)
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
	if err := app.Init(ctx); err != nil {
		return err
	}
	for {
		runtime, err := app.Start(ctx, reset, installer)
		if err != nil {
			return err
		}
		readyErr := installer.MarkReady(ctx)
		if readyErr != nil {
			shutdownCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
			_ = runtime.Shutdown(shutdownCtx)
			stop()
			return fmt.Errorf("confirm upgraded portal startup: %w", readyErr)
		}
		// Startup, migrations and HTTP listener are ready: reset is complete.
		log.Printf("[factory-reset] portal ready; no reset in progress")
		resetRequested := false
		installRequested := false
		select {
		case <-ctx.Done():
		case err = <-runtime.Errors():
		case <-reset.Requests():
			resetRequested = true
			log.Printf("[factory-reset] starting reset")
		case <-installer.Requests():
			installRequested = true
			log.Print("[upgrade] draining portal requests and workers before activation")
		}
		if resetRequested {
			log.Printf("[factory-reset] stopping portal services and draining requests")
		}
		shutdownCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		shutdownErr := runtime.Shutdown(shutdownCtx)
		stop()
		if shutdownErr != nil {
			// Keep a prepared journal armed: the independent monitor can restart
			// the old release even if a stuck worker also prevents process exit.
			return fmt.Errorf("stop portal before shutdown: %w", shutdownErr)
		}
		if installRequested {
			if err := database.Close(); err != nil {
				_ = installer.Abort(context.Background())
				return fmt.Errorf("close database before upgrade: %w", err)
			}
			activateCtx, activateCancel := context.WithTimeout(context.Background(), 2*time.Minute)
			err := installer.Activate(activateCtx)
			activateCancel()
			if err != nil {
				return fmt.Errorf("activate upgrade; independent recovery will restore the prior release: %w", err)
			}
			log.Print("[upgrade] release activated; exiting for systemd restart")
			return nil
		}
		if !resetRequested {
			_ = installer.Abort(context.Background())
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
