package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
)

type runnerFunc func(context.Context, string, ...string) ([]byte, error)

func (f runnerFunc) Run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	return f(ctx, binary, args...)
}

func TestStatus(t *testing.T) {
	c := NewClient("/usr/bin/tailscale", "/run/tailscale.sock", time.Second, runnerFunc(func(_ context.Context, binary string, args ...string) ([]byte, error) {
		if binary != "/usr/bin/tailscale" || !reflect.DeepEqual(args, []string{"--socket=/run/tailscale.sock", "status", "--json"}) {
			t.Fatalf("unexpected command: %s %v", binary, args)
		}
		return []byte(`{"Version":"1.96.0","BackendState":"Running","TailscaleIPs":["100.64.0.1"],"CurrentTailnet":{"Name":"example.test"},"Self":{"ID":"self","HostName":"nanopi","KeyExpiry":"2027-01-01T00:00:00Z"},"Peer":{"b":{"ID":"b","HostName":"phone","Online":true,"RxBytes":30,"TxBytes":40},"a":{"ID":"a","HostName":"desktop","Online":false,"RxBytes":10,"TxBytes":20},"nil":null}}`), nil
	}))
	s, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.BackendState != "Running" || s.Self.Hostname != "nanopi" || s.Self.KeyExpiry == nil || s.Tailnet != "example.test" {
		t.Fatalf("bad status: %+v", s)
	}
	if len(s.Peers) != 2 || s.Peers[0].Hostname != "desktop" || s.RxBytes != 40 || s.TxBytes != 60 {
		t.Fatalf("bad peers: %+v", s)
	}
	if s.Health == nil {
		t.Fatal("health should encode as an array")
	}
}

func TestLoggedOutStatusAndInvalidOutput(t *testing.T) {
	for _, tc := range []struct {
		data  string
		valid bool
	}{
		{`{"BackendState":"NeedsLogin","AuthURL":"https://login.tailscale.com/a/test"}`, true},
		{`null`, false}, {`{}`, false}, {`bad`, false},
	} {
		c := NewClient("tailscale", "", time.Second, runnerFunc(func(context.Context, string, ...string) ([]byte, error) { return []byte(tc.data), nil }))
		s, err := c.Status(context.Background())
		if tc.valid && (err != nil || s.AuthURL == "" || s.Peers == nil || s.Self != nil) {
			t.Fatalf("logged-out status: %+v %v", s, err)
		}
		if tc.valid && (s.CurrentTailnet != nil || s.CertDomains != nil || s.ExtraRecords != nil || s.ClientVersion != nil) {
			t.Fatalf("absent status metadata must remain nil: %+v", s)
		}
		if !tc.valid && !errors.Is(err, ErrInvalidOutput) {
			t.Fatalf("accepted invalid output: %s", tc.data)
		}
	}
}

func TestStatusExpandedFields(t *testing.T) {
	data, err := os.ReadFile("testdata/status.json")
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient("tailscale", "", time.Second, runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		return data, nil
	}))
	s, err := c.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !s.TUN || !s.HaveNodeKey || s.Version != "1.102.4" || s.MagicDNSSuffix != "example.ts.net" ||
		s.CurrentTailnet == nil || !s.CurrentTailnet.MagicDNSEnabled || s.CurrentTailnet.Name != s.Tailnet ||
		s.CurrentTailnet.MagicDNSSuffix != s.MagicDNSSuffix {
		t.Fatalf("status fields were lost: %+v", s)
	}
	if !reflect.DeepEqual(s.CertDomains, []string{"nanotail.example.ts.net"}) ||
		!reflect.DeepEqual(s.ExtraRecords, []DNSRecord{{Name: "service.example.test", Type: "A", Value: "100.64.0.2"}}) ||
		!reflect.DeepEqual(s.ClientVersion, &ClientVersion{LatestVersion: "1.102.5", UrgentSecurityUpdate: true,
			Notify: true, NotifyURL: "https://tailscale.com/download", NotifyText: "Update available"}) {
		t.Fatalf("status metadata was lost: %+v", s)
	}
	if len(s.Peers) != 2 || s.Peers[0].ID != "peer-a" || s.Peers[1].ID != "peer-z" ||
		s.RxBytes != 5000000017 || s.TxBytes != 6000000029 {
		t.Fatalf("peers were not sorted or totaled correctly: %+v", s)
	}
	if s.Self == nil || s.Self.LastWrite != nil || s.Self.LastSeen != nil || s.Self.LastHandshake != nil || s.Self.KeyExpiry != nil {
		t.Fatalf("unknown timestamps must be nil: %+v", s.Self)
	}
	for _, peer := range []*Peer{s.Self, &s.Peers[1]} {
		if peer.IPs == nil || peer.AllowedIPs == nil || peer.Tags == nil || peer.PeerAPIURL == nil {
			t.Fatalf("required lists must be non-nil: %+v", peer)
		}
	}
}

