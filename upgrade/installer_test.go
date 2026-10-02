package upgrade

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pancpp/nanotail-portal/maintenance"
)

type installerFixture struct {
	*integrationFixture
	installer                  *Installer
	public                     ed25519.PublicKey
	private                    ed25519.PrivateKey
	root, state, arch          string
	archive                    *os.File
	digest                     string
	processPID, mainPID        int
	processPath                string
	processAlive               bool
	failNginx, failPortalStart bool
	failRecoveryStart          bool
	dataFiles                  map[string][]byte
	dataInodes                 map[string]os.FileInfo
}

func installerELF(machine elf.Machine, class elf.Class, encoding elf.Data, fileType elf.Type) []byte {
	size, sizeOffset := 64, 52
	if class == elf.ELFCLASS32 {
		size, sizeOffset = 52, 40
	}
	payload := make([]byte, size)
	copy(payload, []byte{0x7f, 'E', 'L', 'F', byte(class), byte(encoding), 1})
	var order binary.ByteOrder = binary.LittleEndian
	if encoding == elf.ELFDATA2MSB {
		order = binary.BigEndian
	}
	order.PutUint16(payload[16:18], uint16(fileType))
	order.PutUint16(payload[18:20], uint16(machine))
	order.PutUint32(payload[20:24], 1)
	order.PutUint16(payload[sizeOffset:sizeOffset+2], uint16(size))
	return payload
}

