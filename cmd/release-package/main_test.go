package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pancpp/nanotail-portal/upgrade"
)

func TestKeygenCreateVerifyAndNoOverwrite(t *testing.T) {
	dir := t.TempDir()
	privatePath := filepath.Join(dir, "private.pem")
	publicPath := filepath.Join(dir, "public.pem")
	var output bytes.Buffer
	keygenArgs := []string{"keygen", "-private-key", privatePath, "-public-key", publicPath}
	if err := run(keygenArgs, &output); err != nil {
		t.Fatal(err)
	}
	privateBytes, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := upgrade.ParsePrivateKey(privateBytes); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(privatePath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private key must have mode 0600: %v, %v", info, err)
	}
	if bytes.Contains(output.Bytes(), privateBytes) {
		t.Fatal("key generation printed the private key")
	}
	if err := run(keygenArgs, &output); err == nil {
		t.Fatal("key generation overwrote existing signing keys")
	}
	unchanged, err := os.ReadFile(privatePath)
	if err != nil || !bytes.Equal(privateBytes, unchanged) {
		t.Fatal("existing private key changed")
	}
	binaryPath := filepath.Join(dir, "portal")
	if err := os.WriteFile(binaryPath, []byte("test release executable"), 0700); err != nil {
		t.Fatal(err)
	}
	packagePath := filepath.Join(dir, "release.tar.gz")
	// The signing tool may run on any build host; both commands must default
	// to the device's Linux/ARM64 target instead of the host architecture.
	createArgs := []string{"create", "-binary", binaryPath, "-version", "v2.3.4", "-private-key", privatePath, "-output", packagePath}
	if err := run(createArgs, &output); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"verify", "-package", packagePath, "-public-key", publicPath}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Verified nanotail-portal v2.3.4 for linux/arm64.") {
		t.Fatalf("verification used the wrong default platform: %s", &output)
	}
	if err := run([]string{"verify", "-package", packagePath, "-public-key", publicPath, "-os", "linux", "-arch", "arm64"}, &output); err != nil {
		t.Fatal(err)
	}
	if err := run(createArgs, &output); err == nil {
		t.Fatal("package creation overwrote an existing release")
	}
	if err := run([]string{"verify", "-package", packagePath, "-public-key", publicPath, "-os", "linux", "-arch", "amd64"}, &output); err == nil {
		t.Fatal("verification accepted the wrong device architecture")
	}
}

func TestRejectUnsupportedPlatformBeforeOpeningInputs(t *testing.T) {
	for _, platform := range [][2]string{
		{"linux", "amd64"}, {"linux", "riscv64"}, {"linux", "arm"},
		{"darwin", "arm64"}, {"windows", "arm64"}, {"", "arm64"}, {"linux", ""},
	} {
		t.Run(platform[0]+"/"+platform[1], func(t *testing.T) {
			dir := t.TempDir()
			packagePath := filepath.Join(dir, "release.tar.gz")
			for _, args := range [][]string{
				{"create", "-binary", filepath.Join(dir, "missing-binary"), "-version", "v2.3.4", "-private-key", filepath.Join(dir, "missing-private-key"), "-output", packagePath},
				{"verify", "-package", packagePath, "-public-key", filepath.Join(dir, "missing-public-key")},
			} {
				args = append(args, "-os", platform[0], "-arch", platform[1])
				var output bytes.Buffer
				err := run(args, &output)
				if err == nil || !strings.Contains(err.Error(), "unsupported package platform") {
					t.Fatalf("%s did not reject the platform before reading inputs: %v", args[0], err)
				}
				if output.Len() != 0 {
					t.Fatalf("%s printed success for an unsupported platform", args[0])
				}
			}
			if _, err := os.Lstat(packagePath); !os.IsNotExist(err) {
				t.Fatalf("created output for an unsupported platform: %v", err)
			}
		})
	}
}

func TestKeygenPreservesExistingPublicKey(t *testing.T) {
	dir := t.TempDir()
	privatePath := filepath.Join(dir, "private.pem")
	publicPath := filepath.Join(dir, "public.pem")
	if err := os.WriteFile(publicPath, []byte("existing trusted key"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"keygen", "-private-key", privatePath, "-public-key", publicPath}, &bytes.Buffer{}); err == nil {
		t.Fatal("overwrote public key")
	}
	if _, err := os.Stat(privatePath); !os.IsNotExist(err) {
		t.Fatal("left an incomplete key pair")
	}
	data, err := os.ReadFile(publicPath)
	if err != nil || string(data) != "existing trusted key" {
		t.Fatal("existing public key changed")
	}
}
