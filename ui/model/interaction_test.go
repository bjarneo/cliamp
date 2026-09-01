package model

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

type interactionBrowseProvider struct {
	commandsTestProvider
}

func (p interactionBrowseProvider) Artists() ([]provider.ArtistInfo, error) { return nil, nil }

func (p interactionBrowseProvider) ArtistAlbums(string) ([]provider.AlbumInfo, error) {
	return nil, nil
}

type trackArtistBrowseProvider struct {
	interactionBrowseProvider
}

type providerPaneBrowseProvider struct {
	interactionBrowseProvider
}

type positionedBrowseProvider struct {
	providerPaneBrowseProvider
}

type sharedModeBrowseProvider struct {
	providerPaneBrowseProvider
}

type preferredBrowseProvider struct {
	providerPaneBrowseProvider
}

func (p *preferredBrowseProvider) DefaultBrowseMode() provider.BrowseMode {
	return provider.BrowseArtistAlbums
}

type favoriteBrowseProvider struct {
	commandsTestProvider
}

func (p favoriteBrowseProvider) BrowseEntries() []provider.BrowseEntry {
	return []provider.BrowseEntry{{ID: "browse:shows", Name: "Shows", Section: "Browse", Mode: provider.BrowseAlbums}}
}

func (p favoriteBrowseProvider) ToggleFavorite(string) (bool, string, error) {
	return true, "Recent Releases", nil
}

func (p providerPaneBrowseProvider) BrowseEntries() []provider.BrowseEntry {
	return []provider.BrowseEntry{
		{ID: "browse:shows", Name: "Shows", Section: "Browse", Mode: provider.BrowseAlbums},
		{ID: "browse:creators", Name: "Creators", Section: "Browse", Mode: provider.BrowseArtistAlbums},
		{ID: "browse:genres", Name: "Genres", Section: "Browse", Mode: provider.BrowseGenres},
	}
}

func (p providerPaneBrowseProvider) AlbumList(string, int, int) ([]provider.AlbumInfo, error) {
	return nil, nil
}

func (p providerPaneBrowseProvider) AlbumSortTypes() []provider.SortType {
	return []provider.SortType{{ID: "latest", Label: "Latest"}}
}

func (p providerPaneBrowseProvider) DefaultAlbumSort() string { return "latest" }

func (p providerPaneBrowseProvider) Genres() ([]provider.GenreInfo, error) { return nil, nil }

func (p providerPaneBrowseProvider) GenreSortTypes() []provider.SortType {
	return []provider.SortType{{ID: "latest", Label: "Latest"}, {ID: "popular", Label: "Popular"}}
}

func (p providerPaneBrowseProvider) GenreTracks(string, string) ([]playlist.Track, error) {
	return []playlist.Track{{Title: "Show", Path: "https://example.com/show"}}, nil
}

func (p providerPaneBrowseProvider) AlbumTracks(string) ([]playlist.Track, error) {
	return []playlist.Track{{Title: "Show", Path: "https://example.com/show"}}, nil
}

func (p positionedBrowseProvider) BrowseEntries() []provider.BrowseEntry {
	return []provider.BrowseEntry{
		{
			ID: "browse:creators", Name: "Creators", Section: "Library",
			Mode: provider.BrowseArtistAlbums, AfterID: "favorites",
			AfterSection: "Library", OpenInPlaylist: true,
		},
		{
			ID: "browse:shows", Name: "Shows", Section: "Browse",
			Mode: provider.BrowseAlbums, AfterSection: "Library", OpenInPlaylist: true,
		},
		{
			ID: "browse:genres", Name: "Genres", Section: "Browse",
			Mode: provider.BrowseGenres, AfterSection: "Library", OpenInPlaylist: true,
		},
	}
}

func (p sharedModeBrowseProvider) BrowseEntries() []provider.BrowseEntry {
	return []provider.BrowseEntry{
		{ID: "browse:preview", Name: "Preview", Section: "Browse", Mode: provider.BrowseAlbums},
		{ID: "browse:play", Name: "Play", Section: "Browse", Mode: provider.BrowseAlbums, OpenInPlaylist: true},
	}
}

func (p trackArtistBrowseProvider) ArtistForTrack(track playlist.Track) (provider.ArtistInfo, bool) {
	if track.Meta("test.creator") == "" {
		return provider.ArtistInfo{}, false
	}
	return provider.ArtistInfo{ID: track.Meta("test.creator"), Name: track.Artist}, true
}

func (p trackArtistBrowseProvider) ArtistAlbums(artistID string) ([]provider.AlbumInfo, error) {
	return []provider.AlbumInfo{{ID: "uploads:" + artistID, Name: "Uploads"}}, nil
}

func keybindingTestModel() Model {
	local := commandsTestProvider{name: "Local"}
	return Model{
		playlist: playlist.New(),
		player:   &playbackFakeEngine{},
		provider: local,
		providers: []ProviderEntry{
			{Key: "local", Name: "Local", Provider: local},
			{Key: "yt", Name: "YouTube", Provider: commandsTestProvider{name: "YouTube"}},
		},
	}
}

