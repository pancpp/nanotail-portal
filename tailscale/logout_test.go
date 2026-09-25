package tailscale

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestLogout(t *testing.T) {
	for _, tc := range []struct {
		name, status                   string
		commandErr, statusErr, wantErr error
	}{
		{name: "confirmed", status: `{"BackendState":"NeedsLogin","HaveNodeKey":false}`},
		{name: "command failure", commandErr: errors.New("private command output"), wantErr: ErrLogoutApply},
		{name: "status failure", statusErr: errors.New("private status output"), wantErr: ErrLogoutApply},
		{name: "invalid status", status: `{`, wantErr: ErrLogoutApply},
		{name: "still enrolled", status: `{"BackendState":"Running","HaveNodeKey":true}`, wantErr: ErrLogoutApply},
		{name: "key retained", status: `{"BackendState":"NeedsLogin","HaveNodeKey":true}`, wantErr: ErrLogoutApply},
		{name: "unknown state", status: `{"BackendState":"NoState","HaveNodeKey":false}`, wantErr: ErrLogoutApply},
		{name: "login pending", status: `{"BackendState":"NeedsLogin","HaveNodeKey":false,"AuthURL":"https://login.tailscale.com/a/private"}`, wantErr: ErrLogoutApply},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls [][]string
			var client *Client
			client = NewClient("/usr/bin/tailscale", "/run/test.sock", time.Second, runnerFunc(func(ctx context.Context, binary string, args ...string) ([]byte, error) {
				if binary != "/usr/bin/tailscale" || args[0] != "--socket=/run/test.sock" {
					t.Fatalf("unexpected command %s %v", binary, args)
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("missing command deadline")
				}
				if len(client.mutations) != 1 || client.keyRenewal != nil {
					t.Fatal("logout must hold the mutation lock and invalidate renewal before the command")
				}
				args = args[1:]
				calls = append(calls, args)
				switch {
				case reflect.DeepEqual(args, []string{"logout"}):
					return nil, tc.commandErr
				case reflect.DeepEqual(args, []string{"status", "--json"}):
					return []byte(tc.status), tc.statusErr
				default:
					t.Fatalf("unexpected command %v", args)
					return nil, nil
				}
			}))
			client.keyRenewal = &keyRenewalAttempt{id: "old-attempt", previousKey: "old-key"}
			if err := client.Logout(t.Context()); !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
			wantCalls := [][]string{{"logout"}, {"status", "--json"}}
			if tc.commandErr != nil {
				wantCalls = wantCalls[:1]
			}
			if !reflect.DeepEqual(calls, wantCalls) {
				t.Fatalf("commands=%v, want %v", calls, wantCalls)
			}
			if len(client.mutations) != 0 || client.keyRenewal != nil {
				t.Fatal("logout leaked a lock or retained an old renewal")
			}
			if _, err := client.BeginNodeKeyRenewal(t.Context(), "old-attempt"); !errors.Is(err, ErrKeyRenewalChanged) {
				t.Fatalf("old sign-in attempt was not rejected: %v", err)
			}
		})
	}
}

func TestLogoutClearsPendingRenewal(t *testing.T) {
	for _, started := range []bool{false, true} {
		client := NewClient("tailscale", "", time.Second, runnerFunc(func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if reflect.DeepEqual(args, []string{"logout"}) {
				return nil, nil
			}
			if !reflect.DeepEqual(args, []string{"status", "--json"}) {
				t.Fatalf("unexpected command %v", args)
			}
			return []byte(`{"BackendState":"NeedsLogin","HaveNodeKey":false}`), nil
		}))
		client.keyRenewal = &keyRenewalAttempt{id: "old-attempt"}
		if started {
			client.keyRenewal.startedAt = time.Now()
		}
		if err := client.Logout(t.Context()); err != nil {
			t.Fatal(err)
		}
		value, err := client.KeyRenewal(t.Context())
		if err != nil || value.State != "IDLE" || !value.CanRenew || value.AttemptID != "" || value.AuthURL != "" {
			t.Fatalf("renewal after logout=%+v err=%v", value, err)
		}
	}
}

func TestLogoutLockAndTimeout(t *testing.T) {
	client := NewClient("tailscale", "", 20*time.Millisecond, runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("must not run while locked or already cancelled")
		return nil, nil
	}))
	attempt := &keyRenewalAttempt{id: "keep-until-write"}
	client.keyRenewal = attempt
	client.mutations <- struct{}{}
	if err := client.Logout(t.Context()); !errors.Is(err, ErrConnectionUnavailable) {
		t.Fatal(err)
	}
	<-client.mutations
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := client.Logout(ctx); !errors.Is(err, ErrConnectionUnavailable) {
		t.Fatal(err)
	}
	if client.keyRenewal != attempt || len(client.mutations) != 0 {
		t.Fatal("cancellation before execution changed the attempt or leaked the lock")
	}
	calls := 0
	client = NewClient("tailscale", "", 20*time.Millisecond, runnerFunc(func(ctx context.Context, _ string, args ...string) ([]byte, error) {
		calls++
		if !reflect.DeepEqual(args, []string{"logout"}) {
			t.Fatal(args)
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	if err := client.Logout(t.Context()); !errors.Is(err, ErrLogoutApply) {
		t.Fatal(err)
	}
	if calls != 1 || len(client.mutations) != 0 {
		t.Fatal("timed-out logout was retried or leaked the mutation lock")
	}
}
