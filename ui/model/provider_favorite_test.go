package model

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

type favoriteAlbumTestProvider struct {
	providerPaneBrowseProvider
	toggled        []string
	favorite       bool
	err            error
	playlistsCalls int
}

func (p *favoriteAlbumTestProvider) ToggleFavorite(id string) (bool, string, error) {
	p.toggled = append(p.toggled, id)
	if p.err != nil {
		return false, "Target Show", p.err
	}
	p.favorite = !p.favorite
	return p.favorite, "Target Show", nil
}

func (p *favoriteAlbumTestProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	p.playlistsCalls++
	return p.commandsTestProvider.Playlists()
}

func favoriteAlbumTestModel(view string) (Model, *favoriteAlbumTestProvider) {
	p := &favoriteAlbumTestProvider{providerPaneBrowseProvider: providerPaneBrowseProvider{interactionBrowseProvider{commandsTestProvider{
		name: "Shows", lists: []playlist.PlaylistInfo{{ID: "s:target", Name: "Target Show"}},
	}}}}
	m := keybindingTestModel()
	m.provider, m.focus = p, focusProvider
	m.providerLists = providerListsWithBrowse(p, p.lists)
	m.provCursor = len(m.providerLists) - 1
	m.navBrowser = navBrowserState{
		prov: p, visible: view == "category" || view == "albums", mode: navBrowseModeByArtistAlbum,
		screen: navBrowseScreenAlbums, search: "target",
		albums: []provider.AlbumInfo{{ID: "other", Name: "Other Show"}, {ID: "target", Name: "Target Show"}},
	}
	if view == "albums" {
		m.navBrowser.mode, m.navBrowser.screen = navBrowseModeByAlbum, navBrowseScreenList
	}
	m.navUpdateSearch()
	show := albumResult("Target Show")
	show.Feed = true
	show.Path = "https://example.com/feed"
	show.ProviderMeta[playlist.MetaAlbumID] = "target"
	m.spotSearch = spotSearchState{prov: p, visible: view == "results", screen: spotSearchResults,
		results: []playlist.Track{trackResult("Episode"), show}, cursor: 1}
	return m, p
}

func TestFavoriteFromProviderAlbumsAndSearch(t *testing.T) {
	withFrameWidth(t, 100)
	for _, view := range []string{"pane", "category", "albums", "results"} {
		for _, outcome := range []string{"add", "remove", "error", "inactive provider"} {
			t.Run(view+"/"+outcome, func(t *testing.T) {
				if view == "pane" && outcome == "inactive provider" {
					return
				}
				m, p := favoriteAlbumTestModel(view)
				p.favorite = outcome == "remove"
				if outcome == "remove" {
					p.lists = nil
				}
				if outcome == "error" {
					p.err = errors.New("subscription store is read-only")
				}
				if outcome == "inactive provider" {
					m.provider = commandsTestProvider{name: "Other"}
				}
				mode, _ := m.keymapContext()
				if help := m.commandHelp(mode); !strings.Contains(help, "Favorite") {
					t.Fatalf("favorite-capable selection has no command help: %q", help)
				}
				albums := slices.Clone(m.navBrowser.albums)
				cmd := m.handleKey(tea.KeyPressMsg{Text: "f"})
				wantID := "target"
				if view == "pane" {
					wantID = "s:target"
				}
				if !slices.Equal(p.toggled, []string{wantID}) {
					t.Fatalf("favorite IDs = %v, want selected %q", p.toggled, wantID)
				}
				wantStatus := "Favorited: Target Show"
				if outcome == "remove" {
					wantStatus = "Removed: Target Show"
				} else if outcome == "error" {
					wantStatus = "Favorite save failed: " + p.err.Error()
					if m.status.kind != feedbackError {
						t.Fatalf("persistence failure kind = %v, want error", m.status.kind)
					}
				}
				if m.status.text != wantStatus {
					t.Fatalf("status = %q, want %q", m.status.text, wantStatus)
				}
				wantRefresh := outcome != "error" && outcome != "inactive provider"
				if (cmd != nil) != (wantRefresh && view != "pane") {
					t.Fatal("favorite returned an unexpected pane refresh command")
				}
				if view != "pane" && p.playlistsCalls != 0 {
					t.Fatal("album favorite fetched playlists synchronously")
				}
				if cmd != nil {
					updated, _ := m.Update(cmd())
					m = updated.(Model)
				}
				wantReads := 0
				if wantRefresh {
					wantReads = 1
				}
				if p.playlistsCalls != wantReads {
					t.Fatalf("playlist reads = %d, want %d", p.playlistsCalls, wantReads)
				}
				if wantRefresh && m.provCursor >= len(m.providerLists) {
					t.Fatalf("provider cursor = %d after refresh of %d rows", m.provCursor, len(m.providerLists))
				}
				if !slices.Equal(m.navBrowser.albums, albums) || m.spotSearch.results[1].Title != "Target Show" {
					t.Fatal("favorite decorated album metadata")
				}
			})
		}
	}
}

func TestFavoriteActionAndHelpGuardSelection(t *testing.T) {
	withFrameWidth(t, 100)
	for _, tt := range []struct {
		name, view string
		setup      func(*Model)
	}{
		{"pane loading", "pane", func(m *Model) { m.provLoading = true }},
		{"browse entry", "pane", func(m *Model) { m.provCursor = 0 }},
		{"pane read-only", "pane", func(m *Model) { m.provider = commandsTestProvider{} }},
		{"category loading", "category", func(m *Model) { m.navBrowser.loading = true }},
		{"album pagination", "albums", func(m *Model) { m.navBrowser.albumLoading = true }},
		{"category genre", "category", func(m *Model) { m.navBrowser.screen = navBrowseScreenList }},
		{"genre sort", "category", func(m *Model) { m.navBrowser.mode = navBrowseModeByGenre }},
		{"category episode", "category", func(m *Model) { m.navBrowser.screen = navBrowseScreenTracks }},
		{"empty filter", "category", func(m *Model) { m.navBrowser.searchIdx = nil }},
		{"typing filter", "category", func(m *Model) { m.navBrowser.searching = true }},
		{"category read-only", "category", func(m *Model) { m.navBrowser.prov = commandsTestProvider{} }},
		{"results loading", "results", func(m *Model) { m.spotSearch.loading = true }},
		{"show expanding", "results", func(m *Model) { m.spotSearch.albumLoading = true }},
		{"result episode", "results", func(m *Model) { m.spotSearch.cursor = 0 }},
		{"missing album ID", "results", func(m *Model) { delete(m.spotSearch.results[1].ProviderMeta, playlist.MetaAlbumID) }},
		{"results read-only", "results", func(m *Model) { m.spotSearch.prov = commandsTestProvider{} }},
		{"typing query", "results", func(m *Model) { m.spotSearch.screen = spotSearchInput }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, p := favoriteAlbumTestModel(tt.view)
			tt.setup(&m)
			mode, _ := m.keymapContext()
			if help := m.commandHelp(mode); strings.Contains(help, "Favorite") {
				t.Fatalf("ineligible selection advertises favorite: %q", help)
			}
			if cmd := m.handleKey(tea.KeyPressMsg{Text: "f"}); cmd != nil || len(p.toggled) != 0 || m.status.text != "" {
				t.Fatalf("ineligible selection triggered favorite: IDs=%v status=%q", p.toggled, m.status.text)
			}
		})
	}
}
