package activityled

import (
	"context"
	"errors"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/pancpp/nanotail-portal/device"
)

type trafficSample = device.TrafficSample
type samplerFunc func(context.Context) (trafficSample, error)

func (f samplerFunc) Sample(ctx context.Context) (trafficSample, error) { return f(ctx) }

type fakeLED struct {
	mu               sync.Mutex
	writes           []bool
	closed           int
	setErr, closeErr error
}

func (l *fakeLED) Set(on bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writes = append(l.writes, on)
	return l.setErr
}
func (l *fakeLED) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed++
	return l.closeErr
}
func (l *fakeLED) pulses() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, on := range l.writes {
		if on {
			n++
		}
	}
	return n
}
func (l *fakeLED) on() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.writes) > 0 && l.writes[len(l.writes)-1]
}

func (l *fakeLED) fail(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.setErr = err
}

func sampleAt(at time.Time, rx, tx uint64) trafficSample {
	return trafficSample{InterfaceName: "tailscale0", CounterEpoch: "boot:1", SampledAt: at, RxBytes: rx, TxBytes: tx}
}

func TestRateTracker(t *testing.T) {
	start := time.Unix(100, 0)
	var rate rateTracker
	if got := rate.update(sampleAt(start, 100, 200), nil, start); got != 0 {
		t.Fatal(got)
	}
	now := start.Add(sampleInterval)
	if got := rate.update(sampleAt(now, 200, 600), nil, now); got != 2000 {
		t.Fatalf("RX+TX rate = %v", got)
	}
	now = now.Add(sampleInterval)
	if got := rate.update(sampleAt(now, 200, 600), nil, now); got != 0 {
		t.Fatalf("idle = %v", got)
	}

	rate = rateTracker{}
	rate.update(sampleAt(start, math.MaxUint64-100, math.MaxUint64-100), nil, start)
	now = start.Add(time.Second)
	if got := rate.update(sampleAt(now, math.MaxUint64, math.MaxUint64), nil, now); got != 200 {
		t.Fatal(got)
	}
	rate = rateTracker{}
	rate.update(sampleAt(start, 0, 0), nil, start)
	if got := rate.update(sampleAt(now, math.MaxUint64, math.MaxUint64), nil, now); got != 2*float64(math.MaxUint64) {
		t.Fatalf("overflowed RX+TX sum: %v", got)
	}
}

func TestRateTrackerDiscontinuities(t *testing.T) {
	start := time.Unix(100, 0)
	for _, name := range []string{"epoch", "rx reset", "tx reset", "equal time", "backwards", "gap",
		"unavailable", "wrong interface", "empty epoch", "zero time", "future", "stale"} {
		t.Run(name, func(t *testing.T) {
			var rate rateTracker
			rate.update(sampleAt(start, 100, 100), nil, start)
			now := start.Add(time.Second)
			sample := sampleAt(now, 200, 200)
			var err error
			switch name {
			case "epoch":
				sample.CounterEpoch = "boot:2"
			case "rx reset":
				sample.RxBytes = 0
			case "tx reset":
				sample.TxBytes = 0
			case "equal time":
				sample.SampledAt = start
			case "backwards":
				sample.SampledAt = start.Add(-time.Millisecond)
			case "gap":
				now = start.Add(3 * time.Second)
				sample.SampledAt = now
			case "unavailable":
				err = os.ErrNotExist
			case "wrong interface":
				sample.InterfaceName = "eth0"
			case "empty epoch":
				sample.CounterEpoch = ""
			case "zero time":
				sample.SampledAt = time.Time{}
			case "future":
				sample.SampledAt = now.Add(time.Second)
			case "stale":
				now = now.Add(3 * time.Second)
			}
			if got := rate.update(sample, err, now); got != 0 {
				t.Fatalf("discontinuity rate = %v", got)
			}
			// Following valid samples establish a baseline and resume normally.
			now = now.Add(time.Second)
			rate.update(sampleAt(now, 300, 300), nil, now)
			now = now.Add(time.Second)
			if got := rate.update(sampleAt(now, 400, 400), nil, now); got != 200 {
				t.Fatalf("recovery rate = %v", got)
			}
		})
	}
}

func TestBlinkRateTiers(t *testing.T) {
	for _, tc := range []struct {
		rate   float64
		period time.Duration
	}{
		{0, 0}, {-1, 0}, {math.NaN(), 0}, {math.Inf(1), 0},
		{1, time.Second}, {16*1024 - 1, time.Second}, {16 * 1024, 500 * time.Millisecond},
		{128*1024 - 1, 500 * time.Millisecond}, {128 * 1024, 250 * time.Millisecond},
		{1024*1024 - 1, 250 * time.Millisecond}, {1024 * 1024, 125 * time.Millisecond},
		{8*1024*1024 - 1, 125 * time.Millisecond}, {8 * 1024 * 1024, 100 * time.Millisecond},
		{1e12, 100 * time.Millisecond},
	} {
		if got := periodForRate(tc.rate); got != tc.period {
			t.Errorf("period(%v) = %v, want %v", tc.rate, got, tc.period)
		}
	}
}

