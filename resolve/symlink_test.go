package resolve

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func createSymlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func TestAudioFilesSymlinks(t *testing.T) {
	base := t.TempDir()

	// Real music library outside the scanned directory.
	extLib := filepath.Join(base, "external")
	extAlbum := filepath.Join(extLib, "Album")
	if err := os.MkdirAll(extAlbum, 0o755); err != nil {
		t.Fatal(err)
	}
	extTrack := filepath.Join(extAlbum, "01.mp3")
	if err := os.WriteFile(extTrack, []byte("mp3"), 0o644); err != nil {
		t.Fatal(err)
	}
	extTop := filepath.Join(extLib, "top.flac")
	if err := os.WriteFile(extTop, []byte("flac"), 0o644); err != nil {
		t.Fatal(err)
	}
	extHiddenDir := filepath.Join(extLib, ".hidden")
	if err := os.MkdirAll(extHiddenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extHiddenDir, "secret.mp3"), []byte("mp3"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Symlinked root directory pointing to external library.
	rootLink := filepath.Join(base, "music-link")
	createSymlinkOrSkip(t, extLib, rootLink)

	// Directory containing symlinked subdir, symlinked audio file, broken symlink,
	// symlink cycle back to parent, diamond duplicate symlink, and hidden symlink.
	scanDir := filepath.Join(base, "scan")
	if err := os.MkdirAll(scanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	directTrack := filepath.Join(scanDir, "direct.ogg")
	if err := os.WriteFile(directTrack, []byte("ogg"), 0o644); err != nil {
		t.Fatal(err)
	}
	createSymlinkOrSkip(t, extAlbum, filepath.Join(scanDir, "linked-album"))
	createSymlinkOrSkip(t, extAlbum, filepath.Join(scanDir, "linked-album-dup"))
	createSymlinkOrSkip(t, extTop, filepath.Join(scanDir, "linked-song.flac"))
	createSymlinkOrSkip(t, filepath.Join(base, "does-not-exist.mp3"), filepath.Join(scanDir, "broken.mp3"))
	createSymlinkOrSkip(t, scanDir, filepath.Join(scanDir, "loop"))
	createSymlinkOrSkip(t, extAlbum, filepath.Join(scanDir, ".hidden-link"))

	// Symlinked directory whose name ends with an audio extension.
	extNamedDir := filepath.Join(base, "dir-target")
	if err := os.MkdirAll(extNamedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extNamedDir, "inside.wav"), []byte("wav"), 0o644); err != nil {
		t.Fatal(err)
	}
	createSymlinkOrSkip(t, extNamedDir, filepath.Join(scanDir, "folder.mp3"))

	tests := []struct {
		name      string
		dir       string
		recursive bool
		want      []string
	}{
		{
			name:      "recursive follows symlinked root directory",
			dir:       rootLink,
			recursive: true,
			want: []string{
				filepath.Join(rootLink, "Album", "01.mp3"),
				filepath.Join(rootLink, "top.flac"),
			},
		},
		{
			name:      "non-recursive reads symlinked root directory",
			dir:       rootLink,
			recursive: false,
			want: []string{
				filepath.Join(rootLink, "top.flac"),
			},
		},
		{
			name:      "recursive follows symlinked subdirs and prevents loops and duplicates",
			dir:       scanDir,
			recursive: true,
			want: []string{
				directTrack,
				filepath.Join(scanDir, "folder.mp3", "inside.wav"),
				filepath.Join(scanDir, "linked-album", "01.mp3"),
				filepath.Join(scanDir, "linked-song.flac"),
			},
		},
		{
			name:      "non-recursive includes symlinked audio files and skips broken links and symlink dirs",
			dir:       scanDir,
			recursive: false,
			want: []string{
				directTrack,
				filepath.Join(scanDir, "linked-song.flac"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AudioFiles(tt.dir, tt.recursive)
			if err != nil {
				t.Fatalf("AudioFiles(%q, %v) error = %v", tt.dir, tt.recursive, err)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("AudioFiles(%q, %v) =\n  %v\nwant:\n  %v", tt.dir, tt.recursive, got, tt.want)
			}
		})
	}
}
