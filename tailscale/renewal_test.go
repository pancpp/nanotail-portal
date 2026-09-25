package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type renewalFixture struct {
	status            map[string]any
	readErr, writeErr error
	reads, writes     int
	wait              bool
	client            *Client
}

func newRenewalFixture(t *testing.T) *renewalFixture {
	t.Helper()
	f := &renewalFixture{status: map[string]any{"BackendState": "Running", "HaveNodeKey": true, "Self": map[string]any{"PublicKey": "nodekey:old"}}}
	f.client = NewClient("/usr/bin/tailscale", "/run/test.sock", time.Second, runnerFunc(func(ctx context.Context, binary string, args ...string) ([]byte, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("missing timeout")
		}
		if binary != "/usr/bin/tailscale" || len(args) == 0 || args[0] != "--socket=/run/test.sock" {
			t.Fatalf("command: %s %v", binary, args)
		}
		if reflect.DeepEqual(args[1:], []string{"status", "--json"}) {
			f.reads++
			if f.readErr != nil {
				return nil, f.readErr
			}
			return json.Marshal(f.status)
		}
		if !reflect.DeepEqual(args[1:], []string{"debug", "localapi", "POST", "/localapi/v0/login-interactive"}) {
			t.Fatalf("unexpected command: %v", args)
		}
		f.writes++
		if f.wait {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return []byte("private daemon output"), f.writeErr
	}))
	return f
}

func prepareRenewal(t *testing.T, f *renewalFixture) string {
	t.Helper()
	value, err := f.client.RenewNodeKey(t.Context())
	if err != nil || value.State != "READY" || value.AttemptID == "" || value.AuthURL != "" || value.CanRenew {
		t.Fatalf("prepare=%+v err=%v", value, err)
	}
	return value.AttemptID
}

func TestNodeKeyRenewalLifecycle(t *testing.T) {
	f := newRenewalFixture(t)
	id := prepareRenewal(t, f)
	if f.writes != 0 {
		t.Fatal("preparing must not touch Tailscale")
	}
	if again := prepareRenewal(t, f); again != id {
		t.Fatal("duplicate preparation replaced attempt")
	}
	check := func(state string, can bool, authURL string) {
		t.Helper()
		value, err := f.client.KeyRenewal(t.Context())
		if err != nil || value != (KeyRenewal{State: state, CanRenew: can, AuthURL: authURL, AttemptID: id}) {
			t.Fatalf("status=%+v err=%v", value, err)
		}
	}
	check("READY", false, "")
	value, err := f.client.BeginNodeKeyRenewal(t.Context(), id)
	if err != nil || value.State != "STARTING" || value.AttemptID != id || f.writes != 1 {
		t.Fatalf("begin=%+v err=%v writes=%d", value, err, f.writes)
	}
	check("STARTING", false, "") // Running with the old key is not success.
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if _, err := f.client.BeginNodeKeyRenewal(t.Context(), id); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if f.writes != 1 {
		t.Fatalf("duplicate starts: %d", f.writes)
	}
	if _, err := f.client.CancelNodeKeyRenewal(t.Context(), id); !errors.Is(err, ErrKeyRenewalStarted) {
		t.Fatal(err)
	}
	f.status["BackendState"], f.status["AuthURL"] = "NeedsLogin", "https://login.tailscale.com/a/test"
	check("AWAITING_LOGIN", false, f.status["AuthURL"].(string))
	if _, err := f.client.RenewNodeKey(t.Context()); err != nil || f.writes != 1 {
		t.Fatalf("active flow restarted: %v", err)
	}
	delete(f.status, "AuthURL")
	f.status["Self"], f.status["BackendState"] = map[string]any{"PublicKey": "nodekey:new"}, "NeedsMachineAuth"
	check("AWAITING_APPROVAL", false, "")
	f.status["BackendState"] = "Starting"
	check("STARTING", false, "")
	f.status["BackendState"] = "Running"
	f.status["Self"] = map[string]any{"PublicKey": "nodekey:new", "KeyExpiry": "2020-01-01T00:00:00Z"}
	check("STARTING", false, "")
	f.status["Self"] = map[string]any{"PublicKey": "nodekey:new", "KeyExpiry": time.Now().Add(time.Hour)}
	check("COMPLETE", true, "")
	f.status["Self"] = map[string]any{"PublicKey": "nodekey:new", "KeyExpiry": nil}
	check("COMPLETE", true, "")
	oldID := id
	id = prepareRenewal(t, f)
	if id == oldID || f.writes != 1 {
		t.Fatal("another prepare must create a new ID, not start sign-in")
	}
	if _, err := f.client.CancelNodeKeyRenewal(t.Context(), oldID); !errors.Is(err, ErrKeyRenewalChanged) {
		t.Fatal(err)
	}
	check("READY", false, "")
}

