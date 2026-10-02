package model

import (
	"context"
	"fmt"
	"maps"
	"os"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// handlePlaylistManagerKey dispatches keys to the active manager screen.
func (m *Model) handlePlaylistManagerKey(msg tea.KeyPressMsg) tea.Cmd {
	// Quick-switch (Shift+letter) jumps to another provider. Only honored when
	// the manager isn't currently capturing text input (filter, new-name) or
	// waiting for a y/n answer, where Y confirms and other keys cancel.
	if m.plManager.screen != plMgrScreenNewName && m.plManager.screen != plMgrScreenRename && !m.plManager.filtering && !m.plManager.confirmDel {
		if cmd, ok := m.quickSwitchProvider(msg.String()); ok {
			return cmd
		}
	}
	switch m.plManager.screen {
	case plMgrScreenList:
		return m.handlePlMgrListKey(msg)
	case plMgrScreenTracks:
		return m.handlePlMgrTracksKey(msg)
	case plMgrScreenDirs:
		return m.handlePlMgrDirsKey(msg)
	case plMgrScreenNewName:
		return m.handlePlMgrNewNameKey(msg)
	case plMgrScreenRename:
		return m.handlePlMgrRenameKey(msg)
	}
	return nil
}

// plMgrVirtualPlaylistName reports the provider-synthesized playlist name
// (Favorites, Recently Played) when name refers to one; regular playlists
// return "". Virtual playlists cannot be renamed, deleted, or given
// directory sources.
func plMgrVirtualPlaylistName(name string) string {
	switch name {
	case favorites.PlaylistName, history.PlaylistName:
		return name
	}
	return ""
}

// handlePlMgrListKey handles keys on screen 0 (playlist list).
func (m *Model) handlePlMgrListKey(msg tea.KeyPressMsg) tea.Cmd {
	// Filter input mode swallows most keys.
	if m.plManager.filtering {
		return m.handlePlMgrFilterKey(msg)
	}

	// If waiting for delete confirmation, only accept y/n.
	if m.plManager.confirmDel {
		switch msg.String() {
		case "y", "Y":
			var refresh tea.Cmd
			realIdx := m.plMgrPlaylistRealIndex(m.plManager.cursor)
			if realIdx >= 0 && plMgrVirtualPlaylistName(m.plManager.playlists[realIdx].Name) != "" {
				m.plManager.confirmDel = false
				return nil
			}
			if realIdx >= 0 {
				name := m.plManager.playlists[realIdx].Name
				undo := plManagerUndo{kind: plUndoPlaylist, name: name}
				// Snapshot the raw document so undo can restore [[dir]]
				// sources verbatim; tracks are the fallback when the
				// provider cannot hand back its document.
				if d, ok := m.localProvider.(provider.PlaylistDocumenter); ok {
					if data, err := d.PlaylistDocument(name); err == nil {
						undo.doc = data
					}
				}
				if undo.doc == nil {
					if tracks, err := m.localProvider.Tracks(name); err == nil {
						undo.tracks = cloneTracks(tracks)
					}
				}
				m.plManager.undo = undo
				if d, ok := m.localProvider.(provider.PlaylistDeleter); ok {
					if err := d.DeletePlaylist(name); err != nil {
						m.status.Errorf(statusTTLDefault, "Delete failed: %s", err)
					} else {
						m.status.Showf(statusTTLDefault, "Deleted %q (u to undo)", name)
					}
				}
				m.plMgrRefreshList()
				// The provider pane lists playlists too; re-pull so the
				// deleted row disappears there without a pill switch.
				refresh = m.refreshPaneAfterLocalWrite()
			}
			m.plManager.confirmDel = false
			return refresh
		default:
			m.plManager.confirmDel = false
		}
		return nil
	}

	count := m.plMgrListViewCount()
	switch msg.String() {
	case "/":
		m.plManager.filtering = true
		m.plManager.savedCursor = m.plManager.cursor
		m.plManager.savedScroll = m.plManager.scroll
		m.plManager.filter = ""
		m.plManager.filtered = nil
		m.plManager.cursor = 0
		m.plManager.scroll = 0
		return nil
	case "up", "k":
		if m.plManager.cursor > 0 {
			m.plManager.cursor--
		} else if count > 0 {
			m.plManager.cursor = count - 1
		}
		m.plMgrListMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "down", "j":
		if m.plManager.cursor < count-1 {
			m.plManager.cursor++
		} else if count > 0 {
			m.plManager.cursor = 0
		}
		m.plMgrListMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "ctrl+x":
		m.toggleExpandedView()
		m.plMgrListMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "pgup", "ctrl+u":
		if m.plManager.cursor > 0 {
			visible := m.effectivePlaylistVisible()
			m.plManager.cursor -= min(m.plManager.cursor, visible)
			m.plMgrListMaybeAdjustScroll(visible)
		}
	case "pgdown", "ctrl+d":
		if m.plManager.cursor < count-1 {
			visible := m.effectivePlaylistVisible()
			m.plManager.cursor = min(count-1, m.plManager.cursor+visible)
			m.plMgrListMaybeAdjustScroll(visible)
		}
	case "home", "g":
		m.plManager.cursor = 0
		m.plMgrListMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "end", "G":
		if count > 0 {
			m.plManager.cursor = count - 1
		}
		m.plMgrListMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "enter", "l", "right":
		realIdx := m.plMgrPlaylistRealIndex(m.plManager.cursor)
		if realIdx >= 0 {
			m.plMgrEnterTrackList(m.plManager.playlists[realIdx].Name)
		} else {
			// "+ New Playlist..." selected. Pre-fill the input with the
			// active filter so a no-match search doubles as "create this".
			m.plManager.screen = plMgrScreenNewName
			m.plManager.newName = m.plManager.filter
			m.plManager.inputErr = ""
		}
	case "a":
		// New playlist: open the name input. After naming, the file browser
		// opens targeted at it so folders can be added as [[dir]] sources.
		m.plManager.screen = plMgrScreenNewName
		m.plManager.newName = ""
		m.plManager.inputErr = ""
	case "A":
		return m.plMgrAppendPlaylist()
	case "D":
		// Choose directories for the highlighted playlist: the file browser
		// opens targeted at it, where D/Enter adds folders as [[dir]] sources.
		realIdx := m.plMgrPlaylistRealIndex(m.plManager.cursor)
		if realIdx < 0 {
			return nil
		}
		name := m.plManager.playlists[realIdx].Name
		if plMgrVirtualPlaylistName(name) != "" {
			m.status.Showf(statusTTLDefault, "%q is a virtual playlist with no directory sources", name)
			return nil
		}
		m.openFileBrowserForPlaylist(name)
	case "w":
		tracks := m.playlist.Tracks()
		if len(tracks) == 0 {
			m.status.Warning("Queue is empty", statusTTLShort)
			return nil
		}
		m.openPlaylistPicker(tracks, fmt.Sprintf("Save %d queued tracks", len(tracks)))
	case "r":
		realIdx := m.plMgrPlaylistRealIndex(m.plManager.cursor)
		if realIdx < 0 {
			return nil
		}
		name := m.plManager.playlists[realIdx].Name
		if plMgrVirtualPlaylistName(name) != "" {
			m.status.Warningf(statusTTLDefault, "%s cannot be renamed", name)
			return nil
		}
		m.plManager.renameOldName = name
		m.plManager.renameName = name
		m.plManager.inputErr = ""
		m.plManager.screen = plMgrScreenRename
	case "d":
		idx := m.plMgrPlaylistRealIndex(m.plManager.cursor)
		if idx < 0 {
			break
		}
		if name := m.plManager.playlists[idx].Name; plMgrVirtualPlaylistName(name) != "" {
			m.status.Warningf(statusTTLDefault, "%s cannot be deleted", name)
			return nil
		}
		m.plManager.confirmDel = true
	case "u":
		return m.plMgrUndoLast()
	case "esc", "p":
		if m.plManager.filter != "" {
			// First Esc clears an active filter rather than closing.
			m.plMgrResetFilter()
			return nil
		}
		m.plManager.visible = false
	}
	return nil
}

// handlePlMgrFilterKey handles keys while typing into the `/` filter on either
// the list or tracks screen.
func (m *Model) handlePlMgrFilterKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		// Cancel filter, restore cursor.
		m.plMgrResetFilter()
		m.plManager.cursor = m.plManager.savedCursor
		m.plManager.scroll = m.plManager.savedScroll
		clampCount := m.plMgrListViewCount()
		if m.plManager.screen == plMgrScreenTracks {
			clampCount = m.plMgrTracksViewCount()
		}
		if clampCount > 0 && m.plManager.cursor >= clampCount {
			m.plManager.cursor = clampCount - 1
		}
		return nil
	case "enter":
		// Commit filter; leave query in place but stop intercepting keys.
		m.plManager.filtering = false
		if m.plManager.filter == "" {
			m.plManager.cursor = m.plManager.savedCursor
			m.plManager.scroll = m.plManager.savedScroll
		}
		return nil
	case "down":
		// Drop into result navigation immediately.
		m.plManager.filtering = false
		count := m.plMgrListViewCount()
		if m.plManager.screen == plMgrScreenTracks {
			count = m.plMgrTracksViewCount()
		}
		if count > 0 {
			m.plManager.cursor = 0
			if m.plManager.screen == plMgrScreenList {
				m.plMgrListMaybeAdjustScroll(m.effectivePlaylistVisible())
			} else {
				m.plMgrTracksMaybeAdjustScroll(m.effectivePlaylistVisible())
			}
		}
		return nil
	case "backspace":
		if m.plManager.filter != "" {
			m.editText("playlist-manager-filter", &m.plManager.filter, msg)
			m.plManager.cursor = 0
			m.plMgrRecomputeFilter()
		} else {
			m.plManager.filtering = false
			m.plManager.cursor = m.plManager.savedCursor
			m.plManager.scroll = m.plManager.savedScroll
		}
		return nil
	}

	if msg.Code == tea.KeySpace && msg.Text == "" {
		m.insertText("playlist-manager-filter", &m.plManager.filter, " ")
		m.plManager.cursor = 0
		m.plMgrRecomputeFilter()
		return nil
	}
	if m.editText("playlist-manager-filter", &m.plManager.filter, msg) {
		m.plManager.cursor = 0
		m.plMgrRecomputeFilter()
	}
	return nil
}

