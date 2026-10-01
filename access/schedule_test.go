package access

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type serviceScheduleFixture struct {
	*serviceRecoveryFixture
	mu sync.Mutex
}

func newServiceScheduleFixture(t *testing.T) *serviceScheduleFixture {
	t.Helper()
	f := &serviceScheduleFixture{serviceRecoveryFixture: newServiceRecoveryFixture(t)}
	transport := f.service.reporter.httpClient.Transport
	f.service.reporter.httpClient.Transport = serviceRecoveryTransport(func(req *http.Request) (*http.Response, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return transport.RoundTrip(req)
	})
	return f
}

func (f *serviceScheduleFixture) snapshot() (int, []serviceObservation) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logins, append([]serviceObservation(nil), f.updates...)
}

func (f *serviceScheduleFixture) setFailure(failure string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = failure
}

func runServiceFixture(t *testing.T, f *serviceScheduleFixture) {
	t.Helper()
	// The worker still gathers the real hostname, but neither address family
	// depends on a host interface and all HTTP is handled by the fixture.
	f.service.interfaceName = "nanotail-schedule-test-interface-does-not-exist"
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.service.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	// Let the initial poll finish before inspecting the fixture's HTTP history.
	synctest.Wait()
}

func TestReportServiceRunReportsImmediatelyAndRefreshesUnchanged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newServiceScheduleFixture(t)
		runServiceFixture(t, f)
		logins, updates := f.snapshot()
		if logins != 1 || len(updates) != 1 {
			t.Fatalf("startup = %d logins, %d updates; want one immediate report", logins, len(updates))
		}
		initial := updates[0]
		time.Sleep(REPORT_INTERVAL - CHECK_INTERVAL)
		synctest.Wait()
		if logins, updates := f.snapshot(); logins != 1 || len(updates) != 1 {
			t.Fatal("unchanged observations reported before the refresh interval")
		}
		// Refresh is checked on the polling interval, so it must happen no later
		// than the first check after the configured report interval has elapsed.
		time.Sleep(2 * CHECK_INTERVAL)
		synctest.Wait()
		logins, updates = f.snapshot()
		if logins != 2 || len(updates) != 2 {
			t.Fatalf("refresh = %d logins, %d updates; want a new login and update", logins, len(updates))
		}
		if got := updates[1]; got != initial {
			t.Fatalf("unchanged refresh = %+v, want %+v", got, initial)
		}
		time.Sleep(CHECK_INTERVAL)
		synctest.Wait()
		if logins, updates := f.snapshot(); logins != 2 || len(updates) != 2 {
			t.Fatal("successful refresh did not reset the reporting interval")
		}
	})
}

func TestReportServiceRunRetriesStartupFailureOnNextCheck(t *testing.T) {
	for _, failure := range []string{"login", "update"} {
		t.Run(failure, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newServiceScheduleFixture(t)
				f.setFailure(failure)
				runServiceFixture(t, f)
				wantInitialUpdates := 0
				if failure == "update" {
					wantInitialUpdates = 1
				}
				if logins, updates := f.snapshot(); logins != 1 || len(updates) != wantInitialUpdates {
					t.Fatalf("failed startup = %d logins, %d updates", logins, len(updates))
				}
				f.setFailure("")
				time.Sleep(CHECK_INTERVAL - time.Second)
				synctest.Wait()
				if logins, _ := f.snapshot(); logins != 1 {
					t.Fatal("failed report retried before the next check")
				}
				time.Sleep(time.Second)
				synctest.Wait()
				if logins, updates := f.snapshot(); logins != 2 || len(updates) != wantInitialUpdates+1 {
					t.Fatalf("next check = %d logins, %d updates; want a fresh login and retry", logins, len(updates))
				}
				time.Sleep(CHECK_INTERVAL)
				synctest.Wait()
				if logins, updates := f.snapshot(); logins != 2 || len(updates) != wantInitialUpdates+1 {
					t.Fatal("successful retry continued reporting unchanged observations")
				}
			})
		})
	}
}

func TestReportServiceReportsChangedObservation(t *testing.T) {
	t.Parallel()
	initial := serviceObservation{Hostname: "nanotail", IPv4: "192.168.1.20", IPv6: "2001:db8::20"}
	for _, field := range []string{"hostname", "IPv4", "IPv6"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			f := newServiceRecoveryFixture(t)
			if err := f.observe(t, initial); err != nil {
				t.Fatal(err)
			}
			changed := initial
			switch field {
			case "hostname":
				changed.Hostname = "nanotail-renamed"
			case "IPv4":
				changed.IPv4 = "192.168.1.21"
			case "IPv6":
				changed.IPv6 = "2001:db8::21"
			}
			if err := f.observe(t, changed); err != nil {
				t.Fatal(err)
			}
			if f.logins != 2 || len(f.updates) != 2 {
				t.Fatalf("changed %s = %d logins, %d updates; want immediate login and report", field, f.logins, len(f.updates))
			}
			if got := f.updates[1]; got != changed {
				t.Fatalf("changed report = %+v, want %+v", got, changed)
			}
			if err := f.observe(t, changed); err != nil {
				t.Fatal(err)
			}
			if f.logins != 2 || len(f.updates) != 2 {
				t.Fatal("the accepted change was reported again without a new change")
			}
		})
	}
}
