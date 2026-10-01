package access

import (
	"log"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

func GetDeviceID() (string, error) {
	const DEVICE_ID_PATH = "/sys/firmware/devicetree/base/serial-number"

	serialNumber, err := os.ReadFile(DEVICE_ID_PATH)
	if err != nil {
		log.Println("(deviceID) fail to read device ID, err:", err)
		return "", err
	}
	// Remove device-tree NUL terminators, then trim surrounding whitespace.
	deviceID := strings.TrimSpace(strings.TrimRight(string(serialNumber), "\x00"))
	if deviceID == "" || !utf8.ValidString(deviceID) || strings.IndexFunc(deviceID, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) != -1 {
		return "", ErrInvalidDeviceID
	}
	return deviceID, nil
}

func GetDeviceSignature() (string, error) {
	// TODO: Load the device's provisioned signature when its source is available.
	// nanotail-server expects padded standard Base64 of an Ed25519 signature
	// over "nanotail-server/auth/device/v1\x00" followed by the exact device ID.
	// Until then, skip reporting instead of sending a fabricated credential.
	return "", ErrDeviceSignatureUnavailable
}
