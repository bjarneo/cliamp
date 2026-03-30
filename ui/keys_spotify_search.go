package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"cliamp/provider"
)

// handleSpotSearchKey dispatches key presses to the active provider search screen.
func (m *Model) handleSpotSearchKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.spotSearch.visible = false
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
func (m *Model) handleSpotSearchInputKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEscape:
		m.spotSearch.visible = false
	case tea.KeyEnter:
		if m.spotSearch.query != "" && !m.spotSearch.loading {
			s, ok := m.spotSearch.prov.(provider.Searcher)
			if !ok {
				return nil
			}
			m.spotSearch.loading = true
			m.spotSearch.err = ""
			return fetchSpotSearchCmd(s, m.spotSearch.query)
		}
	case tea.KeyBackspace:
		if m.spotSearch.query != "" {
			m.spotSearch.query = removeLastRune(m.spotSearch.query)
		}
	case tea.KeySpace:
		m.spotSearch.query += " "
	default:
		if msg.Type == tea.KeyRunes {
			m.spotSearch.query += string(msg.Runes)
		}
	}
	return nil
}

// handleSpotSearchResultsKey handles navigation through search results.
func (m *Model) handleSpotSearchResultsKey(msg tea.KeyMsg) tea.Cmd {
	count := len(m.spotSearch.results)

	switch msg.String() {
	case "up", "k":
		if m.spotSearch.cursor > 0 {
			m.spotSearch.cursor--
		} else if count > 0 {
			m.spotSearch.cursor = count - 1
		}
	case "down", "j":
		if m.spotSearch.cursor < count-1 {
			m.spotSearch.cursor++
		} else if count > 0 {
			m.spotSearch.cursor = 0
		}
	case "enter":
		if count > 0 && !m.spotSearch.loading {
			m.spotSearch.selTrack = m.spotSearch.results[m.spotSearch.cursor]
			m.spotSearch.loading = true
			m.spotSearch.err = ""
			return fetchSpotPlaylistsCmd(m.spotSearch.prov)
		}
	case "esc", "backspace":
		m.spotSearch.screen = spotSearchInput
		m.spotSearch.err = ""
	}
	return nil
}

// handleSpotSearchPlaylistKey handles picking a playlist to add to.
func (m *Model) handleSpotSearchPlaylistKey(msg tea.KeyMsg) tea.Cmd {
	count := len(m.spotSearch.playlists) + 1 // +1 for "+ New Playlist..."

	switch msg.String() {
	case "up", "k":
		if m.spotSearch.cursor > 0 {
			m.spotSearch.cursor--
		} else if count > 0 {
			m.spotSearch.cursor = count - 1
		}
	case "down", "j":
		if m.spotSearch.cursor < count-1 {
			m.spotSearch.cursor++
		} else if count > 0 {
			m.spotSearch.cursor = 0
		}
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
			return addToSpotPlaylistCmd(w, pl.ID, m.spotSearch.selTrack, pl.Name)
		}
		// "+ New Playlist..." selected.
		m.spotSearch.screen = spotSearchNewName
		m.spotSearch.newName = ""
		m.spotSearch.cursor = 0
	case "esc", "backspace":
		m.spotSearch.screen = spotSearchResults
		m.spotSearch.cursor = 0
		m.spotSearch.err = ""
	}
	return nil
}

// handleSpotSearchNewNameKey handles text input for new playlist name.
func (m *Model) handleSpotSearchNewNameKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEscape:
		m.spotSearch.screen = spotSearchPlaylist
		m.spotSearch.cursor = len(m.spotSearch.playlists) // back on "+ New Playlist..."
	case tea.KeyEnter:
		if m.spotSearch.newName != "" && !m.spotSearch.loading {
			c, cOk := m.spotSearch.prov.(provider.PlaylistCreator)
			w, wOk := m.spotSearch.prov.(provider.PlaylistWriter)
			if !cOk || !wOk {
				return nil
			}
			m.spotSearch.loading = true
			m.spotSearch.err = ""
			return createSpotPlaylistCmd(c, w, m.spotSearch.newName, m.spotSearch.selTrack)
		}
	case tea.KeyBackspace:
		if m.spotSearch.newName != "" {
			m.spotSearch.newName = removeLastRune(m.spotSearch.newName)
		}
	case tea.KeySpace:
		m.spotSearch.newName += " "
	default:
		if msg.Type == tea.KeyRunes {
			m.spotSearch.newName += string(msg.Runes)
		}
	}
	return nil
}
