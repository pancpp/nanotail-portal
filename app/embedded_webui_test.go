//go:build embedwebui

package app

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

func TestEmbeddedWebUI(t *testing.T) {
	// Static files must be served from the embedded build, not the working directory.
	t.Chdir(t.TempDir())
	e := newTestApp(t)
	w := appRequest(e, http.MethodGet, "/", "", "")
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("WebUI: status %d, content type %q", w.Code, w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Body.String(), `id="root"`) {
		t.Fatal("WebUI is missing the React root element")
	}
	// Discover hashed asset names from the embedded HTML so rebuilds need no test edits.
	assets := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`).FindAllStringSubmatch(w.Body.String(), -1)
	if len(assets) == 0 {
		t.Fatal("WebUI does not reference any built assets")
	}
	for _, asset := range assets {
		t.Run(asset[1], func(t *testing.T) {
			w := appRequest(e, http.MethodGet, asset[1], "", "")
			if w.Code != http.StatusOK || w.Body.Len() == 0 {
				t.Fatalf("asset: status %d, body length %d", w.Code, w.Body.Len())
			}
			if strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
				t.Fatal("asset request returned HTML")
			}
		})
	}
	w = appRequest(e, http.MethodGet, "/assets/missing.js", "", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing asset: got %d, want 404", w.Code)
	}
}
