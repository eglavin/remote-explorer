package httpapi

import (
	"bytes"
	"errors"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"remote-explorer/internal/extfilter"
	"remote-explorer/internal/fsvc"
)

type uploadFile struct {
	name, content string
}

func newWritableTestServer(t *testing.T, cfg testConfig) *testServer {
	t.Helper()
	cfg.info.Writable = true
	if cfg.info.MaxUpload == 0 {
		cfg.info.MaxUpload = 1 << 20
	}
	return newConfiguredTestServer(t, cfg)
}

func multipartBody(t *testing.T, files ...uploadFile) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	for _, f := range files {
		part, err := mw.CreateFormFile("file", f.name)
		if err != nil {
			t.Fatal(err)
		}
		part.Write([]byte(f.content))
	}
	mw.Close()
	return body, mw.FormDataContentType()
}

func (s *testServer) upload(t *testing.T, query string, files ...uploadFile) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := multipartBody(t, files...)
	req := httptest.NewRequest(http.MethodPost, "/api/upload"+query, body)
	req.Header.Set("Content-Type", contentType)
	return s.do(t, req)
}

func (s *testServer) read(t *testing.T, rel string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(s.dir, filepath.FromSlash(rel)))
	if errors.Is(err, fs.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b), true
}

func (s *testServer) tempFiles(t *testing.T) []string {
	t.Helper()
	var found []string
	_ = filepath.WalkDir(s.dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), ".upload-") {
			found = append(found, p)
		}
		return nil
	})
	return found
}

func TestUploadDisabledByDefault(t *testing.T) {
	s := newTestServer(t, "")
	rr := s.upload(t, "?path=", uploadFile{"x.mp3", "x"})
	if rr.Code != http.StatusNotFound {
		t.Errorf("read-only server: status %d", rr.Code)
	}
	if _, ok := s.read(t, "x.mp3"); ok {
		t.Error("file was written by a read-only server")
	}
}

