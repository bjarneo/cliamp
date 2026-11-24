package model

import (
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
