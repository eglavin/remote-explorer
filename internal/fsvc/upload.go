package fsvc

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"remote-explorer/internal/safepath"
)

var (
	ErrExists        = errors.New("already exists")
	ErrInvalidName   = errors.New("invalid file name")
	ErrDuplicateName = errors.New("file name repeated in upload")
	ErrExtNotAllowed = errors.New("extension not allowed")
)

// ExtNotAllowedError reports a file whose extension is not in the upload list.
type ExtNotAllowedError struct {
	Name    string
	Allowed []string
}

func (e *ExtNotAllowedError) Error() string {
	return fmt.Sprintf("%s: extension not allowed", e.Name)
}

func (e *ExtNotAllowedError) Is(target error) bool { return target == ErrExtNotAllowed }

// Temporary upload files live in the destination folder, or its deepest
// existing parent until mkdirs creates it, so the final move stays on one
// filesystem. They are hidden from listings and downloads.
const (
	tempPrefix = ".upload-"
	tempSuffix = ".tmp"
)

func isTempName(name string) bool {
	return strings.HasPrefix(name, tempPrefix) && strings.HasSuffix(name, tempSuffix)
}

type UploadOptions struct {
	Overwrite bool
	MakeDirs  bool
}

// Upload collects the files of one request. Files are written to temporary
// names by Add and only moved into place by Commit, so a request that fails
// part-way leaves nothing behind. Always call Abort, typically deferred; it
// is a no-op after a successful Commit.
type Upload struct {
	svc *Service
	dir string
	// tmpDir holds the temporary files: dir itself, or with MakeDirs its
	// deepest existing parent, so a failed request creates no folders.
	tmpDir  string
	opts    UploadOptions
	names   map[string]bool
	pending []pendingFile
}

type pendingFile struct {
	tmp, dest string
	committed bool
}

// BeginUpload starts an upload into the directory dir.
func (s *Service) BeginUpload(dir string, opts UploadOptions) (*Upload, error) {
	tmpDir := dir
	for {
		info, err := s.root.Stat(tmpDir)
		switch {
		case err == nil && info.IsDir():
			return &Upload{svc: s, dir: dir, tmpDir: tmpDir, opts: opts, names: map[string]bool{}}, nil
		case err == nil:
			return nil, s.notDirErr(tmpDir)
		case !opts.MakeDirs || !errors.Is(err, fs.ErrNotExist) || tmpDir == ".":
			return nil, s.dirErr(dir, err)
		}
		tmpDir = path.Dir(tmpDir)
	}
}

// Add validates name and streams src into a temporary file.
func (u *Upload) Add(name string, src io.Reader) error {
	if err := safepath.ValidName(name); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidName, err)
	}
	if isTempName(name) {
		return fmt.Errorf("%w: %q is reserved for temporary files", ErrInvalidName, name)
	}
	if !u.svc.upload.Allows(name) {
		return &ExtNotAllowedError{Name: name, Allowed: u.svc.upload.List()}
	}
	// Lowercase so "A.mp3" and "a.mp3" collide on case-insensitive filesystems too.
	key := strings.ToLower(name)
	if u.names[key] {
		return fmt.Errorf("%w: %s", ErrDuplicateName, name)
	}
	u.names[key] = true

	dest := path.Join(u.dir, name)
	if err := u.checkDest(dest); err != nil {
		return err
	}

	tmp := path.Join(u.tmpDir, tempPrefix+rand.Text()[:16]+tempSuffix)
	f, err := u.svc.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return mapErr(err)
	}
	u.pending = append(u.pending, pendingFile{tmp: tmp, dest: dest})
	_, err = io.Copy(f, src)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// checkDest reports whether a file may be written to dest.
func (u *Upload) checkDest(dest string) error {
	info, err := u.svc.root.Lstat(dest)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return mapErr(err)
	case info.IsDir():
		return fmt.Errorf("%w: %s is a directory", ErrExists, dest)
	case !u.opts.Overwrite:
		return fmt.Errorf("%w: %s", ErrExists, dest)
	case !info.Mode().IsRegular():
		return fmt.Errorf("%w: %s is not a regular file", ErrExists, dest)
	}
	// Overwriting through a Windows short name would replace a different
	// file, possibly one the extension filters hide.
	if err := u.svc.checkRealName(dest); err != nil {
		return fmt.Errorf("%w: %s refers to a different file", ErrExists, dest)
	}
	return nil
}

func (u *Upload) Count() int { return len(u.pending) }

// Commit moves every file into place and returns their entries.
func (u *Upload) Commit() ([]Entry, error) {
	// Re-check every destination before moving any, since files may have
	// appeared while the upload streamed. A file created in the moment
	// between this check and the rename can still be replaced.
	for _, p := range u.pending {
		if err := u.checkDest(p.dest); err != nil {
			return nil, err
		}
	}
	if u.dir != u.tmpDir {
		if err := u.svc.root.MkdirAll(u.dir, 0o755); err != nil {
			return nil, u.svc.dirErr(u.dir, err)
		}
	}
	saved := make([]Entry, 0, len(u.pending))
	for i := range u.pending {
		p := &u.pending[i]
		if err := u.place(p); err != nil {
			return nil, err
		}
		info, err := u.svc.root.Stat(p.dest)
		if err != nil {
			return nil, mapErr(err)
		}
		saved = append(saved, fileEntry(p.dest, info))
	}
	return saved, nil
}

// place moves one file into its destination. Without Overwrite it links
// rather than renames, because a link fails if the destination appeared
// since checkDest, where a rename would silently replace it.
func (u *Upload) place(p *pendingFile) error {
	if !u.opts.Overwrite {
		err := u.svc.root.Link(p.tmp, p.dest)
		if err == nil {
			p.committed = true
			_ = u.svc.root.Remove(p.tmp)
			return nil
		}
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%w: %s", ErrExists, p.dest)
		}
		// Some filesystems, such as FAT, have no hard links; rename instead.
	}
	if err := u.svc.root.Rename(p.tmp, p.dest); err != nil {
		return mapErr(err)
	}
	p.committed = true
	return nil
}

// Abort removes any temporary files that were not committed.
func (u *Upload) Abort() {
	for _, p := range u.pending {
		if !p.committed {
			_ = u.svc.root.Remove(p.tmp)
		}
	}
}

func fileEntry(rel string, info fs.FileInfo) Entry {
	size := info.Size()
	return Entry{
		Name:     path.Base(rel),
		Path:     rel,
		Type:     TypeFile,
		Size:     &size,
		Modified: info.ModTime().UTC(),
	}
}
