package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
)

func TestSaveUsesConfiguredDirectory(t *testing.T) {
	for _, viaIPC := range []bool{false, true} {
		name := "keyboard"
		if viaIPC {
			name = "ipc"
		}
		t.Run(name, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "source.mp3")
			if err := os.WriteFile(source, []byte("synthetic audio"), 0600); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(t.TempDir(), "downloads")
			tracks := playlist.New()
			tracks.Add(playlist.Track{Path: source, Title: "Song", Artist: "Artist"})
			m := Model{playlist: tracks}
			m.SetDownloadsDirectory(destination)
			if viaIPC {
				reply := make(chan ipc.Response, 1)
				cmd := m.handleIPCSave(ipcSaveRequest{Reply: reply})
				if cmd == nil {
					t.Fatal("no save command")
				}
				cmd()
				if response := <-reply; !response.OK {
					t.Fatal(response.Error)
				}
			} else {
				if cmd := m.saveTrack(); cmd != nil {
					cmd()
				}
			}
			data, err := os.ReadFile(filepath.Join(destination, "Artist - Song.mp3"))
			if err != nil || string(data) != "synthetic audio" {
				t.Fatalf("data=%q err=%v", data, err)
			}
		})
	}
}

// The s key saves through tracksave.SaveTo in a tea.Cmd, so it follows the
// same file name and temp-dir rules as IPC save and never copies on the
// Update goroutine.
func TestSaveTrackKeyUsesTrackSaveRules(t *testing.T) {
	for _, tc := range []struct {
		name       string
		source     string // relative to the test root
		track      playlist.Track
		wantFile   string // relative to the download directory, "" for none
		wantStatus string
	}{
		{
			name:       "titled download",
			source:     "tmp/dl/source.mp3",
			track:      playlist.Track{Title: "Song", Artist: "Artist"},
			wantFile:   "Artist - Song.mp3",
			wantStatus: "Saved to ",
		},
		{
			name:       "empty title uses the file name",
			source:     "tmp/dl/clip.mp3",
			track:      playlist.Track{Artist: "Artist"},
			wantFile:   "Artist - clip.mp3",
			wantStatus: "Saved to ",
		},
		{
			name:       "sibling of the temp dir",
			source:     "tmpx/own.mp3",
			track:      playlist.Track{Title: "Own"},
			wantStatus: "Save failed: only downloaded tracks can be saved",
		},
		{
			name:       "stream",
			source:     "tmp/dl/live.mp3",
			track:      playlist.Track{Title: "Live", Stream: true},
			wantStatus: "Save failed: only downloaded tracks can be saved",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			// os.TempDir reads TMPDIR on Unix and TMP or TEMP on Windows.
			for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
				t.Setenv(name, filepath.Join(root, "tmp"))
			}
			source := filepath.Join(root, tc.source)
			if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source, []byte("synthetic audio"), 0o600); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(root, "downloads")
			track := tc.track
			track.Path = source
			tracks := playlist.New()
			tracks.Add(track)
			m := Model{playlist: tracks}
			m.SetDownloadsDirectory(destination)

			cmd := m.saveTrack()
			if cmd == nil {
				t.Fatal("saveTrack returned no command")
			}
			if entries, _ := os.ReadDir(destination); len(entries) != 0 {
				t.Fatalf("saveTrack wrote %d files before the command ran", len(entries))
			}
			if m.save.pendingDownloads != 0 {
				t.Fatalf("pendingDownloads = %d, want 0 for a file copy", m.save.pendingDownloads)
			}
			next, _ := m.Update(cmd())
			m = next.(Model)

			if !strings.HasPrefix(m.status.text, tc.wantStatus) {
				t.Fatalf("status = %q, want prefix %q", m.status.text, tc.wantStatus)
			}
			entries, _ := os.ReadDir(destination)
			if tc.wantFile == "" {
				if len(entries) != 0 {
					t.Fatalf("saved %v, want no file", entries)
				}
				return
			}
			data, err := os.ReadFile(filepath.Join(destination, tc.wantFile))
			if err != nil || string(data) != "synthetic audio" {
				t.Fatalf("data=%q err=%v", data, err)
			}
		})
	}
}

// saveTrack shows the download activity only for the tracks that
// tracksave.SaveTo downloads with yt-dlp. The command does not run, so no
// yt-dlp process starts.
func TestSaveTrackStartsDownloadForYTDLTracks(t *testing.T) {
	for _, tc := range []struct {
		path string
		want int
	}{
		{path: "https://www.youtube.com/watch?v=abc", want: 1},
		{path: "https://soundcloud.com/artist/track", want: 1},
		{path: "ytsearch1:lofi", want: 1},
		{path: "https://radio.example/live.mp3", want: 0},
		{path: "/music/song.mp3", want: 0},
	} {
		t.Run(tc.path, func(t *testing.T) {
			tracks := playlist.New()
			tracks.Add(playlist.Track{Path: tc.path, Title: "Song"})
			m := Model{playlist: tracks}
			if cmd := m.saveTrack(); cmd == nil {
				t.Fatal("saveTrack returned no command")
			}
			if m.save.pendingDownloads != tc.want {
				t.Errorf("pendingDownloads = %d, want %d", m.save.pendingDownloads, tc.want)
			}
		})
	}
}