func TestMonitorSamplingAndBlinking(t *testing.T) {
	for _, tc := range []struct {
		name           string
		bytesPerSample uint64
		pulses         int
	}{
		{"idle", 0, 0}, {"light", 100, 2}, {"moderate", 4096, 4},
		{"busy", 32768, 8}, {"heavy", 262144, 16}, {"saturated", 2097152, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				led := &fakeLED{}
				var count int
				source := samplerFunc(func(context.Context) (trafficSample, error) {
					count++
					return sampleAt(time.Now(), uint64(count)*tc.bytesPerSample, 0), nil
				})
				done := make(chan error, 1)
				go func() { done <- newMonitor(led, source).run(ctx) }()
				time.Sleep(2225 * time.Millisecond)
				synctest.Wait()
				if count != 9 {
					t.Errorf("samples = %d, want 9", count)
				}
				if got := led.pulses(); got != tc.pulses {
					t.Errorf("pulses = %d, want %d", got, tc.pulses)
				}
				if len(led.writes) == 0 || led.writes[0] {
					t.Error("did not begin with LED off")
				}
				cancel()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				if led.closed != 1 {
					t.Fatalf("close count = %d", led.closed)
				}
			})
		})
	}
}

func TestMonitorOutageRecoveryAndSpeedChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		led := &fakeLED{}
		var counter uint64
		var increment atomic.Uint64
		increment.Store(1)
		var unavailable atomic.Bool
		source := samplerFunc(func(context.Context) (trafficSample, error) {
			counter += increment.Load()
			if unavailable.Load() {
				return trafficSample{}, os.ErrNotExist
			}
			return sampleAt(time.Now(), counter, 0), nil
		})
		done := make(chan error, 1)
		go func() { done <- newMonitor(led, source).run(ctx) }()
		time.Sleep(275 * time.Millisecond)
		synctest.Wait()
		if !led.on() {
			t.Fatal("traffic did not light LED")
		}
		unavailable.Store(true)
		time.Sleep(250 * time.Millisecond)
		synctest.Wait()
		if led.on() {
			t.Fatal("unavailable interface left LED on")
		}
		unavailable.Store(false)
		increment.Store(2097152)
		time.Sleep(250 * time.Millisecond)
		synctest.Wait()
		if led.on() {
			t.Fatal("recovery baseline invented traffic")
		}
		before := led.pulses()
		time.Sleep(time.Second)
		synctest.Wait()
		if got := led.pulses() - before; got != 8 {
			t.Fatalf("high-rate recovery pulses = %d, want 8", got)
		}
		increment.Store(0)
		time.Sleep(250 * time.Millisecond)
		synctest.Wait()
		if led.on() {
			t.Fatal("idle traffic left LED on")
		}
		before = led.pulses()
		time.Sleep(time.Second)
		synctest.Wait()
		if led.pulses() != before {
			t.Fatal("idle traffic kept blinking")
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestMonitorPulseWidthAndPhase(t *testing.T) {
	led := &fakeLED{}
	m := newMonitor(led, nil)
	m.period = time.Second
	start := time.Unix(100, 0)
	for _, tc := range []struct {
		elapsed time.Duration
		on      bool
	}{
		{0, true}, {25 * time.Millisecond, true}, {50 * time.Millisecond, false},
		{250 * time.Millisecond, false}, {500 * time.Millisecond, false},
		{975 * time.Millisecond, false}, {time.Second, true},
	} {
		if err := m.render(start.Add(tc.elapsed)); err != nil {
			t.Fatal(err)
		}
		if m.state != tc.on {
			t.Errorf("state at %v = %v", tc.elapsed, m.state)
		}
	}
	m.period = 0
	if err := m.render(start.Add(time.Second + time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if m.state || !m.lastPulse.IsZero() {
		t.Fatal("idle did not reset pulse")
	}
}

func TestMonitorFailuresReleaseLED(t *testing.T) {
	source := samplerFunc(func(context.Context) (trafficSample, error) {
		t.Fatal("failed LED should stop polling")
		return trafficSample{}, nil
	})
	led := &fakeLED{setErr: os.ErrPermission, closeErr: os.ErrNotExist}
	if err := newMonitor(led, source).run(t.Context()); !errors.Is(err, os.ErrPermission) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("run = %v", err)
	}
	if led.closed != 1 {
		t.Fatal("LED not released on error")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	led = &fakeLED{}
	if err := newMonitor(led, source).run(ctx); err != nil {
		t.Fatal(err)
	}
	if led.closed != 1 || len(led.writes) != 0 {
		t.Fatal("cancelled monitor touched or retained LED")
	}
}

func TestMonitorOutputFailureDuringTraffic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		led := &fakeLED{}
		var counter uint64
		source := samplerFunc(func(context.Context) (trafficSample, error) {
			counter++
			return sampleAt(time.Now(), counter, 0), nil
		})
		done := make(chan error, 1)
		go func() { done <- newMonitor(led, source).run(t.Context()) }()
		time.Sleep(275 * time.Millisecond)
		synctest.Wait()
		led.fail(os.ErrPermission)
		if err := <-done; !errors.Is(err, os.ErrPermission) {
			t.Fatalf("run = %v", err)
		}
		if led.closed != 1 {
			t.Fatal("failed LED was not restored")
		}
	})
}
