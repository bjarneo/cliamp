package model

import (
	tea "charm.land/bubbletea/v2"

	"cliamp/playlist"
	"cliamp/provider"
)

// handleNavBrowserKey processes key presses while the provider browser is open.
func (m *Model) handleNavBrowserKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.navBrowser.prov == nil {
		m.navBrowser.visible = false
		return nil
	}

	// Search bar: active on any list/track screen (not the mode menu).
	if m.navBrowser.mode != navBrowseModeMenu {
		if m.navBrowser.searching {
			return m.handleNavSearchKey(msg)
		}
		if msg.String() == "/" {
			// Toggle: if already filtered, clear; otherwise open.
			if m.navBrowser.search != "" {
				m.navClearSearch()
			} else {
				m.navBrowser.searching = true
			}
			return nil
		}
	}

	switch m.navBrowser.mode {
	case navBrowseModeMenu:
		return m.handleNavMenuKey(msg)
	case navBrowseModeByAlbum:
		return m.handleNavByAlbumKey(msg)
	case navBrowseModeByArtist:
		return m.handleNavByArtistKey(msg)
	case navBrowseModeByArtistAlbum:
		return m.handleNavByArtistAlbumKey(msg)
	}
	return nil
}

func (m *Model) handleNavMenuKey(msg tea.KeyPressMsg) tea.Cmd {
	const menuItems = 3
	switch msg.String() {
	case "ctrl+c":
		m.navBrowser.visible = false
		return m.quit()
	case "up", "k":
		if m.navBrowser.cursor > 0 {
			m.navBrowser.cursor--
		}
	case "down", "j":
		if m.navBrowser.cursor < menuItems-1 {
			m.navBrowser.cursor++
		}
	case "enter", "l", "right":
		switch m.navBrowser.cursor {
		case 0: // By Album
			ab, ok := m.navBrowser.prov.(provider.AlbumBrowser)
			if !ok {
				return nil
			}
			m.navBrowser.mode = navBrowseModeByAlbum
			m.navBrowser.screen = navBrowseScreenList
			m.navBrowser.cursor = 0
			m.navBrowser.scroll = 0
			m.navBrowser.albums = nil
			m.navBrowser.albumLoading = true
			m.navBrowser.albumDone = false
			m.navBrowser.loading = false
			return fetchNavAlbumListCmd(ab, m.navBrowser.sortType, 0)
		case 1: // By Artist
			ab, ok := m.navBrowser.prov.(provider.ArtistBrowser)
			if !ok {
				return nil
			}
			m.navBrowser.mode = navBrowseModeByArtist
			m.navBrowser.screen = navBrowseScreenList
			m.navBrowser.cursor = 0
			m.navBrowser.scroll = 0
			m.navBrowser.artists = nil
			m.navBrowser.loading = true
			return fetchNavArtistsCmd(ab)
		case 2: // By Artist / Album
			ab, ok := m.navBrowser.prov.(provider.ArtistBrowser)
			if !ok {
				return nil
			}
			m.navBrowser.mode = navBrowseModeByArtistAlbum
			m.navBrowser.screen = navBrowseScreenList
			m.navBrowser.cursor = 0
			m.navBrowser.scroll = 0
			m.navBrowser.artists = nil
			m.navBrowser.loading = true
			return fetchNavArtistsCmd(ab)
		}
	case "esc", "N", "backspace", "b":
		m.navBrowser.visible = false
	}
	return nil
}

func (m *Model) handleNavByAlbumKey(msg tea.KeyPressMsg) tea.Cmd {
	switch m.navBrowser.screen {
	case navBrowseScreenList:
		return m.handleNavAlbumListKey(msg, false)
	case navBrowseScreenTracks:
		return m.handleNavTrackListKey(msg)
	}
	return nil
}

func (m *Model) handleNavByArtistKey(msg tea.KeyPressMsg) tea.Cmd {
	switch m.navBrowser.screen {
	case navBrowseScreenList:
		return m.handleNavArtistListKey(msg)
	case navBrowseScreenTracks:
		return m.handleNavTrackListKey(msg)
	}
	return nil
}

func (m *Model) handleNavByArtistAlbumKey(msg tea.KeyPressMsg) tea.Cmd {
	switch m.navBrowser.screen {
	case navBrowseScreenList:
		return m.handleNavArtistListKey(msg)
	case navBrowseScreenAlbums:
		return m.handleNavAlbumListKey(msg, true)
	case navBrowseScreenTracks:
		return m.handleNavTrackListKey(msg)
	}
	return nil
}

