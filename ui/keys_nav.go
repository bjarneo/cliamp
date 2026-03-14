package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"cliamp/config"
	"cliamp/external/navidrome"
	"cliamp/playlist"
)

// handleNavBrowserKey processes key presses while the Navidrome browser is open.
func (m *Model) handleNavBrowserKey(msg tea.KeyMsg) tea.Cmd {
	navClient := m.navClient
	if navClient == nil {
		m.showNavBrowser = false
		return nil
	}

	// Search bar: active on any list/track screen (not the mode menu).
	if m.navMode != navBrowseModeMenu {
		if m.navSearching {
			return m.handleNavSearchKey(msg)
		}
		if msg.String() == "/" {
			// Toggle: if already filtered, clear; otherwise open.
			if m.navSearch != "" {
				m.navClearSearch()
			} else {
				m.navSearching = true
			}
			return nil
		}
	}

	switch m.navMode {
	case navBrowseModeMenu:
		return m.handleNavMenuKey(msg, navClient)
	case navBrowseModeByAlbum:
		return m.handleNavByAlbumKey(msg, navClient)
	case navBrowseModeByArtist:
		return m.handleNavByArtistKey(msg, navClient)
	case navBrowseModeByArtistAlbum:
		return m.handleNavByArtistAlbumKey(msg, navClient)
	}
	return nil
}

func (m *Model) handleNavMenuKey(msg tea.KeyMsg, navClient *navidrome.NavidromeClient) tea.Cmd {
	const menuItems = 3
	switch msg.String() {
	case "ctrl+c":
		m.showNavBrowser = false
		return m.quit()
	case "up", "k":
		if m.navCursor > 0 {
			m.navCursor--
		}
	case "down", "j":
		if m.navCursor < menuItems-1 {
			m.navCursor++
		}
	case "enter", "l", "right":
		switch m.navCursor {
		case 0: // By Album
			m.navMode = navBrowseModeByAlbum
			m.navScreen = navBrowseScreenList
			m.navCursor = 0
			m.navScroll = 0
			m.navAlbums = nil
			m.navAlbumLoading = true
			m.navAlbumDone = false
			m.navLoading = false
			return fetchNavAlbumListCmd(navClient, m.navSortType, 0)
		case 1: // By Artist
			m.navMode = navBrowseModeByArtist
			m.navScreen = navBrowseScreenList
			m.navCursor = 0
			m.navScroll = 0
			m.navArtists = nil
			m.navLoading = true
			return fetchNavArtistsCmd(navClient)
		case 2: // By Artist / Album
			m.navMode = navBrowseModeByArtistAlbum
			m.navScreen = navBrowseScreenList
			m.navCursor = 0
			m.navScroll = 0
			m.navArtists = nil
			m.navLoading = true
			return fetchNavArtistsCmd(navClient)
		}
	case "esc", "N", "backspace", "b":
		m.showNavBrowser = false
	}
	return nil
}

func (m *Model) handleNavByAlbumKey(msg tea.KeyMsg, navClient *navidrome.NavidromeClient) tea.Cmd {
	switch m.navScreen {
	case navBrowseScreenList:
		return m.handleNavAlbumListKey(msg, navClient, false)
	case navBrowseScreenTracks:
		return m.handleNavTrackListKey(msg)
	}
	return nil
}

func (m *Model) handleNavByArtistKey(msg tea.KeyMsg, navClient *navidrome.NavidromeClient) tea.Cmd {
	switch m.navScreen {
	case navBrowseScreenList:
		return m.handleNavArtistListKey(msg, navClient)
	case navBrowseScreenTracks:
		return m.handleNavTrackListKey(msg)
	}
	return nil
}

func (m *Model) handleNavByArtistAlbumKey(msg tea.KeyMsg, navClient *navidrome.NavidromeClient) tea.Cmd {
	switch m.navScreen {
	case navBrowseScreenList:
		return m.handleNavArtistListKey(msg, navClient)
	case navBrowseScreenAlbums:
		return m.handleNavAlbumListKey(msg, navClient, true)
	case navBrowseScreenTracks:
		return m.handleNavTrackListKey(msg)
	}
	return nil
}

