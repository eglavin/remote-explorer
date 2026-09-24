// Package fsvc implements the file operations behind the API.
//
// Every filesystem access goes through os.Root, so no path can resolve
// outside the served folder, including through symlinks. Callers must still
// validate paths with safepath.Clean first.
package fsvc

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"syscall"
	"time"

	"remote-explorer/internal/extfilter"
	"remote-explorer/internal/safepath"
)

var (
	ErrNotFound = errors.New("not found")
	ErrNotDir   = errors.New("not a directory")
	ErrIsDir    = errors.New("is a directory")
)

type Service struct {
	root    *os.Root
	visible extfilter.Set
	upload  extfilter.Set
}

// New returns a Service over root. Files whose names visible does not allow
// are treated as if they do not exist; upload limits which names can be written.
func New(root *os.Root, visible, upload extfilter.Set) *Service {
	return &Service{root: root, visible: visible, upload: upload}
}

type EntryType string

const (
	TypeFile EntryType = "file"
	TypeDir  EntryType = "dir"
)

type Entry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Type     EntryType `json:"type"`
	Size     *int64    `json:"size,omitempty"`
	Modified time.Time `json:"modified"`
}

type Listing struct {
	Path    string  `json:"path"`
	Parent  *string `json:"parent"`
	Entries []Entry `json:"entries"`
}

// List returns the contents of the directory at rel, folders first, then by name.
func (s *Service) List(rel string) (*Listing, error) {
	info, err := s.root.Stat(rel)
	if err != nil {
		return nil, s.dirErr(rel, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %s", ErrNotDir, rel)
	}
	dir, err := s.root.Open(rel)
	if err != nil {
		return nil, mapErr(err)
	}
	defer dir.Close()
	dirents, err := dir.ReadDir(-1)
	if err != nil {
		return nil, mapErr(err)
	}

	entries := make([]Entry, 0, len(dirents))
	for _, d := range dirents {
		if e, ok := s.entry(rel, d); ok {
			entries = append(entries, e)
		}
	}
	slices.SortFunc(entries, func(a, b Entry) int {
		if a.Type != b.Type {
			if a.Type == TypeDir {
				return -1
			}
			return 1
		}
		return cmp.Or(
			cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)),
			cmp.Compare(a.Name, b.Name),
		)
	})

	l := &Listing{Path: apiPath(rel), Entries: entries}
	if rel != "." {
		parent := apiPath(path.Dir(rel))
		l.Parent = &parent
	}
	return l, nil
}

// Open opens the regular file at rel for reading. The caller must close it.
// Files hidden by the extension filter are reported as not found, so their
// existence is not revealed.
func (s *Service) Open(rel string) (*os.File, fs.FileInfo, error) {
	// Stat before opening: opening a named pipe would block until a writer appears.
	info, err := s.root.Stat(rel)
	if err != nil {
		return nil, nil, mapErr(err)
	}
	if info.IsDir() {
		return nil, nil, fmt.Errorf("%w: %s", ErrIsDir, rel)
	}
	name := path.Base(rel)
	if !info.Mode().IsRegular() || !s.visible.Allows(name) || isTempName(name) {
		return nil, nil, fmt.Errorf("%w: %s", ErrNotFound, rel)
	}
	if err := s.checkRealName(rel); err != nil {
		return nil, nil, err
	}

	f, err := s.root.Open(rel)
	if err != nil {
		return nil, nil, mapErr(err)
	}
	// Re-check the opened file, which may have been swapped since the Stat.
	info, err = f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, fmt.Errorf("%w: %s", ErrNotFound, rel)
	}
	return f, info, nil
}

// entry converts a directory entry, reporting false for entries clients
// should not see: names they could not request, filtered extensions,
// in-progress uploads, symlinks that leave the root or are broken, and
// special files.
func (s *Service) entry(dir string, d fs.DirEntry) (Entry, bool) {
	name := d.Name()
	if safepath.ValidName(name) != nil || isTempName(name) {
		return Entry{}, false
	}
	rel := path.Join(dir, name)

	var info fs.FileInfo
	var err error
	if d.Type()&fs.ModeSymlink != 0 {
		// Resolving through the root fails for links pointing outside it.
		info, err = s.root.Stat(rel)
	} else {
		info, err = d.Info()
	}
	if err != nil {
		return Entry{}, false
	}

	switch {
	case info.IsDir():
		return Entry{Name: name, Path: rel, Type: TypeDir, Modified: info.ModTime().UTC()}, true
	case info.Mode().IsRegular() && s.visible.Allows(name):
		return fileEntry(rel, info), true
	}
	return Entry{}, false
}

// apiPath converts os.Root's "." for the root into the API's "".
func apiPath(rel string) string {
	if rel == "." {
		return ""
	}
	return rel
}

func mapErr(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	case errors.Is(err, syscall.ENOTDIR):
		// A path runs through a file, as in "song.mp3/x".
		return fmt.Errorf("%w: %w", ErrNotDir, err)
	case isEscape(err):
		return fmt.Errorf("%w: %w", safepath.ErrEscape, err)
	}
	return err
}

// dirErr maps an error from resolving or creating the directory rel. A path
// through a file ("song.mp3/x") is reported differently by each OS and Go
// version (ENOTDIR, not found, EEXIST from MkdirAll), so the path itself is
// checked to answer ErrNotDir consistently.
func (s *Service) dirErr(rel string, err error) error {
	for p := rel; p != "."; p = path.Dir(p) {
		info, statErr := s.root.Stat(p)
		if statErr != nil {
			continue
		}
		if !info.IsDir() {
			return fmt.Errorf("%w: %s is a file", ErrNotDir, p)
		}
		// The deepest existing element is a directory, so the parents are too.
		break
	}
	return mapErr(err)
}

// os.Root reports escapes (e.g. through a symlink) with an unexported error,
// so its message is the only way to recognise them.
func isEscape(err error) bool {
	return strings.Contains(err.Error(), "path escapes from parent")
}
