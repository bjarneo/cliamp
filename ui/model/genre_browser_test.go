package model

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

type genreBrowserProvider struct {
	genres []provider.GenreInfo
	styles []string
	tracks []playlist.Track
	search []provider.GenreInfo
}

func (p *genreBrowserProvider) Name() string { return "Genres" }
func (p *genreBrowserProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	lists := make([]playlist.PlaylistInfo, 0, len(p.styles))
	for _, style := range p.styles {
		lists = append(lists, playlist.PlaylistInfo{ID: "style:" + style, Name: style, Section: "Styles"})
	}
	return lists, nil
}
func (p *genreBrowserProvider) Tracks(string) ([]playlist.Track, error) { return nil, nil }
func (p *genreBrowserProvider) Genres() ([]provider.GenreInfo, error) {
	return slices.Clone(p.genres), nil
}
func (p *genreBrowserProvider) GenreSortTypes() []provider.SortType {
	return []provider.SortType{{ID: "latest", Label: "Latest"}, {ID: "popular", Label: "Popular"}}
}
func (p *genreBrowserProvider) GenreTracks(genreID, sortType string) ([]playlist.Track, error) {
	return slices.Clone(p.tracks), nil
}
func (p *genreBrowserProvider) ToggleGenreFavorite(genreID string) (bool, error) {
	favorite := !slices.Contains(p.styles, genreID)
	if favorite {
		p.styles = append(p.styles, genreID)
	} else {
		p.styles = slices.DeleteFunc(p.styles, func(style string) bool { return style == genreID })
	}
	for i := range p.genres {
		if p.genres[i].ID == genreID {
			p.genres[i].Favorite = favorite
		}
	}
	return favorite, nil
}
func (p *genreBrowserProvider) SearchGenres(context.Context, string, int) ([]provider.GenreInfo, error) {
	return slices.Clone(p.search), nil
}
func (p *genreBrowserProvider) BrowseEntries() []provider.BrowseEntry {
	return []provider.BrowseEntry{{
		ID: "browse:genres", Name: "Genres", Section: "Browse",
		Mode: provider.BrowseGenres, OpenInPlaylist: true,
	}}
}

// Compile-time guard against accidentally changing the capability used here.
var _ provider.GenreBrowser = (*genreBrowserProvider)(nil)
var _ provider.GenreFavoriteToggler = (*genreBrowserProvider)(nil)
var _ provider.GenreSearcher = (*genreBrowserProvider)(nil)
var _ playlist.Provider = (*genreBrowserProvider)(nil)

type readOnlyGenreProvider struct {
	genres []provider.GenreInfo
}

func (p *readOnlyGenreProvider) Name() string { return "Read-only genres" }
func (p *readOnlyGenreProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	return nil, nil
}
func (p *readOnlyGenreProvider) Tracks(string) ([]playlist.Track, error) { return nil, nil }
func (p *readOnlyGenreProvider) Genres() ([]provider.GenreInfo, error) {
	return slices.Clone(p.genres), nil
}
func (p *readOnlyGenreProvider) GenreSortTypes() []provider.SortType {
	return []provider.SortType{{ID: "latest", Label: "Latest"}}
}
func (p *readOnlyGenreProvider) GenreTracks(string, string) ([]playlist.Track, error) {
	return nil, nil
}

var _ provider.GenreBrowser = (*readOnlyGenreProvider)(nil)
var _ playlist.Provider = (*readOnlyGenreProvider)(nil)

func TestGenreBrowserFiltersFavoritesAndRefreshesProviderLists(t *testing.T) {
	p := &genreBrowserProvider{
		genres: []provider.GenreInfo{
			{ID: "house", Name: "House", Group: "Music", Favorite: true},
			{ID: "technology", Name: "Technology", Group: "Talk"},
		},
		styles: []string{"house"},
	}
	m := Model{
		provider:  p,
		playlist:  playlist.New(),
		plVisible: 10,
		navBrowser: navBrowserState{
			prov:    p,
			visible: true,
			mode:    navBrowseModeByGenre,
			screen:  navBrowseScreenList,
			genres:  slices.Clone(p.genres),
			search:  "tech",
		},
	}
	m.navUpdateSearch()
	if !slices.Equal(m.navBrowser.searchIdx, []int{1}) {
		t.Fatalf("genre filter indices = %v, want [1]", m.navBrowser.searchIdx)
	}

	cmd := m.handleNavBrowserKey(tea.KeyPressMsg{Text: "f"})
	if !m.navBrowser.genres[1].Favorite || !slices.Equal(p.styles, []string{"house", "technology"}) {
		t.Fatalf("favorite state = ui:%v provider:%v", m.navBrowser.genres[1].Favorite, p.styles)
	}
	if cmd == nil {
		t.Fatal("favorite did not request an immediate provider-menu refresh")
	}
	msg, ok := cmd().(playlistsLoadedMsg)
	if !ok || len(msg.playlists) != 2 {
		t.Fatalf("refresh message = %#v", msg)
	}
	if got := m.renderNavBody(); got == "" {
		t.Fatal("genre browser rendered an empty body")
	}
}