// handleNavArtistListKey handles the artist list screen (used by both By Artist and By Artist/Album modes).
func (m *Model) handleNavArtistListKey(msg tea.KeyMsg, navClient *navidrome.NavidromeClient) tea.Cmd {
	// Determine effective list length (filtered or full).
	listLen := len(m.navArtists)
	if len(m.navSearchIdx) > 0 {
		listLen = len(m.navSearchIdx)
	}

	switch msg.String() {
	case "ctrl+c":
		m.showNavBrowser = false
		return m.quit()
	case "up", "k":
		if m.navCursor > 0 {
			m.navCursor--
			m.navMaybeAdjustScroll()
		}
	case "down", "j":
		if m.navCursor < listLen-1 {
			m.navCursor++
			m.navMaybeAdjustScroll()
		}
	case "enter", "l", "right":
		if m.navLoading || len(m.navArtists) == 0 {
			return nil
		}
		// Resolve raw index (filtered or direct).
		rawIdx := m.navCursor
		if len(m.navSearchIdx) > 0 && m.navCursor < len(m.navSearchIdx) {
			rawIdx = m.navSearchIdx[m.navCursor]
		}
		artist := m.navArtists[rawIdx]
		m.navSelArtist = artist
		m.navLoading = true
		if m.navMode == navBrowseModeByArtistAlbum {
			// Drill into album list for this artist.
			m.navAlbums = nil
			m.navAlbumLoading = false
			m.navScreen = navBrowseScreenAlbums
			m.navCursor = 0
			m.navScroll = 0
			m.navClearSearch()
			return fetchNavArtistAlbumsCmd(navClient, artist.ID)
		}
		// By Artist: fetch all albums first, then all tracks via a two-step command.
		// We use a dedicated command that fetches albums then tracks in one shot.
		// Clear any active artist-list search filter before transitioning so that
		// stale navSearchIdx entries are not misapplied to the incoming track list.
		m.navClearSearch()
		return m.fetchNavArtistAllTracksCmd(navClient, artist.ID)
	case "esc", "h", "left", "backspace":
		// Back to menu.
		m.navClearSearch()
		m.navMode = navBrowseModeMenu
		m.navScreen = navBrowseScreenList
	}
	return nil
}

// handleNavAlbumListKey handles the album list screen.
// artistAlbums=true means this is the artist's album sub-screen (ArtistAlbum mode), not the global list.
func (m *Model) handleNavAlbumListKey(msg tea.KeyMsg, navClient *navidrome.NavidromeClient, artistAlbums bool) tea.Cmd {
	// Determine effective list length (filtered or full).
	listLen := len(m.navAlbums)
	if len(m.navSearchIdx) > 0 {
		listLen = len(m.navSearchIdx)
	}

	switch msg.String() {
	case "ctrl+c":
		m.showNavBrowser = false
		return m.quit()
	case "up", "k":
		if m.navCursor > 0 {
			m.navCursor--
			m.navMaybeAdjustScroll()
		}
	case "down", "j":
		if m.navCursor < listLen-1 {
			m.navCursor++
			m.navMaybeAdjustScroll()
			// Lazy-load next page: only trigger on the raw (unfiltered) list.
			if !artistAlbums && len(m.navSearchIdx) == 0 && !m.navAlbumLoading && !m.navAlbumDone && m.navCursor >= len(m.navAlbums)-10 {
				m.navAlbumLoading = true
				return fetchNavAlbumListCmd(navClient, m.navSortType, len(m.navAlbums))
			}
		}
	case "enter", "l", "right":
		if (m.navLoading && !artistAlbums) || len(m.navAlbums) == 0 {
			return nil
		}
		// Resolve raw index (filtered or direct).
		rawIdx := m.navCursor
		if len(m.navSearchIdx) > 0 && m.navCursor < len(m.navSearchIdx) {
			rawIdx = m.navSearchIdx[m.navCursor]
		}
		album := m.navAlbums[rawIdx]
		m.navSelAlbum = album
		m.navLoading = true
		m.navClearSearch()
		return fetchNavAlbumTracksCmd(navClient, album.ID)
	case "s":
		if artistAlbums {
			return nil // Sort only applies to global album list.
		}
		// Cycle to the next sort type.
		m.navSortType = navNextSort(m.navSortType)
		m.navAlbums = nil
		m.navCursor = 0
		m.navScroll = 0
		m.navAlbumLoading = true
		m.navAlbumDone = false
		m.navClearSearch()
		// Persist the new sort preference.
		if err := config.SaveNavidromeSort(m.navSortType); err != nil {
			m.saveMsg = fmt.Sprintf("Sort save failed: %s", err)
			m.saveMsgTTL = 60
		}
		return fetchNavAlbumListCmd(navClient, m.navSortType, 0)
	case "esc", "h", "left", "backspace":
		m.navClearSearch()
		if artistAlbums {
			// Back to artist list.
			m.navScreen = navBrowseScreenList
		} else {
			// Back to menu.
			m.navMode = navBrowseModeMenu
			m.navScreen = navBrowseScreenList
		}
	}
	return nil
}

