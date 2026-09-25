package app

import (
	"strings"
	"testing"

	"github.com/pancpp/nanotail-portal/app/graph"
	"github.com/pancpp/nanotail-portal/tailscale"
)

func TestRoutingReadSurvivesUnknownHostReadiness(t *testing.T) {
	router := &routingMock{value: tailscale.Routing{BackendState: "Running", AdvertiseExitNode: true}}
	for _, host := range []graph.RoutingHostReader{nil, unavailableRoutingHost{}} {
		result := routingGraphQL(t, router, 1, `{tailscaleRouting{advertiseExitNode defaultSubnetRoutes ipv4Forwarding ipv6Forwarding lanWarning}}`, nil, host)
		if len(result["errors"]) != 0 {
			t.Fatalf("%s", result)
		}
		for _, want := range []string{`"advertiseExitNode":true`, `"defaultSubnetRoutes":[]`, `"ipv4Forwarding":null`, `"ipv6Forwarding":null`} {
			if !strings.Contains(string(result["data"]), want) {
				t.Fatalf("%s", result)
			}
		}
		if strings.Contains(string(result["data"]), "private") {
			t.Fatalf("leaked host diagnostic: %s", result)
		}
	}
}