// handlePlMgrTracksKey handles keys on screen 1 (track list inside a playlist).
func (m *Model) handlePlMgrTracksKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.plManager.filtering {
		return m.handlePlMgrFilterKey(msg)
	}

	count := m.plMgrTracksViewCount()
	switch msg.String() {
	case "ctrl+h":
		m.toggleAlbumHeadersManual()
		m.plMgrTracksMaybeAdjustScroll(m.effectivePlaylistVisible())
		return nil
	case "/":
		m.plManager.filtering = true
		m.plManager.savedCursor = m.plManager.cursor
		m.plManager.savedScroll = m.plManager.scroll
		m.plManager.filter = ""
		m.plManager.filtered = nil
		m.plManager.cursor = 0
		m.plManager.scroll = 0
		return nil
	case "up", "k":
		if m.plManager.cursor > 0 {
			m.plManager.cursor--
		} else if count > 0 {
			m.plManager.cursor = count - 1
		}
		m.plMgrTracksMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "down", "j":
		if m.plManager.cursor < count-1 {
			m.plManager.cursor++
		} else if count > 0 {
			m.plManager.cursor = 0
		}
		m.plMgrTracksMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "[":
		m.plMgrMoveTrack(-1)
	case "]":
		m.plMgrMoveTrack(1)
	case "ctrl+x":
		m.toggleExpandedView()
		m.plMgrTracksMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "pgup", "ctrl+u":
		if m.plManager.cursor > 0 {
			visible := m.effectivePlaylistVisible()
			m.plManager.cursor -= min(m.plManager.cursor, visible)
			m.plMgrTracksMaybeAdjustScroll(visible)
		}
	case "pgdown", "ctrl+d":
		if m.plManager.cursor < count-1 {
			visible := m.effectivePlaylistVisible()
			m.plManager.cursor = min(count-1, m.plManager.cursor+visible)
			m.plMgrTracksMaybeAdjustScroll(visible)
		}
	case "home", "g":
		m.plManager.cursor = 0
		m.plMgrTracksMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "end", "G":
		if count > 0 {
			m.plManager.cursor = count - 1
		}
		m.plMgrTracksMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "enter":
		// Play the highlighted track; the rest of the playlist follows.
		if len(m.plManager.tracks) > 0 {
			startIdx := m.plMgrTrackRealIndex(m.plManager.cursor)
			if startIdx < 0 {
				startIdx = 0
			}
			return m.plMgrLoadAndPlay(startIdx)
		}
	case "p":
		// Play all from the top, regardless of cursor.
		if len(m.plManager.tracks) > 0 {
			return m.plMgrLoadAndPlay(0)
		}
	case "space":
		realIdx := m.plMgrTrackRealIndex(m.plManager.cursor)
		m.plMgrToggleMark(realIdx)
		if m.plManager.cursor < count-1 {
			m.plManager.cursor++
			m.plMgrTracksMaybeAdjustScroll(m.effectivePlaylistVisible())
		}
	case "a":
		m.plMgrToggleMarkAll()
	case "A":
		return m.plMgrAppendSelectedTracks()
	case "s":
		m.plMgrSortTracks()
	case "w":
		tracks := m.plMgrSelectedTracks()
		if len(tracks) > 0 {
			title := "Track: " + tracks[0].DisplayName()
			if len(tracks) > 1 {
				title = fmt.Sprintf("%d tracks selected", len(tracks))
			}
			m.openPlaylistPicker(tracks, title)
		}
	case "o":
		m.openFileBrowserForPlaylist(m.plManager.selPlaylist)
	case "D":
		m.plMgrOpenDirs()
	case "f":
		if m.favStore != nil {
			realIdx := m.plMgrTrackRealIndex(m.plManager.cursor)
			if realIdx >= 0 && realIdx < len(m.plManager.tracks) {
				track := m.plManager.tracks[realIdx]
				cmd, err := m.toggleTrackFavorite(track)
				if err != nil {
					m.status.Errorf(statusTTLDefault, "Favorite failed: %s", err)
					return nil
				}
				// Inside the Favorites screen a toggle re-reads the store so
				// the rows mirror it: an unfavorite drops the row, a
				// re-favorite restores it.
				if m.plManager.selPlaylist == favorites.PlaylistName {
					m.plMgrReloadTracks(favorites.PlaylistName)
				}
				if m.plManager.visible {
					m.plMgrRefreshList()
				}
				return cmd
			}
		}
	case "d":
		m.plMgrRemoveSelectedTracks()
	case "u":
		return m.plMgrUndoLast()
	case "esc", "backspace", "h", "left":
		if m.plManager.filter != "" {
			m.plMgrResetFilter()
			return nil
		}
		// Go back to playlist list.
		m.plMgrRefreshList()
		m.plManager.screen = plMgrScreenList
		m.plMgrResetFilter()
		// Try to position cursor on the playlist we just left.
		for i, pl := range m.plManager.playlists {
			if pl.Name == m.plManager.selPlaylist {
				m.plManager.cursor = i
				break
			}
		}
		m.plManager.confirmDel = false
	}
	return nil
}