func TestHandleKeyEnhancedShiftYSelectsYouTubeProvider(t *testing.T) {
	m := keybindingTestModel()
	msg := tea.KeyPressMsg{Code: 'y', ShiftedCode: 'Y', Mod: tea.ModShift}
	if got := msg.String(); got != "shift+y" {
		t.Fatalf("enhanced Shift+Y string = %q, want shift+y", got)
	}

	m.handleKey(msg)

	if got := m.provider.Name(); got != "YouTube" {
		t.Fatalf("active provider = %q, want YouTube", got)
	}
	if m.lyrics.visible {
		t.Fatal("lyrics.visible = true after enhanced Shift+Y, want false")
	}
}

func TestHandleKeyEnhancedShiftNOpensProviderBrowser(t *testing.T) {
	browse := interactionBrowseProvider{commandsTestProvider{name: "Navidrome"}}
	m := keybindingTestModel()
	m.provider = browse
	m.providers = append(m.providers, ProviderEntry{Key: "navidrome", Name: "Navidrome", Provider: browse})
	msg := tea.KeyPressMsg{Code: 'n', ShiftedCode: 'N', Mod: tea.ModShift}

	m.handleKey(msg)

	if !m.navBrowser.visible {
		t.Fatal("navBrowser.visible = false after enhanced Shift+N, want true")
	}
	if got := m.navBrowser.prov.Name(); got != "Navidrome" {
		t.Fatalf("browser provider = %q, want Navidrome", got)
	}
}

func TestSwitchProviderOpensPreferredBrowseMode(t *testing.T) {
	jellyfin := &preferredBrowseProvider{
		providerPaneBrowseProvider: providerPaneBrowseProvider{
			interactionBrowseProvider{commandsTestProvider{name: "Jellyfin"}},
		},
	}
	m := keybindingTestModel()
	m.providers = append(m.providers, ProviderEntry{Key: "jellyfin", Name: "Jellyfin", Provider: jellyfin})

	cmd := m.switchToProvider("jellyfin")

	if cmd == nil {
		t.Fatal("switchToProvider returned no load command")
	}
	if !m.navBrowser.visible || m.navBrowser.prov != jellyfin {
		t.Fatalf("preferred browser not opened: %+v", m.navBrowser)
	}
	if m.navBrowser.mode != navBrowseModeByArtistAlbum || !m.navBrowser.loading {
		t.Fatalf("browser mode/loading = %d/%v, want artist-album/true", m.navBrowser.mode, m.navBrowser.loading)
	}
}

func TestStartInProviderQueuesPreferredBrowser(t *testing.T) {
	jellyfin := &preferredBrowseProvider{
		providerPaneBrowseProvider: providerPaneBrowseProvider{
			interactionBrowseProvider{commandsTestProvider{name: "Jellyfin"}},
		},
	}
	m := keybindingTestModel()
	m.provider = jellyfin

	m.StartInProvider()
	if !m.openDefaultProviderOnce {
		t.Fatal("StartInProvider did not queue the preferred browser")
	}
	updated, cmd := m.Update(openDefaultProviderBrowserMsg{})
	got := updated.(Model)
	if cmd == nil || !got.navBrowser.visible || got.navBrowser.mode != navBrowseModeByArtistAlbum {
		t.Fatalf("startup browser = visible:%v mode:%d cmd:%v", got.navBrowser.visible, got.navBrowser.mode, cmd != nil)
	}
}

func TestPreferredProviderNOpensModeChooser(t *testing.T) {
	jellyfin := &preferredBrowseProvider{
		providerPaneBrowseProvider: providerPaneBrowseProvider{
			interactionBrowseProvider{commandsTestProvider{name: "Jellyfin"}},
		},
	}
	m := keybindingTestModel()
	m.navBrowser = navBrowserState{
		prov: jellyfin, visible: true, mode: navBrowseModeByArtistAlbum,
	}

	m.handleNavBrowserKey(tea.KeyPressMsg{Text: "N"})

	if !m.navBrowser.visible || m.navBrowser.mode != navBrowseModeMenu {
		t.Fatalf("N left browser in mode %d, want mode chooser", m.navBrowser.mode)
	}
}

func TestBrowseModeSelectionDoesNotChangeProviderDefault(t *testing.T) {
	jellyfin := &preferredBrowseProvider{
		providerPaneBrowseProvider: providerPaneBrowseProvider{
			interactionBrowseProvider{commandsTestProvider{name: "Jellyfin"}},
		},
	}
	m := keybindingTestModel()
	m.navBrowser = navBrowserState{prov: jellyfin, visible: true, mode: navBrowseModeMenu}

	cmd := m.handleNavBrowserKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if cmd == nil {
		t.Fatal("mode selection returned no album-load command")
	}
	if m.navBrowser.mode != navBrowseModeByAlbum {
		t.Fatalf("active mode = %d, want album mode", m.navBrowser.mode)
	}
	if jellyfin.DefaultBrowseMode() != provider.BrowseArtistAlbums {
		t.Fatalf("default mode = %d, want BrowseArtistAlbums", jellyfin.DefaultBrowseMode())
	}
}