func TestUpload(t *testing.T) {
	s := newWritableTestServer(t, testConfig{})
	rr := s.upload(t, "?path=sub", uploadFile{"one.mp3", "111"}, uploadFile{"two.txt", "22"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	resp := decode[struct{ Files []fsvc.Entry }](t, rr)
	if len(resp.Files) != 2 || resp.Files[0].Path != "sub/one.mp3" || *resp.Files[0].Size != 3 {
		t.Errorf("response = %+v", resp.Files)
	}
	if got, _ := s.read(t, "sub/one.mp3"); got != "111" {
		t.Errorf("sub/one.mp3 = %q", got)
	}
	if got, _ := s.read(t, "sub/two.txt"); got != "22" {
		t.Errorf("sub/two.txt = %q", got)
	}

	var uploads []map[string]any
	line := lastRequestLog(t, s.logs)
	for _, l := range logLines(t, s.logs) {
		if l["msg"] == "upload" {
			uploads = append(uploads, l)
		}
	}
	if len(uploads) != 2 || uploads[0]["file"] != "sub/one.mp3" || uploads[0]["req_id"] != line["req_id"] {
		t.Errorf("upload events %v, request %v", uploads, line)
	}
	if line["status"] != float64(201) || line["bytes_in"].(float64) == 0 {
		t.Errorf("request log %v", line)
	}
}

func TestUploadToRoot(t *testing.T) {
	s := newWritableTestServer(t, testConfig{})
	if rr := s.upload(t, "", uploadFile{"root.mp3", "r"}); rr.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	if _, ok := s.read(t, "root.mp3"); !ok {
		t.Error("root.mp3 not written")
	}
}

func TestUploadStripsClientPaths(t *testing.T) {
	s := newWritableTestServer(t, testConfig{})
	for _, name := range []string{"../../evil.mp3", `C:\Users\me\Music\win.mp3`, "/abs/unix.mp3"} {
		rr := s.upload(t, "?path=sub", uploadFile{name, "x"})
		if rr.Code != http.StatusCreated {
			t.Errorf("%q: status %d: %s", name, rr.Code, rr.Body)
		}
	}
	for _, want := range []string{"sub/evil.mp3", "sub/win.mp3", "sub/unix.mp3"} {
		if _, ok := s.read(t, want); !ok {
			t.Errorf("%s not written", want)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(s.dir), "evil.mp3")); err == nil {
		t.Error("file escaped the root")
	}
}

func TestUploadErrors(t *testing.T) {
	s := newWritableTestServer(t, testConfig{
		upload: extfilter.Set{"mp3"},
		info:   Info{MaxUpload: 1000},
	})
	big := strings.Repeat("x", 2000)
	tests := []struct {
		name   string
		query  string
		files  []uploadFile
		status int
		code   string
	}{
		{"escape", "?path=../x", []uploadFile{{"a.mp3", "x"}}, http.StatusForbidden, "path_escape"},
		{"missing dir", "?path=nope", []uploadFile{{"a.mp3", "x"}}, http.StatusNotFound, "not_found"},
		{"dir is file", "?path=a.mp3", []uploadFile{{"x.mp3", "x"}}, http.StatusBadRequest, "not_a_directory"},
		{"exists", "", []uploadFile{{"a.mp3", "new"}}, http.StatusConflict, "exists"},
		{"overwrite disabled", "?overwrite=true", []uploadFile{{"a.mp3", "new"}}, http.StatusForbidden, "overwrite_disabled"},
		{"bad bool", "?overwrite=maybe", []uploadFile{{"q.mp3", "x"}}, http.StatusBadRequest, "invalid_query"},
		{"extension", "", []uploadFile{{"virus.exe", "x"}}, http.StatusUnsupportedMediaType, "ext_not_allowed"},
		{"bad name", "", []uploadFile{{"..", "x"}}, http.StatusBadRequest, "invalid_filename"},
		{"temp name", "", []uploadFile{{".upload-x.tmp", "x"}}, http.StatusBadRequest, "invalid_filename"},
		{"duplicate", "", []uploadFile{{"d.mp3", "1"}, {"d.mp3", "2"}}, http.StatusBadRequest, "duplicate_name"},
		{"no files", "", nil, http.StatusBadRequest, "no_files"},
		{"too large", "", []uploadFile{{"big.mp3", big}}, http.StatusRequestEntityTooLarge, "too_large"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := s.upload(t, tt.query, tt.files...)
			body := decode[errorBody](t, rr)
			if rr.Code != tt.status || body.Code != tt.code {
				t.Errorf("%d %+v, want %d %s", rr.Code, body, tt.status, tt.code)
			}
			if tt.code == "ext_not_allowed" && !slices.Equal(body.Allowed, []string{"mp3"}) {
				t.Errorf("allowed = %v", body.Allowed)
			}
			if left := s.tempFiles(t); len(left) > 0 {
				t.Errorf("temp files left: %v", left)
			}
		})
	}
	if got, _ := s.read(t, "a.mp3"); got != "0123456789" {
		t.Errorf("a.mp3 changed to %q", got)
	}
	for _, name := range []string{"d.mp3", "big.mp3", "virus.exe", "q.mp3"} {
		if _, ok := s.read(t, name); ok {
			t.Errorf("%s was written by a failed upload", name)
		}
	}
}

func TestUploadAllOrNothing(t *testing.T) {
	s := newWritableTestServer(t, testConfig{upload: extfilter.Set{"mp3"}})
	rr := s.upload(t, "", uploadFile{"good.mp3", "ok"}, uploadFile{"bad.exe", "no"})
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status %d", rr.Code)
	}
	if _, ok := s.read(t, "good.mp3"); ok {
		t.Error("good.mp3 saved although the request failed")
	}
	if left := s.tempFiles(t); len(left) > 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestUploadOverwrite(t *testing.T) {
	s := newWritableTestServer(t, testConfig{info: Info{Overwrite: true}})
	if rr := s.upload(t, "", uploadFile{"a.mp3", "new"}); rr.Code != http.StatusConflict {
		t.Errorf("without overwrite=true: status %d", rr.Code)
	}
	if rr := s.upload(t, "?overwrite=true", uploadFile{"a.mp3", "new"}); rr.Code != http.StatusCreated {
		t.Fatalf("overwrite: status %d: %s", rr.Code, rr.Body)
	}
	if got, _ := s.read(t, "a.mp3"); got != "new" {
		t.Errorf("a.mp3 = %q", got)
	}
}

func TestUploadMkdirs(t *testing.T) {
	s := newWritableTestServer(t, testConfig{})
	if rr := s.upload(t, "?path=x/y&mkdirs=true", uploadFile{"deep.mp3", "d"}); rr.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	if _, ok := s.read(t, "x/y/deep.mp3"); !ok {
		t.Error("x/y/deep.mp3 not written")
	}
}

func TestUploadRejectsNonMultipart(t *testing.T) {
	s := newWritableTestServer(t, testConfig{})
	req := httptest.NewRequest(http.MethodPost, "/api/upload", strings.NewReader("raw bytes"))
	req.Header.Set("Content-Type", "application/octet-stream")
	rr := s.do(t, req)
	if rr.Code != http.StatusBadRequest || decode[errorBody](t, rr).Code != "invalid_upload" {
		t.Errorf("status %d: %s", rr.Code, rr.Body)
	}
}

func TestUploadRequiresToken(t *testing.T) {
	const token = "upload-token-123"
	s := newWritableTestServer(t, testConfig{token: token})
	if rr := s.upload(t, "", uploadFile{"t.mp3", "x"}); rr.Code != http.StatusUnauthorized {
		t.Errorf("no token: status %d", rr.Code)
	}
	body, contentType := multipartBody(t, uploadFile{"t.mp3", "x"})
	req := httptest.NewRequest(http.MethodPost, "/api/upload", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+token)
	if rr := s.do(t, req); rr.Code != http.StatusCreated {
		t.Errorf("with token: status %d: %s", rr.Code, rr.Body)
	}
}
