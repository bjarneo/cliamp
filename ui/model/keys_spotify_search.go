package model

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// handleSpotSearchKey dispatches key presses to the active provider search screen.
func (m *Model) handleSpotSearchKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.closeSpotSearch()
		return m.quit()
	}

	switch m.spotSearch.screen {
	case spotSearchInput:
		return m.handleSpotSearchInputKey(msg)
	case spotSearchResults:
		return m.handleSpotSearchResultsKey(msg)
	case spotSearchPlaylist:
		return m.handleSpotSearchPlaylistKey(msg)
	case spotSearchNewName:
		return m.handleSpotSearchNewNameKey(msg)
	}
	return nil
}

// handleSpotSearchInputKey handles text input for the search query.
func (m *Model) handleSpotSearchInputKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.closeSpotSearch()
	case tea.KeyEnter:
		if m.spotSearch.query == "" {
			m.spotSearch.err = "Enter a search query."
			return nil
		}
		if !m.spotSearch.loading {
			s, ok := m.spotSearch.prov.(provider.Searcher)
			if !ok {
				return nil
			}
			m.spotSearch.loading = true
			m.spotSearch.err = ""
			return fetchSpotSearchCmd(m.newSpotRequestContext(30*time.Second), s, m.spotSearch.prov.Name(), m.spotSearch.query, nextRequest(&m.requests.spotSearch))
		}
	default:
		if m.editText("spot-search", &m.spotSearch.query, msg) {
			m.spotSearch.err = ""
		}
	}
	return nil
}

func (m *Model) spotSearchResultsMaybeAdjustScroll(visible int) {
	clampScroll(&m.spotSearch.cursor, &m.spotSearch.scroll, len(m.spotSearch.results), visible)
	// Section separators take rows of their own, so a window sized purely by
	// result count can push the cursor off the bottom.
	for m.spotSearch.scroll < m.spotSearch.cursor &&
		spotSearchRowsToCursor(m.spotSearch.results, m.spotSearch.scroll, m.spotSearch.cursor) > visible {
		m.spotSearch.scroll++
	}
}

// handleSpotSearchResultsKey handles navigation through search results.
func (m *Model) handleSpotSearchResultsKey(msg tea.KeyPressMsg) tea.Cmd {
	count := len(m.spotSearch.results)

	switch msg.String() {
	case "ctrl+x":
		m.toggleExpandedView()
		m.spotSearchResultsMaybeAdjustScroll(m.spotSearchResultsVisible())
	case "up", "k", "ctrl+p":
		if m.spotSearch.cursor > 0 {
			m.spotSearch.cursor--
		} else if count > 0 {
			m.spotSearch.cursor = count - 1
		}
		m.spotSearchResultsMaybeAdjustScroll(m.spotSearchResultsVisible())
	case "down", "j", "ctrl+n":
		if m.spotSearch.cursor < count-1 {
			m.spotSearch.cursor++
		} else if count > 0 {
			m.spotSearch.cursor = 0
		}
		m.spotSearchResultsMaybeAdjustScroll(m.spotSearchResultsVisible())
	case "enter":
		if count > 0 && !m.spotSearchBusy() {
			track := m.spotSearch.results[m.spotSearch.cursor]
			if track.IsAlbum() {
				return m.expandSpotAlbum(track, spotAlbumPlay)
			}
			m.closeSpotSearch()
			return m.playTrackImmediate(track)
		}
	case "a":
		if count > 0 && !m.spotSearchBusy() {
			track := m.spotSearch.results[m.spotSearch.cursor]
			if track.IsAlbum() {
				return m.expandSpotAlbum(track, spotAlbumAppend)
			}
			m.closeSpotSearch()
			return m.appendTrack(track)
		}
	case "q":
		if count > 0 && !m.spotSearchBusy() {
			track := m.spotSearch.results[m.spotSearch.cursor]
			if track.IsAlbum() {
				return m.expandSpotAlbum(track, spotAlbumQueueNext)
			}
			m.closeSpotSearch()
			return m.queueTrackNext(track)
		}
	case "p":
		if count > 0 && !m.spotSearchBusy() {
			track := m.spotSearch.results[m.spotSearch.cursor]
			// The playlist picker adds one track; an album is many, and Spotify
			// has no single call to add a whole record.
			if track.IsAlbum() {
				m.spotSearch.err = "Open the album with Enter, then add tracks from the queue."
				return nil
			}
			m.spotSearch.selTrack = track
			m.spotSearch.loading = true
			m.spotSearch.err = ""
			return fetchSpotPlaylistsCmd(m.spotSearch.prov, nextRequest(&m.requests.spotLists))
		}
	case "esc", "backspace":
		m.spotSearch.screen = spotSearchInput
		m.spotSearch.err = ""
	case "ctrl+u":
		step := m.spotSearchResultsVisible()
		if step < 1 {
			step = 1
		}
		if m.spotSearch.cursor >= step {
			m.spotSearch.cursor -= step
		} else {
			m.spotSearch.cursor = 0
		}
		m.spotSearchResultsMaybeAdjustScroll(m.spotSearchResultsVisible())
	case "ctrl+d":
		step := m.spotSearchResultsVisible()
		if step < 1 {
			step = 1
		}
		m.spotSearch.cursor += step
		if m.spotSearch.cursor >= count {
			m.spotSearch.cursor = max(0, count-1)
		}
		m.spotSearchResultsMaybeAdjustScroll(m.spotSearchResultsVisible())
	}
	return nil
}

