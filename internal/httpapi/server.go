// Package httpapi exposes the file service as a JSON HTTP API.
package httpapi

import (
	"log/slog"
	"net/http"

	"remote-explorer/internal/fsvc"
)

type Options struct {
	Service    *fsvc.Service
	Logger     *slog.Logger
	Info       Info
	Token      string
	TrustProxy bool
}

// Info describes the server's capabilities to clients. It also drives the
// server: uploads are only routed when Writable is set.
type Info struct {
	Writable          bool     `json:"writable"`
	Overwrite         bool     `json:"overwrite"`
	VisibleExtensions []string `json:"visibleExtensions"`
	UploadExtensions  []string `json:"uploadExtensions"`
	MaxUpload         int64    `json:"maxUpload"`
}

// New returns the complete handler, including request logging.
func New(o Options) http.Handler {
	a := &api{svc: o.Service, info: o.Info, logger: o.Logger}

	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /api/info", a.getInfo)
	apiMux.HandleFunc("GET /api/list", a.list)
	apiMux.HandleFunc("GET /api/download", a.download)
	// Not registering write routes at all keeps them unreachable in read-only mode.
	if o.Info.Writable {
		apiMux.HandleFunc("POST /api/upload", a.upload)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.healthz)
	mux.Handle("/api/", requireToken(o.Token, apiMux))

	return withLogging(o.Logger, o.TrustProxy, mux)
}

type api struct {
	svc    *fsvc.Service
	info   Info
	logger *slog.Logger
}
