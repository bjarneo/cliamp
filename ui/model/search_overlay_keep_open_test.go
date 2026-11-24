package model

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

func TestSearchOverlayQueueKeysKeepResultsOpen(t *testing.T) {
	for _, tt := range []struct {
		name     string
		key      tea.KeyPressMsg
		keepOpen bool
		wantOpen bool
	}{
		{"a closes the results", tea.KeyPressMsg{Code: 'a', Text: "a"}, false, false},
		{"q closes the results", tea.KeyPressMsg{Code: 'q', Text: "q"}, false, false},
		{"a keeps the results open", tea.KeyPressMsg{Code: 'a', Text: "a"}, true, true},
		{"q keeps the results open", tea.KeyPressMsg{Code: 'q', Text: "q"}, true, true},
		{"enter still closes them", tea.KeyPressMsg{Code: tea.KeyEnter}, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			m.player = &playbackFakeEngine{playing: true}
			m.playlist.Add(playlist.Track{Title: "Playing"})
			m.SetKeepSearchOpen(tt.keepOpen)
			m.searchOverlay = searchOverlayState{
				visible: true,
				screen:  searchOverlayResults,
				query:   "nofx",
				results: []playlist.Track{trackResult("First"), trackResult("Second")},
				cursor:  1,
			}

			m.handleSearchOverlayResultsKey(tt.key)

			if got := m.searchOverlay.visible; got != tt.wantOpen {
				t.Fatalf("results open = %v, want %v", got, tt.wantOpen)
			}
			if tt.wantOpen && (m.searchOverlay.query != "nofx" || m.searchOverlay.cursor != 1 || len(m.searchOverlay.results) != 2) {
				t.Fatalf("search state changed: query %q, cursor %d, %d results",
					m.searchOverlay.query, m.searchOverlay.cursor, len(m.searchOverlay.results))
			}
			if got := m.playlist.Len(); got != 2 {
				t.Fatalf("playlist length = %d, want 2", got)
			}
		})
	}
}

func TestSearchOverlayAlbumQueueKeepsResultsOpen(t *testing.T) {
	for _, tt := range []struct {
		name     string
		action   searchOverlayAlbumAction
		keepOpen bool
		wantOpen bool
	}{
		{"append closes the results", searchOverlayAlbumAppend, false, false},
		{"append keeps the results open", searchOverlayAlbumAppend, true, true},
		{"queue next keeps the results open", searchOverlayAlbumQueueNext, true, true},
		{"play still closes them", searchOverlayAlbumPlay, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			m.player = &playbackFakeEngine{playing: true}
			m.playlist.Add(playlist.Track{Title: "Playing"})
			m.SetKeepSearchOpen(tt.keepOpen)
			m.searchOverlay = searchOverlayState{
				visible:      true,
				screen:       searchOverlayResults,
				results:      []playlist.Track{albumResult("Album")},
				albumLoading: true,
			}
			const gen = 3
			m.requests.searchOverlayAlbum = gen

			m.handleSearchOverlayAlbumTracks(searchOverlayAlbumTracksMsg{
				gen:    gen,
				action: tt.action,
				album:  albumResult("Album"),
				tracks: []playlist.Track{{Title: "One"}, {Title: "Two"}},
			})

			if got := m.searchOverlay.visible; got != tt.wantOpen {
				t.Fatalf("results open = %v, want %v", got, tt.wantOpen)
			}
			if tt.wantOpen && m.searchOverlay.albumLoading {
				t.Fatal("album still loading after its tracks arrived")
			}
			if got := m.playlist.Len(); got != 3 {
				t.Fatalf("playlist length = %d, want 3", got)
			}
		})
	}
}

func TestNetSearchQueueKeysKeepResultsOpen(t *testing.T) {
	for _, tt := range []struct {
		name     string
		key      tea.KeyPressMsg
		keepOpen bool
		wantOpen bool
	}{
		{"a closes the results", tea.KeyPressMsg{Code: 'a', Text: "a"}, false, false},
		{"a keeps the results open", tea.KeyPressMsg{Code: 'a', Text: "a"}, true, true},
		{"q keeps the results open", tea.KeyPressMsg{Code: 'q', Text: "q"}, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			m.player = &playbackFakeEngine{playing: true}
			m.playlist.Add(playlist.Track{Title: "Playing"})
			m.SetKeepSearchOpen(tt.keepOpen)
			m.netSearch = netSearchState{
				active:  true,
				screen:  netSearchResults,
				query:   "nofx",
				results: []playlist.Track{trackResult("First"), trackResult("Second")},
				cursor:  1,
			}

			m.handleNetSearchResultsKey(tt.key)

			if got := m.netSearch.active; got != tt.wantOpen {
				t.Fatalf("results open = %v, want %v", got, tt.wantOpen)
			}
			if tt.wantOpen && (m.netSearch.query != "nofx" || m.netSearch.cursor != 1) {
				t.Fatalf("search state changed: query %q, cursor %d", m.netSearch.query, m.netSearch.cursor)
			}
			if got := m.playlist.Len(); got != 2 {
				t.Fatalf("playlist length = %d, want 2", got)
			}
		})
	}
}

// A track added while another one is still starting (buffering, so the player
// is not playing yet) must not replace the start the user asked for first.
func TestAddedTrackWaitsForPendingStart(t *testing.T) {
	for _, tt := range []struct {
		name string
		add  func(*Model) tea.Cmd
	}{
		{"append", func(m *Model) tea.Cmd { return m.appendTrack(playlist.Track{Title: "Second"}) }},
		{"queue next", func(m *Model) tea.Cmd { return m.queueTrackNext(playlist.Track{Title: "Second"}) }},
		{"append album", func(m *Model) tea.Cmd {
			return m.appendAlbum(albumResult("Album"), []playlist.Track{{Title: "A1"}, {Title: "A2"}})
		}},
		{"queue album next", func(m *Model) tea.Cmd {
			return m.queueAlbumNext(albumResult("Album"), []playlist.Track{{Title: "A1"}, {Title: "A2"}})
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			m.player = &playbackFakeEngine{}
			m.playlist.Add(playlist.Track{Title: "First"})
			m.playlist.SetIndex(0)
			m.buffering = true

			if cmd := tt.add(&m); cmd != nil {
				t.Fatal("adding a track during a pending start returned a command")
			}
			if got := m.playlist.Index(); got != 0 {
				t.Fatalf("playlist index = %d, want 0 (the track that is starting)", got)
			}
			if !m.buffering {
				t.Fatal("the pending start was dropped")
			}
		})
	}
}

func TestNewTrackStartClearsSupersededBuffering(t *testing.T) {
	for _, tt := range []struct {
		name    string
		playErr error
	}{
		{"start succeeds", nil},
		{"start fails", errors.New("connection refused")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			player := &playbackFakeEngine{playErr: tt.playErr}
			m.player = player
			m.playlist.Add(playlist.Track{Title: "Pending", Path: "https://example.com/live", Stream: true})
			m.playlist.SetIndex(0)
			m.buffering = true

			// A plain http(s) file starts right away, not in a command.
			m.playTrack(playlist.Track{Title: "Next", Path: "https://example.com/next.mp3"})

			if m.buffering {
				t.Fatal("buffering is still set after a new track replaced the pending stream")
			}
			if cmd := m.appendTrack(playlist.Track{Title: "Added", Path: "/added.mp3"}); tt.playErr != nil && cmd == nil {
				t.Fatal("a track added after the failed start did not start")
			}
		})
	}
}
