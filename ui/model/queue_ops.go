package model

import (
	"errors"
	"fmt"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

// The queue edits in this file follow one rule, whether a key, IPC or a Lua
// plugin starts them:
//   - A move is refused while shuffle is on, because the rows then list the
//     play order and not the track order.
//   - When the queue mirrors a writable saved playlist, the edit is saved to
//     that playlist in one locked update.
//   - The gapless preload is re-armed when the edit changes the next track.

var (
	errQueueIndex    = errors.New("no track at that index")
	errQueueShuffled = errors.New("turn off shuffle to move tracks")
	errQueueDirTrack = errors.New("a directory source supplies the track")
)

// appendTracks adds tracks to the end of the queue and returns the index of
// the first one. The queue then mirrors no saved playlist, and the album
// header counters include the new tracks. The caller starts playback or
// re-arms the preload, because that depends on why the tracks came.
func (m *Model) appendTracks(tracks ...playlist.Track) int {
	first := m.playlist.Len()
	m.playlist.Add(tracks...)
	m.clearLoadedPlaylist()
	m.addToHeaderState(tracks)
	return first
}

// moveTrack swaps the track at from with the track at to and saves the new
// order to the loaded playlist. When the save fails, the queue keeps its
// order. The playlist cursor stays on its track.
func (m *Model) moveTrack(from, to int) (tea.Cmd, error) {
	if m.playlist.Shuffled() {
		m.status.Warning(shuffleMoveWarning, statusTTLShort)
		return nil, errQueueShuffled
	}
	tracks := m.playlist.Tracks()
	if from < 0 || from >= len(tracks) || to < 0 || to >= len(tracks) || from == to {
		return nil, errQueueIndex
	}
	tracks[from], tracks[to] = tracks[to], tracks[from]
	if err := m.persistLoadedPlaylistOrder(tracks); err != nil {
		return nil, err
	}
	m.playlist.Move(from, to)
	switch m.plCursor {
	case from:
		m.plCursor = to
	case to:
		m.plCursor = from
	}
	m.recountHeaderState(tracks)
	m.normalizeQueueOverlay()
	m.adjustScroll()
	return m.rearmStalePreload(), nil
}

// removeTrack removes the track at idx. When the queue mirrors a writable
// saved playlist, the track leaves that file too. With recordUndo, Ctrl+Z
// restores both. Only the x key records an undo. A remote or plugin edit
// that follows makes that undo stale. While the queue mirrors a
// writable saved playlist, a track from its directory source is refused,
// because the file cannot drop it. Removing the track that plays stops
// playback. A track that plays detached from the list keeps playing.
func (m *Model) removeTrack(idx int, recordUndo bool) (tea.Cmd, error) {
	track, ok := m.playlist.Track(idx)
	if !ok {
		return nil, errQueueIndex
	}
	loaded := m.writableLoadedPlaylist()
	if loaded != "" && track.DirSourced {
		m.status.Warningf(statusTTLDefault, "Can't remove %q: it's supplied by the playlist's directory source", track.DisplayName())
		return nil, errQueueDirTrack
	}
	snapshot := m.playlist.Snapshot()
	var removed playlist.Track
	savedIdx := -1
	persisted := false
	if loaded != "" {
		if updater, ok := m.localProvider.(playlistUpdater); ok {
			err := updater.UpdatePlaylist(loaded, func(tracks []playlist.Track) ([]playlist.Track, error) {
				// Another writer or a new file in a directory source can
				// have shifted indexes since the queue was loaded. Match the
				// persisted explicit track by path so the wrong track is
				// never removed.
				savedIdx = slices.IndexFunc(tracks, func(candidate playlist.Track) bool {
					return !candidate.DirSourced && candidate.Path == track.Path
				})
				if savedIdx < 0 {
					return nil, fmt.Errorf("selected track is no longer in %q", loaded)
				}
				removed = cloneTracks(tracks[savedIdx : savedIdx+1])[0]
				return slices.Delete(tracks, savedIdx, savedIdx+1), nil
			})
			if err != nil {
				m.status.Errorf(statusTTLDefault, "Remove failed: %s", err)
				return nil, err
			}
			persisted = true
		}
	}
	// A detached track plays outside the list, so the selected row of the
	// list is not the playing track.
	wasActive := !m.playbackDetached && idx == m.playlist.Index()
	if !m.playlist.Remove(idx) {
		return nil, errQueueIndex
	}
	m.normalizeQueueOverlay()
	undoHint := ""
	if recordUndo {
		m.recordPlaylistUndo(playlistUndo{snapshot: snapshot, persisted: persisted, removed: removed, savedIdx: savedIdx})
		undoHint = " (Ctrl+Z to undo)"
	}
	if wasActive {
		m.stopPlayback()
		m.player.ClearPreload()
	}
	if idx < m.plCursor {
		m.plCursor--
	}
	if newLen := m.playlist.Len(); newLen == 0 {
		m.plCursor = 0
	} else if m.plCursor >= newLen {
		m.plCursor = newLen - 1
	}
	m.recountHeaderState(m.playlist.Tracks())
	m.adjustScroll()
	if persisted {
		m.status.Showf(statusTTLDefault, "Removed from %q: %s%s", loaded, track.DisplayName(), undoHint)
	} else {
		m.status.Showf(statusTTLDefault, "Removed from queue: %s%s", track.DisplayName(), undoHint)
	}
	return m.rearmStalePreload(), nil
}

// pathRow names a row of a track list by its path and by the count of
// earlier rows with that path, so two rows of one path stay apart.
type pathRow struct {
	path string
	nth  int
}

// pathRowsOf returns the pathRow of each track, in order.
func pathRowsOf(tracks []playlist.Track) []pathRow {
	seen := make(map[string]int, len(tracks))
	rows := make([]pathRow, len(tracks))
	for i, t := range tracks {
		rows[i] = pathRow{path: t.Path, nth: seen[t.Path]}
		seen[t.Path]++
	}
	return rows
}

// orderByRows returns the saved tracks of a playlist file in the order of the
// same rows in order. Saved tracks that order does not hold, such as a track
// that another writer added, keep their order at the end. Rows of order that
// the file does not hold are left out. Each track keeps its saved fields, and
// a saved duration of 0 takes the duration of the row in order.
func orderByRows(saved, order []playlist.Track) []playlist.Track {
	at := make(map[pathRow]int, len(saved))
	for i, row := range pathRowsOf(saved) {
		at[row] = i
	}
	used := make([]bool, len(saved))
	out := make([]playlist.Track, 0, len(saved))
	for i, row := range pathRowsOf(order) {
		j, ok := at[row]
		if !ok {
			continue
		}
		used[j] = true
		t := saved[j]
		if t.DurationSecs == 0 {
			t.DurationSecs = order[i].DurationSecs
		}
		out = append(out, t)
	}
	for j, t := range saved {
		if !used[j] {
			out = append(out, t)
		}
	}
	return out
}

// persistLoadedPlaylistOrder writes the order of queue to the loaded
// playlist file in one locked update, so a track or a tag that another
// writer added after the load is kept. It returns the error of a failed
// save.
func (m *Model) persistLoadedPlaylistOrder(queue []playlist.Track) error {
	name := m.writableLoadedPlaylist()
	if name == "" {
		return nil
	}
	updater, ok := m.localProvider.(playlistUpdater)
	if !ok {
		return nil
	}
	hasDirTracks := false
	for _, t := range queue {
		if t.DirSourced {
			hasDirTracks = true
			break
		}
	}
	err := updater.UpdatePlaylist(name, func(tracks []playlist.Track) ([]playlist.Track, error) {
		return orderByRows(tracks, queue), nil
	})
	if err != nil {
		m.status.Errorf(statusTTLDefault, "Save failed: %s", err)
		return err
	}
	if hasDirTracks {
		m.status.Warningf(statusTTLDefault, "Reordered %q (directory-sourced tracks keep scan order)", name)
		return nil
	}
	m.status.Showf(statusTTLDefault, "Reordered %q", name)
	return nil
}
