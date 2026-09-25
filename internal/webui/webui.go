// Package webui serves a browser front end for the JSON API, styled after
// Apache's directory index pages.
//
// The pages hold no data of their own and need no token: the script fetches
// everything from /api with the token the user supplies.
package webui

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"time"
)

// Fixed types, because mime.TypeByExtension reads the Windows registry, which
// can map .js to text/plain; with nosniff the browser would then refuse it.
var contentTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".svg":  "image/svg+xml",
}

//go:embed static
var static embed.FS

// AssetPrefix is where the script, stylesheet and icons are served.
const AssetPrefix = "/ui/"

// The script is the only code that runs; everything it loads comes from this
// server. frame-ancestors stops other sites framing the upload form.
const csp = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; " +
	"connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// Index serves the page itself.
func Index() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveFile(w, r, "static/index.html")
	})
}

// Assets serves the files under AssetPrefix. Directories are not listed.
func Assets() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ServeMux has already cleaned the path.
		name := r.URL.Path[len(AssetPrefix)-1:]
		if name == "/index.html" {
			http.NotFound(w, r)
			return
		}
		serveFile(w, r, "static"+name)
	})
}

func serveFile(w http.ResponseWriter, r *http.Request, name string) {
	data, err := fs.ReadFile(static, name)
	contentType, known := contentTypes[path.Ext(name)]
	if err != nil || !known {
		// Also covers directories, which ReadFile refuses.
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	// Embedded files carry no modification time, so make browsers revalidate
	// rather than keep a page from an older binary.
	h.Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}