func TestProviderPaneShiftNDoesNotOpenAnotherProviderBrowser(t *testing.T) {
	mixcloud := providerPaneBrowseProvider{interactionBrowseProvider{commandsTestProvider{name: "Mixcloud"}}}
	spotify := commandsTestProvider{name: "Spotify"}
	m := keybindingTestModel()
	m.focus = focusProvider
	m.provider = spotify
	m.providers = append(m.providers,
		ProviderEntry{Key: "spotify", Name: "Spotify", Provider: spotify},
		ProviderEntry{Key: "mixcloud", Name: "Mixcloud", Provider: mixcloud},
	)

	m.handleKey(tea.KeyPressMsg{Code: 'n', ShiftedCode: 'N', Mod: tea.ModShift})

	if m.navBrowser.visible {
		t.Fatalf("Spotify provider pane opened %q browser", m.navBrowser.prov.Name())
	}
	m.focus = focusPlaylist
	m.handleKey(tea.KeyPressMsg{Code: 'n', ShiftedCode: 'N', Mod: tea.ModShift})
	if m.navBrowser.visible {
		t.Fatalf("Spotify playlist opened %q browser", m.navBrowser.prov.Name())
	}

	m.focus = focusProvider
	m.provider = mixcloud
	m.handleKey(tea.KeyPressMsg{Code: 'n', ShiftedCode: 'N', Mod: tea.ModShift})
	if !m.navBrowser.visible || m.navBrowser.prov == nil || m.navBrowser.prov.Name() != "Mixcloud" {
		t.Fatalf("Mixcloud provider pane did not open its own browser: %+v", m.navBrowser)
	}
}

func TestShiftNOnProviderTrackJumpsToItsArtist(t *testing.T) {
	browse := trackArtistBrowseProvider{interactionBrowseProvider{commandsTestProvider{name: "Mixcloud"}}}
	m := keybindingTestModel()
	m.provider = browse
	m.providers = append(m.providers, ProviderEntry{Key: "mixcloud", Name: "Mixcloud", Provider: browse})
	m.focus = focusPlaylist
	m.playlist.Add(playlist.Track{
		Title: "A Show", Artist: "Creator Name",
		ProviderMeta: map[string]string{"test.creator": "creator"},
	})
	m.plCursor = 0

	cmd := m.handleKey(tea.KeyPressMsg{Code: 'n', ShiftedCode: 'N', Text: "N", Mod: tea.ModShift})
	if cmd == nil {
		t.Fatal("Shift+N returned no artist-load command")
	}
	if !m.navBrowser.visible || m.navBrowser.mode != navBrowseModeByArtistAlbum || m.navBrowser.screen != navBrowseScreenAlbums {
		t.Fatalf("nav state = %+v", m.navBrowser)
	}
	if !m.navBrowser.directTrackJump {
		t.Fatal("directTrackJump = false after selected-track creator jump")
	}
	if m.navBrowser.selArtist.ID != "creator" || m.navBrowser.selArtist.Name != "Creator Name" {
		t.Fatalf("selected artist = %+v", m.navBrowser.selArtist)
	}
	msg, ok := cmd().(navAlbumsLoadedMsg)
	if !ok || len(msg.albums) != 1 || msg.albums[0].Name != "Uploads" {
		t.Fatalf("artist load message = %#v", msg)
	}
}

func TestEscFromDirectTrackCreatorReturnsToPlaylist(t *testing.T) {
	browse := trackArtistBrowseProvider{interactionBrowseProvider{commandsTestProvider{name: "Mixcloud"}}}
	m := keybindingTestModel()
	m.provider = browse
	m.providers = append(m.providers, ProviderEntry{Key: "mixcloud", Name: "Mixcloud", Provider: browse})
	m.focus = focusPlaylist
	m.playlist.Add(playlist.Track{
		Title: "A Show", Artist: "Creator Name",
		ProviderMeta: map[string]string{"test.creator": "creator"},
	})

	if cmd, ok := m.openSelectedTrackArtistBrowser(); !ok || cmd == nil {
		t.Fatal("selected-track creator browser did not open")
	}
	m.handleNavBrowserKey(tea.KeyPressMsg{Code: tea.KeyEscape})

	if m.navBrowser.visible {
		t.Fatalf("nav browser = %+v, want closed after backing out of a direct creator jump", m.navBrowser)
	}
	if m.navBrowser.directTrackJump {
		t.Fatal("directTrackJump remained set after returning to the playlist")
	}
}

