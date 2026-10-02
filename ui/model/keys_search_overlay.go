package model

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// handleSearchOverlayKey dispatches key presses to the active provider search screen.
func (m *Model) handleSearchOverlayKey(msg tea.KeyPressMsg) tea.Cmd {
	switch m.searchOverlay.screen {
	case searchOverlayInput:
		return m.handleSearchOverlayInputKey(msg)
	case searchOverlayResults:
		return m.handleSearchOverlayResultsKey(msg)
	case searchOverlayPlaylist:
		return m.handleSearchOverlayPlaylistKey(msg)
	case searchOverlayNewName:
		return m.handleSearchOverlayNewNameKey(msg)
	}
	return nil
}

// handleSearchOverlayInputKey handles text input for the search query.
func (m *Model) handleSearchOverlayInputKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.closeSearchOverlay()
	case tea.KeyEnter:
		if m.searchOverlay.query == "" {
			m.setSearchOverlayError("Enter a search query.")
			return nil
		}
		if !m.searchOverlay.loading {
			s, ok := m.searchOverlay.prov.(provider.Searcher)
			if !ok {
				return nil
			}
			m.searchOverlay.loading = true
			m.searchOverlay.err = ""
			return fetchSearchOverlayCmd(m.newSearchOverlayRequestContext(30*time.Second), s, m.searchOverlay.prov.Name(), m.searchOverlay.query, nextRequest(&m.requests.searchOverlay))
		}
	default:
		if m.editText("search-overlay", &m.searchOverlay.query, msg) {
			m.searchOverlay.err = ""
		}
	}
	return nil
}

func (m *Model) searchOverlayResultsMaybeAdjustScroll(visible int) {
	clampScroll(&m.searchOverlay.cursor, &m.searchOverlay.scroll, len(m.searchOverlay.results), visible)
	// Section separators take rows of their own, so a window sized purely by
	// result count can push the cursor off the bottom.
	for m.searchOverlay.scroll < m.searchOverlay.cursor &&
		searchOverlayRowsToCursor(m.searchOverlay.results, m.searchOverlay.scroll, m.searchOverlay.cursor) > visible {
		m.searchOverlay.scroll++
	}
}

