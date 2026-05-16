package model

import (
	"strings"

	"cliamp/theme"
)

func (m *Model) measureChrome(before, after []string) int {
	probe := append([]string{}, before...)
	probe = append(probe, "x")
	probe = append(probe, after...)
	probe = m.appendFooterMessages(probe)
	return m.measureOverlayVisible(probe, maxPlVisible)
}

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
	return m.measureChrome(m.themePickerChrome())
}

func (m *Model) themePickerMaybeAdjustScroll(visible int) {
	clampScroll(&m.themePicker.cursor, &m.themePicker.scroll, len(m.themes)+1, visible)
}

func (m *Model) devicePickerHelpLine() string {
	return helpKey("↓↑", "Scroll ") + helpKey("Enter", "Select ") + helpKey("Esc", "Cancel")
}

func (m *Model) devicePickerVisible() int {
	return m.measureChrome(m.devicePickerChrome())
}

func (m *Model) queueHelpLine() string {
	return helpKey("↓↑", "Scroll ") +
		helpKey("Shift+↓↑", "Reorder ") +
		helpKey("d", "Remove ") +
		helpKey("c", "Clear ") +
		helpKey("Esc", "Close")
}

func (m *Model) queueVisible() int {
	return m.measureChrome(m.queueChrome())
}

func (m *Model) searchHelpLine() string {
	return helpKey("↓↑", "Scroll ") +
		helpKey("Enter", "Play ") +
		helpKey("Tab", "Queue ") +
		helpKey("Esc", "Close")
}

func (m *Model) searchVisible() int {
	return m.measureChrome(m.searchChrome())
}

func (m *Model) netSearchResultsHelpLine() string {
	return helpKey("↓↑", "Scroll ") +
		helpKey("Enter", "Play ") +
		helpKey("a", "Append ") +
		helpKey("q", "Queue next ") +
		helpKey("Esc", "Back")
}

func (m *Model) netSearchResultsVisible() int {
	return m.measureChrome(m.netSearchResultsChrome())
}

func (m *Model) spotSearchResultsHelpLine() string {
	return helpKey("↓↑", "Scroll ") +
		helpKey("Enter", "Play ") +
		helpKey("a", "Append ") +
		helpKey("q", "Queue next ") +
		helpKey("p", "Add to playlist ") +
		helpKey("Esc", "Back")
}

func (m *Model) spotSearchResultsVisible() int {
	return m.measureChrome(m.spotSearchResultsChrome())
}

func (m *Model) spotSearchPlaylistHelpLine() string {
	return helpKey("↓↑", "Scroll ") + helpKey("Enter", "Select ") + helpKey("Esc", "Close")
}

func (m *Model) spotSearchPlaylistVisible() int {
	return m.measureChrome(m.spotSearchPlaylistChrome())
}

func (m *Model) plMgrListHelpLine() string {
	addLabel := "Add (nothing playing)"
	if track, idx := m.playlist.Current(); idx >= 0 && track.Path != "" {
		addLabel = "Add: " + truncate(track.DisplayName(), 32)
	}
	return helpKey("↓↑→", "Navigate ") +
		helpKey("Enter", "Open ") +
		helpKey("a", addLabel+" ") +
		helpKey("d", "Delete ") +
		helpKey("/", "Filter ") +
		helpKey("Esc", "Close")
}

func (m *Model) plMgrListVisible() int {
	return m.measureChrome(m.plMgrListChrome())
}

func (m *Model) plMgrListMaybeAdjustScroll(visible int) {
	clampScroll(&m.plManager.cursor, &m.plManager.scroll, m.plMgrListViewCount(), visible)
}

func (m *Model) plMgrTracksHelpLine() string {
	addLabel := "Add (nothing playing)"
	if track, idx := m.playlist.Current(); idx >= 0 && track.Path != "" {
		addLabel = "Add: " + truncate(track.DisplayName(), 32)
	}
	return helpKey("←↓↑", "Navigate ") +
		helpKey("Enter", "Play this ") +
		helpKey("P", "Play all ") +
		helpKey("a", addLabel+" ") +
		helpKey("d", "Remove ") +
		helpKey("/", "Filter ") +
		helpKey("Esc", "Back")
}

func (m *Model) plMgrTracksVisible() int {
	return m.measureChrome(m.plMgrTracksChrome())
}

func (m *Model) plMgrTracksMaybeAdjustScroll(visible int) {
	if m.plManager.filter != "" || !m.showAlbumHeaders {
		clampScroll(&m.plManager.cursor, &m.plManager.scroll, m.plMgrTracksViewCount(), visible)
		return
	}
	tracks := m.plManager.tracks
	if len(tracks) == 0 {
		return
	}
	if m.plManager.cursor < m.plManager.scroll {
		m.plManager.scroll = m.plManager.cursor
	}
	for m.plManager.scroll < m.plManager.cursor && m.albumSeparatorRows(tracks, m.plManager.scroll, m.plManager.cursor, true) > visible {
		m.plManager.scroll++
	}
}

// openPlaylistManager loads playlist metadata and opens the manager overlay.
func (m *Model) openPlaylistManager() {
	m.plMgrResetFilter()
	m.plMgrRefreshList()
	m.plManager.screen = plMgrScreenList
	m.plManager.cursor = 0
	m.plManager.scroll = 0
	m.plManager.confirmDel = false
	m.plManager.visible = true
	m.plMgrListMaybeAdjustScroll(m.plMgrListVisible())
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
	m.plManager.scroll = 0
	m.plManager.confirmDel = false
	m.plMgrResetFilter()
	m.plMgrTracksMaybeAdjustScroll(m.plMgrTracksVisible())
}

// plMgrResetFilter clears any active `/` filter on the playlist manager.
func (m *Model) plMgrResetFilter() {
	m.plManager.filtering = false
	m.plManager.filter = ""
	m.plManager.filtered = nil
	m.plManager.cursor = 0
	m.plManager.scroll = 0
	m.plManager.savedCursor = 0
	m.plManager.savedScroll = 0
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
	m.plManager.scroll = 0
	if m.plManager.screen == plMgrScreenList {
		m.plMgrListMaybeAdjustScroll(m.plMgrListVisible())
	} else if m.plManager.screen == plMgrScreenTracks {
		m.plMgrTracksMaybeAdjustScroll(m.plMgrTracksVisible())
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
	m.plMgrListMaybeAdjustScroll(m.plMgrListVisible())
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
