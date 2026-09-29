package model

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
)

// fakeRelater finds related songs for "fake:" paths.
type fakeRelater struct {
	tracks []playlist.Track
	err    error
	asked  []int // n of each request
}

func (*fakeRelater) Name() string                                { return "Fake" }
func (*fakeRelater) Playlists() ([]playlist.PlaylistInfo, error) { return nil, nil }
func (*fakeRelater) Tracks(string) ([]playlist.Track, error)     { return nil, nil }
func (*fakeRelater) CanRelate(t playlist.Track) bool             { return strings.HasPrefix(t.Path, "fake:") }
func (f *fakeRelater) RelatedTracks(_ context.Context, _ playlist.Track, n int) ([]playlist.Track, error) {
	f.asked = append(f.asked, n)
	return f.tracks, f.err
}

// songRadioModel plays a.mp3 from a playlist of a.mp3, the fake:seed song and
// b.mp3, with the cursor on the seed.
func songRadioModel() (Model, *playbackFakeEngine, *fakeRelater) {
	player := &playbackFakeEngine{playing: true}
	relater := &fakeRelater{tracks: []playlist.Track{
		{Title: "R1", Path: "fake:r1"},
		{Title: "Seed again", Path: "fake:seed"},
		{Title: "R2", Path: "fake:r2"},
	}}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "A", Path: "a.mp3"},
		{Title: "Seed", Path: "fake:seed"},
		{Title: "B", Path: "b.mp3"},
	})
	p.SetIndex(0)
	m := Model{
		player:      player,
		playlist:    p,
		providers:   []ProviderEntry{{Name: "Fake", Provider: relater}},
		focus:       focusPlaylist,
		plCursor:    1,
		configSaver: &recordingConfigSaver{},
	}
	return m, player, relater
}

var songRadioKey = tea.KeyPressMsg{Text: "c", Code: 'c'}

// commandEnabled reports whether key is offered in mode's context help.
func commandEnabled(m Model, mode commandMode, key string) bool {
	for _, c := range commandRegistry {
		if c.ContextHelp && c.Mode&mode != 0 && slices.Contains(c.Keys, key) && c.enabled(m) {
			return true
		}
	}
	return false
}

// runCmds runs cmd and any batched commands, dropping their messages.
func runCmds(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			runCmds(c)
		}
	}
}

func paths(tracks []playlist.Track) []string {
	out := make([]string, len(tracks))
	for i, t := range tracks {
		out[i] = t.Path
	}
	return out
}

// Pressing c looks the songs up first; when they arrive, the queue becomes the
// seed plus its related songs, without repeats, and the seed plays from the top.
func TestSongRadioReplacesQueueWithSeedAndRelated(t *testing.T) {
	m, player, relater := songRadioModel()

	next, cmd := m.Update(songRadioKey)
	m = next.(Model)
	if cmd == nil {
		t.Fatal("c on a relatable song started no lookup")
	}
	if got := paths(m.playlist.Tracks()); strings.Join(got, " ") != "a.mp3 fake:seed b.mp3" || player.stopCalls != 0 {
		t.Fatalf("queue changed before the songs arrived: %v, Stop %d", got, player.stopCalls)
	}
	if !strings.Contains(m.status.text, "Finding songs like Seed") {
		t.Fatalf("status = %q, want the lookup shown", m.status.text)
	}

	msg := cmd()
	if len(relater.asked) != 1 || relater.asked[0] != defaultSongRadioSize {
		t.Fatalf("asked for %v songs, want %d", relater.asked, defaultSongRadioSize)
	}
	next, play := m.Update(msg)
	m = next.(Model)
	runCmds(play)
	if len(player.playCalls) == 0 || player.playCalls[len(player.playCalls)-1] != "fake:seed" {
		t.Fatalf("playCalls = %v, want fake:seed played", player.playCalls)
	}
	if got := paths(m.playlist.Tracks()); strings.Join(got, " ") != "fake:seed fake:r1 fake:r2" {
		t.Fatalf("queue = %v, want the seed then fake:r1 fake:r2", got)
	}
	if m.playlist.Index() != 0 || m.plCursor != 0 || player.stopCalls != 1 {
		t.Fatalf("index %d, cursor %d, Stop %d; want the seed started from the top", m.playlist.Index(), m.plCursor, player.stopCalls)
	}
}

// The seed starts over even when it is the song already playing.
func TestSongRadioRestartsPlayingSeed(t *testing.T) {
	m, player, _ := songRadioModel()
	m.playlist.SetIndex(1)
	player.position = 90 * time.Second

	next, cmd := m.Update(songRadioKey)
	next, _ = next.(Model).Update(cmd())
	if m = next.(Model); m.playlist.Index() != 0 || player.stopCalls != 1 {
		t.Fatalf("index %d, Stop %d; want the playing seed stopped and restarted as the first song", m.playlist.Index(), player.stopCalls)
	}
}

