package upgrade

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func processExecutableMatches(pid int, expected string) bool {
	if pid <= 0 {
		return false
	}
	path, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	return err == nil && path == expected
}

// BeforeStartup runs before database initialization. An interrupted activation
// must be restored by the independent old executable before migrations run.
func (i *Installer) BeforeStartup(ctx context.Context) error {
	tx, err := loadTransaction(i.options.StateDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read upgrade transaction: %w", err)
	}
	i.options.Gate.TryAcquire("upgrade")
	if _, err := i.options.Run(ctx, "systemctl", "start", recoveryUnit); err != nil {
		return fmt.Errorf("start upgrade recovery: %w", err)
	}
	self, err := filepath.EvalSymlinks(i.options.Executable)
	if err != nil {
		return err
	}
	if tx.Phase != "awaiting-ready" || tx.BootID != i.options.BootID || self != filepath.Join(tx.NextRelease, "nanotail-portal") || !time.Now().Before(tx.Deadline) {
		return ErrInstallPending
	}
	i.mu.Lock()
	i.startupID = tx.ID
	i.mu.Unlock()
	return nil
}

// MarkReady identifies this exact process after migrations and HTTP startup.
// Writes stay blocked until the external monitor observes ten stable seconds.
func (i *Installer) MarkReady(ctx context.Context) error {
	i.mu.Lock()
	startupID := i.startupID
	i.mu.Unlock()
	// A new install can be accepted immediately after HTTP starts. Only confirm
	// a transaction observed before this process initialized its database.
	if startupID == "" {
		return nil
	}
	lockCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	lock, err := waitInstallState(lockCtx, i.options.StateDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	tx, err := loadTransaction(i.options.StateDir)
	if errors.Is(err, os.ErrNotExist) {
		var last Installation
		if readInstallJSON(i.options.StateDir, "last-installation.json", &last) == nil && last.ID == startupID && last.Phase == "complete" {
			i.options.Gate.Release("upgrade")
			return nil
		}
		return ErrInstallPending
	}
	if err != nil {
		return err
	}
	if tx.ID != startupID {
		return ErrInstallPending
	}
	self, err := filepath.EvalSymlinks(i.options.Executable)
	if err != nil {
		return err
	}
	if tx.Phase != "awaiting-ready" || tx.BootID != i.options.BootID || self != filepath.Join(tx.NextRelease, "nanotail-portal") {
		return ErrInstallPending
	}
	ready := installReadiness{ID: tx.ID, PID: os.Getpid(), Executable: self, BootID: i.options.BootID}
	if err := writeInstallJSON(i.options.StateDir, "ready.json", &ready); err != nil {
		return err
	}
	go func(id string) {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var last Installation
				if _, err := os.Lstat(filepath.Join(i.options.StateDir, "pending.json")); errors.Is(err, os.ErrNotExist) && readInstallJSON(i.options.StateDir, "last-installation.json", &last) == nil && last.ID == id && last.Phase == "complete" {
					i.options.Gate.Release("upgrade")
					return
				}
			}
		}
	}(tx.ID)
	return nil
}

// RunRecovery is the portal's short-lived --upgrade-recover mode. It never
// reads configuration from the data directory or opens/migrates a database.
func RunRecovery(ctx context.Context) error {
	return runRecovery(ctx, UPGRADE_DIR)
}

