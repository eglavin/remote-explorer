package httpapi

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"remote-explorer/internal/fsvc"
	"remote-explorer/internal/safepath"
)

// upload handles POST /api/upload?path=<dir>[&overwrite=true][&mkdirs=true]
// with a multipart/form-data body. Every part that has a filename is saved
// into dir; the request succeeds or fails as a whole.
func (a *api) upload(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dir, err := safepath.Clean(q.Get("path"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	overwrite, err := boolParam(q, "overwrite")
	if err != nil {
		writeError(w, r, err)
		return
	}
	mkdirs, err := boolParam(q, "mkdirs")
	if err != nil {
		writeError(w, r, err)
		return
	}
	if overwrite && !a.info.Overwrite {
		writeError(w, r, errOverwriteDisabled)
		return
	}

	rc := http.NewResponseController(w)
	r.Body = &idleTimeoutBody{
		ReadCloser: http.MaxBytesReader(w, r.Body, a.info.MaxUpload),
		rc:         rc,
		idle:       a.uploadIdle,
	}
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, r, fmt.Errorf("%w: %w", errNotMultipart, err))
		return
	}
	up, err := a.svc.BeginUpload(dir, fsvc.UploadOptions{Overwrite: overwrite, MakeDirs: mkdirs})
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer up.Abort()

	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeError(w, r, fmt.Errorf("%w: %w", errBadMultipart, err))
			return
		}
		name, ok := uploadName(part.FileName())
		if !ok {
			// Ordinary form fields and empty file inputs carry no file.
			part.Close()
			continue
		}
		if up.Count() >= a.info.MaxFiles {
			part.Close()
			writeError(w, r, fmt.Errorf("%w: more than %d", errTooManyFiles, a.info.MaxFiles))
			return
		}
		err = up.Add(name, part)
		part.Close()
		if err != nil {
			writeError(w, r, err)
			return
		}
	}
	// The body is fully read. A deadline left behind would expire while a
	// slow Commit runs and make net/http treat the connection as broken.
	_ = rc.SetReadDeadline(time.Time{})
	if up.Count() == 0 {
		writeError(w, r, errNoFiles)
		return
	}

	saved, err := up.Commit()
	if err != nil {
		writeError(w, r, err)
		return
	}
	for _, e := range saved {
		a.logger.LogAttrs(r.Context(), slog.LevelInfo, "upload",
			slog.String("req_id", requestID(r)),
			slog.String("file", e.Path),
			slog.Int64("size", *e.Size),
			slog.Bool("overwrite", overwrite),
		)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"files": saved})
}

// uploadName reduces a client-supplied filename to its last element. Some
// browsers send full paths, with either kind of separator whatever the
// server's OS, so both are stripped here rather than relying on filepath.Base.
func uploadName(raw string) (string, bool) {
	if raw == "" {
		return "", false
	}
	return raw[strings.LastIndexAny(raw, `/\`)+1:], true
}

func boolParam(q url.Values, key string) (bool, error) {
	v := q.Get(key)
	if v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%w: %s=%q", errBadQuery, key, v)
	}
	return b, nil
}

// DefaultUploadIdleTimeout is how long an upload may go without receiving
// any bytes. Without it, a stalled client would hold its connection and
// temporary files for as long as TCP takes to notice, or forever.
const DefaultUploadIdleTimeout = time.Minute

// idleTimeoutBody pushes the connection's read deadline forward before each
// read, so slow but steady uploads of any size still succeed.
type idleTimeoutBody struct {
	io.ReadCloser
	rc   *http.ResponseController
	idle time.Duration
}

func (b *idleTimeoutBody) Read(p []byte) (int, error) {
	// Fails only where deadlines are unsupported, such as in tests.
	_ = b.rc.SetReadDeadline(time.Now().Add(b.idle))
	return b.ReadCloser.Read(p)
}

func requestID(r *http.Request) string {
	if st := stateFrom(r.Context()); st != nil {
		return st.id
	}
	return ""
}