func TestProviderPaneBrowseEntryOpensCreatorHierarchy(t *testing.T) {
	browse := providerPaneBrowseProvider{interactionBrowseProvider{commandsTestProvider{name: "Mixcloud"}}}
	lists := providerListsWithBrowse(browse, []playlist.PlaylistInfo{{ID: "recent", Name: "Recent Releases", Section: "Discover"}})
	if len(lists) != 4 || lists[0].Name != "Shows" || lists[1].Name != "Creators" || lists[2].Name != "Genres" || lists[3].Name != "Recent Releases" {
		t.Fatalf("decorated provider lists = %+v", lists)
	}

	m := keybindingTestModel()
	m.provider = browse
	m.providerLists = lists
	cmd := m.openProviderList(1)
	if cmd == nil {
		t.Fatal("Creators entry returned no artist-load command")
	}
	if !m.navBrowser.visible || m.navBrowser.mode != navBrowseModeByArtistAlbum || m.navBrowser.screen != navBrowseScreenList {
		t.Fatalf("nav state = %+v", m.navBrowser)
	}
	if _, ok := cmd().(navArtistsLoadedMsg); !ok {
		t.Fatalf("Creators command returned %T, want navArtistsLoadedMsg", cmd())
	}
}

func TestProviderPaneBrowseEntryBackReturnsToProviderList(t *testing.T) {
	browse := providerPaneBrowseProvider{interactionBrowseProvider{commandsTestProvider{name: "Mixcloud"}}}
	lists := providerListsWithBrowse(browse, nil)

	for _, tt := range []struct {
		name  string
		index int
		mode  navBrowseModeType
	}{
		{name: "shows", index: 0, mode: navBrowseModeByAlbum},
		{name: "creators", index: 1, mode: navBrowseModeByArtistAlbum},
		{name: "genres", index: 2, mode: navBrowseModeByGenre},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			m.provider = browse
			m.providerLists = lists

			if cmd := m.openProviderList(tt.index); cmd == nil {
				t.Fatalf("openProviderList(%d) returned no load command", tt.index)
			}
			if !m.navBrowser.visible || !m.navBrowser.fromProvList || m.navBrowser.mode != tt.mode {
				t.Fatalf("opened nav state = %+v", m.navBrowser)
			}

			m.handleNavBrowserKey(tea.KeyPressMsg{Code: tea.KeyLeft})
			if m.navBrowser.visible || m.navBrowser.fromProvList {
				t.Fatalf("nav state after Back = %+v, want provider list", m.navBrowser)
			}
		})
	}
}

func TestProviderPaneCreatorMultiLevelBackReturnsToProviderList(t *testing.T) {
	browse := providerPaneBrowseProvider{interactionBrowseProvider{commandsTestProvider{name: "Mixcloud"}}}
	m := keybindingTestModel()
	m.provider = browse
	m.providerLists = providerListsWithBrowse(browse, nil)

	if cmd := m.openProviderList(1); cmd == nil {
		t.Fatal("Creators entry returned no artist-load command")
	}
	m.navBrowser.loading = false
	m.navBrowser.artists = []provider.ArtistInfo{{ID: "creator", Name: "Creator"}}
	if cmd := m.handleNavBrowserKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil {
		t.Fatal("creator selection returned no collection-load command")
	}
	if m.navBrowser.screen != navBrowseScreenAlbums || !m.navBrowser.fromProvList {
		t.Fatalf("creator collection state = %+v", m.navBrowser)
	}

	m.handleNavBrowserKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	if !m.navBrowser.visible || m.navBrowser.screen != navBrowseScreenList || !m.navBrowser.fromProvList {
		t.Fatalf("nav state after first Back = %+v, want creator list", m.navBrowser)
	}
	m.handleNavBrowserKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.navBrowser.visible || m.navBrowser.fromProvList {
		t.Fatalf("nav state after second Back = %+v, want provider list", m.navBrowser)
	}
}

func TestNormalBrowseRootBackReturnsToBrowseMenu(t *testing.T) {
	browse := providerPaneBrowseProvider{interactionBrowseProvider{commandsTestProvider{name: "Mixcloud"}}}
	for _, mode := range []provider.BrowseMode{provider.BrowseAlbums, provider.BrowseArtistAlbums, provider.BrowseGenres} {
		m := keybindingTestModel()
		m.openNavBrowserAt(browse, mode)
		m.navBrowser.cursor = 40
		m.navBrowser.scroll = 35
		m.handleNavBrowserKey(tea.KeyPressMsg{Code: tea.KeyLeft})
		if !m.navBrowser.visible || m.navBrowser.mode != navBrowseModeMenu || m.navBrowser.fromProvList {
			t.Fatalf("mode %v nav state after Back = %+v, want browse menu", mode, m.navBrowser)
		}
		if m.navBrowser.cursor != 0 || m.navBrowser.scroll != 0 {
			t.Fatalf("mode %v menu position = cursor:%d scroll:%d, want 0,0", mode, m.navBrowser.cursor, m.navBrowser.scroll)
		}
	}
}

