package conf

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/spf13/viper"
)

// Tests replace global configuration serially and restore it during cleanup.
// Chdir also confines any files created by Init to the test's temporary directory.
func isolateConfig(t *testing.T) string {
	t.Helper()
	t.Chdir(t.TempDir())
	original := gViper
	v := viper.New()
	// Retain the defaults installed by the package's init function instead of
	// duplicating their setup in the test fixture.
	for key, value := range original.AllSettings() {
		v.SetDefault(key, value)
	}
	v.SetEnvPrefix(original.GetEnvPrefix())
	path := filepath.Join(t.TempDir(), "nanotail-portal.yml")
	v.SetConfigFile(path)
	gViper = v
	t.Cleanup(func() { gViper = original })
	return path
}

func TestDefaults(t *testing.T) {
	// Check the actual package configuration before any file is loaded.
	if got := gViper.ConfigFileUsed(); got != "nanotail-portal.yml" {
		t.Errorf("configuration file = %q, want nanotail-portal.yml", got)
	}
	t.Setenv("NANOTAIL_DATA_DIR", "")
	if got := ConfigFile(); got != filepath.Join(DEFAULT_DATA_DIR, "nanotail-portal.yml") {
		t.Errorf("configuration path = %q", got)
	}
	if got := gViper.GetEnvPrefix(); got != "NANOTAIL" {
		t.Errorf("environment prefix = %q, want NANOTAIL", got)
	}
	for key, want := range map[string]string{
		"http_listen_addr":  "127.0.0.1:7080",
		"log_dir":           "logs",
		"database":          "nanotail-portal.sqlite3",
		"tailscale_binary":  "/usr/bin/tailscale",
		"tailscale_socket":  "",
		"tailscale_timeout": "15s",
	} {
		if got := GetString(key); got != want {
			t.Errorf("GetString(%q) = %q, want %q", key, got, want)
		}
	}
	if GetBool("enable_console_log") {
		t.Error("console logging should be disabled by default")
	}
}

func TestInitOverridesDefaults(t *testing.T) {
	path := isolateConfig(t)
	const data = "http_listen_addr: ':9090'\nsession_ttl: 2h\nenable_console_log: false\ndatabase: custom.sqlite3\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"http_listen_addr": ":9090",
		"session_ttl":      "2h",
		"database":         "custom.sqlite3",
		"log_dir":          "logs", // An omitted setting retains its default.
	} {
		if got := GetString(key); got != want {
			t.Errorf("GetString(%q) = %q, want %q", key, got, want)
		}
	}
	if GetBool("enable_console_log") {
		t.Error("explicit false should keep console logging disabled")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != data {
		t.Error("Init modified the configuration file")
	}
}

func TestInitEmptyConfiguration(t *testing.T) {
	path := isolateConfig(t)
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if got := GetString("http_listen_addr"); got != "127.0.0.1:7080" {
		t.Fatalf("empty configuration replaced the default address: %q", got)
	}
}

func TestInitInvalidYAML(t *testing.T) {
	path := isolateConfig(t)
	if err := os.WriteFile(path, []byte("bad: ["), 0600); err != nil {
		t.Fatal(err)
	}
	var parseErr viper.ConfigParseError
	if err := Init(); !errors.As(err, &parseErr) {
		t.Fatalf("Init() = %v, want a YAML parse error", err)
	}
}

func TestInitMissingConfiguration(t *testing.T) {
	path := isolateConfig(t)
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || len(data) != 0 {
		t.Fatalf("missing configuration was not created empty: %q, %v", data, err)
	}
}

func TestTypedGetters(t *testing.T) {
	path := isolateConfig(t)
	const data = `retry_count: 3
byte_limit: 5000000000
enabled: true
names:
  - nanopi
  - gateway
updated_at: "2026-09-22T12:34:56Z"
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if got := GetInt("retry_count"); got != 3 {
		t.Errorf("GetInt() = %d, want 3", got)
	}
	if got := GetInt64("byte_limit"); got != 5_000_000_000 {
		t.Errorf("GetInt64() = %d, want 5000000000", got)
	}
	if !GetBool("enabled") {
		t.Error("GetBool() did not read true")
	}
	if got := GetStringSlice("names"); !slices.Equal(got, []string{"nanopi", "gateway"}) {
		t.Errorf("GetStringSlice() = %v, want [nanopi gateway]", got)
	}
	wantTime := time.Date(2026, time.September, 22, 12, 34, 56, 0, time.UTC)
	if got := GetTime("updated_at"); !got.Equal(wantTime) {
		t.Errorf("GetTime() = %v, want %v", got, wantTime)
	}
}

func TestGetVersion(t *testing.T) {
	originalVersion, originalTime, originalHash, originalNumber := gVersion, gBuildTime, gGitHash, gBuildNumber
	t.Cleanup(func() {
		gVersion, gBuildTime, gGitHash, gBuildNumber = originalVersion, originalTime, originalHash, originalNumber
	})
	gVersion, gBuildTime, gGitHash, gBuildNumber = "0.1.0", "2026-09-22T12:34:56Z", "abc123", "42"
	version, buildTime, gitHash, buildNumber := GetVersion()
	if version != gVersion || buildTime != gBuildTime || gitHash != gGitHash || buildNumber != gBuildNumber {
		t.Fatalf("GetVersion() = (%q, %q, %q, %q)", version, buildTime, gitHash, buildNumber)
	}
}