func newInstallerFixture(t *testing.T, changed bool) *installerFixture {
	t.Helper()
	base := newIntegrationFixture(t)
	public, private := serviceTestKeys(t)
	f := &installerFixture{
		integrationFixture: base, public: public, private: private,
		root: filepath.Dir(filepath.Dir(base.previous)), processPID: os.Getpid(), mainPID: os.Getpid(), processAlive: true,
		dataFiles: map[string][]byte{
			"nanotail-portal.sqlite3":     []byte("database content must remain untouched"),
			"nanotail-portal.sqlite3-wal": []byte("WAL content must remain untouched"),
			"nanotail-portal.sqlite3-shm": []byte("SHM content must remain untouched"),
			"nanotail-portal.yml":         []byte("existing: configuration\n"),
		},
		dataInodes: make(map[string]os.FileInfo),
	}
	f.state = filepath.Join(f.root, "updater")
	if err := os.Chmod(f.root, 0700); err != nil {
		t.Fatal(err)
	}
	f.processPath = filepath.Join(f.previous, APPLICATION)
	// The fixture is always ARM64 regardless of the test host. It supplies a
	// parseable ELF header; no extracted executable or real command is run.
	payload := installerELF(elf.EM_AARCH64, elf.ELFCLASS64, elf.ELFDATA2LSB, elf.ET_EXEC)
	f.arch = "arm64"
	for _, release := range []string{f.previous, f.next} {
		path := filepath.Join(release, APPLICATION)
		writeIntegrationFixture(t, path, payload)
		if err := os.Chmod(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if !changed {
		writeIntegrationFixture(t, filepath.Join(f.next, "integration", APPLICATION+".service"), f.oldService)
		writeIntegrationFixture(t, filepath.Join(f.next, "integration", APPLICATION+".nginx"), f.oldNginx)
	}
	if err := os.Symlink(filepath.Join("releases", filepath.Base(f.previous)), filepath.Join(f.root, "current")); err != nil {
		t.Fatal(err)
	}
	for name, data := range f.dataFiles {
		path := filepath.Join(f.root, "data", name)
		writeIntegrationFixture(t, path, data)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		f.dataInodes[name] = info
	}
	f.installer = NewInstaller(InstallerOptions{
		RootDir: f.root, StateDir: f.state, ServicePath: f.manager.ServicePath, NginxPath: f.manager.NginxPath,
		Executable: f.processPath, BootID: "test-boot", ValidateHost: func(context.Context) error { return nil },
		Run: f.run, CheckProcess: func(pid int, path string) bool { return f.processAlive && pid == f.processPID && path == f.processPath },
	}, []ed25519.PublicKey{public}, "linux", f.arch)
	f.archive, f.digest = f.packageArchive(t, "2.0.0", private)
	return f
}

func (f *installerFixture) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.commands = append(f.commands, name+" "+strings.Join(args, " "))
	if name == "nginx" && f.failNginx {
		return nil, errors.New("nginx syntax failure")
	}
	if name == "systemctl" {
		if len(args) > 0 && args[0] == "show" {
			return []byte(strconv.Itoa(f.mainPID) + "\n"), nil
		}
		if len(args) > 0 && args[0] == "start" && containsInstallerArgument(args, portalUnit) && f.failPortalStart {
			return nil, errors.New("portal restart job rejected")
		}
		if reflect.DeepEqual(args, []string{"restart", recoveryUnit}) && f.failRecoveryStart {
			return nil, errors.New("recovery unit failed")
		}
	}
	return nil, nil
}

func containsInstallerArgument(args []string, wanted string) bool {
	for _, arg := range args {
		if arg == wanted {
			return true
		}
	}
	return false
}

func (f *installerFixture) packageArchive(t *testing.T, version string, key ed25519.PrivateKey) (*os.File, string) {
	t.Helper()
	var output bytes.Buffer
	manifest := Manifest{Version: version, OS: "linux", Arch: f.arch}
	sources := map[string]string{
		"payload/nanotail-portal":             filepath.Join(f.next, APPLICATION),
		"integration/nanotail-portal.service": filepath.Join(f.next, "integration", APPLICATION+".service"),
		"integration/nanotail-portal.nginx":   filepath.Join(f.next, "integration", APPLICATION+".nginx"),
	}
	if err := WritePackage(&output, manifest, sources, key); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(output.Bytes())
	return extractionArchive(t, output.Bytes()), hex.EncodeToString(digest[:])
}

func (f *installerFixture) prepare(t *testing.T) *installTransaction {
	t.Helper()
	result, err := f.installer.Prepare(t.Context(), f.archive, "2.0.0", f.digest)
	if err != nil {
		t.Fatal(err)
	}
	if result.Phase != "prepared" || result.Version != "2.0.0" || result.ID == "" || !f.installer.Pending() {
		t.Fatalf("unexpected preparation: %+v", result)
	}
	tx, err := loadTransaction(f.state)
	if err != nil {
		t.Fatal(err)
	}
	if tx.ID != result.ID || tx.PackageSHA256 != f.digest || tx.PreviousRelease != f.previous {
		t.Fatalf("incorrect persisted preparation: %+v", tx)
	}
	assertInstallerLink(t, filepath.Join(f.root, "current"), f.previous)
	f.commands = nil
	return tx
}

func (f *installerFixture) restarted(executable, boot string) *Installer {
	options := f.installer.options
	options.Executable, options.BootID, options.Gate = executable, boot, &maintenance.Gate{}
	return NewInstaller(options, []ed25519.PublicKey{f.public}, "linux", f.arch)
}

func (f *installerFixture) assertDataUnchanged(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(f.root, "data"))
	if err != nil || len(entries) != len(f.dataFiles) {
		t.Fatalf("persistent data topology changed: %v, %v", entries, err)
	}
	for name, want := range f.dataFiles {
		path := filepath.Join(f.root, "data", name)
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, want) {
			t.Fatalf("persistent data changed: %s, %v", name, err)
		}
		info, err := os.Stat(path)
		if err != nil || !os.SameFile(f.dataInodes[name], info) {
			t.Fatalf("persistent data inode replaced: %s, %v", name, err)
		}
	}
	if err := filepath.WalkDir(f.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.Contains(entry.Name(), ".sqlite3") && filepath.Dir(path) != filepath.Join(f.root, "data") {
			return fmt.Errorf("unexpected database copy: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func assertInstallerLink(t *testing.T, link, wanted string) {
	t.Helper()
	actual, err := filepath.EvalSymlinks(link)
	if err != nil || actual != wanted {
		t.Fatalf("release link %s = %q, want %q: %v", link, actual, wanted, err)
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("release pointer is not a symlink: %v, %v", info, err)
	}
}

func TestInstallerUsesFixedUpgradeDirectory(t *testing.T) {
	installer := NewInstaller(InstallerOptions{}, nil, TARGET_OS, TARGET_ARCH)
	if installer.options.RootDir != INSTALL_ROOT || installer.options.StateDir != UPGRADE_DIR || STAGING_DIR != filepath.Join(UPGRADE_DIR, "staging") {
		t.Fatalf("unexpected installation paths: %+v, staging=%q", installer.options, STAGING_DIR)
	}
}

func TestInstallerRecoveryUnitUsesFixedMode(t *testing.T) {
	f := newInstallerFixture(t, false)
	tx := f.prepare(t)
	unitPath := filepath.Join(filepath.Dir(tx.ServicePath), recoveryUnit)
	contents, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatal(err)
	}
	wantExec := "ExecStart=" + strconv.Quote(filepath.Join(tx.PreviousRelease, APPLICATION)) + " --upgrade-recover\n"
	wantCondition := "ConditionPathExists=" + strconv.Quote(filepath.Join(tx.StateDir, "pending.json")) + "\n"
	if !strings.Contains(string(contents), wantExec) || !strings.Contains(string(contents), wantCondition) || strings.Contains(string(contents), "--upgrade-recover=") {
		t.Fatalf("unexpected recovery unit:\n%s", contents)
	}
}

func TestInstallerRecoveryReadsOnlyItsJournal(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		t.Run(fmt.Sprintf("malformed-%t", malformed), func(t *testing.T) {
			root := t.TempDir()
			state := filepath.Join(root, "upgrade")
			if err := os.Mkdir(state, 0700); err != nil {
				t.Fatal(err)
			}
			dataPath := filepath.Join(root, "data")
			original := []byte("not a directory; recovery must leave data untouched")
			if err := os.WriteFile(dataPath, original, 0600); err != nil {
				t.Fatal(err)
			}
			if malformed {
				if err := os.WriteFile(filepath.Join(state, "pending.json"), []byte("invalid journal"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := runRecovery(t.Context(), state); (err != nil) != malformed {
				t.Fatalf("recovery result: %v", err)
			}
			after, err := os.ReadFile(dataPath)
			if err != nil || !bytes.Equal(after, original) {
				t.Fatalf("recovery changed data: %v", err)
			}
			entries, err := os.ReadDir(state)
			wantEntries := 0
			if malformed {
				wantEntries = 1
			}
			if err != nil || len(entries) != wantEntries {
				t.Fatalf("recovery initialized state: %v, %v", entries, err)
			}
		})
	}
}

func TestInstallerPrepareAndActivateVerifiedRelease(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprintf("changed-integrations-%t", changed), func(t *testing.T) {
			f := newInstallerFixture(t, changed)
			tx := f.prepare(t)
			if tx.Plan.ServiceChanged != changed || tx.Plan.NginxChanged != changed {
				t.Fatalf("incorrect integration plan: %+v", tx.Plan)
			}
			if err := validateExecutable(filepath.Join(tx.NextRelease, APPLICATION)); err != nil {
				t.Fatal(err)
			}
			if err := f.installer.Activate(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertInstallerLink(t, filepath.Join(f.root, "current"), tx.NextRelease)
			assertInstallerLink(t, filepath.Join(f.root, "previous"), f.previous)
			pending, err := loadTransaction(f.state)
			if err != nil || pending.Phase != "awaiting-ready" {
				t.Fatalf("activation state: %+v, %v", pending, err)
			}
			if changed {
				assertIntegrationFile(t, f.manager.ServicePath, f.newService, 0644)
				assertIntegrationFile(t, f.manager.NginxPath, f.newNginx, 0644)
				want := []string{"systemd-analyze verify " + filepath.Join(tx.NextRelease, "integration", APPLICATION+".service"), "systemctl daemon-reload", "nginx -t", "systemctl reload nginx"}
				if !reflect.DeepEqual(f.commands, want) {
					t.Fatalf("activation commands: %v, want %v", f.commands, want)
				}
			} else if len(f.commands) != 0 {
				t.Fatalf("unchanged integrations ran commands: %v", f.commands)
			}
			f.assertDataUnchanged(t)
		})
	}
}

func TestInstallerRejectsUnsupportedPlatformBeforeHostOverrides(t *testing.T) {
	for _, platform := range []struct{ os, arch string }{{"linux", "amd64"}, {"linux", "arm"}, {"linux", ""}, {"darwin", "arm64"}} {
		t.Run(platform.os+"-"+platform.arch, func(t *testing.T) {
			f := newInstallerFixture(t, false)
			hostChecks := 0
			options := f.installer.options
			options.ValidateHost = func(context.Context) error { hostChecks++; return nil }
			installer := NewInstaller(options, []ed25519.PublicKey{f.public}, platform.os, platform.arch)
			if installer.Supported() {
				t.Fatal("unsupported platform advertised installation support")
			}
			if err := installer.validateHost(t.Context()); !errors.Is(err, ErrInstallUnsupported) {
				t.Fatalf("host override bypassed the platform requirement: %v", err)
			}
			if _, err := installer.Prepare(t.Context(), f.archive, "2.0.0", f.digest); !errors.Is(err, ErrInstallUnsupported) {
				t.Fatalf("unsupported platform accepted installation: %v", err)
			}
			if hostChecks != 0 || len(f.commands) != 0 || installer.Pending() {
				t.Fatalf("unsupported platform performed installation work: checks=%d, commands=%v, pending=%t", hostChecks, f.commands, installer.Pending())
			}
			if _, err := os.Stat(f.state); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unsupported platform created installer state: %v", err)
			}
			assertInstallerLink(t, filepath.Join(f.root, "current"), f.previous)
			f.assertDataUnchanged(t)
		})
	}
}

func TestInstallerARM64ExecutableTypes(t *testing.T) {
	for _, fileType := range []elf.Type{elf.ET_EXEC, elf.ET_DYN, elf.ET_REL, elf.ET_CORE} {
		t.Run(fileType.String(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), APPLICATION)
			writeIntegrationFixture(t, path, installerELF(elf.EM_AARCH64, elf.ELFCLASS64, elf.ELFDATA2LSB, fileType))
			err := validateExecutable(path)
			allowed := fileType == elf.ET_EXEC || fileType == elf.ET_DYN
			if (err == nil) != allowed {
				t.Fatalf("ARM64 %s acceptance=%t: %v", fileType, allowed, err)
			}
		})
	}
}

