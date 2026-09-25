package activityled

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestDetectPlatform(t *testing.T) {
	for _, tc := range []struct {
		name, compatible  string
		missing, fallback bool
		readErr           error
		want              string
	}{
		{name: "vendor", compatible: "friendlyelec,nanopi-zero2\x00rockchip,rk3528\x00", want: "NanoPi Zero2"},
		{name: "upstream", compatible: "friendlyarm,nanopi-zero2\x00rockchip,rk3528\x00", want: "NanoPi Zero2"},
		{name: "proc fallback", compatible: "friendlyarm,nanopi-zero2\x00", fallback: true, want: "NanoPi Zero2"},
		{name: "different board", compatible: "friendlyarm,nanopi-r2s\x00", want: "unsupported"},
		{name: "not substring", compatible: "friendlyarm,nanopi-zero2-other\x00", want: "unsupported"},
		{name: "no device tree", missing: true, want: "unsupported"},
		{name: "empty device tree", want: "unsupported"},
		{name: "unreadable", readErr: os.ErrPermission, want: "unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var paths []string
			platform, err := detect(func(path string) ([]byte, error) {
				paths = append(paths, path)
				if tc.missing || tc.fallback && strings.HasPrefix(path, "/sys/") {
					return nil, os.ErrNotExist
				}
				return []byte(tc.compatible), tc.readErr
			}, sysfs{})
			if !errors.Is(err, tc.readErr) || platform.Name() != tc.want {
				t.Fatalf("detect = %s, %v; want %s, %v", platform.Name(), err, tc.want, tc.readErr)
			}
			wantReads := 1
			if tc.missing || tc.fallback {
				wantReads = 2
			}
			if len(paths) != wantReads {
				t.Fatalf("reads = %v", paths)
			}
			if tc.want == "unsupported" {
				if led, err := platform.OpenTrafficLED(); led != nil || err != nil {
					t.Fatalf("unsupported LED = %v, %v", led, err)
				}
			}
		})
	}
}

func TestNanoPiLEDSelection(t *testing.T) {
	for _, name := range []string{"user_led", "green:status"} {
		t.Run(name, func(t *testing.T) {
			fs := newFakeSysfs("/sys/class/leds/"+name, "default-on")
			led, err := (&nanoPiZero2{fs: fs.io()}).OpenTrafficLED()
			if err != nil || led == nil {
				t.Fatalf("open = %v, %v", led, err)
			}
			if err := led.Set(true); err != nil {
				t.Fatal(err)
			}
			if err := led.Close(); err != nil {
				t.Fatal(err)
			}
			for _, write := range fs.writes {
				if !strings.HasPrefix(write.path, "/sys/class/leds/"+name+"/") {
					t.Errorf("wrote to wrong LED: %+v", write)
				}
			}
		})
	}
	t.Run("never use SYS or unrelated LEDs", func(t *testing.T) {
		fs := newFakeSysfs("/sys/class/leds/sys_led", "heartbeat")
		led, err := (&nanoPiZero2{fs: fs.io()}).OpenTrafficLED()
		if err != nil || led != nil || len(fs.writes) != 0 {
			t.Fatalf("missing LED1 = %v, %v, writes %v", led, err, fs.writes)
		}
	})
	t.Run("unreadable LED stops probing", func(t *testing.T) {
		fs := newFakeSysfs("/sys/class/leds/green:status", "none")
		fs.readErrors["/sys/class/leds/user_led/max_brightness"] = os.ErrPermission
		led, err := (&nanoPiZero2{fs: fs.io()}).OpenTrafficLED()
		if led != nil || !errors.Is(err, os.ErrPermission) || len(fs.reads) != 1 || len(fs.writes) != 0 {
			t.Fatalf("unreadable LED = %v, %v; reads %v, writes %v", led, err, fs.reads, fs.writes)
		}
	})
}

func TestNoLEDDoesNotPollTraffic(t *testing.T) {
	source := samplerFunc(func(context.Context) (sample trafficSample, err error) {
		t.Fatal("unsupported platform polled traffic")
		return
	})
	if err := runPlatform(t.Context(), noLEDPlatform{}, source); err != nil {
		t.Fatal(err)
	}
}
