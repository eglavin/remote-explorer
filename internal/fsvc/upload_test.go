package fsvc

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"remote-explorer/internal/extfilter"
)

func newUploadTree(t *testing.T, upload extfilter.Set) (*Service, string) {
	t.Helper()
	svc, rootDir, _ := newTree(t, nil)
	svc.upload = upload
	return svc, rootDir
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// tempFiles returns leftover upload temp files anywhere under dir.
func tempFiles(t *testing.T, dir string) []string {
	t.Helper()
	var found []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && isTempName(d.Name()) {
			found = append(found, p)
		}
		return nil
	})
	return found
}

func TestUploadCommit(t *testing.T) {
	svc, rootDir := newUploadTree(t, nil)
	up, err := svc.BeginUpload("adir", UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer up.Abort()
	if err := up.Add("new.mp3", strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	if err := up.Add("second.txt", strings.NewReader("hi")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rootDir, "adir", "new.mp3")); !errors.Is(err, os.ErrNotExist) {
		t.Error("file is visible before Commit")
	}

	saved, err := up.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 || saved[0].Path != "adir/new.mp3" || *saved[0].Size != 5 || saved[1].Name != "second.txt" {
		t.Errorf("saved = %+v", saved)
	}
	if got := readFile(t, filepath.Join(rootDir, "adir", "new.mp3")); got != "hello" {
		t.Errorf("content = %q", got)
	}
	if left := tempFiles(t, rootDir); len(left) > 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestUploadAllOrNothing(t *testing.T) {
	svc, rootDir := newUploadTree(t, extfilter.Set{"mp3"})
	up, err := svc.BeginUpload(".", UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := up.Add("good.mp3", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if len(tempFiles(t, rootDir)) != 1 {
		t.Fatal("expected one temp file while the upload is in progress")
	}
	err = up.Add("bad.exe", strings.NewReader("x"))
	var extErr *ExtNotAllowedError
	if !errors.As(err, &extErr) || !errors.Is(err, ErrExtNotAllowed) || !slices.Equal(extErr.Allowed, []string{"mp3"}) {
		t.Fatalf("bad.exe: got %v", err)
	}
	up.Abort()

	if _, err := os.Stat(filepath.Join(rootDir, "good.mp3")); !errors.Is(err, os.ErrNotExist) {
		t.Error("good.mp3 was saved although the upload failed")
	}
	if left := tempFiles(t, rootDir); len(left) > 0 {
		t.Errorf("temp files left after Abort: %v", left)
	}
}

func TestUploadConflicts(t *testing.T) {
	svc, rootDir := newUploadTree(t, nil)

	up, _ := svc.BeginUpload(".", UploadOptions{})
	if err := up.Add("b.mp3", strings.NewReader("new")); !errors.Is(err, ErrExists) {
		t.Errorf("existing file without overwrite: got %v", err)
	}
	if err := up.Add("B.MP3", strings.NewReader("new")); err == nil && runtimeCaseInsensitive(t, rootDir) {
		t.Error("existing file under different case was not detected")
	}
	up.Abort()

	up, _ = svc.BeginUpload(".", UploadOptions{Overwrite: true})
	if err := up.Add("adir", strings.NewReader("x")); !errors.Is(err, ErrExists) {
		t.Errorf("directory with overwrite: got %v", err)
	}
	if err := up.Add("b.mp3", strings.NewReader("new")); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if _, err := up.Commit(); err != nil {
		t.Fatal(err)
	}
	up.Abort()
	if got := readFile(t, filepath.Join(rootDir, "b.mp3")); got != "new" {
		t.Errorf("after overwrite content = %q", got)
	}
}

// runtimeCaseInsensitive reports whether rootDir's filesystem ignores case.
func runtimeCaseInsensitive(t *testing.T, rootDir string) bool {
	_, err := os.Stat(filepath.Join(rootDir, "B.MP3"))
	return err == nil
}

func TestUploadConflictAppearsDuringUpload(t *testing.T) {
	svc, rootDir := newUploadTree(t, nil)
	up, _ := svc.BeginUpload(".", UploadOptions{})
	defer up.Abort()
	if err := up.Add("race.mp3", strings.NewReader("mine")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootDir, "race.mp3"), []byte("theirs"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := up.Commit(); !errors.Is(err, ErrExists) {
		t.Errorf("Commit = %v, want ErrExists", err)
	}
	if got := readFile(t, filepath.Join(rootDir, "race.mp3")); got != "theirs" {
		t.Errorf("existing file was replaced: %q", got)
	}
}

func TestUploadNames(t *testing.T) {
	svc, _ := newUploadTree(t, nil)
	up, _ := svc.BeginUpload(".", UploadOptions{})
	defer up.Abort()
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, ".upload-abc.tmp"} {
		if err := up.Add(name, strings.NewReader("x")); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Add(%q) = %v, want ErrInvalidName", name, err)
		}
	}
	if err := up.Add("dup.txt", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if err := up.Add("DUP.txt", strings.NewReader("x")); !errors.Is(err, ErrDuplicateName) {
		t.Errorf("repeated name: got %v", err)
	}
}

func TestBeginUploadDirs(t *testing.T) {
	svc, rootDir := newUploadTree(t, nil)
	if _, err := svc.BeginUpload("new/deeper", UploadOptions{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing dir: got %v", err)
	}
	if _, err := svc.BeginUpload("b.mp3", UploadOptions{}); !errors.Is(err, ErrNotDir) {
		t.Errorf("file as dir: got %v", err)
	}
	if _, err := svc.BeginUpload("b.mp3/sub", UploadOptions{MakeDirs: true}); !errors.Is(err, ErrNotDir) {
		t.Errorf("mkdirs through a file: got %v", err)
	}
	if _, err := svc.BeginUpload("new/deeper", UploadOptions{MakeDirs: true}); err != nil {
		t.Fatalf("mkdirs: %v", err)
	}
	if info, err := os.Stat(filepath.Join(rootDir, "new", "deeper")); err != nil || !info.IsDir() {
		t.Errorf("directory not created: %v", err)
	}
}

func TestTempFilesHidden(t *testing.T) {
	svc, rootDir := newUploadTree(t, nil)
	tmp := ".upload-ABCDEFGH.tmp"
	if err := os.WriteFile(filepath.Join(rootDir, tmp), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := svc.List(".")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(names(l), tmp) {
		t.Error("temp upload file is listed")
	}
	if _, _, err := svc.Open(tmp); !errors.Is(err, ErrNotFound) {
		t.Errorf("Open temp file: got %v", err)
	}
}