// plMgrLoadAndPlay replaces the live playlist with the manager's tracks and
// starts playback at startIdx.
func (m *Model) plMgrLoadAndPlay(startIdx int) tea.Cmd {
	m.stopPlayback()
	m.player.ClearPreload()
	m.retireTracksPaging()
	m.replacePlaylist(m.plManager.tracks)
	m.setHeaderStateFromTracks(m.plManager.tracks)
	// The manager lists the playlists of the local provider.
	localName := ""
	if m.localProvider != nil {
		localName = m.localProvider.Name()
	}
	m.setLoadedLocalPlaylist(localName, m.plManager.selPlaylist)
	if startIdx < 0 || startIdx >= m.playlist.Len() {
		startIdx = 0
	}
	m.plCursor = startIdx
	m.playlist.SetIndex(startIdx)
	m.adjustScroll()
	m.plManager.visible = false
	m.plMgrResetFilter()
	m.focus = focusPlaylist
	return m.playCurrentTrack()
}

// handlePlMgrDirsKey handles keys on the directory-sources screen. The screen
// lists [[dir]] sources and supports add (via the file browser), remove
// (y/n confirm), and toggle-recursive. Navigation mirrors the other screens.
func (m *Model) handlePlMgrDirsKey(msg tea.KeyPressMsg) tea.Cmd {
	count := len(m.plManager.dirs)

	// Remove-confirmation flow takes priority once armed.
	if m.plManager.confirmDel {
		switch msg.String() {
		case "y", "Y":
			i := m.plManager.cursor
			if i >= 0 && i < count {
				src := m.plManager.dirs[i]
				if dm, ok := m.localProvider.(provider.PlaylistDirSourceManager); ok {
					if err := dm.RemoveDirSource(m.plManager.selPlaylist, src.Path); err != nil {
						m.status.Errorf(statusTTLDefault, "Remove failed: %s", err)
					} else {
						m.plMgrReloadDirs()
						m.plMgrRefreshTracksForSel()
						m.plMgrRefreshList()
						m.status.Showf(statusTTLDefault, "Removed %q from %q", src.Path, m.plManager.selPlaylist)
					}
				}
			}
			m.plManager.confirmDel = false
			return nil
		default:
			m.plManager.confirmDel = false
			return nil
		}
	}

	switch msg.String() {
	case "up", "k":
		if m.plManager.cursor > 0 {
			m.plManager.cursor--
		} else if count > 0 {
			m.plManager.cursor = count - 1
		}
		m.plMgrDirsMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "down", "j":
		if m.plManager.cursor < count-1 {
			m.plManager.cursor++
		} else if count > 0 {
			m.plManager.cursor = 0
		}
		m.plMgrDirsMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "pgup", "ctrl+u":
		if m.plManager.cursor > 0 {
			visible := m.effectivePlaylistVisible()
			m.plManager.cursor -= min(m.plManager.cursor, visible)
			m.plMgrDirsMaybeAdjustScroll(visible)
		}
	case "pgdown", "ctrl+d":
		if m.plManager.cursor < count-1 {
			visible := m.effectivePlaylistVisible()
			m.plManager.cursor = min(count-1, m.plManager.cursor+visible)
			m.plMgrDirsMaybeAdjustScroll(visible)
		}
	case "home", "g":
		m.plManager.cursor = 0
		m.plMgrDirsMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "end", "G":
		if count > 0 {
			m.plManager.cursor = count - 1
		}
		m.plMgrDirsMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "a":
		// Open the file browser to pick a directory; the browser's D action
		// adds the picked directory as a [[dir]] source to this playlist.
		m.openFileBrowserForPlaylist(m.plManager.selPlaylist)
		return nil
	case "d":
		if count == 0 {
			return nil
		}
		m.plManager.confirmDel = true
	case "r":
		if count == 0 {
			return nil
		}
		src := m.plManager.dirs[m.plManager.cursor]
		if dm, ok := m.localProvider.(provider.PlaylistDirSourceManager); ok {
			next := !src.Recursive
			if err := dm.SetDirRecursive(m.plManager.selPlaylist, src.Path, next); err != nil {
				m.status.Errorf(statusTTLDefault, "Toggle recursive: %s", err)
			} else {
				mode := "recursive"
				if !next {
					mode = "flat"
				}
				m.plMgrReloadDirs()
				m.plMgrRefreshTracksForSel()
				m.plMgrRefreshList()
				m.status.Showf(statusTTLDefault, "Set %q %s", src.Path, mode)
			}
		}
	case "esc", "backspace", "h", "left":
		// Back to the tracks screen; reload tracks so dir changes are shown.
		m.plMgrEnterTrackList(m.plManager.selPlaylist)
	}
	return nil
}

