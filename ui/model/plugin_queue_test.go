package model

import (
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func TestPluginQueueAddTrackAppendsAsGiven(t *testing.T) {
	m := Model{player: &playbackFakeEngine{}, playlist: playlist.New(), loadedPlaylist: "saved"}
	m.playlist.Add(playlist.Track{Path: "/a.mp3", Title: "A"})

	spotify := playlist.Track{Path: "spotify:track:69kOkLUCkxIZYexIgSG8rq", Title: "Get Lucky", Artist: "Daft Punk", DurationSecs: 369}
	next, _ := m.Update(PluginQueueMsg{Op: "add_track", Track: spotify})
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

// With repeat-all on the last track, the gapless preload has already armed the
// first track. A plugin append makes the new track next, so the preload must be
// re-armed for it, for both queue.add forms.
func TestPluginQueueAddRearmsGaplessPreload(t *testing.T) {
	for name, msg := range map[string]any{
		"table":  PluginQueueMsg{Op: "add_track", Track: playlist.Track{Path: "c.mp3", DurationSecs: 180}},
		"string": pluginQueueAddedMsg{tracks: []playlist.Track{{Path: "c.mp3", DurationSecs: 180}}},
	} {
		t.Run(name, func(t *testing.T) {
			player := &playbackFakeEngine{playing: true, hasPreload: true}
			p := playlist.New()
			p.Replace([]playlist.Track{
				{Title: "A", Path: "a.mp3", DurationSecs: 180},
				{Title: "B", Path: "b.mp3", DurationSecs: 180},
			})
			p.SetRepeat(playlist.RepeatAll)
			p.SetIndex(1)
			m := Model{player: player, playlist: p}

			_, cmd := m.Update(msg)
			if player.clearPreloadCalls != 1 {
				t.Fatalf("ClearPreload calls = %d, want 1 (a.mp3 was armed)", player.clearPreloadCalls)
			}
			if cmd == nil {
				t.Fatal("Update returned no preload command")
			}
			cmd()
			if len(player.preloadCalls) != 1 || player.preloadCalls[0] != "c.mp3" {
				t.Fatalf("preloadCalls = %v, want [c.mp3]", player.preloadCalls)
			}
		})
	}
}
