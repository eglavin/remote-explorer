package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"remote-explorer/internal/extfilter"
)

func TestCrossOriginUploadRejected(t *testing.T) {
	for _, token := range []string{"", "s3cret-token-value"} {
		s := newWritableTestServer(t, testConfig{token: token})
		send := func(name string, headers map[string]string) *httptest.ResponseRecorder {
			t.Helper()
			body, ct := multipartBody(t, uploadFile{name, "x"})
			req := httptest.NewRequest(http.MethodPost, "/api/upload", body)
			req.Host = "127.0.0.1:8080"
			req.Header.Set("Content-Type", ct)
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			for k, v := range headers {
				req.Header.Set(k, v)
			}
			return s.do(t, req)
		}

		for _, headers := range []map[string]string{
			{"Sec-Fetch-Site": "cross-site"},
			{"Sec-Fetch-Site": "same-site"},
			{"Origin": "https://evil.example"},
			{"Origin": "null"},
		} {
			rr := send("csrf.txt", headers)
			if rr.Code != http.StatusForbidden || decode[errorBody](t, rr).Code != "cross_origin" {
				t.Errorf("token=%q %v: %d %s, want 403 cross_origin", token, headers, rr.Code, rr.Body)
			}
			if _, ok := s.read(t, "csrf.txt"); ok {
				t.Fatalf("token=%q %v: cross-origin upload was written", token, headers)
			}
		}

		for i, headers := range []map[string]string{
			nil, // curl and other non-browser clients
			{"Sec-Fetch-Site": "same-origin"},
			{"Origin": "http://127.0.0.1:8080"},
		} {
			name := string(rune('a'+i)) + "-ok.txt"
			if rr := send(name, headers); rr.Code != http.StatusCreated {
				t.Errorf("token=%q %v: %d %s, want 201", token, headers, rr.Code, rr.Body)
			}
		}
	}
}

func TestHostCheckWithoutToken(t *testing.T) {
	s := newConfiguredTestServer(t, testConfig{allowedHosts: []string{"nas.lan"}})
	for host, want := range map[string]int{
		"127.0.0.1:8080":        http.StatusOK,
		"localhost:8080":        http.StatusOK,
		"LOCALHOST":             http.StatusOK,
		"localhost.:8080":       http.StatusOK,
		"[::1]:8080":            http.StatusOK,
		"[::1]":                 http.StatusOK,
		"192.168.1.20":          http.StatusOK,
		"nas.lan:8080":          http.StatusOK,
		"NAS.LAN":               http.StatusOK,
		"evil.example:8080":     http.StatusForbidden,
		"evil.example":          http.StatusForbidden,
		"localhost.evil.test":   http.StatusForbidden,
		"127.0.0.1.nip.io:8080": http.StatusForbidden,
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/list", nil)
		req.Host = host
		rr := s.do(t, req)
		if rr.Code != want {
			t.Errorf("Host %q: %d %s, want %d", host, rr.Code, rr.Body, want)
		}
		if want == http.StatusForbidden && decode[errorBody](t, rr).Code != "host_not_allowed" {
			t.Errorf("Host %q: code %s, want host_not_allowed", host, rr.Body)
		}
	}
}

// With a token, a rebinding page cannot authenticate, so any Host is fine
// (reverse proxies often forward their own name).
func TestHostNotCheckedWithToken(t *testing.T) {
	const token = "s3cret-token-value"
	s := newTestServer(t, token)
	req := httptest.NewRequest(http.MethodGet, "/api/list", nil)
	req.Host = "files.example.org"
	req.Header.Set("Authorization", "Bearer "+token)
	if rr := s.do(t, req); rr.Code != http.StatusOK {
		t.Errorf("status %d %s, want 200", rr.Code, rr.Body)
	}
}

func TestHiddenFilesLookMissingOverHTTP(t *testing.T) {
	s := newWritableTestServer(t, testConfig{visible: extfilter.Set{"mp3"}})
	missing := s.get(t, "/api/list?path=nope.txt").Body.String()
	for _, target := range []string{
		"/api/list?path=b.txt",
		"/api/list?path=b.txt/x",
		"/api/download?path=b.txt",
		"/api/download?path=b.txt/x",
	} {
		rr := s.get(t, target)
		if rr.Code != http.StatusNotFound || rr.Body.String() != missing {
			t.Errorf("%s: %d %s, want the same 404 as a missing file", target, rr.Code, rr.Body)
		}
	}
	for _, query := range []string{"?path=b.txt", "?path=b.txt/x&mkdirs=true"} {
		if rr := s.upload(t, query, uploadFile{"x.mp3", "x"}); rr.Code != http.StatusNotFound {
			t.Errorf("upload %s: %d %s, want 404", query, rr.Code, rr.Body)
		}
	}
	if rr := s.get(t, "/api/list?path=a.mp3"); rr.Code != http.StatusBadRequest {
		t.Errorf("visible file listed as a folder: %d, want 400 not_a_directory", rr.Code)
	}
}
