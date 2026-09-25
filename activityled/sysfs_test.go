package activityled

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type attributeWrite struct{ path, value string }
type fakeSysfs struct {
	files                   map[string]string
	readErrors, writeErrors map[string]error
	reads                   []string
	writes                  []attributeWrite
}

func newFakeSysfs(path, trigger string) *fakeSysfs {
	return &fakeSysfs{
		files: map[string]string{
			path + "/max_brightness": "255\n", path + "/brightness": "7\n",
			path + "/trigger":  "none timer heartbeat default-on [" + trigger + "]\n",
			path + "/delay_on": "123\n", path + "/delay_off": "456\n", path + "/invert": "1\n",
		},
		readErrors: make(map[string]error), writeErrors: make(map[string]error),
	}
}

func (f *fakeSysfs) io() sysfs {
	return sysfs{
		readFile: func(path string) ([]byte, error) {
			f.reads = append(f.reads, path)
			if err := f.readErrors[path]; err != nil {
				return nil, err
			}
			value, ok := f.files[path]
			if !ok {
				return nil, os.ErrNotExist
			}
			return []byte(value), nil
		},
		writeFile: func(path, value string) error {
			f.writes = append(f.writes, attributeWrite{path, value})
			if err := f.writeErrors[path]; err != nil {
				return err
			}
			if _, ok := f.files[path]; !ok {
				return os.ErrNotExist
			}
			f.files[path] = value
			return nil
		},
	}
}

func TestSysfsRestoresOriginalState(t *testing.T) {
	for _, trigger := range []string{"none", "default-on", "timer", "heartbeat", "absent"} {
		t.Run(trigger, func(t *testing.T) {
			const path = "/fake/led"
			fs := newFakeSysfs(path, trigger)
			if trigger == "absent" {
				delete(fs.files, path+"/trigger")
			}
			led, err := openSysfsLED(fs.io(), path)
			if err != nil {
				t.Fatal(err)
			}
			for _, on := range []bool{false, false, true, true, false} {
				if err := led.Set(on); err != nil {
					t.Fatal(err)
				}
			}
			if err := led.Close(); err != nil {
				t.Fatal(err)
			}
			if err := led.Close(); err != nil {
				t.Fatal(err)
			}
			if err := led.Set(true); err == nil {
				t.Error("write after close succeeded")
			}
			var want []attributeWrite
			if trigger != "absent" {
				want = append(want, attributeWrite{path + "/trigger", "none"})
			}
			want = append(want, attributeWrite{path + "/brightness", "0"}, attributeWrite{path + "/brightness", "255"},
				attributeWrite{path + "/brightness", "0"}, attributeWrite{path + "/brightness", "7"})
			if trigger != "absent" {
				want = append(want, attributeWrite{path + "/trigger", trigger})
			}
			if trigger == "timer" {
				want = append(want, attributeWrite{path + "/delay_on", "123"}, attributeWrite{path + "/delay_off", "456"})
			}
			if trigger == "heartbeat" {
				want = append(want, attributeWrite{path + "/invert", "1"})
			}
			if !reflect.DeepEqual(fs.writes, want) {
				t.Fatalf("writes = %v, want %v", fs.writes, want)
			}
		})
	}
}

func TestSysfsRejectsInvalidOrOwnedLEDWithoutWrites(t *testing.T) {
	for _, tc := range []struct{ attribute, value string }{
		{"max_brightness", "0"}, {"max_brightness", "-1"}, {"max_brightness", "invalid"},
		{"brightness", "256"}, {"brightness", "-1"}, {"brightness", ""},
		{"trigger", "[netdev]"}, {"trigger", "[pattern]"}, {"trigger", "none timer"},
		{"trigger", "[none] [timer]"}, {"delay_on", "-1"},
	} {
		t.Run(tc.attribute+"="+tc.value, func(t *testing.T) {
			fs := newFakeSysfs("/fake/led", "timer")
			fs.files["/fake/led/"+tc.attribute] = tc.value
			if led, err := openSysfsLED(fs.io(), "/fake/led"); led != nil || err == nil {
				t.Fatalf("open = %v, %v", led, err)
			}
			if len(fs.writes) != 0 {
				t.Fatalf("modified invalid LED: %v", fs.writes)
			}
		})
	}
	t.Run("permission denied reading trigger", func(t *testing.T) {
		fs := newFakeSysfs("/fake/led", "none")
		fs.readErrors["/fake/led/trigger"] = os.ErrPermission
		if _, err := openSysfsLED(fs.io(), "/fake/led"); !errors.Is(err, os.ErrPermission) {
			t.Fatal(err)
		}
		if len(fs.writes) != 0 {
			t.Fatal(fs.writes)
		}
	})
}

func TestSysfsWriteFailuresAndCleanup(t *testing.T) {
	const path = "/fake/led"
	t.Run("failed acquisition attempts restoration", func(t *testing.T) {
		fs := newFakeSysfs(path, "default-on")
		fs.writeErrors[path+"/trigger"] = os.ErrPermission
		if led, err := openSysfsLED(fs.io(), path); led != nil || !errors.Is(err, os.ErrPermission) {
			t.Fatalf("open = %v, %v", led, err)
		}
		want := []attributeWrite{{path + "/trigger", "none"}, {path + "/brightness", "7"}, {path + "/trigger", "default-on"}}
		if !reflect.DeepEqual(fs.writes, want) {
			t.Fatal(fs.writes)
		}
	})
	t.Run("failed set can retry and cleanup keeps going", func(t *testing.T) {
		fs := newFakeSysfs(path, "timer")
		led, err := openSysfsLED(fs.io(), path)
		if err != nil {
			t.Fatal(err)
		}
		fs.writeErrors[path+"/brightness"] = os.ErrPermission
		if err := led.Set(true); !errors.Is(err, os.ErrPermission) {
			t.Fatal(err)
		}
		delete(fs.writeErrors, path+"/brightness")
		if err := led.Set(true); err != nil {
			t.Fatal(err)
		}
		if fs.files[path+"/brightness"] != "255" {
			t.Fatal("failed Set incorrectly cached state")
		}
		fs.writeErrors[path+"/brightness"] = os.ErrNotExist
		if err := led.Close(); !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if fs.files[path+"/trigger"] != "timer" || fs.files[path+"/delay_off"] != "456" {
			t.Fatal("brightness error prevented trigger restoration")
		}
	})
}

func TestWriteAttributeNeverCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brightness")
	if err := writeAttribute(path, "1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeAttribute(path, "0"); err != nil {
		t.Fatal(err)
	}
	value, err := os.ReadFile(path)
	if err != nil || string(value) != "0" {
		t.Fatalf("value = %q, %v", value, err)
	}
}
