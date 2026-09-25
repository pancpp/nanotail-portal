package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type approvalTransport func(*http.Request) (*http.Response, error)

func (f approvalTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func approvalResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

type approvalFixture struct {
	mu                             sync.Mutex
	credential                     OAuthCredentials
	state, deviceID                string
	routes                         []string
	enabled, advertised            []string
	writes                         [][]string
	tokenCalls, gets               int
	requests                       []string
	failToken, failRead, failWrite int
	writeLost, readbackMissing     bool
	beforeWrite                    func()
}

func newApprovalFixture() *approvalFixture {
	return &approvalFixture{
		credential: OAuthCredentials{"test-client", "test-secret"}, state: "Running", deviceID: "nDevice123",
		routes:     []string{"192.168.42.0/24", "fd00:1234::/64"},
		advertised: []string{"0.0.0.0/0", "::/0", "192.168.42.0/24", "fd00:1234::/64"},
		enabled:    []string{},
	}
}
func (f *approvalFixture) load(context.Context) (OAuthCredentials, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.credential, nil
}
func (f *approvalFixture) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch args[0] {
	case "status":
		return json.Marshal(map[string]any{"BackendState": f.state, "HaveNodeKey": true, "Self": map[string]any{"ID": f.deviceID}})
	case "debug":
		return json.Marshal(map[string]any{"WantRunning": f.state == "Running", "AdvertiseRoutes": append([]string{"0.0.0.0/0", "::/0"}, f.routes...)})
	case "set":
		for _, arg := range args {
			if value, ok := strings.CutPrefix(arg, "--advertise-routes="); ok {
				f.routes = []string{}
				if value != "" {
					f.routes = strings.Split(value, ",")
				}
			}
		}
		return nil, nil
	default:
		return nil, errors.New("unexpected CLI command")
	}
}
func (f *approvalFixture) client(t *testing.T) *Client {
	t.Helper()
	return NewClient("fake", "", time.Second, f).WithRouteApproval(f.load, &http.Client{Transport: approvalTransport(func(req *http.Request) (*http.Response, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if req.URL.Scheme != "https" || req.URL.Host != "api.tailscale.com" {
			t.Fatalf("unexpected API host: %s", req.URL)
		}
		f.requests = append(f.requests, req.Method+" "+req.URL.Path)
		if req.URL.Path == "/api/v2/oauth/token" {
			f.tokenCalls++
			if err := req.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if req.Method != "POST" || req.Form.Get("client_id") != f.credential.ClientID || req.Form.Get("client_secret") != f.credential.ClientSecret ||
				req.Form.Get("scope") != "devices:routes" || req.Form.Get("grant_type") != "client_credentials" || req.Header.Get("Authorization") != "" {
				t.Fatal("incorrect token request")
			}
			if f.failToken != 0 {
				return approvalResponse(f.failToken, "private OAuth diagnostic test-secret"), nil
			}
			return approvalResponse(200, `{"access_token":"test-api-token","token_type":"Bearer","expires_in":3600}`), nil
		}
		if req.URL.Path != "/api/v2/device/"+f.deviceID+"/routes" || req.Header.Get("Authorization") != "Bearer test-api-token" {
			t.Fatal("wrong target or authorization")
		}
		switch req.Method {
		case "GET":
			f.gets++
			if f.failRead != 0 {
				return approvalResponse(f.failRead, "private API diagnostic test-api-token"), nil
			}
			data, _ := json.Marshal(map[string]any{"advertisedRoutes": f.advertised, "enabledRoutes": f.enabled})
			if f.beforeWrite != nil {
				f.beforeWrite()
				f.beforeWrite = nil
			}
			return approvalResponse(200, string(data)), nil
		case "POST":
			var value struct {
				Routes []string `json:"routes"`
			}
			if err := json.NewDecoder(req.Body).Decode(&value); err != nil {
				t.Fatal(err)
			}
			f.writes = append(f.writes, value.Routes)
			if f.failWrite != 0 {
				return approvalResponse(f.failWrite, "private write diagnostic"), nil
			}
			if !f.readbackMissing {
				f.enabled = value.Routes
			}
			if f.writeLost {
				return nil, errors.New("lost response with test-api-token")
			}
			return approvalResponse(200, "{}"), nil
		default:
			t.Fatal("unexpected API method")
			return nil, nil
		}
	})})
}
func approvalState(t *testing.T, client *Client, want string) RouteApproval {
	t.Helper()
	value, err := client.Routing(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if value.Approval.State != want {
		t.Fatalf("approval=%+v want %s", value.Approval, want)
	}
	if strings.Contains(value.Approval.Message, "test-secret") || strings.Contains(value.Approval.Message, "test-api-token") || strings.Contains(value.Approval.Message, "private") {
		t.Fatal("approval exposed secrets")
	}
	return value.Approval
}
func nextApproval(client *Client) {
	client.approval.mu.Lock()
	client.approval.nextCheck = time.Time{}
	client.approval.mu.Unlock()
}

func TestOAuthRouteApprovalLifecycle(t *testing.T) {
	f := newApprovalFixture()
	// Preserve a route approved by an administrator, even if no longer advertised.
	f.enabled = []string{"10.10.0.0/16"}
	client := f.client(t)
	approvalState(t, client, approvalPending)
	if len(f.requests) != 0 {
		t.Fatal("query sent cloud requests")
	}
	if err := client.EnsureRouteApproval(t.Context()); err != nil {
		t.Fatal(err)
	}
	approvalState(t, client, approvalApproved)
	want := []string{"0.0.0.0/0", "10.10.0.0/16", "192.168.42.0/24", "::/0", "fd00:1234::/64"}
	if len(f.writes) != 1 || !reflect.DeepEqual(f.writes[0], want) || f.gets != 2 || f.tokenCalls != 1 {
		t.Fatalf("writes=%v gets=%d tokens=%d", f.writes, f.gets, f.tokenCalls)
	}
	before := len(f.requests)
	if err := client.EnsureRouteApproval(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != before {
		t.Fatal("unchanged approval was not throttled")
	}
	nextApproval(client)
	if err := client.EnsureRouteApproval(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.writes) != 1 || f.tokenCalls != 1 {
		t.Fatal("already-approved routes or token were regenerated")
	}
	client.approval.api.expires = time.Now()
	nextApproval(client)
	if err := client.EnsureRouteApproval(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.tokenCalls != 2 {
		t.Fatal("expired token was reused")
	}

	if err := client.SetRouting(t.Context(), []string{"10.20.0.0/16"}); err != nil {
		t.Fatal(err)
	}
	approvalState(t, client, approvalPending)
	f.advertised = []string{"0.0.0.0/0", "::/0", "10.20.0.0/16"}
	if err := client.EnsureRouteApproval(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.writes) != 2 || !slices.Contains(f.writes[1], "10.20.0.0/16") {
		t.Fatal("changed routes not approved")
	}
	if err := client.SetRouting(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	f.advertised = []string{"0.0.0.0/0", "::/0"}
	if err := client.EnsureRouteApproval(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.writes) != 2 {
		t.Fatal("subnet withdrawal revoked approvals")
	}

	if err := client.UpdateOAuthCredentials(t.Context(), func(context.Context) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.credential.ClientSecret = "replacement-secret"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	approvalState(t, client, approvalPending)
	if client.approval.api.token != "" {
		t.Fatal("credential replacement retained token")
	}
	if err := client.EnsureRouteApproval(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.tokenCalls != 3 {
		t.Fatal("new credential not used")
	}
	if err := client.UpdateOAuthCredentials(t.Context(), func(context.Context) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.credential = OAuthCredentials{}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	approvalState(t, client, approvalDisabled)
	before = len(f.requests)
	if err := client.EnsureRouteApproval(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != before || client.approval.api.token != "" {
		t.Fatal("removed credentials used for API access")
	}
}

func TestOAuthRouteApprovalEmptyAndNormalizedRoutes(t *testing.T) {
	f := newApprovalFixture()
	f.enabled = nil // Tolerate a JSON null list before the first approval.
	f.advertised = []string{"0.0.0.0/0", "::/0", "192.168.42.0/24", "FD00:1234:0:0::/64"}
	client := f.client(t)
	if err := client.EnsureRouteApproval(t.Context()); err != nil {
		t.Fatal(err)
	}
	approvalState(t, client, approvalApproved)
	if len(f.writes) != 1 {
		t.Fatal("initial route approval missing")
	}
}

func TestOAuthRouteApprovalReadinessAndIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, state, id string
		noCredentials   bool
	}{
		{"missing credentials", "Running", "nDevice123", true},
		{"paused", "Stopped", "nDevice123", false},
		{"logged out", "NeedsLogin", "nDevice123", false},
		{"device approval required", "NeedsMachineAuth", "nDevice123", false},
		{"missing device ID", "Running", "", false},
		{"unsafe device ID", "Running", "../another-device", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newApprovalFixture()
			f.state, f.deviceID = tc.state, tc.id
			if tc.noCredentials {
				f.credential = OAuthCredentials{}
			}
			client := f.client(t)
			if err := client.EnsureRouteApproval(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(f.requests) != 0 {
				t.Fatal("API request before ready")
			}
		})
	}
	f := newApprovalFixture()
	client := f.client(t)
	if err := client.EnsureRouteApproval(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.deviceID, f.enabled = "nDifferentNode", []string{}
	approvalState(t, client, approvalPending)
	if err := client.EnsureRouteApproval(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.writes) != 2 {
		t.Fatal("new node not approved")
	}
}

func TestOAuthRouteApprovalFailuresAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*approvalFixture)
		want      error
	}{
		{"invalid credentials", func(f *approvalFixture) { f.failToken = 400 }, errApprovalCredentials},
		{"token unauthorized", func(f *approvalFixture) { f.failToken = 401 }, errApprovalCredentials},
		{"read forbidden", func(f *approvalFixture) { f.failRead = 403 }, errApprovalPermission},
		{"wrong tailnet", func(f *approvalFixture) { f.failRead = 404 }, errApprovalPermission},
		{"write forbidden", func(f *approvalFixture) { f.failWrite = 403 }, errApprovalPermission},
		{"rate limited", func(f *approvalFixture) { f.failRead = 429 }, errApprovalUnavailable},
		{"upstream down", func(f *approvalFixture) { f.failRead = 503 }, errApprovalUnavailable},
		{"control plane propagation", func(f *approvalFixture) { f.advertised = []string{} }, errApprovalPending},
		{"no confirmed readback", func(f *approvalFixture) { f.readbackMissing = true }, errApprovalReadback},
		{"lost successful reply", func(f *approvalFixture) { f.writeLost = true }, errApprovalUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newApprovalFixture()
			tc.configure(f)
			client := f.client(t)
			if err := client.EnsureRouteApproval(t.Context()); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			want := approvalError
			if tc.want == errApprovalPending {
				want = approvalPending
			}
			approvalState(t, client, want)
			calls := len(f.requests)
			_ = client.EnsureRouteApproval(t.Context())
			if len(f.requests) != calls {
				t.Fatal("failure retried without backoff")
			}
			lostWrites := len(f.writes)
			f.failToken, f.failRead, f.failWrite = 0, 0, 0
			f.readbackMissing, f.writeLost = false, false
			f.advertised = []string{"0.0.0.0/0", "::/0", "192.168.42.0/24", "fd00:1234::/64"}
			nextApproval(client)
			if err := client.EnsureRouteApproval(t.Context()); err != nil {
				t.Fatal(err)
			}
			approvalState(t, client, approvalApproved)
			if tc.name == "lost successful reply" && len(f.writes) != lostWrites {
				t.Fatal("lost write was blindly repeated")
			}
		})
	}
}

func TestOAuthRouteApprovalRechecksBeforeWrite(t *testing.T) {
	for _, change := range []string{"routes", "identity", "credentials", "paused"} {
		t.Run(change, func(t *testing.T) {
			f := newApprovalFixture()
			f.beforeWrite = func() {
				switch change {
				case "routes":
					f.routes = []string{"10.0.0.0/24"}
				case "identity":
					f.deviceID = "nNewDevice"
				case "credentials":
					f.credential = OAuthCredentials{}
				case "paused":
					f.state = "Stopped"
				}
			}
			client := f.client(t)
			if err := client.EnsureRouteApproval(t.Context()); !errors.Is(err, errApprovalPending) {
				t.Fatal(err)
			}
			if len(f.writes) != 0 {
				t.Fatal("stale approval was written")
			}
		})
	}
}

func TestOAuthRouteApprovalRejectsMalformedResponsesAndRedirects(t *testing.T) {
	for _, response := range []string{
		"{}", "null", `{"advertisedRoutes":[]}`, `{"advertisedRoutes":[],"enabledRoutes":["bad"]}`,
		`{"advertisedRoutes":[],"enabledRoutes":["192.168.42.1/24"]}`, strings.Repeat("x", (1<<20)+1),
	} {
		o := newOAuthRoutes(&http.Client{Transport: approvalTransport(func(*http.Request) (*http.Response, error) { return approvalResponse(200, response), nil })})
		if _, err := o.read(t.Context(), "/device/nDevice/routes", "token"); !errors.Is(err, errApprovalUnavailable) {
			t.Fatal("accepted invalid routes")
		}
	}
	for _, response := range []string{"{}", `{"access_token":"token","token_type":"Bearer","expires_in":0}`, `{"access_token":"token","token_type":"Basic","expires_in":3600}`} {
		o := newOAuthRoutes(&http.Client{Transport: approvalTransport(func(*http.Request) (*http.Response, error) { return approvalResponse(200, response), nil })})
		if _, err := o.accessToken(t.Context(), OAuthCredentials{"id", "secret"}); !errors.Is(err, errApprovalCredentials) {
			t.Fatal("accepted invalid token")
		}
	}
	calls := 0
	o := newOAuthRoutes(&http.Client{Transport: approvalTransport(func(*http.Request) (*http.Response, error) {
		calls++
		response := approvalResponse(307, "")
		response.Header.Set("Location", "https://attacker.invalid/collect")
		return response, nil
	})})
	if _, err := o.accessToken(t.Context(), OAuthCredentials{"id", "secret"}); err == nil || calls != 1 {
		t.Fatal("followed credential redirect")
	}
}

func TestOAuthRouteApprovalCancellationAndCredentialSerialization(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newApprovalFixture()
		client := f.client(t)
		entered := make(chan struct{})
		client.approval.api.http.Transport = approvalTransport(func(req *http.Request) (*http.Response, error) {
			close(entered)
			<-req.Context().Done()
			return nil, req.Context().Err()
		})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- client.EnsureRouteApproval(ctx) }()
		<-entered
		cleared := make(chan error, 1)
		go func() {
			cleared <- client.UpdateOAuthCredentials(t.Context(), func(context.Context) error {
				f.mu.Lock()
				defer f.mu.Unlock()
				f.credential = OAuthCredentials{}
				return nil
			})
		}()
		synctest.Wait()
		select {
		case <-cleared:
			t.Fatal("credentials changed during an approval request")
		default:
		}
		cancel()
		if err := <-done; err == nil {
			t.Fatal("canceled API succeeded")
		}
		if err := <-cleared; err != nil {
			t.Fatal(err)
		}
		approvalState(t, client, approvalDisabled)
		if len(client.mutations) != 0 || client.approval.api.token != "" {
			t.Fatal("lock or token retained")
		}
	})
}

