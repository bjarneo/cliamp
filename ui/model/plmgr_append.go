package model

import (
	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

// addTracksToCurrentPlaylist appends tracks to the end of the current playlist
// without disturbing playback or the queue. It reports whether the playlist was
// empty beforehand, so the caller knows to start playing.
func (m *Model) addTracksToCurrentPlaylist(tracks []playlist.Track, what string) (wasEmpty, ok bool) {
	if len(tracks) == 0 {
		m.status.Warning("Nothing to add", statusTTLShort)
		return false, false
	}
	wasEmpty = m.playlist.Len() == 0
	m.playlist.Add(tracks...)
	m.loadedPlaylist = ""
	m.addToHeaderState(tracks)
	m.status.Showf(statusTTLDefault, "Added %d track(s) from %s", len(tracks), what)
	return wasEmpty, true
}

// appendTracksToPlaylist adds tracks to the end of the current playlist and
// returns the command the caller must run.
func (m *Model) appendTracksToPlaylist(tracks []playlist.Track, what string) tea.Cmd {
	wasEmpty, ok := m.addTracksToCurrentPlaylist(tracks, what)
	if !ok {
		return nil
	}
	if wasEmpty {
		// An empty playlist has nothing playing to protect, so start there.
		m.plCursor = 0
		m.playlist.SetIndex(0)
		m.adjustScroll()
		cmd := m.playCurrentTrack()
		m.notifyPlayback()
		return cmd
	}
	m.adjustScroll()
	return m.rearmPreload()
}

// plMgrAppendPlaylist appends every track of the highlighted saved playlist to
// the current one. Unlike Enter, which loads and replaces, this keeps what is
// already loaded and the queue built on top of it.
func (m *Model) plMgrAppendPlaylist() tea.Cmd {
	realIdx := m.plMgrPlaylistRealIndex(m.plManager.cursor)
	if realIdx < 0 || realIdx >= len(m.plManager.playlists) {
		return nil
	}
	name := m.plManager.playlists[realIdx].Name
	tracks, err := m.localProvider.Tracks(name)
	if err != nil {
		m.status.Errorf(statusTTLDefault, "Loading %q failed: %s", name, err)
		return nil
	}
	if len(tracks) == 0 {
		m.status.Warningf(statusTTLDefault, "%q is empty", name)
		return nil
	}
	cmd := m.appendTracksToPlaylist(tracks, name)
	m.plManager.visible = false
	m.plMgrResetFilter()
	m.focus = focusPlaylist
	return cmd
}

// plMgrAppendSelectedTracks appends the marked tracks, or the highlighted one
// when nothing is marked, to the current playlist.
func (m *Model) plMgrAppendSelectedTracks() tea.Cmd {
	tracks := m.plMgrSelectedTracks()
	if len(tracks) == 0 {
		return nil
	}
	what := m.plManager.selPlaylist
	if what == "" {
		what = "the playlist"
	}
	return m.appendTracksToPlaylist(tracks, what)
}
