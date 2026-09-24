package extfilter

import (
	"slices"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in      string
		want    Set
		wantErr bool
	}{
		{in: "", want: nil},
		{in: "   ", want: nil},
		{in: " zip,.MP4 ,mp3", want: Set{"mp3", "mp4", "zip"}},
		{in: "zip,ZIP,.zip", want: Set{"zip"}},
		{in: "tar.gz", want: Set{"tar.gz"}},
		{in: "zip,,mp3", wantErr: true},
		{in: "zip,", wantErr: true},
		{in: "../zip", wantErr: true},
		{in: "a/b", wantErr: true},
		{in: `a\b`, wantErr: true},
		{in: "a:b", wantErr: true},
		{in: "..zip", wantErr: true},
		{in: "zip.", wantErr: true},
		{in: "tar..gz", wantErr: true},
	}
	for _, tt := range tests {
		got, err := Parse(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("Parse(%q) = %v, want error", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Parse(%q) error: %v", tt.in, err)
			continue
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("Parse(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestAllows(t *testing.T) {
	set := Set{"mp3", "tar.gz", "zip"}
	tests := []struct {
		name string
		want bool
	}{
		{"a.zip", true},
		{"a.ZIP", true},
		{"Song.Mp3", true},
		{"backup.tar.gz", true},
		{"evil.exe.zip", true},
		{"evil.zip.exe", false},
		{"a.gz", false},
		{"noext", false},
		{".zip", false},
		{"zip", false},
		{"a.zipx", false},
	}
	for _, tt := range tests {
		if got := set.Allows(tt.name); got != tt.want {
			t.Errorf("Allows(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}

	var empty Set
	for _, name := range []string{"anything.exe", "noext", ".bashrc"} {
		if !empty.Allows(name) {
			t.Errorf("empty set should allow %q", name)
		}
	}
}

func TestOutside(t *testing.T) {
	visible := Set{"mp3", "mp4", "zip"}
	if got := (Set{"zip"}).Outside(visible); got != nil {
		t.Errorf("subset: got %v, want nil", got)
	}
	if got := (Set{"exe", "zip"}).Outside(visible); !slices.Equal(got, []string{"exe"}) {
		t.Errorf("got %v, want [exe]", got)
	}
	if got := (Set{"exe"}).Outside(nil); got != nil {
		t.Errorf("empty other allows all: got %v, want nil", got)
	}
}
