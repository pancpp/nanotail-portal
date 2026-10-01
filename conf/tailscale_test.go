package conf

import (
	"os"
	"testing"
)

func TestTailscaleYAMLConfiguration(t *testing.T) {
	path := isolateConfig(t)
	const config = "tailscale_binary: /test/fake-tailscale\ntailscale_socket: /test/tailscaled.sock\ntailscale_timeout: 3s\n"
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"tailscale_binary":  "/test/fake-tailscale",
		"tailscale_socket":  "/test/tailscaled.sock",
		"tailscale_timeout": "3s",
	} {
		if got := GetString(key); got != want {
			t.Errorf("GetString(%q) = %q, want %q", key, got, want)
		}
	}
}
