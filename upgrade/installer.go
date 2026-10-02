package upgrade

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pancpp/nanotail-portal/maintenance"
)

const portalUnit = "nanotail-portal.service"
const recoveryUnit = "nanotail-portal-upgrade-recovery.service"

var (
	ErrInstallUnsupported = errors.New("installation requires an ARM64 Linux device managed by systemd")
	ErrInstallFailed      = errors.New("upgrade installation safety checks failed")
	ErrMaintenanceBusy    = errors.New("another maintenance operation is in progress")
	ErrInvalidSelection   = errors.New("select the exact verified package version and SHA-256")
	ErrNoStagedPackage    = errors.New("no verified package is staged")
	ErrInstallPending     = errors.New("an upgrade installation is pending")
)

type Installation struct {
	ID          string     `json:"id"`
	Version     string     `json:"version"`
	Phase       string     `json:"phase"`
	Error       string     `json:"error,omitempty"`
	StartedAt   time.Time  `json:"startedAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

// Overrides are Go test seams, never request or YAML-controlled commands.
type InstallerOptions struct {
	RootDir, StateDir, ServicePath, NginxPath, Executable string
	Gate                                                  *maintenance.Gate
	Run                                                   func(context.Context, string, ...string) ([]byte, error)
	ValidateHost                                          func(context.Context) error
	BootID                                                string
	CheckProcess                                          func(int, string) bool
}

type Installer struct {
	// Test seam for the publish-then-fsync failure boundary.
	publishPending       func(*installTransaction) error
	options              InstallerOptions
	keys                 []ed25519.PublicKey
	targetOS, targetArch string
	requests             chan struct{}
	mu                   sync.Mutex
	preparing            *Installation
	startupID            string
}

func NewInstaller(options InstallerOptions, keys []ed25519.PublicKey, targetOS, targetArch string) *Installer {
	if options.RootDir == "" {
		options.RootDir = INSTALL_ROOT
	}
	if options.StateDir == "" {
		options.StateDir = UPGRADE_DIR
	}
	if options.ServicePath == "" {
		options.ServicePath = "/etc/systemd/system/" + portalUnit
	}
	if options.NginxPath == "" {
		options.NginxPath = filepath.Join(options.RootDir, "nanotail-portal.nginx")
	}
	if options.Executable == "" {
		options.Executable, _ = os.Executable()
	}
	if options.Gate == nil {
		options.Gate = &maintenance.Gate{}
	}
	if options.Run == nil {
		options.Run = runUpgradeCommand
	}
	if options.CheckProcess == nil {
		options.CheckProcess = processExecutableMatches
	}
	if options.BootID == "" {
		data, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
		options.BootID = strings.TrimSpace(string(data))
	}
	i := &Installer{options: options, targetOS: targetOS, targetArch: targetArch, requests: make(chan struct{}, 1)}
	for _, key := range keys {
		i.keys = append(i.keys, append(ed25519.PublicKey(nil), key...))
	}
	return i
}

func runUpgradeCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w", name, err)
	}
	return data, nil
}

func (i *Installer) Pending() bool             { return i != nil && i.options.Gate.Pending() }
func (i *Installer) Requests() <-chan struct{} { return i.requests }
func (i *Installer) Schedule() {
	select {
	case i.requests <- struct{}{}:
	default:
	}
}
func (i *Installer) Supported() bool {
	if i == nil || !SupportedPlatform(i.targetOS, i.targetArch) {
		return false
	}
	if i.options.ValidateHost != nil {
		return true
	}
	if os.Geteuid() != 0 {
		return false
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return false
	}
	_, err := i.currentRelease()
	return err == nil
}

func (i *Installer) Status() *Installation {
	if i == nil {
		return nil
	}
	var tx installTransaction
	if err := readInstallJSON(i.options.StateDir, "pending.json", &tx); err == nil {
		result := tx.Installation
		return &result
	}
	i.mu.Lock()
	if i.preparing != nil {
		result := *i.preparing
		i.mu.Unlock()
		return &result
	}
	i.mu.Unlock()
	var result Installation
	if readInstallJSON(i.options.StateDir, "last-installation.json", &result) == nil {
		return &result
	}
	return nil
}

func (i *Installer) currentRelease() (string, error) {
	for _, path := range []string{i.options.RootDir, i.options.StateDir, i.options.ServicePath, i.options.NginxPath, i.options.Executable} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\n\r\x00%") {
			return "", ErrInstallUnsupported
		}
	}
	for _, path := range []string{i.options.RootDir, filepath.Join(i.options.RootDir, "releases")} {
		info, err := os.Lstat(path)
		if err != nil || !trustedInstallDirectory(info, false) {
			return "", ErrInstallUnsupported
		}
	}
	current, err := filepath.EvalSymlinks(filepath.Join(i.options.RootDir, "current"))
	if err != nil || !releaseWithinRoot(i.options.RootDir, current) {
		return "", ErrInstallUnsupported
	}
	directoryInfo, err := os.Lstat(current)
	if err != nil || !trustedInstallDirectory(directoryInfo, false) {
		return "", ErrInstallUnsupported
	}
	binary := filepath.Join(current, "nanotail-portal")
	self, err := filepath.EvalSymlinks(i.options.Executable)
	if err != nil || self != binary {
		return "", ErrInstallUnsupported
	}
	info, err := os.Lstat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return "", ErrInstallUnsupported
	}
	return current, nil
}

func (i *Installer) validateHost(ctx context.Context) error {
	if i == nil || !SupportedPlatform(i.targetOS, i.targetArch) {
		return ErrInstallUnsupported
	}
	if i.options.ValidateHost != nil {
		return i.options.ValidateHost(ctx)
	}
	if !i.Supported() {
		return ErrInstallUnsupported
	}
	output, err := i.options.Run(ctx, "systemctl", "show", portalUnit, "--property=MainPID", "--property=Restart", "--property=ExecStart", "--property=Type", "--property=RemainAfterExit", "--property=RestartPreventExitStatus")
	if err != nil {
		return ErrInstallUnsupported
	}
	values := make(map[string]string)
	for _, line := range strings.Split(string(output), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = value
		}
	}
	if (values["Type"] != "simple" && values["Type"] != "exec") || values["RemainAfterExit"] != "no" {
		return ErrInstallUnsupported
	}
	for _, status := range strings.Fields(values["RestartPreventExitStatus"]) {
		if status == "0" {
			return ErrInstallUnsupported
		}
	}
	if values["MainPID"] != strconv.Itoa(os.Getpid()) || values["Restart"] != "always" || !strings.Contains(values["ExecStart"], "path="+filepath.Join(i.options.RootDir, "current", "nanotail-portal")+" ;") {
		return ErrInstallUnsupported
	}
	return nil
}

func (i *Installer) Prepare(ctx context.Context, archive *os.File, version, digest string) (_ *Installation, resultErr error) {
	if i == nil || !SupportedPlatform(i.targetOS, i.targetArch) {
		return nil, ErrInstallUnsupported
	}
	if !ValidVersion(version) || len(digest) != 64 {
		return nil, ErrInvalidSelection
	}
	decoded, err := hex.DecodeString(digest)
	if err != nil || hex.EncodeToString(decoded) != digest || archive == nil {
		return nil, ErrInvalidSelection
	}
	if !i.options.Gate.TryAcquire("upgrade") {
		return nil, ErrMaintenanceBusy
	}
	accepted := false
	defer func() {
		if !accepted {
			i.options.Gate.Release("upgrade")
			i.mu.Lock()
			i.preparing = nil
			i.mu.Unlock()
		}
	}()
	if err := i.validateHost(ctx); err != nil {
		return nil, err
	}
	previous, err := i.currentRelease()
	if err != nil {
		return nil, err
	}
	if i.options.BootID == "" {
		return nil, ErrInstallUnsupported
	}
	lock, err := lockInstallState(i.options.StateDir, true)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInstallFailed, err)
	}
	defer lock.Close()
	if _, err := os.Lstat(filepath.Join(i.options.StateDir, "pending.json")); !errors.Is(err, os.ErrNotExist) {
		return nil, ErrInstallPending
	}
	installation := Installation{ID: rand.Text(), Version: version, Phase: "preparing", StartedAt: time.Now().UTC()}
	i.mu.Lock()
	i.preparing = &installation
	i.mu.Unlock()
	next := filepath.Join(i.options.RootDir, "releases", version+"-"+digest[:12]+"-"+installation.ID[:8])
	manifest, err := ExtractPackage(ctx, archive, next, i.keys, i.targetOS, i.targetArch, digest)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPackage, err)
	}
	defer func() {
		if !accepted {
			_ = os.RemoveAll(next)
		}
	}()
	if manifest.Version != version {
		return nil, ErrInvalidSelection
	}
	if err := validateExecutable(filepath.Join(next, "nanotail-portal")); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPackage, err)
	}
	integration := i.integrations()
	plan, err := integration.Plan(previous, next)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInstallFailed, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tx := installTransaction{Format: 1, Installation: installation, RootDir: i.options.RootDir, StateDir: i.options.StateDir, ServicePath: i.options.ServicePath, NginxPath: i.options.NginxPath, PreviousRelease: previous, NextRelease: next, PackageSHA256: digest, BootID: i.options.BootID, InitiatorPID: os.Getpid(), Deadline: time.Now().Add(3 * time.Minute).UTC(), Plan: plan}
	tx.Phase = "prepared"
	// Make recovery survive reboot before making acceptance durable.
	if err := i.installRecoveryUnit(ctx, &tx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInstallFailed, err)
	}
	publish := i.publishPending
	if publish == nil {
		publish = func(tx *installTransaction) error { return writeInstallJSON(i.options.StateDir, "pending.json", tx) }
	}
	if err := publish(&tx); err != nil {
		// Rename may have succeeded before directory fsync failed. Never delete
		// a release referenced by a published transaction or release its gate.
		if published, readErr := loadTransaction(i.options.StateDir); readErr == nil && published.ID == tx.ID {
			accepted = true
			published.Deadline = time.Now().UTC()
			_ = writeInstallJSON(i.options.StateDir, "pending.json", published)
			recoveryCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_, _ = i.options.Run(recoveryCtx, "systemctl", "restart", recoveryUnit)
			cancel()
		}
		return nil, fmt.Errorf("%w: %v", ErrInstallFailed, err)
	}
	// This is an independent unit, so restarting it cannot stop the portal.
	if _, err := i.options.Run(ctx, "systemctl", "restart", recoveryUnit); err != nil {
		tx.Phase = "aborted"
		tx.Error = "The recovery service could not be started. The installed release was not changed."
		if finishErr := finishInstallation(&tx); finishErr != nil {
			accepted = true
			return nil, fmt.Errorf("%w: recovery unavailable: %v", ErrInstallFailed, finishErr)
		}
		return nil, fmt.Errorf("%w: recovery service unavailable", ErrInstallFailed)
	}
	accepted = true
	i.mu.Lock()
	i.preparing = nil
	i.mu.Unlock()
	result := tx.Installation
	return &result, nil
}

func validateExecutable(path string) error {
	file, err := elf.Open(path)
	if err != nil {
		return errors.New("release executable is not a valid ELF binary")
	}
	defer file.Close()
	if file.Machine != elf.EM_AARCH64 || file.Class != elf.ELFCLASS64 || file.Data != elf.ELFDATA2LSB || (file.Type != elf.ET_EXEC && file.Type != elf.ET_DYN) {
		return errors.New("release executable must be a little-endian ARM64 ELF64 executable")
	}
	return nil
}

func (i *Installer) integrations() *IntegrationManager {
	return &IntegrationManager{ServicePath: i.options.ServicePath, NginxPath: i.options.NginxPath, Run: func(ctx context.Context, name string, args ...string) error {
		_, err := i.options.Run(ctx, name, args...)
		return err
	}}
}

// Activate is called only by main after draining handlers/workers and closing DB.
func (i *Installer) Activate(ctx context.Context) error {
	lock, err := waitInstallState(ctx, i.options.StateDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	tx, err := loadTransaction(i.options.StateDir)
	if err != nil {
		return err
	}
	if tx.Phase != "prepared" || tx.BootID != i.options.BootID {
		return ErrInstallPending
	}
	if current, err := i.currentRelease(); err != nil || current != tx.PreviousRelease {
		return ErrInstallUnsupported
	}
	tx.Phase = "activating"
	if err := writeInstallJSON(i.options.StateDir, "pending.json", tx); err != nil {
		return err
	}
	if err := i.integrations().Apply(ctx, tx.Plan); err != nil {
		return fmt.Errorf("apply release configuration: %w", err)
	}
	if err := atomicReleaseLink(tx.RootDir, "previous", tx.PreviousRelease); err != nil {
		return err
	}
	if err := atomicReleaseLink(tx.RootDir, "current", tx.NextRelease); err != nil {
		return err
	}
	tx.Phase = "awaiting-ready"
	tx.Deadline = time.Now().Add(90 * time.Second).UTC()
	return writeInstallJSON(i.options.StateDir, "pending.json", tx)
}

// Abort cancels a prepared request when draining or database closure failed.
// Once activation starts the independent recovery process owns restoration.
func (i *Installer) Abort(ctx context.Context) error {
	lock, err := lockInstallState(i.options.StateDir, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer lock.Close()
	tx, err := loadTransaction(i.options.StateDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if tx.Phase != "prepared" {
		return nil
	}
	tx.Phase = "aborted"
	tx.Error = "Installation was canceled before changing the installed release."
	if err := finishInstallation(tx); err != nil {
		return err
	}
	i.options.Gate.Release("upgrade")
	return nil
}

func trustedInstallDirectory(info os.FileInfo, private bool) bool {
	if info == nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 {
		return false
	}
	if private && info.Mode().Perm()&0077 != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}
