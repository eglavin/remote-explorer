// Command remote-explorer serves a folder over HTTP as a JSON API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"syscall"
	"time"

	"remote-explorer/internal/config"
	"remote-explorer/internal/fsvc"
	"remote-explorer/internal/httpapi"
	"remote-explorer/internal/logging"
)

const shutdownTimeout = 10 * time.Second

// version is set at build time by scripts/build with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

// versionString falls back to the VCS revision Go embeds in plain
// "go build" and "go install" builds, which do not set version.
func versionString() string {
	if version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	var revision string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if len(revision) < 7 {
		return version
	}
	v := "dev-" + revision[:7]
	if dirty {
		v += "-dirty"
	}
	return v
}

func run(args []string) int {
	cfg, err := config.Parse(args, os.Stderr)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return 0
	case errors.Is(err, config.ErrFlagSyntax):
		// The flag package has already printed the error and usage.
		return 2
	case err != nil:
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	case cfg.ShowVersion:
		fmt.Println("remote-explorer", versionString())
		return 0
	}

	logger, closeLog, err := logging.New(cfg.LogFormat, cfg.LogLevel, cfg.LogFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	defer closeLog()

	if err := serve(cfg, logger); err != nil {
		logger.Error("server stopped", "err", err)
		return 1
	}
	return 0
}

func serve(cfg *config.Config, logger *slog.Logger) error {
	root, err := os.OpenRoot(cfg.Root)
	if err != nil {
		return err
	}
	defer root.Close()

	handler := httpapi.New(httpapi.Options{
		Service: fsvc.New(root, cfg.VisibleExt, cfg.UploadExt),
		Logger:  logger,
		Info: httpapi.Info{
			Writable:          cfg.Write,
			Overwrite:         cfg.Overwrite,
			VisibleExtensions: cfg.VisibleExt.List(),
			UploadExtensions:  cfg.UploadExt.List(),
			MaxUpload:         cfg.MaxUpload,
		},
		Token:      cfg.Token,
		TrustProxy: cfg.TrustProxy,
	})

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// No overall read/write timeouts: they would cut off large uploads and downloads.
		IdleTimeout: 2 * time.Minute,
		ErrorLog:    slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	logStartup(logger, cfg, ln.Addr())
	printAccess(os.Stdout, cfg, ln.Addr())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down", "timeout", shutdownTimeout.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	logger.Info("stopped")
	return nil
}

func logStartup(logger *slog.Logger, cfg *config.Config, addr net.Addr) {
	attrs := []any{
		"version", versionString(),
		"addr", addr.String(),
		"root", cfg.Root,
		"visible_ext", cfg.VisibleExt.String(),
		"auth", !cfg.NoAuth,
	}
	if cfg.Write {
		attrs = append(attrs,
			"mode", "read-write",
			"upload_ext", cfg.UploadExt.String(),
			"max_upload", cfg.MaxUpload,
			"overwrite", cfg.Overwrite,
		)
	} else {
		attrs = append(attrs, "mode", "read-only")
	}
	logger.Info("listening", attrs...)

	tcp, ok := addr.(*net.TCPAddr)
	loopback := ok && tcp.IP.IsLoopback()
	switch {
	case cfg.NoAuth && !loopback:
		logger.Warn("authentication disabled with --no-auth while listening beyond localhost: anyone who can reach this address can use the API")
	case cfg.NoAuth:
		logger.Warn("authentication disabled with --no-auth: any program on this machine can use the API")
	case !loopback:
		logger.Warn("listening beyond localhost")
	}
}

// printAccess tells the user how to authenticate and how to call each
// endpoint. It writes to stdout rather than the logger so the token never
// ends up in log files or collectors.
func printAccess(w io.Writer, cfg *config.Config, addr net.Addr) {
	base := "http://" + browsableAddr(addr)
	mode := "read-only"
	if cfg.Write {
		mode = "read-write"
	}
	fmt.Fprintf(w, "Serving %s at %s (%s)\n\n", cfg.Root, base, mode)

	// A supplied token is the user's secret and stays out of the output;
	// a generated one must be shown or nobody could use the server.
	var auth string
	switch {
	case cfg.NoAuth:
		fmt.Fprintf(w, "No token required (--no-auth).\n\n")
	case cfg.TokenGenerated:
		fmt.Fprintf(w, "Access token (new each run; set --token or %s to keep one):\n\n    %s\n\n", config.TokenEnv, cfg.Token)
		auth = `-H "Authorization: Bearer ` + cfg.Token + `" `
	default:
		fmt.Fprintf(w, "Using the token from --token or %s.\n\n", config.TokenEnv)
		auth = `-H "Authorization: Bearer <token>" `
	}

	example := func(title, flags, endpoint string) {
		fmt.Fprintf(w, "  %s\n    curl %s%s\"%s%s\"\n\n", title, auth, flags, base, endpoint)
	}
	fmt.Fprintf(w, "Examples:\n\n")
	example("Server info and limits:", "", "/api/info")
	example("List the root folder (add ?path=some/folder for others):", "", "/api/list")
	example("Download a file (-OJ saves it under its own name):", "-OJ ", "/api/download?path=some/file.txt")
	if cfg.Write {
		options := "add &mkdirs=true to create the folder"
		if cfg.Overwrite {
			options += ", &overwrite=true to replace existing files"
		}
		example("Upload files into a folder (repeat -F for more files; "+options+"):",
			`-F "file=@local-file.txt" `, "/api/upload?path=some/folder")
	}
}

// browsableAddr replaces a wildcard listen address such as 0.0.0.0 with
// localhost, which a user can actually open.
func browsableAddr(addr net.Addr) string {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok || !tcp.IP.IsUnspecified() {
		return addr.String()
	}
	return net.JoinHostPort("localhost", strconv.Itoa(tcp.Port))
}
