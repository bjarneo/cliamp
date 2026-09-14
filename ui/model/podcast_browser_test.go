package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/external/podcast"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

func TestPodcastBrowserOnlyOpensCategoryShows(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	p := podcast.New("")
	m := Model{provider: p, playlist: playlist.New(), player: &playbackFakeEngine{}, focus: focusProvider}
	m.handleKey(tea.KeyPressMsg{Text: "N"})
	items := m.navMenuItems()
	if len(items) != 1 || items[0].mode != provider.BrowseArtistAlbums {
		t.Fatalf("podcast browse menu = %+v; must not offer loading every feed in a category", items)
	}
	if cmd := m.openNavBrowserAt(p, provider.BrowseArtists); cmd != nil {
		t.Fatal("podcast browser accepted the all-feeds route")
	}
	cmd := m.handleNavMenuKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("category browser did not load")
	}
	next, _ := m.Update(cmd())
	m = next.(Model)
	if len(m.navBrowser.artists) != 19 || !m.navBrowser.openInPlaylist {
		t.Fatalf("category browser = %+v", m.navBrowser)
	}

	// A category show is a refreshable feed, not the category's entire catalog.
	feedURL := "https://example.com/feed"
	m.navBrowser.selAlbum = provider.AlbumInfo{ID: feedURL}
	next, _ = m.Update(navTracksLoadedMsg{gen: m.requests.nav, tracks: []playlist.Track{{Title: "Episode", Path: "https://example.com/episode.mp3"}}})
	m = next.(Model)
	if m.navBrowser.visible || m.focus != focusPlaylist || m.activeProviderPlaylistID != feedURL {
		t.Fatalf("opened show state = visible %v, focus %v, playlist %q", m.navBrowser.visible, m.focus, m.activeProviderPlaylistID)
	}
	m.catalogBatch = catalogBatchState{offset: 100, done: true}
	if cmd := m.handleKey(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl}); cmd == nil {
		t.Fatal("category show could not be refreshed")
	}
	if m.catalogBatch != (catalogBatchState{}) {
		t.Fatalf("refresh retained catalog pagination: %+v", m.catalogBatch)
	}
}

func TestProviderRefreshRestartsCatalogPagination(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	p := podcast.New("")
	m := Model{
		provider: p, playlist: playlist.New(), player: &playbackFakeEngine{}, focus: focusProvider,
		catalogBatch: catalogBatchState{offset: 100, done: true},
	}
	oldGeneration := m.requests.catalog
	cmd := m.handleKey(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if cmd == nil || m.catalogBatch != (catalogBatchState{}) || m.requests.catalog == oldGeneration {
		t.Fatalf("refresh did not invalidate catalog state: %+v", m.catalogBatch)
	}
	next, cmd := m.Update(cmd()) // Playlists is local; do not execute the network catalog command.
	m = next.(Model)
	if cmd == nil || !m.catalogBatch.loading || m.catalogBatch.offset != 0 {
		t.Fatalf("refresh did not start a new first page: %+v", m.catalogBatch)
	}
}

func TestPodcastCategoryRouteDuringSearch(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	p := podcast.New("")
	if _, err := p.SearchCatalog(" "); err != nil {
		t.Fatal(err)
	}
	if len(providerListsWithBrowse(p, nil)) != 0 {
		t.Fatal("browse shortcuts should not be shown as search results")
	}
	m := Model{provider: p}
	if cmd := m.openNavBrowserAt(p, provider.BrowseArtistAlbums); cmd == nil || !m.navBrowser.openInPlaylist {
		t.Fatal("search visibility changed the category route's show-opening behavior")
	}
}

func TestPodcastShortcut(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	for _, view := range []string{"playlist", "provider", "browser"} {
		t.Run(view, func(t *testing.T) {
			p := podcast.New("")
			current := commandsTestProvider{name: "Other"}
			m := keybindingTestModel()
			m.provider = current
			m.providers = []ProviderEntry{{Key: "other", Provider: current}, {Key: "podcast", Name: p.Name(), Provider: p}}
			m.focus = focusPlaylist
			if view == "provider" {
				m.focus = focusProvider
			} else if view == "browser" {
				m.openNavBrowserWith(current)
			}
			cmd := m.handleKey(tea.KeyPressMsg{Text: "O"})
			if cmd == nil || m.provider != p || m.focus != focusProvider || m.navBrowser.visible {
				t.Fatal("Shift+O did not switch directly to Podcasts")
			}
		})
	}
}