// handleSearchOverlayResultsKey handles navigation through search results.
func (m *Model) handleSearchOverlayResultsKey(msg tea.KeyPressMsg) tea.Cmd {
	count := len(m.searchOverlay.results)

	switch msg.String() {
	case "ctrl+x":
		m.toggleExpandedView()
		m.searchOverlayResultsMaybeAdjustScroll(m.searchOverlayResultsVisible())
	case "up", "k", "ctrl+p":
		if m.searchOverlay.cursor > 0 {
			m.searchOverlay.cursor--
		} else if count > 0 {
			m.searchOverlay.cursor = count - 1
		}
		m.searchOverlayResultsMaybeAdjustScroll(m.searchOverlayResultsVisible())
	case "down", "j", "ctrl+n":
		if m.searchOverlay.cursor < count-1 {
			m.searchOverlay.cursor++
		} else if count > 0 {
			m.searchOverlay.cursor = 0
		}
		m.searchOverlayResultsMaybeAdjustScroll(m.searchOverlayResultsVisible())
	case "enter":
		if count > 0 && !m.searchOverlayBusy() {
			track := m.searchOverlay.results[m.searchOverlay.cursor]
			if track.IsAlbum() {
				return m.expandSearchOverlayAlbum(track, searchOverlayAlbumPlay)
			}
			m.closeSearchOverlay()
			return m.playTrackImmediate(track)
		}
	case "a":
		if count > 0 && !m.searchOverlayBusy() {
			track := m.searchOverlay.results[m.searchOverlay.cursor]
			if track.IsAlbum() {
				return m.expandSearchOverlayAlbum(track, searchOverlayAlbumAppend)
			}
			m.closeSearchOverlay()
			return m.appendTrack(track)
		}
	case "q":
		if count > 0 && !m.searchOverlayBusy() {
			track := m.searchOverlay.results[m.searchOverlay.cursor]
			if track.IsAlbum() {
				return m.expandSearchOverlayAlbum(track, searchOverlayAlbumQueueNext)
			}
			m.closeSearchOverlay()
			return m.queueTrackNext(track)
		}
	case "p":
		if count > 0 && !m.searchOverlayBusy() {
			track := m.searchOverlay.results[m.searchOverlay.cursor]
			// The playlist picker adds one track; an album is many, and Spotify
			// has no single call to add a whole record.
			if track.IsAlbum() {
				m.setSearchOverlayError("You cannot add a whole album to a playlist. Select a track, then press p.")
				return nil
			}
			m.searchOverlay.selTrack = track
			m.searchOverlay.loading = true
			m.searchOverlay.err = ""
			return fetchSearchOverlayPlaylistsCmd(m.searchOverlay.prov, nextRequest(&m.requests.searchOverlayLists))
		}
	case "f":
		if !m.searchOverlayBusy() && m.searchOverlay.cursor >= 0 && m.searchOverlay.cursor < count {
			track := m.searchOverlay.results[m.searchOverlay.cursor]
			if !track.IsAlbum() {
				return m.favoriteTrackKey(track)
			}
			if m.toggleFavorite(m.searchOverlay.prov, track.AlbumID()) && m.isActiveProvider(m.searchOverlay.prov.Name()) {
				return m.fetchProviderPlaylists()
			}
		}
	case "esc", "backspace":
		m.invalidateSearchOverlayAlbumRequest()
		nextRequest(&m.requests.searchOverlayLists)
		m.searchOverlay.loading = false
		m.searchOverlay.screen = searchOverlayInput
		m.searchOverlay.err = ""
	case "ctrl+u":
		step := m.searchOverlayResultsVisible()
		if step < 1 {
			step = 1
		}
		if m.searchOverlay.cursor >= step {
			m.searchOverlay.cursor -= step
		} else {
			m.searchOverlay.cursor = 0
		}
		m.searchOverlayResultsMaybeAdjustScroll(m.searchOverlayResultsVisible())
	case "ctrl+d":
		step := m.searchOverlayResultsVisible()
		if step < 1 {
			step = 1
		}
		m.searchOverlay.cursor += step
		if m.searchOverlay.cursor >= count {
			m.searchOverlay.cursor = max(0, count-1)
		}
		m.searchOverlayResultsMaybeAdjustScroll(m.searchOverlayResultsVisible())
	}
	return nil
}

// searchOverlayBusy reports whether a request for the results screen is in flight,
// covering both the playlist fetch and an album expansion.
func (m *Model) searchOverlayBusy() bool {
	return m.searchOverlay.loading || m.searchOverlay.albumLoading
}

func (m *Model) setSearchOverlayError(message string) {
	m.searchOverlay.err = message
	switch m.searchOverlay.screen {
	case searchOverlayResults:
		m.searchOverlayResultsMaybeAdjustScroll(m.searchOverlayResultsVisible())
	case searchOverlayPlaylist:
		m.searchOverlayPlaylistMaybeAdjustScroll(m.effectivePlaylistVisible())
	}
}

// expandSearchOverlayAlbum fetches the tracks of the selected album placeholder. The
// overlay stays open while it runs: closing it would bump the request
// generation and drop the response.
func (m *Model) expandSearchOverlayAlbum(album playlist.Track, action searchOverlayAlbumAction) tea.Cmd {
	loader, ok := m.searchOverlay.prov.(provider.AlbumTrackLoader)
	if !ok {
		m.setSearchOverlayError("This provider cannot open albums.")
		return nil
	}
	m.searchOverlay.albumLoading = true
	m.searchOverlay.err = ""
	ctx := m.newSearchOverlayRequestContext(30 * time.Second)
	return fetchSearchOverlayAlbumTracksCmd(ctx, loader, album, action, nextRequest(&m.requests.searchOverlayAlbum))
}

