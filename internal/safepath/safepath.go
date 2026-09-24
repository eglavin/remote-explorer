// Package safepath validates client-supplied paths before they reach the filesystem.
//
// It is the first of two layers: every filesystem access also goes through
// os.Root, which blocks escapes (including via symlinks) at the OS level.
// This layer exists to reject hostile input early with a clear error.
package safepath

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

var (
	ErrInvalid = errors.New("invalid path")
	ErrEscape  = errors.New("path escapes root")
)

// Clean validates a forward-slash path relative to the served root and
// returns it in canonical form. The root itself is returned as ".".
func Clean(p string) (string, error) {
	if p == "" {
		return ".", nil
	}
	if strings.IndexByte(p, 0) >= 0 {
		return "", fmt.Errorf("%w: contains NUL byte", ErrInvalid)
	}
	if p[0] == '/' || p[0] == '\\' || hasVolumePrefix(p) {
		return "", fmt.Errorf("%w: %q is absolute", ErrEscape, p)
	}
	// Split on both separators so "..\x" is reported as a traversal attempt
	// rather than just a malformed path.
	for _, seg := range strings.FieldsFunc(p, isSeparator) {
		if seg == ".." {
			return "", fmt.Errorf("%w: %q contains ..", ErrEscape, p)
		}
	}
	if strings.ContainsRune(p, '\\') {
		return "", fmt.Errorf("%w: %q contains a backslash; use forward slashes", ErrInvalid, p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." {
			continue
		}
		if err := ValidName(seg); err != nil {
			return "", err
		}
	}
	cleaned := path.Clean(p)
	if !filepath.IsLocal(filepath.FromSlash(cleaned)) {
		return "", fmt.Errorf("%w: %q", ErrInvalid, p)
	}
	return cleaned, nil
}

// ValidName reports whether name is usable as a single path segment on this
// platform. It is used for path segments, upload file names, and to hide
// on-disk entries that clients could never address.
func ValidName(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("%w: %q is not a file name", ErrInvalid, name)
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("%w: %q contains a separator or NUL", ErrInvalid, name)
	}
	// On Windows this also rejects reserved device names such as CON and NUL.txt.
	if !filepath.IsLocal(name) {
		return fmt.Errorf("%w: %q is a reserved name", ErrInvalid, name)
	}
	return validPlatformName(name)
}

func isSeparator(r rune) bool { return r == '/' || r == '\\' }
