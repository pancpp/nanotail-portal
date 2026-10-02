package upgrade

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	STAGED_PACKAGE_NAME           = "package.tar.gz"
	INCOMING_PACKAGE_PREFIX       = ".incoming-"
	INCOMING_PACKAGE_TOKEN_LENGTH = 26
)

var (
	ErrInvalidPackage  = errors.New("The upgrade package could not be verified")
	ErrTooLarge        = errors.New("The upgrade package exceeds the 128 MiB size limit")
	ErrBusy            = errors.New("Another upgrade operation is in progress")
	ErrUnavailable     = errors.New("Online update checking is unavailable")
	ErrVersionMismatch = errors.New("The package does not match the selected release")
)

type Release struct {
	Version     string `json:"version"`
	Notes       string `json:"notes"`
	PublishedAt string `json:"publishedAt"`
	PackageName string `json:"packageName"`
	Size        int64  `json:"size"`
}

type StagedPackage struct {
	Version    string    `json:"version"`
	SHA256     string    `json:"sha256"`
	Size       int64     `json:"size"`
	VerifiedAt time.Time `json:"verifiedAt"`
	Notes      string    `json:"notes"`
}

type Status struct {
	CurrentVersion        string         `json:"currentVersion"`
	LatestRelease         *Release       `json:"latestRelease"`
	UpdateAvailable       bool           `json:"updateAvailable"`
	CheckedAt             *time.Time     `json:"checkedAt"`
	StagedPackage         *StagedPackage `json:"stagedPackage"`
	InstallationSupported bool           `json:"installationSupported"`
	Installation          *Installation  `json:"installation"`
	OnlineCheckSupported  bool           `json:"onlineCheckSupported"`
	Phase                 string         `json:"phase"`
}

// ReleaseSource owns download locations. An API caller can select only the
// version returned by Latest, never an arbitrary URL or filesystem location.
type ReleaseSource interface {
	Latest(ctx context.Context, targetOS, targetArch string) (*Release, error)
	Open(ctx context.Context, release Release) (io.ReadCloser, error)
}

// Service authenticates and stores one downloaded package. Installation is
// delegated to the optional installer while holding the staging operation lock.
type Service struct {
	directory  string
	targetOS   string
	targetArch string
	keys       []ed25519.PublicKey
	source     ReleaseSource
	operation  sync.Mutex
	mu         sync.Mutex
	status     Status
	installer  *Installer
}

func NewService(directory, currentVersion, targetOS, targetArch string, keys []ed25519.PublicKey, source ReleaseSource) *Service {
	s := &Service{
		directory: directory, targetOS: targetOS, targetArch: targetArch, source: source,
		status: Status{CurrentVersion: currentVersion, OnlineCheckSupported: source != nil && SupportedPlatform(targetOS, targetArch), Phase: "idle"},
	}
	for _, key := range keys {
		s.keys = append(s.keys, append(ed25519.PublicKey(nil), key...))
	}
	return s
}

func (s *Service) Status() Status {
	s.mu.Lock()
	status := s.status
	installer := s.installer
	if status.LatestRelease != nil {
		copy := *status.LatestRelease
		status.LatestRelease = &copy
	}
	if status.StagedPackage != nil {
		copy := *status.StagedPackage
		status.StagedPackage = &copy
	}
	if status.CheckedAt != nil {
		copy := *status.CheckedAt
		status.CheckedAt = &copy
	}
	s.mu.Unlock()
	if installer != nil {
		status.InstallationSupported = installer.Supported()
		status.Installation = installer.Status()
	}
	return status
}

func (s *Service) SetInstaller(installer *Installer) {
	s.mu.Lock()
	s.installer = installer
	s.mu.Unlock()
}

func (s *Service) installationController() *Installer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.installer
}

func (s *Service) setPhase(phase string) {
	s.mu.Lock()
	s.status.Phase = phase
	s.mu.Unlock()
}

func (s *Service) finish() {
	s.mu.Lock()
	s.status.Phase = "idle"
	if s.status.StagedPackage != nil {
		s.status.Phase = "staged"
	}
	s.mu.Unlock()
	s.operation.Unlock()
}

func (s *Service) Check(ctx context.Context) (Status, error) {
	if !s.operation.TryLock() {
		return s.Status(), ErrBusy
	}
	err := s.check(ctx)
	s.finish()
	return s.Status(), err
}