func TestOAuthRouteApprovalStartsWithDefaultLAN(t *testing.T) {
	f := newApprovalFixture()
	f.routes = nil
	f.advertised = []string{"0.0.0.0/0", "::/0", "192.168.42.0/24"}
	store := &memorySubnetDefaults{}
	client := f.client(t).WithSubnetDefaults(store, func(context.Context) ([]string, error) { return []string{"192.168.42.0/24"}, nil })
	if err := client.EnsureExitNode(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := client.EnsureSubnetDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := client.EnsureRouteApproval(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !store.configured || len(f.writes) != 1 || !slices.Contains(f.writes[0], "192.168.42.0/24") {
		t.Fatal("default LAN was not approved")
	}
}

func TestOAuthRouteApprovalWorkerLifecycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newApprovalFixture()
		client := f.client(t)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { defer close(done); client.MaintainRouting(ctx) }()
		synctest.Wait()
		approvalState(t, client, approvalApproved)
		f.mu.Lock()
		if len(f.writes) != 1 {
			t.Fatal("startup did not approve advertisements")
		}
		f.mu.Unlock()
		// Unchanged success does not generate requests every ten seconds.
		time.Sleep(30 * time.Second)
		synctest.Wait()
		f.mu.Lock()
		if f.gets != 2 || f.tokenCalls != 1 {
			t.Fatal("worker did not throttle successful approval")
		}
		// A cloud revocation is restored on the next scheduled fresh read.
		f.enabled = []string{}
		f.mu.Unlock()
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		approvalState(t, client, approvalApproved)
		f.mu.Lock()
		if len(f.writes) != 2 {
			t.Fatal("worker did not recheck approval")
		}
		f.mu.Unlock()
		cancel()
		<-done
		if len(client.mutations) != 0 {
			t.Fatal("worker leaked mutation lock")
		}
	})
}