// handlePlMgrNewNameKey handles keys on screen 2 (new playlist name input).
func (m *Model) handlePlMgrNewNameKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.plManager.screen = plMgrScreenList
	case tea.KeyEnter:
		name := strings.TrimSpace(m.plManager.newName)
		if name == "" {
			m.plManager.inputErr = "Playlist name is required."
			return nil
		}
		if !m.createPlaylistFromManager(name) {
			return nil
		}
		m.plMgrRefreshList()
		m.plManager.screen = plMgrScreenList
		// Surface the new playlist in the provider pane right away.
		cmd := m.refreshPaneAfterLocalWrite()
		// Drop straight into the file browser targeted at the new
		// playlist, starting from home: select folders and/or files
		// with Space, descend with Enter, finish with Esc.
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			m.fileBrowser.dir = home
		}
		m.openFileBrowserForPlaylist(name)
		m.status.Showf(statusTTLDefault, "Created %q — Space to select, Esc when done", name)
		return cmd
	default:
		if msg.Code == tea.KeySpace && msg.Text == "" {
			m.insertText("playlist-manager-new-name", &m.plManager.newName, " ")
			m.plManager.inputErr = ""
		} else if m.editText("playlist-manager-new-name", &m.plManager.newName, msg) {
			m.plManager.inputErr = ""
		}
	}
	return nil
}

