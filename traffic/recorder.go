package traffic

import (
	"context"
	"log"
	"math/big"
	"sort"
	"time"

	"github.com/pancpp/nanotail-portal/database"
	"github.com/pancpp/nanotail-portal/device"
)

const sampleInterval = time.Minute
const maxSampleGap = 2*time.Minute + 5*time.Second

type Sampler interface {
	Sample(context.Context) (device.TrafficSample, error)
}
type HourWriter interface {
	Save(context.Context, []database.NetworkActivityHour, time.Time) error
}

type bucket struct {
	rx, tx   big.Int
	observed time.Duration
}

type Recorder struct {
	source       Sampler
	store        HourWriter
	now          func() time.Time
	hour         time.Time
	previous     *device.TrafficSample
	lastSampleAt time.Time
	buckets      map[int64]*bucket
}

func NewRecorder(source Sampler, store HourWriter) *Recorder {
	return &Recorder{source: source, store: store, now: time.Now, buckets: make(map[int64]*bucket)}
}

// Run samples in memory each minute, including hour boundaries. Database writes
// only happen on hour rollover, never on HTTP requests or on shutdown.
func (r *Recorder) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := r.tick(ctx); err != nil && ctx.Err() == nil {
			log.Printf("(network activity) hourly save failed: %v", err)
		}
		now := r.now()
		timer := time.NewTimer(now.Truncate(sampleInterval).Add(sampleInterval).Sub(now))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (r *Recorder) tick(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sample, err := r.source.Sample(ctx)
	now := r.now().UTC()
	return r.record(ctx, now, sample, err)
}

// record is called serially by Run; live queries use their own read-only sampler.
func (r *Recorder) record(ctx context.Context, now time.Time, sample device.TrafficSample, sampleErr error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	hour := now.UTC().Truncate(time.Hour)
	if r.hour.IsZero() {
		r.hour = hour
	}
	if hour.Before(r.hour) {
		// Do not mix a clock rewind with a previously measured interval.
		r.previous = nil
		r.buckets = make(map[int64]*bucket)
		r.hour = hour
	}
	if r.buckets[hour.Unix()] == nil {
		r.buckets[hour.Unix()] = &bucket{}
	}
	if sampleErr != nil || sample.InterfaceName != "tailscale0" || sample.CounterEpoch == "" ||
		!sample.SampledAt.After(r.lastSampleAt) ||
		sample.SampledAt.After(now) || now.Sub(sample.SampledAt) > 5*time.Second {
		r.previous = nil
	} else {
		if p := r.previous; p != nil {
			elapsed := sample.SampledAt.Sub(p.SampledAt)
			if elapsed > 0 && elapsed <= maxSampleGap && p.CounterEpoch == sample.CounterEpoch &&
				sample.RxBytes >= p.RxBytes && sample.TxBytes >= p.TxBytes {
				r.add(p.SampledAt, sample.SampledAt, sample.RxBytes-p.RxBytes, sample.TxBytes-p.TxBytes)
			}
		}
		r.previous = &sample
		r.lastSampleAt = sample.SampledAt
	}
	if !hour.After(r.hour) {
		return nil
	}
	r.hour = hour
	cutoff := hour.Add(-24 * time.Hour).Unix()
	rows := []database.NetworkActivityHour{}
	for start, value := range r.buckets {
		if start < cutoff {
			delete(r.buckets, start)
			continue
		}
		if start >= hour.Unix() {
			continue
		}
		rows = append(rows, database.NetworkActivityHour{
			HourStart: start, RxBytes: value.rx.String(), TxBytes: value.tx.String(), ObservedSeconds: value.observed.Seconds(),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].HourStart < rows[j].HourStart })
	// A failed batch stays in bounded memory and is retried at the next hourly
	// save, not on every sample. Live activity remains independent of DB failures.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := r.store.Save(ctx, rows, hour); err != nil {
		return err
	}
	for _, row := range rows {
		delete(r.buckets, row.HourStart)
	}
	return nil
}

func (r *Recorder) add(start, end time.Time, rx, tx uint64) {
	total := end.Sub(start)
	rxLeft, txLeft := new(big.Int).SetUint64(rx), new(big.Int).SetUint64(tx)
	for at := start; at.Before(end); {
		hour := at.UTC().Truncate(time.Hour)
		until := hour.Add(time.Hour)
		if end.Before(until) {
			until = end
		}
		duration := until.Sub(at)
		// Samples straddling an hour boundary are prorated by elapsed time;
		// the last portion receives the rounding remainder, conserving bytes.
		part := func(bytes uint64, remaining *big.Int) *big.Int {
			if until.Equal(end) {
				return new(big.Int).Set(remaining)
			}
			value := new(big.Int).SetUint64(bytes)
			value.Mul(value, big.NewInt(int64(duration)))
			value.Quo(value, big.NewInt(int64(total)))
			remaining.Sub(remaining, value)
			return value
		}
		rxPart, txPart := part(rx, rxLeft), part(tx, txLeft)
		// Never reopen an already-flushed bucket after a slightly late sample.
		if !hour.Before(r.hour) {
			value := r.buckets[hour.Unix()]
			if value == nil {
				value = &bucket{}
				r.buckets[hour.Unix()] = value
			}
			value.rx.Add(&value.rx, rxPart)
			value.tx.Add(&value.tx, txPart)
			value.observed += duration
		}
		at = until
	}
}
