package fsvc

import (
	"fmt"
	"strings"
)

// checkRealName rejects Windows 8.3 short names. Windows resolves them, so
// "TRACK~1.MP3" can open "track.mp3x" and slip past the extension filter.
// Short names always contain '~', so only those names pay for the directory
// scan that confirms an entry with exactly that long name exists.
func (s *Service) checkRealName(rel string) error {
	dirPath, name := splitRaw(rel)
	if !strings.Contains(name, "~") {
		return nil
	}
	dir, err := s.root.Open(dirPath)
	if err != nil {
		return mapErr(err)
	}
	defer dir.Close()
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return mapErr(err)
	}
	for _, n := range names {
		// Windows file names are case-insensitive, so "SONG.MP3" is the same file as "song.mp3".
		if strings.EqualFold(n, name) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s is not a real file name", ErrNotFound, rel)
}
