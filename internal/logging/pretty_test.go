package logging

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func prettyLogger(color bool) (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(newPrettyHandler(&buf, slog.LevelInfo, color)), &buf
}

func requestAttrs(urlPath string, status int, bytesIn int64) []slog.Attr {
	return []slog.Attr{
		slog.String("req_id", "1664b022b5bca8f7"),
		slog.String("method", "GET"),
		slog.String("url_path", urlPath),
		slog.Int("status", status),
		slog.Int64("bytes_in", bytesIn),
		slog.Int64("bytes_out", 248),
		slog.Int64("duration_ms", 3),
		slog.String("remote", "127.0.0.1:54180"),
		slog.String("user_agent", "curl/8.17.0"),
	}
}

// line strips the timestamp, which depends on the clock.
func line(t *testing.T, buf *bytes.Buffer) string {
	t.Helper()
	out := buf.String()
	buf.Reset()
	if !strings.HasSuffix(out, "\n") || strings.Count(out, "\n") != 1 {
		t.Fatalf("want exactly one line, got %q", out)
	}
	_, rest, _ := strings.Cut(strings.TrimSuffix(out, "\n"), " ")
	return rest
}

func TestPrettyRequestLine(t *testing.T) {
	logger, buf := prettyLogger(false)
	logger.LogAttrs(t.Context(), slog.LevelInfo, "request", requestAttrs("/api/list", 200, 0)...)
	if got, want := line(t, buf), "INF GET /api/list 200 248 B 3ms 127.0.0.1:54180 req=1664b022b5bca8f7"; got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}

	attrs := append(requestAttrs("/api/upload", 403, 3<<20),
		slog.String("path", "../x"), slog.String("error_code", "path_escape"))
	logger.LogAttrs(t.Context(), slog.LevelWarn, "request", attrs...)
	want := "WRN GET /api/upload 403 248 B in=3.0 MiB 3ms 127.0.0.1:54180 req=1664b022b5bca8f7 path=../x error_code=path_escape"
	if got := line(t, buf); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestPrettyEscapesClientInput(t *testing.T) {
	logger, buf := prettyLogger(false)
	logger.LogAttrs(t.Context(), slog.LevelInfo, "request", requestAttrs("/a\n12:00:00 ERR forged\x1b[31m", 200, 0)...)
	got := line(t, buf)
	if !strings.Contains(got, `"/a\n12:00:00 ERR forged\x1b[31m"`) {
		t.Errorf("url_path not quoted: %q", got)
	}

	logger.Warn("http: TLS handshake error from 1.2.3.4: bogus \x1b[2J\r\ngreeting")
	if got := line(t, buf); got != `WRN "http: TLS handshake error from 1.2.3.4: bogus \x1b[2J\r\ngreeting"` {
		t.Errorf("message not escaped: %q", got)
	}
}

func TestPrettyGenericLine(t *testing.T) {
	logger, buf := prettyLogger(false)
	logger.With("component", "server").WithGroup("tls").Info("listening beyond localhost",
		"mode", "self-signed", "err", errors.New("a b"), "empty", "", slog.Group("g", "x", 1))
	want := `INF listening beyond localhost component=server tls.mode=self-signed tls.err="a b" tls.empty="" tls.g.x=1`
	if got := line(t, buf); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}

	logger.Debug("hidden")
	if buf.Len() != 0 {
		t.Errorf("debug line written at info level: %q", buf.String())
	}
}

func TestPrettyColor(t *testing.T) {
	logger, buf := prettyLogger(true)
	logger.LogAttrs(t.Context(), slog.LevelError, "request", requestAttrs("/x", 500, 0)...)
	got := buf.String()
	for _, want := range []string{"\x1b[1;31mERR\x1b[0m", "\x1b[1;31m500\x1b[0m", "\x1b[2mreq=1664b022b5bca8f7\x1b[0m"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q: %q", want, got)
		}
	}
}

func TestFormatBytesAndDuration(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KiB", 300000372: "286.1 MiB", 5 << 40: "5.0 TiB"} {
		if got := formatBytes(n); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", n, got, want)
		}
	}
	for d, want := range map[time.Duration]string{0: "0ms", 643 * time.Millisecond: "643ms", 12345 * time.Millisecond: "12.3s"} {
		if got := formatDuration(d); got != want {
			t.Errorf("formatDuration(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestNewHandlerAuto(t *testing.T) {
	var buf bytes.Buffer
	if _, ok := NewHandler(&buf, "auto", slog.LevelInfo).(*slog.TextHandler); !ok {
		t.Error("auto on a non-terminal should use the text handler")
	}
	if _, ok := NewHandler(&buf, "pretty", slog.LevelInfo).(*prettyHandler); !ok {
		t.Error("pretty should use the pretty handler")
	}
}
