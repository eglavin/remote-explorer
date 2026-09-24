package fsvc

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"remote-explorer/internal/extfilter"
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
	if _, err := svc.List("escape"); !errors.Is(err, ErrNotFound) {
		t.Errorf("listing through escaping symlink: got %v, want ErrNotFound", err)
	}
	if _, _, err := svc.Open("escape/secret.mp3"); !errors.Is(err, ErrNotFound) {
		t.Errorf("opening through escaping symlink: got %v, want ErrNotFound", err)
	}
	if f, _, err := svc.Open("inside/c.mp3"); err != nil {
		t.Errorf("opening through relative symlink: %v", err)
	} else {
		f.Close()
	}
}

func TestSymlinksCannotBypassFilter(t *testing.T) {
	svc, rootDir, _ := newTree(t, extfilter.Set{"mp3"})
	links := map[string]string{
		"leak.mp3":      "A.txt",
		"alias.mp3":     "b.mp3",
		"chain.mp3":     "hop.mp3",
		"hop.mp3":       "A.txt",
		"adir/up.mp3":   "../A.txt",
		"adir/ok.mp3":   "../b.mp3",
		"zdir/deep.mp3": "../adir/up.mp3",
		"hidden.txt":    "b.mp3",
	}
	for link, target := range links {
		if err := os.Symlink(filepath.FromSlash(target), filepath.Join(rootDir, filepath.FromSlash(link))); err != nil {
			t.Skipf("cannot create symlinks here: %v", err)
		}
	}

	for rel, want := range map[string]bool{
		"leak.mp3":      false,
		"chain.mp3":     false,
		"adir/up.mp3":   false,
		"zdir/deep.mp3": false,
		"hidden.txt":    false,
		"alias.mp3":     true,
		"adir/ok.mp3":   true,
	} {
		f, _, err := svc.Open(rel)
		if f != nil {
			f.Close()
		}
		if want && err != nil {
			t.Errorf("Open(%q) = %v, want the link to be served", rel, err)
		}
		if !want && !errors.Is(err, ErrNotFound) {
			t.Errorf("Open(%q) = %v, want ErrNotFound", rel, err)
		}

		dir, name := path.Split(rel)
		l, err := svc.List(path.Clean(dir))
		if err != nil {
			t.Fatal(err)
		}
		if got := slices.Contains(names(l), name); got != want {
			t.Errorf("%q listed = %v, want %v", rel, got, want)
		}
	}
}

func TestSymlinkToTempFileHidden(t *testing.T) {
	svc, rootDir, _ := newTree(t, nil)
	tmp := tempPrefix + "0123456789abcdef" + tempSuffix
	if err := os.WriteFile(filepath.Join(rootDir, tmp), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tmp, filepath.Join(rootDir, "partial.mp3")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	if _, _, err := svc.Open("partial.mp3"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Open through a link to an upload temp file = %v, want ErrNotFound", err)
	}
}

// Every way of naming a filtered file must fail exactly like a missing one.
func TestHiddenFilesLookMissing(t *testing.T) {
	svc, rootDir, outside := newTree(t, extfilter.Set{"mp3"})
	svc.upload = extfilter.Set{"mp3"}
	hasLinks := os.Symlink(outside, filepath.Join(rootDir, "escape")) == nil
	if hasLinks {
		if err := os.Symlink("A.txt", filepath.Join(rootDir, "leak.mp3")); err != nil {
			t.Fatal(err)
		}
	}

	hidden := []string{"A.txt", "A.txt/x", "A.txt/x/y", "missing", "missing/x"}
	if hasLinks {
		hidden = append(hidden, "escape", "escape/secret.mp3", "leak.mp3", "leak.mp3/x")
	}
	for _, rel := range hidden {
		if _, err := svc.List(rel); !errors.Is(err, ErrNotFound) {
			t.Errorf("List(%q) = %v, want ErrNotFound", rel, err)
		}
		if f, _, err := svc.Open(rel); !errors.Is(err, ErrNotFound) {
			if f != nil {
				f.Close()
			}
			t.Errorf("Open(%q) = %v, want ErrNotFound", rel, err)
		}
		for _, mkdirs := range []bool{false, true} {
			if mkdirs && strings.HasPrefix(rel, "missing") {
				continue // creating it is the point of mkdirs
			}
			if _, err := svc.BeginUpload(rel, UploadOptions{MakeDirs: mkdirs}); !errors.Is(err, ErrNotFound) {
				t.Errorf("BeginUpload(%q, mkdirs=%v) = %v, want ErrNotFound", rel, mkdirs, err)
			}
		}
	}

	// A visible file is still reported as one.
	for _, rel := range []string{"b.mp3", "b.mp3/x"} {
		if _, err := svc.List(rel); !errors.Is(err, ErrNotDir) {
			t.Errorf("List(%q) = %v, want ErrNotDir", rel, err)
		}
	}
}
