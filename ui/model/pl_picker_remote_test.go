package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

func TestPlaylistPickerRemoteSectionFromActiveProvider(t *testing.T) {
	fake := &fakeSpotifyLibProvider{name: "Spotify", lists: []playlist.PlaylistInfo{
		{ID: "YOUR MUSIC", Name: "Liked Songs"},
		{ID: "pl1", Name: "Road Trip", TrackCount: 12},
	}}
	m := newSpotifyTestModel(fake)
	m.providerLists = fake.lists

	cmd := m.openPlaylistPicker([]playlist.Track{{Path: "spotify:track:1", Title: "Dreams"}}, "Track: Dreams")
	if cmd != nil {
		t.Fatal("active provider section should come from providerLists without a fetch")
	}
	if m.plPicker.remoteName != "Spotify" || m.plPicker.remoteLoading {
		t.Fatalf("remote section = %q loading=%v", m.plPicker.remoteName, m.plPicker.remoteLoading)
	}
	if len(m.plPicker.remote) != 1 || m.plPicker.remote[0].ID != "pl1" {
		t.Fatalf("remote = %+v; want synthetic IDs filtered", m.plPicker.remote)
	}

	// Local section stays first; remote section appended after local "+ New".
	foundLocal := -1
	foundRemote := -1
	for i, item := range m.plPickerItems() {
		if item.header && item.label == "Local Playlists" {
			foundLocal = i
		}
		if item.header && item.label == "Spotify Playlists" {
			foundRemote = i
		}
	}
	if foundLocal != 0 {
		t.Fatalf("Local header at %d; want 0 (local first)", foundLocal)
	}
	if foundRemote <= foundLocal {
		t.Fatalf("Spotify header at %d; want after local section", foundRemote)
	}
}

func TestPlaylistPickerRemoteSectionFetchesInBackground(t *testing.T) {
	fake := &fakeSpotifyLibProvider{name: "Spotify", lists: []playlist.PlaylistInfo{
		{ID: "TOP TRACKS", Name: "Top"},
		{ID: "pl1", Name: "Road Trip"},
	}}
	m := newSpotifyTestModel(fake)
	m.provider = commandsTestProvider{name: "Local"} // spotify not active
	m.providers = []ProviderEntry{{Key: "spotify", Name: "Spotify", Provider: fake}}

	cmd := m.openPlaylistPicker([]playlist.Track{{Path: "spotify:track:1"}}, "Track: X")
	if cmd == nil {
		t.Fatal("inactive provider section should trigger a background fetch")
	}
	if !m.plPicker.remoteLoading {
		t.Fatal("remote section should be loading")
	}
	updated, _ := m.Update(cmd())
	m = updated.(Model)
	if m.plPicker.remoteLoading || len(m.plPicker.remote) != 1 || m.plPicker.remote[0].ID != "pl1" {
		t.Fatalf("remote = %+v loading=%v; want filtered fetch result", m.plPicker.remote, m.plPicker.remoteLoading)
	}
}

func TestPlaylistPickerRemoteWrite(t *testing.T) {
	fake := &fakeSpotifyLibProvider{name: "Spotify", lists: []playlist.PlaylistInfo{{ID: "pl1", Name: "Road Trip"}}}
	m := newSpotifyTestModel(fake)
	m.localProvider = commandsTestProvider{name: "Local", lists: []playlist.PlaylistInfo{{ID: "mix", Name: "Mix"}}}
	m.providerLists = fake.lists
	tracks := []playlist.Track{{Path: "spotify:track:1", Title: "Dreams"}}
	m.openPlaylistPicker(tracks, "Track: Dreams")

	// Selectable rows: local playlists (0), +New local (1), pl1 (2), +New remote (3).
	item, ok := m.plPickerItemAt(2)
	if !ok || !item.remote || item.playlist.ID != "pl1" {
		t.Fatalf("item at 2 = %+v; want remote pl1", item)
	}
	m.plPicker.cursor = 2
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.plPicker.visible {
		t.Fatal("picker should close optimistically on remote write")
	}
	msg, ok := cmd().(pickerRemoteWriteMsg)
	if !ok {
		t.Fatalf("enter produced %T", cmd())
	}
	if len(fake.batchAdds) != 1 || fake.batchAdds[0] != "pl1" {
		t.Fatalf("batchAdds = %v", fake.batchAdds)
	}
	if msg.added != 1 {
		t.Fatalf("added = %d, want 1", msg.added)
	}
}

