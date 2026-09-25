// Package activityled displays VPN traffic through an optional platform LED.
// Unsupported hardware never causes startup to fail or touches arbitrary LEDs.
package activityled

import (
	"bytes"
	"errors"
	"os"
)

// LED is the hardware-independent output used by the traffic monitor. Calls are
// serialized by the monitor. Close releases ownership and restores prior state.
type LED interface {
	Set(on bool) error
	Close() error
}

// Platform maps the logical traffic indicator to the appropriate hardware.
// A nil LED with no error means the platform has no supported indicator.
// New platforms can implement any LED transport, not just Linux sysfs.
type Platform interface {
	Name() string
	OpenTrafficLED() (LED, error)
}

type noLEDPlatform struct{}

func (noLEDPlatform) Name() string                 { return "unsupported" }
func (noLEDPlatform) OpenTrafficLED() (LED, error) { return nil, nil }

// Detect selects a board by its exact device-tree compatible string, never by
// the presence of a similarly named LED on an unrelated board.
func Detect() (Platform, error) {
	return detect(os.ReadFile, systemSysfs())
}

func detect(readFile func(string) ([]byte, error), fs sysfs) (Platform, error) {
	for _, path := range []string{"/sys/firmware/devicetree/base/compatible", "/proc/device-tree/compatible"} {
		data, err := readFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return noLEDPlatform{}, err
		}
		for _, compatible := range bytes.Split(data, []byte{0}) {
			switch string(compatible) {
			case "friendlyelec,nanopi-zero2", "friendlyarm,nanopi-zero2":
				return &nanoPiZero2{fs: fs}, nil
			}
		}
		return noLEDPlatform{}, nil
	}
	return noLEDPlatform{}, nil
}
