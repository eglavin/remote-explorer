// Package term decides whether output goes to a terminal and can use ANSI
// colour codes.
package term

import (
	"io"
	"os"
)

// IsTerminal reports whether w is a terminal rather than a pipe or file.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// Color reports whether w is a terminal that should get colour. It honours
// NO_COLOR (https://no-color.org) and TERM=dumb, and never colours pipes or
// files, where escape codes would end up as literal text.
func Color(w io.Writer) bool {
	if !IsTerminal(w) || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	return enableVT(w.(*os.File))
}
