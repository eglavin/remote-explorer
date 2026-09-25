// Package logging builds the application's slog.Logger from the logging flags.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"remote-explorer/internal/term"
)

// New returns a logger writing to stderr, or appending to file when it is set.
// The returned close function must be called on shutdown.
func New(format string, level slog.Level, file string) (*slog.Logger, func() error, error) {
	var w io.Writer = os.Stderr
	closeFn := func() error { return nil }
	if file != "" {
		f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			return nil, nil, fmt.Errorf("open log file: %w", err)
		}
		w, closeFn = f, f.Close
	}
	return slog.New(NewHandler(w, format, level)), closeFn, nil
}

// NewHandler returns a handler for format: "pretty", "text", "json", or
// "auto", which picks pretty for a terminal and text for pipes and files,
// where logs are more likely to be read by tools.
func NewHandler(w io.Writer, format string, level slog.Level) slog.Handler {
	if format == "auto" {
		format = "text"
		if term.IsTerminal(w) {
			format = "pretty"
		}
	}
	opts := &slog.HandlerOptions{Level: level}
	switch format {
	case "json":
		return slog.NewJSONHandler(w, opts)
	case "pretty":
		return newPrettyHandler(w, level, term.Color(w))
	}
	return slog.NewTextHandler(w, opts)
}