func TestPositionedBrowseEntriesFollowPreferredSection(t *testing.T) {
	browse := positionedBrowseProvider{providerPaneBrowseProvider{interactionBrowseProvider{commandsTestProvider{name: "Mixcloud"}}}}
	lists := providerListsWithBrowse(browse, []playlist.PlaylistInfo{
		{ID: "stream", Name: "Stream", Section: "Library"},
		{ID: "favorites", Name: "Favorites", Section: "Library"},
		{ID: "collection", Name: "Collection", Section: "Collections"},
		{ID: "recent", Name: "Recent", Section: "Discover"},
	})
	want := []string{"stream", "favorites", "browse:creators", "browse:shows", "browse:genres", "collection", "recent"}
	got := make([]string, 0, len(lists))
	for _, item := range lists {
		got = append(got, item.ID)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("positioned provider lists = %v, want %v", got, want)
	}

	publicOnly := providerListsWithBrowse(browse, []playlist.PlaylistInfo{{ID: "recent", Name: "Recent", Section: "Discover"}})
	if len(publicOnly) != 3 || publicOnly[0].ID != "browse:shows" || publicOnly[1].ID != "browse:genres" || publicOnly[2].ID != "recent" {
		t.Fatalf("public-only provider lists = %+v, want Browse without account-only Creators", publicOnly)
	}
	m := keybindingTestModel()
	m.openNavBrowserAt(browse, provider.BrowseArtistAlbums)
	if !m.navBrowser.openInPlaylist {
		t.Fatal("creator leaf behavior depended on the account-only pane entry being visible")
	}
}

func TestBrowseEntryGroupingAndAnchorRules(t *testing.T) {
	lists := []playlist.PlaylistInfo{
		{ID: "stream", Name: "Stream", Section: "Library"},
		{ID: "favorites", Name: "Favorites", Section: "Library"},
		{ID: "uploads", Name: "Uploads", Section: "Library"},
		{ID: "collection", Name: "Collection", Section: "Collections"},
		{ID: "recent", Name: "Recent", Section: "Discover"},
	}
	entries := []provider.BrowseEntry{
		{ID: "recent", Name: "Duplicate", Section: "Browse"},
		{ID: "browse:creator", Name: "Creator", Section: "Library", AfterID: "favorites", AfterSection: "Library"},
		{ID: "browse:fallback", Name: "Fallback", Section: "Browse", AfterID: "missing", AfterSection: "Collections"},
		{ID: "browse:fallback-2", Name: "Fallback 2", Section: "Browse", AfterID: "missing", AfterSection: "Collections"},
		{ID: "browse:account", Name: "Account", Section: "Account", AfterSection: "Account"},
		{ID: "browse:public", Name: "Public", Section: "Browse", AfterSection: "Missing"},
		{ID: "", Name: "Invalid", Section: "Browse"},
	}

	groups := groupBrowseEntries(entries, lists)
	gotLists := spliceBrowseGroups(lists, groups)
	got := make([]string, 0, len(gotLists))
	for _, item := range gotLists {
		got = append(got, item.ID)
	}
	want := []string{
		"browse:public", "stream", "favorites", "browse:creator", "uploads",
		"collection", "browse:fallback", "browse:fallback-2", "recent",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("spliced browse lists = %v, want %v", got, want)
	}
}

func TestBrowsePlayableLeavesOpenInMainPlaylist(t *testing.T) {
	browse := positionedBrowseProvider{providerPaneBrowseProvider{interactionBrowseProvider{commandsTestProvider{name: "Mixcloud"}}}}
	lists := providerListsWithBrowse(browse, []playlist.PlaylistInfo{
		{ID: "stream", Name: "Stream", Section: "Library"},
		{ID: "favorites", Name: "Favorites", Section: "Library"},
	})

	for _, id := range []string{"browse:shows", "browse:creators", "browse:genres"} {
		t.Run(id, func(t *testing.T) {
			index := slices.IndexFunc(lists, func(item playlist.PlaylistInfo) bool { return item.ID == id })
			if index < 0 {
				t.Fatalf("browse entry %q missing from %+v", id, lists)
			}
			m := keybindingTestModel()
			m.provider = browse
			m.providerLists = lists
			if cmd := m.openProviderList(index); cmd == nil {
				t.Fatalf("openProviderList(%q) returned no load command", id)
			}
			if !m.navBrowser.openInPlaylist {
				t.Fatalf("browse entry %q did not opt into the main playlist", id)
			}

			updated, _ := m.Update(navTracksLoadedMsg{
				tracks: []playlist.Track{{Title: id, Path: "https://example.com/show"}},
				gen:    m.requests.nav,
			})
			got := updated.(Model)
			if got.navBrowser.visible || got.focus != focusPlaylist || got.playlist.Len() != 1 {
				t.Fatalf("browse entry %q result = nav:%t focus:%v tracks:%d", id, got.navBrowser.visible, got.focus, got.playlist.Len())
			}
			if !strings.Contains(got.status.text, "Replaced queue with 1 tracks") {
				t.Fatalf("browse entry %q status = %+v", id, got.status)
			}
		})
	}
}

