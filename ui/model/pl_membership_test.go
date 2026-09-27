package model

import (
	"maps"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/playlist"
)

// membershipTestProvider keeps playlist membership in memory: lists maps a
// playlist name to path -> locked (supplied by a [[dir]] source).
type membershipTestProvider struct {
	lists   map[string]map[string]bool
	favs    map[string]bool
	lookups int
	removed []string
}

func newMembershipTestProvider() *membershipTestProvider {
	return &membershipTestProvider{
		lists: map[string]map[string]bool{
			"Chill":  {"/a.mp3": false},
			"Folder": {"/a.mp3": true},
			"Gym":    {"/b.mp3": false},
		},
		favs: map[string]bool{},
	}
}

func (p *membershipTestProvider) Name() string { return "Local" }

func (p *membershipTestProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	infos := []playlist.PlaylistInfo{{ID: favorites.PlaylistName, Name: favorites.PlaylistName}}
	for _, name := range []string{"Chill", "Folder", "Gym"} {
		infos = append(infos, playlist.PlaylistInfo{ID: name, Name: name})
	}
	return infos, nil
}

func (p *membershipTestProvider) Tracks(string) ([]playlist.Track, error) { return nil, nil }

func (p *membershipTestProvider) PlaylistMembership(path string) (map[string]bool, error) {
	p.lookups++
	member := map[string]bool{}
	if p.favs[path] {
		member[favorites.PlaylistName] = false
	}
	for name, paths := range p.lists {
		if locked, ok := paths[path]; ok {
			member[name] = locked
		}
	}
	return member, nil
}

func (p *membershipTestProvider) RemoveTrackByPath(name, path string) error {
	delete(p.lists[name], path)
	p.removed = append(p.removed, name)
	return nil
}

func (p *membershipTestProvider) ToggleFavorite(t playlist.Track) (bool, error) {
	p.favs[t.Path] = !p.favs[t.Path]
	return p.favs[t.Path], nil
}

func (p *membershipTestProvider) IsFavorited(path string) bool { return p.favs[path] }
func (p *membershipTestProvider) FavoritesCount() int          { return len(p.favs) }

func newMembershipTestModel(prov *membershipTestProvider) Model {
	m := newColumnTestModel(80, 24)
	m.localProvider, m.favMgr = prov, prov
	m.plMembers = &playlistMembershipCache{}
	m.playlist.SetTrack(0, playlist.Track{Path: "/a.mp3", Title: "A"})
	m.playlist.SetTrack(1, playlist.Track{Path: "/b.mp3", Title: "B"})
	m.focus, m.plCursor = focusPlaylist, 0
	return m
}

// pickRow moves the picker cursor to the named playlist and presses Enter.
func pickRow(t *testing.T, m *Model, name string) {
	t.Helper()
	m.handleKey(tea.KeyPressMsg{Text: "w"})
	if !m.plPicker.visible {
		t.Fatal("w did not open the playlist picker")
	}
	for i, pl := range m.plPicker.playlists {
		if pl.Name == name {
			m.plPicker.cursor = i
			m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
			return
		}
	}
	t.Fatalf("picker has no %q row", name)
}

func TestPlaylistPickerMembershipToggle(t *testing.T) {
	tests := []struct {
		name        string
		row         string
		wantRemoved []string
		wantFav     bool
		wantStatus  string
	}{
		{name: "checked row removes", row: "Chill", wantRemoved: []string{"Chill"}, wantStatus: `Removed from "Chill"`},
		{name: "locked row refuses", row: "Folder", wantStatus: "from a folder"},
		{name: "favorites adds", row: favorites.PlaylistName, wantFav: true, wantStatus: `Added to "Favorites"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prov := newMembershipTestProvider()
			m := newMembershipTestModel(prov)
			pickRow(t, &m, tt.row)
			if !slices.Equal(prov.removed, tt.wantRemoved) {
				t.Errorf("removed = %v, want %v", prov.removed, tt.wantRemoved)
			}
			if prov.favs["/a.mp3"] != tt.wantFav {
				t.Errorf("favorited = %v, want %v", prov.favs["/a.mp3"], tt.wantFav)
			}
			if !strings.Contains(m.status.text, tt.wantStatus) {
				t.Errorf("status = %q, want it to contain %q", m.status.text, tt.wantStatus)
			}
			if m.plPicker.visible {
				t.Error("picker stayed open after Enter")
			}
		})
	}
}

func TestPlaylistPickerMarksMembership(t *testing.T) {
	m := newMembershipTestModel(newMembershipTestProvider())
	m.handleKey(tea.KeyPressMsg{Text: "w"})
	want := map[string]bool{"Chill": false, "Folder": true}
	if !maps.Equal(m.plPicker.member, want) {
		t.Fatalf("member = %v, want %v", m.plPicker.member, want)
	}
	body := ansi.Strip(m.renderPlaylistPickerBody())
	for _, line := range strings.Split(body, "\n") {
		checked := strings.HasPrefix(strings.TrimSpace(line), membershipCheck)
		for _, name := range []string{"Chill", "Folder", "Gym"} {
			if strings.Contains(line, name) && checked != (name != "Gym") {
				t.Errorf("row %q checked = %v:\n%s", name, checked, body)
			}
		}
	}

	// A batch has no single membership and keeps the plain list.
	m.openPlaylistPicker([]playlist.Track{{Path: "/a.mp3"}, {Path: "/b.mp3"}}, "")
	if m.plPicker.member != nil || strings.Contains(m.renderPlaylistPickerBody(), membershipCheck) {
		t.Fatal("batch picker showed membership marks")
	}
}

func TestPlaylistsPaneToggle(t *testing.T) {
	prov := newMembershipTestProvider()
	m := newMembershipTestModel(prov)
	m.handleKey(tea.KeyPressMsg{Code: 'i', Mod: tea.ModCtrl})
	if !m.showMetadata {
		t.Fatal("Ctrl+I did not show metadata")
	}
	m.handleKey(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	if !m.showPlaylists || m.showMetadata {
		t.Fatalf("Ctrl+L: showPlaylists=%v showMetadata=%v, want true false", m.showPlaylists, m.showMetadata)
	}

	pane := ansi.Strip(m.renderSettingsPane(m.effectivePlaylistVisible()))
	for _, want := range []string{"Playlists [Ctrl+L]", "Chill", "Folder"} {
		if !strings.Contains(pane, want) {
			t.Fatalf("pane lacks %q:\n%s", want, pane)
		}
	}
	if strings.Contains(pane, "Gym") {
		t.Fatalf("pane lists a playlist the track is not in:\n%s", pane)
	}

	// Rendering again for the same track reuses the cached lookup.
	lookups := prov.lookups
	m.renderSettingsPane(m.effectivePlaylistVisible())
	if prov.lookups != lookups {
		t.Fatalf("same track re-read playlists: %d lookups, want %d", prov.lookups, lookups)
	}

	m.plCursor = 1
	pane = ansi.Strip(m.renderSettingsPane(m.effectivePlaylistVisible()))
	if !strings.Contains(pane, "Gym") || strings.Contains(pane, "Chill") {
		t.Fatalf("pane did not follow the highlight:\n%s", pane)
	}

	m.handleKey(tea.KeyPressMsg{Code: 'i', Mod: tea.ModCtrl})
	if m.showPlaylists || !m.showMetadata {
		t.Fatalf("Ctrl+I: showPlaylists=%v showMetadata=%v, want false true", m.showPlaylists, m.showMetadata)
	}
}
