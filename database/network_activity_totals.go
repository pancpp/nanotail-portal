package database

import (
	"errors"
	"math"
	"math/big"
	"time"

	"github.com/uptrace/bun"
)

// A singleton snapshot, updated atomically with the completed hourly records.
// WindowEnd is also a monotonic accounting watermark: pruned hours must never
// be counted again when an old save is retried or the wall clock goes backwards.
type NetworkActivityTotals struct {
	bun.BaseModel        `bun:"table:network_activity_totals"`
	ID                   int     `bun:"id,pk"`
	WindowEnd            int64   `bun:"window_end,notnull"`
	RxBytes24h           string  `bun:"rx_bytes_24h,notnull"`
	TxBytes24h           string  `bun:"tx_bytes_24h,notnull"`
	ObservedSeconds24h   float64 `bun:"observed_seconds_24h,notnull"`
	TotalRxBytes         string  `bun:"total_rx_bytes,notnull"`
	TotalTxBytes         string  `bun:"total_tx_bytes,notnull"`
	TotalObservedSeconds float64 `bun:"total_observed_seconds,notnull"`
	RecordedSince        *int64  `bun:"recorded_since"`
}

func EmptyNetworkActivityTotals() NetworkActivityTotals {
	return NetworkActivityTotals{ID: 1, RxBytes24h: "0", TxBytes24h: "0", TotalRxBytes: "0", TotalTxBytes: "0"}
}

// Byte values stay decimal strings, never SQLite SUM/REAL or signed integers.
func AddTrafficBytes(a, b string) (string, error) {
	parse := func(value string) (*big.Int, error) {
		n, ok := new(big.Int).SetString(value, 10)
		if !ok || n.Sign() < 0 || n.String() != value {
			return nil, errors.New("invalid recorded traffic bytes")
		}
		return n, nil
	}
	left, err := parse(a)
	if err != nil {
		return "", err
	}
	right, err := parse(b)
	if err != nil {
		return "", err
	}
	return left.Add(left, right).String(), nil
}

func (totals *NetworkActivityTotals) AddHour(hour NetworkActivityHour) error {
	if hour.HourStart%3600 != 0 || math.IsNaN(hour.ObservedSeconds) || math.IsInf(hour.ObservedSeconds, 0) || hour.ObservedSeconds < 0 || hour.ObservedSeconds > 3600 ||
		(hour.ObservedSeconds == 0 && (hour.RxBytes != "0" || hour.TxBytes != "0")) {
		return errors.New("invalid recorded traffic hour")
	}
	var err error
	if totals.TotalRxBytes, err = AddTrafficBytes(totals.TotalRxBytes, hour.RxBytes); err != nil {
		return err
	}
	if totals.TotalTxBytes, err = AddTrafficBytes(totals.TotalTxBytes, hour.TxBytes); err != nil {
		return err
	}
	totals.TotalObservedSeconds += hour.ObservedSeconds
	if hour.ObservedSeconds > 0 && (totals.RecordedSince == nil || hour.HourStart < *totals.RecordedSince) {
		start := hour.HourStart
		totals.RecordedSince = &start
	}
	return nil
}

func (totals *NetworkActivityTotals) SetWindow(hours []NetworkActivityHour, end int64) error {
	window := EmptyNetworkActivityTotals()
	for _, hour := range hours {
		if hour.HourStart >= end-86400 && hour.HourStart < end {
			if err := window.AddHour(hour); err != nil {
				return err
			}
		}
	}
	totals.WindowEnd = end
	totals.RxBytes24h, totals.TxBytes24h = window.TotalRxBytes, window.TotalTxBytes
	totals.ObservedSeconds24h = window.TotalObservedSeconds
	return nil
}

// Seed from retained records when upgrading. Never infer already-pruned usage
// from interface lifetime counters, which may cover unrelated time periods.
func SeedNetworkActivityTotals(hours []NetworkActivityHour) (NetworkActivityTotals, error) {
	totals := EmptyNetworkActivityTotals()
	var end int64
	for _, hour := range hours {
		if err := totals.AddHour(hour); err != nil {
			return NetworkActivityTotals{}, err
		}
		if candidate := hour.HourStart + int64(time.Hour/time.Second); candidate > end {
			end = candidate
		}
	}
	if err := totals.SetWindow(hours, end); err != nil {
		return NetworkActivityTotals{}, err
	}
	return totals, nil
}
