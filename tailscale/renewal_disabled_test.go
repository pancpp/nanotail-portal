package tailscale

import (
	"errors"
	"testing"
	"time"
)

func TestDisabledExpiryPreventsRenewal(t *testing.T) {
	for _, state := range []string{"Running", "Stopped", "Starting", "NeedsLogin"} {
		for _, expiry := range []any{nil, "0001-01-01T00:00:00Z"} {
			for _, authURL := range []string{"", "https://login.tailscale.com/a/existing"} {
				f := newRenewalFixture(t)
				f.status["BackendState"] = state
				f.status["AuthURL"] = authURL
				f.status["Self"] = map[string]any{"PublicKey": "nodekey:old", "KeyExpiry": expiry}
				value, err := f.client.KeyRenewal(t.Context())
				if err != nil || value.CanRenew || value.State != "IDLE" {
					t.Fatalf("state=%s expiry=%v read=%+v err=%v", state, expiry, value, err)
				}
				if _, err := f.client.RenewNodeKey(t.Context()); !errors.Is(err, ErrKeyRenewalDisabled) {
					t.Fatalf("state=%s expiry=%v err=%v", state, expiry, err)
				}
				if f.writes != 0 || f.client.keyRenewal != nil {
					t.Fatal("disabled renewal created an attempt or touched the daemon")
				}
			}
		}
	}
}

func TestDisabledExpiryBlocksPreparedRenewal(t *testing.T) {
	f := newRenewalFixture(t)
	id := prepareRenewal(t, f)
	f.status["Self"] = map[string]any{"PublicKey": "nodekey:old"} // omitted expiry also means disabled
	for _, authURL := range []string{"", "https://login.tailscale.com/a/existing"} {
		f.status["AuthURL"] = authURL
		if _, err := f.client.BeginNodeKeyRenewal(t.Context(), id); !errors.Is(err, ErrKeyRenewalDisabled) {
			t.Fatal(err)
		}
		if _, err := f.client.RenewNodeKey(t.Context()); !errors.Is(err, ErrKeyRenewalDisabled) {
			t.Fatal(err)
		}
	}
	if f.writes != 0 || !f.client.keyRenewal.startedAt.IsZero() {
		t.Fatal("disabled prepared request started authentication")
	}
	if value, err := f.client.CancelNodeKeyRenewal(t.Context(), id); err != nil || value.State != "CANCELLED" {
		t.Fatalf("%+v %v", value, err)
	}
}

func TestDisabledExpiryPreventsStalledRenewalRetry(t *testing.T) {
	f := newRenewalFixture(t)
	id := prepareRenewal(t, f)
	if _, err := f.client.BeginNodeKeyRenewal(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	f.client.keyRenewal.startedAt = time.Now().Add(-time.Minute)
	f.status["Self"] = map[string]any{"PublicKey": "nodekey:old", "KeyExpiry": nil}
	value, err := f.client.KeyRenewal(t.Context())
	if err != nil || value.CanRenew {
		t.Fatalf("%+v %v", value, err)
	}
	if _, err := f.client.BeginNodeKeyRenewal(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if f.writes != 1 {
		t.Fatal("disabled expiry allowed another login command")
	}
}
