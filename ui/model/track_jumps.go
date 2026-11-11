package model

import (
	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// albumOpenedMsg carries the tracks of the album that Alt+O opened from the
// track at path from.
type albumOpenedMsg struct {
	id       uint64
	provider string
	album    playlist.PlaylistInfo
	from     string
	tracks   []playlist.Track
	err      error
}

// selectedQueueTrack returns the highlighted playlist row while the playlist
// has focus.
func (m Model) selectedQueueTrack() (playlist.Track, bool) {
	if m.focus != focusPlaylist || m.playlist == nil {
		return playlist.Track{}, false
	}
	return m.playlist.Track(m.plCursor)
}

// trackAlbum finds the provider that can open the album of track.
func (m Model) trackAlbum(track playlist.Track) (playlist.Provider, string, playlist.PlaylistInfo, bool) {
	r, name := findCapable(&m, func(r provider.TrackAlbumResolver) bool {
		_, ok := r.AlbumForTrack(track)
		return ok
	})
	prov, ok := r.(playlist.Provider)
	if !ok {
		return nil, "", playlist.PlaylistInfo{}, false
	}
	album, _ := r.AlbumForTrack(track)
	return prov, name, album, true
}

// trackArtistFilter finds the provider whose list filter can show the artist
// of track, and that filter.
func (m Model) trackArtistFilter(track playlist.Track) (string, string, bool) {
	f, name := findCapable(&m, func(f provider.TrackArtistFilter) bool {
		_, ok := f.ArtistFilter(track)
		return ok
	})
	if f == nil {
		return "", "", false
	}
	query, _ := f.ArtistFilter(track)
	return name, query, true
}

func (m Model) canOpenTrackAlbum() bool {
	track, ok := m.selectedQueueTrack()
	if !ok {
		return false
	}
	_, _, _, ok = m.trackAlbum(track)
	return ok
}

func (m Model) canOpenTrackArtist() bool {
	track, ok := m.selectedQueueTrack()
	if !ok {
		return false
	}
	_, _, ok = m.trackArtistFilter(track)
	return ok
}

// openTrackAlbum loads the album of the highlighted track. The queue is left
// alone until its tracks arrive; any newer queue makes the answer stale.
func (m *Model) openTrackAlbum() tea.Cmd {
	track, ok := m.selectedQueueTrack()
	if !ok {
		return nil
	}
	prov, name, album, ok := m.trackAlbum(track)
	if !ok {
		return nil
	}
	id := nextRequest(&m.requests.queue)
	m.status.Activityf(statusTTLLong, "Opening %s...", album.Name)
	return func() tea.Msg {
		tracks, err := prov.Tracks(album.ID)
		return albumOpenedMsg{id: id, provider: name, album: album, from: track.Path, tracks: tracks, err: err}
	}
}

// handleAlbumOpened replaces the queue with the album and puts the cursor on
// the track the jump started from. Playback goes on: a playing track on the
// album becomes its current track, and any other keeps playing until it ends.
// Ctrl+Z brings the old queue back.
func (m *Model) handleAlbumOpened(msg albumOpenedMsg) tea.Cmd {
	if msg.id != m.requests.queue {
		return nil
	}
	if msg.err != nil {
		m.status.Errorf(statusTTLMedium, "Opening %s failed: %s", msg.album.Name, msg.err)
		return nil
	}
	if len(msg.tracks) == 0 {
		m.status.Warningf(statusTTLMedium, "%s has no tracks", msg.album.Name)
		return nil
	}
	snapshot := m.playlist.Snapshot()
	undo := playlistUndo{snapshot: snapshot, cursorPath: msg.from, providerPlaylistID: m.activeProviderPlaylistID}
	if m.provider != nil {
		undo.providerName = m.provider.Name()
	}
	m.retireTracksPaging()
	m.replacePlayerPlaylist(msg.tracks)
	if i := trackIndexByPath(msg.tracks, m.playingTrack.Path); m.playingTrackActive && i >= 0 {
		m.playlist.SetIndex(i)
		m.playbackDetached = false
		if t, ok := m.playlist.Track(i); ok {
			m.playingTrack = t
		}
	}
	if i := trackIndexByPath(msg.tracks, msg.from); i >= 0 {
		m.plCursor = i
	}
	// The queue is the album now, so the open playlist is the album, or none
	// when the album's provider is not the active one: Ctrl+R and the saved
	// resume context must not bring back the playlist the album replaced.
	m.activeProviderPlaylistID = ""
	if m.isActiveProvider(msg.provider) {
		m.activeProviderPlaylistID = msg.album.ID
	}
	m.adjustScroll()
	m.status.Successf(statusTTLDefault, "Album: %s (Ctrl+Z to undo)", msg.album.Name)
	m.recordPlaylistUndo(undo)
	return m.rearmStalePreload()
}

// openTrackArtist opens the browser of the provider that owns the highlighted
// track, filtered to the lists of its artist. The filter is an ordinary one:
// it can be edited, and Esc clears it.
func (m *Model) openTrackArtist() tea.Cmd {
	track, ok := m.selectedQueueTrack()
	if !ok {
		return nil
	}
	name, query, ok := m.trackArtistFilter(track)
	if !ok {
		return nil
	}
	var cmd tea.Cmd
	if !m.isActiveProvider(name) {
		idx := -1
		for i, pe := range m.providers {
			if pe.Name == name {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil
		}
		cmd = m.switchProvider(idx)
	}
	m.focus = focusProvider
	m.provSearch = provSearchState{active: true, query: query}
	m.updateProvSearch()
	return cmd
}
