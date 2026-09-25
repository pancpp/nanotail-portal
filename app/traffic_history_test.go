package app

import (
	"net/http"
	"testing"
)

func TestNetworkActivityHistoryRequiresAuthentication(t *testing.T) {
	e := newTestApp(t)
	for _, authorization := range []string{"", "Bearer invalid"} {
		w := appRequest(e, http.MethodPost, "/api/v1/query", `{"query":"query { networkActivityHistory { windowStart windowEnd hours { startedAt rxBytes txBytes observedSeconds } } }"}`, authorization)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
		}
	}
}
