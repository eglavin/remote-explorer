package main

import (
	"bytes"
	"net"
	"strings"
	"testing"

	"remote-explorer/internal/config"
)

func accessOutput(cfg config.Config) string {
	var buf bytes.Buffer
	cfg.Root = "/srv/files"
	printAccess(&buf, &cfg, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080})
	return buf.String()
}

func TestPrintAccessGeneratedToken(t *testing.T) {
	out := accessOutput(config.Config{Token: "GENERATEDTOKEN", TokenGenerated: true})
	for _, want := range []string{
		"(read-only)",
		"    GENERATEDTOKEN\n",
		`curl -H "Authorization: Bearer GENERATEDTOKEN" "http://127.0.0.1:8080/api/info"`,
		`curl -H "Authorization: Bearer GENERATEDTOKEN" "http://127.0.0.1:8080/api/list"`,
		`curl -H "Authorization: Bearer GENERATEDTOKEN" -OJ "http://127.0.0.1:8080/api/download?path=some/file.txt"`,
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
		{config.Config{WebUI: true, Token: "GENERATEDTOKEN", TokenGenerated: true}, "    http://127.0.0.1:8080/#token=GENERATEDTOKEN\n"},
		{config.Config{WebUI: true, Token: "my-own-secret"}, "    http://127.0.0.1:8080/\n"},
		{config.Config{WebUI: true, NoAuth: true}, "    http://127.0.0.1:8080/\n"},
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
	want := `curl -F "file=@local-file.txt" "http://127.0.0.1:8080/api/upload?path=some/folder"`
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

func TestBrowsableAddr(t *testing.T) {
	if got := browsableAddr(&net.TCPAddr{IP: net.IPv4zero, Port: 9000}); got != "localhost:9000" {
		t.Errorf("0.0.0.0 -> %q", got)
	}
	if got := browsableAddr(&net.TCPAddr{IP: net.IPv4(192, 168, 1, 5), Port: 9000}); got != "192.168.1.5:9000" {
		t.Errorf("192.168.1.5 -> %q", got)
	}
}
