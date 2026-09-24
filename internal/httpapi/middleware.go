package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

type ctxKey struct{}

// requestState lets handlers pass details to the logging middleware.
type requestState struct {
	id      string
	errCode string
	err     error
}

func stateFrom(ctx context.Context) *requestState {
	st, _ := ctx.Value(ctxKey{}).(*requestState)
	return st
}

// withLogging logs one line per request and recovers from panics. It must be
// the outermost handler so auth failures and panics are logged too.
func withLogging(logger *slog.Logger, trustProxy bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		st := &requestState{id: newRequestID()}
		rec := &responseRecorder{ResponseWriter: w}
		body := &countingBody{ReadCloser: r.Body}
		if r.Body != nil {
			r.Body = body
		}
		r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, st))
		rec.Header().Set("X-Request-ID", st.id)

		defer func() {
			p := recover()
			if p != nil {
				st.errCode, st.err = "internal", fmt.Errorf("panic: %v", p)
				logger.Error("panic", "req_id", st.id, "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
				if rec.status == 0 {
					writeJSON(rec, http.StatusInternalServerError, errorBody{Error: "internal server error", Code: "internal"})
				}
			}
			logRequest(r.Context(), logger, r, rec, body.n, st, time.Since(start), trustProxy)
			// net/http uses ErrAbortHandler to drop the connection; let it through.
			if p == http.ErrAbortHandler {
				panic(p)
			}
		}()
		next.ServeHTTP(rec, r)
	})
}

func logRequest(ctx context.Context, logger *slog.Logger, r *http.Request, rec *responseRecorder,
	bytesIn int64, st *requestState, elapsed time.Duration, trustProxy bool) {
	status := rec.status
	if status == 0 {
		// The handler wrote nothing; net/http sends 200.
		status = http.StatusOK
	}
	// Client-controlled values are only ever attributes, never part of the
	// message, so the handler escapes them and they cannot forge log lines.
	attrs := []slog.Attr{
		slog.String("req_id", st.id),
		slog.String("method", r.Method),
		slog.String("url_path", r.URL.Path),
	}
	if p := r.URL.Query().Get("path"); p != "" {
		attrs = append(attrs, slog.String("path", p))
	}
	attrs = append(attrs,
		slog.Int("status", status),
		slog.Int64("bytes_in", bytesIn),
		slog.Int64("bytes_out", rec.bytes),
		slog.Int64("duration_ms", elapsed.Milliseconds()),
		slog.String("remote", clientAddr(r, trustProxy)),
		slog.String("user_agent", r.UserAgent()),
	)
	if st.errCode != "" {
		attrs = append(attrs, slog.String("error_code", st.errCode))
	}
	if st.err != nil {
		attrs = append(attrs, slog.String("err", st.err.Error()))
	}
	logger.LogAttrs(ctx, levelFor(status), "request", attrs...)
}

func levelFor(status int) slog.Level {
	switch {
	case status >= 500:
		return slog.LevelError
	case status >= 400:
		return slog.LevelWarn
	}
	return slog.LevelInfo
}

// clientAddr ignores X-Forwarded-For unless told to trust it, since any
// client can set it.
func clientAddr(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			return strings.TrimSpace(first)
		}
	}
	return r.RemoteAddr
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func requireToken(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, r, errUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (rec *responseRecorder) WriteHeader(code int) {
	if rec.status == 0 {
		rec.status = code
	}
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *responseRecorder) Write(b []byte) (int, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	n, err := rec.ResponseWriter.Write(b)
	rec.bytes += int64(n)
	return n, err
}

// ReadFrom keeps the underlying writer's fast path (sendfile) for downloads,
// which wrapping would otherwise hide.
func (rec *responseRecorder) ReadFrom(src io.Reader) (int64, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	n, err := io.Copy(rec.ResponseWriter, src)
	rec.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer for
// flushing and per-request deadlines.
func (rec *responseRecorder) Unwrap() http.ResponseWriter { return rec.ResponseWriter }

type countingBody struct {
	io.ReadCloser
	n int64
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.n += int64(n)
	return n, err
}
