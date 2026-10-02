package upgrade

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type installTransaction struct {
	Format int `json:"format"`
	Installation
	RootDir         string           `json:"rootDir"`
	StateDir        string           `json:"stateDir"`
	ServicePath     string           `json:"servicePath"`
	NginxPath       string           `json:"nginxPath"`
	PreviousRelease string           `json:"previousRelease"`
	NextRelease     string           `json:"nextRelease"`
	PackageSHA256   string           `json:"packageSha256"`
	BootID          string           `json:"bootID"`
	InitiatorPID    int              `json:"initiatorPID"`
	Deadline        time.Time        `json:"deadline"`
	Plan            *IntegrationPlan `json:"integrations"`
	ReadyPID        int              `json:"readyPID,omitempty"`
	ReadySince      time.Time        `json:"readySince,omitempty"`
}

type installReadiness struct {
	ID         string `json:"id"`
	PID        int    `json:"pid"`
	Executable string `json:"executable"`
	BootID     string `json:"bootID"`
}

func releaseWithinRoot(root, release string) bool {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || !filepath.IsAbs(release) || filepath.Clean(release) != release {
		return false
	}
	relative, err := filepath.Rel(filepath.Join(root, "releases"), release)
	return err == nil && relative != "." && relative != ".." && !strings.Contains(relative, string(os.PathSeparator)) && relative != ""
}

func lockInstallState(directory string, create bool) (*os.File, error) {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return nil, errors.New("invalid upgrade state directory")
	}
	if create {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return nil, err
		}
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !trustedInstallDirectory(info, true) {
		return nil, errors.New("upgrade state directory must be a private regular directory")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.OpenFile(".installation.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, errors.New("invalid installation lock file")
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("%w: installation state locked", ErrInstallPending)
	}
	return file, nil
}

func waitInstallState(ctx context.Context, directory string) (*os.File, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := lockInstallState(directory, false)
		if !errors.Is(err, ErrInstallPending) {
			return file, err
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func readInstallJSON(directory, name string, value any) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return errors.New("invalid upgrade journal file")
	}
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		return err
	}
	if len(data) > 64<<10 {
		return errors.New("upgrade journal is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("invalid upgrade journal suffix")
	}
	return nil
}

func writeInstallJSON(directory, name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeInstallFile(filepath.Join(directory, name), append(data, '\n'), 0600)
}

func writeInstallFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".upgrade-write-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return errors.New("refusing to replace nonregular upgrade file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	return syncInstallDir(dir)
}

func syncInstallDir(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func atomicReleaseLink(root, name, target string) error {
	if (name != "current" && name != "previous") || !releaseWithinRoot(root, target) {
		return errors.New("invalid release link")
	}
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("release is not a regular directory")
	}
	dest := filepath.Join(root, name)
	if info, err := os.Lstat(dest); err == nil && info.Mode()&os.ModeSymlink == 0 {
		return errors.New("refusing to replace a nonsymlink release pointer")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(root, ".upgrade-link-")
	if err != nil {
		return err
	}
	temp := file.Name()
	file.Close()
	os.Remove(temp)
	defer os.Remove(temp)
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if err := os.Symlink(relative, temp); err != nil {
		return err
	}
	if err := os.Rename(temp, dest); err != nil {
		return err
	}
	return syncInstallDir(root)
}

func loadTransaction(directory string) (*installTransaction, error) {
	var tx installTransaction
	if err := readInstallJSON(directory, "pending.json", &tx); err != nil {
		return nil, err
	}
	if tx.Format != 1 || tx.ID == "" || !ValidVersion(tx.Version) || tx.StateDir != directory || !releaseWithinRoot(tx.RootDir, tx.PreviousRelease) || !releaseWithinRoot(tx.RootDir, tx.NextRelease) || tx.PreviousRelease == tx.NextRelease || tx.Plan == nil || tx.Plan.PreviousRelease != tx.PreviousRelease || tx.Plan.NextRelease != tx.NextRelease || tx.Deadline.IsZero() || tx.BootID == "" {
		return nil, errors.New("invalid upgrade transaction")
	}
	for _, path := range []string{tx.RootDir, tx.StateDir, tx.ServicePath, tx.NginxPath} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\n\r\x00%") {
			return nil, errors.New("invalid upgrade transaction path")
		}
	}
	switch tx.Phase {
	case "prepared", "activating", "awaiting-ready", "rolling-back", "rollback-failed":
	default:
		return nil, errors.New("invalid upgrade transaction phase")
	}
	return &tx, nil
}

func finishInstallation(tx *installTransaction) error {
	now := time.Now().UTC()
	tx.CompletedAt = &now
	if err := writeInstallJSON(tx.StateDir, "last-installation.json", &tx.Installation); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(tx.StateDir, "pending.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_ = os.Remove(filepath.Join(tx.StateDir, "ready.json"))
	return syncInstallDir(tx.StateDir)
}

func (i *Installer) installRecoveryUnit(ctx context.Context, tx *installTransaction) error {
	// The unit is dormant without pending.json. Its executable is pinned to the
	// old release and runs without initializing the database or normal services.
	unitPath := filepath.Join(filepath.Dir(tx.ServicePath), recoveryUnit)
	contents := []byte("[Unit]\nDescription=Nanotail portal upgrade recovery\nConditionPathExists=" + strconv.Quote(filepath.Join(tx.StateDir, "pending.json")) + "\nStartLimitIntervalSec=0\n\n[Service]\nType=simple\nExecStart=" + strconv.Quote(filepath.Join(tx.PreviousRelease, "nanotail-portal")) + " --upgrade-recover\nRestart=on-failure\nRestartSec=2\nTimeoutStopSec=35\n\n[Install]\nWantedBy=multi-user.target\n")
	old, _, err := readInstalledIntegration(unitPath)
	if err != nil {
		return err
	}
	if !bytes.Equal(old, contents) {
		if err := writeInstallFile(unitPath, contents, 0644); err != nil {
			return err
		}
		if _, err := i.options.Run(ctx, "systemctl", "daemon-reload"); err != nil {
			return err
		}
	}
	wants := filepath.Join(filepath.Dir(tx.ServicePath), "multi-user.target.wants")
	if err := os.MkdirAll(wants, 0755); err != nil {
		return err
	}
	link := filepath.Join(wants, recoveryUnit)
	target := "../" + recoveryUnit
	if existing, err := os.Readlink(link); err == nil {
		if existing != target {
			return errors.New("recovery unit enable link is unexpected")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.Symlink(target, link); err != nil {
			return err
		}
	} else {
		return err
	}
	return syncInstallDir(wants)
}