func TestGenreBrowserDrillsIntoLatestAndPopular(t *testing.T) {
	p := &genreBrowserProvider{
		genres: []provider.GenreInfo{{ID: "deep-house", Name: "Deep House", Favorite: true}},
		tracks: []playlist.Track{{Title: "A Show", Path: "https://example.com/show"}},
	}
	m := Model{
		player:   &playbackFakeEngine{},
		playlist: playlist.New(),
		navBrowser: navBrowserState{
			prov:           p,
			visible:        true,
			mode:           navBrowseModeByGenre,
			screen:         navBrowseScreenList,
			genres:         slices.Clone(p.genres),
			openInPlaylist: true,
		},
	}

	if cmd := m.handleNavBrowserKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Fatal("genre selection unexpectedly performed network I/O")
	}
	if m.navBrowser.screen != navBrowseScreenAlbums || len(m.navBrowser.genreSorts) != 2 {
		t.Fatalf("genre sort screen = %v, sorts=%v", m.navBrowser.screen, m.navBrowser.genreSorts)
	}
	m.navBrowser.cursor = 1
	cmd := m.handleNavBrowserKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Popular selection returned no track-loading command")
	}
	if m.navBrowser.cursor != 1 || m.navBrowser.selGenreSort.ID != "popular" {
		t.Fatalf("genre selection moved while loading: %+v", m.navBrowser)
	}
	msg := cmd().(navTracksLoadedMsg)
	if len(msg.tracks) != 1 || msg.tracks[0].Title != "A Show" {
		t.Fatalf("genre tracks = %+v", msg.tracks)
	}
	updated, _ := m.Update(msg)
	got := updated.(Model)
	if got.navBrowser.visible || got.navBrowser.selGenreSort.ID != "popular" {
		t.Fatalf("genre browser remained open after loading a playable leaf: %+v", got.navBrowser)
	}
	if got.focus != focusPlaylist || got.playlist.Len() != 1 || got.plCursor != 0 {
		t.Fatalf("main playlist state = focus:%v len:%d cursor:%d", got.focus, got.playlist.Len(), got.plCursor)
	}
}

func TestGenreBrowseEntryAppearsInProviderPaneAndNavMenu(t *testing.T) {
	p := &genreBrowserProvider{}
	lists := providerListsWithBrowse(p, nil)
	if len(lists) != 1 || lists[0].ID != "browse:genres" {
		t.Fatalf("provider lists = %+v", lists)
	}
	// A genre-only provider must be offered only the genre route. The album and
	// artist rows would close the browser with no explanation when selected.
	m := Model{navBrowser: navBrowserState{prov: p}}
	menu := m.navMenuItems()
	if len(menu) != 1 || menu[0].mode != provider.BrowseGenres {
		t.Fatalf("nav menu = %+v", menu)
	}
}

func TestNavMenuListsOnlyRoutesTheProviderImplements(t *testing.T) {
	for _, tc := range []struct {
		name  string
		prov  playlist.Provider
		modes []provider.BrowseMode
	}{
		{
			name: "album, artist and genre browser",
			prov: providerPaneBrowseProvider{},
			modes: []provider.BrowseMode{
				provider.BrowseAlbums, provider.BrowseArtists,
				provider.BrowseArtistAlbums, provider.BrowseGenres,
			},
		},
		{
			name:  "genre browser only",
			prov:  &genreBrowserProvider{},
			modes: []provider.BrowseMode{provider.BrowseGenres},
		},
		{
			name:  "no browse capability at all",
			prov:  &commandsTestProvider{},
			modes: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := Model{navBrowser: navBrowserState{prov: tc.prov}}
			menu := m.navMenuItems()
			if len(menu) != len(tc.modes) {
				t.Fatalf("nav menu = %+v, want %d entries", menu, len(tc.modes))
			}
			for i, want := range tc.modes {
				if menu[i].mode != want {
					t.Errorf("menu[%d].mode = %v, want %v", i, menu[i].mode, want)
				}
			}
		})
	}
}

