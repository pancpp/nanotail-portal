package factoryreset

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func setupFiles(t *testing.T) (*Files, string) {
	t.Helper()
	dir := t.TempDir()
	f, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	for _, name := range []string{"nanotail.yml", "nanotail.sqlite3", "nanotail.key", "nanotail.sqlite3-wal", "nanotail.sqlite3-shm", "nanotail.sqlite3-journal", "keep.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("old data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "logs", "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "logs", "nested", "old.log"), []byte("old logs"), 0600); err != nil {
		t.Fatal(err)
	}
	return f, dir
}

func assertCleared(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"nanotail.yml", "nanotail.sqlite3"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || len(data) != 0 {
			t.Fatalf("%s not empty: %q, %v", name, data, err)
		}
	}
	for _, name := range []string{marker, "nanotail.key", "nanotail.sqlite3-wal", "nanotail.sqlite3-shm", "nanotail.sqlite3-journal"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s still exists: %v", name, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "logs"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("logs not empty: %v, %v", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "keep.txt"))
	if err != nil || string(data) != "old data" {
		t.Fatalf("unrelated file changed: %q, %v", data, err)
	}
}

func TestClearOnlyResetTargetsAndRecover(t *testing.T) {
	f, dir := setupFiles(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "logs", "link")); err != nil {
		t.Fatal(err)
	}
	if err := f.ValidatePaths(filepath.Join(dir, "nanotail.yml"), filepath.Join(dir, "nanotail.sqlite3"), filepath.Join(dir, "logs")); err != nil {
		t.Fatal(err)
	}
	if err := f.Prepare(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "nanotail.sqlite3"))
	if string(data) != "old data" {
		t.Fatal("preparation cleared data before restart")
	}
	// Simulate a crash after just the configuration has been cleared.
	if err := os.WriteFile(filepath.Join(dir, "nanotail.yml"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.Resume(); err != nil {
		t.Fatal(err)
	}
	assertCleared(t, dir)
	if data, err := os.ReadFile(filepath.Join(outside, "keep")); err != nil || string(data) != "outside" {
		t.Fatal("followed symlink outside logs")
	}
	if err := f.Resume(); err != nil {
		t.Fatal(err)
	}
}

func TestResetRejectsUnsafeTargetsBeforeClearing(t *testing.T) {
	for _, name := range []string{"nanotail.yml", "nanotail.sqlite3", "nanotail.key", "nanotail.sqlite3-wal", "logs"} {
		t.Run(name, func(t *testing.T) {
			f, dir := setupFiles(t)
			if err := os.Rename(filepath.Join(dir, name), filepath.Join(dir, "original")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("original", filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
			if err := f.Clear(); err == nil {
				t.Fatal("accepted symlink")
			}
			if _, err := os.Stat(filepath.Join(dir, marker)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("marked unsafe reset pending")
			}
		})
	}
	t.Run("hard link", func(t *testing.T) {
		f, dir := setupFiles(t)
		if err := os.Link(filepath.Join(dir, "nanotail.sqlite3"), filepath.Join(dir, "copy")); err != nil {
			t.Fatal(err)
		}
		if err := f.Clear(); err == nil {
			t.Fatal("accepted multiply linked database")
		}
		data, _ := os.ReadFile(filepath.Join(dir, "nanotail.yml"))
		if string(data) != "old data" {
			t.Fatal("cleared config before safety checks")
		}
	})
}

func TestResetRejectsCustomOrBroadPaths(t *testing.T) {
	f, dir := setupFiles(t)
	config, db, logs := filepath.Join(dir, "nanotail.yml"), filepath.Join(dir, "nanotail.sqlite3"), filepath.Join(dir, "logs")
	for _, paths := range [][3]string{{config, db, dir}, {config, db, "/"}, {config, db, os.Getenv("HOME")}, {config, filepath.Join(dir, "other.sqlite3"), logs}, {filepath.Join(dir, "other.yml"), db, logs}, {config, config, logs}} {
		if err := f.ValidatePaths(paths[0], paths[1], paths[2]); !errors.Is(err, ErrPaths) {
			t.Fatalf("unsafe paths %v: %v", paths, err)
		}
	}
}

func TestResetToleratesAlreadyDeletedKey(t *testing.T) {
	f, dir := setupFiles(t)
	if err := os.Remove(filepath.Join(dir, "nanotail.key")); err != nil {
		t.Fatal(err)
	}
	if err := f.Clear(); err != nil {
		t.Fatal(err)
	}
	assertCleared(t, dir)
}

func TestRecoveryRequiresValidMarker(t *testing.T) {
	f, dir := setupFiles(t)
	if err := f.Resume(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, marker), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.Resume(); err == nil {
		t.Fatal("accepted invalid recovery marker")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "nanotail.sqlite3"))
	if string(data) != "old data" {
		t.Fatal("invalid marker erased data")
	}
}

func TestInstanceLock(t *testing.T) {
	f, _ := setupFiles(t)
	lock, err := f.LockInstance()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	if other, err := f.LockInstance(); err == nil {
		other.Close()
		t.Fatal("second portal acquired lock")
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := f.LockInstance()
	if err != nil {
		t.Fatal(err)
	}
	other.Close()
}

func TestControllerOnlyAcceptsOneReset(t *testing.T) {
	c := NewController(func() error { return nil })
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if err := c.Accept(); err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, ErrBusy) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 1 || !c.Pending() {
		t.Fatal("duplicate reset accepted")
	}
	c.Schedule()
	<-c.Requests()
	c.RetryAllowed()
	if err := c.Accept(); err != nil {
		t.Fatal(err)
	}
}

func TestControllerPreflightFailureAllowsRetry(t *testing.T) {
	failure := errors.New("unsafe")
	c := NewController(func() error { return failure })
	if err := c.Accept(); !errors.Is(err, failure) || c.Pending() {
		t.Fatal("failed preflight scheduled reset")
	}
	select {
	case <-c.Requests():
		t.Fatal("unexpected scheduled reset")
	default:
	}
	failure = nil
	if err := c.Accept(); err != nil {
		t.Fatal(err)
	}
}
