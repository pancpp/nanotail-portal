package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/tailscale/hujson"
)

const relayTestStatus = `{"BackendState":"Running","HaveNodeKey":true,"CurrentTailnet":{"Name":"example.com"},"Self":{"ID":"nRelay123","TailscaleIPs":["fd7a:115c:a1e0::1","100.64.0.1"]}}`
const relayTestPolicy = `{"grants":[{"src":["*"],"dst":["100.64.0.1"],"app":{"tailscale.com/cap/relay":[]}}]}`

func relayTestCredentials(context.Context) (OAuthCredentials, error) {
	return OAuthCredentials{"relay-client", "relay-secret"}, nil
}

type relayPolicyFixture struct {
	policy, status                   string
	port                             *uint16
	requests                         []string
	posts, gets, tokens, localWrites int
	failToken, failRead, failWrite   int
	missingETag, readbackMissing     bool
	afterRead                        func()
}

func (f *relayPolicyFixture) httpClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: approvalTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "api.tailscale.com" || req.URL.Scheme != "https" {
			t.Fatalf("unexpected URL: %s", req.URL)
		}
		if _, ok := req.Context().Deadline(); !ok {
			t.Error("missing request deadline")
		}
		f.requests = append(f.requests, req.Method+" "+req.URL.Path)
		if req.URL.Path == "/api/v2/oauth/token" {
			f.tokens++
			if err := req.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if req.Method != "POST" || req.Form.Get("scope") != "policy_file" || req.Form.Get("client_id") != "relay-client" || req.Form.Get("client_secret") != "relay-secret" || req.Header.Get("Authorization") != "" {
				t.Fatal("incorrect OAuth request")
			}
			if f.failToken != 0 {
				return approvalResponse(f.failToken, "private relay-secret"), nil
			}
			return approvalResponse(200, `{"access_token":"relay-token","token_type":"Bearer","expires_in":3600}`), nil
		}
		if req.URL.Path != "/api/v2/tailnet/example.com/acl" || req.Header.Get("Authorization") != "Bearer relay-token" || req.Header.Get("Accept") != "application/hujson" {
			t.Fatal("incorrect policy request")
		}
		switch req.Method {
		case "GET":
			f.gets++
			if f.failRead != 0 {
				return approvalResponse(f.failRead, "private relay-token"), nil
			}
			if f.afterRead != nil {
				callback := f.afterRead
				f.afterRead = nil
				callback()
			}
			response := approvalResponse(200, f.policy)
			if !f.missingETag {
				response.Header.Set("ETag", `"version-`+strconv.Itoa(f.posts)+`"`)
			}
			return response, nil
		case "POST":
			if req.Header.Get("If-Match") != `"version-`+strconv.Itoa(f.posts)+`"` || req.Header.Get("Content-Type") != "application/hujson" {
				t.Fatal("missing conditional policy write")
			}
			f.posts++
			if f.failWrite != 0 {
				return approvalResponse(f.failWrite, "private relay-token"), nil
			}
			data, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !f.readbackMissing {
				f.policy = string(data)
			}
			return approvalResponse(200, `{}`), nil
		default:
			t.Fatalf("unexpected HTTP method %s", req.Method)
			return nil, nil
		}
	})}
}

func (f *relayPolicyFixture) client(t *testing.T) *Client {
	t.Helper()
	if f.status == "" {
		f.status = relayTestStatus
	}
	return NewClient("tailscale", "", time.Second, runnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch {
		case reflect.DeepEqual(args, []string{"status", "--json"}):
			return []byte(f.status), nil
		case reflect.DeepEqual(args, []string{"debug", "prefs"}):
			return json.Marshal(map[string]any{"WantRunning": true, "RelayServerPort": f.port})
		case len(args) == 2 && args[0] == "set" && strings.HasPrefix(args[1], "--relay-server-port="):
			f.localWrites++
			value := strings.TrimPrefix(args[1], "--relay-server-port=")
			f.port = nil
			if value != "" {
				if _, missing, err := addPeerRelayGrant([]byte(f.policy), "100.64.0.1"); err != nil || missing {
					t.Fatal("listener enabled before policy confirmation")
				}
				n, err := strconv.Atoi(value)
				if err != nil {
					t.Fatal(err)
				}
				f.port = new(uint16(n))
			}
			return nil, nil
		default:
			t.Fatalf("unexpected CLI call %v", args)
			return nil, nil
		}
	})).WithPeerRelayPolicy(relayTestCredentials, f.httpClient(t))
}

