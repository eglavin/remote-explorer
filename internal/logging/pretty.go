package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	ansiDim     = "2"
	ansiBold    = "1"
	ansiRed     = "1;31"
	ansiGreen   = "32"
	ansiYellow  = "33"
	ansiMagenta = "35"
	ansiCyan    = "36"
)

// prettyHandler writes compact lines meant for a person watching a terminal:
//
//	22:03:27 INF GET /api/list 200 248 B 0ms 127.0.0.1:54180 req=1664b022b5bca8f7
//	22:03:26 WRN listening beyond localhost
//
// Request lines get their own layout because they are most of the output.
// Everything else is the message followed by key=value pairs.
type prettyHandler struct {
	mu    *sync.Mutex
	w     io.Writer
	level slog.Leveler
	color bool
	// attrs come from WithAttrs, with group prefixes already applied.
	attrs  []slog.Attr
	prefix string
}

func newPrettyHandler(w io.Writer, level slog.Leveler, color bool) *prettyHandler {
	return &prettyHandler{mu: &sync.Mutex{}, w: w, level: level, color: color}
}

func (h *prettyHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level.Level()
}

func (h *prettyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h2 := *h
	h2.attrs = flatten(slices.Clone(h.attrs), h.prefix, attrs)
	return &h2
}

func (h *prettyHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	h2 := *h
	h2.prefix = h.prefix + name + "."
	return &h2
}

func (h *prettyHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := slices.Clone(h.attrs)
	r.Attrs(func(a slog.Attr) bool {
		attrs = flatten(attrs, h.prefix, []slog.Attr{a})
		return true
	})

	var b strings.Builder
	if !r.Time.IsZero() {
		b.WriteString(h.paint(ansiDim, r.Time.Format("15:04:05")))
		b.WriteByte(' ')
	}
	b.WriteString(h.levelLabel(r.Level))
	b.WriteByte(' ')
	if r.Message == "request" && h.prefix == "" {
		attrs = h.writeRequest(&b, attrs)
	} else {
		b.WriteString(escapeMessage(r.Message))
	}
	for _, a := range attrs {
		b.WriteByte(' ')
		b.WriteString(h.paint(ansiDim, a.Key+"="))
		b.WriteString(quoteValue(valueString(a.Value)))
	}
	b.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	return err
}

// writeRequest lays out the fields every request line has and returns the
// attributes left over for the generic key=value tail.
func (h *prettyHandler) writeRequest(b *strings.Builder, attrs []slog.Attr) []slog.Attr {
	take := func(key string) (slog.Value, bool) {
		i := slices.IndexFunc(attrs, func(a slog.Attr) bool { return a.Key == key })
		if i < 0 {
			return slog.Value{}, false
		}
		v := attrs[i].Value
		attrs = slices.Delete(attrs, i, i+1)
		return v, true
	}
	var fields []string
	if v, ok := take("method"); ok {
		fields = append(fields, h.paint(ansiBold, quoteValue(v.String())))
	}
	if v, ok := take("url_path"); ok {
		fields = append(fields, quoteValue(v.String()))
	}
	if v, ok := take("status"); ok {
		fields = append(fields, h.paint(statusColor(v.Int64()), v.String()))
	}
	if v, ok := take("bytes_out"); ok {
		fields = append(fields, formatBytes(v.Int64()))
	}
	// Only uploads send a body worth mentioning.
	if v, ok := take("bytes_in"); ok && v.Int64() > 0 {
		fields = append(fields, h.paint(ansiDim, "in=")+formatBytes(v.Int64()))
	}
	if v, ok := take("duration_ms"); ok {
		fields = append(fields, formatDuration(time.Duration(v.Int64())*time.Millisecond))
	}
	if v, ok := take("remote"); ok {
		fields = append(fields, h.paint(ansiDim, quoteValue(v.String())))
	}
	if v, ok := take("req_id"); ok {
		fields = append(fields, h.paint(ansiDim, "req="+quoteValue(v.String())))
	}
	// Too long for a line read at a glance; text and json keep it.
	take("user_agent")
	b.WriteString(strings.Join(fields, " "))
	return attrs
}

func (h *prettyHandler) levelLabel(l slog.Level) string {
	switch l {
	case slog.LevelDebug:
		return h.paint(ansiMagenta, "DBG")
	case slog.LevelInfo:
		return h.paint(ansiCyan, "INF")
	case slog.LevelWarn:
		return h.paint(ansiYellow, "WRN")
	case slog.LevelError:
		return h.paint(ansiRed, "ERR")
	}
	return l.String()
}

func (h *prettyHandler) paint(code, text string) string {
	if !h.color {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func statusColor(status int64) string {
	switch {
	case status >= 500:
		return ansiRed
	case status >= 400:
		return ansiYellow
	case status >= 300:
		return ansiCyan
	}
	return ansiGreen
}

func flatten(dst []slog.Attr, prefix string, attrs []slog.Attr) []slog.Attr {
	for _, a := range attrs {
		a.Value = a.Value.Resolve()
		if a.Equal(slog.Attr{}) {
			continue
		}
		if a.Value.Kind() == slog.KindGroup {
			groupPrefix := prefix
			if a.Key != "" {
				groupPrefix += a.Key + "."
			}
			dst = flatten(dst, groupPrefix, a.Value.Group())
			continue
		}
		a.Key = prefix + a.Key
		dst = append(dst, a)
	}
	return dst
}

func valueString(v slog.Value) string {
	if v.Kind() == slog.KindTime {
		return v.Time().Format(time.RFC3339Nano)
	}
	return v.String()
}

// quoteValue quotes values that would otherwise be ambiguous or could
// forge output: client-supplied paths and headers may contain spaces,
// newlines or escape codes of their own.
func quoteValue(s string) string {
	if s == "" {
		return `""`
	}
	if !utf8.ValidString(s) || strings.ContainsFunc(s, func(r rune) bool {
		return r == ' ' || r == '=' || r == '"' || !unicode.IsPrint(r)
	}) {
		return strconv.Quote(s)
	}
	return s
}

// escapeMessage leaves ordinary sentences alone but quotes messages with
// control characters. net/http's error log puts client input in messages.
func escapeMessage(s string) string {
	if !utf8.ValidString(s) || strings.ContainsFunc(s, func(r rune) bool { return r != ' ' && !unicode.IsPrint(r) }) {
		return strconv.Quote(s)
	}
	return s
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
