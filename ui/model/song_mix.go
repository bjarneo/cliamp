package model

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/resolve"
)

// songMixTimeout bounds one lookup of related songs.
const songMixTimeout = 30 * time.Second

// songMixMsg carries the songs found for a song mix back to Update.
type songMixMsg struct {
	id     uint64
	seed   playlist.Track
	tracks []playlist.Track
	err    error
}

// SetSongMixSize sets how many related songs a song mix adds after the
// seed. The config keeps it between 1 and 100.
func (m *Model) SetSongMixSize(n int) {
	m.songMixSize = n
}

// songMixRelater returns the source that can start a song mix from track:
// a configured provider that supports it, or YouTube, which no provider owns.
// Album placeholders in search results are not songs, so they cannot.
func (m Model) songMixRelater(track playlist.Track) (provider.Relater, bool) {
	if track.IsAlbum() {
		return nil, false
	}
	if r, _ := findCapable(&m, func(r provider.Relater) bool { return r.CanRelate(track) }); r != nil {
		return r, true
	}
	if yt := (resolve.YouTubeRelater{}); yt.CanRelate(track) {
		return yt, true
	}
	return nil, false
}

func (m Model) canSongMix(track playlist.Track) bool {
	_, ok := m.songMixRelater(track)
	return ok
}

// selectedPlaylistTrack returns the highlighted playlist row while the
// playlist has focus.
func (m Model) selectedPlaylistTrack() (playlist.Track, bool) {
	if m.focus != focusPlaylist || m.playlist == nil {
		return playlist.Track{}, false
	}
	return m.playlist.Track(m.plCursor)
}

// selectedSearchResult returns the highlighted row of an open search results
// list.
func (m Model) selectedSearchResult() (playlist.Track, bool) {
	switch {
	case m.searchOverlay.visible && m.searchOverlay.screen == searchOverlayResults && !m.searchOverlayBusy():
		if c := m.searchOverlay.cursor; c >= 0 && c < len(m.searchOverlay.results) {
			return m.searchOverlay.results[c], true
		}
	case m.netSearch.active && m.netSearch.screen == netSearchResults && !m.netSearch.loading:
		if c := m.netSearch.cursor; c >= 0 && c < len(m.netSearch.results) {
			return m.netSearch.results[c], true
		}
	}
	return playlist.Track{}, false
}

// startSongMix looks up songs related to seed. The queue is left alone until
// they arrive. A newer request stops this lookup; any newer queue makes its
// answer stale.
func (m *Model) startSongMix(seed playlist.Track) tea.Cmd {
	r, ok := m.songMixRelater(seed)
	if !ok {
		return nil
	}
	if m.songMixCancel != nil {
		m.songMixCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), songMixTimeout)
	m.songMixCancel = cancel
	id := nextRequest(&m.requests.queue)
	n := m.songMixSize
	m.status.Activityf(statusTTLLong, "Finding songs like %s...", seed.DisplayName())
	return func() tea.Msg {
		defer cancel()
		tracks, err := r.RelatedTracks(ctx, seed, n)
		return songMixMsg{id: id, seed: seed, tracks: tracks, err: err}
	}
}

// handleSongMix replaces the queue with the seed and its related songs and
// plays the seed from the top. Ctrl+Z brings the old queue back, unlinked from
// the playlist it came from. The new queue comes from no playlist, so it keeps
// no link to the old one either. On failure the queue and playback are left
// alone.
func (m *Model) handleSongMix(msg songMixMsg) tea.Cmd {
	if msg.id != m.requests.queue {
		return nil
	}
	if msg.err != nil {
		m.status.Errorf(statusTTLMedium, "Song mix failed for %s: %s", msg.seed.DisplayName(), msg.err)
		return nil
	}
	if len(msg.tracks) == 0 {
		m.status.Warningf(statusTTLMedium, "No songs found like %s", msg.seed.DisplayName())
		return nil
	}
	tracks := append([]playlist.Track{msg.seed}, msg.tracks...)
	snapshot := m.playlist.Snapshot()
	m.stopPlayback()
	m.player.ClearPreload()
	m.retireTracksPaging()
	m.replacePlaylist(tracks)
	m.clearLoadedPlaylist()
	m.activeProviderPlaylistID = ""
	m.setHeaderStateFromTracks(tracks)
	m.focus = focusPlaylist
	m.status.Successf(statusTTLDefault, "Song mix: %s and %d related songs (Ctrl+Z to undo)", msg.seed.DisplayName(), len(msg.tracks))
	cmd := m.playCurrentTrack()
	m.recordPlaylistUndo(playlistUndo{snapshot: snapshot})
	return cmd
}