// handleNavArtistListKey handles the artist list screen.
func (m *Model) handleNavArtistListKey(msg tea.KeyPressMsg) tea.Cmd {
	// Determine effective list length (filtered or full).
	listLen := len(m.navBrowser.artists)
	if len(m.navBrowser.searchIdx) > 0 {
		listLen = len(m.navBrowser.searchIdx)
	}

	switch msg.String() {
	case "ctrl+c":
		m.navBrowser.visible = false
		return m.quit()
	case "up", "k":
		if m.navBrowser.cursor > 0 {
			m.navBrowser.cursor--
			m.navMaybeAdjustScroll()
		}
	case "down", "j":
		if m.navBrowser.cursor < listLen-1 {
			m.navBrowser.cursor++
			m.navMaybeAdjustScroll()
		}
	case "enter", "l", "right":
		if m.navBrowser.loading || len(m.navBrowser.artists) == 0 {
			return nil
		}
		ab, ok := m.navBrowser.prov.(provider.ArtistBrowser)
		if !ok {
			return nil
		}
		// Resolve raw index (filtered or direct).
		rawIdx := m.navBrowser.cursor
		if len(m.navBrowser.searchIdx) > 0 && m.navBrowser.cursor < len(m.navBrowser.searchIdx) {
			rawIdx = m.navBrowser.searchIdx[m.navBrowser.cursor]
		}
		artist := m.navBrowser.artists[rawIdx]
		m.navBrowser.selArtist = artist
		m.navBrowser.loading = true
		if m.navBrowser.mode == navBrowseModeByArtistAlbum {
			// Drill into album list for this artist.
			m.navBrowser.albums = nil
			m.navBrowser.albumLoading = false
			m.navBrowser.screen = navBrowseScreenAlbums
			m.navBrowser.cursor = 0
			m.navBrowser.scroll = 0
			m.navClearSearch()
			return fetchNavArtistAlbumsCmd(ab, artist.ID)
		}
		m.navClearSearch()
		return m.fetchNavArtistAllTracksCmd(ab, artist.ID)
	case "esc", "h", "left", "backspace":
		// Back to menu.
		m.navClearSearch()
		m.navBrowser.mode = navBrowseModeMenu
		m.navBrowser.screen = navBrowseScreenList
	}
	return nil
}

// handleNavAlbumListKey handles the album list screen.
// artistAlbums=true means this is the artist's album sub-screen (ArtistAlbum mode), not the global list.
func (m *Model) handleNavAlbumListKey(msg tea.KeyPressMsg, artistAlbums bool) tea.Cmd {
	// Determine effective list length (filtered or full).
	listLen := len(m.navBrowser.albums)
	if len(m.navBrowser.searchIdx) > 0 {
		listLen = len(m.navBrowser.searchIdx)
	}

	switch msg.String() {
	case "ctrl+c":
		m.navBrowser.visible = false
		return m.quit()
	case "up", "k":
		if m.navBrowser.cursor > 0 {
			m.navBrowser.cursor--
			m.navMaybeAdjustScroll()
		}
	case "down", "j":
		if m.navBrowser.cursor < listLen-1 {
			m.navBrowser.cursor++
			m.navMaybeAdjustScroll()
			// Lazy-load next page: only trigger on the raw (unfiltered) list.
			if !artistAlbums && len(m.navBrowser.searchIdx) == 0 && !m.navBrowser.albumLoading && !m.navBrowser.albumDone && m.navBrowser.cursor >= len(m.navBrowser.albums)-10 {
				if ab, ok := m.navBrowser.prov.(provider.AlbumBrowser); ok {
					m.navBrowser.albumLoading = true
					return fetchNavAlbumListCmd(ab, m.navBrowser.sortType, len(m.navBrowser.albums))
				}
			}
		}
	case "enter", "l", "right":
		if (m.navBrowser.loading && !artistAlbums) || len(m.navBrowser.albums) == 0 {
			return nil
		}
		// Resolve raw index (filtered or direct).
		rawIdx := m.navBrowser.cursor
		if len(m.navBrowser.searchIdx) > 0 && m.navBrowser.cursor < len(m.navBrowser.searchIdx) {
			rawIdx = m.navBrowser.searchIdx[m.navBrowser.cursor]
		}
		album := m.navBrowser.albums[rawIdx]
		m.navBrowser.selAlbum = album
		m.navBrowser.loading = true
		m.navClearSearch()
		if l, ok := m.navBrowser.prov.(provider.AlbumTrackLoader); ok {
			return fetchNavAlbumTracksCmd(l, album.ID)
		}
		return nil
	case "s":
		if artistAlbums {
			return nil // Sort only applies to global album list.
		}
		ab, ok := m.navBrowser.prov.(provider.AlbumBrowser)
		if !ok {
			return nil
		}
		m.navBrowser.sortType = navNextSort(m.navBrowser.sortType, ab.AlbumSortTypes())
		m.navBrowser.albums = nil
		m.navBrowser.cursor = 0
		m.navBrowser.scroll = 0
		m.navBrowser.albumLoading = true
		m.navBrowser.albumDone = false
		m.navClearSearch()
		if saver, ok := m.navBrowser.prov.(provider.AlbumSortSaver); ok {
			if err := saver.SaveAlbumSort(m.navBrowser.sortType); err != nil {
				m.status.Showf(statusTTLDefault, "Sort save failed: %s", err)
			}
		}
		return fetchNavAlbumListCmd(ab, m.navBrowser.sortType, 0)
	case "esc", "h", "left", "backspace":
		m.navClearSearch()
		if artistAlbums {
			// Back to artist list.
			m.navBrowser.screen = navBrowseScreenList
		} else {
			// Back to menu.
			m.navBrowser.mode = navBrowseModeMenu
			m.navBrowser.screen = navBrowseScreenList
		}
	}
	return nil
}

