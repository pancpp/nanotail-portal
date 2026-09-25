package graph

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/pancpp/nanotail-portal/app/graph/model"
)

var ErrNetworkActivity = errors.New("Unable to read VPN traffic. Check that tailscaled is running with the tailscale0 network interface")
var ErrNetworkActivityHistory = errors.New("Unable to read saved VPN history. Check database access and the startup migration logs")

func (r *queryResolver) networkActivityHistory(ctx context.Context) (*model.NetworkActivityHistory, error) {
	if r.TrafficHistory == nil {
		return nil, ErrNetworkActivityHistory
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	history, err := r.TrafficHistory.History(ctx, time.Now())
	if err != nil {
		return nil, ErrNetworkActivityHistory
	}
	result := &model.NetworkActivityHistory{WindowStart: history.WindowStart, WindowEnd: history.WindowEnd, Hours: make([]*model.NetworkActivityHour, len(history.Hours))}
	totals := history.Totals
	result.Totals = &model.NetworkActivityTotals{
		RxBytes24h: totals.RxBytes24h, TxBytes24h: totals.TxBytes24h, ObservedSeconds24h: totals.ObservedSeconds24h,
		TotalRxBytes: totals.TotalRxBytes, TotalTxBytes: totals.TotalTxBytes, TotalObservedSeconds: totals.TotalObservedSeconds,
	}
	if totals.RecordedSince != nil {
		since := time.Unix(*totals.RecordedSince, 0).UTC()
		result.Totals.RecordedSince = &since
	}
	for i, hour := range history.Hours {
		result.Hours[i] = &model.NetworkActivityHour{StartedAt: time.Unix(hour.HourStart, 0).UTC(), RxBytes: hour.RxBytes, TxBytes: hour.TxBytes, ObservedSeconds: hour.ObservedSeconds}
	}
	return result, nil
}

func (r *queryResolver) networkActivity(ctx context.Context) (*model.NetworkActivity, error) {
	if r.Traffic == nil {
		return nil, ErrNetworkActivity
	}
	sample, err := r.Traffic.Sample(ctx)
	if err != nil {
		return nil, ErrNetworkActivity
	}
	return &model.NetworkActivity{
		InterfaceName: sample.InterfaceName, RxBytes: strconv.FormatUint(sample.RxBytes, 10),
		TxBytes: strconv.FormatUint(sample.TxBytes, 10), SampledAt: sample.SampledAt, CounterEpoch: sample.CounterEpoch,
	}, nil
}
