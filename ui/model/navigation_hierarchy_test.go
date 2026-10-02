package model

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/charmbracelet/x/ansi"
)

func TestNavHeaderIncludesSourceBreadcrumb(t *testing.T) {
	m := Model{
		layout: frameLayout{panelWidth: 80},
		navBrowser: navBrowserState{
			prov:      commandsTestProvider{name: "Navidrome"},
			mode:      navBrowseModeByArtistAlbum,
			screen:    navBrowseScreenTracks,
			selArtist: provider.ArtistInfo{Name: "Miles Davis"},
			selAlbum:  provider.AlbumInfo{Name: "Kind of Blue"},
			tracks:    make([]playlist.Track, 1),
		},
	}

	plain := ansi.Strip(m.navHeaderLine())
	want := "Navidrome / Miles Davis / Kind of Blue / Tracks"
	if !strings.Contains(plain, want) {
		t.Fatalf("nav header = %q, want breadcrumb %q", plain, want)
	}
}

func TestKeymapStartsWithCurrentScreenCommands(t *testing.T) {
	m := Model{navBrowser: navBrowserState{visible: true, mode: navBrowseModeMenu}}
	entries := m.buildKeymapEntries()
	if len(entries) < 2 || !entries[0].divider || entries[0].action != "— current: Browse —" {
		t.Fatalf("keymap starts with %+v, want current Browse section", entries)
	}
	if !strings.Contains(entries[1].action, "Back") && !strings.Contains(entries[1].action, "Help") {
		t.Fatalf("first current-screen keymap entry = %+v, want browser command", entries[1])
	}
}

func TestNavFilterHeaderKeepsInputVisible(t *testing.T) {
	m := Model{
		layout: frameLayout{panelWidth: 40},
		navBrowser: navBrowserState{
			prov:      commandsTestProvider{name: "A very long provider name"},
			mode:      navBrowseModeByArtistAlbum,
			screen:    navBrowseScreenTracks,
			selArtist: provider.ArtistInfo{Name: "An extremely long artist name"},
			selAlbum:  provider.AlbumInfo{Name: "An extremely long album name"},
			searching: true,
			search:    "find",
		},
	}

	plain := ansi.Strip(m.navHeaderLine())
	if !strings.Contains(plain, "find_") {
		t.Fatalf("nav filter header = %q, want visible query cursor", plain)
	}
	if width := lipgloss.Width(plain); width > m.layout.panelWidth {
		t.Fatalf("nav filter width = %d, want <= %d", width, m.layout.panelWidth)
	}
}

func TestKeymapIdentifiesProviderFilter(t *testing.T) {
	m := Model{focus: focusProvider, provSearch: provSearchState{active: true}}
	entries := m.buildKeymapEntries()
	if len(entries) == 0 || entries[0].action != "— current: Provider Filter —" {
		t.Fatalf("keymap starts with %+v, want provider-filter context", entries)
	}
}
