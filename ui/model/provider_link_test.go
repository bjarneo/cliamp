package model

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
)

// waveProvider serves one refreshable playlist, "wave", like Yandex's
// endless wave.
type waveProvider struct{ refreshes int }

func (*waveProvider) Name() string                                { return "Wave" }
func (*waveProvider) Playlists() ([]playlist.PlaylistInfo, error) { return nil, nil }
func (*waveProvider) Tracks(string) ([]playlist.Track, error)     { return nil, nil }
func (w *waveProvider) Refresh()                                  { w.refreshes++ }
func (*waveProvider) CanRefreshPlaylist(id string) bool           { return id == "wave" }

// savedPlaylists serves every saved playlist as x.mp3 and y.mp3.
type savedPlaylists struct{ waveProvider }

func (*savedPlaylists) Name() string { return "Local" }
func (*savedPlaylists) Tracks(string) ([]playlist.Track, error) {
	return []playlist.Track{{Title: "X", Path: "x.mp3"}, {Title: "Y", Path: "y.mp3"}}, nil
}

// Once another queue replaces a loaded provider playlist, the provider list no
// longer marks that playlist as loaded and Ctrl+R in the playlist no longer
// reloads it over the new queue.
func TestReplacingQueueForgetsProviderPlaylist(t *testing.T) {
	wave := &waveProvider{}
	m := Model{
		player:        &playbackFakeEngine{},
		playlist:      playlist.New(),
		provider:      wave,
		providers:     []ProviderEntry{{Name: "Wave", Provider: wave}},
		localProvider: &savedPlaylists{},
		focus:         focusPlaylist,
		configSaver:   &recordingConfigSaver{},
	}
	ctrlR := tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl}

	next, _ := m.Update(tracksLoadedMsg{tracks: []playlist.Track{{Title: "W", Path: "w.mp3"}}, playlistID: "wave", providerName: "Wave", gen: m.requests.tracks})
	m = next.(Model)
	if !m.isProviderRowActive(playlist.PlaylistInfo{ID: "wave"}) {
		t.Fatal("the loaded provider playlist is not marked as loaded")
	}
	next, _ = m.Update(ctrlR)
	m = next.(Model)
	if wave.refreshes != 1 {
		t.Fatalf("Ctrl+R on the loaded provider playlist refreshed %d times, want 1", wave.refreshes)
	}
	m.provLoading = false

	next, _ = m.Update(ipc.LoadMsg{Playlist: "mine"})
	m = next.(Model)
	if m.isProviderRowActive(playlist.PlaylistInfo{ID: "wave"}) {
		t.Fatal("the provider playlist is still marked as loaded after another queue replaced it")
	}
	next, _ = m.Update(ctrlR)
	m = next.(Model)
	if wave.refreshes != 1 {
		t.Fatalf("Ctrl+R after another queue replaced the provider playlist refreshed it (%d)", wave.refreshes)
	}
	var got []string
	for _, tr := range m.playlist.Tracks() {
		got = append(got, tr.Path)
	}
	if strings.Join(got, " ") != "x.mp3 y.mp3" {
		t.Fatalf("queue = %v, want the loaded playlist kept", got)
	}
}
