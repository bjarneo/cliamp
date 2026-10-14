package model

import (
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/external/radio"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

// shortcutTestModel registers a provider for every Shift+letter shortcut,
// plus navidrome, and starts on a provider that no shortcut names.
func shortcutTestModel() Model {
	m := keybindingTestModel()
	other := commandsTestProvider{name: "Other"}
	m.provider = other
	m.providers = []provider.Entry{{Key: "other", Name: "Other", Provider: other}}
	for _, key := range []string{"navidrome", "spotify", "plex", "jellyfin", "emby", "audiobookshelf",
		"yt", "soundcloud", "mixcloud", "netease", "qobuz", "tidal", "local", "radio", "podcast"} {
		m.providers = append(m.providers, provider.Entry{Key: key, Name: key, Provider: commandsTestProvider{name: key}})
	}
	return m
}

func TestProviderShortcutsSwitchFromEveryFocus(t *testing.T) {
	focuses := []struct {
		name  string
		focus focusArea
	}{
		{"playlist", focusPlaylist},
		{"provider", focusProvider},
		{"equalizer", focusEQ},
		{"volume", focusVolume},
	}
	shortcuts := map[string]string{
		"S": "spotify", "P": "plex", "J": "jellyfin", "E": "emby", "B": "audiobookshelf",
		"Y": "yt", "C": "soundcloud", "X": "mixcloud", "M": "netease", "Q": "qobuz",
		"T": "tidal", "L": "local", "R": "radio", "O": "podcast",
	}
	for _, f := range focuses {
		for key, want := range shortcuts {
			t.Run(f.name+"/"+key, func(t *testing.T) {
				m := shortcutTestModel()
				m.focus = f.focus

				if cmd := m.handleKey(tea.KeyPressMsg{Text: key}); cmd == nil {
					t.Fatal("shortcut returned no command")
				}
				if got := m.provider.Name(); got != want {
					t.Fatalf("provider = %q, want %q", got, want)
				}
				if m.focus != focusProvider {
					t.Fatalf("focus = %v, want the provider pane", m.focus)
				}
			})
		}
		// N browses the provider on screen. It never switches to Navidrome.
		t.Run(f.name+"/N", func(t *testing.T) {
			m := shortcutTestModel()
			m.focus = f.focus

			m.handleKey(tea.KeyPressMsg{Text: "N"})

			if got := m.provider.Name(); got != "Other" {
				t.Fatalf("N switched the provider to %q", got)
			}
		})
	}
}

// quickSwitchProvider takes every key that providerKeyForShortcut maps, so an
// overlay returns after the switch even when the switch has no command. A
// provider that is not configured keeps the overlays open: there is nowhere
// to land, so only the setup hint applies.
func TestQuickSwitchProviderTakesEveryShortcut(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	// A Radio with an active search starts no catalog load, so the switch to
	// it has no command.
	searching := radio.New(radio.Options{Country: radio.CountryDeclined})
	searching.SetSearchResults(nil)
	tests := []struct {
		name         string
		key          string
		drop         string // provider key left unconfigured
		wantOK       bool
		wantCmd      bool
		wantProvider string
		wantOpen     bool // overlays stay open
	}{
		{name: "configured provider", key: "S", wantOK: true, wantCmd: true, wantProvider: "spotify"},
		{name: "N names Navidrome", key: "N", wantOK: true, wantCmd: true, wantProvider: "navidrome"},
		{name: "switch with no command", key: "R", wantOK: true, wantProvider: "Radio"},
		{name: "provider not configured keeps overlays", key: "T", drop: "tidal", wantOK: true, wantProvider: "Other", wantOpen: true},
		{name: "lowercase letter", key: "s", wantProvider: "Other", wantOpen: true},
		{name: "other key", key: "ctrl+f", wantProvider: "Other", wantOpen: true},
		{name: "empty key", key: "", wantProvider: "Other", wantOpen: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := shortcutTestModel()
			m.providers = slices.DeleteFunc(m.providers, func(e provider.Entry) bool { return e.Key == tt.drop })
			for i, e := range m.providers {
				if e.Key == "radio" {
					m.providers[i].Provider = searching
				}
			}
			m.navBrowser.visible = true
			m.plManager.visible = true
			m.fileBrowser.visible = true

			cmd, ok := m.quickSwitchProvider(tt.key)

			if ok != tt.wantOK || (cmd != nil) != tt.wantCmd {
				t.Fatalf("quickSwitchProvider(%q) = command %v, ok %v; want command %v, ok %v", tt.key, cmd != nil, ok, tt.wantCmd, tt.wantOK)
			}
			if got := m.provider.Name(); got != tt.wantProvider {
				t.Errorf("provider = %q, want %q", got, tt.wantProvider)
			}
			if m.navBrowser.visible != tt.wantOpen || m.plManager.visible != tt.wantOpen || m.fileBrowser.visible != tt.wantOpen {
				t.Errorf("overlays visible = %v %v %v, want %v", m.navBrowser.visible, m.plManager.visible, m.fileBrowser.visible, tt.wantOpen)
			}
		})
	}
	t.Run("every shortcut", func(t *testing.T) {
		for r := 'A'; r <= 'Z'; r++ {
			m := shortcutTestModel()
			key := string(r)
			if _, ok := m.quickSwitchProvider(key); ok != (providerKeyForShortcut(key) != "") {
				t.Errorf("quickSwitchProvider(%q) ok = %v", key, ok)
			}
		}
	})
}

