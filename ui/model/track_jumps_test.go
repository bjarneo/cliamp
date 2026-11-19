package model

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// fakeAlbumProvider owns "fk:" tracks: all of them are on one album, and its
// artist filter is "artist".
type fakeAlbumProvider struct {
	commandsTestProvider
	album []playlist.Track
}

func (p *fakeAlbumProvider) Playlists() ([]playlist.PlaylistInfo, error) { return p.lists, nil }
func (p *fakeAlbumProvider) Tracks(id string) ([]playlist.Track, error) {
	if id != "alb" {
		return nil, nil
	}
	return p.album, nil
}
func (p *fakeAlbumProvider) AlbumForTrack(t playlist.Track) (playlist.PlaylistInfo, bool) {
	if !strings.HasPrefix(t.Path, "fk:") {
		return playlist.PlaylistInfo{}, false
	}
	return playlist.PlaylistInfo{ID: "alb", Name: "Artist — Album"}, true
}
func (p *fakeAlbumProvider) ArtistFilter(t playlist.Track) (string, bool) {
	if !strings.HasPrefix(t.Path, "fk:") {
		return "", false
	}
	return "artist", true
}

// trackJumpModel plays x.mp3 from a queue of x.mp3, fk:2 and y.mp3, with the
// cursor on fk:2, whose album is fk:1 fk:2 fk:3.
func trackJumpModel() (Model, *playbackFakeEngine, *fakeAlbumProvider) {
	player := &playbackFakeEngine{playing: true}
	fake := &fakeAlbumProvider{
		commandsTestProvider: commandsTestProvider{name: "Fake", lists: []playlist.PlaylistInfo{
			{ID: "alb", Name: "Artist — Album (2001)"},
			{ID: "other", Name: "Other — Album (2002)"},
			{ID: "alb2", Name: "Artist — Second (2005)"},
		}},
		album: []playlist.Track{{Path: "fk:1"}, {Path: "fk:2"}, {Path: "fk:3"}},
	}
	p := playlist.New()
	p.Replace([]playlist.Track{{Path: "x.mp3"}, {Path: "fk:2"}, {Path: "y.mp3"}})
	p.SetIndex(0)
	m := Model{
		player:    player,
		playlist:  p,
		providers: []provider.Entry{{Name: "Fake", Provider: fake}},
		focus:     focusPlaylist,
		plCursor:  1,
	}
	m.playingTrack, m.playingTrackActive = playlist.Track{Path: "x.mp3"}, true
	return m, player, fake
}

var (
	altO = tea.KeyPressMsg{Code: 'o', Mod: tea.ModAlt}
	altL = tea.KeyPressMsg{Code: 'l', Mod: tea.ModAlt}
)

// Alt+O replaces the queue with the album of the highlighted track, puts the
// cursor on that track, leaves playback alone and can be undone.
func TestOpenTrackAlbum(t *testing.T) {
	m, player, fake := trackJumpModel()
	fake.album = []playlist.Track{{Path: "fk:1"}, {Path: "fk:3"}, {Path: "fk:2"}}

	next, cmd := m.Update(altO)
	m = next.(Model)
	if cmd == nil {
		t.Fatal("Alt+O on a track with an album started no load")
	}
	if got := jumpPaths(m.playlist.Tracks()); strings.Join(got, " ") != "x.mp3 fk:2 y.mp3" {
		t.Fatalf("queue changed before the album arrived: %v", got)
	}
	next, _ = m.Update(cmd())
	m = next.(Model)

	if got := jumpPaths(m.playlist.Tracks()); strings.Join(got, " ") != "fk:1 fk:3 fk:2" {
		t.Fatalf("queue = %v, want the album", got)
	}
	if m.plCursor != 2 || player.stopCalls != 0 || !m.playingTrackActive || m.playingTrack.Path != "x.mp3" {
		t.Fatalf("cursor %d, Stop %d, playing %q (%v); want the cursor on fk:2 and x.mp3 still playing",
			m.plCursor, player.stopCalls, m.playingTrack.Path, m.playingTrackActive)
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	m = next.(Model)
	if got := jumpPaths(m.playlist.Tracks()); strings.Join(got, " ") != "x.mp3 fk:2 y.mp3" || m.plCursor != 1 {
		t.Fatalf("Ctrl+Z queue = %v, cursor %d; want the old queue back with the cursor on fk:2", got, m.plCursor)
	}
}

// Alt+O makes the album the open provider playlist, or none when its
// provider is not the active one, so Ctrl+R and the resume context do not
// bring back the replaced playlist. Ctrl+Z reopens that playlist while its
// provider is still active.
func TestOpenTrackAlbumOpenPlaylist(t *testing.T) {
	navidrome := commandsTestProvider{name: "Navidrome"}
	tests := []struct {
		name      string
		active    string // "fake" or "navidrome"
		switchTo  bool   // another provider becomes active before Ctrl+Z
		wantAlbum string
		wantUndo  string
	}{
		{name: "album provider active", active: "fake", wantAlbum: "alb", wantUndo: "pl-x"},
		{name: "other provider active", active: "navidrome", wantAlbum: "", wantUndo: "pl-x"},
		{name: "provider switched before undo", active: "navidrome", switchTo: true, wantAlbum: "", wantUndo: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _, fake := trackJumpModel()
			m.provider = fake
			if tt.active == "navidrome" {
				m.provider = navidrome
			}
			m.activeProviderPlaylistID = "pl-x"

			next, cmd := m.Update(altO)
			m = next.(Model)
			next, _ = m.Update(cmd())
			m = next.(Model)
			if m.activeProviderPlaylistID != tt.wantAlbum {
				t.Fatalf("open playlist after Alt+O = %q, want %q", m.activeProviderPlaylistID, tt.wantAlbum)
			}
			if tt.switchTo {
				m.provider = commandsTestProvider{name: "Radio"}
			}
			next, _ = m.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			m = next.(Model)
			if m.activeProviderPlaylistID != tt.wantUndo {
				t.Fatalf("open playlist after Ctrl+Z = %q, want %q", m.activeProviderPlaylistID, tt.wantUndo)
			}
		})
	}
}

