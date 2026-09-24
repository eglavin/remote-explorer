// Package extfilter matches file names against a list of allowed extensions.
package extfilter

import (
	"fmt"
	"slices"
	"strings"
)

// Set is a sorted list of lowercase extensions without the leading dot.
// An empty Set allows every file.
type Set []string

// Parse turns a comma-separated list such as "zip, .MP4,tar.gz" into a Set.
// An empty string yields an empty Set.
func Parse(s string) (Set, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var out Set
	for _, raw := range strings.Split(s, ",") {
		ext := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(raw), "."))
		if ext == "" {
			return nil, fmt.Errorf("empty extension in %q", s)
		}
		if strings.ContainsAny(ext, "/\\:\x00") || strings.HasPrefix(ext, ".") ||
			strings.HasSuffix(ext, ".") || strings.Contains(ext, "..") {
			return nil, fmt.Errorf("invalid extension %q", strings.TrimSpace(raw))
		}
		if !slices.Contains(out, ext) {
			out = append(out, ext)
		}
	}
	slices.Sort(out)
	return out, nil
}

// Allows reports whether name ends in one of the extensions, ignoring case.
func (s Set) Allows(name string) bool {
	if len(s) == 0 {
		return true
	}
	lower := strings.ToLower(name)
	for _, ext := range s {
		suffix := "." + ext
		// A name that is only the suffix (".zip") is a dotfile with no real name, not a zip.
		if len(lower) > len(suffix) && strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// Outside returns the extensions in s that other does not allow.
func (s Set) Outside(other Set) []string {
	if len(other) == 0 {
		return nil
	}
	var missing []string
	for _, ext := range s {
		if !slices.Contains(other, ext) {
			missing = append(missing, ext)
		}
	}
	return missing
}

// List returns the extensions as a non-nil slice, so it encodes as [] rather than null.
func (s Set) List() []string {
	if s == nil {
		return []string{}
	}
	return slices.Clone(s)
}

func (s Set) String() string {
	if len(s) == 0 {
		return "all"
	}
	return strings.Join(s, ",")
}
