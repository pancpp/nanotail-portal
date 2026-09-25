package traffic

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/pancpp/nanotail-portal/database"
	"github.com/pancpp/nanotail-portal/device"
)

var baseHour = time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)

type saveCall struct {
	rows []database.NetworkActivityHour
	end  time.Time
}
type fakeWriter struct {
	calls []saveCall
	err   error
}

func (w *fakeWriter) Save(_ context.Context, rows []database.NetworkActivityHour, end time.Time) error {
	w.calls = append(w.calls, saveCall{append([]database.NetworkActivityHour{}, rows...), end})
	return w.err
}
func sample(at time.Time, rx, tx uint64, epoch string) device.TrafficSample {
	return device.TrafficSample{InterfaceName: "tailscale0", SampledAt: at, RxBytes: rx, TxBytes: tx, CounterEpoch: epoch}
}
func record(t *testing.T, r *Recorder, at time.Time, rx, tx uint64, epoch string) {
	t.Helper()
	if err := r.record(t.Context(), at, sample(at, rx, tx, epoch), nil); err != nil {
		t.Fatal(err)
	}
}

func TestRecorderWritesOnlyOncePerCompletedHour(t *testing.T) {
	w := &fakeWriter{}
	r := NewRecorder(nil, w)
	for minute := 0; minute <= 120; minute++ {
		at := baseHour.Add(time.Duration(minute) * time.Minute)
		record(t, r, at, uint64(100000+minute*100), uint64(200000+minute*25), "boot:1")
		if len(w.calls) != minute/60 {
			t.Fatalf("minute %d: wrote %d times", minute, len(w.calls))
		}
	}
	for i, call := range w.calls {
		if len(call.rows) != 1 {
			t.Fatalf("rows=%+v", call.rows)
		}
		row := call.rows[0]
		if row.HourStart != baseHour.Add(time.Duration(i)*time.Hour).Unix() || row.RxBytes != "6000" || row.TxBytes != "1500" || row.ObservedSeconds != 3600 {
			t.Fatalf("unexpected hourly totals: %+v", row)
		}
	}
	if len(r.buckets) != 1 {
		t.Fatal("saved buckets were not released")
	}
}

func TestRecorderBoundaryProration(t *testing.T) {
	w := &fakeWriter{}
	r := NewRecorder(nil, w)
	start := baseHour.Add(59*time.Minute + 30*time.Second)
	record(t, r, start, 1000, 1000, "boot:1")
	record(t, r, start.Add(time.Minute), 1101, 1003, "boot:1")
	row := w.calls[0].rows[0]
	if row.RxBytes != "50" || row.TxBytes != "1" || row.ObservedSeconds != 30 {
		t.Fatalf("old hour %+v", row)
	}
	next := r.buckets[baseHour.Add(time.Hour).Unix()]
	if next.rx.String() != "51" || next.tx.String() != "2" || next.observed != 30*time.Second {
		t.Fatalf("new hour %+v", next)
	}
}

func TestRecorderMissingSamplesAndResets(t *testing.T) {
	w := &fakeWriter{}
	r := NewRecorder(nil, w)
	start := baseHour.Add(30 * time.Minute)
	record(t, r, start, 1000, 1000, "boot:1")
	record(t, r, start.Add(time.Minute), 1120, 1060, "boot:1")
	if err := r.record(t.Context(), start.Add(2*time.Minute), device.TrafficSample{}, errors.New("missing interface")); err != nil {
		t.Fatal(err)
	}
	record(t, r, start.Add(3*time.Minute), 5000, 5000, "boot:1")
	record(t, r, start.Add(4*time.Minute), 5050, 5050, "boot:1")
	record(t, r, start.Add(5*time.Minute), 20, 20, "boot:2")
	record(t, r, start.Add(6*time.Minute), 120, 120, "boot:2")
	record(t, r, start.Add(7*time.Minute), 0, 0, "boot:2")
	record(t, r, start.Add(8*time.Minute), 25, 25, "boot:2")
	// A long observation gap cannot be attributed to one hour.
	record(t, r, baseHour.Add(time.Hour), 10000, 10000, "boot:2")
	row := w.calls[0].rows[0]
	if row.RxBytes != "295" || row.TxBytes != "235" || row.ObservedSeconds != 240 {
		t.Fatalf("invented traffic or coverage: %+v", row)
	}
}

