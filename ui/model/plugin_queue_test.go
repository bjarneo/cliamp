package model

import (
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func TestPluginQueueAddTrackAppendsAsGiven(t *testing.T) {
	m := Model{playlist: playlist.New(), loadedPlaylist: "saved"}
	m.playlist.Add(playlist.Track{Path: "/a.mp3", Title: "A"})

	spotify := playlist.Track{Path: "spotify:track:69kOkLUCkxIZYexIgSG8rq", Title: "Get Lucky", Artist: "Daft Punk", DurationSecs: 369}
	next, cmd := m.Update(PluginQueueMsg{Op: "add_track", Track: spotify})
	if cmd != nil {
		t.Fatal("add_track returned a command; the track must be queued without resolution")
	}
	m = next.(Model)
	next, _ = m.Update(PluginQueueMsg{Op: "add_track", Track: playlist.Track{Path: "https://example.com/live"}})
	m = next.(Model)

	tracks := m.playlist.Tracks()
	if len(tracks) != 3 {
		t.Fatalf("playlist has %d tracks, want 3", len(tracks))
	}
	if got := tracks[1]; got.Path != spotify.Path || got.Title != spotify.Title || got.Artist != spotify.Artist || got.DurationSecs != 369 || got.Stream {
		t.Errorf("queued track = %+v, want %+v", got, spotify)
	}
	if !tracks[2].Stream {
		t.Error("an http URL queued without stream = true should still be a stream, as with IPC track.queue")
	}
	if m.loadedPlaylist != "" {
		t.Errorf("loadedPlaylist = %q, want cleared after a plugin changed the queue", m.loadedPlaylist)
	}
}
