package access

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type serviceRecoveryTransport func(*http.Request) (*http.Response, error)

func (f serviceRecoveryTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type serviceObservation struct {
	Hostname string `json:"hostname"`
	IPv4     string `json:"ipv4"`
	IPv6     string `json:"ipv6"`
}

type serviceRecoveryFixture struct {
	service *ReportService
	logins  int
	updates []serviceObservation
	fail    string
}

func newServiceRecoveryFixture(t *testing.T) *serviceRecoveryFixture {
	t.Helper()
	reporter, err := NewIPReporter("https://example.invalid/api/device/v1", "board-123", "test-signature")
	if err != nil {
		t.Fatal(err)
	}
	f := &serviceRecoveryFixture{service: &ReportService{reporter: reporter}}
	reporter.httpClient.Transport = serviceRecoveryTransport(func(req *http.Request) (*http.Response, error) {
		defer req.Body.Close()
		status, body := http.StatusOK, `{"status":"success"}`
		switch req.URL.Path {
		case "/api/device/v1/login":
			f.logins++
			body = fmt.Sprintf(`{"token":"token-%d"}`, f.logins)
			if f.fail == "login" {
				status = http.StatusUnauthorized
			}
		case "/api/device/v1/update-ip":
			if got, want := req.Header.Get("Authorization"), fmt.Sprintf("Bearer token-%d", f.logins); got != want {
				t.Fatalf("update token = %q, want current login token %q", got, want)
			}
			var observation serviceObservation
			if err := json.NewDecoder(req.Body).Decode(&observation); err != nil {
				t.Fatal(err)
			}
			f.updates = append(f.updates, observation)
			if f.fail == "update" {
				status = http.StatusInternalServerError
			}
		default:
			t.Fatalf("unexpected request path: %s", req.URL.Path)
		}
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	return f
}

func (f *serviceRecoveryFixture) observe(t *testing.T, observation serviceObservation) error {
	t.Helper()
	return f.service.reportNetwork(t.Context(), observation.Hostname, observation.IPv4, observation.IPv6)
}

func TestReportServiceReportsReturningAddressImmediately(t *testing.T) {
	t.Parallel()
	for _, family := range []string{"IPv4", "IPv6"} {
		for _, periodic := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/periodic_partial_report=%t", family, periodic), func(t *testing.T) {
				t.Parallel()
				f := newServiceRecoveryFixture(t)
				available := serviceObservation{Hostname: "nanotail", IPv4: "192.168.1.20", IPv6: "2001:db8::20"}
				if err := f.observe(t, available); err != nil {
					t.Fatal(err)
				}
				absent := available
				if family == "IPv4" {
					absent.IPv4 = ""
				} else {
					absent.IPv6 = ""
				}
				if err := f.observe(t, absent); err != nil {
					t.Fatal(err)
				}
				if f.logins != 1 || len(f.updates) != 1 {
					t.Fatal("absence alone should leave the server field to expire")
				}
				if periodic {
					// Repeated periodic refreshes must keep the absent family empty.
					for range 7 {
						f.service.lastReportTime = time.Now().Add(-REPORT_INTERVAL - time.Second)
						before := len(f.updates)
						if err := f.observe(t, absent); err != nil {
							t.Fatal(err)
						}
						if len(f.updates) != before+1 || f.updates[before] != absent {
							t.Fatalf("partial report = %v, want current addresses %v", f.updates, absent)
						}
					}
				}
				if time.Since(f.service.lastReportTime) >= REPORT_INTERVAL {
					t.Fatal("fixture must recover before the next periodic deadline")
				}
				beforeLogins, beforeUpdates := f.logins, len(f.updates)
				if err := f.observe(t, available); err != nil {
					t.Fatal(err)
				}
				if f.logins != beforeLogins+1 || len(f.updates) != beforeUpdates+1 {
					t.Fatal("return of the same address did not trigger immediate login and update")
				}
				if got := f.updates[beforeUpdates]; got != available {
					t.Fatalf("recovery report = %+v, want %+v", got, available)
				}
			})
		}
	}
}

func TestReportServiceRetriesFailedAddressRecovery(t *testing.T) {
	t.Parallel()
	for _, family := range []string{"IPv4", "IPv6"} {
		for _, failure := range []string{"login", "update"} {
			t.Run(family+"/"+failure, func(t *testing.T) {
				t.Parallel()
				f := newServiceRecoveryFixture(t)
				available := serviceObservation{Hostname: "nanotail", IPv4: "192.168.1.20", IPv6: "2001:db8::20"}
				if err := f.observe(t, available); err != nil {
					t.Fatal(err)
				}
				absent := available
				if family == "IPv4" {
					absent.IPv4 = ""
				} else {
					absent.IPv6 = ""
				}
				f.service.lastReportTime = time.Now().Add(-REPORT_INTERVAL - time.Second)
				if err := f.observe(t, absent); err != nil {
					t.Fatal(err)
				}
				lastReportTime := f.service.lastReportTime
				beforeLogins, beforeUpdates := f.logins, len(f.updates)
				f.fail = failure
				if err := f.observe(t, available); !errors.Is(err, ErrAPIHTTPStatus) {
					t.Fatalf("failed recovery error = %v, want ErrAPIHTTPStatus", err)
				}
				cached := serviceObservation{Hostname: f.service.hostname, IPv4: f.service.ipv4, IPv6: f.service.ipv6}
				if cached != absent || !f.service.lastReportTime.Equal(lastReportTime) {
					t.Fatal("failed recovery committed cached addresses or report time")
				}
				if failure == "login" && len(f.updates) != beforeUpdates {
					t.Fatal("failed login sent an IP update")
				}
				f.fail = ""
				beforeRetry := len(f.updates)
				if err := f.observe(t, available); err != nil {
					t.Fatal(err)
				}
				if f.logins != beforeLogins+2 || len(f.updates) != beforeRetry+1 {
					t.Fatal("next observation did not retry recovery with a fresh login")
				}
				cached = serviceObservation{Hostname: f.service.hostname, IPv4: f.service.ipv4, IPv6: f.service.ipv6}
				if cached != available || f.updates[beforeRetry] != available {
					t.Fatal("successful retry did not report and commit the recovered addresses")
				}
			})
		}
	}
}
