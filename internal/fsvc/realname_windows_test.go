package fsvc

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"remote-explorer/internal/extfilter"
)

func shortName(t *testing.T, long string) string {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(long)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, syscall.MAX_PATH)
	n, err := syscall.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 {
		t.Skipf("GetShortPathName: %v", err)
	}
	return filepath.Base(syscall.UTF16ToString(buf[:n]))
}

func TestOpenRejectsShortNames(t *testing.T) {
	svc, rootDir, _ := newTree(t, extfilter.Set{"mp3"})
	long := filepath.Join(rootDir, "track.mp3x")
	if err := os.WriteFile(long, []byte("hidden"), 0o644); err != nil {
		t.Fatal(err)
	}
	short := shortName(t, long)
	if !strings.Contains(short, "~") {
		t.Skipf("8.3 short names are disabled on this volume (got %q)", short)
	}
	if !svc.visible.Allows(short) {
		t.Fatalf("test assumption broken: filter does not allow short name %q", short)
	}
	if f, _, err := svc.Open(short); !errors.Is(err, ErrNotFound) {
		if f != nil {
			f.Close()
		}
		t.Errorf("Open(%q) (short name of track.mp3x) = %v, want ErrNotFound", short, err)
	}

	up, err := svc.BeginUpload(".", UploadOptions{Overwrite: true})
	if err != nil {
		t.Fatal(err)
	}
	defer up.Abort()
	if err := up.Add(short, strings.NewReader("clobber")); !errors.Is(err, ErrExists) {
		t.Errorf("overwrite via short name %q = %v, want ErrExists", short, err)
	}
	if b, _ := os.ReadFile(long); string(b) != "hidden" {
		t.Errorf("track.mp3x was modified: %q", b)
	}
}