// handlePlMgrRenameKey handles keys on screen 3 (rename playlist input).
func (m *Model) handlePlMgrRenameKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.plManager.screen = plMgrScreenList
	case tea.KeyEnter:
		if m.plMgrCommitRename() {
			m.plManager.screen = plMgrScreenList
		}
	default:
		if msg.Code == tea.KeySpace && msg.Text == "" {
			m.insertText("playlist-manager-rename", &m.plManager.renameName, " ")
			m.plManager.inputErr = ""
		} else if m.editText("playlist-manager-rename", &m.plManager.renameName, msg) {
			m.plManager.inputErr = ""
		}
	}
	return nil
}

// plMgrCommitRename applies the pending rename. No-op when the name is
// empty, unchanged, or the local provider doesn't support renaming.
func (m *Model) plMgrCommitRename() bool {
	newName := strings.TrimSpace(m.plManager.renameName)
	oldName := m.plManager.renameOldName
	if newName == "" {
		m.plManager.inputErr = "Playlist name is required."
		return false
	}
	if newName == oldName {
		return true
	}
	r, ok := m.localProvider.(provider.PlaylistRenamer)
	if !ok {
		m.plManager.inputErr = "Playlist renaming is not supported."
		return false
	}
	if err := r.RenamePlaylist(oldName, newName); err != nil {
		m.plManager.inputErr = "Rename failed: " + err.Error()
		return false
	}
	m.status.Showf(statusTTLDefault, "Renamed %q to %q", oldName, newName)
	m.renameLoadedPlaylist(oldName, newName)
	m.plMgrRefreshList()
	return true
}

