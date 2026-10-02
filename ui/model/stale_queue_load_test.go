package model

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

func queuePaths(m Model) []string {
	var paths []string
	for _, track := range m.playlist.Tracks() {
		paths = append(paths, track.Path)
	}
	return paths
}

// A feed that resolves after the listener moved on is dropped. It must not
// replace the queue that the listener loaded, or stop the track that plays.
func TestFeedTrackResolvedDropsStaleResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, ipcPodcastRSS)
	}))
	defer srv.Close()
	feed := playlist.Track{Title: "Podcast", Path: srv.URL + "/feed.xml", Feed: true}
	episodes := []string{"https://example.com/z.mp3", "https://example.com/a.mp3"}

	tests := []struct {
		name      string
		during    func(m *Model)
		wantQueue []string
		wantPlay  string
	}{
		{
			name:      "current result",
			during:    func(*Model) {},
			wantQueue: episodes,
			wantPlay:  episodes[0],
		},
		{
			name:      "page of the queue during the resolve",
			during:    func(m *Model) { m.appendTracks(playlist.Track{Title: "Page", Path: "page.mp3"}) },
			wantQueue: episodes,
			wantPlay:  episodes[0],
		},
		{
			name: "queue replaced during the resolve",
			during: func(m *Model) {
				m.replacePlaylist([]playlist.Track{{Title: "B", Path: "b.mp3"}, {Title: "C", Path: "c.mp3"}})
				m.playCurrentTrack()
			},
			wantQueue: []string{"b.mp3", "c.mp3"},
			wantPlay:  "b.mp3",
		},
		{
			name:      "stop during the resolve",
			during:    func(m *Model) { m.stopByUser() },
			wantQueue: []string{"current.mp3", feed.Path},
		},
		{
			name:      "newer feed during the resolve",
			during:    func(m *Model) { m.playIndex(1) },
			wantQueue: []string{"current.mp3", feed.Path},
			wantPlay:  "current.mp3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := &playbackFakeEngine{}
			pl := playlist.New()
			pl.Add(playlist.Track{Title: "Current", Path: "current.mp3"}, feed)
			m := Model{player: engine, playlist: pl, vis: ui.NewVisualizer(float64(engine.SampleRate()))}
			m.playIndex(0)

			cmd := m.playIndex(1)
			if cmd == nil || !m.feedLoading {
				t.Fatal("playing the feed row did not start a feed resolve")
			}
			tt.during(&m)
			msg, ok := cmd().(feedTrackResolvedMsg)
			if !ok || msg.err != nil {
				t.Fatalf("feed resolve = %#v, want episodes", msg)
			}
			m.handleFeedTrackResolved(msg)

			if got := queuePaths(m); !slices.Equal(got, tt.wantQueue) {
				t.Fatalf("queue = %v, want %v", got, tt.wantQueue)
			}
			if got, _ := m.currentPlaybackTrack(); m.playingTrackActive != (tt.wantPlay != "") || m.playingTrackActive && got.Path != tt.wantPlay {
				t.Fatalf("playing %q active %v, want %q", got.Path, m.playingTrackActive, tt.wantPlay)
			}
			if m.feedLoading {
				t.Fatal("feedLoading still set after the resolve")
			}
		})
	}
}

// A file browser replace that resolves after another queue replaced the
// queue is dropped. A track change in the old queue does not drop it, since
// the replace stops that playback anyway.
func TestFBReplaceDropsStaleResult(t *testing.T) {
	dir := t.TempDir()
	song := filepath.Join(dir, "song.mp3")
	if err := os.WriteFile(song, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		during    func(m *Model)
		wantQueue []string
	}{
		{name: "current result", during: func(*Model) {}, wantQueue: []string{song}},
		{name: "track change during the walk", during: func(m *Model) { m.skipNext() }, wantQueue: []string{song}},
		{
			name:      "queue replaced during the walk",
			during:    func(m *Model) { m.replacePlaylist([]playlist.Track{{Title: "B", Path: "b.mp3"}}) },
			wantQueue: []string{"b.mp3"},
		},
		{
			name: "newer replace during the walk",
			during: func(m *Model) {
				m.fileBrowser.selected = map[string]bool{song: true}
				m.fbConfirm(true)
			},
			wantQueue: []string{"one.mp3", "two.mp3"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := &playbackFakeEngine{}
			pl := playlist.New()
			pl.Add(playlist.Track{Title: "One", Path: "one.mp3"}, playlist.Track{Title: "Two", Path: "two.mp3"})
			m := Model{player: engine, playlist: pl, vis: ui.NewVisualizer(float64(engine.SampleRate()))}
			m.playIndex(0)
			m.fileBrowser = fileBrowserState{
				visible:  true,
				entries:  []fbEntry{{name: "song.mp3", path: song, isAudio: true}},
				selected: map[string]bool{song: true},
			}

			cmd := m.fbConfirm(true)
			if cmd == nil {
				t.Fatal("fbConfirm(true) = nil, want a resolve command")
			}
			tt.during(&m)
			msg, ok := cmd().(fbTracksResolvedMsg)
			if !ok || msg.err != nil {
				t.Fatalf("file browser resolve = %#v, want tracks", msg)
			}
			m.handleFBTracksResolved(msg)

			if got := queuePaths(m); !slices.Equal(got, tt.wantQueue) {
				t.Fatalf("queue = %v, want %v", got, tt.wantQueue)
			}
		})
	}
}