// The nav browser and the playlist manager return after a provider shortcut
// and stay closed. The nav menu N and the track screen R keep their results.
func TestOverlayProviderShortcuts(t *testing.T) {
	nav := func(mode navBrowseModeType, screen navBrowseScreenType) func(*Model) {
		return func(m *Model) {
			m.navBrowser = navBrowserState{
				prov: commandsTestProvider{name: "Browse"}, visible: true, mode: mode, screen: screen,
				tracks: []playlist.Track{{Path: "replacement.mp3"}},
			}
		}
	}
	navPrompt := func(m *Model) {
		nav(navBrowseModeByAlbum, navBrowseScreenTracks)(m)
		m.navBrowser.confirmReplace = true
	}
	manager := func(screen plMgrScreenType) func(*Model) {
		return func(m *Model) {
			m.plManager = plManagerState{visible: true, screen: screen, selPlaylist: "music"}
		}
	}
	tests := []struct {
		name         string
		open         func(*Model)
		key          string
		drop         string // provider key left unconfigured
		wantProvider string
		wantOpen     bool
		wantReplace  bool
	}{
		{name: "nav menu N switches to Navidrome", open: nav(navBrowseModeMenu, navBrowseScreenList), key: "N", wantProvider: "navidrome"},
		{name: "nav menu N without Navidrome keeps the browser", open: nav(navBrowseModeMenu, navBrowseScreenList), key: "N", drop: "navidrome", wantProvider: "Other", wantOpen: true},
		{name: "nav album list R switches to Radio", open: nav(navBrowseModeByAlbum, navBrowseScreenList), key: "R", wantProvider: "radio"},
		{name: "nav track screen R asks to replace", open: nav(navBrowseModeByAlbum, navBrowseScreenTracks), key: "R", wantProvider: "Other", wantOpen: true, wantReplace: true},
		{name: "nav S without Spotify keeps the browser", open: nav(navBrowseModeByAlbum, navBrowseScreenList), key: "S", drop: "spotify", wantProvider: "Other", wantOpen: true},
		{name: "nav replace prompt keeps S", open: navPrompt, key: "S", wantProvider: "Other", wantOpen: true, wantReplace: true},
		{name: "nav replace prompt keeps S without Spotify", open: navPrompt, key: "S", drop: "spotify", wantProvider: "Other", wantOpen: true, wantReplace: true},
		{name: "manager list R switches to Radio", open: manager(plMgrScreenList), key: "R", wantProvider: "radio"},
		{name: "manager tracks S without Spotify keeps the manager", open: manager(plMgrScreenTracks), key: "S", drop: "spotify", wantProvider: "Other", wantOpen: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := shortcutTestModel()
			m.playlist.Add(playlist.Track{Path: "existing.mp3"})
			m.providers = slices.DeleteFunc(m.providers, func(e provider.Entry) bool { return e.Key == tt.drop })
			tt.open(&m)

			if m.navBrowser.visible {
				m.handleNavBrowserKey(tea.KeyPressMsg{Text: tt.key})
			} else {
				m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: tt.key})
			}

			if got := m.provider.Name(); got != tt.wantProvider {
				t.Errorf("provider = %q, want %q", got, tt.wantProvider)
			}
			if open := m.navBrowser.visible || m.plManager.visible; open != tt.wantOpen {
				t.Errorf("overlay open = %v, want %v", open, tt.wantOpen)
			}
			if m.navBrowser.confirmReplace != tt.wantReplace {
				t.Errorf("confirmReplace = %v, want %v", m.navBrowser.confirmReplace, tt.wantReplace)
			}
		})
	}
}

