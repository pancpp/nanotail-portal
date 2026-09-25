package activityled

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type sysfs struct {
	readFile  func(string) ([]byte, error)
	writeFile func(string, string) error
}

func systemSysfs() sysfs {
	return sysfs{readFile: os.ReadFile, writeFile: writeAttribute}
}

// sysfs attributes already exist: never use O_CREATE or replace them via rename.
func writeAttribute(path, value string) error {
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(value)
	return errors.Join(writeErr, file.Close())
}

type sysfsLED struct {
	fs                      sysfs
	path                    string
	max, originalBrightness int
	originalTrigger         string
	triggerPresent          bool
	parameters              map[string]string
	state, stateKnown       bool
	closed                  bool
}

func openSysfsLED(fs sysfs, path string) (_ *sysfsLED, err error) {
	led := &sysfsLED{fs: fs, path: path, parameters: make(map[string]string)}
	if led.max, err = led.readNumber("max_brightness"); err != nil {
		return nil, err
	}
	if led.max == 0 {
		return nil, errors.New("LED max_brightness is zero")
	}
	if led.originalBrightness, err = led.readNumber("brightness"); err != nil {
		return nil, err
	}
	if led.originalBrightness > led.max {
		return nil, errors.New("LED brightness exceeds maximum")
	}
	data, err := fs.readFile(path + "/trigger")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		led.triggerPresent = true
		for _, value := range strings.Fields(string(data)) {
			if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
				if led.originalTrigger != "" {
					return nil, errors.New("ambiguous LED trigger")
				}
				led.originalTrigger = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
			}
		}
		// Do not disrupt arbitrary trigger-specific configuration we cannot
		// restore, such as another service's netdev or pattern trigger.
		var parameters []string
		switch led.originalTrigger {
		case "none", "default-on":
		case "timer":
			parameters = []string{"delay_on", "delay_off"}
		case "heartbeat":
			parameters = []string{"invert"}
		default:
			return nil, fmt.Errorf("LED has unsupported active trigger %q; leave it unchanged", led.originalTrigger)
		}
		for _, parameter := range parameters {
			value, err := led.readNumber(parameter)
			if err != nil {
				return nil, err
			}
			led.parameters[parameter] = strconv.Itoa(value)
		}
		if err := fs.writeFile(path+"/trigger", "none"); err != nil {
			return nil, errors.Join(err, led.Close())
		}
	}
	return led, nil
}

func (l *sysfsLED) readNumber(attribute string) (int, error) {
	data, err := l.fs.readFile(l.path + "/" + attribute)
	if err != nil {
		return 0, err
	}
	value, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid LED %s", attribute)
	}
	return value, nil
}

func (l *sysfsLED) Set(on bool) error {
	if l.closed {
		return errors.New("LED is closed")
	}
	if l.stateKnown && l.state == on {
		return nil
	}
	value := 0
	if on {
		value = l.max
	}
	if err := l.fs.writeFile(l.path+"/brightness", strconv.Itoa(value)); err != nil {
		return err
	}
	l.state, l.stateKnown = on, true
	return nil
}

func (l *sysfsLED) Close() error {
	if l.closed {
		return nil
	}
	l.closed = true
	// Restore brightness first: writing zero after restoring a timer trigger
	// would disable that trigger again.
	err := l.fs.writeFile(l.path+"/brightness", strconv.Itoa(l.originalBrightness))
	if l.triggerPresent {
		err = errors.Join(err, l.fs.writeFile(l.path+"/trigger", l.originalTrigger))
		for _, parameter := range []string{"delay_on", "delay_off", "invert"} {
			if value, ok := l.parameters[parameter]; ok {
				err = errors.Join(err, l.fs.writeFile(l.path+"/"+parameter, value))
			}
		}
	}
	return err
}
