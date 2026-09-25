package activityled

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/pancpp/nanotail-portal/device"
)

const (
	sampleInterval = 250 * time.Millisecond
	renderInterval = 25 * time.Millisecond
	pulseWidth     = 50 * time.Millisecond
	maxSampleGap   = 2 * time.Second
)

type Sampler interface {
	Sample(context.Context) (device.TrafficSample, error)
}

// Run is optional and independent of the database and browser. Unknown boards
// and missing LEDs return immediately without starting a traffic polling loop.
func Run(ctx context.Context, source Sampler) error {
	if ctx.Err() != nil {
		return nil
	}
	platform, err := Detect()
	if err != nil {
		return err
	}
	return runPlatform(ctx, platform, source)
}

func runPlatform(ctx context.Context, platform Platform, source Sampler) error {
	if ctx.Err() != nil {
		return nil
	}
	led, err := platform.OpenTrafficLED()
	if err != nil {
		return err
	}
	if led == nil {
		return nil
	}
	if err := newMonitor(led, source).run(ctx); err != nil {
		return fmt.Errorf("%s traffic LED: %w", platform.Name(), err)
	}
	return nil
}

type monitor struct {
	led        LED
	source     Sampler
	rate       rateTracker
	period     time.Duration
	lastPulse  time.Time
	state      bool
	nextSample time.Time
}

func newMonitor(led LED, source Sampler) *monitor {
	return &monitor{led: led, source: source}
}

func (m *monitor) run(ctx context.Context) (err error) {
	defer func() { err = errors.Join(err, m.led.Close()) }()
	if ctx.Err() != nil {
		return nil
	}
	if err := m.led.Set(false); err != nil {
		return err
	}
	ticker := time.NewTicker(renderInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		now := time.Now()
		if !now.Before(m.nextSample) {
			sample, sampleErr := m.source.Sample(ctx)
			now = time.Now()
			if ctx.Err() != nil {
				return nil
			}
			m.period = periodForRate(m.rate.update(sample, sampleErr, now))
			m.nextSample = now.Add(sampleInterval)
		}
		if err := m.render(now); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// render keeps pulse phase across samples; resampling must not force every
// traffic rate to blink at the polling frequency.
func (m *monitor) render(now time.Time) error {
	on := m.state
	if m.period == 0 {
		on = false
		m.lastPulse = time.Time{}
	} else if on {
		if now.Sub(m.lastPulse) >= pulseWidth {
			on = false
		}
	} else if m.lastPulse.IsZero() || now.Sub(m.lastPulse) >= m.period {
		on = true
		m.lastPulse = now
	}
	if on == m.state {
		return nil
	}
	if err := m.led.Set(on); err != nil {
		return err
	}
	m.state = on
	return nil
}

// Approximate throughput tiers keep the pattern legible rather than turning
// high traffic into a solid light. Rates are combined RX+TX bytes per second.
func periodForRate(bytesPerSecond float64) time.Duration {
	switch {
	case bytesPerSecond <= 0 || math.IsNaN(bytesPerSecond) || math.IsInf(bytesPerSecond, 0):
		return 0
	case bytesPerSecond < 16*1024:
		return time.Second
	case bytesPerSecond < 128*1024:
		return 500 * time.Millisecond
	case bytesPerSecond < 1024*1024:
		return 250 * time.Millisecond
	case bytesPerSecond < 8*1024*1024:
		return 125 * time.Millisecond
	default:
		return 100 * time.Millisecond
	}
}

type rateTracker struct{ previous *device.TrafficSample }

func (r *rateTracker) update(sample device.TrafficSample, err error, now time.Time) float64 {
	if err != nil || sample.InterfaceName != "tailscale0" || sample.CounterEpoch == "" ||
		sample.SampledAt.IsZero() || sample.SampledAt.After(now) || now.Sub(sample.SampledAt) > maxSampleGap {
		r.previous = nil
		return 0
	}
	previous := r.previous
	r.previous = &sample
	if previous == nil || previous.CounterEpoch != sample.CounterEpoch {
		return 0
	}
	elapsed := sample.SampledAt.Sub(previous.SampledAt)
	if elapsed <= 0 || elapsed > maxSampleGap || sample.RxBytes < previous.RxBytes || sample.TxBytes < previous.TxBytes {
		return 0
	}
	// Subtract uint64 counters before converting, and sum after converting,
	// preserving tiny deltas near uint64 max without overflowing their sum.
	return (float64(sample.RxBytes-previous.RxBytes) + float64(sample.TxBytes-previous.TxBytes)) / elapsed.Seconds()
}
