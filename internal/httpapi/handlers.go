package httpapi

import (
	"mime"
	"net/http"

	"remote-explorer/internal/safepath"
)

func (a *api) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *api) getInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.info)
}

func (a *api) list(w http.ResponseWriter, r *http.Request) {
	rel, err := safepath.Clean(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	listing, err := a.svc.List(rel)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, listing)
}

func (a *api) download(w http.ResponseWriter, r *http.Request) {
	rel, err := safepath.Clean(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	f, info, err := a.svc.Open(rel)
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer f.Close()

	h := w.Header()
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": info.Name()})
	if disposition == "" {
		disposition = "attachment"
	}
	h.Set("Content-Disposition", disposition)
	// Uploaded files are untrusted; stop browsers from rendering or running
	// them as pages from this server if they ignore the attachment header.
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	// ServeContent handles Range, HEAD and If-Modified-Since.
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}
