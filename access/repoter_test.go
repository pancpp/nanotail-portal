package access

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewIPReporter(t *testing.T) {
	t.Parallel()
	for _, prefix := range []string{
		"http://localhost/api/device/v1",
		"https://example.com/api/device/v1/",
		"https://example.com/api/device/v1///",
	} {
		t.Run(prefix, func(t *testing.T) {
			t.Parallel()
			client, err := NewIPReporter(prefix, "device-123", "signature")
			if err != nil {
				t.Fatal(err)
			}
			if client.apiPrefix != strings.TrimRight(prefix, "/") {
				t.Fatalf("prefix = %q, want normalized %q", client.apiPrefix, prefix)
			}
			if client.httpClient.Timeout != SERVER_REQUEST_TIMEOUT {
				t.Fatalf("request timeout = %v, want %v", client.httpClient.Timeout, SERVER_REQUEST_TIMEOUT)
			}
		})
	}
	for _, prefix := range []string{
		"", "/api/device/v1", "example.com/api/device/v1", "ftp://example.com/api/device/v1",
		"http:///api/device/v1", "http://", "https://user:secret@example.com/api/device/v1",
		"https://example.com/api/device/v1?query=value", "https://example.com/api/device/v1#fragment",
		"https://example.com/api/device/v1?", "https://example.com/api/device/v1#",
		"https://example.com/%invalid", "https://bad host/api/device/v1",
	} {
		t.Run("reject "+prefix, func(t *testing.T) {
			t.Parallel()
			if _, err := NewIPReporter(prefix, "device-123", "signature"); !errors.Is(err, ErrInvalidAPIURL) {
				t.Fatalf("NewIPReporter(%q) error = %v, want ErrInvalidAPIURL", prefix, err)
			}
		})
	}
}

func TestIPReporterRequestsMatchDeviceRESTContract(t *testing.T) {
	t.Parallel()
	const DEVICE_ID = "device-123"
	const SIGNATURE = "test-device-signature"
	var requests atomic.Int32
	var logins atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request = %s with Content-Type %q, want POST application/json", r.Method, r.Header.Get("Content-Type"))
		}
		if r.URL.RawQuery != "" {
			t.Errorf("unexpected query parameters: %s", r.URL.RawQuery)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/device/v1/login":
			if got := r.Header.Get("Authorization"); got != "" {
				t.Errorf("login Authorization = %q, want absent", got)
			}
			want := map[string]string{"device_id": DEVICE_ID, "device_sig": SIGNATURE}
			if !reflect.DeepEqual(body, want) {
				t.Errorf("login body = %v, want exactly %v", body, want)
			}
			_, _ = fmt.Fprintf(w, `{"token":"test-device-token-%d"}`, logins.Add(1))
		case "/api/device/v1/update-ip":
			if got := r.Header.Get("Authorization"); got != fmt.Sprintf("Bearer test-device-token-%d", logins.Load()) {
				t.Errorf("update Authorization = %q, want returned bearer token", got)
			}
			want := map[string]string{"hostname": "nanotail", "ipv4": "192.168.1.20", "ipv6": "2001:db8::20"}
			if !reflect.DeepEqual(body, want) {
				t.Errorf("update body = %v, want exactly %v", body, want)
			}
			_, _ = io.WriteString(w, `{"status":"success"}`)
		default:
			t.Errorf("unexpected API path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	client, err := NewIPReporter(server.URL+"/api/device/v1/", DEVICE_ID, SIGNATURE)
	if err != nil {
		t.Fatal(err)
	}
	// Identical reports must each log in and use their own returned token.
	for range 2 {
		if err := client.Report(t.Context(), "nanotail", "192.168.1.20", "2001:db8::20"); err != nil {
			t.Fatal(err)
		}
	}
	if requests.Load() != 4 || logins.Load() != 2 {
		t.Fatalf("request count = %d, logins = %d, want two login/update pairs", requests.Load(), logins.Load())
	}
}

func TestIPReporterRejectsInvalidResponses(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		login  bool
		body   string
		status int
		want   error
	}{
		{name: "login numeric token", login: true, body: `{"token":123}`},
		{name: "login malformed JSON", login: true, body: `{"token":`},
		{name: "login empty body", login: true},
		{name: "login trailing JSON", login: true, body: `{"token":"token"}{}`},
		{name: "login denied", login: true, status: http.StatusUnauthorized, body: `{"message":"Unauthorized"}`, want: ErrAPIHTTPStatus},
		{name: "login unexpected success code", login: true, status: http.StatusCreated, body: `{"token":"token"}`, want: ErrAPIHTTPStatus},
		{name: "login oversized response", login: true, body: `{"token":"` + strings.Repeat("a", MAX_RESPONSE_BYTES) + `"}`, want: ErrAPIResponseTooLarge},
		{name: "report missing status", body: `{}`, want: ErrInvalidAPIResponse},
		{name: "report null status", body: `{"status":null}`, want: ErrInvalidAPIResponse},
		{name: "report null body", body: `null`, want: ErrInvalidAPIResponse},
		{name: "report empty status", body: `{"status":""}`, want: ErrInvalidAPIResponse},
		{name: "report unsuccessful status", body: `{"status":"error"}`, want: ErrReportRejected},
		{name: "report wrong case status", body: `{"status":"Success"}`, want: ErrReportRejected},
		{name: "report numeric status", body: `{"status":123}`},
		{name: "report malformed JSON", body: `{"status":`},
		{name: "report empty body"},
		{name: "report trailing JSON", body: `{"status":"success"}{}`},
		{name: "report expired token", status: http.StatusUnauthorized, body: `{"message":"Unauthorized"}`, want: ErrAPIHTTPStatus},
		{name: "report server failure", status: http.StatusInternalServerError, body: `{"message":"Internal Server Error"}`, want: ErrAPIHTTPStatus},
		{name: "report unexpected success code", status: http.StatusNoContent, want: ErrAPIHTTPStatus},
		{name: "report oversized response", body: `{"status":"success","padding":"` + strings.Repeat("a", MAX_RESPONSE_BYTES) + `"}`, want: ErrAPIResponseTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var updates atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == LOGIN_API && !test.login {
					_, _ = io.WriteString(w, `{"token":"token"}`)
					return
				}
				if r.URL.Path == UPDATE_IP_API {
					updates.Add(1)
				}
				if test.status != 0 {
					w.WriteHeader(test.status)
				}
				_, _ = io.WriteString(w, test.body)
			}))
			t.Cleanup(server.Close)
			client, err := NewIPReporter(server.URL, "device-123", "signature")
			if err != nil {
				t.Fatal(err)
			}
			err = client.Report(t.Context(), "nanotail", "192.168.1.20", "")
			if err == nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want nonnil error matching %v", err, test.want)
			}
			if test.login && updates.Load() != 0 {
				t.Fatal("sent an IP update after login failed")
			}
			if !test.login && updates.Load() != 1 {
				t.Fatalf("update count = %d, want one attempted update", updates.Load())
			}
		})
	}
}

