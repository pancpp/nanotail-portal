package conf

import (
	"os"
	"testing"
)

func TestAccessConfiguration(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		if !GetBool("access_enable") {
			t.Error("IP reporting should default to enabled")
		}
		if got := GetString("access_api_prefix"); got != "https://tailscale.fairkid.ca/api/device/v1" {
			t.Errorf("default API prefix = %q", got)
		}
		if got := GetString("access_eth_name"); got != "eth0" {
			t.Errorf("default interface = %q", got)
		}
	})
	t.Run("YAML overrides", func(t *testing.T) {
		path := isolateConfig(t)
		const config = "access_enable: false\naccess_api_prefix: https://example.test/device\naccess_eth_name: lan0\n"
		if err := os.WriteFile(path, []byte(config), 0600); err != nil {
			t.Fatal(err)
		}
		if err := Init(); err != nil {
			t.Fatal(err)
		}
		if GetBool("access_enable") {
			t.Error("YAML opt-out was ignored")
		}
		if got := GetString("access_api_prefix"); got != "https://example.test/device" {
			t.Errorf("API prefix YAML override = %q", got)
		}
		if got := GetString("access_eth_name"); got != "lan0" {
			t.Errorf("interface YAML override = %q", got)
		}
	})
}
