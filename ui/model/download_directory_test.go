package model

import (
	"os"
	"path/filepath"
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
				cmd := m.handleIPCSave(ipc.SaveRequestMsg{Reply: reply})
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
