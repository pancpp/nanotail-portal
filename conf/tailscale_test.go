package conf

import "testing"

func TestTailscaleBinaryEnvironmentOverride(t *testing.T) {
	t.Setenv("NANOTAIL_TAILSCALE_BINARY", "/test/fake-tailscale")
	if GetString("tailscale_binary") != "/test/fake-tailscale" {
		t.Fatal("Tailscale binary environment override was ignored")
	}
}
