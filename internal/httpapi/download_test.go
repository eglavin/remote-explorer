package httpapi

import (
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"remote-explorer/internal/extfilter"
)

func TestDownload(t *testing.T) {
	s := newTestServer(t, "")
	rr := s.get(t, "/api/download?path=a.mp3")
	if rr.Code != http.StatusOK || rr.Body.String() != "0123456789" {
		t.Fatalf("status %d, body %q", rr.Code, rr.Body)
	}
	h := rr.Header()
	want := map[string]string{
		"Content-Disposition":    "attachment; filename=a.mp3",
		"Content-Length":         "10",
		"Accept-Ranges":          "bytes",
		"X-Content-Type-Options": "nosniff",
	}
	for k, v := range want {
		if h.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, h.Get(k), v)
		}
	}
	if h.Get("Last-Modified") == "" || h.Get("Content-Security-Policy") == "" {
		t.Errorf("missing headers: %v", h)
	}

	line := lastRequestLog(t, s.logs)
	if line["status"] != float64(200) || line["bytes_out"] != float64(10) || line["path"] != "a.mp3" {
		t.Errorf("log %v", line)
	}
}

func TestDownloadSubdir(t *testing.T) {
	s := newTestServer(t, "")
	if rr := s.get(t, "/api/download?path=sub/c.mp3"); rr.Code != http.StatusOK || rr.Body.String() != "c" {
		t.Errorf("status %d, body %q", rr.Code, rr.Body)
	}
}

func TestDownloadRange(t *testing.T) {
	s := newTestServer(t, "")
	req := httptest.NewRequest(http.MethodGet, "/api/download?path=a.mp3", nil)
	req.Header.Set("Range", "bytes=2-4")
	rr := s.do(t, req)
	if rr.Code != http.StatusPartialContent || rr.Body.String() != "234" {
		t.Errorf("status %d, body %q", rr.Code, rr.Body)
	}
	if got := rr.Header().Get("Content-Range"); got != "bytes 2-4/10" {
		t.Errorf("Content-Range = %q", got)
	}
}

func TestDownloadHead(t *testing.T) {
	s := newTestServer(t, "")
	rr := s.do(t, httptest.NewRequest(http.MethodHead, "/api/download?path=a.mp3", nil))
	if rr.Code != http.StatusOK || rr.Body.Len() != 0 || rr.Header().Get("Content-Length") != "10" {
		t.Errorf("status %d, body %q, length %q", rr.Code, rr.Body, rr.Header().Get("Content-Length"))
	}
}

func TestDownloadUnicodeName(t *testing.T) {
	s := newTestServer(t, "")
	name := "héllo wörld; 'x'.mp3"
	if err := os.WriteFile(filepath.Join(s.dir, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rr := s.get(t, "/api/download?path="+url.QueryEscape(name))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	_, params, err := mime.ParseMediaType(rr.Header().Get("Content-Disposition"))
	if err != nil || params["filename"] != name {
		t.Errorf("Content-Disposition %q parsed to %q, %v", rr.Header().Get("Content-Disposition"), params["filename"], err)
	}
}

func TestDownloadErrors(t *testing.T) {
	s := newFilteredTestServer(t, "", extfilter.Set{"mp3"})
	tests := []struct {
		query  string
		status int
		code   string
	}{
		{"", http.StatusBadRequest, "is_directory"},
		{"sub", http.StatusBadRequest, "is_directory"},
		{"missing.mp3", http.StatusNotFound, "not_found"},
		{"b.txt", http.StatusNotFound, "not_found"},
		{"../a.mp3", http.StatusForbidden, "path_escape"},
		{"sub/../../a.mp3", http.StatusForbidden, "path_escape"},
		{"/a.mp3", http.StatusForbidden, "path_escape"},
	}
	for _, tt := range tests {
		rr := s.get(t, "/api/download?path="+tt.query)
		body := decode[errorBody](t, rr)
		if rr.Code != tt.status || body.Code != tt.code {
			t.Errorf("path=%q: %d %+v, want %d %s", tt.query, rr.Code, body, tt.status, tt.code)
		}
		if rr.Header().Get("Content-Disposition") != "" {
			t.Errorf("path=%q: error response has Content-Disposition", tt.query)
		}
	}
}

func TestDownloadRequiresToken(t *testing.T) {
	const token = "download-token-123"
	s := newTestServer(t, token)
	if rr := s.get(t, "/api/download?path=a.mp3"); rr.Code != http.StatusUnauthorized {
		t.Errorf("no token: status %d", rr.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/download?path=a.mp3", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if rr := s.do(t, req); rr.Code != http.StatusOK || rr.Body.String() != "0123456789" {
		t.Errorf("with token: status %d, body %q", rr.Code, rr.Body)
	}
}