func TestInstallerRejectsChangedSelectionAndUntrustedPackages(t *testing.T) {
	for _, scenario := range []string{"digest", "version", "signature", "executable", "machine", "class", "endianness"} {
		t.Run(scenario, func(t *testing.T) {
			f := newInstallerFixture(t, false)
			archive, version, digest := f.archive, "2.0.0", f.digest
			switch scenario {
			case "digest":
				digest = strings.Repeat("0", 64)
			case "version":
				version = "2.1.0"
			case "signature":
				_, wrong := serviceTestKeys(t)
				archive, digest = f.packageArchive(t, version, wrong)
			case "executable":
				writeIntegrationFixture(t, filepath.Join(f.next, APPLICATION), []byte("not an ELF executable"))
				archive, digest = f.packageArchive(t, version, f.private)
			case "machine", "class", "endianness":
				machine, class, encoding := elf.EM_AARCH64, elf.ELFCLASS64, elf.ELFDATA2LSB
				if scenario == "machine" {
					machine = elf.EM_X86_64
				} else if scenario == "class" {
					class = elf.ELFCLASS32
				} else {
					encoding = elf.ELFDATA2MSB
				}
				writeIntegrationFixture(t, filepath.Join(f.next, APPLICATION), installerELF(machine, class, encoding, elf.ET_EXEC))
				archive, digest = f.packageArchive(t, version, f.private)
			}
			if _, err := f.installer.Prepare(t.Context(), archive, version, digest); err == nil {
				t.Fatal("invalid release accepted")
			}
			if f.installer.Pending() {
				t.Fatal("failed preparation retained the maintenance gate")
			}
			if _, err := os.Stat(filepath.Join(f.state, "pending.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected release left a pending transaction: %v", err)
			}
			entries, err := os.ReadDir(filepath.Join(f.root, "releases"))
			if err != nil || len(entries) != 2 {
				t.Fatalf("rejected release left extracted files: %v, %v", entries, err)
			}
			assertInstallerLink(t, filepath.Join(f.root, "current"), f.previous)
			f.assertDataUnchanged(t)
		})
	}
}

