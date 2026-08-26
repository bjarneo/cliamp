package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

type commandsTestProvider struct {
	name  string
	lists []playlist.PlaylistInfo
}

func (p commandsTestProvider) Name() string { return p.name }

func (p commandsTestProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	return append([]playlist.PlaylistInfo(nil), p.lists...), nil
}

func (p commandsTestProvider) Tracks(string) ([]playlist.Track, error) { return nil, nil }

type playlistManagerTestProvider struct {
	commandsTestProvider
	saveName string
	saved    []playlist.Track
}

func (p *playlistManagerTestProvider) SavePlaylist(name string, tracks []playlist.Track) error {
	p.saveName = name
	p.saved = append([]playlist.Track(nil), tracks...)
	return nil
}

func TestFetchSpotPlaylistsFiltersHistoryOnlyForLocal(t *testing.T) {
	lists := []playlist.PlaylistInfo{
		{ID: "recent", Name: history.PlaylistName},
		{ID: "mix", Name: "Mix"},
	}

	msg := fetchSpotPlaylistsCmd(commandsTestProvider{name: "Spotify", lists: lists}, 1)().(spotPlaylistsMsg)
	if len(msg.playlists) != 2 {
		t.Fatalf("Spotify playlists = %d, want 2", len(msg.playlists))
	}

	msg = fetchSpotPlaylistsCmd(commandsTestProvider{name: "Local", lists: lists}, 2)().(spotPlaylistsMsg)
	if len(msg.playlists) != 1 || msg.playlists[0].Name != "Mix" {
		t.Fatalf("Local playlists = %+v, want only Mix", msg.playlists)
	}
}

func TestTracksLoadedMsgMarksOnlyExactLocalPlaylist(t *testing.T) {
	player := &playbackFakeEngine{}
	m := Model{
		player:        player,
		playlist:      playlist.New(),
		localProvider: commandsTestProvider{name: "Local"},
		provider:      commandsTestProvider{name: "Local"},
		vis:           ui.NewVisualizer(float64(player.SampleRate())),
	}
	m.requests.tracks = 1

	updated, _ := m.Update(tracksLoadedMsg{
		tracks:        []playlist.Track{{Path: "/a.mp3", Title: "A"}},
		playlistID:    "mix",
		providerName:  "Local",
		playlistExact: true,
		gen:           1,
	})
	m = updated.(Model)
	if m.loadedPlaylist != "mix" {
		t.Fatalf("loadedPlaylist = %q, want mix", m.loadedPlaylist)
	}

	updated, _ = m.Update(tracksLoadedMsg{
		tracks:        []playlist.Track{{Path: "https://example.com/stream", Title: "Stream", Stream: true}},
		playlistID:    "mix",
		providerName:  "Local",
		playlistExact: false,
		gen:           1,
	})
	m = updated.(Model)
	if m.loadedPlaylist != "" {
		t.Fatalf("loadedPlaylist = %q, want empty after expanded playlist load", m.loadedPlaylist)
	}
}

func TestPlaylistManagerTrackSortUsesLowercaseKey(t *testing.T) {
	player := &playbackFakeEngine{}
	local := &playlistManagerTestProvider{commandsTestProvider: commandsTestProvider{name: "Local"}}
	m := Model{
		player:        player,
		playlist:      playlist.New(),
		localProvider: local,
		provider:      local,
		providers: []ProviderEntry{
			{Key: "spotify", Name: "Spotify", Provider: commandsTestProvider{name: "Spotify"}},
		},
		vis: ui.NewVisualizer(float64(player.SampleRate())),
		plManager: plManagerState{
			visible:     true,
			screen:      plMgrScreenTracks,
			selPlaylist: "mix",
			tracks: []playlist.Track{
				{Path: "/b.mp3", Title: "B"},
				{Path: "/a.mp3", Title: "A"},
			},
		},
	}

	cmd := m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "s"})
	if cmd != nil {
		t.Fatal("s returned command; want playlist sort only")
	}
	if !m.plManager.visible {
		t.Fatal("playlist manager was closed; want it to stay open")
	}
	if local.saveName != "mix" {
		t.Fatalf("SavePlaylist name = %q, want mix", local.saveName)
	}
	if len(local.saved) != 2 || local.saved[0].Title != "A" || local.saved[1].Title != "B" {
		t.Fatalf("saved tracks = %+v, want sorted by title", local.saved)
	}
}

// TestURLOverlayLoadsRawStream pins the u overlay end to end: a stream address
// entered there must load as a track, the way the same address does as a
// command-line argument. The overlay used to hand its input to resolve.Remote,
// whose default arm assumes the URL was already classified as a feed, so a raw
// stream was parsed as XML and failed.
func TestURLOverlayLoadsRawStream(t *testing.T) {
	const raw = "https://example.com/live.mp3"

	m := Model{urlInputting: true, urlInput: raw}

	cmd := m.handleURLInputKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on a non-empty URL returned no command")
	}

	loaded, ok := cmd().(feedsLoadedMsg)
	if !ok {
		msg := cmd()
		t.Fatalf("url overlay produced %T (%v), want feedsLoadedMsg", msg, msg)
	}
	if len(loaded.tracks) != 1 {
		t.Fatalf("loaded %d tracks, want 1", len(loaded.tracks))
	}
	if loaded.tracks[0].Path != raw {
		t.Fatalf("track path = %q, want %q", loaded.tracks[0].Path, raw)
	}
	if len(loaded.urls) != 1 || loaded.urls[0] != raw {
		t.Fatalf("urls = %#v, want [%q] so incremental loading still sees the source", loaded.urls, raw)
	}
	if !loaded.autoPlay {
		t.Fatal("autoPlay = false, want true")
	}
}
