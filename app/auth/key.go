package auth

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const SigningKeyFile = "nanotail.key"
const maxSigningKeySize = 4096

// Initialized once at startup, before any HTTP handlers are created.
var gJwtSigningKey []byte

func Init() error {
	gJwtSigningKey = nil
	key, err := loadSigningKey(SigningKeyFile)
	if err != nil {
		return fmt.Errorf("initialize JWT signing key: %w", err)
	}
	gJwtSigningKey = key
	return nil
}

func GetJwtSignKey() []byte {
	return bytes.Clone(gJwtSigningKey)
}

func loadSigningKey(path string) ([]byte, error) {
	// Do not follow links or read devices/FIFOs. Only missing/empty files may
	// generate a key; permission and other I/O errors must not rotate sessions.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err == nil {
		key, readErr := readSigningKey(file)
		closeErr := file.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(key) != 0 {
			return key, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return nil, err
	}
	key := []byte(hex.EncodeToString(random))
	// main holds the working-directory instance lock. Publish the full key
	// atomically so a crash cannot leave a truncated but nonempty signing key.
	dir := filepath.Dir(path)
	temporary, err := os.CreateTemp(dir, ".nanotail-key-*")
	if err != nil {
		return nil, err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	defer temporary.Close()
	if _, err := temporary.Write(key); err != nil {
		return nil, err
	}
	if err := temporary.Sync(); err != nil {
		return nil, err
	}
	if err := temporary.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return nil, err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return nil, err
	}
	return key, nil
}

func readSigningKey(file *os.File) ([]byte, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("nanotail.key must be a regular file")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Nlink != 1 {
		return nil, errors.New("nanotail.key must not have multiple hard links")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maxSigningKeySize+1))
	if err != nil {
		return nil, err
	}
	if len(contents) > maxSigningKeySize {
		return nil, errors.New("nanotail.key exceeds the maximum size of 4096 bytes")
	}
	// Treat whitespace-only files as empty; tolerate a newline in text files.
	return bytes.TrimSpace(contents), nil
}