func TestInstallerRecoveryRestoresInterruptedActivationWithoutTouchingData(t *testing.T) {
	for _, scenario := range []string{"nginx validation", "crash after link switch"} {
		t.Run(scenario, func(t *testing.T) {
			f := newInstallerFixture(t, true)
			tx := f.prepare(t)
			if scenario == "nginx validation" {
				f.failNginx = true
			}
			err := f.installer.Activate(t.Context())
			if scenario == "nginx validation" && err == nil {
				t.Fatal("invalid nginx configuration was accepted")
			}
			if scenario != "nginx validation" && err != nil {
				t.Fatal(err)
			}
			f.failNginx = false
			tx, err = loadTransaction(f.state)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "crash after link switch" {
				tx.Phase = "activating"
				if err := writeInstallJSON(f.state, "pending.json", tx); err != nil {
					t.Fatal(err)
				}
			}
			monitor := f.restarted(filepath.Join(f.previous, APPLICATION), "test-boot")
			done, err := monitor.recoverStep(t.Context(), time.Now().UTC())
			if err != nil || !done {
				t.Fatalf("recovery: done=%t, %v", done, err)
			}
			assertInstallerLink(t, filepath.Join(f.root, "current"), f.previous)
			assertIntegrationFile(t, f.manager.ServicePath, f.oldService, 0644)
			assertIntegrationFile(t, f.manager.NginxPath, f.oldNginx, 0644)
			if status := monitor.Status(); status == nil || status.Phase != "rolled-back" || status.CompletedAt == nil {
				t.Fatalf("missing recovery result: %+v", status)
			}
			if _, err := loadTransaction(f.state); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("recovery did not finish journal: %v", err)
			}
			f.assertDataUnchanged(t)
		})
	}
}