// A failed lookup, or one that finds nothing, leaves the queue and playback
// alone and says so.
func TestSongRadioFailureKeepsQueue(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tracks []playlist.Track
		err    error
		status string
	}{
		{name: "error", err: errors.New("station unavailable"), status: "Song radio failed for Seed: station unavailable"},
		{name: "nothing found", tracks: []playlist.Track{{Path: "fake:seed"}}, status: "No songs found like Seed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, player, relater := songRadioModel()
			relater.tracks, relater.err = tc.tracks, tc.err

			next, cmd := m.Update(songRadioKey)
			next, _ = next.(Model).Update(cmd())
			m = next.(Model)
			if got := paths(m.playlist.Tracks()); strings.Join(got, " ") != "a.mp3 fake:seed b.mp3" || m.playlist.Index() != 0 || player.stopCalls != 0 {
				t.Fatalf("queue %v, index %d, Stop %d; want both untouched", got, m.playlist.Index(), player.stopCalls)
			}
			if m.status.text != tc.status {
				t.Fatalf("status = %q, want %q", m.status.text, tc.status)
			}
		})
	}
}

// A newer c press replaces a lookup still in flight: the older answer is
// ignored.
func TestSongRadioNewerRequestWins(t *testing.T) {
	m, player, _ := songRadioModel()
	m.playlist.Replace(append(m.playlist.Tracks(), playlist.Track{Title: "Seed 2", Path: "fake:seed2"}))

	next, first := m.Update(songRadioKey)
	m = next.(Model)
	m.plCursor = 3
	next, second := m.Update(songRadioKey)
	m = next.(Model)

	next, _ = m.Update(first())
	if m = next.(Model); player.stopCalls != 0 || m.playlist.Len() != 4 {
		t.Fatalf("the replaced lookup changed the queue (%d tracks, Stop %d)", m.playlist.Len(), player.stopCalls)
	}
	next, _ = m.Update(second())
	if m = next.(Model); m.playlist.Tracks()[0].Path != "fake:seed2" {
		t.Fatalf("queue starts with %s, want the newer seed", m.playlist.Tracks()[0].Path)
	}
}

