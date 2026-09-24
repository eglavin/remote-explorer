package fsvc

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"remote-explorer/internal/extfilter"
	"remote-explorer/internal/safepath"
)

// newTree creates root/{b.mp3, A.txt, empty.mp3, zdir/, adir/c.mp3} and a sibling
// "outside" folder, and returns a Service over root.
func newTree(t *testing.T, visible extfilter.Set) (svc *Service, rootDir, outside string) {
	t.Helper()
	base := t.TempDir()
	rootDir = filepath.Join(base, "root")
	outside = filepath.Join(base, "outside")
	for _, d := range []string{rootDir, outside, filepath.Join(rootDir, "zdir"), filepath.Join(rootDir, "adir")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"b.mp3":                 "bbb",
		"A.txt":                 "a",
		"empty.mp3":             "",
		"adir/c.mp3":            "c",
		"../outside/secret.mp3": "secret",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(rootDir, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return New(root, visible, nil), rootDir, outside
}

func names(l *Listing) []string {
	var out []string
	for _, e := range l.Entries {
		out = append(out, e.Name)
	}
	return out
}

func TestListRoot(t *testing.T) {
	svc, _, _ := newTree(t, nil)
	l, err := svc.List(".")
	if err != nil {
		t.Fatal(err)
	}
	if l.Path != "" || l.Parent != nil {
		t.Errorf("root path=%q parent=%v, want empty and nil", l.Path, l.Parent)
	}
	want := []string{"adir", "zdir", "A.txt", "b.mp3", "empty.mp3"}
	if got := names(l); !slices.Equal(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
	for _, e := range l.Entries {
		switch e.Type {
		case TypeDir:
			if e.Size != nil {
				t.Errorf("%s: dir has size", e.Name)
			}
		case TypeFile:
			if e.Size == nil {
				t.Errorf("%s: file has no size", e.Name)
			}
		}
	}
	if e := l.Entries[4]; *e.Size != 0 {
		t.Errorf("empty.mp3 size = %d", *e.Size)
	}
}

func TestListSubdir(t *testing.T) {
	svc, _, _ := newTree(t, nil)
	l, err := svc.List("adir")
	if err != nil {
		t.Fatal(err)
	}
	if l.Path != "adir" || l.Parent == nil || *l.Parent != "" {
		t.Errorf("path=%q parent=%v", l.Path, l.Parent)
	}
	if len(l.Entries) != 1 || l.Entries[0].Path != "adir/c.mp3" {
		t.Errorf("entries = %+v", l.Entries)
	}
}

func TestListFiltersExtensions(t *testing.T) {
	svc, _, _ := newTree(t, extfilter.Set{"mp3"})
	l, err := svc.List(".")
	if err != nil {
		t.Fatal(err)
	}
	// Folders are always shown so matching files inside stay reachable.
	want := []string{"adir", "zdir", "b.mp3", "empty.mp3"}
	if got := names(l); !slices.Equal(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
}

func TestListErrors(t *testing.T) {
	svc, _, _ := newTree(t, nil)
	if _, err := svc.List("missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: got %v, want ErrNotFound", err)
	}
	if _, err := svc.List("A.txt"); !errors.Is(err, ErrNotDir) {
		t.Errorf("file: got %v, want ErrNotDir", err)
	}
	if _, err := svc.List("A.txt/x/y"); !errors.Is(err, ErrNotDir) {
		t.Errorf("path through file: got %v, want ErrNotDir", err)
	}
	if _, err := svc.List("adir/missing/x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing parent: got %v, want ErrNotFound", err)
	}
}

func TestOpen(t *testing.T) {
	svc, _, _ := newTree(t, extfilter.Set{"mp3"})
	f, info, err := svc.Open("adir/c.mp3")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if info.Name() != "c.mp3" || info.Size() != 1 {
		t.Errorf("info = %s %d", info.Name(), info.Size())
	}

	tests := []struct {
		rel  string
		want error
	}{
		{".", ErrIsDir},
		{"adir", ErrIsDir},
		{"missing.mp3", ErrNotFound},
		// Hidden by the filter: reported as missing so its existence is not revealed.
		{"A.txt", ErrNotFound},
	}
	for _, tt := range tests {
		if f, _, err := svc.Open(tt.rel); !errors.Is(err, tt.want) {
			if f != nil {
				f.Close()
			}
			t.Errorf("Open(%q) = %v, want %v", tt.rel, err, tt.want)
		}
	}
}

func TestOpenNameWithTilde(t *testing.T) {
	svc, rootDir, _ := newTree(t, nil)
	if err := os.WriteFile(filepath.Join(rootDir, "my~file.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, _, err := svc.Open("my~file.mp3")
	if err != nil {
		t.Fatalf("real name containing ~: %v", err)
	}
	f.Close()
}

func TestListSymlinks(t *testing.T) {
	svc, rootDir, outside := newTree(t, nil)
	if err := os.Symlink(outside, filepath.Join(rootDir, "escape")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	if err := os.Symlink("adir", filepath.Join(rootDir, "inside")); err != nil {
		t.Fatal(err)
	}
	// os.Root refuses absolute link targets even when they resolve inside the root.
	if err := os.Symlink(filepath.Join(rootDir, "adir"), filepath.Join(rootDir, "absinside")); err != nil {
		t.Fatal(err)
	}

	l, err := svc.List(".")
	if err != nil {
		t.Fatal(err)
	}
	got := names(l)
	if slices.Contains(got, "escape") {
		t.Error("symlink pointing outside the root is listed")
	}
	if slices.Contains(got, "absinside") {
		t.Error("symlink with an absolute target is listed")
	}
	if !slices.Contains(got, "inside") {
		t.Error("relative symlink inside the root is not listed")
	}
	if _, err := svc.List("escape"); !errors.Is(err, safepath.ErrEscape) {
		t.Errorf("listing through escaping symlink: got %v, want ErrEscape", err)
	}
	if _, _, err := svc.Open("escape/secret.mp3"); !errors.Is(err, safepath.ErrEscape) {
		t.Errorf("opening through escaping symlink: got %v, want ErrEscape", err)
	}
	if f, _, err := svc.Open("inside/c.mp3"); err != nil {
		t.Errorf("opening through relative symlink: %v", err)
	} else {
		f.Close()
	}
}
