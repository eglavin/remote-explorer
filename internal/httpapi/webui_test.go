package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestWebUIOffByDefault(t *testing.T) {
	s := newTestServer(t, "")
	for _, target := range []string{"/", "/?path=sub", "/ui/app.js", "/ui/style.css"} {
		if rr := s.get(t, target); rr.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404 without WebUI", target, rr.Code)
		}
	}
}

func TestWebUIServedWithoutToken(t *testing.T) {
	s := newConfiguredTestServer(t, testConfig{token: "s3cret-token-value", webUI: true})
	for target, wantType := range map[string]string{
		"/":                    "text/html",
		"/?path=sub":           "text/html",
		"/ui/app.js":           "text/javascript",
		"/ui/style.css":        "text/css",
		"/ui/icons/folder.svg": "image/svg+xml",
	} {
		rr := s.get(t, target)
		if rr.Code != http.StatusOK {
			t.Errorf("%s: %d, want 200", target, rr.Code)
			continue
		}
		if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, wantType) {
			t.Errorf("%s: Content-Type %q, want %s", target, ct, wantType)
		}
		if csp := rr.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Errorf("%s: Content-Security-Policy %q", target, csp)
		}
	}
}

func TestWebUINoListingsOrStrayFiles(t *testing.T) {
	s := newConfiguredTestServer(t, testConfig{webUI: true})
	for _, target := range []string{"/ui/", "/ui/icons/", "/ui/icons", "/ui/index.html", "/ui/missing.js", "/index.html", "/app.js"} {
		if rr := s.get(t, target); rr.Code != http.StatusNotFound {
			t.Errorf("%s: %d %q, want 404", target, rr.Code, rr.Body)
		}
	}
	if rr := s.do(t, mustRequest(t, http.MethodPost, "/")); rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /: %d, want 405", rr.Code)
	}
}

func mustRequest(t *testing.T, method, target string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}
