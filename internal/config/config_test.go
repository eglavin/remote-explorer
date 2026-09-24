package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func parse(t *testing.T, args ...string) (*Config, error) {
	t.Helper()
	return Parse(args, io.Discard)
}

func TestParseDefaults(t *testing.T) {
	t.Setenv(TokenEnv, "")
	dir := t.TempDir()
	c, err := parse(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Root != dir || c.Addr != "127.0.0.1:8080" || c.Write || c.MaxUpload != 0 ||
		c.VisibleExt != nil || c.UploadExt != nil || c.NoAuth ||
		c.LogFormat != "text" || c.LogLevel != slog.LevelInfo {
		t.Errorf("unexpected defaults: %+v", c)
	}
}

func TestParseGeneratesToken(t *testing.T) {
	t.Setenv(TokenEnv, "")
	dir := t.TempDir()
	a, err := parse(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := parse(t, dir)
	if !a.TokenGenerated || len(a.Token) < 20 {
		t.Errorf("token = %q generated=%v, want a generated token", a.Token, a.TokenGenerated)
	}
	if a.Token == b.Token {
		t.Error("two runs generated the same token")
	}
}

func TestParseTokenLength(t *testing.T) {
	t.Setenv(TokenEnv, "")
	dir := t.TempDir()

	c, _ := parse(t, dir)
	if len(c.Token) != DefaultTokenLength {
		t.Errorf("default token length = %d, want %d", len(c.Token), DefaultTokenLength)
	}
	for _, n := range []int{MinTokenLength, 40, MaxTokenLength} {
		c, err := parse(t, fmt.Sprintf("--token-length=%d", n), dir)
		if err != nil {
			t.Errorf("--token-length=%d: %v", n, err)
			continue
		}
		if len(c.Token) != n || strings.Trim(c.Token, tokenAlphabet) != "" {
			t.Errorf("--token-length=%d: got %q", n, c.Token)
		}
	}

	for _, args := range [][]string{
		{"--token-length=7"},
		{"--token-length=0"},
		{"--token-length=-5"},
		{fmt.Sprintf("--token-length=%d", MaxTokenLength+1)},
		{"--token-length=12", "--token=abcdefghij"},
		{"--token-length=12", "--no-auth"},
		{"--token=short"},
	} {
		if _, err := parse(t, append(args, dir)...); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", args)
		}
	}

	t.Setenv(TokenEnv, "short")
	if _, err := parse(t, dir); err == nil {
		t.Error("short token from environment: want error")
	}
}

func TestParseNoAuth(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(TokenEnv, "from-env")
	c, err := parse(t, "--no-auth", dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Token != "" || c.TokenGenerated || !c.NoAuth {
		t.Errorf("--no-auth: token=%q generated=%v", c.Token, c.TokenGenerated)
	}
	if _, err := parse(t, "--no-auth", "--token=x", dir); err == nil {
		t.Error("--no-auth with --token: want error")
	}
}

func TestParseRoot(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if c, err := parse(t, "--root", dir); err != nil || c.Root != dir {
		t.Errorf("--root: %v, %v", c, err)
	}
	for _, args := range [][]string{
		{},
		{"--root", dir, dir},
		{dir, dir},
		{filepath.Join(dir, "missing")},
		{file},
	} {
		if _, err := parse(t, args...); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", args)
		}
	}
}

func TestParseFlagsNeedWrite(t *testing.T) {
	dir := t.TempDir()
	for _, flagArg := range []string{"--overwrite", "--max-upload=5MB", "--allow-upload-ext=zip"} {
		_, err := parse(t, flagArg, dir)
		if err == nil || !strings.Contains(err.Error(), "--write") {
			t.Errorf("%s without --write: got %v", flagArg, err)
		}
		if _, err := parse(t, "--write", flagArg, dir); err != nil {
			t.Errorf("%s with --write: %v", flagArg, err)
		}
	}
}

func TestParseExtensions(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		args        []string
		visible     []string
		upload      []string
		errContains string
	}{
		{args: nil, visible: nil, upload: nil},
		{args: []string{"--allow-ext=zip,MP4,.mp3"}, visible: []string{"mp3", "mp4", "zip"}, upload: []string{"mp3", "mp4", "zip"}},
		{args: []string{"--write", "--allow-upload-ext=zip"}, visible: nil, upload: []string{"zip"}},
		{args: []string{"--write", "--allow-ext=zip,mp4,mp3", "--allow-upload-ext=zip"}, visible: []string{"mp3", "mp4", "zip"}, upload: []string{"zip"}},
		{args: []string{"--write", "--allow-ext=zip", "--allow-upload-ext=zip,exe"}, errContains: "exe"},
		{args: []string{"--allow-ext=zip,,mp3"}, errContains: "--allow-ext"},
	}
	for _, tt := range tests {
		c, err := parse(t, append(tt.args, dir)...)
		if tt.errContains != "" {
			if err == nil || !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("%q: got %v, want error containing %q", tt.args, err, tt.errContains)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", tt.args, err)
			continue
		}
		if !slices.Equal(c.VisibleExt, tt.visible) || !slices.Equal(c.UploadExt, tt.upload) {
			t.Errorf("%q: visible=%v upload=%v, want %v %v", tt.args, c.VisibleExt, c.UploadExt, tt.visible, tt.upload)
		}
	}
}

func TestParseTokenFromEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(TokenEnv, "from-env")
	if c, _ := parse(t, dir); c.Token != "from-env" || c.TokenGenerated {
		t.Errorf("token = %q generated=%v, want from-env", c.Token, c.TokenGenerated)
	}
	if c, _ := parse(t, "--token=from-flag", dir); c.Token != "from-flag" || c.TokenGenerated {
		t.Errorf("token = %q generated=%v, want from-flag", c.Token, c.TokenGenerated)
	}
}

func TestParseLogging(t *testing.T) {
	dir := t.TempDir()
	c, err := parse(t, "--log-format=json", "--log-level=warn", dir)
	if err != nil || c.LogFormat != "json" || c.LogLevel != slog.LevelWarn {
		t.Errorf("got %+v, %v", c, err)
	}
	for _, arg := range []string{"--log-format=xml", "--log-level=loud"} {
		if _, err := parse(t, arg, dir); err == nil {
			t.Errorf("%s: want error", arg)
		}
	}
}

func TestParseVersionNeedsNoFolder(t *testing.T) {
	c, err := parse(t, "--version")
	if err != nil || !c.ShowVersion {
		t.Errorf("--version: %+v, %v", c, err)
	}
}

func TestParseFlagErrors(t *testing.T) {
	if _, err := parse(t, "--help"); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("--help: got %v", err)
	}
	if _, err := parse(t, "--nope"); !errors.Is(err, ErrFlagSyntax) {
		t.Errorf("--nope: got %v", err)
	}
}

func TestParseSize(t *testing.T) {
	tests := []struct {
		in   string
		want int64
	}{
		{"1024", 1024},
		{"10B", 10},
		{"1KiB", 1024},
		{"500MB", 500_000_000},
		{"2GiB", 2 << 30},
		{"1 gib", 1 << 30},
	}
	for _, tt := range tests {
		if got, err := ParseSize(tt.in); err != nil || got != tt.want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", tt.in, got, err, tt.want)
		}
	}
	for _, in := range []string{"", "0", "-5MB", "MB", "1.5GB", "abc", "99999999999TiB"} {
		if _, err := ParseSize(in); err == nil {
			t.Errorf("ParseSize(%q) succeeded, want error", in)
		}
	}
}

func TestParseAllowHost(t *testing.T) {
	t.Setenv(TokenEnv, "")
	dir := t.TempDir()
	c, err := parse(t, "--no-auth", "--allow-host", " NAS.lan., files.example.org ", dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"nas.lan", "files.example.org"}; !slices.Equal(c.AllowedHosts, want) {
		t.Errorf("AllowedHosts = %q, want %q", c.AllowedHosts, want)
	}

	for _, args := range [][]string{
		{"--allow-host=nas.lan"}, // a token is in use
		{"--no-auth", "--allow-host="},
		{"--no-auth", "--allow-host=nas.lan,"},
		{"--no-auth", "--allow-host=nas.lan:8080"},
		{"--no-auth", "--allow-host=http://nas.lan"},
		{"--no-auth", "--allow-host=[::1]"},
	} {
		if _, err := parse(t, append(args, dir)...); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", args)
		}
	}
}

func TestParseMaxFiles(t *testing.T) {
	t.Setenv(TokenEnv, "")
	dir := t.TempDir()
	c, err := parse(t, "--write", dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxFiles != DefaultMaxFiles {
		t.Errorf("default MaxFiles = %d, want %d", c.MaxFiles, DefaultMaxFiles)
	}
	if c, err := parse(t, "--write", "--max-files=5", dir); err != nil || c.MaxFiles != 5 {
		t.Errorf("--max-files=5: %v, %+v", err, c)
	}
	for _, args := range [][]string{
		{"--write", "--max-files=0"},
		{"--write", "--max-files=-1"},
		{"--max-files=5"}, // without --write
	} {
		if _, err := parse(t, append(args, dir)...); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", args)
		}
	}
}