// runRecovery accepts an isolated directory for package tests. Production
// recovery always uses UPGRADE_DIR through RunRecovery.
func runRecovery(ctx context.Context, stateDir string) error {
	if !filepath.IsAbs(stateDir) || filepath.Clean(stateDir) != stateDir {
		return errors.New("upgrade recovery requires an absolute state directory")
	}
	tx, err := loadTransaction(stateDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	installer := NewInstaller(InstallerOptions{RootDir: tx.RootDir, StateDir: tx.StateDir, ServicePath: tx.ServicePath, NginxPath: tx.NginxPath, Executable: filepath.Join(tx.PreviousRelease, "nanotail-portal")}, nil, TARGET_OS, TARGET_ARCH)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		done, err := installer.recoverStep(ctx, time.Now().UTC())
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (i *Installer) recoverStep(ctx context.Context, now time.Time) (bool, error) {
	lock, err := lockInstallState(i.options.StateDir, false)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if errors.Is(err, ErrInstallPending) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer lock.Close()
	tx, err := loadTransaction(i.options.StateDir)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	switch tx.Phase {
	case "prepared":
		if tx.BootID == i.options.BootID && now.Before(tx.Deadline) && i.options.CheckProcess(tx.InitiatorPID, filepath.Join(tx.PreviousRelease, "nanotail-portal")) {
			return false, nil
		}
		// No activation has occurred, so neither configuration nor release pointers
		// need restoration. Restart the old service to clear any maintenance gate.
		if _, err := i.options.Run(ctx, "systemctl", "stop", portalUnit); err != nil {
			return false, err
		}
		if _, err := i.options.Run(ctx, "systemctl", "reset-failed", portalUnit); err != nil {
			return false, err
		}
		if _, err := i.options.Run(ctx, "systemctl", "start", "--no-block", portalUnit); err != nil {
			return false, err
		}
		tx.Phase = "aborted"
		tx.Error = "Installation was interrupted before activation. The installed release was not changed."
		return true, finishInstallation(tx)
	case "awaiting-ready":
		if tx.BootID == i.options.BootID && now.Before(tx.Deadline) {
			var ready installReadiness
			healthy := readInstallJSON(tx.StateDir, "ready.json", &ready) == nil && ready.ID == tx.ID && ready.BootID == tx.BootID && ready.Executable == filepath.Join(tx.NextRelease, "nanotail-portal") && i.options.CheckProcess(ready.PID, ready.Executable)
			if healthy {
				mainPID, err := i.options.Run(ctx, "systemctl", "show", portalUnit, "--property=MainPID", "--value")
				healthy = err == nil && strings.TrimSpace(string(mainPID)) == strconv.Itoa(ready.PID)
			}
			if healthy {
				if tx.ReadyPID != ready.PID || tx.ReadySince.IsZero() {
					tx.ReadyPID = ready.PID
					tx.ReadySince = now
					if err := writeInstallJSON(tx.StateDir, "pending.json", tx); err != nil {
						return false, err
					}
				}
				if now.Sub(tx.ReadySince) >= 10*time.Second {
					tx.Phase = "complete"
					tx.Error = ""
					return true, finishInstallation(tx)
				}
			} else if tx.ReadyPID != 0 || !tx.ReadySince.IsZero() {
				tx.ReadyPID = 0
				tx.ReadySince = time.Time{}
				if err := writeInstallJSON(tx.StateDir, "pending.json", tx); err != nil {
					return false, err
				}
			}
			return false, nil
		}
	}
	// Activating observed without its writer's lock means activation was
	// interrupted. Rolling-back/rollback-failed are resumed after helper restart.
	return i.rollback(ctx, tx)
}

func (i *Installer) rollback(ctx context.Context, tx *installTransaction) (bool, error) {
	tx.Phase = "rolling-back"
	tx.Error = "The new release did not become ready. Restoring the previous release."
	if err := writeInstallJSON(tx.StateDir, "pending.json", tx); err != nil {
		return false, err
	}
	fail := func(err error) (bool, error) {
		tx.Phase = "rollback-failed"
		tx.Error = "Automatic recovery could not finish. Check the upgrade recovery service logs."
		_ = writeInstallJSON(tx.StateDir, "pending.json", tx)
		return false, err
	}
	if _, err := i.options.Run(ctx, "systemctl", "stop", portalUnit); err != nil {
		return fail(err)
	}
	if err := atomicReleaseLink(tx.RootDir, "current", tx.PreviousRelease); err != nil {
		return fail(err)
	}
	if err := i.integrations().Restore(ctx, tx.Plan); err != nil {
		return fail(err)
	}
	// Keep pending until all restoration steps succeed. Database state is never
	// copied, restored, or migrated here; compatibility is the developer's contract.
	// Queue restart before removing pending.json. A helper crash after journal
	// removal must not strand an explicitly stopped portal. If the old process
	// starts before removal, BeforeStartup fails closed and Restart=always retries.
	if _, err := i.options.Run(ctx, "systemctl", "reset-failed", portalUnit); err != nil {
		return fail(err)
	}
	if _, err := i.options.Run(ctx, "systemctl", "start", "--no-block", portalUnit); err != nil {
		return fail(err)
	}
	tx.Phase = "rolled-back"
	tx.Error = "The new release failed to start reliably. The previous release was restored."
	if err := finishInstallation(tx); err != nil {
		return fail(err)
	}
	return true, nil
}
