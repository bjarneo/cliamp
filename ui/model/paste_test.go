package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
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
				if m.keymap.filter != "ctrl" {
					t.Fatalf("keymap.filter = %q, want %q", m.keymap.filter, "ctrl")
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
			model:   Model{jump: jumpState{active: true, input: "1:"}},
			content: "30",
			check: func(t *testing.T, m *Model) {
				if m.jump.input != "1:30" {
					t.Fatalf("jump.input = %q, want %q", m.jump.input, "1:30")
				}
			},
		},
		{
			name:    "url input",
			model:   Model{urlInput: urlInputState{active: true}},
			content: "https://example.com/song.mp3",
			check: func(t *testing.T, m *Model) {
				if m.urlInput.input != "https://example.com/song.mp3" {
					t.Fatalf("urlInput = %q, want %q", m.urlInput.input, "https://example.com/song.mp3")
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
			name: "provider search input",
			model: Model{searchOverlay: searchOverlayState{
				visible: true,
				screen:  searchOverlayInput,
			}},
			content: "arctic monkeys",
			check: func(t *testing.T, m *Model) {
				if m.searchOverlay.query != "arctic monkeys" {
					t.Fatalf("searchOverlay.query = %q, want %q", m.searchOverlay.query, "arctic monkeys")
				}
			},
		},
		{
			name: "spotify new name",
			model: Model{searchOverlay: searchOverlayState{
				visible: true,
				screen:  searchOverlayNewName,
			}},
			content: "New Playlist",
			check: func(t *testing.T, m *Model) {
				if m.searchOverlay.newName != "New Playlist" {
					t.Fatalf("searchOverlay.newName = %q, want %q", m.searchOverlay.newName, "New Playlist")
				}
			},
		},
		{
			name:    "provider search (non-catalog)",
			model:   Model{focus: focusProvider, provSearch: provSearchState{active: true, query: "rock"}},
			content: " ballads",
			check: func(t *testing.T, m *Model) {
				if m.provSearch.query != "rock ballads" {
					t.Fatalf("provSearch.query = %q, want %q", m.provSearch.query, "rock ballads")
				}
			},
		},
		{
			name: "subscriptions filter",
			model: Model{subs: subsOverlay{
				visible:   true,
				filtering: true,
				filter:    "dead ",
				shows:     []provider.SubscriptionInfo{{Name: "Dead Drop"}, {Name: "Part Of The Problem"}},
			}},
			content: "drop",
			check: func(t *testing.T, m *Model) {
				if m.subs.filter != "dead drop" {
					t.Fatalf("subs.filter = %q, want %q", m.subs.filter, "dead drop")
				}
				if len(m.subs.filtered) != 1 || m.subs.filtered[0] != 0 {
					t.Fatalf("subs.filtered = %v, want [0]", m.subs.filtered)
				}
			},
		},
		{
			name:    "theme picker filter",
			model:   Model{themePicker: themePickerState{visible: true, filterList: filterList{filtering: true}}},
			content: "dark",
			check: func(t *testing.T, m *Model) {
				if m.themePicker.filter != "dark" {
					t.Fatalf("themePicker.filter = %q, want %q", m.themePicker.filter, "dark")
				}
			},
		},
		{
			name:    "theme picker without filter drops the paste",
			model:   Model{themePicker: themePickerState{visible: true}, search: searchState{active: true}},
			content: "dark",
			check: func(t *testing.T, m *Model) {
				if m.themePicker.filter != "" || m.search.query != "" {
					t.Fatalf("filter = %q, search = %q, want both empty", m.themePicker.filter, m.search.query)
				}
			},
		},
		{
			name:    "visualizer picker filter",
			model:   Model{visPicker: visPickerState{visible: true, filterList: filterList{filtering: true}}},
			content: "bars",
			check: func(t *testing.T, m *Model) {
				if m.visPicker.filter != "bars" {
					t.Fatalf("visPicker.filter = %q, want %q", m.visPicker.filter, "bars")
				}
			},
		},
		{
			name:    "playlist picker name",
			model:   Model{plPicker: playlistPickerState{visible: true, screen: plPickerNewName, inputErr: "empty"}},
			content: "Mix",
			check: func(t *testing.T, m *Model) {
				if m.plPicker.newName != "Mix" || m.plPicker.inputErr != "" {
					t.Fatalf("plPicker newName = %q, inputErr = %q, want %q and empty", m.plPicker.newName, m.plPicker.inputErr, "Mix")
				}
			},
		},
		{
			name:    "file browser search",
			model:   Model{fileBrowser: fileBrowserState{visible: true, filterList: filterList{filtering: true}}},
			content: "flac",
			check: func(t *testing.T, m *Model) {
				if m.fileBrowser.filter != "flac" {
					t.Fatalf("fileBrowser.filter = %q, want %q", m.fileBrowser.filter, "flac")
				}
			},
		},
		{
			name:    "playlist manager rename",
			model:   Model{plManager: plManagerState{visible: true, screen: plMgrScreenRename}},
			content: "Road",
			check: func(t *testing.T, m *Model) {
				if m.plManager.renameName != "Road" {
					t.Fatalf("plManager.renameName = %q, want %q", m.plManager.renameName, "Road")
				}
			},
		},
		{
			name:    "playlist manager filter",
			model:   Model{plManager: plManagerState{visible: true, filtering: true}},
			content: "jazz",
			check: func(t *testing.T, m *Model) {
				if m.plManager.filter != "jazz" {
					t.Fatalf("plManager.filter = %q, want %q", m.plManager.filter, "jazz")
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
	// When multiple input states are active, the top overlay wins. The
	// YouTube search opens over the nav browser, so it gets the paste.
	m := Model{
		navBrowser: navBrowserState{
			visible:   true,
			mode:      navBrowseModeByAlbum,
			searching: true,
		},
		netSearch: netSearchState{active: true},
	}

	m.handlePaste("test")

	if m.netSearch.query != "test" {
		t.Fatalf("netSearch.query = %q, want %q", m.netSearch.query, "test")
	}
	if m.navBrowser.search != "" {
		t.Fatalf("navBrowser.search = %q, want empty (lower overlay)", m.navBrowser.search)
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

// A load that moves the focus to the playlist leaves the provider filter
// open but hidden. Keys and pastes then go to the playlist, as the help line
// says, and not into the hidden filter.
func TestProviderFilterTakesInputOnlyWithFocus(t *testing.T) {
	tracks := []playlist.Track{{Title: "A", Path: "a.mp3"}, {Title: "B", Path: "b.mp3"}}
	tests := []struct {
		name string
		load func(m *Model)
	}{
		{name: "file browser result", load: func(m *Model) {
			m.handleFBTracksResolved(fbTracksResolvedMsg{tracks: tracks})
		}},
		{name: "provider tracks", load: func(m *Model) {
			m.handleTracksLoaded(tracksLoadedMsg{tracks: tracks, providerName: m.provider.Name(), gen: m.requests.tracks})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			m.player = &playbackFakeEngine{playing: true}
			m.focus = focusProvider
			m.provSearch.active = true

			tt.load(&m)
			if m.focus != focusPlaylist {
				t.Fatalf("focus = %v after the load, want the playlist", m.focus)
			}
			if _, screen := m.commandContext(); screen != "Playlist" {
				t.Fatalf("help context = %q, want Playlist", screen)
			}
			m.plCursor = 0
			m.handleKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
			m.handlePaste("abc")

			if m.provSearch.query != "" {
				t.Fatalf("hidden filter query = %q, want no input", m.provSearch.query)
			}
			if m.plCursor != 1 {
				t.Fatalf("playlist cursor = %d, want j to move it to 1", m.plCursor)
			}
		})
	}
}

// A resize fits the scroll of what shows, not of the provider filter that an
// overlay or another focus hides.
func TestResizeClampsTheVisibleListOverTheProviderFilter(t *testing.T) {
	tests := []struct {
		name  string
		setup func(m *Model)
		check func(t *testing.T, m *Model)
	}{
		{
			name: "keymap over the filter",
			setup: func(m *Model) {
				m.focus = focusProvider
				m.openKeymap()
				m.keymap.cursor, m.keymap.scroll = 1000, 1000
			},
			check: func(t *testing.T, m *Model) {
				if n := m.keymapCount(); m.keymap.cursor >= n || m.keymap.scroll >= n {
					t.Fatalf("keymap cursor %d scroll %d, want both inside %d entries", m.keymap.cursor, m.keymap.scroll, n)
				}
			},
		},
		{
			name: "playlist after a load",
			setup: func(m *Model) {
				m.focus = focusPlaylist
				m.plScroll = 50
			},
			check: func(t *testing.T, m *Model) {
				if m.plScroll != 0 {
					t.Fatalf("plScroll = %d, want 0 for a two-track playlist", m.plScroll)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			m.playlist.Add(playlist.Track{Title: "A", Path: "a.mp3"}, playlist.Track{Title: "B", Path: "b.mp3"})
			m.provSearch.active = true
			tt.setup(&m)

			updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
			m = updated.(Model)

			tt.check(t, &m)
		})
	}
}
