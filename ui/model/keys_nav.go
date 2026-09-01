package model

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// handleNavBrowserKey processes key presses while the provider browser is open.
func (m *Model) handleNavBrowserKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.navBrowser.prov == nil {
		m.navBrowser.visible = false
		return nil
	}

	key := msg.String()

	// Providers with a preferred route use N as the mode chooser while their
	// hierarchy is open. Other providers retain N as the Navidrome quick-switch.
	if !m.navBrowser.searching && key == "N" {
		if _, ok := m.navBrowser.prov.(provider.DefaultBrowseModeProvider); ok {
			m.openNavBrowserWith(m.navBrowser.prov)
			return nil
		}
	}

	if !m.navBrowser.searching && key == "ctrl+f" {
		m.openProviderSearchWith(m.navBrowser.prov)
		return nil
	}

	// Shift+letter quick-switch to another provider — only when not typing
	// into the filter, so users can still type capital letters in queries.
	if !m.navBrowser.searching && (key != "R" || m.navBrowser.screen != navBrowseScreenTracks) {
		if cmd := m.quickSwitchProvider(key); cmd != nil {
			return cmd
		}
	}

	// Search bar: active on any list/track screen (not the mode menu).
	if m.navBrowser.mode != navBrowseModeMenu {
		if m.navBrowser.searching {
			return m.handleNavSearchKey(msg)
		}

		if key == "ctrl+x" {
			m.toggleExpandedView()
			m.navMaybeAdjustScroll()
			return nil
		}

		if key == "/" {
			// Toggle: if already filtered, clear; otherwise open.
			if m.navBrowser.search != "" {
				m.navClearSearch()
			} else {
				m.navBrowser.searching = true
				if m.navBrowser.mode == navBrowseModeByAlbum &&
					m.navBrowser.screen == navBrowseScreenList && !m.navBrowser.albumDone {
					if ab, ok := m.navBrowser.prov.(provider.AlbumBrowser); ok {
						m.navBrowser.albumLoading = true
						return fetchNavRemainingAlbumsCmd(ab, m.navBrowser.sortType, len(m.navBrowser.albums), m.nextNavRequest())
					}
				}
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
	case navBrowseModeByGenre:
		return m.handleNavByGenreKey(msg)
	}
	return nil
}

type navMenuItem struct {
	label string
	mode  provider.BrowseMode
}

// navMenuItems lists only the routes the browsed provider can actually serve.
// Offering a level the provider does not implement is not a harmless extra
// row: selecting it closes the browser with no explanation.
func (m Model) navMenuItems() []navMenuItem {
	labels := m.navLabels()
	var items []navMenuItem
	if _, ok := m.navBrowser.prov.(provider.AlbumBrowser); ok {
		items = append(items, navMenuItem{label: "By " + labels.album, mode: provider.BrowseAlbums})
	}
	if _, ok := m.navBrowser.prov.(provider.ArtistBrowser); ok {
		items = append(items,
			navMenuItem{label: "By " + labels.artist, mode: provider.BrowseArtists},
			navMenuItem{label: "By " + labels.artist + " / " + labels.album, mode: provider.BrowseArtistAlbums},
		)
	}
	if _, ok := m.navBrowser.prov.(provider.GenreBrowser); ok {
		items = append(items, navMenuItem{label: labels.genresTitle(), mode: provider.BrowseGenres})
	}
	return items
}

func (m *Model) handleNavMenuKey(msg tea.KeyPressMsg) tea.Cmd {
	menuItems := m.navMenuItems()
	menuLen := len(menuItems)
	switch msg.String() {
	case "ctrl+x":
		m.toggleExpandedView()
		return nil
	case "ctrl+c":
		m.navBrowser.visible = false
		return m.quit()
	case "up", "k":
		if m.navBrowser.cursor > 0 {
			m.navBrowser.cursor--
		} else if menuLen > 0 {
			m.navBrowser.cursor = menuLen - 1
		}
	case "down", "j":
		if m.navBrowser.cursor < menuLen-1 {
			m.navBrowser.cursor++
		} else {
			m.navBrowser.cursor = 0
		}
	case "enter", "l", "right":
		if m.navBrowser.cursor >= 0 && m.navBrowser.cursor < menuLen {
			return m.openNavBrowserAt(m.navBrowser.prov, menuItems[m.navBrowser.cursor].mode)
		}
	case "esc", "N", "backspace", "b":
		m.cancelNavRequests()
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

func (m *Model) handleNavByGenreKey(msg tea.KeyPressMsg) tea.Cmd {
	switch m.navBrowser.screen {
	case navBrowseScreenList:
		return m.handleNavGenreListKey(msg)
	case navBrowseScreenAlbums:
		return m.handleNavGenreSortKey(msg)
	case navBrowseScreenTracks:
		return m.handleNavTrackListKey(msg)
	}
	return nil
}

func (m *Model) handleNavGenreListKey(msg tea.KeyPressMsg) tea.Cmd {
	listLen := len(m.navBrowser.genres)
	if m.navBrowser.search != "" {
		listLen = len(m.navBrowser.searchIdx)
	}

	switch msg.String() {
	case "ctrl+c":
		m.navBrowser.visible = false
		return m.quit()
	case "up", "k":
		if m.navBrowser.cursor > 0 {
			m.navBrowser.cursor--
		} else if listLen > 0 {
			m.navBrowser.cursor = listLen - 1
		}
		m.navMaybeAdjustScroll()
	case "down", "j":
		if m.navBrowser.cursor < listLen-1 {
			m.navBrowser.cursor++
		} else if listLen > 0 {
			m.navBrowser.cursor = 0
		}
		m.navMaybeAdjustScroll()
	case "enter", "l", "right":
		genre, ok := m.selectedNavGenre()
		if m.navBrowser.loading || !ok {
			return nil
		}
		browser, ok := m.navBrowser.prov.(provider.GenreBrowser)
		if !ok {
			return nil
		}
		m.navBrowser.selGenre = genre
		m.navBrowser.genreSorts = browser.GenreSortTypes()
		m.navBrowser.screen = navBrowseScreenAlbums
		m.navClearSearch()
	case "f":
		genre, ok := m.selectedNavGenre()
		if m.navBrowser.loading || !ok {
			return nil
		}
		browser, ok := m.navBrowser.prov.(provider.GenreFavoriteToggler)
		if !ok {
			return nil
		}
		favorite, err := browser.ToggleGenreFavorite(genre.ID)
		if err != nil {
			m.status.Errorf(statusTTLDefault, "Genre favorite save failed: %s", err)
			return nil
		}
		rawIdx := m.selectedNavRawIndex(len(m.navBrowser.genres))
		m.navBrowser.genres[rawIdx].Favorite = favorite
		providerName := m.navBrowser.prov.Name()
		if favorite {
			m.status.Showf(statusTTLDefault, "★ Added %s to %s favorites", genre.Name, providerName)
		} else {
			m.status.Showf(statusTTLDefault, "☆ Removed %s from %s favorites", genre.Name, providerName)
		}
		if m.provider != nil && m.provider == m.navBrowser.prov {
			return m.fetchProviderPlaylists()
		}
	case "esc", "h", "left", "backspace":
		m.navBackFromRoot()
	}
	return nil
}

func (m *Model) handleNavGenreSortKey(msg tea.KeyPressMsg) tea.Cmd {
	listLen := len(m.navBrowser.genreSorts)
	if m.navBrowser.search != "" {
		listLen = len(m.navBrowser.searchIdx)
	}
	switch msg.String() {
	case "ctrl+c":
		m.navBrowser.visible = false
		return m.quit()
	case "up", "k":
		if m.navBrowser.cursor > 0 {
			m.navBrowser.cursor--
		} else if listLen > 0 {
			m.navBrowser.cursor = listLen - 1
		}
		m.navMaybeAdjustScroll()
	case "down", "j":
		if m.navBrowser.cursor < listLen-1 {
			m.navBrowser.cursor++
		} else if listLen > 0 {
			m.navBrowser.cursor = 0
		}
		m.navMaybeAdjustScroll()
	case "enter", "l", "right":
		if listLen == 0 {
			return nil
		}
		rawIdx := m.selectedNavRawIndex(len(m.navBrowser.genreSorts))
		if rawIdx < 0 {
			return nil
		}
		browser, ok := m.navBrowser.prov.(provider.GenreBrowser)
		if !ok {
			return nil
		}
		m.navBrowser.selGenreSort = m.navBrowser.genreSorts[rawIdx]
		m.navBrowser.loading = true
		m.navClearSearchKeepingCursor(rawIdx)
		return fetchNavGenreTracksCmd(browser, m.navBrowser.selGenre.ID, m.navBrowser.selGenreSort.ID, m.nextNavRequest())
	case "esc", "h", "left", "backspace":
		m.cancelNavRequests()
		m.navClearSearch()
		m.navBrowser.screen = navBrowseScreenList
	}
	return nil
}

func (m Model) selectedNavRawIndex(rawLen int) int {
	idx := m.navBrowser.cursor
	if m.navBrowser.search != "" {
		if idx < 0 || idx >= len(m.navBrowser.searchIdx) {
			return -1
		}
		idx = m.navBrowser.searchIdx[idx]
	}
	if idx < 0 || idx >= rawLen {
		return -1
	}
	return idx
}

func (m Model) selectedNavGenre() (provider.GenreInfo, bool) {
	idx := m.selectedNavRawIndex(len(m.navBrowser.genres))
	if idx < 0 {
		return provider.GenreInfo{}, false
	}
	return m.navBrowser.genres[idx], true
}

// handleNavArtistListKey handles the artist list screen.
func (m *Model) handleNavArtistListKey(msg tea.KeyPressMsg) tea.Cmd {
	// Determine effective list length (filtered or full).
	listLen := len(m.navBrowser.artists)
	if m.navBrowser.search != "" {
		listLen = len(m.navBrowser.searchIdx)
	}

	switch msg.String() {
	case "ctrl+c":
		m.navBrowser.visible = false
		return m.quit()
	case "up", "k":
		if m.navBrowser.cursor > 0 {
			m.navBrowser.cursor--
		} else if listLen > 0 {
			m.navBrowser.cursor = listLen - 1
		}
		m.navMaybeAdjustScroll()
	case "down", "j":
		if m.navBrowser.cursor < listLen-1 {
			m.navBrowser.cursor++
		} else if listLen > 0 {
			m.navBrowser.cursor = 0
		}
		m.navMaybeAdjustScroll()
	case "enter", "l", "right":
		if m.navBrowser.loading || listLen == 0 {
			return nil
		}
		ab, ok := m.navBrowser.prov.(provider.ArtistBrowser)
		if !ok {
			return nil
		}
		// Resolve raw index (filtered or direct).
		rawIdx := m.navBrowser.cursor
		if m.navBrowser.search != "" && m.navBrowser.cursor < len(m.navBrowser.searchIdx) {
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
			return fetchNavArtistAlbumsCmd(ab, artist.ID, m.nextNavRequest())
		}
		m.navClearSearchKeepingCursor(rawIdx)
		return m.fetchNavArtistAllTracksCmd(ab, artist.ID)
	case "esc", "h", "left", "backspace":
		m.navBackFromRoot()
	}
	return nil
}

// handleNavAlbumListKey handles the album list screen.
// artistAlbums=true means this is the artist's album sub-screen (ArtistAlbum mode), not the global list.
func (m *Model) handleNavAlbumListKey(msg tea.KeyPressMsg, artistAlbums bool) tea.Cmd {
	// Determine effective list length (filtered or full).
	listLen := len(m.navBrowser.albums)
	if m.navBrowser.search != "" {
		listLen = len(m.navBrowser.searchIdx)
	}

	switch msg.String() {
	case "ctrl+c":
		m.navBrowser.visible = false
		return m.quit()
	case "up", "k":
		if m.navBrowser.cursor > 0 {
			m.navBrowser.cursor--
		} else if listLen > 0 {
			m.navBrowser.cursor = listLen - 1
		}
		m.navMaybeAdjustScroll()
	case "down", "j":
		if m.navBrowser.cursor < listLen-1 {
			m.navBrowser.cursor++
			m.navMaybeAdjustScroll()
			// Lazy-load next page: only trigger on the raw (unfiltered) list.
			if !artistAlbums && m.navBrowser.search == "" && !m.navBrowser.albumLoading && !m.navBrowser.albumDone && m.navBrowser.cursor >= len(m.navBrowser.albums)-10 {
				if ab, ok := m.navBrowser.prov.(provider.AlbumBrowser); ok {
					m.navBrowser.albumLoading = true
					return fetchNavAlbumListCmd(ab, m.navBrowser.sortType, len(m.navBrowser.albums), m.nextNavRequest())
				}
			}
		} else if listLen > 0 {
			m.navBrowser.cursor = 0
			m.navMaybeAdjustScroll()
		}
	case "enter", "l", "right":
		if (m.navBrowser.loading && !artistAlbums) || listLen == 0 {
			return nil
		}
		// Resolve raw index (filtered or direct).
		rawIdx := m.navBrowser.cursor
		if m.navBrowser.search != "" && m.navBrowser.cursor < len(m.navBrowser.searchIdx) {
			rawIdx = m.navBrowser.searchIdx[m.navBrowser.cursor]
		}
		album := m.navBrowser.albums[rawIdx]
		m.navBrowser.selAlbum = album
		m.navBrowser.loading = true
		m.navClearSearchKeepingCursor(rawIdx)
		if l, ok := m.navBrowser.prov.(provider.AlbumTrackLoader); ok {
			return fetchNavAlbumTracksCmd(l, album.ID, m.nextNavRequest())
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
				m.status.Errorf(statusTTLDefault, "Sort save failed: %s", err)
			}
		}
		return fetchNavAlbumListCmd(ab, m.navBrowser.sortType, 0, m.nextNavRequest())
	case "esc", "h", "left", "backspace":
		if artistAlbums {
			m.cancelNavRequests()
			m.navClearSearch()
			if m.navBrowser.directTrackJump {
				m.navBrowser.directTrackJump = false
				m.navBrowser.visible = false
				return nil
			}
			// Back to artist list.
			m.navBrowser.screen = navBrowseScreenList
		} else {
			m.navBackFromRoot()
		}
	}
	return nil
}

// handleNavTrackListKey handles the final track-list screen (used by all modes).
func (m *Model) handleNavTrackListKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.navBrowser.confirmReplace {
		switch msg.String() {
		case "enter":
			m.navBrowser.confirmReplace = false
			return m.replacePlaylistFromNav()
		case "esc", "R":
			m.navBrowser.confirmReplace = false
		}
		return nil
	}

	// Determine effective list length (filtered or full).
	listLen := len(m.navBrowser.tracks)
	if m.navBrowser.search != "" {
		listLen = len(m.navBrowser.searchIdx)
	}

	switch msg.String() {
	case "ctrl+h":
		m.toggleAlbumHeadersManual()
		m.navMaybeAdjustScroll()
		return nil
	case "ctrl+c":
		m.navBrowser.visible = false
		return m.quit()
	case "up", "k":
		if m.navBrowser.cursor > 0 {
			m.navBrowser.cursor--
		} else if listLen > 0 {
			m.navBrowser.cursor = listLen - 1
		}
		m.navMaybeAdjustScroll()
	case "down", "j":
		if m.navBrowser.cursor < listLen-1 {
			m.navBrowser.cursor++
		} else if listLen > 0 {
			m.navBrowser.cursor = 0
		}
		m.navMaybeAdjustScroll()
	case "enter":
		// Play the highlighted track immediately, then enqueue everything from
		// that position to the end of the list (capped at 500 total tracks).
		if listLen == 0 {
			return nil
		}
		rawIdx := m.navBrowser.cursor
		if m.navBrowser.search != "" && m.navBrowser.cursor < len(m.navBrowser.searchIdx) {
			rawIdx = m.navBrowser.searchIdx[m.navBrowser.cursor]
		}
		if rawIdx < len(m.navBrowser.tracks) {
			const maxAdd = 500
			m.player.Stop()
			m.player.ClearPreload()

			var toAdd []playlist.Track
			if m.navBrowser.search != "" {
				for j := m.navBrowser.cursor; j < len(m.navBrowser.searchIdx) && len(toAdd) < maxAdd; j++ {
					toAdd = append(toAdd, m.navBrowser.tracks[m.navBrowser.searchIdx[j]])
				}
			} else {
				for i := rawIdx; i < len(m.navBrowser.tracks) && len(toAdd) < maxAdd; i++ {
					toAdd = append(toAdd, m.navBrowser.tracks[i])
				}
			}

			m.playlist.Add(toAdd...)
			m.loadedPlaylist = ""
			m.addToHeaderState(toAdd)
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
		if m.playlist.Len() > 0 {
			m.navBrowser.confirmReplace = true
			return nil
		}
		return m.replacePlaylistFromNav()
	case "a":
		// Append all displayed tracks to the playlist (keep current playback).
		tracks := m.navBrowser.tracks
		if m.navBrowser.search != "" {
			filtered := make([]playlist.Track, 0, len(m.navBrowser.searchIdx))
			for _, i := range m.navBrowser.searchIdx {
				filtered = append(filtered, m.navBrowser.tracks[i])
			}
			tracks = filtered
		}
		if len(tracks) > 0 {
			wasEmpty := m.playlist.Len() == 0
			m.playlist.Add(tracks...)
			m.loadedPlaylist = ""
			m.addToHeaderState(tracks)
			m.status.Showf(statusTTLMedium, "Added %d tracks", len(tracks))
			if wasEmpty || !m.player.IsPlaying() {
				m.playlist.SetIndex(0)
				cmd := m.playCurrentTrack()
				m.notifyPlayback()
				return cmd
			}
		}
	case "q":
		// Add the highlighted track and queue it to play next.
		if listLen == 0 {
			return nil
		}
		rawIdx := m.navBrowser.cursor
		if m.navBrowser.search != "" && m.navBrowser.cursor < len(m.navBrowser.searchIdx) {
			rawIdx = m.navBrowser.searchIdx[m.navBrowser.cursor]
		}
		if rawIdx < len(m.navBrowser.tracks) {
			t := m.navBrowser.tracks[rawIdx]
			m.playlist.Add(t)
			m.loadedPlaylist = ""
			m.addToHeaderState([]playlist.Track{t})
			newIdx := m.playlist.Len() - 1
			m.playlist.Queue(newIdx)
			m.normalizeQueueOverlay()
			m.status.Showf(statusTTLMedium, "Queued: %s", t.DisplayName())
			if !m.player.IsPlaying() {
				cmd := m.nextTrack()
				m.notifyPlayback()
				return cmd
			}
			return m.rearmPreload()
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
		case navBrowseModeByGenre:
			m.navBrowser.screen = navBrowseScreenAlbums
		}
	}
	return nil
}

func (m *Model) navDisplayedTracks() []playlist.Track {
	if m.navBrowser.search == "" {
		return m.navBrowser.tracks
	}
	tracks := make([]playlist.Track, 0, len(m.navBrowser.searchIdx))
	for _, i := range m.navBrowser.searchIdx {
		tracks = append(tracks, m.navBrowser.tracks[i])
	}
	return tracks
}

// replacePlaylistFromNav discards the live queue. Its caller confirms whenever
// that queue is non-empty because this browser replacement has no undo snapshot.
func (m *Model) replacePlaylistFromNav() tea.Cmd {
	tracks := m.navDisplayedTracks()
	if len(tracks) == 0 {
		return nil
	}
	m.player.Stop()
	m.player.ClearPreload()
	m.resetYTDLBatch()
	m.replacePlaylist(tracks)
	m.loadedPlaylist = ""
	m.setHeaderStateFromTracks(tracks)
	m.plCursor = 0
	m.plScroll = 0
	m.playlist.SetIndex(0)
	m.focus = focusPlaylist
	m.navBrowser.visible = false
	m.status.Successf(statusTTLDefault, "Replaced queue with %d tracks", len(tracks))
	cmd := m.playCurrentTrack()
	m.notifyPlayback()
	return cmd
}

// handleNavSearchKey handles key input while the nav search bar is open.
func (m *Model) handleNavSearchKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.navBrowser.searching = false
		return nil
	case tea.KeyEnter:
		m.navBrowser.searching = false
		if m.navBrowser.mode == navBrowseModeByGenre && m.navBrowser.screen == navBrowseScreenList {
			query := strings.TrimSpace(m.navBrowser.search)
			searcher, ok := m.navBrowser.prov.(provider.GenreSearcher)
			if ok && query != "" {
				m.navBrowser.genreQuery = query
				m.navBrowser.search = ""
				m.navBrowser.searchIdx = nil
				m.navBrowser.genres = nil
				m.navBrowser.cursor = 0
				m.navBrowser.scroll = 0
				m.navBrowser.loading = true
				return fetchNavGenreSearchCmd(searcher, query, m.nextNavRequest())
			}
		}
		return nil
	}
	if msg.Code == tea.KeySpace && msg.Text == "" {
		m.insertText("nav-search", &m.navBrowser.search, " ")
	} else if !m.editText("nav-search", &m.navBrowser.search, msg) {
		return nil
	}
	m.navBrowser.cursor = 0
	m.navBrowser.scroll = 0
	m.navUpdateSearch()
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
	count := len(m.navMenuItems())
	switch m.navView() {
	case navViewArtists:
		count = len(m.navBrowser.artists)
	case navViewAlbums:
		count = len(m.navBrowser.albums)
	case navViewTracks:
		count = len(m.navBrowser.tracks)
	case navViewGenres:
		count = len(m.navBrowser.genres)
	case navViewGenreSorts:
		count = len(m.navBrowser.genreSorts)
	}
	if m.navBrowser.search != "" {
		count = len(m.navBrowser.searchIdx)
	}
	clampScroll(&m.navBrowser.cursor, &m.navBrowser.scroll, count, m.navVisible())
}
