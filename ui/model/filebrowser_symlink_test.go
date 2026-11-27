package model

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileBrowserSymlinkClassification(t *testing.T) {
	base := t.TempDir()
	targetDir := filepath.Join(base, "target-dir")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	targetAudio := filepath.Join(base, "song.mp3")
	if err := os.WriteFile(targetAudio, []byte("mp3"), 0o644); err != nil {
		t.Fatal(err)
	}
	targetText := filepath.Join(base, "notes.txt")
	if err := os.WriteFile(targetText, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	browseDir := filepath.Join(base, "browse")
	if err := os.MkdirAll(browseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mkLink := func(target, name string) {
		if err := os.Symlink(target, filepath.Join(browseDir, name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	mkLink(targetDir, "dir-link")
	mkLink(targetDir, "dir-looks-like.mp3")
	mkLink(targetAudio, "song-link.mp3")
	mkLink(targetText, "notes-link.txt")
	mkLink(filepath.Join(base, "missing"), "broken-link")
	mkLink(filepath.Join(base, "missing.mp3"), "broken-audio.mp3")

	m := &Model{}
	m.fileBrowser.dir = browseDir
	m.loadFBDir()

	byPath := make(map[string]fbEntry, len(m.fileBrowser.entries))
	for _, e := range m.fileBrowser.entries {
		byPath[e.path] = e
	}

	tests := []struct {
		name      string
		linkName  string
		wantName  string
		wantDir   bool
		wantAudio bool
	}{
		{
			name:      "directory symlink gets @ suffix and isDir",
			linkName:  "dir-link",
			wantName:  "dir-link@",
			wantDir:   true,
			wantAudio: false,
		},
		{
			name:      "directory symlink with audio extension stays a directory",
			linkName:  "dir-looks-like.mp3",
			wantName:  "dir-looks-like.mp3@",
			wantDir:   true,
			wantAudio: false,
		},
		{
			name:      "audio file symlink is audio and not a directory",
			linkName:  "song-link.mp3",
			wantName:  "song-link.mp3",
			wantDir:   false,
			wantAudio: true,
		},
		{
			name:      "non-audio file symlink is not classified as directory",
			linkName:  "notes-link.txt",
			wantName:  "notes-link.txt",
			wantDir:   false,
			wantAudio: false,
		},
		{
			name:      "broken symlink is neither directory nor audio",
			linkName:  "broken-link",
			wantName:  "broken-link",
			wantDir:   false,
			wantAudio: false,
		},
		{
			name:      "broken audio symlink is not playable audio",
			linkName:  "broken-audio.mp3",
			wantName:  "broken-audio.mp3",
			wantDir:   false,
			wantAudio: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := byPath[filepath.Join(browseDir, tt.linkName)]
			if !ok {
				t.Fatalf("entry %q missing from file browser listing", tt.linkName)
			}
			if got.name != tt.wantName || got.isDir != tt.wantDir || got.isAudio != tt.wantAudio {
				t.Fatalf("entry %q = {name:%q isDir:%v isAudio:%v}, want {name:%q isDir:%v isAudio:%v}",
					tt.linkName, got.name, got.isDir, got.isAudio, tt.wantName, tt.wantDir, tt.wantAudio)
			}
		})
	}
}
