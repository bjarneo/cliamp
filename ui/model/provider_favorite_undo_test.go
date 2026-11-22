package model

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/external/radio"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

func TestProviderFavoriteRemovalUndo(t *testing.T) {
	m, p := favoriteAlbumTestModel("pane")
	m.providers = []provider.Entry{{Key: "podcast", Name: p.Name(), Provider: p}}
	p.favorite = true
	p.lists = nil

	m.handleKey(tea.KeyPressMsg{Text: "f"})
	if !m.providerFavoriteUndo.active || m.providerFavoriteUndo.id != "s:target" {
		t.Fatalf("undo slot = %+v, want active removal of s:target", m.providerFavoriteUndo)
	}
	if !strings.Contains(m.status.text, "Ctrl+Z") {
		t.Fatalf("status = %q, want undo hint", m.status.text)
	}

	p.favorite = false
	p.lists = []playlist.PlaylistInfo{{ID: "s:target", Name: "Target Show"}}
	m.handleKey(tea.KeyPressMsg{Text: "ctrl+z", Mod: tea.ModCtrl, Code: 'z'})
	if m.providerFavoriteUndo.active {
		t.Fatal("undo slot still active after restore")
	}
	if !p.favorite || m.status.text != "Restored: Target Show" {
		t.Fatalf("after undo: favorite=%v status=%q", p.favorite, m.status.text)
	}
	if !slices.ContainsFunc(m.provPane.lists, func(pl playlist.PlaylistInfo) bool { return pl.ID == "s:target" }) {
		t.Fatalf("provider pane = %+v, want restored row", m.provPane.lists)
	}
	if m.provPane.lists[m.provPane.cursor].ID != "s:target" {
		t.Fatalf("cursor on %q, want s:target", m.provPane.lists[m.provPane.cursor].ID)
	}
}

func TestRadioFavoriteListRemovalUndo(t *testing.T) {
	m, p, tracks := radioFavoriteTestModel(t)
	if _, _, err := p.ToggleFavorite("c:0"); err != nil {
		t.Fatal(err)
	}
	lists, err := p.Playlists()
	if err != nil {
		t.Fatal(err)
	}
	m.replaceProviderLists(lists)
	m.focus = focusProvider
	favID := "f:" + tracks[0].Path
	for i, list := range m.provPane.lists {
		if list.ID == favID {
			m.provPane.cursor = i
			break
		}
	}
	m.handleKey(tea.KeyPressMsg{Text: "f"})
	if m.providerFavoriteUndo.stationURL != tracks[0].Path || m.providerFavoriteUndo.id != "" {
		t.Fatalf("undo slot = %+v, want station URL %+v", m.providerFavoriteUndo, tracks[0].Path)
	}
	if m.radioFavorites.Contains(tracks[0].Path) {
		t.Fatal("station should be unfavorited")
	}

	m.handleKey(tea.KeyPressMsg{Text: "ctrl+z", Mod: tea.ModCtrl, Code: 'z'})
	if !m.radioFavorites.Contains(tracks[0].Path) {
		t.Fatal("undo failed after removing a favorite-row entry")
	}
	if m.provPane.lists[m.provPane.cursor].ID != favID {
		t.Fatalf("cursor on %q, want %q", m.provPane.lists[m.provPane.cursor].ID, favID)
	}
}

func TestRadioStationFavoriteRemovalUndo(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	p := radio.New(radio.Options{Country: radio.CountryDeclined})
	favorites := radio.LoadFavorites()
	station := radio.CatalogStation{Name: "Jazz FM", URL: "https://stream.example/jazz"}
	if _, err := favorites.Toggle(station); err != nil {
		t.Fatal(err)
	}
	m := keybindingTestModel()
	m.provider = p
	m.providers = []provider.Entry{{Key: providerKeyRadio, Name: p.Name(), Provider: p}}
	m.radioFavorites = favorites
	m.focus = focusPlaylist
	track := playlist.Track{
		Path: station.URL, Stream: true, Realtime: true,
		ProviderMeta: map[string]string{"radio.name": station.Name, "radio.url": station.URL},
	}

	m.togglePlaylistTrackFavorite(track, false)
	if !m.providerFavoriteUndo.active || m.providerFavoriteUndo.stationURL != station.URL {
		t.Fatalf("undo slot = %+v", m.providerFavoriteUndo)
	}
	if favorites.Contains(station.URL) {
		t.Fatal("station should be unfavorited before undo")
	}

	m.handleKey(tea.KeyPressMsg{Text: "ctrl+z", Mod: tea.ModCtrl, Code: 'z'})
	if !favorites.Contains(station.URL) {
		t.Fatal("undo did not restore station favorite")
	}
	if m.status.text != "Restored: "+station.Name {
		t.Fatalf("status = %q", m.status.text)
	}
}