func (m *Model) searchOverlayPlaylistMaybeAdjustScroll(visible int) {
	count := len(m.searchOverlay.playlists) + 1
	rows := visible - 1 // the selected track line
	if m.searchOverlay.err != "" {
		rows-- // the error line
	}
	clampScroll(&m.searchOverlay.cursor, &m.searchOverlay.scroll, count, max(1, rows))
}

// handleSearchOverlayPlaylistKey handles picking a playlist to add to.
func (m *Model) handleSearchOverlayPlaylistKey(msg tea.KeyPressMsg) tea.Cmd {
	count := len(m.searchOverlay.playlists) + 1 // +1 for "+ New Playlist..."

	switch msg.String() {
	case "ctrl+x":
		m.toggleExpandedView()
		m.searchOverlayPlaylistMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "up", "k":
		if m.searchOverlay.cursor > 0 {
			m.searchOverlay.cursor--
		} else if count > 0 {
			m.searchOverlay.cursor = count - 1
		}
		m.searchOverlayPlaylistMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "down", "j":
		if m.searchOverlay.cursor < count-1 {
			m.searchOverlay.cursor++
		} else if count > 0 {
			m.searchOverlay.cursor = 0
		}
		m.searchOverlayPlaylistMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "enter":
		if m.searchOverlay.loading {
			return nil
		}
		w, ok := m.searchOverlay.prov.(provider.PlaylistWriter)
		if !ok {
			return nil
		}
		if m.searchOverlay.cursor < len(m.searchOverlay.playlists) {
			// Add to existing playlist.
			pl := m.searchOverlay.playlists[m.searchOverlay.cursor]
			m.searchOverlay.loading = true
			m.searchOverlay.err = ""
			return addToSearchOverlayPlaylistCmd(m.newSearchOverlayRequestContext(15*time.Second), w, pl.ID, m.searchOverlay.selTrack, m.searchOverlay.prov.Name(), pl.Name, nextRequest(&m.requests.searchOverlayMutation))
		}
		// "+ New Playlist..." selected.
		m.searchOverlay.screen = searchOverlayNewName
		m.searchOverlay.newName = ""
		m.searchOverlay.cursor = 0
		m.searchOverlay.scroll = 0
	case "esc", "backspace":
		m.searchOverlay.screen = searchOverlayResults
		m.searchOverlay.cursor = 0
		m.searchOverlay.scroll = 0
		m.searchOverlay.err = ""
	}
	return nil
}

// handleSearchOverlayNewNameKey handles text input for new playlist name.
func (m *Model) handleSearchOverlayNewNameKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.searchOverlay.screen = searchOverlayPlaylist
		m.searchOverlay.cursor = len(m.searchOverlay.playlists)
		m.searchOverlayPlaylistMaybeAdjustScroll(m.effectivePlaylistVisible())
	case tea.KeyEnter:
		if strings.TrimSpace(m.searchOverlay.newName) == "" {
			m.setSearchOverlayError("Playlist name is required.")
			return nil
		}
		if !m.searchOverlay.loading {
			c, cOk := m.searchOverlay.prov.(provider.PlaylistCreator)
			w, wOk := m.searchOverlay.prov.(provider.PlaylistWriter)
			if !cOk || !wOk {
				return nil
			}
			m.searchOverlay.loading = true
			m.searchOverlay.err = ""
			return createSearchOverlayPlaylistCmd(m.newSearchOverlayRequestContext(15*time.Second), c, w, m.searchOverlay.prov.Name(), m.searchOverlay.newName, m.searchOverlay.selTrack, nextRequest(&m.requests.searchOverlayMutation))
		}
	default:
		if m.editText("search-overlay-playlist-name", &m.searchOverlay.newName, msg) {
			m.searchOverlay.err = ""
		}
	}
	return nil
}