// handleNavTrackListKey handles the final track-list screen (used by all modes).
func (m *Model) handleNavTrackListKey(msg tea.KeyPressMsg) tea.Cmd {
	// Determine effective list length (filtered or full).
	listLen := len(m.navBrowser.tracks)
	if len(m.navBrowser.searchIdx) > 0 {
		listLen = len(m.navBrowser.searchIdx)
	}

	switch msg.String() {
	case "ctrl+c":
		m.navBrowser.visible = false
		return m.quit()
	case "up", "k":
		if m.navBrowser.cursor > 0 {
			m.navBrowser.cursor--
			m.navMaybeAdjustScroll()
		}
	case "down", "j":
		if m.navBrowser.cursor < listLen-1 {
			m.navBrowser.cursor++
			m.navMaybeAdjustScroll()
		}
	case "enter":
		// Play the selected track immediately, then enqueue everything from that
		// position to the end of the list (capped at 500 total tracks added).
		if len(m.navBrowser.tracks) == 0 {
			return nil
		}
		rawIdx := m.navBrowser.cursor
		if len(m.navBrowser.searchIdx) > 0 && m.navBrowser.cursor < len(m.navBrowser.searchIdx) {
			rawIdx = m.navBrowser.searchIdx[m.navBrowser.cursor]
		}
		if rawIdx < len(m.navBrowser.tracks) {
			const maxAdd = 500
			m.player.Stop()
			m.player.ClearPreload()

			// Build the slice of tracks to add: from rawIdx to end (or 500 max).
			var toAdd []playlist.Track
			if len(m.navBrowser.searchIdx) > 0 {
				// Filtered: use positions from navCursor onward in the filtered list.
				for j := m.navBrowser.cursor; j < len(m.navBrowser.searchIdx) && len(toAdd) < maxAdd; j++ {
					toAdd = append(toAdd, m.navBrowser.tracks[m.navBrowser.searchIdx[j]])
				}
			} else {
				for i := rawIdx; i < len(m.navBrowser.tracks) && len(toAdd) < maxAdd; i++ {
					toAdd = append(toAdd, m.navBrowser.tracks[i])
				}
			}

			m.playlist.Add(toAdd...)
			newIdx := m.playlist.Len() - len(toAdd)
			m.playlist.SetIndex(newIdx)
			m.plCursor = newIdx
			m.adjustScroll()
			if len(toAdd) > 1 {
				m.status.Showf(statusTTLMedium, "Playing: %s (+%d queued)", toAdd[0].DisplayName(), len(toAdd)-1)
			} else {
				m.status.Showf(statusTTLMedium, "Playing: %s", toAdd[0].DisplayName())
			}
			cmd := m.playCurrentTrack()
			m.notifyPlayback()
			return cmd
		}
	case "R":
		// Replace playlist with all displayed tracks and close browser.
		tracks := m.navBrowser.tracks
		if len(m.navBrowser.searchIdx) > 0 {
			// Replace with only the filtered subset.
			filtered := make([]playlist.Track, 0, len(m.navBrowser.searchIdx))
			for _, i := range m.navBrowser.searchIdx {
				filtered = append(filtered, m.navBrowser.tracks[i])
			}
			tracks = filtered
		}
		if len(tracks) > 0 {
			m.player.Stop()
			m.player.ClearPreload()
			m.resetYTDLBatch()
			m.playlist.Replace(tracks)
			m.plCursor = 0
			m.plScroll = 0
			m.playlist.SetIndex(0)
			m.focus = focusPlaylist
			m.navBrowser.visible = false
			cmd := m.playCurrentTrack()
			m.notifyPlayback()
			return cmd
		}
	case "a":
		// Append all displayed tracks to the playlist (keep current playback).
		tracks := m.navBrowser.tracks
		if len(m.navBrowser.searchIdx) > 0 {
			filtered := make([]playlist.Track, 0, len(m.navBrowser.searchIdx))
			for _, i := range m.navBrowser.searchIdx {
				filtered = append(filtered, m.navBrowser.tracks[i])
			}
			tracks = filtered
		}
		if len(tracks) > 0 {
			wasEmpty := m.playlist.Len() == 0
			m.playlist.Add(tracks...)
			m.status.Showf(statusTTLMedium, "Added %d tracks", len(tracks))
			if wasEmpty || !m.player.IsPlaying() {
				m.playlist.SetIndex(0)
				cmd := m.playCurrentTrack()
				m.notifyPlayback()
				return cmd
			}
		}
	case "q":
		// Add selected track to playlist and queue it to play next.
		if len(m.navBrowser.tracks) == 0 {
			return nil
		}
		rawIdx := m.navBrowser.cursor
		if len(m.navBrowser.searchIdx) > 0 && m.navBrowser.cursor < len(m.navBrowser.searchIdx) {
			rawIdx = m.navBrowser.searchIdx[m.navBrowser.cursor]
		}
		if rawIdx < len(m.navBrowser.tracks) {
			t := m.navBrowser.tracks[rawIdx]
			m.playlist.Add(t)
			newIdx := m.playlist.Len() - 1
			m.playlist.Queue(newIdx)
			m.status.Showf(statusTTLMedium, "Queued: %s", t.DisplayName())
			if !m.player.IsPlaying() {
				m.playlist.Next()
				cmd := m.playCurrentTrack()
				m.notifyPlayback()
				return cmd
			}
		}
	case "esc", "h", "left", "backspace":
		// Navigate back one level depending on the mode and how we got here.
		m.navClearSearch()
		m.navBrowser.cursor = 0
		m.navBrowser.scroll = 0
		switch m.navBrowser.mode {
		case navBrowseModeByAlbum:
			m.navBrowser.screen = navBrowseScreenList
		case navBrowseModeByArtist:
			m.navBrowser.screen = navBrowseScreenList
		case navBrowseModeByArtistAlbum:
			m.navBrowser.screen = navBrowseScreenAlbums
		}
	}
	return nil
}

