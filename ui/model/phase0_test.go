package model

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/lyrics"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

func TestProviderSearchTakesPrecedenceOverNavigationBrowser(t *testing.T) {
	m := Model{
		searchOverlay: searchOverlayState{
			visible: true,
			screen:  searchOverlayInput,
		},
		navBrowser: navBrowserState{
			visible:   true,
			searching: true,
		},
	}

	if screen := m.activeScreen(); screen != screenSearchOverlay {
		t.Fatalf("activeScreen() = %v, want provider search", screen)
	}
	if _, ok := m.activeOverlay(); !ok {
		t.Fatal("activeOverlay() = none, want provider search")
	}

	m.handleKey(tea.KeyPressMsg{Text: "x"})
	if m.searchOverlay.query != "x" {
		t.Fatalf("provider search query = %q, want x", m.searchOverlay.query)
	}
	if m.navBrowser.search != "" {
		t.Fatalf("navigation filter = %q, want empty", m.navBrowser.search)
	}

	m.handlePaste("y")
	if m.searchOverlay.query != "xy" {
		t.Fatalf("provider search query after paste = %q, want xy", m.searchOverlay.query)
	}

	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.searchOverlay.visible || !m.navBrowser.visible || m.activeScreen() != screenNavBrowser {
		t.Fatalf("nested close state = search:%t nav:%t screen:%v, want navigation browser", m.searchOverlay.visible, m.navBrowser.visible, m.activeScreen())
	}
}

func TestFullVisualizerBlocksHiddenPlaylistMutations(t *testing.T) {
	player := &playbackFakeEngine{}
	p := playlist.New()
	p.Add(playlist.Track{Path: "one.mp3", Title: "One"})
	m := Model{
		player:   player,
		playlist: p,
		vis:      ui.NewVisualizer(float64(player.SampleRate())),
		fullVis:  true,
	}

	m.handleKey(tea.KeyPressMsg{Text: "x"})
	if got := m.playlist.Len(); got != 1 {
		t.Fatalf("playlist length after hidden remove = %d, want 1", got)
	}

	m.handleKey(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	if m.fullVis {
		t.Fatal("fullVis = true after Ctrl+K, want false")
	}
	if !m.keymap.visible {
		t.Fatal("keymap.visible = false after Ctrl+K, want true")
	}
}

func TestClosingProviderSearchCancelsItsRequest(t *testing.T) {
	canceled := false
	m := Model{searchOverlay: searchOverlayState{
		visible: true,
		cancel:  func() { canceled = true },
	}}

	m.closeSearchOverlay()
	if !canceled {
		t.Fatal("provider search request was not canceled")
	}
}

func TestNavigationFilterWithNoMatchesCannotActOnHiddenTracks(t *testing.T) {
	p := playlist.New()
	p.Add(playlist.Track{Path: "existing.mp3", Title: "Existing"})
	m := Model{
		player:   &playbackFakeEngine{},
		playlist: p,
		navBrowser: navBrowserState{
			prov:      commandsTestProvider{name: "Browse"},
			visible:   true,
			mode:      navBrowseModeByAlbum,
			screen:    navBrowseScreenTracks,
			tracks:    []playlist.Track{{Path: "hidden.mp3", Title: "Hidden"}},
			search:    "missing",
			searchIdx: nil,
		},
	}

	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyEnter},
		{Text: "a"},
		{Text: "q"},
		{Text: "R"},
	} {
		m.handleNavBrowserKey(key)
	}
	if got := m.playlist.Tracks()[0].Path; got != "existing.mp3" {
		t.Fatalf("playlist changed by zero-result filter: %q", got)
	}
}

func TestNavigationTrackReplaceWinsOverRadioShortcut(t *testing.T) {
	player := &playbackFakeEngine{}
	p := playlist.New()
	p.Add(playlist.Track{Path: "existing.mp3", Title: "Existing"})
	current := commandsTestProvider{name: "Current"}
	m := Model{
		player:   player,
		playlist: p,
		provider: current,
		vis:      ui.NewVisualizer(float64(player.SampleRate())),
		providers: []provider.Entry{
			{Key: "radio", Name: "Radio", Provider: commandsTestProvider{name: "Radio"}},
		},
		navBrowser: navBrowserState{
			prov:    commandsTestProvider{name: "Browse"},
			visible: true,
			mode:    navBrowseModeByAlbum,
			screen:  navBrowseScreenTracks,
			tracks:  []playlist.Track{{Path: "replacement.mp3", Title: "Replacement"}},
		},
	}

	m.handleNavBrowserKey(tea.KeyPressMsg{Text: "R"})
	if !m.navBrowser.confirmReplace {
		t.Fatal("replace confirmation was not opened")
	}
	m.handleNavBrowserKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	tracks := m.playlist.Tracks()
	if len(tracks) != 1 || tracks[0].Path != "replacement.mp3" {
		t.Fatalf("playlist after R = %#v, want replacement track", tracks)
	}
	if m.provider.Name() != "Current" {
		t.Fatalf("provider after R = %q, want Current", m.provider.Name())
	}
}