func (m *Model) localSaver() provider.PlaylistSaver {
	s, _ := m.localProvider.(provider.PlaylistSaver)
	return s
}

func cloneTracks(tracks []playlist.Track) []playlist.Track {
	cloned := append([]playlist.Track(nil), tracks...)
	for i := range cloned {
		cloned[i].ProviderMeta = maps.Clone(cloned[i].ProviderMeta)
	}
	return cloned
}

func (m *Model) plMgrSetTrackUndo() {
	m.plMgrEnsureMissingLocal()
	undo := plManagerUndo{
		kind:         plUndoTracks,
		name:         m.plManager.selPlaylist,
		tracks:       cloneTracks(m.plManager.tracks),
		missingLocal: append([]bool(nil), m.plManager.missingLocal...),
	}
	// Snapshot the raw document too so undo restores [[dir]] sources that
	// SavePlaylist would drop.
	if d, ok := m.localProvider.(provider.PlaylistDocumenter); ok {
		if data, err := d.PlaylistDocument(undo.name); err == nil {
			undo.doc = data
		}
	}
	m.plManager.undo = undo
}

// plMgrUndoLast restores the last deleted playlist or removed tracks and
// returns a provider-pane refresh command when a playlist came back, so the
// pane lists it without a pill switch.
func (m *Model) plMgrUndoLast() tea.Cmd {
	undo := m.plManager.undo
	if undo.kind == plUndoNone || undo.name == "" {
		m.status.Warning("Nothing to undo", statusTTLShort)
		return nil
	}
	saver := m.localSaver()
	if saver == nil {
		m.status.Warning("Undo unavailable", statusTTLDefault)
		return nil
	}
	if len(undo.doc) > 0 {
		if r, ok := saver.(provider.PlaylistDocumenter); ok {
			if err := r.RestorePlaylistDocument(undo.name, undo.doc); err != nil {
				m.status.Errorf(statusTTLDefault, "Undo failed: %s", err)
				return nil
			}
			m.plMgrFinishUndo(undo)
			return m.undoRefreshCmd(undo)
		}
	}
	if err := saver.SavePlaylist(undo.name, cloneTracks(undo.tracks)); err != nil {
		m.status.Errorf(statusTTLDefault, "Undo failed: %s", err)
		return nil
	}
	m.plMgrFinishUndo(undo)
	return m.undoRefreshCmd(undo)
}

// undoRefreshCmd schedules a provider-pane refresh after a deleted playlist
// comes back, so the pane lists it without a pill switch.
func (m *Model) undoRefreshCmd(undo plManagerUndo) tea.Cmd {
	if undo.kind == plUndoPlaylist {
		return m.refreshPaneAfterLocalWrite()
	}
	return nil
}

// plMgrFinishUndo clears the undo slot and refreshes the manager list plus an
// open tracks screen after a successful restore.
func (m *Model) plMgrFinishUndo(undo plManagerUndo) {
	m.plManager.undo = plManagerUndo{}
	m.plMgrRefreshList()
	if m.plManager.screen == plMgrScreenTracks && m.plManager.selPlaylist == undo.name {
		m.plMgrRestoreTracks(undo.tracks, undo.missingLocal)
		m.plManager.marked = make(map[int]bool)
		m.plMgrRecomputeFilter()
		m.plMgrTracksMaybeAdjustScroll(m.effectivePlaylistVisible())
	}
	m.status.Showf(statusTTLDefault, "Restored %q", undo.name)
}

func (m *Model) plMgrSelectedTrackIndices() []int {
	if len(m.plManager.marked) > 0 {
		indices := make([]int, 0, len(m.plManager.marked))
		for idx := range m.plManager.marked {
			if idx >= 0 && idx < len(m.plManager.tracks) {
				indices = append(indices, idx)
			}
		}
		sort.Ints(indices)
		return indices
	}
	realIdx := m.plMgrTrackRealIndex(m.plManager.cursor)
	if realIdx < 0 {
		return nil
	}
	return []int{realIdx}
}