// Ctrl+Z after a song radio brings back the old queue and where it came from,
// while the seed keeps playing.
func TestSongRadioUndoRestoresOldQueue(t *testing.T) {
	m, player, _ := songRadioModel()
	m.loadedPlaylist, m.activeProviderPlaylistID = "mine", "p1"

	next, cmd := m.Update(songRadioKey)
	next, _ = next.(Model).Update(cmd())
	m = next.(Model)
	if m.loadedPlaylist != "" || m.activeProviderPlaylistID != "" {
		t.Fatalf("radio queue still linked to %q / %q", m.loadedPlaylist, m.activeProviderPlaylistID)
	}
	if !strings.Contains(m.status.text, "Ctrl+Z to undo") {
		t.Fatalf("status = %q, want the undo offered", m.status.text)
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	m = next.(Model)
	if got := paths(m.playlist.Tracks()); strings.Join(got, " ") != "a.mp3 fake:seed b.mp3" {
		t.Fatalf("queue after Ctrl+Z = %v, want the old queue", got)
	}
	if m.loadedPlaylist != "mine" || m.activeProviderPlaylistID != "p1" {
		t.Fatalf("links after Ctrl+Z = %q / %q, want mine / p1", m.loadedPlaylist, m.activeProviderPlaylistID)
	}
	if player.stopCalls != 1 {
		t.Fatalf("Stop %d, want the seed left playing", player.stopCalls)
	}
}

// fakeLocalPlaylists serves saved playlists for IPC load.
type fakeLocalPlaylists struct{ fakeRelater }

func (*fakeLocalPlaylists) Tracks(string) ([]playlist.Track, error) {
	return []playlist.Track{{Title: "X", Path: "x.mp3"}, {Title: "Y", Path: "y.mp3"}}, nil
}

// A queue loaded while the songs are being looked up wins: the radio's late
// answer is ignored. Moving to another song in the same queue does not cancel
// it.
func TestSongRadioSupersededByNewQueue(t *testing.T) {
	t.Run("new queue", func(t *testing.T) {
		m, _, _ := songRadioModel()
		m.localProvider = &fakeLocalPlaylists{}
		next, lookup := m.Update(songRadioKey)
		next, _ = next.(Model).Update(ipc.LoadMsg{Playlist: "mine"})
		next, _ = next.(Model).Update(lookup())
		if got := paths(next.(Model).playlist.Tracks()); strings.Join(got, " ") != "x.mp3 y.mp3" {
			t.Fatalf("queue = %v, want the playlist loaded after c", got)
		}
	})
	t.Run("same queue", func(t *testing.T) {
		m, _, _ := songRadioModel()
		next, lookup := m.Update(songRadioKey)
		next, _ = next.(Model).Update(PluginQueueMsg{Op: "jump", Index: 2})
		next, _ = next.(Model).Update(lookup())
		if got := next.(Model).playlist.Tracks()[0].Path; got != "fake:seed" {
			t.Fatalf("queue starts with %s, want the radio applied", got)
		}
	})
}

// Late pages or batches of the old queue's loads do not land on the radio.
func TestSongRadioDropsOldQueueLoads(t *testing.T) {
	m, _, relater := songRadioModel()
	m.provider = relater
	m.tracksPaging = true
	pageGen := nextRequest(&m.requests.tracks)
	m.ytdlBatch.loading = true
	batchGen := m.ytdlBatch.gen

	next, lookup := m.Update(songRadioKey)
	next, _ = next.(Model).Update(lookup())
	late := []playlist.Track{{Title: "Late", Path: "late.mp3"}}
	next, _ = next.(Model).Update(tracksLoadedMsg{tracks: late, providerName: relater.Name(), gen: pageGen, offset: 3})
	next, _ = next.(Model).Update(ytdlBatchMsg{gen: batchGen, tracks: late})
	if got := paths(next.(Model).playlist.Tracks()); strings.Join(got, " ") != "fake:seed fake:r1 fake:r2" {
		t.Fatalf("queue = %v, want the radio without the old loads' songs", got)
	}
}

// song_radio_size decides how many related songs are asked for.
func TestSongRadioSize(t *testing.T) {
	m, _, relater := songRadioModel()
	m.SetSongRadioSize(5)
	_, cmd := m.Update(songRadioKey)
	cmd()
	if len(relater.asked) != 1 || relater.asked[0] != 5 {
		t.Fatalf("asked for %v songs, want 5", relater.asked)
	}
}

// c only works, and is only offered, on a song some source can find related
// songs for; on anything else it does nothing.
func TestSongRadioOnlyForRelatableSongs(t *testing.T) {
	m, _, _ := songRadioModel()
	if !commandEnabled(m, commandModeMain, "c") {
		t.Fatal("c not offered on a relatable song")
	}
	m.plCursor = 0
	if commandEnabled(m, commandModeMain, "c") {
		t.Fatal("c offered on a local file")
	}
	if _, cmd := m.Update(songRadioKey); cmd != nil {
		t.Fatal("c on a local file started a lookup")
	}
}

// In search results, c starts a song radio from the highlighted song and
// closes the search; an album placeholder is not a seed.
func TestSongRadioFromSearchResults(t *testing.T) {
	t.Run("provider search", func(t *testing.T) {
		m, _, relater := songRadioModel()
		m.spotSearch = spotSearchState{prov: relater, visible: true, screen: spotSearchResults, results: []playlist.Track{
			{Title: "Album", Path: "fake:album", ProviderMeta: map[string]string{playlist.MetaKind: playlist.MetaKindAlbum}},
			{Title: "Hit", Path: "fake:hit"},
		}}

		if commandEnabled(m, commandModeSpotSearch, "c") {
			t.Fatal("c offered on an album placeholder")
		}
		if _, cmd := m.Update(songRadioKey); cmd != nil {
			t.Fatal("c on an album placeholder started a lookup")
		}

		m.spotSearch.cursor = 1
		if !commandEnabled(m, commandModeSpotSearch, "c") {
			t.Fatal("c not offered on a song result")
		}
		next, cmd := m.Update(songRadioKey)
		m = next.(Model)
		if cmd == nil || m.spotSearch.visible {
			t.Fatalf("lookup %v, search visible %v; want a lookup and the search closed", cmd != nil, m.spotSearch.visible)
		}
		next, _ = m.Update(cmd())
		if got := next.(Model).playlist.Tracks()[0].Path; got != "fake:hit" {
			t.Fatalf("queue starts with %s, want fake:hit", got)
		}
	})

	t.Run("online search", func(t *testing.T) {
		m, _, _ := songRadioModel()
		m.netSearch = netSearchState{active: true, screen: netSearchResults, results: []playlist.Track{{Title: "Hit", Path: "fake:hit"}}}
		m.focus = focusNetSearch
		if !commandEnabled(m, commandModeNetSearch, "c") {
			t.Fatal("c not offered on an online result")
		}
		next, cmd := m.Update(songRadioKey)
		if cmd == nil || next.(Model).netSearch.active {
			t.Fatal("c on an online result did not start a lookup and close the search")
		}
	})
}