// handleNavSearchKey handles key input while the nav search bar is open.
func (m *Model) handleNavSearchKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.navBrowser.searching = false
		return nil
	case tea.KeyEnter:
		m.navBrowser.searching = false
		return nil
	case tea.KeyBackspace, tea.KeyDelete:
		if m.navBrowser.search != "" {
			m.navBrowser.search = removeLastRune(m.navBrowser.search)
			m.navBrowser.cursor = 0
			m.navBrowser.scroll = 0
			m.navUpdateSearch()
		}
		return nil
	}
	if len(msg.Text) > 0 {
		m.navBrowser.search += msg.Text
		m.navBrowser.cursor = 0
		m.navBrowser.scroll = 0
		m.navUpdateSearch()
	}
	return nil
}

// navNextSort returns the next sort option, wrapping around the list.
func navNextSort(s string, types []provider.SortType) string {
	for i, t := range types {
		if t.ID == s {
			return types[(i+1)%len(types)].ID
		}
	}
	if len(types) > 0 {
		return types[0].ID
	}
	return s
}

// navMaybeAdjustScroll keeps navCursor visible within the rendered list window.
func (m *Model) navMaybeAdjustScroll() {
	visible := max(m.plVisible, 5)
	if m.navBrowser.cursor < m.navBrowser.scroll {
		m.navBrowser.scroll = m.navBrowser.cursor
	}
	if m.navBrowser.cursor >= m.navBrowser.scroll+visible {
		m.navBrowser.scroll = m.navBrowser.cursor - visible + 1
	}
}
