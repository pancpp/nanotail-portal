package database

import "github.com/uptrace/bun"

// NetworkActivityHour stores VPN byte deltas, not interface lifetime counters.
// Decimal text keeps SQLite's signed integer range from truncating byte totals.
type NetworkActivityHour struct {
	bun.BaseModel   `bun:"table:network_activity_hours"`
	HourStart       int64   `bun:"hour_start,pk"`
	RxBytes         string  `bun:"rx_bytes,notnull"`
	TxBytes         string  `bun:"tx_bytes,notnull"`
	ObservedSeconds float64 `bun:"observed_seconds,notnull"`
}
