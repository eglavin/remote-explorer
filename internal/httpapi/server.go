// Package httpapi exposes the file service as a JSON HTTP API.
package httpapi

import (
	"cmp"
	"log/slog"
	"net/http"
	"time"

	"remote-explorer/internal/fsvc"
	"remote-explorer/internal/webui"
)

type Options struct {
	Service    *fsvc.Service
	Logger     *slog.Logger
	Info       Info
	Token      string
	TrustProxy bool
	// WebUI serves the browser front end at /. Without it only /api and
	// /healthz exist.
	WebUI bool
	// UploadIdleTimeout aborts an upload when no body bytes arrive for this
	// long. Zero means DefaultUploadIdleTimeout.
	UploadIdleTimeout time.Duration
	// AllowedHosts are host names accepted in the Host header besides IP
	// addresses and localhost. Only checked when Token is empty.
	AllowedHosts []string
}

// Info describes the server's capabilities to clients. It also drives the
// server: uploads are only routed when Writable is set.
type Info struct {
	Writable          bool     `json:"writable"`
	Overwrite         bool     `json:"overwrite"`
	VisibleExtensions []string `json:"visibleExtensions"`
	UploadExtensions  []string `json:"uploadExtensions"`
	MaxUpload         int64    `json:"maxUpload"`
	MaxFiles          int      `json:"maxFiles"`
}

// New returns the complete handler, including request logging.
func New(o Options) http.Handler {
	a := &api{svc: o.Service, info: o.Info, logger: o.Logger, uploadIdle: cmp.Or(o.UploadIdleTimeout, DefaultUploadIdleTimeout)}

	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /api/info", a.getInfo)
	apiMux.HandleFunc("GET /api/list", a.list)
	apiMux.HandleFunc("GET /api/download", a.download)
	// Not registering write routes at all keeps them unreachable in read-only mode.
	if o.Info.Writable {
		apiMux.HandleFunc("POST /api/upload", a.upload)
	}

	// Browsers attach no token, so the token already stops cross-site pages.
	// These checks protect --no-auth servers, and cost nothing otherwise.
	csrf := http.NewCrossOriginProtection()
	csrf.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, errCrossOrigin)
	}))
	apiHandler := csrf.Handler(requireToken(o.Token, apiMux))
	if o.Token == "" {
		apiHandler = requireKnownHost(o.AllowedHosts, apiHandler)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.healthz)
	if o.WebUI {
		mux.Handle("GET /{$}", webui.Index())
		mux.Handle("GET "+webui.AssetPrefix, webui.Assets())
	}
	mux.Handle("/api/", apiHandler)

	return withLogging(o.Logger, o.TrustProxy, mux)
}

type api struct {
	svc        *fsvc.Service
	info       Info
	logger     *slog.Logger
	uploadIdle time.Duration
}