// spotSearchBusy reports whether a request for the results screen is in flight,
// covering both the playlist fetch and an album expansion.
func (m *Model) spotSearchBusy() bool {
	return m.spotSearch.loading || m.spotSearch.albumLoading
}

// expandSpotAlbum fetches the tracks of the selected album placeholder. The
// overlay stays open while it runs: closing it would bump the request
// generation and drop the response.
func (m *Model) expandSpotAlbum(album playlist.Track, action spotAlbumAction) tea.Cmd {
	loader, ok := m.spotSearch.prov.(provider.AlbumTrackLoader)
	if !ok {
		m.spotSearch.err = "This provider cannot open albums."
		return nil
	}
	m.spotSearch.albumLoading = true
	m.spotSearch.err = ""
	return fetchSpotAlbumTracksCmd(loader, album, action, nextRequest(&m.requests.spotAlbum))
}

func (m *Model) spotSearchPlaylistMaybeAdjustScroll(visible int) {
	count := len(m.spotSearch.playlists) + 1
	clampScroll(&m.spotSearch.cursor, &m.spotSearch.scroll, count, max(1, visible-1))
}

// handleSpotSearchPlaylistKey handles picking a playlist to add to.
func (m *Model) handleSpotSearchPlaylistKey(msg tea.KeyPressMsg) tea.Cmd {
	count := len(m.spotSearch.playlists) + 1 // +1 for "+ New Playlist..."

	switch msg.String() {
	case "ctrl+x":
		m.toggleExpandedView()
		m.spotSearchPlaylistMaybeAdjustScroll(m.spotSearchPlaylistVisible())
	case "up", "k":
		if m.spotSearch.cursor > 0 {
			m.spotSearch.cursor--
		} else if count > 0 {
			m.spotSearch.cursor = count - 1
		}
		m.spotSearchPlaylistMaybeAdjustScroll(m.spotSearchPlaylistVisible())
	case "down", "j":
		if m.spotSearch.cursor < count-1 {
			m.spotSearch.cursor++
		} else if count > 0 {
			m.spotSearch.cursor = 0
		}
		m.spotSearchPlaylistMaybeAdjustScroll(m.spotSearchPlaylistVisible())
	case "enter":
		if m.spotSearch.loading {
			return nil
		}
		w, ok := m.spotSearch.prov.(provider.PlaylistWriter)
		if !ok {
			return nil
		}
		if m.spotSearch.cursor < len(m.spotSearch.playlists) {
			// Add to existing playlist.
			pl := m.spotSearch.playlists[m.spotSearch.cursor]
			// Skip "Your Music" — uses a different endpoint.
			if pl.ID == "YOUR MUSIC" {
				return nil
			}
			m.spotSearch.loading = true
			m.spotSearch.err = ""
			return addToSpotPlaylistCmd(m.newSpotRequestContext(15*time.Second), w, pl.ID, m.spotSearch.selTrack, m.spotSearch.prov.Name(), pl.Name, nextRequest(&m.requests.spotMutation))
		}
		// "+ New Playlist..." selected.
		m.spotSearch.screen = spotSearchNewName
		m.spotSearch.newName = ""
		m.spotSearch.cursor = 0
		m.spotSearch.scroll = 0
	case "esc", "backspace":
		m.spotSearch.screen = spotSearchResults
		m.spotSearch.cursor = 0
		m.spotSearch.scroll = 0
		m.spotSearch.err = ""
	}
	return nil
}

// handleSpotSearchNewNameKey handles text input for new playlist name.
func (m *Model) handleSpotSearchNewNameKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.spotSearch.screen = spotSearchPlaylist
		m.spotSearch.cursor = len(m.spotSearch.playlists)
		m.spotSearchPlaylistMaybeAdjustScroll(m.spotSearchPlaylistVisible())
	case tea.KeyEnter:
		if strings.TrimSpace(m.spotSearch.newName) == "" {
			m.spotSearch.err = "Playlist name is required."
			return nil
		}
		if !m.spotSearch.loading {
			c, cOk := m.spotSearch.prov.(provider.PlaylistCreator)
			w, wOk := m.spotSearch.prov.(provider.PlaylistWriter)
			if !cOk || !wOk {
				return nil
			}
			m.spotSearch.loading = true
			m.spotSearch.err = ""
			return createSpotPlaylistCmd(m.newSpotRequestContext(15*time.Second), c, w, m.spotSearch.prov.Name(), m.spotSearch.newName, m.spotSearch.selTrack, nextRequest(&m.requests.spotMutation))
		}
	default:
		if m.editText("spot-playlist-name", &m.spotSearch.newName, msg) {
			m.spotSearch.err = ""
		}
	}
	return nil
}