func (m *Model) plMgrSelectedTracks() []playlist.Track {
	indices := m.plMgrSelectedTrackIndices()
	tracks := make([]playlist.Track, 0, len(indices))
	for _, idx := range indices {
		tracks = append(tracks, m.plManager.tracks[idx])
	}
	return tracks
}

func (m *Model) plMgrToggleMark(realIdx int) {
	if realIdx < 0 || realIdx >= len(m.plManager.tracks) {
		return
	}
	if m.plManager.marked == nil {
		m.plManager.marked = make(map[int]bool)
	}
	if m.plManager.marked[realIdx] {
		delete(m.plManager.marked, realIdx)
		return
	}
	m.plManager.marked[realIdx] = true
}

func (m *Model) plMgrToggleMarkAll() {
	count := m.plMgrTracksViewCount()
	if count == 0 {
		return
	}
	if m.plManager.marked == nil {
		m.plManager.marked = make(map[int]bool)
	}
	allMarked := true
	for i := 0; i < count; i++ {
		realIdx := m.plMgrTrackRealIndex(i)
		if realIdx >= 0 && !m.plManager.marked[realIdx] {
			allMarked = false
			break
		}
	}
	for i := 0; i < count; i++ {
		realIdx := m.plMgrTrackRealIndex(i)
		if realIdx < 0 {
			continue
		}
		if allMarked {
			delete(m.plManager.marked, realIdx)
		} else {
			m.plManager.marked[realIdx] = true
		}
	}
}

// plMgrUpdateTracks writes an edit of the open playlist through fn in one
// locked update, so a change that another writer made after the manager
// loaded the playlist is kept.
func (m *Model) plMgrUpdateTracks(status string, fn func([]playlist.Track) ([]playlist.Track, error)) bool {
	updater, ok := m.localProvider.(playlistUpdater)
	if !ok {
		m.status.Warning("Playlist saving is not supported", statusTTLDefault)
		return false
	}
	if err := updater.UpdatePlaylist(m.plManager.selPlaylist, fn); err != nil {
		m.status.Errorf(statusTTLDefault, "Save failed: %s", err)
		return false
	}
	if status != "" {
		m.status.Show(status, statusTTLDefault)
	}
	return true
}

// plMgrSaveOrder writes the row order of the manager to the open playlist.
func (m *Model) plMgrSaveOrder(status string) bool {
	order := m.plManager.tracks
	return m.plMgrUpdateTracks(status, func(tracks []playlist.Track) ([]playlist.Track, error) {
		return orderByRows(tracks, order), nil
	})
}

// plMgrRemoveSelectedTracks removes the selected tracks from the open playlist.
func (m *Model) plMgrRemoveSelectedTracks() {
	indices := m.plMgrSelectedTrackIndices()
	if len(indices) == 0 {
		return
	}
	if m.plManager.selPlaylist == favorites.PlaylistName {
		m.status.Warning("Use n to remove tracks from Favorites", statusTTLDefault)
		return
	}
	if m.plManager.selPlaylist == history.PlaylistName {
		m.status.Warning("Recently Played tracks cannot be removed", statusTTLDefault)
		return
	}
	for _, i := range indices {
		if m.plManager.tracks[i].DirSourced {
			m.status.Warningf(statusTTLDefault, "Can't remove %q: it's supplied by the playlist's directory source", m.plManager.tracks[i].DisplayName())
			return
		}
	}
	m.plMgrSetTrackUndo()
	rows := pathRowsOf(m.plManager.tracks)
	drop := make(map[pathRow]bool, len(indices))
	for _, idx := range indices {
		drop[rows[idx]] = true
	}
	for i := len(indices) - 1; i >= 0; i-- {
		idx := indices[i]
		m.plManager.tracks = append(m.plManager.tracks[:idx], m.plManager.tracks[idx+1:]...)
		m.plManager.missingLocal = append(m.plManager.missingLocal[:idx], m.plManager.missingLocal[idx+1:]...)
	}
	m.plManager.marked = make(map[int]bool)
	removed := m.plMgrUpdateTracks(fmt.Sprintf("Removed %d track(s) from %q", len(indices), m.plManager.selPlaylist), func(tracks []playlist.Track) ([]playlist.Track, error) {
		kept := make([]playlist.Track, 0, len(tracks))
		for i, row := range pathRowsOf(tracks) {
			if !drop[row] {
				kept = append(kept, tracks[i])
			}
		}
		return kept, nil
	})
	if !removed {
		m.plMgrRestoreTracks(m.plManager.undo.tracks, m.plManager.undo.missingLocal)
		return
	}
	if m.plManager.filter != "" {
		m.plMgrRecomputeFilter()
	}
	newCount := m.plMgrTracksViewCount()
	if m.plManager.cursor >= newCount {
		m.plManager.cursor = newCount - 1
	}
	if m.plManager.cursor < 0 {
		m.plManager.cursor = 0
	}
	m.plMgrTracksMaybeAdjustScroll(m.effectivePlaylistVisible())
}

