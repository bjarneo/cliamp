package tracksave

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func TestSaveToCopiesTemporaryDownload(t *testing.T) {
	home := setTestHome(t)
	source, err := os.CreateTemp("", "cliamp-save-*.flac")
	if err != nil {
		t.Fatal(err)
	}
	sourcePath := source.Name()
	t.Cleanup(func() { _ = os.Remove(sourcePath) })
	if _, err := source.WriteString("audio"); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}

	destination, err := SaveTo(context.Background(), playlist.Track{Path: sourcePath, Title: "Song", Artist: "Artist"}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "Music", "cliamp", "Artist - Song.flac")
	if destination != want {
		t.Fatalf("destination = %q, want %q", destination, want)
	}
	if data, err := os.ReadFile(destination); err != nil || string(data) != "audio" {
		t.Fatalf("saved data = %q, err=%v", data, err)
	}
}

func TestSaveToRejectsUserLibraryFile(t *testing.T) {
	setTestHome(t)
	if _, err := SaveTo(context.Background(), playlist.Track{Path: "/var/lib/music/song.flac"}, ""); err == nil {
		t.Fatal("SaveTo accepted a non-temporary library file")
	}
}

func setTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestDirectory(t *testing.T) {
	home := setTestHome(t)
	got, err := Directory("")
	if err != nil || got != filepath.Join(home, "Music", "cliamp") {
		t.Fatalf("directory=%q err=%v", got, err)
	}
	custom := t.TempDir()
	got, err = Directory(custom)
	if err != nil || got != custom {
		t.Fatalf("directory=%q err=%v", got, err)
	}
}

func TestDirectoryRejectsRelativePaths(t *testing.T) {
	for _, directory := range []string{"Music", ".", "..", "../Music", "~/Music"} {
		t.Run(directory, func(t *testing.T) {
			got, err := Directory(directory)
			if err == nil || got != "" {
				t.Fatalf("Directory(%q) = %q, %v; want empty path and error", directory, got, err)
			}
		})
	}
}

func TestNeedsDownload(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"https://www.youtube.com/watch?v=abc", true},
		{"https://youtu.be/abc", true},
		{"https://soundcloud.com/artist/track", true},
		{"ytsearch1:lofi", true},
		{"https://radio.example/live.mp3", false},
		{"/tmp/cliamp-download/track.m4a", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := NeedsDownload(playlist.Track{Path: tt.path}); got != tt.want {
				t.Errorf("NeedsDownload(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// failingReader fails every read, standing in for a download that breaks or
// must not be read at all.
type failingReader struct{ read *bool }

func (r failingReader) Read([]byte) (int, error) {
	*r.read = true
	return 0, errors.New("connection reset")
}

func TestSaveFrom(t *testing.T) {
	dir := t.TempDir()
	track := playlist.Track{Path: "plex:track:1", Title: "Windowpane", Artist: "Opeth", Stream: true}
	open := func(body io.Reader) Opener {
		return func(context.Context, playlist.Track) (io.ReadCloser, string, error) {
			return io.NopCloser(body), ".flac", nil
		}
	}
	want := filepath.Join(dir, "Opeth - Windowpane.flac")

	path, existed, err := SaveFrom(t.Context(), track, dir, open(strings.NewReader("audio")))
	if err != nil || existed || path != want {
		t.Fatalf("SaveFrom = %q, %v, %v; want %q saved", path, existed, err, want)
	}
	if got, _ := os.ReadFile(want); string(got) != "audio" {
		t.Fatalf("saved %q, want audio", got)
	}

	// A file saved before stays as it is, and the download is not read.
	var read bool
	path, existed, err = SaveFrom(t.Context(), track, dir, open(failingReader{&read}))
	if err != nil || !existed || path != want || read {
		t.Fatalf("SaveFrom again = %q, %v, %v (read %v); want the existing file, nothing read", path, existed, err, read)
	}

	// A broken download leaves neither the track nor a temporary file.
	other := playlist.Track{Path: "plex:track:2", Title: "Death Whispered a Lullaby", Artist: "Opeth"}
	if _, _, err := SaveFrom(t.Context(), other, dir, open(failingReader{&read})); err == nil {
		t.Fatal("SaveFrom of a broken download succeeded")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("directory holds %d files after a broken download, want only the first track", len(entries))
	}

	// An open that fails reports its error.
	failed := func(context.Context, playlist.Track) (io.ReadCloser, string, error) {
		return nil, "", errors.New("offline")
	}
	if _, _, err := SaveFrom(t.Context(), other, dir, failed); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("SaveFrom with a failing open = %v, want offline", err)
	}
}
