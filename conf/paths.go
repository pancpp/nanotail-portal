package conf

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/pflag"
)

const (
	DEFAULT_DATA_DIR = "/srv/nanotail-portal/data"
)

var (
	dataDirectory         = DEFAULT_DATA_DIR
	resolvedDataDirectory string
)

// DataDir is selected before reading YAML, so a configuration file cannot move
// its own database, signing key, or reset boundary by changing this setting.
func DataDir() string {
	if resolvedDataDirectory != "" {
		return resolvedDataDirectory
	}
	if flag := pflag.Lookup("data-dir"); flag == nil || !flag.Changed {
		if directory := os.Getenv("NANOTAIL_DATA_DIR"); directory != "" {
			return directory
		}
	}
	return dataDirectory
}

// PrepareDataDir runs before the instance lock and any configuration writes.
// It resolves relative overrides once without changing the process directory.
func PrepareDataDir() error {
	if err := validateArguments(); err != nil {
		return err
	}
	if DataDir() == "" {
		return fmt.Errorf("data directory must not be empty")
	}
	directory, err := filepath.Abs(DataDir())
	if err != nil {
		return fmt.Errorf("resolve data directory: %w", err)
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	resolvedDataDirectory = directory
	return nil
}

// DataPath resolves a runtime path against the data directory. Absolute custom
// paths remain supported, but factory reset only permits its fixed defaults.
func DataPath(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(DataDir(), path)
}

func DatabasePath() string {
	return DataPath(GetString("database"))
}

func LogDir() string {
	return DataPath(GetString("log_dir"))
}

func validateArguments() error {
	if pflag.NArg() != 0 {
		return fmt.Errorf("unexpected arguments %q: database initialization and migrations now run automatically at startup", pflag.Args())
	}
	return nil
}
