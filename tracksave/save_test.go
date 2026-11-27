package tracksave

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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

func TestSaveToSanitizesFilenameAndGuardsSpecialStems(t *testing.T) {
	tests := []struct {
		name     string
		title    string
		artist   string
		srcBase  string
		wantName string
	}{
		{name: "strips control and NUL chars", title: "So\x00ng\n\r\t", artist: "Art\x1fist", srcBase: "dl.mp3", wantName: "Artist - Song.mp3"},
		{name: "replaces slashes and backslashes", title: `a/b\c:d*e?f"g<h>i|j`, artist: "", srcBase: "dl.mp3", wantName: "a_b_c_d_e_f_g_h_i_j.mp3"},
		{name: "guards dot stem", title: ".", artist: "", srcBase: "dl.flac", wantName: "track.flac"},
		{name: "guards dotdot stem", title: "..", artist: "", srcBase: "dl.flac", wantName: "track.flac"},
		{name: "guards dotdot with NUL", title: ".\x00.", artist: "", srcBase: "dl.flac", wantName: "track.flac"},
		{name: "guards empty stem", title: "\x00\n  ", artist: "\r\t", srcBase: ".mp3", wantName: "track.mp3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			tmpDir := filepath.Join(root, "tmp")
			saveDir := filepath.Join(root, "saved")
			if err := os.MkdirAll(tmpDir, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, env := range []string{"TMPDIR", "TMP", "TEMP"} {
				t.Setenv(env, tmpDir)
			}
			srcPath := filepath.Join(tmpDir, tt.srcBase)
			if err := os.WriteFile(srcPath, []byte("audio-bytes"), 0o600); err != nil {
				t.Fatal(err)
			}

			dest, err := SaveTo(context.Background(), playlist.Track{
				Path:   srcPath,
				Title:  tt.title,
				Artist: tt.artist,
			}, saveDir)
			if err != nil {
				t.Fatalf("SaveTo() error = %v", err)
			}
			want := filepath.Join(saveDir, tt.wantName)
			if dest != want {
				t.Fatalf("dest = %q, want %q", dest, want)
			}
			if filepath.Dir(dest) != saveDir {
				t.Fatalf("dest dir = %q, want %q", filepath.Dir(dest), saveDir)
			}
			data, err := os.ReadFile(dest)
			if err != nil || string(data) != "audio-bytes" {
				t.Fatalf("saved content = %q, err = %v", data, err)
			}
		})
	}
}

func TestSaveToRejectsUnsafeSourcesAndSameFile(t *testing.T) {
	root := t.TempDir()
	tmpDir := filepath.Join(root, "tmp")
	outsideDir := filepath.Join(root, "outside")
	saveDir := filepath.Join(root, "saved")
	for _, d := range []string{tmpDir, outsideDir, saveDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, env := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(env, tmpDir)
	}

	outsideFile := filepath.Join(outsideDir, "secret.flac")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(tmpDir, "subdir.flac")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sameFile := filepath.Join(tmpDir, "Artist - Song.flac")
	if err := os.WriteFile(sameFile, []byte("keep-me"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		setup     func(t *testing.T) (playlist.Track, string)
		wantError string
	}{
		{
			name: "rejects directory inside temp dir",
			setup: func(t *testing.T) (playlist.Track, string) {
				return playlist.Track{Path: subDir, Title: "Dir"}, saveDir
			},
			wantError: "only downloaded tracks can be saved",
		},
		{
			name: "rejects symlink pointing outside temp dir",
			setup: func(t *testing.T) (playlist.Track, string) {
				if runtime.GOOS == "windows" {
					t.Skip("symlink creation requires elevated permissions on Windows")
				}
				link := filepath.Join(tmpDir, "escape.flac")
				if err := os.Symlink(outsideFile, link); err != nil {
					t.Fatal(err)
				}
				return playlist.Track{Path: link, Title: "Escape"}, saveDir
			},
			wantError: "only downloaded tracks can be saved",
		},
		{
			name: "rejects same-file copy",
			setup: func(t *testing.T) (playlist.Track, string) {
				return playlist.Track{Path: sameFile, Title: "Song", Artist: "Artist"}, tmpDir
			},
			wantError: "same file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			track, dir := tt.setup(t)
			dest, err := SaveTo(context.Background(), track, dir)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("SaveTo() = (%q, %v), want error containing %q", dest, err, tt.wantError)
			}
		})
	}

	// Verify SaveTo kept the same-file source intact.
	data, err := os.ReadFile(sameFile)
	if err != nil || string(data) != "keep-me" {
		t.Fatalf("sameFile content = %q, err = %v", data, err)
	}
}

func TestSaveToAvoidsOverwritingExistingFilesAndSymlinks(t *testing.T) {
	root := t.TempDir()
	tmpDir := filepath.Join(root, "tmp")
	saveDir := filepath.Join(root, "saved")
	victimDir := filepath.Join(root, "victim")
	for _, d := range []string{tmpDir, saveDir, victimDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, env := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(env, tmpDir)
	}

	src := filepath.Join(tmpDir, "dl.flac")
	if err := os.WriteFile(src, []byte("new-audio"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Pre-populate an existing regular file at the primary destination.
	existing := filepath.Join(saveDir, "Artist - Song.flac")
	if err := os.WriteFile(existing, []byte("existing-audio"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Also plant a symlink at the first collision slot on POSIX platforms.
	victim := filepath.Join(victimDir, "target.txt")
	if err := os.WriteFile(victim, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantDest := filepath.Join(saveDir, "Artist - Song (1).flac")
	if runtime.GOOS != "windows" {
		if err := os.Symlink(victim, wantDest); err != nil {
			t.Fatal(err)
		}
		wantDest = filepath.Join(saveDir, "Artist - Song (2).flac")
	}

	dest, err := SaveTo(context.Background(), playlist.Track{
		Path:   src,
		Title:  "Song",
		Artist: "Artist",
	}, saveDir)
	if err != nil {
		t.Fatalf("SaveTo() error = %v", err)
	}
	if dest != wantDest {
		t.Fatalf("dest = %q, want %q", dest, wantDest)
	}
	if got, err := os.ReadFile(dest); err != nil || string(got) != "new-audio" {
		t.Fatalf("new file content = %q, err = %v", got, err)
	}
	if got, err := os.ReadFile(existing); err != nil || string(got) != "existing-audio" {
		t.Fatalf("existing file was overwritten: %q, err = %v", got, err)
	}
	if got, err := os.ReadFile(victim); err != nil || string(got) != "untouched" {
		t.Fatalf("symlink target was modified: %q, err = %v", got, err)
	}
}
