package app

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/pancpp/nanotail-portal/conf"
)

func TestPortalVersion(t *testing.T) {
	// No database, device metrics or Tailscale access is needed for build metadata.
	e := newTestApp(t)
	const body = `{"operationName":"PortalVersion","query":"query PortalVersion { portalVersion }"}`
	for _, authorization := range []string{"", "Bearer invalid"} {
		w := appRequest(e, http.MethodPost, "/api/v1/query", body, authorization)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated version request returned %d: %s", w.Code, w.Body.String())
		}
	}
	w := appRequest(e, http.MethodPost, "/api/v1/query", body, testAuthorization(t, 42))
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("version response: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	var response struct {
		Data struct {
			Version *string `json:"portalVersion"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	want, _, _, _ := conf.GetVersion()
	if len(response.Errors) != 0 || response.Data.Version == nil || *response.Data.Version != want {
		t.Fatalf("expected conf.gVersion %q, got %s", want, w.Body.String())
	}
}
