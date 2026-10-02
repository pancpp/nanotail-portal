package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pancpp/nanotail-portal/maintenance"
)

func TestServiceInstallRequiresStagedSelection(t *testing.T) {
	directory := t.TempDir()
	service := NewService(directory, "v1.0.0", "linux", "arm64", nil, nil)
	digest := strings.Repeat("ab", 32)
	if _, err := service.PrepareInstall(t.Context(), "v2.0.0", digest); !errors.Is(err, ErrInstallUnsupported) {
		t.Fatalf("missing installer: %v", err)
	}
	for _, selection := range [][2]string{{"../../file", digest}, {"v2.0.0", "bad"}, {"v2.0.0", strings.ToUpper(digest)}} {
		if _, err := service.PrepareInstall(t.Context(), selection[0], selection[1]); !errors.Is(err, ErrInvalidSelection) {
			t.Fatalf("malformed selection: %v", err)
		}
	}
	hostCalls := 0
	installer := NewInstaller(InstallerOptions{StateDir: t.TempDir(), ValidateHost: func(context.Context) error { hostCalls++; return nil }}, nil, "linux", "arm64")
	service.SetInstaller(installer)
	if !service.Status().InstallationSupported {
		t.Fatal("attached installer support missing from status")
	}
	if _, err := service.PrepareInstall(t.Context(), "v2.0.0", digest); !errors.Is(err, ErrNoStagedPackage) {
		t.Fatalf("missing staged metadata: %v", err)
	}
	service.status.StagedPackage = &StagedPackage{Version: "v2.0.0", SHA256: digest}
	for _, selection := range [][2]string{{"v3.0.0", digest}, {"v2.0.0", strings.Repeat("cd", 32)}} {
		if _, err := service.PrepareInstall(t.Context(), selection[0], selection[1]); !errors.Is(err, ErrInvalidSelection) {
			t.Fatalf("stale staged selection: %v", err)
		}
	}
	if _, err := service.PrepareInstall(t.Context(), "v2.0.0", digest); !errors.Is(err, ErrNoStagedPackage) {
		t.Fatalf("missing staged archive: %v", err)
	}
	archive := filepath.Join(directory, STAGED_PACKAGE_NAME)
	outside := filepath.Join(t.TempDir(), "package.tar.gz")
	if err := os.WriteFile(outside, []byte("untrusted"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, archive); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PrepareInstall(t.Context(), "v2.0.0", digest); !errors.Is(err, ErrInvalidPackage) {
		t.Fatalf("symlink staged archive accepted: %v", err)
	}
	if err := os.Remove(archive); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(archive, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PrepareInstall(t.Context(), "v2.0.0", digest); !errors.Is(err, ErrInvalidPackage) {
		t.Fatalf("nonregular staged archive accepted: %v", err)
	}
	if hostCalls != 0 {
		t.Fatal("rejected selection reached installation")
	}
}

func TestServiceMaintenanceExcludesCheckDownloadAndInstall(t *testing.T) {
	var gate maintenance.Gate
	source := &serviceTestSource{}
	service := NewService(t.TempDir(), "v1.0.0", "linux", "arm64", nil, source)
	installer := NewInstaller(InstallerOptions{StateDir: t.TempDir(), Gate: &gate, ValidateHost: func(context.Context) error { return nil }}, nil, "linux", "arm64")
	service.SetInstaller(installer)
	for _, operation := range []string{"reset", "install"} {
		gate.TryAcquire(operation)
		if _, err := service.Check(t.Context()); !errors.Is(err, ErrMaintenanceBusy) {
			t.Fatalf("check during %s: %v", operation, err)
		}
		if _, err := service.Download(t.Context(), "v2.0.0"); !errors.Is(err, ErrMaintenanceBusy) {
			t.Fatalf("download during %s: %v", operation, err)
		}
		if _, err := service.PrepareInstall(t.Context(), "v2.0.0", strings.Repeat("ab", 32)); !errors.Is(err, ErrInstallPending) {
			t.Fatalf("install during %s: %v", operation, err)
		}
		gate.Release(operation)
	}
	if source.targetOS != "" || source.opened != 0 {
		t.Fatal("maintenance request contacted the release source")
	}
	service.operation.Lock()
	if _, err := service.PrepareInstall(t.Context(), "v2.0.0", strings.Repeat("ab", 32)); !errors.Is(err, ErrBusy) {
		t.Fatalf("installation overlapped staging: %v", err)
	}
	service.operation.Unlock()
	if _, err := service.Check(t.Context()); err != nil {
		t.Fatalf("released maintenance still blocks checking: %v", err)
	}
}

func serviceTestKeys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return public, private
}

func serviceTestPackage(t *testing.T, private ed25519.PrivateKey, version string) []byte {
	t.Helper()
	binary := []byte("test portal executable " + version)
	hash := sha256.Sum256(binary)
	manifest, err := json.Marshal(Manifest{
		FormatVersion: 1, Application: APPLICATION, Version: version, OS: "linux", Arch: "arm64",
		ReleaseNotes: "Signed release notes for " + version,
		Files:        map[string]PackageFile{"payload/nanotail-portal": {SHA256: hex.EncodeToString(hash[:]), Size: int64(len(binary))}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	zipped := gzip.NewWriter(&output)
	archive := tar.NewWriter(zipped)
	for _, file := range []struct {
		name string
		data []byte
	}{
		{"manifest.json", manifest},
		{"manifest.sig", ed25519.Sign(private, manifest)},
		{"payload/nanotail-portal", binary},
	} {
		if err := archive.WriteHeader(&tar.Header{Name: PACKAGE_PREFIX + file.name, Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len(file.data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write(file.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func assertOnlyStagedPackage(t *testing.T, directory string, expected []byte) {
	t.Helper()
	files, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name() != STAGED_PACKAGE_NAME {
		t.Fatalf("unexpected staging directory entries: %v", files)
	}
	actual, err := os.ReadFile(filepath.Join(directory, STAGED_PACKAGE_NAME))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, expected) {
		t.Fatal("staged package bytes changed unexpectedly")
	}
}

func TestServiceStagesAndReloadsVerifiedPackage(t *testing.T) {
	public, private := serviceTestKeys(t)
	data := serviceTestPackage(t, private, "2.0.0")
	directory := filepath.Join(t.TempDir(), "staging")
	source := &serviceTestSource{}
	s := NewService(directory, "1.0.0", "linux", "arm64", []ed25519.PublicKey{public}, source)
	if err := s.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, err := downloadTestPackage(t, s, source, "2.0.0", data)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	staged := status.StagedPackage
	if status.Phase != "staged" || staged == nil || staged.Version != "2.0.0" || staged.SHA256 != hex.EncodeToString(digest[:]) || staged.Size != int64(len(data)) || staged.VerifiedAt.IsZero() || staged.Notes != "Signed release notes for 2.0.0" {
		t.Fatalf("unexpected staged status: %+v, package: %+v", status, staged)
	}
	if status.InstallationSupported || !status.OnlineCheckSupported {
		t.Fatalf("unsupported operations advertised: %+v", status)
	}
	assertOnlyStagedPackage(t, directory, data)

	reopened := NewService(directory, "1.0.0", "linux", "arm64", []ed25519.PublicKey{public}, nil)
	if err := reopened.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	loaded := reopened.Status().StagedPackage
	if loaded == nil || loaded.SHA256 != staged.SHA256 || loaded.Version != staged.Version || loaded.Size != staged.Size {
		t.Fatalf("reloaded package differs: %+v", loaded)
	}
	// A caller's status snapshot cannot mutate the service's trust decision.
	status.StagedPackage.Version = "99.0.0"
	if s.Status().StagedPackage.Version != "2.0.0" {
		t.Fatal("status returned mutable internal state")
	}
	if err := os.WriteFile(filepath.Join(directory, STAGED_PACKAGE_NAME), []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Load(context.Background()); !errors.Is(err, ErrInvalidPackage) {
		t.Fatalf("corrupt package accepted: %v", err)
	}
	if reopened.Status().StagedPackage != nil {
		t.Fatal("corrupt package still appears verified")
	}
}

func TestServiceRejectedDownloadsPreservePreviousPackage(t *testing.T) {
	public, private := serviceTestKeys(t)
	_, untrusted := serviceTestKeys(t)
	good := serviceTestPackage(t, private, "2.0.0")
	directory := t.TempDir()
	source := &serviceTestSource{}
	s := NewService(directory, "1.0.0", "linux", "arm64", []ed25519.PublicKey{public}, source)
	if _, err := downloadTestPackage(t, s, source, "2.0.0", good); err != nil {
		t.Fatal(err)
	}
	for name, rejected := range map[string][]byte{
		"malformed": []byte("invalid package"),
		"untrusted": serviceTestPackage(t, untrusted, "3.0.0"),
	} {
		t.Run(name, func(t *testing.T) {
			status, err := downloadTestPackage(t, s, source, "3.0.0", rejected)
			if !errors.Is(err, ErrInvalidPackage) {
				t.Fatalf("expected verification error, got %v", err)
			}
			if status.Phase != "staged" || status.StagedPackage.Version != "2.0.0" {
				t.Fatalf("rejected download replaced status: %+v", status)
			}
			assertOnlyStagedPackage(t, directory, good)
		})
	}
}

type serviceBlockingReader struct {
	once    sync.Once
	started chan struct{}
	release chan struct{}
	reader  io.Reader
}

func (r *serviceBlockingReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return r.reader.Read(p)
}

func TestServiceConcurrentAndCanceledDownload(t *testing.T) {
	public, private := serviceTestKeys(t)
	good := serviceTestPackage(t, private, "1.1.0")
	directory := t.TempDir()
	source := &serviceTestSource{}
	s := NewService(directory, "1.0.0", "linux", "arm64", []ed25519.PublicKey{public}, source)
	if _, err := downloadTestPackage(t, s, source, "1.1.0", good); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	data := serviceTestPackage(t, private, "1.2.0")
	reader := &serviceBlockingReader{started: make(chan struct{}), release: make(chan struct{}), reader: bytes.NewReader(data)}
	source.latest, source.reader = serviceTestRelease("1.2.0", int64(len(data))), reader
	if _, err := s.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, err := s.Download(ctx, "1.2.0")
		finished <- err
	}()
	<-reader.started
	if status := s.Status(); status.Phase != "downloading" || status.StagedPackage.Version != "1.1.0" {
		t.Errorf("wrong concurrent status: %+v", status)
	}
	if _, err := s.Check(context.Background()); !errors.Is(err, ErrBusy) {
		t.Errorf("concurrent check: %v", err)
	}
	if _, err := s.Download(context.Background(), "1.2.0"); !errors.Is(err, ErrBusy) {
		t.Errorf("concurrent download: %v", err)
	}
	if err := s.Load(context.Background()); !errors.Is(err, ErrBusy) {
		t.Errorf("concurrent load: %v", err)
	}
	cancel()
	close(reader.release)
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled download committed: %v", err)
	}
	assertOnlyStagedPackage(t, directory, good)
}

type serviceTestSource struct {
	latest   *Release
	data     []byte
	reader   io.Reader
	opened   int
	targetOS string
	arch     string
}

func (s *serviceTestSource) Latest(_ context.Context, targetOS, arch string) (*Release, error) {
	s.targetOS, s.arch = targetOS, arch
	return s.latest, nil
}

func (s *serviceTestSource) Open(_ context.Context, release Release) (io.ReadCloser, error) {
	s.opened++
	if s.reader != nil {
		return io.NopCloser(s.reader), nil
	}
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

func serviceTestRelease(version string, size int64) *Release {
	return &Release{Version: version, PublishedAt: "2026-10-01T12:00:00Z", PackageName: "release.tar.gz", Size: size}
}

func downloadTestPackage(t *testing.T, s *Service, source *serviceTestSource, version string, data []byte) (Status, error) {
	t.Helper()
	source.latest, source.data, source.reader = serviceTestRelease(version, int64(len(data))), data, nil
	if _, err := s.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s.Download(context.Background(), version)
}

func TestServiceOnlineCheckAndSelectedReleaseVerification(t *testing.T) {
	public, private := serviceTestKeys(t)
	data := serviceTestPackage(t, private, "1.2.0")
	source := &serviceTestSource{latest: &Release{Version: "1.2.0", Size: int64(len(data)), PackageName: "release.tar.gz", PublishedAt: "2026-10-01T12:00:00Z"}, data: data}
	s := NewService(t.TempDir(), "1.0.0", "linux", "arm64", []ed25519.PublicKey{public}, source)
	if _, err := s.Download(context.Background(), "1.2.0"); !errors.Is(err, ErrVersionMismatch) || source.opened != 0 {
		t.Fatalf("unchecked release was downloaded: %v", err)
	}
	status, err := s.Check(context.Background())
	if err != nil || !status.UpdateAvailable || status.LatestRelease.Version != "1.2.0" || status.CheckedAt == nil || !status.OnlineCheckSupported || source.targetOS != "linux" || source.arch != "arm64" {
		t.Fatalf("unexpected release check: %+v, %v", status, err)
	}
	status.LatestRelease.Version = "99.0.0"
	if _, err := s.Download(context.Background(), "99.0.0"); !errors.Is(err, ErrVersionMismatch) || source.opened != 0 {
		t.Fatalf("caller-supplied release was downloaded: %v", err)
	}
	status, err = s.Download(context.Background(), "1.2.0")
	if err != nil || status.StagedPackage.Version != "1.2.0" {
		t.Fatalf("valid release failed: %+v, %v", status, err)
	}
	previous := append([]byte(nil), data...)
	// A valid signature is insufficient when the downloaded version is not the
	// version selected by the user from the checked release metadata.
	source.data = serviceTestPackage(t, private, "1.3.0")
	source.latest.Size = int64(len(source.data))
	if _, err := s.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Download(context.Background(), "1.2.0"); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("wrong release version accepted: %v", err)
	}
	assertOnlyStagedPackage(t, s.directory, previous)
	for _, version := range []string{"1.0.0", "0.9.0", "1.0.0-beta.1"} {
		source.latest.Version = version
		status, err := s.Check(context.Background())
		if err != nil || status.UpdateAvailable {
			t.Fatalf("non-newer release advertised: %+v, %v", status, err)
		}
	}
}

func TestServiceCurrentVersionUpdateAvailability(t *testing.T) {
	public, private := serviceTestKeys(t)
	data := serviceTestPackage(t, private, "v1.2.0")
	for _, test := range []struct {
		current   string
		available bool
	}{
		{"v1.0.0", true},
		{"v1.2.0", false},
		{"v2.0.0", false},
		{"v1.2.0+g137e856", false},
		{"v2.0.0+g137e856", false},
		{"v2.0.0+g137e856.dirtyish", false},
		{"v2.0.0+g137e856.dirty.fixed", false},
		{"137e856", false},
		{"v1.0.0-dirty", true},
		{"v1.2.0-dirty", true},
		{"v2.0.0-dirty", true},
		{"v2.0.0-3-g137e856-dirty", true},
		{"137e856-dirty", true},
		{"v0.0.0-dev.57.20261002094814+g137e856.dirty", true},
		{"v1.2.0+g137e856.dirty", true},
		{"v2.0.0+g137e856.dirty", true},
	} {
		t.Run(test.current, func(t *testing.T) {
			source := &serviceTestSource{latest: serviceTestRelease("v1.2.0", int64(len(data))), data: data}
			s := NewService(t.TempDir(), test.current, "linux", "arm64", []ed25519.PublicKey{public}, source)
			status, err := s.Check(t.Context())
			if err != nil || status.UpdateAvailable != test.available || status.CurrentVersion != test.current || status.LatestRelease == nil || status.LatestRelease.Version != "v1.2.0" || status.CheckedAt == nil {
				t.Fatalf("unexpected update availability: %+v, %v", status, err)
			}
			if test.available {
				status, err = s.Download(t.Context(), "v1.2.0")
				if err != nil || status.StagedPackage == nil || status.StagedPackage.Version != "v1.2.0" {
					t.Fatalf("latest signed release was not staged: %+v, %v", status, err)
				}
			}
			// Dirty builds still need a compatible release from GitHub.
			source.latest = nil
			status, err = s.Check(t.Context())
			if err != nil || status.UpdateAvailable || status.LatestRelease != nil {
				t.Fatalf("missing release advertised as available: %+v, %v", status, err)
			}
		})
	}
}

func TestServiceUnknownCurrentVersionCanDownloadCheckedRelease(t *testing.T) {
	public, private := serviceTestKeys(t)
	data := serviceTestPackage(t, private, "1.2.0")
	for _, current := range []string{"", "development", "(devel)"} {
		t.Run(current, func(t *testing.T) {
			source := &serviceTestSource{}
			s := NewService(t.TempDir(), current, "linux", "arm64", []ed25519.PublicKey{public}, source)
			status, err := downloadTestPackage(t, s, source, "1.2.0", data)
			if err != nil {
				t.Fatal(err)
			}
			if status.UpdateAvailable || status.CurrentVersion != current || status.LatestRelease == nil || status.LatestRelease.Version != "1.2.0" || status.StagedPackage == nil || status.StagedPackage.Version != "1.2.0" {
				t.Fatalf("unknown-version comparison or selected download is incorrect: %+v", status)
			}
		})
	}
}

func TestServiceDownloadSizeMismatchPreservesPreviousPackage(t *testing.T) {
	public, private := serviceTestKeys(t)
	good := serviceTestPackage(t, private, "1.1.0")
	source := &serviceTestSource{}
	s := NewService(t.TempDir(), "1.0.0", "linux", "arm64", []ed25519.PublicKey{public}, source)
	if _, err := downloadTestPackage(t, s, source, "1.1.0", good); err != nil {
		t.Fatal(err)
	}
	source.data = serviceTestPackage(t, private, "1.2.0")
	source.latest = serviceTestRelease("1.2.0", int64(len(source.data))+1)
	if _, err := s.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Download(context.Background(), "1.2.0"); !errors.Is(err, ErrInvalidPackage) {
		t.Fatalf("incorrect advertised size accepted: %v", err)
	}
	assertOnlyStagedPackage(t, s.directory, good)
}

func TestServiceMissingSourceAndUnsafePersistedPackage(t *testing.T) {
	s := NewService(t.TempDir(), "1.0.0", "linux", "arm64", nil, nil)
	if _, err := s.Check(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing-source check: %v", err)
	}
	if _, err := s.Download(context.Background(), "1.2.0"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing-source download: %v", err)
	}
	target := filepath.Join(t.TempDir(), "unrelated")
	if err := os.WriteFile(target, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(s.directory, STAGED_PACKAGE_NAME)); err != nil {
		t.Fatal(err)
	}
	if err := s.Load(context.Background()); !errors.Is(err, ErrInvalidPackage) {
		t.Fatalf("symlink loaded as package: %v", err)
	}
}

func TestServiceUnsupportedPlatformDisablesOnlineOperations(t *testing.T) {
	for _, platform := range [][2]string{
		{"linux", "amd64"}, {"linux", "riscv64"}, {"linux", "arm"},
		{"darwin", "arm64"}, {"windows", "arm64"}, {"", "arm64"}, {"linux", ""},
	} {
		t.Run(platform[0]+"/"+platform[1], func(t *testing.T) {
			source := &serviceTestSource{latest: serviceTestRelease("v2.0.0", 100)}
			directory := filepath.Join(t.TempDir(), "staging")
			service := NewService(directory, "v1.0.0", platform[0], platform[1], nil, source)
			if service.Status().OnlineCheckSupported {
				t.Fatal("unsupported platform advertised online update support")
			}
			status, err := service.Check(t.Context())
			if !errors.Is(err, ErrUnavailable) || status.OnlineCheckSupported || status.CheckedAt != nil || status.LatestRelease != nil || status.Phase != "idle" {
				t.Fatalf("unsupported platform check: %+v, %v", status, err)
			}
			// Even a previously selected release must not bypass platform gating.
			service.status.LatestRelease = source.latest
			status, err = service.Download(t.Context(), source.latest.Version)
			if !errors.Is(err, ErrUnavailable) || status.OnlineCheckSupported || status.StagedPackage != nil || status.Phase != "idle" {
				t.Fatalf("unsupported platform download: %+v, %v", status, err)
			}
			if source.targetOS != "" || source.arch != "" || source.opened != 0 {
				t.Fatal("unsupported platform contacted the release source")
			}
			if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unsupported platform created a staging directory: %v", err)
			}
		})
	}
}

type serviceErrorReader struct{ err error }

func (r serviceErrorReader) Read([]byte) (int, error) { return 0, r.err }

func TestServiceDownloadPreservesReaderErrors(t *testing.T) {
	want := errors.New("download connection interrupted")
	source := &serviceTestSource{latest: serviceTestRelease("1.2.0", 100), reader: serviceErrorReader{want}}
	s := NewService(t.TempDir(), "1.0.0", "linux", "arm64", nil, source)
	if _, err := s.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Download(context.Background(), "1.2.0"); !errors.Is(err, want) {
		t.Fatalf("reader error classification was lost: %v", err)
	}
	files, err := os.ReadDir(s.directory)
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary file left behind: %v, %v", files, err)
	}
}

type serviceZeroReader struct{}

func (serviceZeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestServiceDownloadLimitPreservesPreviousPackage(t *testing.T) {
	public, private := serviceTestKeys(t)
	good := serviceTestPackage(t, private, "1.1.0")
	source := &serviceTestSource{}
	s := NewService(t.TempDir(), "1.0.0", "linux", "arm64", []ed25519.PublicKey{public}, source)
	if _, err := downloadTestPackage(t, s, source, "1.1.0", good); err != nil {
		t.Fatal(err)
	}
	// Bound the actual response even when the source advertises a small asset.
	source.latest, source.reader = serviceTestRelease("1.2.0", 100), io.LimitReader(serviceZeroReader{}, MAX_PACKAGE_BYTES+1)
	if _, err := s.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Download(context.Background(), "1.2.0"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized download accepted: %v", err)
	}
	assertOnlyStagedPackage(t, s.directory, good)
}

func TestServiceRejectsInvalidOnlineReleaseMetadata(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Release)
	}{
		{"version", func(r *Release) { r.Version = "development" }},
		{"date", func(r *Release) { r.PublishedAt = "not-a-date" }},
		{"name", func(r *Release) { r.PackageName = " " }},
		{"empty size", func(r *Release) { r.Size = 0 }},
		{"oversized", func(r *Release) { r.Size = MAX_PACKAGE_BYTES + 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			release := &Release{Version: "1.2.0", PublishedAt: "2026-10-01T12:00:00Z", PackageName: "release.tar.gz", Size: 100}
			test.mutate(release)
			s := NewService(t.TempDir(), "1.0.0", "linux", "arm64", nil, &serviceTestSource{latest: release})
			status, err := s.Check(context.Background())
			if err == nil || status.UpdateAvailable || status.LatestRelease != nil || status.CheckedAt != nil {
				t.Fatalf("invalid metadata became a release: %+v, %v", status, err)
			}
		})
	}
}

func TestServiceLoadRemovesOnlyInterruptedDownloads(t *testing.T) {
	public, private := serviceTestKeys(t)
	good := serviceTestPackage(t, private, "1.1.0")
	directory := t.TempDir()
	source := &serviceTestSource{}
	s := NewService(directory, "1.0.0", "linux", "arm64", []ed25519.PublicKey{public}, source)
	if _, err := downloadTestPackage(t, s, source, "1.1.0", good); err != nil {
		t.Fatal(err)
	}
	var interrupted []string
	// Cover multiple directory-read batches as well as cleanup after a crash.
	for range 70 {
		name := INCOMING_PACKAGE_PREFIX + rand.Text()[:INCOMING_PACKAGE_TOKEN_LENGTH]
		if err := os.WriteFile(filepath.Join(directory, name), []byte("partial download"), 0600); err != nil {
			t.Fatal(err)
		}
		interrupted = append(interrupted, name)
	}
	unrelated := []string{
		"notes.txt", ".incoming-", ".incoming-" + strings.Repeat("a", 26),
		".incoming-" + strings.Repeat("A", 25), ".incoming-" + strings.Repeat("A", 27),
		".incoming-" + strings.Repeat("A", 25) + "0", ".incoming-" + strings.Repeat("A", 26) + ".tar.gz",
	}
	for _, name := range unrelated {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("keep me"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	matchingDirectory := filepath.Join(directory, ".incoming-"+strings.Repeat("B", 26))
	if err := os.Mkdir(matchingDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(matchingDirectory, "keep.txt"), []byte("keep me"), 0600); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(external, []byte("keep me"), 0600); err != nil {
		t.Fatal(err)
	}
	matchingLink := filepath.Join(directory, ".incoming-"+strings.Repeat("C", 26))
	if err := os.Symlink(external, matchingLink); err != nil {
		t.Fatal(err)
	}

	reopened := NewService(directory, "1.0.0", "linux", "arm64", []ed25519.PublicKey{public}, nil)
	if err := reopened.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, name := range interrupted {
		if _, err := os.Lstat(filepath.Join(directory, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("interrupted download remains: %s, %v", name, err)
		}
	}
	for _, name := range unrelated {
		if data, err := os.ReadFile(filepath.Join(directory, name)); err != nil || string(data) != "keep me" {
			t.Errorf("unrelated file changed: %s, %v", name, err)
		}
	}
	for _, path := range []string{external, filepath.Join(matchingDirectory, "keep.txt")} {
		if data, err := os.ReadFile(path); err != nil || string(data) != "keep me" {
			t.Errorf("non-regular matching entry affected: %s, %v", path, err)
		}
	}
	if link, err := os.Readlink(matchingLink); err != nil || link != external {
		t.Errorf("matching symlink changed: %q, %v", link, err)
	}
	if data, err := os.ReadFile(filepath.Join(directory, STAGED_PACKAGE_NAME)); err != nil || !bytes.Equal(data, good) {
		t.Fatalf("committed package changed: %v", err)
	}
	if status := reopened.Status(); status.Phase != "staged" || status.StagedPackage == nil || status.StagedPackage.Version != "1.1.0" {
		t.Fatalf("committed package not restored: %+v", status)
	}
}