// Ctrl+Z after Alt+O brings back the album headers of the old queue too: a
// list of single tracks from many albums shows none.
func TestOpenTrackAlbumUndoRestoresHeaders(t *testing.T) {
	m, _, fake := trackJumpModel()
	m.playlist.Replace([]playlist.Track{{Path: "x.mp3", Album: "X"}, {Path: "fk:2", Album: "A"}, {Path: "y.mp3", Album: "Y"}})
	m.setHeaderStateFromTracks(m.playlist.Tracks())
	fake.album = []playlist.Track{{Path: "fk:1", Album: "A"}, {Path: "fk:2", Album: "A"}, {Path: "fk:3", Album: "A"}}

	next, cmd := m.Update(altO)
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if !m.showAlbumHeaders {
		t.Fatal("the album shows no album headers")
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	m = next.(Model)
	if m.showAlbumHeaders {
		t.Fatal("Ctrl+Z kept the album's headers on a list of single tracks")
	}
}

// A playing track on the album goes on as the album's current track.
func TestOpenTrackAlbumKeepsPlayingTrack(t *testing.T) {
	m, player, _ := trackJumpModel()
	m.playlist.SetIndex(1)
	m.playingTrack = playlist.Track{Path: "fk:2"}

	next, cmd := m.Update(altO)
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)

	if m.playlist.Index() != 1 || m.playbackDetached || player.stopCalls != 0 || len(player.playCalls) != 0 {
		t.Fatalf("index %d, detached %v, Stop %d, playCalls %v; want fk:2 current and untouched",
			m.playlist.Index(), m.playbackDetached, player.stopCalls, player.playCalls)
	}
}

// Alt+O and Alt+L do nothing on a track no provider places.
func TestTrackJumpsNeedAProvider(t *testing.T) {
	m, _, _ := trackJumpModel()
	m.plCursor = 0
	for _, key := range []tea.KeyPressMsg{altO, altL} {
		next, cmd := m.Update(key)
		if cmd != nil || next.(Model).focus != focusPlaylist {
			t.Fatalf("%s on x.mp3 did something", key.String())
		}
	}
}

// Alt+L opens the owning provider with the artist filter, which applies to
// the lists once they arrive.
func TestOpenTrackArtist(t *testing.T) {
	m, _, fake := trackJumpModel()

	next, _ := m.Update(altL)
	m = next.(Model)
	if m.focus != focusProvider || !m.isActiveProvider("Fake") {
		t.Fatalf("focus %v, provider %v; want the Fake browser", m.focus, m.provider)
	}
	if !m.provSearch.active || m.provSearch.query != "artist" {
		t.Fatalf("filter = %q (active %v), want artist", m.provSearch.query, m.provSearch.active)
	}

	m.replaceProviderLists(fake.lists)
	var got []string
	for _, i := range m.provSearch.results {
		got = append(got, m.provPane.lists[i].Name)
	}
	if strings.Join(got, "|") != "Artist — Album (2001)|Artist — Second (2005)" {
		t.Fatalf("filtered lists = %q, want the two Artist albums", got)
	}
}

func jumpPaths(tracks []playlist.Track) []string {
	out := make([]string, len(tracks))
	for i, t := range tracks {
		out[i] = t.Path
	}
	return out
}
