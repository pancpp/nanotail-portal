package graph

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/pancpp/nanotail-portal/database"
	"github.com/pancpp/nanotail-portal/traffic"
)

type historyReaderFunc func(context.Context, time.Time) (traffic.History, error)

func (f historyReaderFunc) History(ctx context.Context, now time.Time) (traffic.History, error) {
	return f(ctx, now)
}

func TestNetworkActivityHistoryGraphQL(t *testing.T) {
	for _, mode := range []string{"success", "empty", "failed", "missing"} {
		t.Run(mode, func(t *testing.T) {
			r := &Resolver{}
			if mode != "missing" {
				r.TrafficHistory = historyReaderFunc(func(ctx context.Context, now time.Time) (traffic.History, error) {
					if deadline, ok := ctx.Deadline(); !ok || deadline.Sub(time.Now()) > 5*time.Second {
						t.Fatal("history read is not bounded")
					}
					if time.Since(now) > time.Second {
						t.Fatal("missing current time")
					}
					if mode == "failed" {
						return traffic.History{}, errors.New("private SQL database diagnostic")
					}
					end := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
					result := traffic.History{WindowStart: end.Add(-24 * time.Hour), WindowEnd: end}
					if mode == "success" {
						result.Hours = []database.NetworkActivityHour{{HourStart: end.Add(-time.Hour).Unix(), RxBytes: "36893488147419103230", TxBytes: "0", ObservedSeconds: 1200.5}}
					}
					var err error
					result.Totals, err = database.SeedNetworkActivityTotals(result.Hours)
					if err != nil {
						t.Fatal(err)
					}
					if mode == "success" {
						// Lifetime accounting includes a previously pruned hour.
						if err := result.Totals.AddHour(database.NetworkActivityHour{HourStart: end.Add(-48 * time.Hour).Unix(), RxBytes: "1", TxBytes: "2", ObservedSeconds: 3600}); err != nil {
							t.Fatal(err)
						}
					}
					return result, nil
				})
			}
			srv := handler.New(NewExecutableSchema(Config{Resolvers: r}))
			srv.AddTransport(transport.POST{})
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/query", strings.NewReader(`{"query":"query { networkActivityHistory { windowStart windowEnd hours { startedAt rxBytes txBytes observedSeconds } totals { rxBytes24h txBytes24h observedSeconds24h totalRxBytes totalTxBytes totalObservedSeconds recordedSince } } }"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			var result struct {
				Data struct {
					History struct {
						WindowStart, WindowEnd string
						Hours                  []struct {
							StartedAt, RxBytes, TxBytes string
							ObservedSeconds             float64
						}
						Totals struct {
							RxBytes24h, TxBytes24h, TotalRxBytes, TotalTxBytes string
							ObservedSeconds24h, TotalObservedSeconds           float64
							RecordedSince                                      *string
						}
					} `json:"networkActivityHistory"`
				}
				Errors []struct{ Message string }
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusOK {
				t.Fatalf("HTTP %d %s", w.Code, w.Body.String())
			}
			if mode == "missing" || mode == "failed" {
				if len(result.Errors) != 1 || result.Errors[0].Message != ErrNetworkActivityHistory.Error() || strings.Contains(w.Body.String(), "private SQL") {
					t.Fatalf("unmasked failure %s", w.Body.String())
				}
				return
			}
			if len(result.Errors) != 0 || result.Data.History.WindowEnd != "2026-09-24T12:00:00Z" || result.Data.History.WindowStart != "2026-09-23T12:00:00Z" {
				t.Fatalf("bad window %s", w.Body.String())
			}
			if mode == "empty" {
				if result.Data.History.Hours == nil || len(result.Data.History.Hours) != 0 {
					t.Fatal("empty history must be []")
				}
				totals := result.Data.History.Totals
				if totals.RxBytes24h != "0" || totals.TxBytes24h != "0" || totals.TotalRxBytes != "0" || totals.TotalTxBytes != "0" || totals.ObservedSeconds24h != 0 || totals.TotalObservedSeconds != 0 || totals.RecordedSince != nil {
					t.Fatalf("bad empty totals %s", w.Body.String())
				}
				return
			}
			if len(result.Data.History.Hours) != 1 {
				t.Fatal(w.Body.String())
			}
			hour := result.Data.History.Hours[0]
			if hour.RxBytes != "36893488147419103230" || hour.TxBytes != "0" || hour.ObservedSeconds != 1200.5 || hour.StartedAt != "2026-09-24T11:00:00Z" {
				t.Fatalf("history lost data %s", w.Body.String())
			}
			totals := result.Data.History.Totals
			if totals.RxBytes24h != hour.RxBytes || totals.TxBytes24h != "0" || totals.TotalRxBytes != "36893488147419103231" || totals.TotalTxBytes != "2" ||
				totals.ObservedSeconds24h != 1200.5 || totals.TotalObservedSeconds != 4800.5 || totals.RecordedSince == nil || *totals.RecordedSince != "2026-09-22T12:00:00Z" {
				t.Fatalf("totals lost data %s", w.Body.String())
			}
		})
	}
}