func TestOAuthRouteApprovalTimeoutAndStaleStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newApprovalFixture()
		client := f.client(t)
		if err := client.EnsureRouteApproval(t.Context()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(6 * time.Minute)
		approvalState(t, client, approvalPending)
		client.approval.api.http.Transport = approvalTransport(func(req *http.Request) (*http.Response, error) {
			<-req.Context().Done()
			return nil, req.Context().Err()
		})
		before := time.Now()
		if err := client.EnsureRouteApproval(t.Context()); !errors.Is(err, errApprovalUnavailable) {
			t.Fatal(err)
		}
		if time.Since(before) > 20*time.Second || len(client.mutations) != 0 {
			t.Fatal("request deadline or lock cleanup failed")
		}
		approvalState(t, client, approvalError)
	})
}

func TestOAuthRouteApprovalCredentialFailure(t *testing.T) {
	f := newApprovalFixture()
	client := f.client(t)
	if err := client.EnsureRouteApproval(t.Context()); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("private database diagnostic")
	if err := client.UpdateOAuthCredentials(t.Context(), func(context.Context) error { return failure }); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if client.approval.api.token == "" {
		t.Fatal("failed credential removal cleared token")
	}
	client.approval.load = func(context.Context) (OAuthCredentials, error) { return OAuthCredentials{}, failure }
	if err := client.EnsureRouteApproval(t.Context()); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal("unsafe credential read error")
	}
	approvalState(t, client, approvalError)
	if client.approval.api.token != "" {
		t.Fatal("unreadable credential retained cached token")
	}
}
