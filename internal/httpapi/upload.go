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

	r.Body = http.MaxBytesReader(w, r.Body, a.info.MaxUpload)
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
		err = up.Add(name, part)
		part.Close()
		if err != nil {
			writeError(w, r, err)
			return
		}
	}
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

func requestID(r *http.Request) string {
	if st := stateFrom(r.Context()); st != nil {
		return st.id
	}
	return ""
}
