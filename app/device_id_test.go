package app

import (
	"net/http"
	"testing"
)

func TestDeviceIDRequiresAuthentication(t *testing.T) {
	e := newTestApp(t)
	for _, authorization := range []string{"", "Bearer invalid"} {
		w := appRequest(e, http.MethodPost, "/api/v1/query", `{"query":"query { deviceID }"}`, authorization)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated device ID query returned %d: %s", w.Code, w.Body.String())
		}
	}
}
