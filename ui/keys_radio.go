package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"cliamp/playlist"
)

// handleRadioCatalogKey processes key presses while the radio catalog is open.
func (m *Model) handleRadioCatalogKey(msg tea.KeyMsg) tea.Cmd {
	if m.radioCatalog.searching {
		return m.handleRadioSearchInput(msg)
	}

	switch msg.String() {
	case "ctrl+c":
		m.radioCatalog.visible = false
		return m.quit()
	case "up", "k":
		if m.radioCatalog.cursor > 0 {
			m.radioCatalog.cursor--
			m.radioMaybeAdjustScroll()
		}
	case "down", "j":
		if m.radioCatalog.cursor < len(m.radioCatalog.stations)-1 {
			m.radioCatalog.cursor++
			m.radioMaybeAdjustScroll()
		}
	case "enter":
		if len(m.radioCatalog.stations) == 0 || m.radioCatalog.loading {
			return nil
		}
		s := m.radioCatalog.stations[m.radioCatalog.cursor]
		track := playlist.Track{
			Path:     s.URL,
			Title:    s.Name,
			Stream:   true,
			Realtime: true,
		}
		m.player.Stop()
		m.player.ClearPreload()
		m.playlist.Add(track)
		newIdx := m.playlist.Len() - 1
		m.playlist.SetIndex(newIdx)
		m.plCursor = newIdx
		m.adjustScroll()
		m.radioCatalog.visible = false
		m.focus = focusPlaylist
		m.status.Showf(statusTTLMedium, "Playing: %s", s.Name)
		cmd := m.playCurrentTrack()
		m.notifyMPRIS()
		return cmd
	case "a":
		// Append station to playlist without closing the catalog.
		if len(m.radioCatalog.stations) == 0 || m.radioCatalog.loading {
			return nil
		}
		s := m.radioCatalog.stations[m.radioCatalog.cursor]
		track := playlist.Track{
			Path:     s.URL,
			Title:    s.Name,
			Stream:   true,
			Realtime: true,
		}
		wasEmpty := m.playlist.Len() == 0
		m.playlist.Add(track)
		m.status.Showf(statusTTLMedium, "Added: %s", s.Name)
		if wasEmpty || !m.player.IsPlaying() {
			m.playlist.SetIndex(0)
			cmd := m.playCurrentTrack()
			m.notifyMPRIS()
			return cmd
		}
	case "f":
		// Toggle favorite on the current station.
		if len(m.radioCatalog.stations) == 0 || m.radioCatalog.loading {
			return nil
		}
		s := m.radioCatalog.stations[m.radioCatalog.cursor]
		if m.radioCatalog.favorites == nil {
			return nil
		}
		if m.radioCatalog.favorites.Contains(s.URL) {
			_ = m.radioCatalog.favorites.Remove(s.URL)
			m.status.Showf(statusTTLMedium, "Removed: %s", s.Name)
			// If viewing favorites, refresh the visible list.
			if m.radioCatalog.showFavorites {
				m.radioCatalog.stations = m.radioCatalog.favorites.Stations()
				if m.radioCatalog.cursor >= len(m.radioCatalog.stations) {
					m.radioCatalog.cursor = max(0, len(m.radioCatalog.stations)-1)
				}
				m.radioMaybeAdjustScroll()
			}
		} else {
			_ = m.radioCatalog.favorites.Add(s)
			m.status.Showf(statusTTLMedium, "Favorited: %s", s.Name)
		}
	case "F":
		// Toggle between favorites view and catalog view.
		m.radioCatalog.showFavorites = !m.radioCatalog.showFavorites
		m.radioCatalog.cursor = 0
		m.radioCatalog.scroll = 0
		if m.radioCatalog.showFavorites && m.radioCatalog.favorites != nil {
			m.radioCatalog.stations = m.radioCatalog.favorites.Stations()
		} else {
			// Switching back to catalog: re-fetch.
			m.radioCatalog.loading = true
			m.radioCatalog.stations = nil
			if m.radioCatalog.query != "" {
				return fetchRadioSearchCmd(m.radioCatalog.query)
			}
			return fetchRadioTopCmd()
		}
	case "/":
		if m.radioCatalog.showFavorites {
			return nil // no search in favorites view
		}
		m.radioCatalog.searching = true
		m.radioCatalog.query = ""
	case "esc", "R":
		m.radioCatalog.visible = false
	}
	return nil
}

// handleRadioSearchInput processes key presses in the radio catalog search bar.
func (m *Model) handleRadioSearchInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEnter:
		m.radioCatalog.searching = false
		if m.radioCatalog.query == "" {
			m.radioCatalog.loading = true
			return fetchRadioTopCmd()
		}
		m.radioCatalog.loading = true
		m.radioCatalog.stations = nil
		m.radioCatalog.cursor = 0
		m.radioCatalog.scroll = 0
		return fetchRadioSearchCmd(m.radioCatalog.query)
	case tea.KeyEscape:
		m.radioCatalog.searching = false
	case tea.KeyBackspace, tea.KeyDelete:
		if m.radioCatalog.query != "" {
			m.radioCatalog.query = removeLastRune(m.radioCatalog.query)
		}
	case tea.KeySpace:
		m.radioCatalog.query += " "
	default:
		if msg.Type == tea.KeyRunes {
			m.radioCatalog.query += string(msg.Runes)
		}
	}
	return nil
}

// radioMaybeAdjustScroll keeps the cursor visible within the rendered list window.
func (m *Model) radioMaybeAdjustScroll() {
	visible := m.plVisible
	if visible < 5 {
		visible = 5
	}
	if m.radioCatalog.cursor < m.radioCatalog.scroll {
		m.radioCatalog.scroll = m.radioCatalog.cursor
	}
	if m.radioCatalog.cursor >= m.radioCatalog.scroll+visible {
		m.radioCatalog.scroll = m.radioCatalog.cursor - visible + 1
	}
}