// defaultModeTestProvider opens the provider browser on a preferred route,
// so N chooses the browse mode instead of switching to Navidrome.
type defaultModeTestProvider struct{ commandsTestProvider }

func (defaultModeTestProvider) DefaultBrowseMode() provider.BrowseMode { return provider.BrowseAlbums }

// The replace prompt of the provider browser owns the keys until it is
// answered, as the delete prompt of the playlist manager does. Keys that the
// browser handles before its screens leave the prompt and the browser alone.
func TestNavReplacePromptOwnsTheKeys(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyPressMsg
		prov playlist.Provider
	}{
		{name: "expanded view", key: tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}},
		{name: "filter", key: tea.KeyPressMsg{Code: '/', Text: "/"}},
		{name: "provider search", key: tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl}},
		{name: "mode chooser", key: tea.KeyPressMsg{Code: 'N', Text: "N"}, prov: defaultModeTestProvider{commandsTestProvider{name: "Browse"}}},
		{name: "provider shortcut", key: tea.KeyPressMsg{Code: 'T', Text: "T"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := shortcutTestModel()
			m.playlist.Add(playlist.Track{Path: "existing.mp3"})
			prov := tt.prov
			if prov == nil {
				prov = commandsTestProvider{name: "Browse"}
			}
			m.navBrowser = navBrowserState{
				prov: prov, visible: true, mode: navBrowseModeByAlbum, screen: navBrowseScreenTracks,
				tracks: []playlist.Track{{Path: "replacement.mp3"}}, confirmReplace: true,
			}

			m.handleNavBrowserKey(tt.key)

			if !m.navBrowser.visible || !m.navBrowser.confirmReplace || m.navBrowser.mode != navBrowseModeByAlbum {
				t.Fatalf("browser visible %v prompt %v mode %v, want the prompt still open",
					m.navBrowser.visible, m.navBrowser.confirmReplace, m.navBrowser.mode)
			}
			if m.navBrowser.searching || m.heightExpanded || m.searchOverlay.visible || m.netSearch.active {
				t.Fatalf("searching %v expanded %v search overlay %v net search %v, want no change behind the prompt",
					m.navBrowser.searching, m.heightExpanded, m.searchOverlay.visible, m.netSearch.active)
			}
			if got := m.provider.Name(); got != "Other" {
				t.Fatalf("provider = %q, want Other", got)
			}
			if m.status.text == "" {
				t.Error("stray key behind the prompt left no hint; want the valid keys named")
			}
		})
	}
}

