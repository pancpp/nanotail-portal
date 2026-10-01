package conf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

func isolatePaths(t *testing.T) *pflag.FlagSet {
	t.Helper()
	isolateConfig(t)
	originalFlags, originalDirectory, originalResolved := pflag.CommandLine, dataDirectory, resolvedDataDirectory
	flags := pflag.NewFlagSet("paths", pflag.ContinueOnError)
	flags.StringVar(&dataDirectory, "data-dir", DEFAULT_DATA_DIR, "")
	pflag.CommandLine = flags
	resolvedDataDirectory = ""
	t.Setenv("NANOTAIL_DATA_DIR", "")
	gViper.SetConfigFile("nanotail-portal.yml")
	t.Cleanup(func() {
		pflag.CommandLine, dataDirectory, resolvedDataDirectory = originalFlags, originalDirectory, originalResolved
	})
	return flags
}

func TestDataDirectorySelection(t *testing.T) {
	flags := isolatePaths(t)
	if DataDir() != DEFAULT_DATA_DIR {
		t.Fatalf("default data directory = %q", DataDir())
	}
	t.Setenv("NANOTAIL_DATA_DIR", "environment-data")
	if DataDir() != "environment-data" {
		t.Fatal("environment override ignored")
	}
	if err := flags.Parse([]string{"--data-dir", "cli-data"}); err != nil {
		t.Fatal(err)
	}
	if DataDir() != "cli-data" {
		t.Fatal("CLI did not override the environment")
	}
	before, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := PrepareDataDir(); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(before, "cli-data")
	if DataDir() != want {
		t.Fatalf("resolved directory = %q, want %q", DataDir(), want)
	}
	if current, err := os.Getwd(); err != nil || current != before {
		t.Fatal("startup changed the working directory")
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("data directory missing: %v", err)
	}
	// All paths stay bound to the selected directory after initialization.
	t.Setenv("NANOTAIL_DATA_DIR", "another-data")
	t.Chdir(t.TempDir())
	if DataDir() != want {
		t.Fatal("selected directory changed after startup")
	}
}

func TestRuntimePathsAndResetReload(t *testing.T) {
	isolatePaths(t)
	dir := filepath.Join(t.TempDir(), "data")
	t.Setenv("NANOTAIL_DATA_DIR", dir)
	if err := PrepareDataDir(); err != nil {
		t.Fatal(err)
	}
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	for got, name := range map[string]string{ConfigFile(): "nanotail-portal.yml", DatabasePath(): "nanotail-portal.sqlite3", LogDir(): "logs", DataPath("nanotail-portal.key"): "nanotail-portal.key"} {
		if want := filepath.Join(dir, name); got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
	}
	info, err := os.Stat(ConfigFile())
	if err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatalf("new configuration permissions: %v %v", info, err)
	}
	const config = "data_dir: /must-not-be-used\ndatabase: custom.sqlite3\nlog_dir: custom-logs\n"
	if err := os.WriteFile(ConfigFile(), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if DataDir() != dir || DatabasePath() != filepath.Join(dir, "custom.sqlite3") || LogDir() != filepath.Join(dir, "custom-logs") {
		t.Fatal("relative configuration paths did not use the selected data directory")
	}
	if err := os.WriteFile(ConfigFile(), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if DatabasePath() != filepath.Join(dir, "nanotail-portal.sqlite3") || LogDir() != filepath.Join(dir, "logs") {
		t.Fatal("empty configuration did not restore defaults within the same data directory")
	}
}

func TestCustomConfigAndAbsoluteStoragePaths(t *testing.T) {
	for _, absoluteConfig := range []bool{false, true} {
		t.Run(map[bool]string{false: "relative", true: "absolute"}[absoluteConfig], func(t *testing.T) {
			isolatePaths(t)
			dir, outside := t.TempDir(), t.TempDir()
			t.Setenv("NANOTAIL_DATA_DIR", dir)
			config := "custom.yml"
			if absoluteConfig {
				config = filepath.Join(outside, config)
			}
			gViper.SetConfigFile(config)
			if err := Init(); err != nil {
				t.Fatal(err)
			}
			wantConfig := filepath.Join(dir, "custom.yml")
			if absoluteConfig {
				wantConfig = config
			}
			if ConfigFile() != wantConfig {
				t.Fatalf("config path = %q, want %q", ConfigFile(), wantConfig)
			}
			gViper.Set("database", filepath.Join(outside, "custom.sqlite3"))
			gViper.Set("log_dir", filepath.Join(outside, "logs"))
			if DatabasePath() != filepath.Join(outside, "custom.sqlite3") || LogDir() != filepath.Join(outside, "logs") {
				t.Fatal("absolute storage paths were rebased")
			}
		})
	}
}

func TestPrepareDataDirRejectsInvalidStartup(t *testing.T) {
	for _, scenario := range []string{"empty", "file", "arguments"} {
		t.Run(scenario, func(t *testing.T) {
			flags := isolatePaths(t)
			dir := filepath.Join(t.TempDir(), "data")
			args := []string{"--data-dir", dir}
			switch scenario {
			case "empty":
				args = []string{"--data-dir="}
			case "file":
				if err := os.WriteFile(dir, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			case "arguments":
				args = append(args, "db", "init")
			}
			if err := flags.Parse(args); err != nil {
				t.Fatal(err)
			}
			err := PrepareDataDir()
			if err == nil {
				t.Fatal("invalid startup accepted")
			}
			if scenario == "arguments" {
				if !strings.Contains(err.Error(), "unexpected arguments") {
					t.Fatal(err)
				}
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Fatal("rejected command created data directory")
				}
			}
		})
	}
}