func TestStaleAsyncResponsesDoNotChangeCurrentState(t *testing.T) {
	current := commandsTestProvider{name: "Current"}
	m := Model{
		player:   &playbackFakeEngine{},
		playlist: playlist.New(),
		provider: current,
		provPane: providerPane{
			lists:   []playlist.PlaylistInfo{{ID: "current", Name: "Current"}},
			loading: true,
		},
		navBrowser: navBrowserState{
			visible: true,
			loading: true,
		},
		netSearch: netSearchState{
			active:  true,
			loading: true,
			request: "ytsearch10:current",
		},
		lyrics: lyricsState{
			visible: true,
			loading: true,
			query:   "Artist\nCurrent",
		},
		buffering: true,
	}
	m.playlist.Add(playlist.Track{Path: "current.mp3", Title: "Current"})
	m.requests.tracks = 2
	m.requests.nav = 2
	m.requests.netSearch = 2
	m.requests.lyrics = 2
	m.requests.stream = 2

	updates := []tea.Msg{
		tracksLoadedMsg{
			tracks:       []playlist.Track{{Path: "stale.mp3", Title: "Stale"}},
			providerName: "Previous",
			gen:          1,
		},
		navArtistsLoadedMsg{
			artists: []provider.ArtistInfo{{ID: "stale", Name: "Stale"}},
			gen:     1,
		},
		netSearchResultsMsg{
			tracks: []playlist.Track{{Path: "stale.mp3", Title: "Stale"}},
			query:  "ytsearch10:stale",
			gen:    1,
		},
		lyricsLoadedMsg{
			lines: []lyrics.Line{{Text: "stale"}},
			query: "Artist\nStale",
			gen:   1,
		},
		streamPlayedMsg{
			path: "current.mp3",
			gen:  1,
			err:  errors.New("stale failure"),
		},
	}
	for _, msg := range updates {
		updated, _ := m.Update(msg)
		m = updated.(Model)
	}

	if got := m.playlist.Tracks()[0].Path; got != "current.mp3" {
		t.Fatalf("playlist track = %q, want current.mp3", got)
	}
	if len(m.navBrowser.artists) != 0 || !m.navBrowser.loading {
		t.Fatalf("navigation state = %+v, want stale result ignored", m.navBrowser)
	}
	if len(m.netSearch.results) != 0 || !m.netSearch.loading {
		t.Fatalf("net search state = %+v, want stale result ignored", m.netSearch)
	}
	if len(m.lyrics.lines) != 0 || !m.lyrics.loading {
		t.Fatalf("lyrics state = %+v, want stale result ignored", m.lyrics)
	}
	if !m.buffering || m.err != nil {
		t.Fatalf("stream state = buffering:%t err:%v, want unchanged", m.buffering, m.err)
	}
}

func TestProviderRefreshFailureKeepsExistingLists(t *testing.T) {
	current := commandsTestProvider{name: "Current"}
	m := Model{
		provider: current,
		provPane: providerPane{
			lists:   []playlist.PlaylistInfo{{ID: "mix", Name: "Mix"}},
			loading: true,
		},
	}
	m.requests.provider = 1

	updated, _ := m.Update(playlistsLoadedMsg{
		providerName: "Current",
		gen:          1,
		err:          errors.New("offline"),
	})
	m = updated.(Model)

	if len(m.provPane.lists) != 1 || m.provPane.lists[0].Name != "Mix" {
		t.Fatalf("provider lists after refresh failure = %+v, want prior Mix list", m.provPane.lists)
	}
	if m.provPane.loading {
		t.Fatal("provPane.loading = true after failed refresh, want false")
	}
}

func TestProviderPartialRefreshShowsPublicListsAndWarning(t *testing.T) {
	current := providerPaneBrowseProvider{interactionBrowseProvider{commandsTestProvider{name: "Mixcloud"}}}
	m := Model{provider: current, provPane: providerPane{loading: true}}
	m.requests.provider = 1

	updated, _ := m.Update(playlistsLoadedMsg{
		providerName: "Mixcloud",
		gen:          1,
		playlists:    []playlist.PlaylistInfo{{ID: "recent", Name: "Recent Releases", Section: "Discover"}},
		err:          errors.New("mixcloud: account views unavailable: User does not exist"),
	})
	m = updated.(Model)

	if got := len(m.provPane.lists); got != 4 {
		t.Fatalf("provider lists = %+v, want three browse entries and public discovery", m.provPane.lists)
	}
	if m.provPane.lists[0].Name != "Shows" || m.provPane.lists[1].Name != "Creators" || m.provPane.lists[2].Name != "Genres" || m.provPane.lists[3].Name != "Recent Releases" {
		t.Fatalf("provider lists = %+v", m.provPane.lists)
	}
	if m.err != nil || m.status.kind != feedbackWarning || !strings.Contains(m.status.text, "account views unavailable") {
		t.Fatalf("partial refresh state = err:%v status:%+v", m.err, m.status)
	}
}