func TestPlaylistPickerRemoteCreate(t *testing.T) {
	fake := &fakeSpotifyLibProvider{name: "Spotify", lists: []playlist.PlaylistInfo{{ID: "pl1", Name: "Road Trip"}}}
	m := newSpotifyTestModel(fake)
	m.providerLists = fake.lists
	m.openPlaylistPicker([]playlist.Track{{Path: "spotify:track:1"}}, "Track: Dreams")

	// Last selectable row is "+ New Spotify Playlist...".
	m.plPicker.cursor = m.plPickerCount() - 1
	item, _ := m.plPickerItemAt(m.plPicker.cursor)
	if !item.isNew || !item.remote {
		t.Fatalf("last item = %+v; want remote +New", item)
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if !m.plPicker.visible || m.plPicker.screen != plPickerNewName || !m.plPicker.newNameRemote {
		t.Fatalf("picker = visible=%v screen=%d remoteName=%v", m.plPicker.visible, m.plPicker.screen, m.plPicker.newNameRemote)
	}
	m.plPicker.newName = "Mixtape"
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	msg, ok := cmd().(pickerRemoteWriteMsg)
	if !ok || !msg.created {
		t.Fatalf("enter produced %+v; want created write", msg)
	}
	if len(fake.created) != 1 || fake.created[0] != "Mixtape" {
		t.Fatalf("created = %v", fake.created)
	}
	if len(fake.batchAdds) != 1 || fake.batchAdds[0] != "new-Mixtape" {
		t.Fatalf("batchAdds = %v; want new playlist ID", fake.batchAdds)
	}
}

func TestPlaylistPickerEscReturnsToOriginatingNewRow(t *testing.T) {
	fake := &fakeSpotifyLibProvider{name: "Spotify", lists: []playlist.PlaylistInfo{{ID: "pl1", Name: "Road Trip"}}}
	m := newSpotifyTestModel(fake)
	m.localProvider = commandsTestProvider{name: "Local", lists: []playlist.PlaylistInfo{{ID: "mix", Name: "Mix"}}}
	m.providerLists = fake.lists
	m.openPlaylistPicker([]playlist.Track{{Path: "spotify:track:1"}}, "Track: X")

	// Selectable rows: mix (0), +New local (1), pl1 (2), +New remote (3).
	localNew := len(m.plPicker.playlists)
	if item, _ := m.plPickerItemAt(localNew); !item.isNew || item.remote {
		t.Fatalf("item at %d = %+v; want local +New row", localNew, item)
	}
	m.plPicker.cursor = localNew
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.plPicker.screen != plPickerNewName || m.plPicker.newNameRemote {
		t.Fatalf("screen = %d remote = %v; want local new-name input", m.plPicker.screen, m.plPicker.newNameRemote)
	}

	// Esc must land back on the local +New row, not the remote one.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.plPicker.screen != plPickerChoose {
		t.Fatal("Esc should return to the chooser")
	}
	if m.plPicker.cursor != localNew {
		t.Fatalf("cursor = %d; want the originating local +New row %d", m.plPicker.cursor, localNew)
	}
}

func TestPlaylistPickerSkipsRemoteForMixedOrLocalTracks(t *testing.T) {
	tests := []struct {
		name   string
		tracks []playlist.Track
	}{
		{"local files", []playlist.Track{{Path: "/a.mp3"}}},
		{"mixed schemes", []playlist.Track{{Path: "spotify:track:1"}, {Path: "/a.mp3"}}},
		{"empty", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeSpotifyLibProvider{name: "Spotify"}
			m := newSpotifyTestModel(fake)
			cmd := m.openPlaylistPicker(tt.tracks, "Selection")
			if cmd != nil || m.plPicker.remoteName != "" {
				t.Fatalf("remote section = %q cmd=%v; want none", m.plPicker.remoteName, cmd != nil)
			}
		})
	}
}