func (m *Model) plMgrMoveTrack(delta int) {
	if m.plManager.filter != "" {
		m.status.Warning("Clear filter before moving tracks", statusTTLDefault)
		return
	}
	from := m.plManager.cursor
	to := from + delta
	if from < 0 || from >= len(m.plManager.tracks) || to < 0 || to >= len(m.plManager.tracks) {
		return
	}
	m.plMgrSetTrackUndo()
	m.plManager.tracks[from], m.plManager.tracks[to] = m.plManager.tracks[to], m.plManager.tracks[from]
	m.plManager.missingLocal[from], m.plManager.missingLocal[to] = m.plManager.missingLocal[to], m.plManager.missingLocal[from]
	m.plManager.cursor = to
	m.plManager.marked = make(map[int]bool)
	if m.plMgrSaveOrder(fmt.Sprintf("Reordered %q", m.plManager.selPlaylist)) {
		m.plMgrTracksMaybeAdjustScroll(m.effectivePlaylistVisible())
	} else {
		m.plMgrRestoreTracks(m.plManager.undo.tracks, m.plManager.undo.missingLocal)
	}
}

var plMgrSortModes = []string{"track", "title", "artist", "album", "artist+album", "path"}

func (m *Model) plMgrSortTracks() {
	if len(m.plManager.tracks) < 2 {
		return
	}
	m.plMgrSetTrackUndo()
	mode := plMgrSortModes[m.plManager.sortMode%len(plMgrSortModes)]
	m.plManager.sortMode++
	order := make([]int, len(m.plManager.tracks))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		return compareUITracks(m.plManager.tracks[order[i]], m.plManager.tracks[order[j]], mode) < 0
	})
	tracks := make([]playlist.Track, len(order))
	missingLocal := make([]bool, len(order))
	for i, idx := range order {
		tracks[i] = m.plManager.tracks[idx]
		missingLocal[i] = m.plManager.missingLocal[idx]
	}
	m.plManager.tracks = tracks
	m.plManager.missingLocal = missingLocal
	m.plManager.marked = make(map[int]bool)
	if m.plMgrSaveOrder(fmt.Sprintf("Sorted %q by %s", m.plManager.selPlaylist, mode)) {
		m.plManager.cursor = 0
		m.plManager.scroll = 0
		m.plMgrRecomputeFilter()
	} else {
		m.plMgrRestoreTracks(m.plManager.undo.tracks, m.plManager.undo.missingLocal)
	}
}

func compareUITracks(a, b playlist.Track, mode string) int {
	cmpString := func(x, y string) int {
		return strings.Compare(strings.ToLower(x), strings.ToLower(y))
	}
	first := func(values ...int) int {
		for _, v := range values {
			if v != 0 {
				return v
			}
		}
		return 0
	}
	switch mode {
	case "track":
		return first(a.TrackNumber-b.TrackNumber, cmpString(a.Title, b.Title), cmpString(a.Path, b.Path))
	case "artist":
		return first(cmpString(a.Artist, b.Artist), cmpString(a.Album, b.Album), a.TrackNumber-b.TrackNumber, cmpString(a.Title, b.Title), cmpString(a.Path, b.Path))
	case "album":
		return first(cmpString(a.Album, b.Album), a.TrackNumber-b.TrackNumber, cmpString(a.Title, b.Title), cmpString(a.Path, b.Path))
	case "artist+album":
		return first(cmpString(a.Artist, b.Artist), cmpString(a.Album, b.Album), a.TrackNumber-b.TrackNumber, cmpString(a.Title, b.Title), cmpString(a.Path, b.Path))
	case "path":
		return cmpString(a.Path, b.Path)
	default:
		return first(cmpString(a.Title, b.Title), cmpString(a.Artist, b.Artist), cmpString(a.Path, b.Path))
	}
}

func (m *Model) createPlaylistFromManager(name string) bool {
	c, ok := m.localProvider.(provider.PlaylistCreator)
	if !ok {
		m.plManager.inputErr = "Playlist creation is not supported."
		return false
	}
	if _, err := c.CreatePlaylist(context.Background(), name); err != nil {
		m.plManager.inputErr = "Create failed: " + err.Error()
		return false
	}
	return true
}
