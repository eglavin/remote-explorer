// Package config parses and validates the command-line flags.
package config

import (
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"remote-explorer/internal/extfilter"
)

// TokenEnv can supply the token instead of --token, which keeps it out of
// the process list.
const TokenEnv = "REMOTE_EXPLORER_TOKEN"

const (
	MinTokenLength     = 8
	MaxTokenLength     = 256
	DefaultTokenLength = 26
	DefaultMaxFiles    = 1000
)

// ErrFlagSyntax wraps errors the flag package has already printed along with usage.
var ErrFlagSyntax = errors.New("invalid flags")

type Config struct {
	Root       string
	Addr       string
	Write      bool
	Overwrite  bool
	WebUI      bool
	MaxUpload  int64
	MaxFiles   int
	VisibleExt extfilter.Set
	UploadExt  extfilter.Set
	Token      string
	// TokenGenerated is true when no token was supplied and one was created
	// for this run, so it must be shown to the user.
	TokenGenerated bool
	NoAuth         bool
	// AllowedHosts are extra Host header names accepted with --no-auth.
	AllowedHosts []string
	TrustProxy   bool
	// NoTLS serves plain HTTP. Otherwise TLSCert and TLSKey name the
	// certificate files, or are empty for a self-signed certificate.
	NoTLS     bool
	TLSCert   string
	TLSKey    string
	LogFormat string
	LogLevel  slog.Level
	LogFile   string
	// ShowVersion is set by --version. No other fields are filled in then.
	ShowVersion bool
}

// Parse parses args (without the program name). Usage and flag errors are written to output.
func Parse(args []string, output io.Writer) (*Config, error) {
	fs := flag.NewFlagSet("remote-explorer", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.Usage = func() {
		fmt.Fprintln(output, "Usage: remote-explorer [flags] <folder>")
		fmt.Fprintln(output)
		fmt.Fprintln(output, "Serves <folder> over HTTPS as a JSON API. Read-only unless --write is given.")
		fmt.Fprintln(output)
		fs.PrintDefaults()
	}

	var (
		c                                     Config
		maxUpload, allowExt, uploadExt, level string
		allowHost                             string
		tokenLength, maxFiles                 int
	)
	fs.StringVar(&c.Root, "root", "", "folder to serve (can also be given as the only argument)")
	fs.StringVar(&c.Addr, "addr", "127.0.0.1:8080", "address to listen on; use 0.0.0.0:8080 to accept other machines")
	fs.BoolVar(&c.Write, "write", false, "enable uploads; without it the server is read-only")
	fs.BoolVar(&c.Overwrite, "overwrite", false, "let uploads replace existing files (needs --write)")
	fs.StringVar(&maxUpload, "max-upload", "1GiB", "maximum size of one upload request, e.g. 500MB or 2GiB (needs --write)")
	fs.IntVar(&maxFiles, "max-files", DefaultMaxFiles, "maximum number of files in one upload request (needs --write)")
	fs.BoolVar(&c.WebUI, "web-ui", false, "serve a folder listing page for web browsers at /")
	fs.StringVar(&allowExt, "allow-ext", "", "comma-separated extensions that are listed, downloadable and uploadable, e.g. zip,mp4,mp3 (default all)")
	fs.StringVar(&uploadExt, "allow-upload-ext", "", "comma-separated extensions that can be uploaded; must be within --allow-ext (needs --write)")
	fs.StringVar(&c.Token, "token", "", "bearer token required on /api requests (or set "+TokenEnv+"); a random one is generated and printed if not given")
	fs.IntVar(&tokenLength, "token-length", DefaultTokenLength,
		fmt.Sprintf("length of the generated token, %d to %d characters", MinTokenLength, MaxTokenLength))
	fs.BoolVar(&c.NoAuth, "no-auth", false, "do not require a token; anyone who can reach the server can use the API")
	fs.StringVar(&allowHost, "allow-host", "", "comma-separated host names clients may use to reach a --no-auth server, besides IP addresses and localhost")
	fs.BoolVar(&c.NoTLS, "no-tls", false, "serve plain HTTP instead of HTTPS; the token and files travel unencrypted")
	fs.StringVar(&c.TLSCert, "tls-cert", "", "PEM certificate file to serve (needs --tls-key); a self-signed one is generated if not given")
	fs.StringVar(&c.TLSKey, "tls-key", "", "PEM private key file for --tls-cert")
	fs.BoolVar(&c.TrustProxy, "trust-proxy", false, "log the client address from X-Forwarded-For (only behind a proxy you control)")
	fs.StringVar(&c.LogFormat, "log-format", "text", "log format: text or json")
	fs.StringVar(&level, "log-level", "info", "minimum log level: debug, info, warn or error")
	fs.StringVar(&c.LogFile, "log-file", "", "append logs to this file instead of stderr")
	fs.BoolVar(&c.ShowVersion, "version", false, "print the version and exit")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %w", ErrFlagSyntax, err)
	}
	if c.ShowVersion {
		return &Config{ShowVersion: true}, nil
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	switch fs.NArg() {
	case 0:
	case 1:
		if c.Root != "" {
			return nil, errors.New("give the folder either as --root or as an argument, not both")
		}
		c.Root = fs.Arg(0)
	default:
		return nil, fmt.Errorf("expected one folder argument, got %d: %q", fs.NArg(), fs.Args())
	}
	root, err := resolveRoot(c.Root)
	if err != nil {
		return nil, err
	}
	c.Root = root

	if !c.Write {
		for _, name := range []string{"overwrite", "max-upload", "max-files", "allow-upload-ext"} {
			if set[name] {
				return nil, fmt.Errorf("--%s has no effect without --write", name)
			}
		}
	}
	if c.Write {
		if c.MaxUpload, err = ParseSize(maxUpload); err != nil {
			return nil, fmt.Errorf("--max-upload: %w", err)
		}
		if maxFiles < 1 {
			return nil, fmt.Errorf("--max-files must be at least 1, got %d", maxFiles)
		}
		c.MaxFiles = maxFiles
	}

	if c.VisibleExt, err = extfilter.Parse(allowExt); err != nil {
		return nil, fmt.Errorf("--allow-ext: %w", err)
	}
	if c.UploadExt, err = extfilter.Parse(uploadExt); err != nil {
		return nil, fmt.Errorf("--allow-upload-ext: %w", err)
	}
	if missing := c.UploadExt.Outside(c.VisibleExt); len(missing) > 0 {
		return nil, fmt.Errorf("--allow-upload-ext contains %s which --allow-ext does not allow",
			strings.Join(missing, ", "))
	}
	if len(c.UploadExt) == 0 {
		c.UploadExt = c.VisibleExt
	}

	if tokenLength < MinTokenLength || tokenLength > MaxTokenLength {
		return nil, fmt.Errorf("--token-length must be between %d and %d, got %d",
			MinTokenLength, MaxTokenLength, tokenLength)
	}
	switch {
	case c.NoAuth && set["token"]:
		return nil, errors.New("--no-auth and --token cannot be used together")
	case c.NoAuth && set["token-length"]:
		return nil, errors.New("--token-length has no effect with --no-auth")
	case c.NoAuth:
		// A token left in the environment must not silently re-enable auth.
		c.Token = ""
	default:
		if c.Token == "" {
			c.Token = os.Getenv(TokenEnv)
		}
		if c.Token == "" {
			c.Token, c.TokenGenerated = generateToken(tokenLength), true
			break
		}
		if set["token-length"] {
			return nil, errors.New("--token-length only applies to generated tokens, but a token was supplied")
		}
		if len(c.Token) < MinTokenLength {
			return nil, fmt.Errorf("the supplied token must be at least %d characters", MinTokenLength)
		}
	}
	if set["allow-host"] {
		if !c.NoAuth {
			return nil, errors.New("--allow-host only applies with --no-auth; with a token any host name is accepted")
		}
		if c.AllowedHosts, err = parseHosts(allowHost); err != nil {
			return nil, fmt.Errorf("--allow-host: %w", err)
		}
	}
	switch {
	case c.NoTLS && (c.TLSCert != "" || c.TLSKey != ""):
		return nil, errors.New("--tls-cert and --tls-key have no effect with --no-tls")
	case (c.TLSCert == "") != (c.TLSKey == ""):
		return nil, errors.New("--tls-cert and --tls-key must be given together")
	}
	if c.LogFormat != "text" && c.LogFormat != "json" {
		return nil, fmt.Errorf("--log-format must be text or json, got %q", c.LogFormat)
	}
	if err := c.LogLevel.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("--log-level: %w", err)
	}
	return &c, nil
}

