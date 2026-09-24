package safepath

import (
	"errors"
	"runtime"
	"testing"
)

func TestClean(t *testing.T) {
	windows := runtime.GOOS == "windows"
	tests := []struct {
		in      string
		want    string
		wantErr error
		skip    bool
	}{
		{in: "", want: "."},
		{in: ".", want: "."},
		{in: "a", want: "a"},
		{in: "a/b/c.txt", want: "a/b/c.txt"},
		{in: "a//b/./c", want: "a/b/c"},
		{in: "a/b/", want: "a/b"},
		{in: "photos 2024/é.jpg", want: "photos 2024/é.jpg"},
		{in: "..hidden", want: "..hidden"},

		{in: "..", wantErr: ErrEscape},
		{in: "../x", wantErr: ErrEscape},
		{in: "a/../../x", wantErr: ErrEscape},
		{in: "a/..", wantErr: ErrEscape},
		{in: `..\x`, wantErr: ErrEscape},
		{in: `a\..\..\x`, wantErr: ErrEscape},
		{in: "/etc/passwd", wantErr: ErrEscape},
		{in: "/", wantErr: ErrEscape},
		{in: `\\server\share`, wantErr: ErrEscape},
		{in: `C:\Windows`, wantErr: ErrEscape, skip: !windows},
		{in: "C:foo", wantErr: ErrEscape, skip: !windows},

		{in: "a\x00b", wantErr: ErrInvalid},
		{in: `a\b`, wantErr: ErrInvalid},
		{in: "file.txt:stream", wantErr: ErrInvalid, skip: !windows},
		{in: "a/CON", wantErr: ErrInvalid, skip: !windows},
		{in: "nul.txt", wantErr: ErrInvalid, skip: !windows},
		{in: "COM1.log", wantErr: ErrInvalid, skip: !windows},
		{in: "lpt².x", wantErr: ErrInvalid, skip: !windows},
		{in: "console.txt", want: "console.txt"},
		{in: "com10", want: "com10"},
		{in: "a.txt.", wantErr: ErrInvalid, skip: !windows},
		{in: "a.txt ", wantErr: ErrInvalid, skip: !windows},
		{in: "a?b", wantErr: ErrInvalid, skip: !windows},

		{in: "file.txt:stream", want: "file.txt:stream", skip: windows},
		{in: "a/CON", want: "a/CON", skip: windows},
	}
	for _, tt := range tests {
		if tt.skip {
			continue
		}
		got, err := Clean(tt.in)
		if tt.wantErr != nil {
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Clean(%q) = %q, %v; want error %v", tt.in, got, err, tt.wantErr)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("Clean(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
}

func TestValidName(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "a\x00"} {
		if err := ValidName(name); !errors.Is(err, ErrInvalid) {
			t.Errorf("ValidName(%q) = %v, want ErrInvalid", name, err)
		}
	}
	for _, name := range []string{"a.txt", ".bashrc", "My Song.mp3"} {
		if err := ValidName(name); err != nil {
			t.Errorf("ValidName(%q) = %v, want nil", name, err)
		}
	}
}