func TestInstallerReadinessRequiresStableCurrentSystemdProcess(t *testing.T) {
	f := newInstallerFixture(t, false)
	tx := f.prepare(t)
	if err := f.installer.Activate(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.processPath = filepath.Join(tx.NextRelease, APPLICATION)
	newPortal := f.restarted(f.processPath, "test-boot")
	if err := newPortal.BeforeStartup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !newPortal.Pending() {
		t.Fatal("writes were admitted before readiness confirmation")
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	if err := newPortal.MarkReady(ctx); err != nil {
		t.Fatal(err)
	}
	monitor := f.restarted(filepath.Join(f.previous, APPLICATION), "test-boot")
	now := time.Now().UTC()
	for _, offset := range []time.Duration{0, 9 * time.Second} {
		if done, err := monitor.recoverStep(t.Context(), now.Add(offset)); done || err != nil {
			t.Fatalf("premature confirmation: %t, %v", done, err)
		}
	}
	// A valid readiness marker from a process that is not systemd's MainPID
	// cannot count towards the stability interval.
	f.mainPID++
	if done, err := monitor.recoverStep(t.Context(), now.Add(10*time.Second)); done || err != nil {
		t.Fatalf("wrong MainPID accepted: %t, %v", done, err)
	}
	state, err := loadTransaction(f.state)
	if err != nil || state.ReadyPID != 0 || !state.ReadySince.IsZero() {
		t.Fatalf("stability interval was not reset: %+v, %v", state, err)
	}
	f.mainPID = f.processPID
	if done, err := monitor.recoverStep(t.Context(), now.Add(11*time.Second)); done || err != nil {
		t.Fatalf("confirmation did not restart interval: %t, %v", done, err)
	}
	// A fresh monitor must continue the persisted interval after its own restart.
	monitor = f.restarted(filepath.Join(f.previous, APPLICATION), "test-boot")
	if done, err := monitor.recoverStep(t.Context(), now.Add(20*time.Second)); done || err != nil {
		t.Fatalf("confirmation before ten stable seconds: %t, %v", done, err)
	}
	if done, err := monitor.recoverStep(t.Context(), now.Add(21*time.Second)); !done || err != nil {
		t.Fatalf("healthy confirmation failed: %t, %v", done, err)
	}
	if status := monitor.Status(); status == nil || status.Phase != "complete" || status.CompletedAt == nil {
		t.Fatalf("wrong completion: %+v", status)
	}
	assertInstallerLink(t, filepath.Join(f.root, "current"), tx.NextRelease)
	f.assertDataUnchanged(t)
}

func TestInstallerTimeoutOrRebootRecoversPersistedActivation(t *testing.T) {
	for _, scenario := range []string{"timeout", "reboot"} {
		t.Run(scenario, func(t *testing.T) {
			f := newInstallerFixture(t, false)
			f.prepare(t)
			if err := f.installer.Activate(t.Context()); err != nil {
				t.Fatal(err)
			}
			tx, err := loadTransaction(f.state)
			if err != nil {
				t.Fatal(err)
			}
			boot, now := "test-boot", tx.Deadline.Add(time.Second)
			if scenario == "reboot" {
				boot, now = "another-boot", time.Now().UTC()
			}
			monitor := f.restarted(filepath.Join(f.previous, APPLICATION), boot)
			if done, err := monitor.recoverStep(t.Context(), now); !done || err != nil {
				t.Fatalf("recovery failed: %t, %v", done, err)
			}
			assertInstallerLink(t, filepath.Join(f.root, "current"), f.previous)
			if status := monitor.Status(); status.Phase != "rolled-back" {
				t.Fatalf("wrong outcome: %+v", status)
			}
			f.assertDataUnchanged(t)
		})
	}
}

func TestInstallerRecoveryRetainsJournalUntilRestartJobAccepted(t *testing.T) {
	f := newInstallerFixture(t, true)
	f.prepare(t)
	if err := f.installer.Activate(t.Context()); err != nil {
		t.Fatal(err)
	}
	tx, err := loadTransaction(f.state)
	if err != nil {
		t.Fatal(err)
	}
	f.failPortalStart = true
	monitor := f.restarted(filepath.Join(f.previous, APPLICATION), "test-boot")
	if done, err := monitor.recoverStep(t.Context(), tx.Deadline.Add(time.Second)); done || err == nil {
		t.Fatalf("rejected restart was considered recovered: %t, %v", done, err)
	}
	state, err := loadTransaction(f.state)
	if err != nil || state.Phase != "rollback-failed" {
		t.Fatalf("retryable journal lost: %+v, %v", state, err)
	}
	assertInstallerLink(t, filepath.Join(f.root, "current"), f.previous)
	assertIntegrationFile(t, f.manager.ServicePath, f.oldService, 0644)
	f.failPortalStart = false
	monitor = f.restarted(filepath.Join(f.previous, APPLICATION), "test-boot")
	if done, err := monitor.recoverStep(t.Context(), tx.Deadline.Add(2*time.Second)); !done || err != nil {
		t.Fatalf("retry failed: %t, %v", done, err)
	}
	if _, err := loadTransaction(f.state); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("finished retry left journal: %v", err)
	}
	f.assertDataUnchanged(t)
}

func TestInstallerPreparedRequestWaitsForInitiatorAndRecoversItsExit(t *testing.T) {
	f := newInstallerFixture(t, false)
	f.prepare(t)
	monitor := f.restarted(filepath.Join(f.previous, APPLICATION), "test-boot")
	if done, err := monitor.recoverStep(t.Context(), time.Now().UTC()); done || err != nil {
		t.Fatalf("live initiator was aborted: %t, %v", done, err)
	}
	if len(f.commands) != 0 {
		t.Fatalf("waiting for initiator changed services: %v", f.commands)
	}
	if err := monitor.BeforeStartup(t.Context()); !errors.Is(err, ErrInstallPending) {
		t.Fatalf("prepared transaction admitted normal startup: %v", err)
	}
	f.processAlive = false
	if done, err := monitor.recoverStep(t.Context(), time.Now().UTC()); !done || err != nil {
		t.Fatalf("interrupted preparation was not canceled: %t, %v", done, err)
	}
	if status := monitor.Status(); status == nil || status.Phase != "aborted" {
		t.Fatalf("unexpected outcome: %+v", status)
	}
	assertInstallerLink(t, filepath.Join(f.root, "current"), f.previous)
	f.assertDataUnchanged(t)
}

func TestInstallerRecoveryStartFailureRejectsPreparation(t *testing.T) {
	f := newInstallerFixture(t, false)
	f.failRecoveryStart = true
	if _, err := f.installer.Prepare(t.Context(), f.archive, "2.0.0", f.digest); !errors.Is(err, ErrInstallFailed) {
		t.Fatalf("unmonitored upgrade accepted: %v", err)
	}
	if f.installer.Pending() {
		t.Fatal("rejected preparation retained maintenance gate")
	}
	if _, err := loadTransaction(f.state); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected preparation retained journal: %v", err)
	}
	assertInstallerLink(t, filepath.Join(f.root, "current"), f.previous)
	f.assertDataUnchanged(t)
}

