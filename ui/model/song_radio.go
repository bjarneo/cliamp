package model

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/resolve"
)

// defaultSongRadioSize is how many related songs a song radio adds after the
// seed when the config does not say.
const defaultSongRadioSize = 30

// songRadioTimeout bounds one lookup of related songs.
const songRadioTimeout = 30 * time.Second

// songRadioMsg carries the songs found for a song radio back to Update.
type songRadioMsg struct {
	id     uint64
	seed   playlist.Track
	tracks []playlist.Track
	err    error
}

// SetSongRadioSize sets how many related songs a song radio adds after the
// seed. A non-positive size keeps the default.
func (m *Model) SetSongRadioSize(n int) {
	if n > 0 {
		m.songRadioSize = n
	}
}

// relaters returns every source that can find songs related to a track: the
// configured providers that support it, then YouTube, which no provider owns.
func (m Model) relaters() []provider.Relater {
	var relaters []provider.Relater
	for _, pe := range m.providers {
		if r, ok := pe.Provider.(provider.Relater); ok {
			relaters = append(relaters, r)
		}
	}
	return append(relaters, resolve.YouTubeRelater{})
}

// songRadioRelater returns the source that can start a song radio from track.
// Album placeholders in search results are not songs, so they cannot.
func (m Model) songRadioRelater(track playlist.Track) (provider.Relater, bool) {
	if track.IsAlbum() {
		return nil, false
	}
	return provider.RelaterFor(track, m.relaters()...)
}

func (m Model) canSongRadio(track playlist.Track) bool {
	_, ok := m.songRadioRelater(track)
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
	case m.spotSearch.visible && m.spotSearch.screen == spotSearchResults && !m.spotSearchBusy():
		if c := m.spotSearch.cursor; c >= 0 && c < len(m.spotSearch.results) {
			return m.spotSearch.results[c], true
		}
	case m.netSearch.active && m.netSearch.screen == netSearchResults && !m.netSearch.loading:
		if c := m.netSearch.cursor; c >= 0 && c < len(m.netSearch.results) {
			return m.netSearch.results[c], true
		}
	}
	return playlist.Track{}, false
}

// startSongRadio looks up songs related to seed. The queue is left alone until
// they arrive. A newer request, or any new queue, replaces this one: the older
// lookup still runs to completion or its timeout, but its answer is ignored.
func (m *Model) startSongRadio(seed playlist.Track) tea.Cmd {
	r, ok := m.songRadioRelater(seed)
	if !ok {
		return nil
	}
	id := nextRequest(&m.requests.songRadio)
	n := m.songRadioSize
	if n <= 0 {
		n = defaultSongRadioSize
	}
	m.status.Activityf(statusTTLLong, "Finding songs like %s...", seed.DisplayName())
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), songRadioTimeout)
		defer cancel()
		tracks, err := provider.Related(ctx, r, seed, n)
		return songRadioMsg{id: id, seed: seed, tracks: tracks, err: err}
	}
}

// handleSongRadio replaces the queue with the seed and its related songs and
// plays the seed from the top. Ctrl+Z brings the old queue back. The new queue
// comes from no playlist, so it keeps no link to the old one. On failure the
// queue and playback are left alone.
func (m *Model) handleSongRadio(msg songRadioMsg) tea.Cmd {
	if msg.id != m.requests.songRadio {
		return nil
	}
	if msg.err != nil {
		m.status.Errorf(statusTTLMedium, "Song radio failed for %s: %s", msg.seed.DisplayName(), msg.err)
		return nil
	}
	if len(msg.tracks) == 0 {
		m.status.Warningf(statusTTLMedium, "No songs found like %s", msg.seed.DisplayName())
		return nil
	}
	tracks := append([]playlist.Track{msg.seed}, msg.tracks...)
	undo := m.newPlaylistUndo()
	m.player.Stop()
	m.player.ClearPreload()
	m.resetYTDLBatch()
	m.retireTracksPaging()
	m.replacePlaylist(tracks)
	m.playlistUndo = undo
	m.loadedPlaylist = ""
	m.activeProviderPlaylistID = ""
	m.setHeaderStateFromTracks(tracks)
	m.plCursor = 0
	m.plScroll = 0
	m.playlist.SetIndex(0)
	m.focus = focusPlaylist
	m.status.Successf(statusTTLDefault, "Song radio: %s and %d related songs (Ctrl+Z to undo)", msg.seed.DisplayName(), len(msg.tracks))
	cmd := m.playCurrentTrack()
	m.notifyPlayback()
	return cmd
}
