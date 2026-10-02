package access

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func newLifecycleService(t *testing.T, transport serviceRecoveryTransport) *ReportService {
	t.Helper()
	reporter, err := NewIPReporter("https://example.invalid/api/device/v1", "board-123", "test-signature")
	if err != nil {
		t.Fatal(err)
	}
	reporter.httpClient.Transport = transport
	return &ReportService{
		reporter: reporter,
		// Longer than a Linux interface name, so discovery performs only a
		// failed local lookup and cannot depend on the host's LAN addresses.
		interfaceName: "nanotail-lifecycle-test-interface-does-not-exist",
	}
}

func TestReportServiceRunAlreadyCanceled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var requests atomic.Int32
		service := newLifecycleService(t, func(req *http.Request) (*http.Response, error) {
			defer req.Body.Close()
			requests.Add(1)
			return nil, context.Canceled
		})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		done := make(chan struct{})
		go func() {
			defer close(done)
			service.Run(ctx)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("canceled report service did not return promptly")
		}
		if got := requests.Load(); got != 0 {
			t.Fatalf("canceled service sent %d HTTP requests", got)
		}
	})
}

func TestInitAlreadyCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// A canceled startup must not require a board serial or device signature.
	if err := Init(ctx); err != nil {
		t.Fatalf("canceled Init = %v, want nil without hardware initialization", err)
	}
}

func TestRunRestartsReportingWithoutChangingInitialization(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		originalID, originalService := gDeviceID, gReportService
		t.Cleanup(func() {
			gDeviceID, gReportService = originalID, originalService
		})
		f := newServiceScheduleFixture(t)
		f.service.interfaceName = "nanotail-lifecycle-test-interface-does-not-exist"
		gDeviceID, gReportService = "board-123", f.service
		initialized := *gReportService

		for run := 1; run <= 2; run++ {
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			go func() {
				defer close(done)
				Run(ctx)
			}()
			t.Cleanup(func() {
				cancel()
				<-done
			})
			synctest.Wait()
			logins, updates := f.snapshot()
			if logins != run || len(updates) != run {
				t.Fatalf("run %d started with %d logins and %d updates; want one immediate report per run", run, logins, len(updates))
			}
			if updates[run-1] != updates[0] {
				t.Fatalf("run %d changed observations: got %+v, want %+v", run, updates[run-1], updates[0])
			}

			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("reporting did not stop promptly after cancellation")
			}
			time.Sleep(2 * CHECK_INTERVAL)
			synctest.Wait()
			if logins, updates := f.snapshot(); logins != run || len(updates) != run {
				t.Fatalf("run %d continued reporting after cancellation", run)
			}
			if got := GetDeviceID(); got != "board-123" {
				t.Fatalf("run %d changed cached device ID to %q", run, got)
			}
			if gReportService != f.service || *gReportService != initialized {
				t.Fatalf("run %d changed the initialized report service", run)
			}
		}
	})
}

func TestReportServiceRunCancelsAndJoinsRequests(t *testing.T) {
	for _, stage := range []string{LOGIN_API, UPDATE_IP_API} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
				var requests atomic.Int32
				service := newLifecycleService(t, func(req *http.Request) (*http.Response, error) {
					defer req.Body.Close()
					requests.Add(1)
					if req.URL.Path == "/api/device/v1"+stage {
						close(entered)
						<-req.Context().Done()
						close(canceled)
						// Keep the transport alive after cancellation so the test
						// can distinguish canceling work from waiting for its exit.
						<-release
						return nil, req.Context().Err()
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader(`{"token":"test-token"}`)),
					}, nil
				})
				done := make(chan struct{})
				go func() {
					defer close(done)
					service.Run(ctx)
				}()
				<-entered
				canceledAt := time.Now()
				cancel()
				<-canceled
				if elapsed := time.Since(canceledAt); elapsed >= time.Second {
					t.Errorf("request observed shutdown cancellation after %v", elapsed)
				}
				synctest.Wait()
				select {
				case <-done:
					t.Error("service returned before the canceled request finished")
				default:
				}
				close(release)
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("service did not return after the canceled request finished")
				}
				wantRequests := int32(1)
				if stage == UPDATE_IP_API {
					wantRequests = 2
				}
				time.Sleep(2 * CHECK_INTERVAL)
				if got := requests.Load(); got != wantRequests {
					t.Fatalf("requests after shutdown = %d, want %d", got, wantRequests)
				}
			})
		})
	}
}
