package model

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// TestSearchInputsShowModeAndExitKey checks that every search or filter input
// names its mode and shows the key that exits it, in the same badge style.
func TestSearchInputsShowModeAndExitKey(t *testing.T) {
	searcher := &catalogTestProvider{commandsTestProvider: commandsTestProvider{name: "Spotify"}}
	tests := []struct {
		name   string
		setup  func(*Model)
		header func(*Model) string
		want   string
	}{
		{
			name:   "playlist search",
			setup:  func(m *Model) { m.search.active = true; m.search.query = "jazz" },
			header: (*Model).searchHeaderLine,
			want:   "[Filter: Playlist] jazz_",
		},
		{
			name:   "file browser filter",
			setup:  func(m *Model) { m.fileBrowser.searching = true; m.fileBrowser.search = "mp3" },
			header: (*Model).fbHeaderLine,
			want:   "[Filter: Files] mp3_",
		},
		{
			name: "provider browser filter",
			setup: func(m *Model) {
				m.navBrowser = navBrowserState{prov: commandsTestProvider{name: "Navidrome"}, mode: navBrowseModeByAlbum, searching: true, search: "blue"}
			},
			header: (*Model).navHeaderLine,
			want:   "[Filter: Navidrome",
		},
		{
			name:   "Ctrl+F provider search",
			setup:  func(m *Model) { m.openProviderSearchWith(searcher) },
			header: (*Model).spotSearchHeaderLine,
			want:   "[Search: Spotify] _",
		},
		{
			name:   "Ctrl+F YouTube fallback",
			setup:  func(m *Model) { m.openProviderSearchWith(commandsTestProvider{name: "Radio"}) },
			header: (*Model).netSearchHeaderLine,
			want:   "[Search: YouTube] _",
		},
		{
			name: "provider pane filter",
			setup: func(m *Model) {
				m.focus = focusProvider
				m.providerLists = []playlist.PlaylistInfo{{ID: "1", Name: "Mix"}}
				m.provSearch = provSearchState{active: true, query: "mi"}
			},
			header: (*Model).renderProviderList,
			want:   "[Filter: Local] mi_",
		},
		{
			name: "radio search",
			setup: func(m *Model) {
				m.provider = &catalogTestProvider{commandsTestProvider: commandsTestProvider{name: "Radio"}}
				m.focus = focusProvider
				m.providerLists = []playlist.PlaylistInfo{{ID: "c:1", Name: "Station"}}
				m.provSearch = provSearchState{active: true, query: "rock"}
			},
			header: (*Model).renderProviderList,
			want:   "[Search: Radio] rock_",
		},
	}
	for _, width := range []int{40, 80} {
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				old := ui.PanelWidth
				ui.PanelWidth = width
				t.Cleanup(func() { ui.PanelWidth = old })

				m := keybindingTestModel()
				m.plVisible = 6
				tt.setup(&m)
				line, _, _ := strings.Cut(tt.header(&m), "\n")
				plain := ansi.Strip(line)
				if !strings.Contains(plain, tt.want) {
					t.Fatalf("width %d: header = %q, want %q", width, plain, tt.want)
				}
				if !strings.Contains(plain, "Esc") {
					t.Fatalf("width %d: header = %q, want the Esc exit key", width, plain)
				}
				if got := lipgloss.Width(line); got > width {
					t.Fatalf("width %d: header is %d columns wide: %q", width, got, plain)
				}
			})
		}
	}
}

func TestCtrlFFallbackNamesTheProviderWithoutSearch(t *testing.T) {
	m := keybindingTestModel()
	m.plVisible = 6
	m.provider = commandsTestProvider{name: "Radio"}

	m.openProviderSearch()

	if !m.netSearch.active || m.netSearch.from != "Radio" {
		t.Fatalf("net search = %+v, want an active fallback from Radio", m.netSearch)
	}
	body := ansi.Strip(m.renderNetSearchBody())
	if !strings.Contains(body, "Radio has no Ctrl+F search. This searches YouTube.") {
		t.Fatalf("fallback body = %q, want it to name Radio and YouTube", body)
	}
}

func TestProviderSearchHelpShowsHowToLeave(t *testing.T) {
	old := ui.PanelWidth
	ui.PanelWidth = 80
	t.Cleanup(func() { ui.PanelWidth = old })

	cs := &catalogTestProvider{commandsTestProvider: commandsTestProvider{name: "Radio"}}
	tests := []struct {
		name      string
		setup     func(*Model)
		want      string
		wantLabel string
	}{
		{name: "typing a query", setup: func(m *Model) { m.provSearch.active = true }, want: "Cancel", wantLabel: "Radio / Playlists / Search"},
		{name: "search results", setup: func(m *Model) { cs.results = []playlist.PlaylistInfo{{ID: "s:1", Name: "Hit"}} }, want: "Clear search", wantLabel: "Radio / Playlists / Search results"},
		{name: "search in flight", setup: func(m *Model) { cs.results = nil; m.provSearch.loading = true }, want: "Clear search", wantLabel: "Radio / Playlists / Search results"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			m.provider = cs
			m.focus = focusProvider
			tt.setup(&m)
			if help := ansi.Strip(m.renderHelp()); !strings.Contains(help, "Esc") || !strings.Contains(help, tt.want) {
				t.Fatalf("help = %q, want Esc %s", help, tt.want)
			}
			if header := ansi.Strip(m.renderPlaylistHeader()); !strings.Contains(header, tt.wantLabel) {
				t.Fatalf("header = %q, want %q", header, tt.wantLabel)
			}
		})
	}
}