func TestConfigDoesNotExposeKeys(t *testing.T) {
	c := NewClient("tailscale", "", time.Second, runnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if !reflect.DeepEqual(args, []string{"debug", "prefs"}) {
			t.Fatal(args)
		}
		return []byte(`{"WantRunning":true,"Hostname":"nanopi","CorpDNS":true,"RouteAll":false,"ExitNodeIP":"100.64.0.9","AdvertiseRoutes":["0.0.0.0/0","::/0","192.168.1.0/24"],"Persist":{"PrivateNodeKey":"do-not-export"}}`), nil
	}))
	cfg, err := c.Config(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AdvertiseExitNode || !cfg.AcceptDNS || cfg.AcceptRoutes || len(cfg.AdvertiseRoutes) != 1 {
		t.Fatalf("bad config: %+v", cfg)
	}
	data, _ := json.Marshal(cfg)
	if strings.Contains(string(data), "do-not-export") || strings.Contains(string(data), "Persist") {
		t.Fatal("private keys leaked")
	}
}

func TestPartialConfigUpdate(t *testing.T) {
	var calls [][]string
	c := NewClient("tailscale", "", time.Second, runnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, args)
		return nil, nil
	}))
	var update ConfigUpdate
	if err := json.Unmarshal([]byte(`{"accept_dns":false,"exit_node":"","advertise_routes":[]}`), &update); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateConfig(context.Background(), update); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"set", "--accept-dns=false", "--exit-node=", "--advertise-routes="}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("got %v, want %v", calls, want)
	}
	for _, data := range []string{`{}`, `null`, `{"hostname":"--reset"}`, `{"exit_node":"$(touch /tmp/bad)"}`, `{"advertise_routes":["192.168.1.1/24"]}`, `{"advertise_routes":["0.0.0.0/0"]}`, `{"advertise_routes":["bad"]}`} {
		var invalid ConfigUpdate
		if err := json.Unmarshal([]byte(data), &invalid); err != nil {
			t.Fatal(err)
		}
		if err := c.UpdateConfig(context.Background(), invalid); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("invalid config accepted: %s", data)
		}
	}
	if len(calls) != 1 {
		t.Fatal("invalid requests executed commands")
	}
	if err := c.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Down(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls[1], []string{"up"}) || !reflect.DeepEqual(calls[2], []string{"down"}) {
		t.Fatalf("unexpected controls: %v", calls)
	}
}

func TestTimeoutAndUnavailable(t *testing.T) {
	c := NewClient("tailscale", "", time.Millisecond, runnerFunc(func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	if _, err := c.Status(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	c = NewClient("tailscale", "", time.Second, runnerFunc(func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("secret diagnostic") }))
	e := echo.New()
	c.Register(e.Group("/api/tailscale"))
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest("GET", "/api/tailscale/status", nil))
	if w.Code != 503 || strings.Contains(w.Body.String(), "secret diagnostic") {
		t.Fatalf("unsafe error: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	r := httptest.NewRequest("PATCH", "/api/tailscale/config", strings.NewReader(`{"unknown":true}`))
	r.Header.Set("Content-Type", "application/json")
	e.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatalf("unknown field accepted: %d", w.Code)
	}
}