// refreshTestProvider counts Refresh calls. Only the playlist IDs in stable
// stay valid across Refresh.
type refreshTestProvider struct {
	commandsTestProvider
	refreshes *int
	stable    map[string]bool
}

func (p refreshTestProvider) Refresh() { *p.refreshes++ }

func (p refreshTestProvider) CanRefreshPlaylist(id string) bool { return p.stable[id] }

var _ playlist.RefreshablePlaylist = refreshTestProvider{}

// refresherOnlyProvider can drop its cache, but no playlist ID stays valid.
type refresherOnlyProvider struct {
	commandsTestProvider
	refreshes *int
}

func (p refresherOnlyProvider) Refresh() { *p.refreshes++ }

func TestCtrlRRefreshesTheActiveProvider(t *testing.T) {
	const (
		none   = iota // no reload starts
		tracks        // the open playlist reloads in place
		lists         // the playlist list reloads
	)
	tests := []struct {
		name       string
		focus      focusArea
		kind       string // "stable", "refresher" or "plain"
		playlistID string
		loading    bool
		want       int
		// wantRefreshes counts Refresh calls on the provider.
		wantRefreshes int
		// wantID is activeProviderPlaylistID after the key.
		wantID string
	}{
		{name: "pane reopens a stable playlist", focus: focusProvider, kind: "stable", playlistID: "wave", want: tracks, wantRefreshes: 1, wantID: "wave"},
		{name: "pane reloads the lists for a positional ID", focus: focusProvider, kind: "stable", playlistID: "station-3", want: lists, wantRefreshes: 1},
		{name: "pane reloads the lists with no open playlist", focus: focusProvider, kind: "stable", want: lists, wantRefreshes: 1},
		{name: "pane refreshes a provider with no stable IDs", focus: focusProvider, kind: "refresher", playlistID: "wave", want: lists, wantRefreshes: 1},
		{name: "pane reloads a provider with no cache", focus: focusProvider, kind: "plain", playlistID: "wave", want: lists},
		{name: "pane waits while the provider loads", focus: focusProvider, kind: "stable", playlistID: "wave", loading: true, want: none, wantID: "wave"},
		{name: "playlist reopens a stable playlist", focus: focusPlaylist, kind: "stable", playlistID: "wave", want: tracks, wantRefreshes: 1, wantID: "wave"},
		{name: "playlist skips a positional ID", focus: focusPlaylist, kind: "stable", playlistID: "station-3", want: none, wantID: "station-3"},
		{name: "playlist skips with no open playlist", focus: focusPlaylist, kind: "stable", want: none},
		{name: "playlist skips a provider with no stable IDs", focus: focusPlaylist, kind: "refresher", playlistID: "wave", want: none, wantID: "wave"},
		{name: "playlist waits while the provider loads", focus: focusPlaylist, kind: "stable", playlistID: "wave", loading: true, want: none, wantID: "wave"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refreshes := 0
			base := commandsTestProvider{name: "Wave"}
			var prov playlist.Provider = base
			switch tt.kind {
			case "stable":
				prov = refreshTestProvider{commandsTestProvider: base, refreshes: &refreshes, stable: map[string]bool{"wave": true}}
			case "refresher":
				prov = refresherOnlyProvider{commandsTestProvider: base, refreshes: &refreshes}
			}
			m := keybindingTestModel()
			m.provider = prov
			m.playlist.Add(playlist.Track{Title: "Song"})
			m.focus = tt.focus
			m.activeProviderPlaylistID = tt.playlistID
			m.provPane.loading = tt.loading
			m.catalogBatch = catalogBatchState{offset: 100, done: true}

			cmd := m.handleKey(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})

			if refreshes != tt.wantRefreshes {
				t.Errorf("Refresh calls = %d, want %d", refreshes, tt.wantRefreshes)
			}
			if m.activeProviderPlaylistID != tt.wantID {
				t.Errorf("activeProviderPlaylistID = %q, want %q", m.activeProviderPlaylistID, tt.wantID)
			}
			if tt.want == none {
				if cmd != nil {
					t.Fatal("ctrl+r started a reload")
				}
				if m.catalogBatch == (catalogBatchState{}) {
					t.Error("ctrl+r reset the catalog pages without a reload")
				}
				return
			}
			if cmd == nil {
				t.Fatal("ctrl+r started no reload")
			}
			if !m.provPane.loading || m.catalogBatch != (catalogBatchState{}) {
				t.Errorf("provPane.loading = %v, catalogBatch = %+v, want a fresh load", m.provPane.loading, m.catalogBatch)
			}
			switch msg := cmd().(type) {
			case tracksLoadedMsg:
				if tt.want != tracks || msg.playlistID != "wave" {
					t.Fatalf("ctrl+r reloaded playlist %q, want kind %d", msg.playlistID, tt.want)
				}
			case playlistsLoadedMsg:
				if tt.want != lists {
					t.Fatal("ctrl+r reloaded the playlist list, want the open playlist")
				}
			default:
				t.Fatalf("ctrl+r command sent %T", msg)
			}
		})
	}
}

