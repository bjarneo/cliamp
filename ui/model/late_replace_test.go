package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
)

// lateReplaceSaved serves every saved playlist as x.mp3 and y.mp3.
type lateReplaceSaved struct{}

func (lateReplaceSaved) Name() string                                { return "Local" }
func (lateReplaceSaved) Playlists() ([]playlist.PlaylistInfo, error) { return nil, nil }
func (lateReplaceSaved) Tracks(string) ([]playlist.Track, error) {
	return []playlist.Track{{Title: "X", Path: "x.mp3"}, {Title: "Y", Path: "y.mp3"}}, nil
}

func lateReplaceModel() Model {
	p := playlist.New()
	p.Replace([]playlist.Track{{Title: "A", Path: "a.mp3"}})
	return Model{
		player:        &playbackFakeEngine{},
		playlist:      p,
		localProvider: lateReplaceSaved{},
		focus:         focusPlaylist,
		configSaver:   &recordingConfigSaver{},
	}
}

func queuePaths(m Model) string {
	var out []string
	for _, t := range m.playlist.Tracks() {
		out = append(out, filepath.Base(t.Path))
	}
	return strings.Join(out, " ")
}

// A replacement that resolves in the background is dropped when another queue
// replaced the queue first; without one in between, it applies.
func TestLateQueueReplacementIsDropped(t *testing.T) {
	loadMine := ipc.LoadMsg{Playlist: "mine"}

	fileBrowserR := func(t *testing.T) (Model, tea.Msg) {
		dir := t.TempDir()
		song := filepath.Join(dir, "song.mp3")
		if err := os.WriteFile(song, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		m := lateReplaceModel()
		m.fileBrowser.entries = []fbEntry{{name: "song.mp3", path: song}}
		m.fileBrowser.selected = map[string]bool{song: true}
		cmd := m.fbConfirm(true)
		return m, cmd()
	}
	feed := func(t *testing.T) (Model, tea.Msg) {
		m := lateReplaceModel()
		m.playTrack(playlist.Track{Path: "https://example.com/feed.xml", Feed: true})
		return m, feedTrackResolvedMsg{tracks: []playlist.Track{{Title: "Episode", Path: "episode.mp3"}}, id: m.requests.queueReplace}
	}

	for _, tc := range []struct {
		name  string
		start func(*testing.T) (Model, tea.Msg)
		want  string
	}{
		{name: "file browser R", start: fileBrowserR, want: "song.mp3"},
		{name: "feed", start: feed, want: "episode.mp3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, late := tc.start(t)
			next, _ := m.Update(late)
			if got := queuePaths(next.(Model)); got != tc.want {
				t.Fatalf("with nothing in between, queue = %q, want %q", got, tc.want)
			}

			m, late = tc.start(t)
			next, _ = m.Update(loadMine)
			next, _ = next.(Model).Update(late)
			if got := queuePaths(next.(Model)); got != "x.mp3 y.mp3" {
				t.Fatalf("after loading a playlist first, queue = %q, want x.mp3 y.mp3", got)
			}
		})
	}
}

// Of two background replacements of the same kind, the newer wins whichever
// arrives first.
func TestNewerQueueReplacementWins(t *testing.T) {
	fileBrowserR := func(t *testing.T) (Model, tea.Msg, tea.Msg) {
		dir := t.TempDir()
		m := lateReplaceModel()
		var answers []tea.Msg
		for _, name := range []string{"old.mp3", "new.mp3"} {
			song := filepath.Join(dir, name)
			if err := os.WriteFile(song, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			m.fileBrowser.entries = []fbEntry{{name: name, path: song}}
			m.fileBrowser.selected = map[string]bool{song: true}
			answers = append(answers, m.fbConfirm(true)())
		}
		return m, answers[0], answers[1]
	}
	feed := func(*testing.T) (Model, tea.Msg, tea.Msg) {
		m := lateReplaceModel()
		m.playTrack(playlist.Track{Path: "https://example.com/old.xml", Feed: true})
		old := feedTrackResolvedMsg{tracks: []playlist.Track{{Path: "old.mp3"}}, id: m.requests.queueReplace}
		m.playTrack(playlist.Track{Path: "https://example.com/new.xml", Feed: true})
		newer := feedTrackResolvedMsg{tracks: []playlist.Track{{Path: "new.mp3"}}, id: m.requests.queueReplace}
		return m, old, newer
	}

	for name, start := range map[string]func(*testing.T) (Model, tea.Msg, tea.Msg){"file browser R": fileBrowserR, "feed": feed} {
		for _, olderFirst := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s, older first %v", name, olderFirst), func(t *testing.T) {
				m, old, newer := start(t)
				first, second := old, newer
				if !olderFirst {
					first, second = newer, old
				}
				next, _ := m.Update(first)
				next, _ = next.(Model).Update(second)
				if got := queuePaths(next.(Model)); got != "new.mp3" {
					t.Fatalf("queue = %q, want new.mp3", got)
				}
			})
		}
	}
}
