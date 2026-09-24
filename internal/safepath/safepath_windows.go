package safepath

import (
	"fmt"
	"strings"
)

func hasVolumePrefix(p string) bool {
	return len(p) >= 2 && p[1] == ':' && isASCIILetter(p[0])
}

func validPlatformName(name string) error {
	for _, r := range name {
		if r < 32 {
			return fmt.Errorf("%w: %q contains a control character", ErrInvalid, name)
		}
	}
	// ':' selects an alternate data stream (file.txt:hidden) or a drive.
	if strings.ContainsAny(name, `:<>"|?*`) {
		return fmt.Errorf("%w: %q contains a character Windows does not allow", ErrInvalid, name)
	}
	// Windows silently strips trailing dots and spaces, so "a.txt." would
	// alias "a.txt" and sidestep name-based checks.
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return fmt.Errorf("%w: %q ends in a dot or space", ErrInvalid, name)
	}
	if isReservedDeviceName(name) {
		return fmt.Errorf("%w: %q is a reserved device name", ErrInvalid, name)
	}
	return nil
}

// filepath.IsLocal follows Windows 11, where "NUL.txt" is an ordinary file,
// but older Windows versions still open the NUL device for it.
func isReservedDeviceName(name string) bool {
	base, _, _ := strings.Cut(name, ".")
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if len(base) >= 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		switch base[3:] {
		case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³":
			return true
		}
	}
	return false
}

func isASCIILetter(b byte) bool {
	return ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z')
}