func TestHandleGlobalKey(t *testing.T) {
	tests := []struct {
		name     string
		key      tea.KeyPressMsg
		tooSmall bool
		keymap   bool
		wantOK   bool
		wantQuit bool
		// wantKeymap is keymap.visible after the key.
		wantKeymap bool
		// wantUndo is whether the key restores the removed track.
		wantUndo bool
	}{
		{name: "ctrl+c quits", key: tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, wantOK: true, wantQuit: true},
		{name: "ctrl+z undoes", key: tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}, wantOK: true, wantUndo: true},
		{name: "ctrl+k opens the keymap", key: tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl}, wantOK: true, wantKeymap: true},
		{name: "ctrl+k over the keymap goes on", key: tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl}, keymap: true, wantKeymap: true},
		{name: "q goes on", key: tea.KeyPressMsg{Text: "q"}},
		{name: "q quits when too small", key: tea.KeyPressMsg{Text: "q"}, tooSmall: true, wantOK: true, wantQuit: true},
		{name: "other keys stop when too small", key: tea.KeyPressMsg{Text: "x"}, tooSmall: true, wantOK: true},
		{name: "ctrl+c quits when too small", key: tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, tooSmall: true, wantOK: true, wantQuit: true},
		{name: "ctrl+z stops when too small", key: tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}, tooSmall: true, wantOK: true},
		{name: "ctrl+k stops when too small", key: tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl}, tooSmall: true, wantOK: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			m.playlist.Add(playlist.Track{Title: "One"}, playlist.Track{Title: "Two"})
			if _, err := m.removeTrack(0, true); err != nil {
				t.Fatalf("removeTrack: %v", err)
			}
			m.keymap.visible = tt.keymap
			if tt.tooSmall {
				m.width, m.height = 39, 9
				m.recomputeLayout()
			}

			_, ok := m.handleGlobalKey(tt.key)

			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if m.quitting != tt.wantQuit {
				t.Errorf("quitting = %v, want %v", m.quitting, tt.wantQuit)
			}
			if m.keymap.visible != tt.wantKeymap {
				t.Errorf("keymap.visible = %v, want %v", m.keymap.visible, tt.wantKeymap)
			}
			wantLen := 1
			if tt.wantUndo {
				wantLen = 2
			}
			if got := m.playlist.Len(); got != wantLen {
				t.Errorf("playlist.Len() = %d, want %d", got, wantLen)
			}
		})
	}
}

