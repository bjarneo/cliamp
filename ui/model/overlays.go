package model

import (
	"strings"

	"charm.land/lipgloss/v2"

	"cliamp/theme"
	"cliamp/ui"
)

// openThemePicker re-loads themes from disk (picking up new user files)
// and opens the theme selector overlay.
func (m *Model) openThemePicker() {
	m.themes = theme.LoadAll()
	m.themePicker.visible = true
	m.themePicker.savedIdx = m.themeIdx
	// Position cursor on the currently active theme.
	// Picker list: 0 = Default, 1..N = themes[0..N-1]
	m.themePicker.cursor = m.themeIdx + 1
	m.themePicker.scroll = 0
	m.themePickerMaybeAdjustScroll(m.themePickerVisible())
}

// themePickerApply applies the theme under the cursor for live preview.
func (m *Model) themePickerApply() {
	if m.themePicker.cursor == 0 {
		m.themeIdx = -1
		applyThemeAll(theme.Default())
	} else {
		m.themeIdx = m.themePicker.cursor - 1
		applyThemeAll(m.themes[m.themeIdx])
	}
}

// themePickerSelect confirms the current selection and closes the picker.
func (m *Model) themePickerSelect() {
	m.themePickerApply()
	m.themePicker.visible = false
}

// themePickerCancel restores the theme from before the picker was opened.
func (m *Model) themePickerCancel() {
	m.themeIdx = m.themePicker.savedIdx
	if m.themeIdx < 0 {
		applyThemeAll(theme.Default())
	} else {
		applyThemeAll(m.themes[m.themeIdx])
	}
	m.themePicker.visible = false
}

func (m *Model) themePickerHelpLine() string {
	return helpKey("↓↑", "Scroll ") + helpKey("Enter", "Select ") + helpKey("Esc", "Close")
}

func (m *Model) themePickerVisible() int {
	probeSections := []string{
		titleStyle.Render("T H E M E S"),
		"",
		"x", // 1-line list placeholder
		"",
		dimStyle.Render("  0/0 themes"),
		"",
		m.themePickerHelpLine(),
	}

	probeFrame := ui.FrameStyle.Render(strings.Join(probeSections, "\n"))
	fixedHeight := lipgloss.Height(probeFrame) - 1

	limit := maxPlVisible
	if m.heightExpanded {
		limit = m.height
	}
	return max(3, min(limit, m.height-fixedHeight))
}

func (m *Model) themePickerMaybeAdjustScroll(visible int) {
	if visible <= 0 {
		return
	}
	count := len(m.themes) + 1
	if m.themePicker.cursor < 0 {
		m.themePicker.cursor = 0
	}
	if m.themePicker.cursor >= count && count > 0 {
		m.themePicker.cursor = count - 1
	}

	if m.themePicker.cursor < m.themePicker.scroll {
		m.themePicker.scroll = m.themePicker.cursor
	} else if m.themePicker.cursor >= m.themePicker.scroll+visible {
		m.themePicker.scroll = m.themePicker.cursor - visible + 1
	}

	if m.themePicker.scroll+visible > count && count > 0 {
		m.themePicker.scroll = max(0, count-visible)
	}
}

// openPlaylistManager loads playlist metadata and opens the manager overlay.
func (m *Model) openPlaylistManager() {
	m.plMgrResetFilter()
	m.plMgrRefreshList()
	m.plManager.screen = plMgrScreenList
	m.plManager.confirmDel = false
	m.plManager.visible = true
}

// plMgrEnterTrackList loads the tracks for a playlist and switches to screen 1.
func (m *Model) plMgrEnterTrackList(name string) {
	tracks, err := m.localProvider.Tracks(name)
	if err != nil {
		m.status.Showf(statusTTLDefault, "Load failed: %s", err)
		return
	}
	m.plManager.selPlaylist = name
	m.plManager.tracks = tracks
	m.setInitialHeaderState(tracks)
	m.plManager.screen = plMgrScreenTracks
	m.plManager.cursor = 0
	m.plManager.confirmDel = false
	m.plMgrResetFilter()
}

// plMgrResetFilter clears any active `/` filter on the playlist manager.
func (m *Model) plMgrResetFilter() {
	m.plManager.filtering = false
	m.plManager.filter = ""
	m.plManager.filtered = nil
	m.plManager.savedCursor = 0
}

// plMgrRecomputeFilter rebuilds the filter index for the active screen.
func (m *Model) plMgrRecomputeFilter() {
	m.plManager.filtered = m.plManager.filtered[:0]
	if m.plManager.filter == "" {
		m.plManager.filtered = nil
		return
	}
	q := strings.ToLower(m.plManager.filter)
	switch m.plManager.screen {
	case plMgrScreenList:
		for i, p := range m.plManager.playlists {
			if strings.Contains(strings.ToLower(p.Name), q) {
				m.plManager.filtered = append(m.plManager.filtered, i)
			}
		}
	case plMgrScreenTracks:
		for i, t := range m.plManager.tracks {
			hay := strings.ToLower(t.DisplayName() + " " + t.Album + " " + t.Artist)
			if strings.Contains(hay, q) {
				m.plManager.filtered = append(m.plManager.filtered, i)
			}
		}
	}
	if m.plManager.cursor < 0 {
		m.plManager.cursor = 0
	}
}

// plMgrRealIndex maps a view-index to the real index in the underlying slice
// (playlists on the list screen, tracks on the track screen). Returns -1 if
// out of range or pointing at the "+ New Playlist" pseudo-entry on the list
// screen. unfilteredLen is the length of the unfiltered slice.
func (m Model) plMgrRealIndex(view, unfilteredLen int) int {
	if m.plManager.filter == "" {
		if view < 0 || view >= unfilteredLen {
			return -1
		}
		return view
	}
	if view < 0 || view >= len(m.plManager.filtered) {
		return -1
	}
	return m.plManager.filtered[view]
}

func (m Model) plMgrPlaylistRealIndex(view int) int {
	return m.plMgrRealIndex(view, len(m.plManager.playlists))
}

func (m Model) plMgrTrackRealIndex(view int) int {
	return m.plMgrRealIndex(view, len(m.plManager.tracks))
}

// plMgrRefreshList reloads playlist names and counts from disk and clamps the cursor.
func (m *Model) plMgrRefreshList() {
	if m.localProvider == nil {
		return
	}
	playlists, err := m.localProvider.Playlists()
	if err != nil {
		m.status.Showf(statusTTLDefault, "Load failed: %s", err)
	}
	m.plManager.playlists = playlists
	if m.plManager.filter != "" {
		m.plMgrRecomputeFilter()
	}
	total := m.plMgrListViewCount()
	if m.plManager.cursor >= total {
		m.plManager.cursor = total - 1
	}
	if m.plManager.cursor < 0 {
		m.plManager.cursor = 0
	}
}

// plMgrListViewCount returns the visible row count on the list screen
// (filtered playlists + "+ New Playlist..." entry).
func (m Model) plMgrListViewCount() int {
	if m.plManager.filter != "" {
		return len(m.plManager.filtered) + 1
	}
	return len(m.plManager.playlists) + 1
}

// plMgrTracksViewCount returns the visible row count on the tracks screen.
func (m Model) plMgrTracksViewCount() int {
	if m.plManager.filter != "" {
		return len(m.plManager.filtered)
	}
	return len(m.plManager.tracks)
}
