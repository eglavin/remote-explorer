package httpapi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"remote-explorer/internal/extfilter"
	"remote-explorer/internal/fsvc"
)

type testServer struct {
	handler http.Handler
	logs    *bytes.Buffer
	dir     string
}

type testConfig struct {
	token           string
	visible, upload extfilter.Set
	info            Info
	allowedHosts    []string
}

// newTestServer serves a tree of {a.mp3, b.txt, sub/c.mp3} read-only with no extension filter.
func newTestServer(t *testing.T, token string) *testServer {
	return newConfiguredTestServer(t, testConfig{token: token})
}

func newFilteredTestServer(t *testing.T, token string, visible extfilter.Set) *testServer {
	return newConfiguredTestServer(t, testConfig{token: token, visible: visible})
}

func newConfiguredTestServer(t *testing.T, cfg testConfig) *testServer {
	t.Helper()
	if cfg.info.VisibleExtensions == nil {
		cfg.info.VisibleExtensions = cfg.visible.List()
	}
	if cfg.info.UploadExtensions == nil {
		cfg.info.UploadExtensions = cfg.upload.List()
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"a.mp3": "0123456789", "b.txt": "text", "sub/c.mp3": "c"}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })

	logs := &bytes.Buffer{}
	h := New(Options{
		Service: fsvc.New(root, cfg.visible, cfg.upload),
		Logger:  slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Info:    cfg.info,
		Token:   cfg.token,
		// httptest.NewRequest sends "Host: example.com".
		AllowedHosts: append([]string{"example.com"}, cfg.allowedHosts...),
	})
	return &testServer{handler: h, logs: logs, dir: dir}
}

func (s *testServer) do(t *testing.T, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	s.handler.ServeHTTP(rr, req)
	return rr
}

func (s *testServer) get(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	return s.do(t, httptest.NewRequest(http.MethodGet, target, nil))
}

// logLines returns every log line as a decoded JSON object.
func logLines(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(logs.Bytes()))
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("bad log line %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	return out
}

// requestLogs returns every "request" log line.
func requestLogs(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, m := range logLines(t, logs) {
		if m["msg"] == "request" {
			out = append(out, m)
		}
	}
	return out
}

func lastRequestLog(t *testing.T, logs *bytes.Buffer) map[string]any {
	t.Helper()
	lines := requestLogs(t, logs)
	if len(lines) == 0 {
		t.Fatal("no request log lines")
	}
	return lines[len(lines)-1]
}

func decode[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return v
}

func TestList(t *testing.T) {
	s := newTestServer(t, "")
	rr := s.get(t, "/api/list")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	l := decode[fsvc.Listing](t, rr)
	if len(l.Entries) != 3 || l.Entries[0].Name != "sub" {
		t.Errorf("entries = %+v", l.Entries)
	}

	rr = s.get(t, "/api/list?path=sub")
	l = decode[fsvc.Listing](t, rr)
	if rr.Code != http.StatusOK || l.Path != "sub" || len(l.Entries) != 1 || l.Entries[0].Path != "sub/c.mp3" {
		t.Errorf("sub: status %d, listing %+v", rr.Code, l)
	}
}

func TestListErrors(t *testing.T) {
	s := newTestServer(t, "")
	tests := []struct {
		query  string
		status int
		code   string
		level  string
	}{
		{"../x", http.StatusForbidden, "path_escape", "WARN"},
		{"sub/../../x", http.StatusForbidden, "path_escape", "WARN"},
		{"%2e%2e/x", http.StatusForbidden, "path_escape", "WARN"},
		{`..%5Cx`, http.StatusForbidden, "path_escape", "WARN"},
		{"/etc", http.StatusForbidden, "path_escape", "WARN"},
		{"a%00b", http.StatusBadRequest, "invalid_path", "WARN"},
		{"missing", http.StatusNotFound, "not_found", "WARN"},
		{"a.mp3", http.StatusBadRequest, "not_a_directory", "WARN"},
	}
	for _, tt := range tests {
		rr := s.get(t, "/api/list?path="+tt.query)
		body := decode[errorBody](t, rr)
		if rr.Code != tt.status || body.Code != tt.code {
			t.Errorf("path=%s: %d %+v, want %d %s", tt.query, rr.Code, body, tt.status, tt.code)
		}
		line := lastRequestLog(t, s.logs)
		if line["level"] != tt.level || line["error_code"] != tt.code || line["status"] != float64(tt.status) {
			t.Errorf("path=%s: log %v", tt.query, line)
		}
	}
}