func TestPreparedRenewalCancellation(t *testing.T) {
	f := newRenewalFixture(t)
	id := prepareRenewal(t, f)
	f.readErr = errors.New("daemon is down")
	reads := f.reads
	for range 2 {
		value, err := f.client.CancelNodeKeyRenewal(t.Context(), id)
		if err != nil || value.State != "CANCELLED" || value.AttemptID != id || f.writes != 0 || f.reads != reads {
			t.Fatalf("cancel=%+v err=%v reads=%d writes=%d", value, err, f.reads, f.writes)
		}
	}
	// A lost cancel response can be reconciled even while the daemon is down.
	value, err := f.client.KeyRenewal(t.Context())
	if err != nil || value.State != "CANCELLED" {
		t.Fatalf("read=%+v err=%v", value, err)
	}
	if _, err := f.client.BeginNodeKeyRenewal(t.Context(), id); !errors.Is(err, ErrKeyRenewalChanged) {
		t.Fatal(err)
	}
	f.readErr = nil
	newID := prepareRenewal(t, f)
	if newID == id {
		t.Fatal("reused cancelled ID")
	}
	for _, stale := range []string{"", "unknown", id} {
		if _, err := f.client.CancelNodeKeyRenewal(t.Context(), stale); !errors.Is(err, ErrKeyRenewalChanged) {
			t.Fatal(err)
		}
		if _, err := f.client.BeginNodeKeyRenewal(t.Context(), stale); !errors.Is(err, ErrKeyRenewalChanged) {
			t.Fatal(err)
		}
	}
	if f.writes != 0 {
		t.Fatal("cancel, stale start, or prepare mutated daemon")
	}
	f.status["Self"] = map[string]any{"PublicKey": "different-profile"}
	if _, err := f.client.BeginNodeKeyRenewal(t.Context(), newID); !errors.Is(err, ErrKeyRenewalChanged) {
		t.Fatal("must not reauthenticate a changed profile", err)
	}
	if _, err := f.client.CancelNodeKeyRenewal(t.Context(), newID); err != nil {
		t.Fatal(err)
	}
}

func TestRenewalBeginCancelRace(t *testing.T) {
	for range 30 {
		f := newRenewalFixture(t)
		id := prepareRenewal(t, f)
		var beginErr, cancelErr error
		var wg sync.WaitGroup
		wg.Go(func() { _, beginErr = f.client.BeginNodeKeyRenewal(t.Context(), id) })
		wg.Go(func() { _, cancelErr = f.client.CancelNodeKeyRenewal(t.Context(), id) })
		wg.Wait()
		if cancelErr == nil {
			if f.writes != 0 || !errors.Is(beginErr, ErrKeyRenewalChanged) {
				t.Fatalf("cancel won but daemon started: %v %d", beginErr, f.writes)
			}
		} else if beginErr != nil || !errors.Is(cancelErr, ErrKeyRenewalStarted) || f.writes != 1 {
			t.Fatalf("begin=%v cancel=%v writes=%d", beginErr, cancelErr, f.writes)
		}
	}
}