func TestInstallerRetainsPublishedTransactionAfterSyncFailure(t *testing.T) {
	f := newInstallerFixture(t, false)
	f.installer.publishPending = func(tx *installTransaction) error {
		if err := writeInstallJSON(f.state, "pending.json", tx); err != nil {
			return err
		}
		return errors.New("injected directory sync failure after rename")
	}
	if _, err := f.installer.Prepare(t.Context(), f.archive, "2.0.0", f.digest); !errors.Is(err, ErrInstallFailed) {
		t.Fatalf("publish error: %v", err)
	}
	tx, err := loadTransaction(f.state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tx.NextRelease, APPLICATION)); err != nil {
		t.Fatalf("published transaction lost its release: %v", err)
	}
	if !f.installer.Pending() {
		t.Fatal("uncertain published transaction admitted new work")
	}
	if !containsInstallerArgument(f.commands, "systemctl restart "+recoveryUnit) {
		t.Fatal("published transaction was not handed to recovery")
	}
	monitor := f.restarted(filepath.Join(f.previous, APPLICATION), "test-boot")
	if done, err := monitor.recoverStep(t.Context(), time.Now().Add(time.Second)); !done || err != nil {
		t.Fatalf("recover failed preparation: %t %v", done, err)
	}
	if status := monitor.Status(); status == nil || status.Phase != "aborted" {
		t.Fatalf("unexpected failed-publish outcome: %+v", status)
	}
	assertInstallerLink(t, filepath.Join(f.root, "current"), f.previous)
	f.assertDataUnchanged(t)
}