// Stray keys behind a confirm prompt name the valid keys instead of dying
// silently. The filebrowser replace prompt stays open; the manager delete
// prompt cancels but says so.
func TestConfirmPromptsHintOnStrayKeys(t *testing.T) {
	t.Run("filebrowser replace stays open with a hint", func(t *testing.T) {
		m := keybindingTestModel()
		m.fileBrowser.visible = true
		m.fileBrowser.confirmReplace = true

		m.handleFileBrowserKey(tea.KeyPressMsg{Text: "x"})

		if !m.fileBrowser.confirmReplace {
			t.Fatal("stray key dismissed the replace prompt; want it open")
		}
		if m.status.text == "" {
			t.Fatal("stray key left no hint; want the valid keys named")
		}
	})

	t.Run("manager delete cancels with a hint", func(t *testing.T) {
		m := keybindingTestModel()
		m.plManager = plManagerState{visible: true, screen: plMgrScreenList, confirmDel: true}

		m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "x"})

		if m.plManager.confirmDel {
			t.Fatal("confirmDel still armed after stray key; want it cancelled")
		}
		if m.status.text == "" {
			t.Fatal("stray key left no hint; want the cancellation named")
		}
	})
}

// A shortcut to an unconfigured provider keeps the browser open and names
// the setup step, instead of closing the browser onto nothing.
func TestOverlayProviderShortcutMissHintsSetup(t *testing.T) {
	m := shortcutTestModel()
	m.playlist.Add(playlist.Track{Path: "existing.mp3"})
	m.providers = slices.DeleteFunc(m.providers, func(e provider.Entry) bool { return e.Key == "spotify" })
	m.navBrowser = navBrowserState{
		prov: commandsTestProvider{name: "Browse"}, visible: true, mode: navBrowseModeByAlbum, screen: navBrowseScreenTracks,
		tracks: []playlist.Track{{Path: "replacement.mp3"}},
	}

	m.handleNavBrowserKey(tea.KeyPressMsg{Text: "S"})

	if !m.navBrowser.visible {
		t.Fatal("nav browser closed for an unconfigured provider; want it open")
	}
	if m.status.text == "" {
		t.Fatal("no setup hint; want the status to name the setup step")
	}
}

// q above the main screens closes the top overlay instead of quitting:
// keymap, file browser, theme/visualizer pickers, playlist picker and
// manager, and the queue.
func TestOverlayQCloses(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*Model)
		isOpen func(*Model) bool
	}{
		{name: "keymap", setup: func(m *Model) { m.keymap.visible = true }, isOpen: func(m *Model) bool { return m.keymap.visible }},
		{name: "file browser", setup: func(m *Model) { m.fileBrowser.visible = true }, isOpen: func(m *Model) bool { return m.fileBrowser.visible }},
		{name: "theme picker", setup: func(m *Model) { m.themePicker.visible = true }, isOpen: func(m *Model) bool { return m.themePicker.visible }},
		{name: "visualizer picker", setup: func(m *Model) { m.visPicker.visible = true }, isOpen: func(m *Model) bool { return m.visPicker.visible }},
		{name: "playlist picker", setup: func(m *Model) { m.plPicker.visible = true }, isOpen: func(m *Model) bool { return m.plPicker.visible }},
		{name: "playlist manager list", setup: func(m *Model) {
			m.plManager = plManagerState{visible: true, screen: plMgrScreenList}
		}, isOpen: func(m *Model) bool { return m.plManager.visible }},
		{name: "queue", setup: func(m *Model) { m.queue.visible = true }, isOpen: func(m *Model) bool { return m.queue.visible }},
		{name: "info", setup: func(m *Model) { m.info.visible = true }, isOpen: func(m *Model) bool { return m.info.visible }},
		{name: "lyrics", setup: func(m *Model) { m.lyrics.visible = true }, isOpen: func(m *Model) bool { return m.lyrics.visible }},
		{name: "device picker", setup: func(m *Model) { m.devicePicker.visible = true }, isOpen: func(m *Model) bool { return m.devicePicker.visible }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			m.vis = ui.NewVisualizer(48000)
			tt.setup(&m)

			if cmd := m.handleKey(tea.KeyPressMsg{Text: "q"}); cmd != nil {
				t.Errorf("cmd = non-nil, want nil (close, not quit)")
			}
			if tt.isOpen(&m) {
				t.Error("overlay still open, want closed")
			}
			if m.quitting {
				t.Error("quitting = true, want false (q must not quit)")
			}
		})
	}
}