func TestProviderPaneUsesExactBrowseEntryLeafBehavior(t *testing.T) {
	browse := sharedModeBrowseProvider{providerPaneBrowseProvider{interactionBrowseProvider{commandsTestProvider{name: "Shared mode"}}}}
	lists := providerListsWithBrowse(browse, nil)
	m := keybindingTestModel()
	m.provider = browse
	m.providerLists = lists

	if cmd := m.openProviderList(1); cmd == nil {
		t.Fatal("second same-mode browse entry returned no load command")
	}
	if !m.navBrowser.openInPlaylist {
		t.Fatalf("second entry inherited first entry behavior: %+v", m.navBrowser)
	}
}

func TestBackCancelsInFlightPlayableLeaf(t *testing.T) {
	browse := positionedBrowseProvider{providerPaneBrowseProvider{interactionBrowseProvider{commandsTestProvider{name: "Mixcloud"}}}}
	tests := []struct {
		name  string
		start func(*Model) tea.Cmd
	}{
		{
			name: "shows",
			start: func(m *Model) tea.Cmd {
				m.openNavBrowserAt(browse, provider.BrowseAlbums)
				m.navBrowser.albumLoading = false
				m.navBrowser.albums = []provider.AlbumInfo{{ID: "show", Name: "Show"}}
				return m.handleNavBrowserKey(tea.KeyPressMsg{Code: tea.KeyEnter})
			},
		},
		{
			name: "creator collection",
			start: func(m *Model) tea.Cmd {
				m.openNavBrowserAt(browse, provider.BrowseArtistAlbums)
				m.navBrowser.loading = false
				m.navBrowser.screen = navBrowseScreenAlbums
				m.navBrowser.albums = []provider.AlbumInfo{{ID: "uploads", Name: "Uploads"}}
				return m.handleNavBrowserKey(tea.KeyPressMsg{Code: tea.KeyEnter})
			},
		},
		{
			name: "genre sort",
			start: func(m *Model) tea.Cmd {
				m.openNavBrowserAt(browse, provider.BrowseGenres)
				m.navBrowser.loading = false
				m.navBrowser.genres = []provider.GenreInfo{{ID: "house", Name: "House"}}
				m.handleNavBrowserKey(tea.KeyPressMsg{Code: tea.KeyEnter})
				m.navBrowser.cursor = 1
				return m.handleNavBrowserKey(tea.KeyPressMsg{Code: tea.KeyEnter})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			m.playlist.Add(playlist.Track{Title: "Keep", Path: "keep.mp3"})
			cmd := tt.start(&m)
			if cmd == nil {
				t.Fatal("leaf selection returned no load command")
			}
			leafRequest := m.requests.nav
			m.handleNavBrowserKey(tea.KeyPressMsg{Code: tea.KeyEscape})
			if m.requests.nav == leafRequest {
				t.Fatal("Back did not cancel the in-flight leaf request")
			}

			updated, _ := m.Update(cmd())
			got := updated.(Model)
			track, _ := got.playlist.Track(0)
			if !got.navBrowser.visible || got.playlist.Len() != 1 || track.Title != "Keep" {
				t.Fatalf("stale leaf changed state = nav:%t tracks:%d first:%+v", got.navBrowser.visible, got.playlist.Len(), track)
			}
		})
	}
}

func TestEmptyBrowseLeafPreservesCurrentPlaylist(t *testing.T) {
	browse := positionedBrowseProvider{providerPaneBrowseProvider{interactionBrowseProvider{commandsTestProvider{name: "Mixcloud"}}}}
	m := keybindingTestModel()
	m.playlist.Add(playlist.Track{Title: "Keep", Path: "keep.mp3"})
	m.openNavBrowserAt(browse, provider.BrowseGenres)

	updated, _ := m.Update(navTracksLoadedMsg{gen: m.requests.nav})
	got := updated.(Model)
	track, _ := got.playlist.Track(0)
	if !got.navBrowser.visible || got.playlist.Len() != 1 || track.Title != "Keep" {
		t.Fatalf("empty leaf result = nav:%t tracks:%d first:%+v", got.navBrowser.visible, got.playlist.Len(), track)
	}
	if got.status.kind != feedbackWarning || !strings.Contains(got.status.text, "No tracks found") {
		t.Fatalf("empty leaf status = %+v", got.status)
	}
}

func TestAlbumLeafKeepsSelectedRowHighlightedWhileLoading(t *testing.T) {
	browse := positionedBrowseProvider{providerPaneBrowseProvider{interactionBrowseProvider{commandsTestProvider{name: "Mixcloud"}}}}
	m := keybindingTestModel()
	m.navBrowser = navBrowserState{
		prov: browse, visible: true, mode: navBrowseModeByArtistAlbum,
		screen: navBrowseScreenAlbums, openInPlaylist: true, cursor: 1,
		albums: []provider.AlbumInfo{{ID: "uploads", Name: "Uploads"}, {ID: "favorites", Name: "Favorites"}},
	}
	if cmd := m.handleNavBrowserKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil {
		t.Fatal("Favorites selection returned no load command")
	}
	if m.navBrowser.cursor != 1 || m.navBrowser.selAlbum.ID != "favorites" {
		t.Fatalf("leaf selection moved while loading: %+v", m.navBrowser)
	}
}

