package auth

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func isolatedKey(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	previous := gJwtSigningKey
	gJwtSigningKey = nil
	t.Cleanup(func() { gJwtSigningKey = previous })
}

func verifyToken(token string) error {
	_, err := jwt.ParseWithClaims(token, new(Claims), func(*jwt.Token) (any, error) {
		return GetJwtSignKey(), nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	return err
}

func TestKeyGeneratedForMissingOrEmptyFile(t *testing.T) {
	for _, initial := range []string{"missing", "", " \n\t"} {
		t.Run(fmtKeyCase(initial), func(t *testing.T) {
			isolatedKey(t)
			if initial != "missing" {
				if err := os.WriteFile(SigningKeyFile, []byte(initial), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := Init(); err != nil {
				t.Fatal(err)
			}
			key := GetJwtSignKey()
			random, err := hex.DecodeString(string(key))
			if err != nil || len(random) != 32 {
				t.Fatal("generated key must contain 256 random bits encoded as hex")
			}
			stored, err := os.ReadFile(SigningKeyFile)
			if err != nil || !bytes.Equal(stored, key) {
				t.Fatal("active key does not match persisted key")
			}
			info, err := os.Stat(SigningKeyFile)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("generated key is not owner-only")
			}
			token, err := CreateJwtToken(1)
			if err != nil {
				t.Fatal(err)
			}
			if err := Init(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(GetJwtSignKey(), key) || verifyToken(token) != nil {
				t.Fatal("reinitialization rotated the key or invalidated a session")
			}
			leftovers, err := filepath.Glob(".nanotail-portal-key-*")
			if err != nil || len(leftovers) != 0 {
				t.Fatal("temporary key files were not cleaned up")
			}
		})
	}
}

func fmtKeyCase(value string) string {
	if value == "missing" {
		return "missing"
	}
	if value == "" {
		return "empty"
	}
	return "whitespace"
}

func TestExistingKeyIsLoadedWithoutReplacement(t *testing.T) {
	isolatedKey(t)
	const contents = "existing-custom-signing-key\n"
	if err := os.WriteFile(SigningKeyFile, []byte(contents), 0400); err != nil {
		t.Fatal(err)
	}
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if string(GetJwtSignKey()) != strings.TrimSpace(contents) {
		t.Fatal("existing key was not used")
	}
	stored, err := os.ReadFile(SigningKeyFile)
	if err != nil || string(stored) != contents {
		t.Fatal("nonempty key file was changed")
	}
	copy := GetJwtSignKey()
	copy[0] ^= 0xff
	if string(GetJwtSignKey()) != strings.TrimSpace(contents) {
		t.Fatal("getter exposed mutable signing-key storage")
	}
}

func TestDeletedKeyInvalidatesExistingJWTs(t *testing.T) {
	isolatedKey(t)
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	oldKey := GetJwtSignKey()
	oldToken, err := CreateJwtToken(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(SigningKeyFile); err != nil {
		t.Fatal(err)
	}
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(oldKey, GetJwtSignKey()) {
		t.Fatal("deleted key was reused")
	}
	if !errors.Is(verifyToken(oldToken), jwt.ErrTokenSignatureInvalid) {
		t.Fatal("token signed before key deletion was accepted")
	}
	newToken, err := CreateJwtToken(1)
	if err != nil || verifyToken(newToken) != nil {
		t.Fatal("new token was not signed with the replacement key")
	}
}

func TestKeyErrorsDoNotFallbackOrReplaceFiles(t *testing.T) {
	for _, kind := range []string{"directory", "symlink", "dangling symlink", "hard link", "FIFO", "oversized", "unreadable"} {
		t.Run(kind, func(t *testing.T) {
			isolatedKey(t)
			outside := filepath.Join(t.TempDir(), "untouched")
			if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "directory":
				err = os.Mkdir(SigningKeyFile, 0700)
			case "symlink":
				err = os.Symlink(outside, SigningKeyFile)
			case "dangling symlink":
				err = os.Symlink(outside+"-missing", SigningKeyFile)
			case "hard link":
				err = os.Link(outside, SigningKeyFile)
			case "FIFO":
				err = syscall.Mkfifo(SigningKeyFile, 0600)
			case "oversized":
				err = os.WriteFile(SigningKeyFile, bytes.Repeat([]byte("x"), maxSigningKeySize+1), 0600)
			case "unreadable":
				if os.Geteuid() == 0 {
					t.Skip("root bypasses file permissions")
				}
				err = os.WriteFile(SigningKeyFile, []byte("keep"), 0000)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := Init(); err == nil {
				t.Fatal("unsafe/unreadable key was accepted")
			}
			if len(GetJwtSignKey()) != 0 {
				t.Fatal("failed initialization installed a fallback key")
			}
			if _, err := CreateJwtToken(1); err == nil {
				t.Fatal("signed a token without initializing a key")
			}
			contents, err := os.ReadFile(outside)
			if err != nil || string(contents) != "keep" {
				t.Fatal("changed a file outside the key target")
			}
		})
	}
}

func TestKeyWriteFailurePreventsInitialization(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	isolatedKey(t)
	if err := os.Chmod(".", 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(".", 0700) })
	if err := Init(); err == nil {
		t.Fatal("initialized despite failing to persist a signing key")
	}
	if len(GetJwtSignKey()) != 0 {
		t.Fatal("used an ephemeral key after persistence failed")
	}
	if _, err := CreateJwtToken(1); err == nil {
		t.Fatal("issued a token with an unpersisted key")
	}
}
