// Command remote-explorer serves a folder over HTTP as a JSON API.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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
	"strings"
	"syscall"
	"time"

	"remote-explorer/internal/config"
	"remote-explorer/internal/fsvc"
	"remote-explorer/internal/httpapi"
	"remote-explorer/internal/logging"
	"remote-explorer/internal/term"
	"remote-explorer/internal/tlscert"
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

	svc := fsvc.New(root, cfg.VisibleExt, cfg.UploadExt)
	handler := httpapi.New(httpapi.Options{
		Service: svc,
		Logger:  logger,
		Info: httpapi.Info{
			Writable:          cfg.Write,
			Overwrite:         cfg.Overwrite,
			VisibleExtensions: cfg.VisibleExt.List(),
			UploadExtensions:  cfg.UploadExt.List(),
			MaxUpload:         cfg.MaxUpload,
			MaxFiles:          cfg.MaxFiles,
		},
		Token:        cfg.Token,
		TrustProxy:   cfg.TrustProxy,
		WebUI:        cfg.WebUI,
		AllowedHosts: cfg.AllowedHosts,
	})

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}
	var tlsConfig *tls.Config
	var selfSigned *x509.Certificate
	if !cfg.NoTLS {
		cert, err := loadCertificate(cfg, ln.Addr())
		if err != nil {
			ln.Close()
			return err
		}
		tlsConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
		if cfg.TLSCert == "" {
			selfSigned = cert.Leaf
		}
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// No overall read/write timeouts: they would cut off large uploads and downloads.
		IdleTimeout: 2 * time.Minute,
		ErrorLog:    slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
		TLSConfig:   tlsConfig,
	}
	// The banner comes first so that every log line, including startup
	// warnings, appears below it rather than being buried above it.
	printAccess(os.Stdout, cfg, ln.Addr(), selfSigned)
	logStartup(logger, cfg, ln.Addr())
	if cfg.Write {
		go removeStaleTempFiles(logger, svc)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveErr := make(chan error, 1)
	go func() {
		if cfg.NoTLS {
			serveErr <- srv.Serve(ln)
		} else {
			serveErr <- srv.ServeTLS(ln, "", "")
		}
	}()

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

// loadCertificate reads --tls-cert and --tls-key, or creates a self-signed
// certificate for the addresses clients may use to reach addr.
func loadCertificate(cfg *config.Config, addr net.Addr) (tls.Certificate, error) {
	if cfg.TLSCert != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("--tls-cert/--tls-key: %w", err)
		}
		return cert, nil
	}
	var ip net.IP
	if tcp, ok := addr.(*net.TCPAddr); ok {
		ip = tcp.IP
	}
	names, ips := tlscert.Names(ip, cfg.AllowedHosts)
	cert, err := tlscert.SelfSigned(names, ips, time.Now())
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create self-signed certificate: %w", err)
	}
	return cert, nil
}

// staleTempAge is well beyond DefaultUploadIdleTimeout, so any temporary
// file this old belongs to an upload that can no longer finish.
const staleTempAge = 24 * time.Hour

// removeStaleTempFiles runs in the background because walking a large
// folder can take a while, and nothing depends on it finishing.
func removeStaleTempFiles(logger *slog.Logger, svc *fsvc.Service) {
	n, err := svc.RemoveStaleTempFiles(staleTempAge)
	if n > 0 {
		logger.Info("removed temporary files left by interrupted uploads", "count", n)
	}
	if err != nil {
		logger.Warn("cleaning up temporary upload files", "err", err)
	}
}