func (s *Service) check(ctx context.Context) error {
	if installer := s.installationController(); installer != nil && installer.Pending() {
		return ErrMaintenanceBusy
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.source == nil || !SupportedPlatform(s.targetOS, s.targetArch) {
		return ErrUnavailable
	}
	s.setPhase("checking")
	release, err := s.source.Latest(ctx, s.targetOS, s.targetArch)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	available := false
	if release != nil {
		_, publishedErr := time.Parse(time.RFC3339, release.PublishedAt)
		if !ValidVersion(release.Version) || release.Size <= 0 || release.Size > MAX_PACKAGE_BYTES || strings.TrimSpace(release.PackageName) == "" || publishedErr != nil {
			return errors.New("release source returned invalid release metadata")
		}
		copy := *release
		release = &copy
		// Development builds without a valid version cannot be ordered against
		// releases. They can still download the checked release explicitly.
		comparison, err := CompareVersions(release.Version, s.Status().CurrentVersion)
		available = err == nil && comparison > 0
	}
	now := time.Now().UTC()
	s.mu.Lock()
	s.status.LatestRelease = release
	s.status.UpdateAvailable = available
	s.status.CheckedAt = &now
	s.mu.Unlock()
	return nil
}

func (s *Service) Download(ctx context.Context, version string) (Status, error) {
	if !s.operation.TryLock() {
		return s.Status(), ErrBusy
	}
	err := s.download(ctx, version)
	s.finish()
	return s.Status(), err
}

func (s *Service) download(ctx context.Context, version string) error {
	if installer := s.installationController(); installer != nil && installer.Pending() {
		return ErrMaintenanceBusy
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.source == nil || !SupportedPlatform(s.targetOS, s.targetArch) {
		return ErrUnavailable
	}
	status := s.Status()
	if status.LatestRelease == nil || version != status.LatestRelease.Version {
		return ErrVersionMismatch
	}
	if status.LatestRelease.Size > MAX_PACKAGE_BYTES {
		return ErrTooLarge
	}
	s.setPhase("downloading")
	reader, err := s.source.Open(ctx, *status.LatestRelease)
	if err != nil {
		return err
	}
	if reader == nil {
		return errors.New("release source returned no package")
	}
	defer reader.Close()
	return s.stage(ctx, reader, *status.LatestRelease)
}

// PrepareInstall pins the exact staged archive selected by the caller. The
// installer re-verifies this open file before extracting; no client path is used.
func (s *Service) PrepareInstall(ctx context.Context, version, digest string) (*Installation, error) {
	if !s.operation.TryLock() {
		return nil, ErrBusy
	}
	defer s.finish()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	decoded, err := hex.DecodeString(digest)
	if !ValidVersion(version) || err != nil || len(decoded) != sha256.Size || digest != strings.ToLower(digest) {
		return nil, ErrInvalidSelection
	}
	installer := s.installationController()
	if installer == nil {
		return nil, ErrInstallUnsupported
	}
	if installer.Pending() {
		return nil, ErrInstallPending
	}
	staged := s.Status().StagedPackage
	if staged == nil {
		return nil, ErrNoStagedPackage
	}
	if staged.Version != version || staged.SHA256 != digest {
		return nil, ErrInvalidSelection
	}
	root, err := os.OpenRoot(s.directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoStagedPackage
	}
	if err != nil {
		return nil, fmt.Errorf("%w: open staging directory: %v", ErrInstallFailed, err)
	}
	defer root.Close()
	archive, err := root.OpenFile(STAGED_PACKAGE_NAME, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoStagedPackage
	}
	if err != nil {
		return nil, fmt.Errorf("%w: open staged archive: %v", ErrInvalidPackage, err)
	}
	defer archive.Close()
	info, err := archive.Stat()
	if err != nil {
		return nil, fmt.Errorf("%w: inspect staged archive: %v", ErrInvalidPackage, err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return nil, ErrInvalidPackage
	}
	if info.Size() > MAX_PACKAGE_BYTES {
		return nil, ErrTooLarge
	}
	return installer.Prepare(ctx, archive, version, digest)
}

// ScheduleInstall is called only after PrepareInstall has durably accepted the
// operation and HTTP has attempted to acknowledge it.
func (s *Service) ScheduleInstall() {
	if installer := s.installationController(); installer != nil {
		installer.Schedule()
	}
}

func (s *Service) stage(ctx context.Context, reader io.Reader, release Release) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if reader == nil {
		return ErrInvalidPackage
	}
	if err := os.MkdirAll(s.directory, 0700); err != nil {
		return fmt.Errorf("create upgrade staging directory: %w", err)
	}
	root, err := os.OpenRoot(s.directory)
	if err != nil {
		return fmt.Errorf("open upgrade staging directory: %w", err)
	}
	defer root.Close()
	// Fix the filename shape even if rand.Text returns longer tokens in a
	// future Go release, so restart cleanup can keep a narrow allowlist.
	name := INCOMING_PACKAGE_PREFIX + rand.Text()[:INCOMING_PACKAGE_TOKEN_LENGTH]
	file, err := root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create temporary package: %w", err)
	}
	defer root.Remove(name)
	defer file.Close()
	digest := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, digest), io.LimitReader(contextReader{ctx, reader}, MAX_PACKAGE_BYTES+1))
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if size > MAX_PACKAGE_BYTES {
		return ErrTooLarge
	}
	if size != release.Size {
		return fmt.Errorf("%w: downloaded package size does not match the selected release", ErrInvalidPackage)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	s.setPhase("verifying")
	manifest, err := s.verify(ctx, file)
	if err != nil {
		return err
	}
	if manifest.Version != release.Version {
		return ErrVersionMismatch
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Rename is the commit point: failed verification or an interrupted download
	// leaves the previously verified package intact.
	if err := root.Rename(name, STAGED_PACKAGE_NAME); err != nil {
		return fmt.Errorf("commit staged package: %w", err)
	}
	s.recordStaged(manifest, size, hex.EncodeToString(digest.Sum(nil)))
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

// Load authenticates the package again on startup. No unsigned metadata file
// can cause an unverified package to appear trusted in the UI.
func (s *Service) Load(ctx context.Context) error {
	if !s.operation.TryLock() {
		return ErrBusy
	}
	defer s.finish()
	s.mu.Lock()
	s.status.StagedPackage = nil
	s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(s.directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	if err := removeInterruptedDownloads(ctx, root); err != nil {
		return fmt.Errorf("clean interrupted downloads: %w", err)
	}
	// Do not follow a package symlink, including one replaced after startup.
	file, err := root.OpenFile(STAGED_PACKAGE_NAME, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: could not open the staged package", ErrInvalidPackage)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: staged package must be a regular file", ErrInvalidPackage)
	}
	if info.Size() > MAX_PACKAGE_BYTES {
		return ErrTooLarge
	}
	s.setPhase("verifying")
	digest := sha256.New()
	manifest, err := s.verify(ctx, io.TeeReader(file, digest))
	if err != nil {
		return err
	}
	s.recordStaged(manifest, info.Size(), hex.EncodeToString(digest.Sum(nil)))
	return nil
}

func removeInterruptedDownloads(ctx context.Context, root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	removed := false
	for {
		entries, readErr := directory.ReadDir(64)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := entry.Name()
			token, incoming := strings.CutPrefix(name, INCOMING_PACKAGE_PREFIX)
			if !incoming || len(token) != INCOMING_PACKAGE_TOKEN_LENGTH || strings.Trim(token, "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567") != "" {
				continue
			}
			// Lstat and Remove are both confined to this root. Never follow
			// links or recurse into directories resembling temporary files.
			info, err := root.Lstat(name)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				continue
			}
			if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			removed = true
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	if removed {
		return directory.Sync()
	}
	return nil
}

func (s *Service) verify(ctx context.Context, reader io.Reader) (*Manifest, error) {
	manifest, err := VerifyPackage(contextReader{ctx, reader}, s.keys, s.targetOS, s.targetArch)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPackage, err)
	}
	return manifest, nil
}

func (s *Service) recordStaged(manifest *Manifest, size int64, digest string) {
	s.mu.Lock()
	s.status.StagedPackage = &StagedPackage{
		Version: manifest.Version, Notes: manifest.ReleaseNotes,
		Size: size, SHA256: digest, VerifiedAt: time.Now().UTC(),
	}
	s.mu.Unlock()
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
