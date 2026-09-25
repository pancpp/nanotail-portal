package activityled

import (
	"errors"
	"fmt"
	"os"
)

// Both vendor and upstream device trees wire LED1 to GPIO4_PB1. SYS uses
// GPIO4_PB0 and must never be selected as a fallback.
// Vendor: friendlyarm/kernel-rockchip, rk3528-nanopi-rev01.dts (user_led).
// Upstream: torvalds/linux, rk3528-nanopi-zero2.dts (green:status).
type nanoPiZero2 struct{ fs sysfs }

func (*nanoPiZero2) Name() string { return "NanoPi Zero2" }

func (p *nanoPiZero2) OpenTrafficLED() (LED, error) {
	for _, name := range []string{"user_led", "green:status"} {
		path := "/sys/class/leds/" + name
		// Probe the driver attribute without creating directories or files.
		if _, err := p.fs.readFile(path + "/max_brightness"); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("probe NanoPi Zero2 LED1: %w", err)
		}
		led, err := openSysfsLED(p.fs, path)
		if err != nil {
			return nil, fmt.Errorf("open NanoPi Zero2 LED1 (%s): %w", name, err)
		}
		return led, nil
	}
	// A known board can still lack LED support in its kernel/device tree.
	return nil, nil
}