func logStartup(logger *slog.Logger, cfg *config.Config, addr net.Addr) {
	attrs := []any{
		"version", versionString(),
		"addr", addr.String(),
		"root", cfg.Root,
		"visible_ext", cfg.VisibleExt.String(),
		"auth", !cfg.NoAuth,
		"web_ui", cfg.WebUI,
		"tls", tlsMode(cfg),
	}
	if cfg.Write {
		attrs = append(attrs,
			"mode", "read-write",
			"upload_ext", cfg.UploadExt.String(),
			"max_upload", cfg.MaxUpload,
			"max_files", cfg.MaxFiles,
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
	if cfg.NoTLS && !loopback {
		logger.Warn("TLS disabled with --no-tls while listening beyond localhost: tokens and files travel unencrypted")
	}
}

func tlsMode(cfg *config.Config) string {
	switch {
	case cfg.NoTLS:
		return "off"
	case cfg.TLSCert != "":
		return "certificate file"
	default:
		return "self-signed"
	}
}

// style wraps banner text in ANSI escape codes, or leaves it alone when
// the output is not a colour terminal.
type style bool

func (s style) wrap(code, text string) string {
	if !s {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s style) heading(text string) string { return s.wrap("1", text) }
func (s style) value(text string) string   { return s.wrap("1;36", text) }
func (s style) command(text string) string { return s.wrap("32", text) }
func (s style) dim(text string) string     { return s.wrap("2", text) }

// printAccess tells the user how to authenticate and how to call each
// endpoint. It writes to stdout rather than the logger so the token never
// ends up in log files or collectors. selfSigned is the generated
// certificate, or nil when TLS is off or the certificate was supplied.
func printAccess(w io.Writer, cfg *config.Config, addr net.Addr, selfSigned *x509.Certificate) {
	s := style(term.Color(w))
	base := "https://" + browsableAddr(addr)
	if cfg.NoTLS {
		base = "http://" + browsableAddr(addr)
	}
	mode := "read-only"
	if cfg.Write {
		mode = "read-write"
	}
	fmt.Fprintf(w, "%s %s at %s %s\n\n", s.heading("Serving"), cfg.Root, s.value(base), s.dim("("+mode+")"))

	// Browsers cannot know a new certificate, so they warn about it; the
	// fingerprint lets the user check it is this server's before going on.
	var curlTLS string
	if selfSigned != nil {
		fmt.Fprintf(w, "%s %s\n%s\n\n    %s\n\n",
			s.heading("Self-signed certificate"),
			s.dim("(new each run; set --tls-cert and --tls-key to use your own)."),
			"Browsers will warn about it. Continue only if its SHA-256 fingerprint is:",
			s.value(tlscert.Fingerprint(selfSigned)))
		curlTLS = `-k --pinnedpubkey "` + tlscert.PublicKeyPin(selfSigned) + `" `
	}

	// A supplied token is the user's secret and stays out of the output;
	// a generated one must be shown or nobody could use the server.
	var auth string
	switch {
	case cfg.NoAuth:
		fmt.Fprintf(w, "%s\n\n", s.heading("No token required (--no-auth)."))
	case cfg.TokenGenerated:
		fmt.Fprintf(w, "%s %s\n\n    %s\n\n", s.heading("Access token"),
			s.dim("(new each run; set --token or "+config.TokenEnv+" to keep one):"), s.value(cfg.Token))
		auth = `-H "Authorization: Bearer ` + cfg.Token + `" `
	default:
		fmt.Fprintf(w, "%s\n\n", s.heading("Using the token from --token or "+config.TokenEnv+"."))
		auth = `-H "Authorization: Bearer <token>" `
	}

	// Generated tokens use only A-Z and 2-7, so they need no escaping in the URL.
	if cfg.WebUI {
		browserURL := base + "/"
		if cfg.TokenGenerated {
			browserURL += "#token=" + cfg.Token
		}
		fmt.Fprintf(w, "%s\n\n    %s\n\n", s.heading("Browse in a web browser:"), s.value(browserURL))
	}

	example := func(title, flags, endpoint string) {
		fmt.Fprintf(w, "  %s\n    %s\n\n", title, s.command(fmt.Sprintf(`curl %s%s%s"%s%s"`, curlTLS, auth, flags, base, endpoint)))
	}
	fmt.Fprintf(w, "%s\n\n", s.heading("Examples:"))
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

	// The rule marks where the banner ends and the request log begins, so
	// the examples do not scroll away unnoticed among log lines.
	logs := "Requests are logged below."
	if cfg.LogFile != "" {
		logs = "Requests are logged to " + cfg.LogFile + "."
	}
	fmt.Fprintf(w, "%s\n%s\n\n", s.dim(strings.Repeat("─", 72)), s.dim("Press Ctrl+C to stop. "+logs))
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