func TestIPReporterDoesNotFollowCredentialRedirects(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		for _, login := range []bool{true, false} {
			name := http.StatusText(status) + "/report"
			if login {
				name = http.StatusText(status) + "/login"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				var forwarded atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == LOGIN_API && !login {
						_, _ = io.WriteString(w, `{"token":"token"}`)
						return
					}
					if r.URL.Path == "/redirected" {
						forwarded.Add(1)
						_, _ = io.WriteString(w, `{"token":"token","status":"success"}`)
						return
					}
					http.Redirect(w, r, "/redirected", status)
				}))
				t.Cleanup(server.Close)
				client, err := NewIPReporter(server.URL, "device-123", "signature")
				if err != nil {
					t.Fatal(err)
				}
				if err = client.Report(t.Context(), "nanotail", "192.168.1.20", ""); !errors.Is(err, ErrAPIHTTPStatus) {
					t.Fatalf("redirect error = %v, want ErrAPIHTTPStatus", err)
				}
				if forwarded.Load() != 0 {
					t.Fatalf("followed credential-bearing redirect %d times", forwarded.Load())
				}
			})
		}
	}
}

func TestIPReporterRequestCancellationAndTimeout(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"canceled login", "canceled report", "login timeout", "report timeout"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == LOGIN_API && strings.Contains(name, "report") {
					_, _ = io.WriteString(w, `{"token":"token"}`)
					return
				}
				// Consume the body so the server can observe the client disconnect.
				_, _ = io.Copy(io.Discard, r.Body)
				if strings.HasPrefix(name, "canceled") {
					cancel()
				}
				select {
				case <-r.Context().Done():
				case <-time.After(2 * time.Second):
				}
			}))
			t.Cleanup(server.Close)
			client, err := NewIPReporter(server.URL, "device-123", "signature")
			if err != nil {
				t.Fatal(err)
			}
			want := context.DeadlineExceeded
			if strings.HasPrefix(name, "canceled") {
				want = context.Canceled
			} else {
				client.httpClient.Timeout = 20 * time.Millisecond
			}
			err = client.Report(ctx, "nanotail", "192.168.1.20", "")
			if !errors.Is(err, want) {
				t.Fatalf("error = %v, want %v", err, want)
			}
		})
	}
}