func TestRequestLogFields(t *testing.T) {
	s := newTestServer(t, "")
	req := httptest.NewRequest(http.MethodGet, "/api/list?path=sub", nil)
	req.Header.Set("User-Agent", "test-agent")
	rr := s.do(t, req)

	line := lastRequestLog(t, s.logs)
	id := rr.Header().Get("X-Request-ID")
	if id == "" || line["req_id"] != id {
		t.Errorf("req_id header %q, log %v", id, line["req_id"])
	}
	want := map[string]any{
		"level":      "INFO",
		"method":     "GET",
		"url_path":   "/api/list",
		"path":       "sub",
		"status":     float64(200),
		"bytes_out":  float64(rr.Body.Len()),
		"user_agent": "test-agent",
		"remote":     req.RemoteAddr,
	}
	for k, v := range want {
		if line[k] != v {
			t.Errorf("log %s = %v, want %v", k, line[k], v)
		}
	}
	if _, ok := line["duration_ms"]; !ok {
		t.Error("log has no duration_ms")
	}
}

func TestLogsUnknownRoutes(t *testing.T) {
	s := newTestServer(t, "")
	rr := s.get(t, "/nope")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rr.Code)
	}
	if line := lastRequestLog(t, s.logs); line["status"] != float64(404) || line["level"] != "WARN" {
		t.Errorf("log %v", line)
	}
}

func TestToken(t *testing.T) {
	const token = "s3cret-token-value"
	s := newTestServer(t, token)

	rr := s.get(t, "/api/list")
	if rr.Code != http.StatusUnauthorized || decode[errorBody](t, rr).Code != "unauthorized" {
		t.Errorf("no token: %d %s", rr.Code, rr.Body)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/list", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	if rr := s.do(t, req); rr.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/list", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if rr := s.do(t, req); rr.Code != http.StatusOK {
		t.Errorf("right token: %d", rr.Code)
	}

	if rr := s.get(t, "/healthz"); rr.Code != http.StatusOK {
		t.Errorf("healthz without token: %d", rr.Code)
	}
	if strings.Contains(s.logs.String(), token) {
		t.Error("token appears in logs")
	}
}

func TestInfo(t *testing.T) {
	s := newTestServer(t, "")
	rr := s.get(t, "/api/info")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	want := `{"writable":false,"overwrite":false,"visibleExtensions":[],"uploadExtensions":[],"maxUpload":0}`
	if got := strings.TrimSpace(rr.Body.String()); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestPanicIsLogged(t *testing.T) {
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	h := withLogging(logger, false, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil))

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d", rr.Code)
	}
	line := lastRequestLog(t, logs)
	if line["level"] != "ERROR" || line["status"] != float64(500) || line["error_code"] != "internal" {
		t.Errorf("log %v", line)
	}
	if !strings.Contains(logs.String(), `"msg":"panic"`) {
		t.Error("panic stack not logged")
	}
}

func TestBytesInAndProxy(t *testing.T) {
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	h := withLogging(logger, true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("hello"))
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	h.ServeHTTP(httptest.NewRecorder(), req)

	line := lastRequestLog(t, logs)
	if line["bytes_in"] != float64(5) || line["remote"] != "203.0.113.9" || line["status"] != float64(200) {
		t.Errorf("log %v", line)
	}
}

func TestForwardedForIgnoredByDefault(t *testing.T) {
	s := newTestServer(t, "")
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	s.do(t, req)
	if line := lastRequestLog(t, s.logs); line["remote"] != req.RemoteAddr {
		t.Errorf("remote = %v, want %s", line["remote"], req.RemoteAddr)
	}
}

func TestLogInjection(t *testing.T) {
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	h := withLogging(logger, false, http.NotFoundHandler())
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet,
		"/x?path=a%0Atime=fake%20level=INFO%20msg=request", nil))
	if n := strings.Count(strings.TrimSpace(logs.String()), "\n"); n != 0 {
		t.Errorf("one request produced %d extra log lines:\n%s", n, logs)
	}
}
