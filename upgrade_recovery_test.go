//go:build embedwebui

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestUpgradeRecoveryDoesNotInitializePortal(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess integration test")
	}
	binary := buildFactoryResetPortal(t)
	// The subprocess fixture overlays the fixed installation root so these
	// tests never access the host's real upgrade journal.
	state := filepath.Join(filepath.Dir(binary), "installation", "upgrade")
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	for _, malformed := range []bool{false, true} {
		name := "no_pending_installation"
		if malformed {
			name = "malformed_journal"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			data := filepath.Join(root, "data-must-not-be-opened")
			original := []byte("not a data directory; recovery must never initialize it")
			if err := os.WriteFile(data, original, 0600); err != nil {
				t.Fatal(err)
			}
			if malformed {
				if err := os.WriteFile(filepath.Join(state, "pending.json"), []byte("invalid json"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			command := exec.Command(binary, "--upgrade-recover", "--data-dir="+data, "--config="+filepath.Join(root, "missing-config.yml"))
			command.Env = append(os.Environ(), "NANOTAIL_TEST_TAILSCALE_BINARY=/must-not-run-tailscale")
			output, err := command.CombinedOutput()
			if (err != nil) != malformed {
				t.Fatalf("recovery: %v %s", err, output)
			}
			after, err := os.ReadFile(data)
			if err != nil || !bytes.Equal(after, original) {
				t.Fatalf("recovery changed portal data: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "missing-config.yml")); !os.IsNotExist(err) {
				t.Fatal("recovery created portal configuration")
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 1 {
				t.Fatalf("recovery initialized unexpected files: %v %v", entries, err)
			}
		})
	}
	t.Run("state_directory_override_is_rejected", func(t *testing.T) {
		command := exec.Command(binary, "--upgrade-recover="+t.TempDir())
		command.Env = append(os.Environ(), "NANOTAIL_TEST_TAILSCALE_BINARY=/must-not-run-tailscale")
		if output, err := command.CombinedOutput(); err == nil || !bytes.Contains(output, []byte("invalid argument")) {
			t.Fatalf("recovery accepted a configurable directory: %v %s", err, output)
		}
	})
}