func TestUnknownArtistItemCountIsOmitted(t *testing.T) {
	m := keybindingTestModel()
	m.plVisible = 5
	m.navBrowser = navBrowserState{
		prov:    &labelProv{},
		visible: true,
		mode:    navBrowseModeByArtist,
		screen:  navBrowseScreenList,
		artists: []provider.ArtistInfo{
			{ID: "unknown", Name: "Unknown count"},
			{ID: "known", Name: "Known count", AlbumCount: 3},
		},
	}
	body := m.renderNavBody()
	if strings.Contains(body, "Unknown count (0 books)") {
		t.Fatalf("artist body displayed unknown zero count: %q", body)
	}
	if !strings.Contains(body, "Known count (3 books)") {
		t.Fatalf("artist body omitted known count: %q", body)
	}
}

func TestProviderFavoriteRefreshKeepsBrowseEntries(t *testing.T) {
	p := favoriteBrowseProvider{commandsTestProvider{name: "Both", lists: []playlist.PlaylistInfo{{ID: "recent", Name: "Recent Releases"}}}}
	m := Model{
		provider:      p,
		providerLists: providerListsWithBrowse(p, p.lists),
		provCursor:    1,
	}

	m.toggleProviderFavorite()

	if len(m.providerLists) != 2 || m.providerLists[0].ID != "browse:shows" || m.providerLists[1].ID != "recent" {
		t.Fatalf("provider lists after favorite refresh = %+v", m.providerLists)
	}
	if m.provCursor != 1 {
		t.Fatalf("provider cursor = %d, want refreshed item at 1", m.provCursor)
	}
}

func TestHandleKeyEnhancedShiftLetterReachesActiveTextInput(t *testing.T) {
	m := keybindingTestModel()
	m.search.active = true

	m.handleKey(tea.KeyPressMsg{Code: 'a', ShiftedCode: 'A', Mod: tea.ModShift})

	if got := m.search.query; got != "A" {
		t.Fatalf("search query = %q, want A", got)
	}
}

func TestHandleKeyPreservesExistingLetterRepresentations(t *testing.T) {
	t.Run("lowercase toggles lyrics", func(t *testing.T) {
		m := keybindingTestModel()
		m.handleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})

		if !m.lyrics.visible {
			t.Fatal("lyrics.visible = false after lowercase y, want true")
		}
		if got := m.provider.Name(); got != "Local" {
			t.Fatalf("active provider = %q, want Local", got)
		}
	})

	t.Run("uppercase text selects provider", func(t *testing.T) {
		m := keybindingTestModel()
		msg := tea.KeyPressMsg{Code: 'y', ShiftedCode: 'Y', Text: "Y", Mod: tea.ModShift}
		if got := msg.String(); got != "Y" {
			t.Fatalf("uppercase text string = %q, want Y", got)
		}

		m.handleKey(msg)

		if got := m.provider.Name(); got != "YouTube" {
			t.Fatalf("active provider = %q, want YouTube", got)
		}
	})
}

func TestHandleKeyPreservesUnrelatedModifiedKeys(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.KeyPressMsg
		want string
	}{
		{name: "shifted non-letter", msg: tea.KeyPressMsg{Code: '1', ShiftedCode: '!', Mod: tea.ModShift}, want: "shift+1"},
		{name: "control shifted letter", msg: tea.KeyPressMsg{Code: 'y', ShiftedCode: 'Y', Mod: tea.ModCtrl | tea.ModShift}, want: "ctrl+shift+y"},
		{name: "alt shifted letter", msg: tea.KeyPressMsg{Code: 'y', ShiftedCode: 'Y', Mod: tea.ModAlt | tea.ModShift}, want: "alt+shift+y"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			if got := tt.msg.String(); got != tt.want {
				t.Fatalf("key string = %q, want %q", got, tt.want)
			}
			if got := normalizeShiftedLetter(tt.msg).String(); got != tt.want {
				t.Fatalf("normalized key string = %q, want %q", got, tt.want)
			}

			m.handleKey(tt.msg)

			if got := m.provider.Name(); got != "Local" {
				t.Fatalf("active provider = %q, want Local", got)
			}
			if m.lyrics.visible || m.navBrowser.visible {
				t.Fatalf("unexpected action: lyrics.visible=%v navBrowser.visible=%v", m.lyrics.visible, m.navBrowser.visible)
			}
		})
	}
}

