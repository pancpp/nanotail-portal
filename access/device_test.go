package access

import (
	"errors"
	"os"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// GetDeviceID reads a fixed hardware path, so these checks inspect the current
// device without writing to sysfs or substituting a test-only implementation.
func TestGetDeviceID(t *testing.T) {
	t.Parallel()
	const DEVICE_ID_PATH = "/sys/firmware/devicetree/base/serial-number"
	serialNumber, readErr := os.ReadFile(DEVICE_ID_PATH)
	deviceID, err := GetDeviceID()
	if readErr != nil {
		var want, got *os.PathError
		if !errors.As(readErr, &want) || !errors.As(err, &got) {
			t.Fatalf("GetDeviceID error = %v, want original filesystem error %v", err, readErr)
		}
		if deviceID != "" || got.Op != want.Op || got.Path != want.Path || !errors.Is(err, want.Err) {
			t.Fatalf("GetDeviceID did not preserve the filesystem error: ID = %q, error = %v", deviceID, err)
		}
		return
	}

	// Remove trailing device-tree terminators, then trim surrounding whitespace.
	want := strings.TrimSpace(strings.TrimRight(string(serialNumber), "\x00"))
	if want == "" || !utf8.ValidString(want) || strings.ContainsFunc(want, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) {
		if deviceID != "" || !errors.Is(err, ErrInvalidDeviceID) {
			t.Fatalf("GetDeviceID = (%q, %v), want an empty ID and ErrInvalidDeviceID", deviceID, err)
		}
		return
	}
	if err != nil {
		t.Fatalf("GetDeviceID failed for the available device-tree serial: %v", err)
	}
	if deviceID != want {
		t.Fatalf("GetDeviceID = %q, want the trimmed device-tree serial %q", deviceID, want)
	}
}

func TestGetDeviceSignaturePlaceholder(t *testing.T) {
	t.Parallel()
	if signature, err := GetDeviceSignature(); signature != "" || !errors.Is(err, ErrDeviceSignatureUnavailable) {
		t.Fatalf("placeholder = (%q, %v), want explicit unavailable error", signature, err)
	}
}
