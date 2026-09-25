package conf

import (
	"os"
	"testing"
)

func TestVPNTrafficLEDConfiguration(t *testing.T) {
	t.Run("default and environment override", func(t *testing.T) {
		t.Setenv("NANOTAIL_VPN_TRAFFIC_LED", "")
		if !GetBool("vpn_traffic_led") {
			t.Fatal("traffic LED should default to enabled")
		}
		t.Setenv("NANOTAIL_VPN_TRAFFIC_LED", "false")
		if GetBool("vpn_traffic_led") {
			t.Fatal("environment opt-out was ignored")
		}
	})
	t.Run("YAML override", func(t *testing.T) {
		path := isolateConfig(t)
		if err := os.WriteFile(path, []byte("vpn_traffic_led: false\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := Init(); err != nil {
			t.Fatal(err)
		}
		if GetBool("vpn_traffic_led") {
			t.Fatal("YAML opt-out was ignored")
		}
	})
}
