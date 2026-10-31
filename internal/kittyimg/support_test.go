package kittyimg

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestSupportsGraphics(t *testing.T) {
	tests := []struct {
		name    string
		term    string
		program string
		want    bool
	}{
		{name: "kitty term", term: "xterm-kitty", want: true},
		{name: "ghostty term", term: "xterm-ghostty", want: true},
		{name: "wezterm program", term: "xterm-256color", program: "WezTerm", want: true},
		{name: "plain terminal", term: "xterm-256color", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TERM", tt.term)
			t.Setenv("TERM_PROGRAM", tt.program)
			if got := SupportsGraphics(); got != tt.want {
				t.Fatalf("SupportsGraphics() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoadFileURLUnescapesPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "album art")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	path := filepath.Join(dir, "cover image.png")
	want := []byte("image bytes")
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := load((&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
	if err != nil {
		t.Fatalf("load file URL: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("load = %q, want %q", got, want)
	}
}

func TestFileURLPathRejectsRemoteHost(t *testing.T) {
	if _, err := fileURLPath("file://server/share/cover.png"); err == nil {
		t.Fatal("fileURLPath accepted a remote host")
	}
}

func TestPlacementBlockIsStable(t *testing.T) {
	p := Placement{ID: 1001, Cols: 4, Rows: 2}
	first := p.Block()
	second := p.Block()
	if first != second {
		t.Fatal("Placement.Block changed without a placement change")
	}
}
