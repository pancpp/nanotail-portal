package factoryreset

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const marker = ".nanotail-reset-pending"
const markerContents = "nanotail factory reset v1\n"

var ErrPaths = errors.New("Factory reset requires nanotail.yml, nanotail.sqlite3, and logs in the portal working directory; custom storage paths must be reset manually")

// Files deliberately operates on a fixed allowlist, never paths supplied by an
// HTTP request or an unchecked log_dir. Root also confines operations if paths
// are replaced with symlinks while the process is running.
type Files struct {
	root      *os.Root
	directory string
}

func Open(directory string) (*Files, error) {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	return &Files{root: root, directory: directory}, nil
}

func (f *Files) Close() error { return f.root.Close() }

func (f *Files) ValidatePaths(config, database, logs string) error {
	for _, target := range [][2]string{{config, "nanotail.yml"}, {database, "nanotail.sqlite3"}, {logs, "logs"}} {
		absolute, err := filepath.Abs(target[0])
		if err != nil || absolute != filepath.Join(f.directory, target[1]) {
			return ErrPaths
		}
	}
	return f.validate()
}

func (f *Files) validate() error {
	for _, name := range []string{"nanotail.yml", "nanotail.sqlite3", "nanotail.key", "nanotail.sqlite3-wal", "nanotail.sqlite3-shm", "nanotail.sqlite3-journal", marker, "logs"} {
		info, err := f.root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing factory reset: %s is a symbolic link", name)
		}
		if name == "logs" {
			if !info.IsDir() {
				return errors.New("refusing factory reset: logs is not a directory")
			}
		} else {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("refusing factory reset: %s is not a regular file", name)
			}
			if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Nlink != 1 {
				return fmt.Errorf("refusing factory reset: %s has multiple hard links", name)
			}
		}
	}
	return nil
}

func (f *Files) syncDirectory() error {
	dir, err := f.root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (f *Files) write(name, contents string, exclusive bool) error {
	flags := os.O_WRONLY | os.O_CREATE | syscall.O_NOFOLLOW
	if exclusive {
		flags |= os.O_EXCL
	}
	file, err := f.root.OpenFile(name, flags, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	// Check the opened inode before truncation, including a replacement hard link.
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Nlink != 1 || !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to clear unsafe reset target %s", name)
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := io.WriteString(file, contents); err != nil {
		return err
	}
	return file.Sync()
}

// Prepare records intent after successful logout. The process then re-execs,
// so even logger compression goroutines are gone before Resume clears files.
func (f *Files) Prepare() error {
	if err := f.validate(); err != nil {
		return err
	}
	if _, err := f.root.Stat(marker); errors.Is(err, os.ErrNotExist) {
		if err := f.write(marker, markerContents, true); err != nil {
			return err
		}
		if err := f.syncDirectory(); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return nil
}

// Clear only runs before services start, including on recovery from a crash.
// The marker remains until every operation has completed and been synced.
func (f *Files) Clear() error {
	if err := f.Prepare(); err != nil {
		return err
	}
	if err := f.write("nanotail.yml", "", false); err != nil {
		return err
	}
	if err := f.write("nanotail.sqlite3", "", false); err != nil {
		return err
	}
	// The next startup generates a new signing key, invalidating every JWT
	// issued before this reset, including tokens saved in other browsers.
	if err := f.root.Remove("nanotail.key"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// SQLite sidecars can otherwise restore pre-reset data when SQLite reopens.
	for _, name := range []string{"nanotail.sqlite3-wal", "nanotail.sqlite3-shm", "nanotail.sqlite3-journal"} {
		if err := f.root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := f.root.RemoveAll("logs"); err != nil {
		return err
	}
	if err := f.root.Mkdir("logs", 0755); err != nil {
		return err
	}
	if err := f.syncDirectory(); err != nil {
		return err
	}
	if err := f.root.Remove(marker); err != nil {
		return err
	}
	return f.syncDirectory()
}

func (f *Files) Resume() error {
	file, err := f.root.OpenFile(marker, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, 128))
	if err != nil {
		return err
	}
	if string(contents) != markerContents {
		return errors.New("invalid factory reset recovery marker; manual recovery required")
	}
	return f.Clear()
}

// LockInstance prevents another portal in the same working directory from
// continuing to write while a factory reset is clearing its files.
func (f *Files) LockInstance() (*os.File, error) {
	lock, err := f.root.OpenFile(".nanotail-portal.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("another portal is running in this working directory: %w", err)
	}
	return lock, nil
}