func parseHosts(s string) ([]string, error) {
	var hosts []string
	for _, raw := range strings.Split(s, ",") {
		h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
		if h == "" {
			return nil, fmt.Errorf("empty host name in %q", s)
		}
		if strings.ContainsAny(h, ":/[] ") {
			return nil, fmt.Errorf("%q must be a bare host name, without port or scheme", strings.TrimSpace(raw))
		}
		hosts = append(hosts, h)
	}
	return hosts, nil
}

// tokenAlphabet matches rand.Text: unambiguous, and safe in URLs and headers.
const tokenAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

func generateToken(length int) string {
	b := make([]byte, length)
	_, _ = rand.Read(b)
	for i := range b {
		// 256 is a multiple of 32, so masking keeps every character equally likely.
		b[i] = tokenAlphabet[b[i]&31]
	}
	return string(b)
}

func resolveRoot(root string) (string, error) {
	if root == "" {
		return "", errors.New("a folder to serve is required (see --help)")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", root, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("folder to serve: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("folder to serve: %s is not a directory", abs)
	}
	return abs, nil
}

var sizeUnits = []struct {
	suffix string
	mult   int64
}{
	// Longer suffixes first so "KiB" is not read as "B".
	{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}, {"TiB", 1 << 40},
	{"KB", 1e3}, {"MB", 1e6}, {"GB", 1e9}, {"TB", 1e12},
	{"B", 1},
}

// ParseSize parses sizes such as "1024", "500MB" or "2GiB". Units are case-insensitive.
func ParseSize(s string) (int64, error) {
	num, mult := strings.TrimSpace(s), int64(1)
	for _, u := range sizeUnits {
		if len(num) > len(u.suffix) && strings.EqualFold(num[len(num)-len(u.suffix):], u.suffix) {
			num, mult = strings.TrimSpace(num[:len(num)-len(u.suffix)]), u.mult
			break
		}
	}
	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	if n > math.MaxInt64/mult {
		return 0, fmt.Errorf("size %q is too large", s)
	}
	return n * mult, nil
}