func TestRecorderUnmeasuredAndIdleAreDistinct(t *testing.T) {
	for _, missing := range []bool{false, true} {
		w := &fakeWriter{}
		r := NewRecorder(nil, w)
		for minute := 0; minute <= 60; minute++ {
			at := baseHour.Add(time.Duration(minute) * time.Minute)
			var err error
			if missing {
				err = errors.New("missing")
			}
			if err := r.record(t.Context(), at, sample(at, 42, 42, "boot:1"), err); err != nil {
				t.Fatal(err)
			}
		}
		row := w.calls[0].rows[0]
		wantSeconds := float64(3600)
		if missing {
			wantSeconds = 0
		}
		if row.RxBytes != "0" || row.TxBytes != "0" || row.ObservedSeconds != wantSeconds {
			t.Fatalf("missing=%v: %+v", missing, row)
		}
	}
}

func TestRecorderLargeCounters(t *testing.T) {
	w := &fakeWriter{}
	r := NewRecorder(nil, w)
	record(t, r, baseHour, 0, 0, "first")
	record(t, r, baseHour.Add(time.Minute), math.MaxUint64, math.MaxUint64, "first")
	record(t, r, baseHour.Add(2*time.Minute), 0, 0, "second")
	record(t, r, baseHour.Add(3*time.Minute), math.MaxUint64, math.MaxUint64, "second")
	record(t, r, baseHour.Add(time.Hour), math.MaxUint64, math.MaxUint64, "second")
	row := w.calls[0].rows[0]
	if row.RxBytes != "36893488147419103230" || row.TxBytes != row.RxBytes || row.ObservedSeconds != 120 {
		t.Fatalf("overflow: %+v", row)
	}
}

func TestRecorderFailedWritesRetryOnlyHourly(t *testing.T) {
	w := &fakeWriter{err: errors.New("locked")}
	r := NewRecorder(nil, w)
	for minute := 0; minute <= 120; minute++ {
		at := baseHour.Add(time.Duration(minute) * time.Minute)
		err := r.record(t.Context(), at, sample(at, uint64(minute), uint64(minute), "epoch"), nil)
		if minute == 60 {
			if err == nil {
				t.Fatal("lost save error")
			}
			w.err = nil
		} else if err != nil {
			t.Fatal(err)
		}
		if len(w.calls) != minute/60 {
			t.Fatalf("wrote between hours at %d", minute)
		}
	}
	if len(w.calls[1].rows) != 2 || w.calls[1].rows[0].RxBytes != "60" || w.calls[1].rows[1].RxBytes != "60" {
		t.Fatalf("lost pending totals: %+v", w.calls)
	}
	if len(r.buckets) != 1 {
		t.Fatal("saved retry not released")
	}
}

func TestRecorderPendingMemoryIsBounded(t *testing.T) {
	w := &fakeWriter{err: errors.New("unavailable")}
	r := NewRecorder(nil, w)
	for hour := 0; hour < 100; hour++ {
		at := baseHour.Add(time.Duration(hour) * time.Hour)
		_ = r.record(t.Context(), at, sample(at, 1, 1, "epoch"), nil)
		if len(r.buckets) > 25 {
			t.Fatal("unbounded pending hours")
		}
	}
	if len(w.calls) != 99 || len(w.calls[98].rows) != 24 {
		t.Fatalf("unexpected retry cadence: %d", len(w.calls))
	}
}

func TestRecorderClockRewindDoesNotDoubleCount(t *testing.T) {
	w := &fakeWriter{}
	r := NewRecorder(nil, w)
	for minute := 0; minute <= 10; minute++ {
		record(t, r, baseHour.Add(time.Duration(minute)*time.Minute), uint64(minute*10), 0, "epoch")
	}
	for minute := 5; minute <= 10; minute++ {
		record(t, r, baseHour.Add(time.Duration(minute)*time.Minute), 10000, 0, "epoch")
	}
	record(t, r, baseHour.Add(11*time.Minute), 20000, 0, "epoch")
	record(t, r, baseHour.Add(12*time.Minute), 20010, 0, "epoch")
	record(t, r, baseHour.Add(time.Hour), 30000, 0, "epoch")
	row := w.calls[0].rows[0]
	if row.RxBytes != "110" || row.ObservedSeconds != 660 {
		t.Fatalf("recounted time: %+v", row)
	}
}

type samplerFunc func(context.Context) (device.TrafficSample, error)

func (f samplerFunc) Sample(ctx context.Context) (device.TrafficSample, error) { return f(ctx) }

func TestRecorderRunStopsWithoutShutdownWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	called := make(chan struct{}, 1)
	done := make(chan struct{})
	w := &fakeWriter{}
	r := NewRecorder(samplerFunc(func(context.Context) (device.TrafficSample, error) {
		called <- struct{}{}
		return sample(baseHour, 100, 100, "epoch"), nil
	}), w)
	r.now = func() time.Time { return baseHour }
	go func() { r.Run(ctx); close(done) }()
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("recorder did not start without a browser")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("recorder did not stop")
	}
	if len(w.calls) != 0 {
		t.Fatal("wrote an unfinished hour on shutdown")
	}
}