func TestRenewalStartRecovery(t *testing.T) {
	f := newRenewalFixture(t)
	id := prepareRenewal(t, f)
	f.writeErr = errors.New("private daemon diagnostic")
	if _, err := f.client.BeginNodeKeyRenewal(t.Context(), id); !errors.Is(err, ErrKeyRenewalStart) {
		t.Fatal(err)
	}
	if _, err := f.client.CancelNodeKeyRenewal(t.Context(), id); !errors.Is(err, ErrKeyRenewalStarted) {
		t.Fatal("uncertain start must not be cancelled", err)
	}
	f.client.keyRenewal.startedAt = time.Now().Add(-time.Minute)
	value, err := f.client.KeyRenewal(t.Context())
	if err != nil || value.State != "STARTING" || !value.CanRenew || f.writes != 1 {
		t.Fatalf("read retried write: %+v %v", value, err)
	}
	f.writeErr = nil
	if _, err := f.client.BeginNodeKeyRenewal(t.Context(), id); err != nil || f.writes != 2 {
		t.Fatal(err)
	}
	// Existing externally-started login: defer exposing its URL until Sign in,
	// and never report a verified rotation without the old key baseline.
	f = newRenewalFixture(t)
	f.status = map[string]any{"BackendState": "NeedsLogin", "AuthURL": "https://login.tailscale.com/a/existing"}
	id = prepareRenewal(t, f)
	value, err = f.client.BeginNodeKeyRenewal(t.Context(), id)
	if err != nil || value.State != "AWAITING_LOGIN" || f.writes != 0 {
		t.Fatalf("resume=%+v err=%v", value, err)
	}
	f.status = map[string]any{"BackendState": "Running", "HaveNodeKey": true, "Self": map[string]any{"PublicKey": "new"}}
	value, err = f.client.KeyRenewal(t.Context())
	if err != nil || value.State != "SIGNED_IN" {
		t.Fatalf("unknown baseline falsely completed: %+v %v", value, err)
	}
}

func TestRenewalPreparationValidation(t *testing.T) {
	for _, tc := range []struct {
		state string
		self  bool
		url   string
		want  error
	}{
		{"Running", true, "", nil}, {"Stopped", true, "", nil}, {"NeedsLogin", true, "", nil},
		{"NoState", true, "", ErrKeyRenewalUnconfigured}, {"NeedsLogin", false, "", nil},
		{"NeedsLogin", true, "javascript:secret", ErrKeyRenewalURL}, {"", true, "", ErrKeyRenewalUnavailable},
	} {
		f := newRenewalFixture(t)
		f.status["BackendState"] = tc.state
		f.status["AuthURL"] = tc.url
		if !tc.self {
			delete(f.status, "Self")
		}
		if _, err := f.client.RenewNodeKey(t.Context()); !errors.Is(err, tc.want) {
			t.Fatalf("state=%s err=%v", tc.state, err)
		}
		if f.writes != 0 {
			t.Fatal("preparation started daemon login")
		}
	}
}

func TestFirstTimeBrowserSignIn(t *testing.T) {
	for _, self := range []any{nil, map[string]any{}, map[string]any{"PublicKey": "nodekey:zero"}} {
		f := newRenewalFixture(t)
		f.status = map[string]any{"BackendState": "NeedsLogin", "HaveNodeKey": false, "Self": self}
		value, err := f.client.KeyRenewal(t.Context())
		if err != nil || value.State != "IDLE" || !value.CanRenew || f.writes != 0 {
			t.Fatalf("initial status=%+v err=%v", value, err)
		}
		id := prepareRenewal(t, f)
		if _, err := f.client.CancelNodeKeyRenewal(t.Context(), id); err != nil || f.writes != 0 {
			t.Fatal("closing first-time sign-in changed the daemon", err)
		}
		id = prepareRenewal(t, f)
		value, err = f.client.BeginNodeKeyRenewal(t.Context(), id)
		if err != nil || value.State != "STARTING" || f.writes != 1 {
			t.Fatalf("first sign-in=%+v err=%v writes=%d", value, err, f.writes)
		}
		f.status["AuthURL"] = "https://login.tailscale.com/a/first-login"
		value, err = f.client.KeyRenewal(t.Context())
		if err != nil || value.State != "AWAITING_LOGIN" || value.AuthURL != f.status["AuthURL"] {
			t.Fatalf("login link=%+v err=%v", value, err)
		}
		delete(f.status, "AuthURL")
		for _, tc := range []struct {
			state   string
			haveKey bool
			self    any
			want    string
		}{
			{"NeedsMachineAuth", true, map[string]any{"PublicKey": "new"}, "AWAITING_APPROVAL"},
			{"Starting", true, map[string]any{"PublicKey": "new"}, "STARTING"},
			{"Running", false, map[string]any{"PublicKey": "new"}, "STARTING"},
			{"Running", true, nil, "STARTING"},
			{"Running", true, map[string]any{}, "STARTING"},
			{"Running", true, map[string]any{"PublicKey": "new", "KeyExpiry": "2020-01-01T00:00:00Z"}, "STARTING"},
			{"Running", true, map[string]any{"PublicKey": "new", "KeyExpiry": time.Now().Add(time.Hour)}, "SIGNED_IN"},
			{"Running", true, map[string]any{"PublicKey": "new"}, "SIGNED_IN"},
		} {
			f.status = map[string]any{"BackendState": tc.state, "HaveNodeKey": tc.haveKey, "Self": tc.self}
			value, err = f.client.KeyRenewal(t.Context())
			if err != nil || value.State != tc.want || value.AttemptID != id || f.writes != 1 {
				t.Fatalf("status=%+v err=%v want=%s writes=%d", value, err, tc.want, f.writes)
			}
		}
		if _, err := f.client.BeginNodeKeyRenewal(t.Context(), id); err != nil || f.writes != 1 {
			t.Fatal("completed sign-in was started again", err)
		}
	}
}