func TestGenreLabelerRenamesTheCategoryLevel(t *testing.T) {
	m := Model{navBrowser: navBrowserState{prov: &placeBrowserProvider{}}}
	if got := m.navLabels().genresTitle(); got != "Countries" {
		t.Errorf("genresTitle() = %q, want Countries", got)
	}
	menu := m.navMenuItems()
	if len(menu) != 1 || menu[0].label != "Countries" {
		t.Fatalf("nav menu = %+v, want a single Countries row", menu)
	}
}

// placeBrowserProvider browses places rather than genres, the way the radio
// directory does.
type placeBrowserProvider struct {
	genreBrowserProvider
}

func (p *placeBrowserProvider) GenreLabel() string { return "Countries" }

var _ provider.GenreLabeler = (*placeBrowserProvider)(nil)

func TestGenreBrowseEntriesAreUniqueAndGenreOnlyProvidersCanBrowse(t *testing.T) {
	p := &genreBrowserProvider{}
	lists := providerListsWithBrowse(p, []playlist.PlaylistInfo{{ID: "browse:genres", Name: "Playable collision"}})
	if len(lists) != 1 || lists[0].Name != "Playable collision" {
		t.Fatalf("playable-list collision was not preserved: %+v", lists)
	}

	duplicateEntries := duplicateGenreEntriesProvider{genreBrowserProvider: p}
	lists = providerListsWithBrowse(duplicateEntries, nil)
	if len(lists) != 1 {
		t.Fatalf("duplicate browse entries = %+v, want one", lists)
	}

	readOnly := &readOnlyGenreProvider{}
	if !providerSupportsBrowse(readOnly) {
		t.Fatal("genre-only provider was not recognized as browsable")
	}
	m := Model{provider: readOnly, providers: []ProviderEntry{{Provider: readOnly}}}
	if got := m.findBrowseProvider(); got != readOnly {
		t.Fatalf("findBrowseProvider() = %T, want read-only genre provider", got)
	}
}

type duplicateGenreEntriesProvider struct {
	*genreBrowserProvider
}

func (p duplicateGenreEntriesProvider) BrowseEntries() []provider.BrowseEntry {
	entry := provider.BrowseEntry{ID: "browse:genres", Name: "Genres", Mode: provider.BrowseGenres}
	return []provider.BrowseEntry{entry, entry}
}

func TestReadOnlyGenreBrowserDoesNotAdvertiseFavorite(t *testing.T) {
	p := &readOnlyGenreProvider{genres: []provider.GenreInfo{{ID: "house", Name: "House"}}}
	m := Model{
		playlist: playlist.New(),
		navBrowser: navBrowserState{
			prov:    p,
			visible: true,
			mode:    navBrowseModeByGenre,
			screen:  navBrowseScreenList,
			genres:  slices.Clone(p.genres),
		},
	}
	if help := m.commandHelp(commandModeNavBrowser); strings.Contains(help, "Favorite genre") {
		t.Fatalf("read-only genre browser advertised favorite action: %q", help)
	}
	if cmd := m.handleNavBrowserKey(tea.KeyPressMsg{Text: "f"}); cmd != nil {
		t.Fatal("read-only genre browser handled favorite action")
	}
}

func TestGenreFilterEnterSearchesFullProviderCatalogue(t *testing.T) {
	p := &genreBrowserProvider{search: []provider.GenreInfo{{ID: "acid-techno", Name: "Acid Techno", Group: "Tag"}}}
	m := Model{
		playlist: playlist.New(),
		navBrowser: navBrowserState{
			prov:      p,
			visible:   true,
			mode:      navBrowseModeByGenre,
			screen:    navBrowseScreenList,
			searching: true,
			search:    "acid techno",
		},
	}

	cmd := m.handleNavBrowserKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || !m.navBrowser.loading || m.navBrowser.genreQuery != "acid techno" || m.navBrowser.search != "" {
		t.Fatalf("remote genre search state = %+v, cmd nil=%v", m.navBrowser, cmd == nil)
	}
	msg := cmd().(navGenresLoadedMsg)
	updated, _ := m.Update(msg)
	got := updated.(Model)
	if len(got.navBrowser.genres) != 1 || got.navBrowser.genres[0].ID != "acid-techno" {
		t.Fatalf("remote genre results = %+v", got.navBrowser.genres)
	}
	if breadcrumb := got.navBreadcrumb(); breadcrumb != "Genres / Genres / Search: acid techno" {
		t.Fatalf("search breadcrumb = %q", breadcrumb)
	}
}