func TestInstallerStartupReadinessDoesNotConsumeNewInstall(t *testing.T) {
	f := newInstallerFixture(t, false)
	if err := f.installer.BeforeStartup(t.Context()); err != nil {
		t.Fatal(err)
	}
	tx := f.prepare(t)
	// A request can arrive immediately after app.Start opens its listener.
	if err := f.installer.MarkReady(t.Context()); err != nil {
		t.Fatalf("new request mistaken for startup transaction: %v", err)
	}
	saved, err := loadTransaction(f.state)
	if err != nil || saved.ID != tx.ID || saved.Phase != "prepared" {
		t.Fatalf("startup changed new installation: %+v %v", saved, err)
	}
	if _, err := os.Stat(filepath.Join(f.state, "ready.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("old process wrote readiness for new package")
	}
}

func TestInstallerStartupReleasesGateWhenAlreadyConfirmed(t *testing.T) {
	f := newInstallerFixture(t, false)
	tx := f.prepare(t)
	if err := f.installer.Activate(t.Context()); err != nil {
		t.Fatal(err)
	}
	portal := f.restarted(filepath.Join(tx.NextRelease, APPLICATION), "test-boot")
	if err := portal.BeforeStartup(t.Context()); err != nil {
		t.Fatal(err)
	}
	tx.Phase = "complete"
	if err := finishInstallation(tx); err != nil {
		t.Fatal(err)
	}
	if err := portal.MarkReady(t.Context()); err != nil {
		t.Fatal(err)
	}
	if portal.Pending() {
		t.Fatal("completed startup remained in maintenance")
	}
}