func TestPeerRelayPolicyLifecycle(t *testing.T) {
	original := `{
  // Preserve operators' comments, rules, and unknown fields.
  "acls":[{"action":"accept","src":["*"],"dst":["*:*"]}],
  "ssh":[], "autoApprovers":{"routes":{"10.0.0.0/8":["tag:router"]}},
  "future":{"large":9007199254740993},
}`
	f := &relayPolicyFixture{policy: original}
	c := f.client(t)
	if err := c.SetPeerRelay(t.Context(), true, 40001); err != nil {
		t.Fatal(err)
	}
	if f.posts != 1 || f.gets != 2 || f.localWrites != 1 || f.port == nil || *f.port != 40001 {
		t.Fatalf("unexpected calls: %+v", f)
	}
	before, _ := hujson.Standardize([]byte(original))
	after, _ := hujson.Standardize([]byte(f.policy))
	var want, got map[string]json.RawMessage
	_ = json.Unmarshal(before, &want)
	_ = json.Unmarshal(after, &got)
	var grant map[string]json.RawMessage
	_ = json.Unmarshal([]byte(relayTestPolicy), &grant)
	var actualGrants, expectedGrants any
	_ = json.Unmarshal(got["grants"], &actualGrants)
	_ = json.Unmarshal(grant["grants"], &expectedGrants)
	if !reflect.DeepEqual(actualGrants, expectedGrants) {
		t.Fatalf("wrong grant: %s", got["grants"])
	}
	delete(got, "grants")
	if !reflect.DeepEqual(want, got) || !strings.Contains(f.policy, "// Preserve operators' comments") {
		t.Fatalf("existing policy changed: %s", f.policy)
	}
	for _, port := range []int{40001, 45678} {
		if err := c.SetPeerRelay(t.Context(), true, port); err != nil {
			t.Fatal(err)
		}
	}
	if f.posts != 1 || f.tokens != 1 || f.localWrites != 2 {
		t.Fatalf("duplicate write/token: %+v", f)
	}
	requests := len(f.requests)
	if err := c.SetPeerRelay(t.Context(), false, 40001); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != requests || f.port != nil || f.posts != 1 {
		t.Fatal("disable changed cloud policy")
	}
	if err := c.UpdateOAuthCredentials(t.Context(), func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if c.relayPolicy.api.token != "" {
		t.Fatal("cached token survived credential update")
	}
}

func TestPeerRelayPolicyFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*relayPolicyFixture, *Client)
		want      error
		posts     int
	}{
		{"no configuration", func(_ *relayPolicyFixture, c *Client) { c.relayPolicy = nil }, ErrPeerRelayCredentials, 0},
		{"no credentials", func(_ *relayPolicyFixture, c *Client) {
			c.relayPolicy.load = func(context.Context) (OAuthCredentials, error) { return OAuthCredentials{}, nil }
		}, ErrPeerRelayCredentials, 0},
		{"credential read fails", func(_ *relayPolicyFixture, c *Client) {
			c.relayPolicy.load = func(context.Context) (OAuthCredentials, error) {
				return OAuthCredentials{}, errors.New("private database")
			}
		}, ErrPeerRelayCredentials, 0},
		{"token denied", func(f *relayPolicyFixture, _ *Client) { f.failToken = 400 }, ErrPeerRelayCredentials, 0},
		{"read denied", func(f *relayPolicyFixture, _ *Client) { f.failRead = 403 }, ErrPeerRelayCredentials, 0},
		{"read failure", func(f *relayPolicyFixture, _ *Client) { f.failRead = 503 }, ErrPeerRelayPolicy, 0},
		{"missing ETag", func(f *relayPolicyFixture, _ *Client) { f.missingETag = true }, ErrPeerRelayPolicy, 0},
		{"invalid policy", func(f *relayPolicyFixture, _ *Client) { f.policy = `{"grants":{}}` }, ErrPeerRelayPolicy, 0},
		{"write denied", func(f *relayPolicyFixture, _ *Client) { f.failWrite = 403 }, ErrPeerRelayCredentials, 1},
		{"write rejected", func(f *relayPolicyFixture, _ *Client) { f.failWrite = 400 }, ErrPeerRelayPolicy, 1},
		{"concurrent edit", func(f *relayPolicyFixture, _ *Client) { f.failWrite = 412 }, ErrPeerRelayPolicyConflict, 1},
		{"failed readback", func(f *relayPolicyFixture, _ *Client) { f.readbackMissing = true }, ErrPeerRelayPolicy, 1},
		{"missing identity", func(f *relayPolicyFixture, _ *Client) { f.status = `{"BackendState":"NeedsLogin"}` }, ErrPeerRelayIdentity, 0},
		{"identity changed", func(f *relayPolicyFixture, _ *Client) {
			f.afterRead = func() { f.status = strings.ReplaceAll(f.status, "100.64.0.1", "100.64.0.2") }
		}, ErrPeerRelayIdentity, 0},
		{"credentials changed", func(f *relayPolicyFixture, c *Client) {
			f.afterRead = func() {
				c.relayPolicy.load = func(context.Context) (OAuthCredentials, error) { return OAuthCredentials{}, nil }
			}
		}, ErrPeerRelayIdentity, 0},
		{"redirect blocked", func(f *relayPolicyFixture, _ *Client) { f.failWrite = 307 }, ErrPeerRelayPolicy, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &relayPolicyFixture{policy: `{}`}
			c := f.client(t)
			tc.configure(f, c)
			if err := c.SetPeerRelay(t.Context(), true, 40001); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if f.posts != tc.posts || f.localWrites != 0 {
				t.Fatalf("unexpected writes: %+v", f)
			}
			// Disabling remains available even when policy or credentials fail.
			f.port = new(uint16(40001))
			requests := len(f.requests)
			if err := c.SetPeerRelay(t.Context(), false, 40001); err != nil || len(f.requests) != requests {
				t.Fatalf("disable: %v", err)
			}
		})
	}
}