func TestFirstSignInRejectsChangedProfile(t *testing.T) {
	f := newRenewalFixture(t)
	f.status = map[string]any{"BackendState": "NeedsLogin", "HaveNodeKey": false}
	id := prepareRenewal(t, f)
	f.status = map[string]any{"BackendState": "Running", "HaveNodeKey": true, "Self": map[string]any{"PublicKey": "external-login"}}
	if _, err := f.client.BeginNodeKeyRenewal(t.Context(), id); !errors.Is(err, ErrKeyRenewalChanged) || f.writes != 0 {
		t.Fatal("must not reauthenticate a profile joined after preparation", err)
	}
	if _, err := f.client.CancelNodeKeyRenewal(t.Context(), id); err != nil {
		t.Fatal(err)
	}
}

func TestNodeKeyRenewalURL(t *testing.T) {
	for _, value := range []string{"", "http://login.tailscale.com/a/test", "https://evil.test/a/test", "https://login.tailscale.com.evil.test/a/test", "https://login.tailscale.com@evil.test/a/test", "https://user@login.tailscale.com/a/test", "https://login.tailscale.com:444/a/test", "https://login.tailscale.com/a/", "https://login.tailscale.com/admin", "https://login.tailscale.com/a/test#fragment", "https://login.tailscale.com/a/te st", "https://login.tailscale.com/a/te\\st", "javascript:alert(1)"} {
		if validRenewalURL(value) {
			t.Errorf("accepted unsafe URL %q", value)
		}
	}
	if !validRenewalURL("https://login.tailscale.com/a/test") {
		t.Fatal("rejected Tailscale login")
	}
}

func TestNodeKeyRenewalContextCancellation(t *testing.T) {
	f := newRenewalFixture(t)
	id := prepareRenewal(t, f)
	f.client.timeout = 10 * time.Millisecond
	for _, call := range []func(context.Context) (KeyRenewal, error){f.client.KeyRenewal, f.client.RenewNodeKey,
		func(ctx context.Context) (KeyRenewal, error) { return f.client.BeginNodeKeyRenewal(ctx, id) }, func(ctx context.Context) (KeyRenewal, error) { return f.client.CancelNodeKeyRenewal(ctx, id) },
	} {
		f.client.mutations <- struct{}{}
		if _, err := call(t.Context()); !errors.Is(err, ErrKeyRenewalUnavailable) {
			t.Fatal(err)
		}
		<-f.client.mutations
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := call(ctx); !errors.Is(err, ErrKeyRenewalUnavailable) {
			t.Fatal(err)
		}
	}
	f.wait = true
	if _, err := f.client.BeginNodeKeyRenewal(t.Context(), id); !errors.Is(err, ErrKeyRenewalStart) {
		t.Fatal(err)
	}
	if len(f.client.mutations) != 0 {
		t.Fatal("mutation lock leaked")
	}
}