// handleNavTrackListKey handles the final track-list screen (used by all modes).
func (m *Model) handleNavTrackListKey(msg tea.KeyMsg) tea.Cmd {
	// Determine effective list length (filtered or full).
	listLen := len(m.navTracks)
	if len(m.navSearchIdx) > 0 {
		listLen = len(m.navSearchIdx)
	}

	switch msg.String() {
	case "ctrl+c":
		m.showNavBrowser = false
		return m.quit()
	case "up", "k":
		if m.navCursor > 0 {
			m.navCursor--
			m.navMaybeAdjustScroll()
		}
	case "down", "j":
		if m.navCursor < listLen-1 {
			m.navCursor++
			m.navMaybeAdjustScroll()
		}
	case "enter":
		// Play the selected track immediately, then enqueue everything from that
		// position to the end of the list (capped at 500 total tracks added).
		if len(m.navTracks) == 0 {
			return nil
		}
		rawIdx := m.navCursor
		if len(m.navSearchIdx) > 0 && m.navCursor < len(m.navSearchIdx) {
			rawIdx = m.navSearchIdx[m.navCursor]
		}
		if rawIdx < len(m.navTracks) {
			const maxAdd = 500
			m.player.Stop()
			m.player.ClearPreload()

			// Build the slice of tracks to add: from rawIdx to end (or 500 max).
			var toAdd []playlist.Track
			if len(m.navSearchIdx) > 0 {
				// Filtered: use positions from navCursor onward in the filtered list.
				for j := m.navCursor; j < len(m.navSearchIdx) && len(toAdd) < maxAdd; j++ {
					toAdd = append(toAdd, m.navTracks[m.navSearchIdx[j]])
				}
			} else {
				for i := rawIdx; i < len(m.navTracks) && len(toAdd) < maxAdd; i++ {
					toAdd = append(toAdd, m.navTracks[i])
				}
			}

			m.playlist.Add(toAdd...)
			newIdx := m.playlist.Len() - len(toAdd)
			m.playlist.SetIndex(newIdx)
			m.plCursor = newIdx
			m.adjustScroll()
			if len(toAdd) > 1 {
				m.saveMsg = fmt.Sprintf("Playing: %s (+%d queued)", toAdd[0].DisplayName(), len(toAdd)-1)
			} else {
				m.saveMsg = fmt.Sprintf("Playing: %s", toAdd[0].DisplayName())
			}
			m.saveMsgTTL = 80
			cmd := m.playCurrentTrack()
			m.notifyMPRIS()
			return cmd
		}
	case "R":
		// Replace playlist with all displayed tracks and close browser.
		tracks := m.navTracks
		if len(m.navSearchIdx) > 0 {
			// Replace with only the filtered subset.
			filtered := make([]playlist.Track, 0, len(m.navSearchIdx))
			for _, i := range m.navSearchIdx {
				filtered = append(filtered, m.navTracks[i])
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
			m.showNavBrowser = false
			cmd := m.playCurrentTrack()
			m.notifyMPRIS()
			return cmd
		}
	case "a":
		// Append all displayed tracks to the playlist (keep current playback).
		tracks := m.navTracks
		if len(m.navSearchIdx) > 0 {
			filtered := make([]playlist.Track, 0, len(m.navSearchIdx))
			for _, i := range m.navSearchIdx {
				filtered = append(filtered, m.navTracks[i])
			}
			tracks = filtered
		}
		if len(tracks) > 0 {
			wasEmpty := m.playlist.Len() == 0
			m.playlist.Add(tracks...)
			m.saveMsg = fmt.Sprintf("Added %d tracks", len(tracks))
			m.saveMsgTTL = 80
			if wasEmpty || !m.player.IsPlaying() {
				m.playlist.SetIndex(0)
				cmd := m.playCurrentTrack()
				m.notifyMPRIS()
				return cmd
			}
		}
	case "q":
		// Add selected track to playlist and queue it to play next.
		if len(m.navTracks) == 0 {
			return nil
		}
		rawIdx := m.navCursor
		if len(m.navSearchIdx) > 0 && m.navCursor < len(m.navSearchIdx) {
			rawIdx = m.navSearchIdx[m.navCursor]
		}
		if rawIdx < len(m.navTracks) {
			t := m.navTracks[rawIdx]
			m.playlist.Add(t)
			newIdx := m.playlist.Len() - 1
			m.playlist.Queue(newIdx)
			m.saveMsg = fmt.Sprintf("Queued: %s", t.DisplayName())
			m.saveMsgTTL = 80
			if !m.player.IsPlaying() {
				m.playlist.Next()
				cmd := m.playCurrentTrack()
				m.notifyMPRIS()
				return cmd
			}
		}
	case "esc", "h", "left", "backspace":
		// Navigate back one level depending on the mode and how we got here.
		m.navClearSearch()
		m.navCursor = 0
		m.navScroll = 0
		switch m.navMode {
		case navBrowseModeByAlbum:
			m.navScreen = navBrowseScreenList
		case navBrowseModeByArtist:
			m.navScreen = navBrowseScreenList
		case navBrowseModeByArtistAlbum:
			m.navScreen = navBrowseScreenAlbums
		}
	}
	return nil
}

// handleNavSearchKey handles key input while the nav search bar is open.
func (m *Model) handleNavSearchKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEscape:
		// Close the search bar; keep the filter active so the user can act on results.
		m.navSearching = false
		return nil
	case tea.KeyEnter:
		m.navSearching = false
		return nil
	case tea.KeyBackspace, tea.KeyDelete:
		if m.navSearch != "" {
			m.navSearch = removeLastRune(m.navSearch)
			m.navCursor = 0
			m.navScroll = 0
			m.navUpdateSearch()
		}
		return nil
	}
	// Printable character — append to query.
	if msg.Type == tea.KeyRunes {
		m.navSearch += string(msg.Runes)
		m.navCursor = 0
		m.navScroll = 0
		m.navUpdateSearch()
	}
	return nil
}

// navNextSort returns the sort type that follows s in SortTypes, wrapping around.
func navNextSort(s string) string {
	for i, t := range navidrome.SortTypes {
		if t == s {
			return navidrome.SortTypes[(i+1)%len(navidrome.SortTypes)]
		}
	}
	return navidrome.SortTypes[0]
}

// navMaybeAdjustScroll keeps navCursor visible within the rendered list window.
func (m *Model) navMaybeAdjustScroll() {
	visible := m.plVisible
	if visible < 5 {
		visible = 5
	}
	if m.navCursor < m.navScroll {
		m.navScroll = m.navCursor
	}
	if m.navCursor >= m.navScroll+visible {
		m.navScroll = m.navCursor - visible + 1
	}
}