// Space in the manager tracks screen advances only when marking: checking
// walks down the list, unchecking stays on the row under review.
func TestManagerSpaceAdvanceOnMarkOnly(t *testing.T) {
	newTracksModel := func() *Model {
		m := keybindingTestModel()
		m.plManager = plManagerState{visible: true, screen: plMgrScreenTracks, selPlaylist: "music"}
		m.plMgrLoadTracks([]playlist.Track{
			{Path: "/one.mp3", Title: "One"},
			{Path: "/two.mp3", Title: "Two"},
		})
		return &m
	}
	space := tea.KeyPressMsg{Code: tea.KeySpace}

	t.Run("marking advances", func(t *testing.T) {
		m := newTracksModel()
		m.handlePlaylistManagerKey(space)
		if !m.plManager.marked[0] {
			t.Fatal("track 0 not marked after space")
		}
		if m.plManager.cursor != 1 {
			t.Fatalf("cursor = %d, want 1 (advance on mark)", m.plManager.cursor)
		}
	})

	t.Run("unmarking stays", func(t *testing.T) {
		m := newTracksModel()
		m.handlePlaylistManagerKey(space)
		m.plManager.cursor = 0
		m.handlePlaylistManagerKey(space)
		if m.plManager.marked[0] {
			t.Fatal("track 0 still marked after second space")
		}
		if m.plManager.cursor != 0 {
			t.Fatalf("cursor = %d, want 0 (stay on unmark)", m.plManager.cursor)
		}
	})

	t.Run("last row clamps", func(t *testing.T) {
		m := newTracksModel()
		m.plManager.cursor = 1
		m.handlePlaylistManagerKey(space)
		if !m.plManager.marked[1] {
			t.Fatal("track 1 not marked after space")
		}
		if m.plManager.cursor != 1 {
			t.Fatalf("cursor = %d, want 1 (clamp on last row)", m.plManager.cursor)
		}
	})
}

// q on a manager tracks screen steps back to the list, like Esc does.
func TestManagerTracksQBacksToList(t *testing.T) {
	m := keybindingTestModel()
	m.localProvider = &localInlineFake{commandsTestProvider: commandsTestProvider{name: "Local"}}
	m.plManager = plManagerState{visible: true, screen: plMgrScreenTracks, selPlaylist: "music"}

	m.handleKey(tea.KeyPressMsg{Text: "q"})

	if !m.plManager.visible {
		t.Fatal("manager closed; want it open on the list screen")
	}
	if m.plManager.screen != plMgrScreenList {
		t.Fatalf("screen = %v, want plMgrScreenList", m.plManager.screen)
	}
	if m.quitting {
		t.Error("quitting = true, want false (q must not quit)")
	}
}

// q in the full-screen visualizer exits back to the player instead of
// quitting the app; quitting there stays on Ctrl+C.
func TestFullVisualizerExitKeys(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{name: "q", key: tea.KeyPressMsg{Text: "q"}},
		{name: "esc", key: tea.KeyPressMsg{Code: tea.KeyEscape}},
		{name: "backspace", key: tea.KeyPressMsg{Code: tea.KeyBackspace}},
		{name: "b", key: tea.KeyPressMsg{Text: "b"}},
		{name: "V", key: tea.KeyPressMsg{Text: "V"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			m.fullVis = true

			if cmd := m.handleKey(tt.key); cmd != nil {
				t.Errorf("cmd = non-nil, want nil (no quit command)")
			}
			if m.fullVis {
				t.Error("fullVis = true, want false (exit the visualizer)")
			}
			if m.quitting {
				t.Error("quitting = true, want false (q must not quit)")
			}
		})
	}
}
