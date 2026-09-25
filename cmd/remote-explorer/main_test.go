package main

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"remote-explorer/internal/config"
	"remote-explorer/internal/tlscert"
)

func accessOutput(cfg config.Config) string {
	return accessOutputWithCert(cfg, nil)
}

func accessOutputWithCert(cfg config.Config, cert *x509.Certificate) string {
	var buf bytes.Buffer
	cfg.Root = "/srv/files"
	printAccess(&buf, &cfg, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}, cert)
	return buf.String()
}

func TestPrintAccessGeneratedToken(t *testing.T) {
	out := accessOutput(config.Config{Token: "GENERATEDTOKEN", TokenGenerated: true})
	for _, want := range []string{
		"(read-only)",
		"    GENERATEDTOKEN\n",
		`curl -H "Authorization: Bearer GENERATEDTOKEN" "https://127.0.0.1:8080/api/info"`,
		`curl -H "Authorization: Bearer GENERATEDTOKEN" "https://127.0.0.1:8080/api/list"`,
		`curl -H "Authorization: Bearer GENERATEDTOKEN" -OJ "https://127.0.0.1:8080/api/download?path=some/file.txt"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "/api/upload") {
		t.Errorf("upload example shown for a read-only server:\n%s", out)
	}
}

func TestPrintAccessSuppliedTokenIsHidden(t *testing.T) {
	out := accessOutput(config.Config{Token: "my-own-secret"})
	if strings.Contains(out, "my-own-secret") {
		t.Errorf("supplied token printed:\n%s", out)
	}
	if !strings.Contains(out, `-H "Authorization: Bearer <token>"`) {
		t.Errorf("examples lack the token placeholder:\n%s", out)
	}
}

func TestPrintAccessWebUI(t *testing.T) {
	if out := accessOutput(config.Config{Token: "GENERATEDTOKEN", TokenGenerated: true}); strings.Contains(out, "web browser") {
		t.Errorf("browser link shown without --web-ui:\n%s", out)
	}
	for _, tc := range []struct {
		cfg  config.Config
		want string
	}{
		{config.Config{WebUI: true, Token: "GENERATEDTOKEN", TokenGenerated: true}, "    https://127.0.0.1:8080/#token=GENERATEDTOKEN\n"},
		{config.Config{WebUI: true, Token: "my-own-secret"}, "    https://127.0.0.1:8080/\n"},
		{config.Config{WebUI: true, NoAuth: true}, "    https://127.0.0.1:8080/\n"},
	} {
		out := accessOutput(tc.cfg)
		if !strings.Contains(out, tc.want) {
			t.Errorf("%+v: output missing %q:\n%s", tc.cfg, tc.want, out)
		}
		if strings.Contains(out, "my-own-secret") {
			t.Errorf("supplied token printed:\n%s", out)
		}
	}
}

func TestPrintAccessWrite(t *testing.T) {
	out := accessOutput(config.Config{NoAuth: true, Write: true})
	want := `curl -F "file=@local-file.txt" "https://127.0.0.1:8080/api/upload?path=some/folder"`
	if !strings.Contains(out, want) || !strings.Contains(out, "(read-write)") {
		t.Errorf("output missing upload example:\n%s", out)
	}
	if strings.Contains(out, "Authorization") {
		t.Errorf("--no-auth examples include a token header:\n%s", out)
	}
	if strings.Contains(out, "overwrite=true") {
		t.Errorf("overwrite hint shown without --overwrite:\n%s", out)
	}
	if out := accessOutput(config.Config{NoAuth: true, Write: true, Overwrite: true}); !strings.Contains(out, "&overwrite=true") {
		t.Errorf("overwrite hint missing with --overwrite:\n%s", out)
	}
}

func TestPrintAccessSelfSigned(t *testing.T) {
	cert, err := tlscert.SelfSigned([]string{"localhost"}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	out := accessOutputWithCert(config.Config{Token: "GENERATEDTOKEN", TokenGenerated: true}, cert.Leaf)
	pin := tlscert.PublicKeyPin(cert.Leaf)
	for _, want := range []string{
		"    " + tlscert.Fingerprint(cert.Leaf) + "\n",
		`curl -k --pinnedpubkey "` + pin + `" -H "Authorization: Bearer GENERATEDTOKEN" "https://127.0.0.1:8080/api/info"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintAccessSuppliedCertificate(t *testing.T) {
	out := accessOutput(config.Config{NoAuth: true})
	if strings.Contains(out, "fingerprint") || strings.Contains(out, "-k ") {
		t.Errorf("self-signed details shown for a supplied certificate:\n%s", out)
	}
}

func TestPrintAccessNoTLS(t *testing.T) {
	out := accessOutput(config.Config{NoAuth: true, NoTLS: true, WebUI: true})
	for _, want := range []string{"at http://127.0.0.1:8080 ", `curl "http://127.0.0.1:8080/api/info"`, "    http://127.0.0.1:8080/\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "https://") {
		t.Errorf("https URL shown with --no-tls:\n%s", out)
	}
}

func TestPrintAccessFooter(t *testing.T) {
	out := accessOutput(config.Config{NoAuth: true})
	if !strings.HasSuffix(out, "\n\n"+strings.Repeat("─", 72)+"\nPress Ctrl+C to stop. Requests are logged below.\n\n") {
		t.Errorf("output does not end with the divider and footer:\n%s", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("escape codes written to a non-terminal:\n%q", out)
	}
	if out := accessOutput(config.Config{NoAuth: true, LogFile: "server.log"}); !strings.Contains(out, "Requests are logged to server.log.") {
		t.Errorf("footer does not name the log file:\n%s", out)
	}
}

func TestStyle(t *testing.T) {
	if got := style(false).value("x"); got != "x" {
		t.Errorf("colour off: %q", got)
	}
	if got := style(true).value("x"); got != "\x1b[1;36mx\x1b[0m" {
		t.Errorf("colour on: %q", got)
	}
}

func TestLoadCertificateFromFiles(t *testing.T) {
	want, err := tlscert.SelfSigned([]string{"localhost"}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(want.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: want.Certificate[0]}), 0o600)
	os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0o600)

	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}
	got, err := loadCertificate(&config.Config{TLSCert: certFile, TLSKey: keyFile}, addr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Certificate[0], want.Certificate[0]) {
		t.Error("loaded a different certificate")
	}
	if _, err := loadCertificate(&config.Config{TLSCert: certFile, TLSKey: certFile}, addr); err == nil {
		t.Error("certificate given as its own key: want error")
	}
}

func TestBrowsableAddr(t *testing.T) {
	if got := browsableAddr(&net.TCPAddr{IP: net.IPv4zero, Port: 9000}); got != "localhost:9000" {
		t.Errorf("0.0.0.0 -> %q", got)
	}
	if got := browsableAddr(&net.TCPAddr{IP: net.IPv4(192, 168, 1, 5), Port: 9000}); got != "192.168.1.5:9000" {
		t.Errorf("192.168.1.5 -> %q", got)
	}
}
