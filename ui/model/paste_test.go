package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"cliamp/playlist"
)

func TestHandlePasteRoutesToActiveInput(t *testing.T) {
	tests := []struct {
		name    string
		model   Model
		content string
		check   func(t *testing.T, m *Model)
	}{
		{
			name:    "keymap search",
			model:   Model{keymap: keymapOverlay{visible: true}},
			content: "ctrl",
			check: func(t *testing.T, m *Model) {
				if m.keymap.search != "ctrl" {
					t.Fatalf("keymap.search = %q, want %q", m.keymap.search, "ctrl")
				}
			},
		},
		{
			name:    "net search",
			model:   Model{netSearch: netSearchState{active: true, query: "hello "}},
			content: "world",
			check: func(t *testing.T, m *Model) {
				if m.netSearch.query != "hello world" {
					t.Fatalf("netSearch.query = %q, want %q", m.netSearch.query, "hello world")
				}
			},
		},
		{
			name:    "search appends and filters",
			model:   Model{search: searchState{active: true, query: "ja"}, playlist: playlist.New()},
			content: "zz",
			check: func(t *testing.T, m *Model) {
				if m.search.query != "jazz" {
					t.Fatalf("search.query = %q, want %q", m.search.query, "jazz")
				}
			},
		},
		{
			name:    "jump input",
			model:   Model{jumping: true, jumpInput: "1:"},
			content: "30",
			check: func(t *testing.T, m *Model) {
				if m.jumpInput != "1:30" {
					t.Fatalf("jumpInput = %q, want %q", m.jumpInput, "1:30")
				}
			},
		},
		{
			name:    "url input",
			model:   Model{urlInputting: true},
			content: "https://example.com/song.mp3",
			check: func(t *testing.T, m *Model) {
				if m.urlInput != "https://example.com/song.mp3" {
					t.Fatalf("urlInput = %q, want %q", m.urlInput, "https://example.com/song.mp3")
				}
			},
		},
		{
			name: "playlist manager new name",
			model: Model{plManager: plManagerState{
				visible: true,
				screen:  plMgrScreenNewName,
			}},
			content: "My Playlist",
			check: func(t *testing.T, m *Model) {
				if m.plManager.newName != "My Playlist" {
					t.Fatalf("plManager.newName = %q, want %q", m.plManager.newName, "My Playlist")
				}
			},
		},
		{
			name: "spotify search input",
			model: Model{spotSearch: spotSearchState{
				visible: true,
				screen:  spotSearchInput,
			}},
			content: "arctic monkeys",
			check: func(t *testing.T, m *Model) {
				if m.spotSearch.query != "arctic monkeys" {
					t.Fatalf("spotSearch.query = %q, want %q", m.spotSearch.query, "arctic monkeys")
				}
			},
		},
		{
			name: "spotify new name",
			model: Model{spotSearch: spotSearchState{
				visible: true,
				screen:  spotSearchNewName,
			}},
			content: "New Playlist",
			check: func(t *testing.T, m *Model) {
				if m.spotSearch.newName != "New Playlist" {
					t.Fatalf("spotSearch.newName = %q, want %q", m.spotSearch.newName, "New Playlist")
				}
			},
		},
		{
			name:    "provider search (non-catalog)",
			model:   Model{provSearch: provSearchState{active: true, query: "rock"}},
			content: " ballads",
			check: func(t *testing.T, m *Model) {
				if m.provSearch.query != "rock ballads" {
					t.Fatalf("provSearch.query = %q, want %q", m.provSearch.query, "rock ballads")
				}
			},
		},
		{
			name: "nav browser search",
			model: Model{navBrowser: navBrowserState{
				visible:   true,
				mode:      navBrowseModeByAlbum,
				searching: true,
			}},
			content: "album",
			check: func(t *testing.T, m *Model) {
				if m.navBrowser.search != "album" {
					t.Fatalf("navBrowser.search = %q, want %q", m.navBrowser.search, "album")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.model
			if cmd := m.handlePaste(tt.content); cmd != nil {
				t.Fatalf("handlePaste returned non-nil cmd")
			}
			tt.check(t, &m)
		})
	}
}

func TestHandlePasteEmptyContentIsNoop(t *testing.T) {
	m := Model{netSearch: netSearchState{active: true, query: "before"}}

	if cmd := m.handlePaste(""); cmd != nil {
		t.Fatalf("handlePaste(\"\") returned non-nil cmd")
	}
	if m.netSearch.query != "before" {
		t.Fatalf("query changed on empty paste: got %q", m.netSearch.query)
	}
}

func TestHandlePasteNoInputActiveIsNoop(t *testing.T) {
	m := Model{focus: focusPlaylist}

	if cmd := m.handlePaste("ignored text"); cmd != nil {
		t.Fatalf("handlePaste returned non-nil cmd when no input active")
	}
}

func TestHandlePastePriorityOrder(t *testing.T) {
	// When multiple input states are active, the highest-priority one wins.
	// Nav browser search has higher priority than net search.
	m := Model{
		navBrowser: navBrowserState{
			visible:   true,
			mode:      navBrowseModeByAlbum,
			searching: true,
		},
		netSearch: netSearchState{active: true},
	}

	m.handlePaste("test")

	if m.navBrowser.search != "test" {
		t.Fatalf("navBrowser.search = %q, want %q", m.navBrowser.search, "test")
	}
	if m.netSearch.query != "" {
		t.Fatalf("netSearch.query = %q, want empty (lower priority)", m.netSearch.query)
	}
}

func TestUpdateRoutesPasteMsg(t *testing.T) {
	m := Model{netSearch: netSearchState{active: true}}

	next, cmd := m.Update(tea.PasteMsg{Content: "pasted"})
	got := next.(Model)

	if cmd != nil {
		t.Fatalf("Update(PasteMsg) cmd = %v, want nil", cmd)
	}
	if got.netSearch.query != "pasted" {
		t.Fatalf("netSearch.query = %q, want %q", got.netSearch.query, "pasted")
	}
}