func TestAddPeerRelayGrant(t *testing.T) {
	for _, tc := range []struct {
		name, policy     string
		changed, invalid bool
	}{
		{"absent grants", `{"acls":[]}`, true, false},
		{"empty grants", `{"grants":[]}`, true, false},
		{"existing", relayTestPolicy, false, false},
		{"formatted", `{"grants":[{"app":{"tailscale.com/cap/relay":[ /*comment*/ ]},"dst":["100.64.0.1"],"src":["*"]}]}`, false, false},
		{"different IP", strings.ReplaceAll(relayTestPolicy, "100.64.0.1", "100.64.0.2"), true, false},
		{"restricted source", strings.ReplaceAll(relayTestPolicy, `"src":["*"]`, `"src":["tag:client"]`), true, false},
		{"conditional", strings.ReplaceAll(relayTestPolicy, `"src":`, `"srcPosture":["posture:managed"],"src":`), true, false},
		{"other grants", `{"grants":[{"src":["*"],"dst":["*"],"ip":["*"]}]}`, true, false},
		{"malformed", `{`, false, true},
		{"array", `[]`, false, true},
		{"null policy", `null`, false, true},
		{"null grants", `{"grants":null}`, false, true},
		{"duplicate keys", `{"grants":[],"grants":[]}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, changed, err := addPeerRelayGrant([]byte(tc.policy), "100.64.0.1")
			if (err != nil) != tc.invalid || changed != tc.changed {
				t.Fatalf("changed=%v err=%v", changed, err)
			}
			if err == nil {
				if _, again, err := addPeerRelayGrant(data, "100.64.0.1"); err != nil || again {
					t.Fatalf("not idempotent: %s %v", data, err)
				}
			}
		})
	}
}

func TestPeerRelayPolicyCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &relayPolicyFixture{policy: `{}`}
		c := f.client(t)
		f.afterRead = func() { time.Sleep(2 * time.Second) }
		if err := c.SetPeerRelay(t.Context(), true, 40001); err == nil {
			t.Fatal("deadline ignored")
		}
		if f.posts != 0 || f.localWrites != 0 {
			t.Fatal("write after deadline")
		}
	})
}

func TestPeerRelayTarget(t *testing.T) {
	for _, tc := range []struct {
		name           string
		state, tailnet string
		ips            []string
		want           string
	}{
		{"prefer IPv4", "Running", "example.com", []string{"fd7a:115c:a1e0::1", "100.64.0.1"}, "100.64.0.1"},
		{"IPv6 only", "Running", "example.com", []string{"fd7a:115c:a1e0:0::1"}, "fd7a:115c:a1e0::1"},
		{"signed in but stopped", "Stopped", "example.com", []string{"100.64.0.1"}, "100.64.0.1"},
		{"signed out", "NeedsLogin", "example.com", []string{"100.64.0.1"}, ""},
		{"no IP", "Running", "example.com", nil, ""},
		{"invalid IP", "Running", "example.com", []string{"*", "100.64.0.1/32", "127.0.0.1"}, ""},
		{"no tailnet", "Running", "", []string{"100.64.0.1"}, ""},
		{"token default disallowed", "Running", "-", []string{"100.64.0.1"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := Status{BackendState: tc.state, HaveNodeKey: true, CurrentTailnet: &Tailnet{Name: tc.tailnet}, Self: &Peer{ID: "nRelay123", IPs: tc.ips}}
			target, err := peerRelayTarget(status)
			if (err != nil) != (tc.want == "") || target.ip != tc.want {
				t.Fatalf("%+v %v", target, err)
			}
		})
	}
}

func TestPeerRelayPolicySerializesCredentialRemoval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &relayPolicyFixture{policy: `{}`}
		c := f.client(t)
		release := make(chan struct{})
		f.afterRead = func() { <-release }
		saved, removed := make(chan error, 1), make(chan error, 1)
		go func() { saved <- c.SetPeerRelay(t.Context(), true, 40001) }()
		synctest.Wait()
		go func() {
			removed <- c.UpdateOAuthCredentials(t.Context(), func(context.Context) error {
				c.relayPolicy.load = func(context.Context) (OAuthCredentials, error) { return OAuthCredentials{}, nil }
				return nil
			})
		}()
		synctest.Wait()
		select {
		case <-removed:
			t.Fatal("credentials changed during policy save")
		default:
		}
		close(release)
		if err := <-saved; err != nil {
			t.Fatal(err)
		}
		if err := <-removed; err != nil {
			t.Fatal(err)
		}
		requests := len(f.requests)
		if err := c.SetPeerRelay(t.Context(), true, 40001); !errors.Is(err, ErrPeerRelayCredentials) {
			t.Fatal(err)
		}
		if len(f.requests) != requests || c.relayPolicy.api.token != "" {
			t.Fatal("cloud access after credential removal")
		}
	})
}