func TestGlobalHelpOpensOverActiveTextInput(t *testing.T) {
	m := Model{search: searchState{active: true, query: "jazz"}}

	m.handleKey(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	if !m.keymap.visible {
		t.Fatal("keymap.visible = false after Ctrl+K, want true")
	}
	if !m.search.active || m.search.query != "jazz" {
		t.Fatalf("search state = %+v, want active input preserved", m.search)
	}

	m.handleKey(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	if m.keymap.visible {
		t.Fatal("keymap.visible = true after second Ctrl+K, want false")
	}
	if !m.search.active || m.search.query != "jazz" {
		t.Fatalf("search state = %+v after closing help, want active input preserved", m.search)
	}
}

func TestLyricsRetryStartsNewRequest(t *testing.T) {
	p := playlist.New()
	p.Add(playlist.Track{Artist: "Artist", Title: "Title"})
	m := Model{
		playlist: p,
		lyrics: lyricsState{
			visible: true,
			err:     errors.New("temporary failure"),
		},
	}

	if cmd := m.handleKey(tea.KeyPressMsg{Text: "r"}); cmd == nil {
		t.Fatal("lyrics retry command is nil")
	}
	if !m.lyrics.loading {
		t.Fatal("lyrics.loading = false after retry")
	}
	if m.lyrics.err != nil {
		t.Fatalf("lyrics.err = %v after retry, want nil", m.lyrics.err)
	}
	if m.lyrics.query != "Artist\nTitle" {
		t.Fatalf("lyrics.query = %q, want lookup key", m.lyrics.query)
	}
}

func TestUndoRestoresClearedQueue(t *testing.T) {
	p := playlist.New()
	p.Add(playlist.Track{Title: "One"}, playlist.Track{Title: "Two"})
	p.Queue(0)
	p.Queue(1)
	m := Model{
		player:    &playbackFakeEngine{},
		playlist:  p,
		plVisible: 1,
		queue:     queueOverlay{visible: true, cursor: 1, scroll: 1},
	}

	m.handleQueueKey(tea.KeyPressMsg{Text: "c"})
	if got := p.QueueLen(); got != 0 {
		t.Fatalf("queue length after clear = %d, want 0", got)
	}
	if m.queue.cursor != 0 || m.queue.scroll != 0 {
		t.Fatalf("queue state after clear = cursor %d, scroll %d; want 0, 0", m.queue.cursor, m.queue.scroll)
	}
	m.handleKey(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	if got := p.QueueTracks(); len(got) != 2 || got[0].Title != "One" || got[1].Title != "Two" {
		t.Fatalf("queue after undo = %#v, want original queue", got)
	}
	if m.queue.cursor != 0 || m.queue.scroll != 0 {
		t.Fatalf("queue state after undo = cursor %d, scroll %d; want 0, 0", m.queue.cursor, m.queue.scroll)
	}
}

func TestNextTrackNormalizesQueueAfterSkippingUnavailableEntry(t *testing.T) {
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "Current", Path: "current.mp3"},
		{Title: "Unavailable", Unplayable: true},
		{Title: "Next", Path: "next.mp3"},
		{Title: "Remaining", Path: "remaining.mp3"},
	})
	p.Queue(1)
	p.Queue(2)
	p.Queue(3)
	m := Model{
		player:    &playbackFakeEngine{},
		playlist:  p,
		plVisible: 2,
		queue:     queueOverlay{visible: true, cursor: 2, scroll: 2},
	}

	m.nextTrack()

	if got := p.QueueLen(); got != 1 {
		t.Fatalf("QueueLen() = %d after Next, want 1", got)
	}
	if m.queue.cursor != 0 || m.queue.scroll != 0 {
		t.Fatalf("queue state after Next = cursor %d, scroll %d; want 0, 0", m.queue.cursor, m.queue.scroll)
	}
	view, ok := m.activeOverlay()
	if !ok {
		t.Fatal("queue overlay is not active after Next")
	}
	header := stripAnsi(view.header(&m))
	if !strings.Contains(header, "Queue  1/1") {
		t.Fatalf("queue header after Next = %q, want valid 1/1 selection", header)
	}
}

func TestIPCQueueMutationNormalizesOverlay(t *testing.T) {
	p := playlist.New()
	p.Replace([]playlist.Track{{Title: "A"}, {Title: "B"}, {Title: "C"}})
	p.Queue(0)
	p.Queue(1)
	p.Queue(2)
	m := Model{
		player:    &playbackFakeEngine{},
		playlist:  p,
		plVisible: 2,
		queue:     queueOverlay{visible: true, cursor: 2, scroll: 2},
	}

	reply := make(chan ipc.Response, 1)
	m.handleIPCQueue(ipc.QueueRequestMsg{Op: "queue.remove", Index: 2, Reply: reply})
	<-reply
	if m.queue.cursor != 1 || m.queue.scroll != 0 {
		t.Fatalf("queue state after IPC remove = cursor %d, scroll %d; want 1, 0", m.queue.cursor, m.queue.scroll)
	}

	reply = make(chan ipc.Response, 1)
	m.handleIPCQueue(ipc.QueueRequestMsg{Op: "queue.clear", Reply: reply})
	<-reply
	if m.queue.cursor != 0 || m.queue.scroll != 0 {
		t.Fatalf("queue state after IPC clear = cursor %d, scroll %d; want 0, 0", m.queue.cursor, m.queue.scroll)
	}
	view, ok := m.activeOverlay()
	if !ok {
		t.Fatal("queue overlay is not active after IPC clear")
	}
	if header := stripAnsi(view.header(&m)); strings.Contains(header, "1/0") {
		t.Fatalf("queue header after IPC clear = %q, want no invalid selection", header)
	}
}
