package model

import (
	"fmt"
	"strings"

	"github.com/bjarneo/cliamp/theme"
	"github.com/bjarneo/cliamp/ui"
)

// openThemePicker re-loads themes from disk (picking up new user files)
// and opens the theme selector overlay.
func (m *Model) openThemePicker() {
	savedName := m.ThemeName()
	m.themes = theme.LoadAll()
	m.themePicker.visible = true
	m.themePicker.savedName = savedName
	m.themePicker.filtering = false
	m.themePicker.filter = ""
	m.themePicker.filtered = nil
	m.themePicker.savedCursor = 0
	m.themePicker.savedScroll = 0
	m.themeIdx = -1
	for i, t := range m.themes {
		if strings.EqualFold(t.Name, savedName) {
			m.themeIdx = i
			break
		}
	}
	// Position cursor on the currently active theme.
	// Picker list: 0 = Default, 1..N = themes[0..N-1]
	m.themePicker.cursor = m.themeIdx + 1
	m.themePicker.scroll = 0
	m.themePickerMaybeAdjustScroll(m.themePickerVisible())
}

// themePickerApply applies the theme under the cursor for live preview.
func (m *Model) themePickerApply() bool {
	rawIdx, ok := m.themePickerRawIndex(m.themePicker.cursor)
	if !ok {
		return false
	}
	if rawIdx == 0 {
		m.themeIdx = -1
		applyThemeAll(theme.Default())
	} else {
		m.themeIdx = rawIdx - 1
		applyThemeAll(m.themes[m.themeIdx])
	}
	return true
}

// themePickerSelect confirms the current selection and closes the picker.
func (m *Model) themePickerSelect() {
	if !m.themePickerApply() {
		return
	}
	m.themePicker.visible = false
	m.themePicker.filtering = false
	m.themePicker.filter = ""
	m.themePicker.filtered = nil
}

// themePickerCancel restores the theme from before the picker was opened.
func (m *Model) themePickerCancel() {
	if !m.SetTheme(m.themePicker.savedName) {
		m.themeIdx = -1
		applyThemeAll(theme.Default())
	}
	m.themePicker.visible = false
	m.themePicker.filtering = false
	m.themePicker.filter = ""
	m.themePicker.filtered = nil
}

func (m *Model) themePickerHelpLine() string {
	if m.themePicker.filtering {
		return m.commandHelp(commandModeThemePickerFilter)
	}
	return m.commandHelp(commandModeThemePicker)
}

func (m *Model) themePickerVisible() int {
	return m.effectivePlaylistVisible()
}

func (m *Model) themePickerMaybeAdjustScroll(visible int) {
	clampScroll(&m.themePicker.cursor, &m.themePicker.scroll, m.themePickerViewCount(), visible)
}

func (m Model) themePickerViewCount() int {
	if m.themePicker.filter != "" {
		return len(m.themePicker.filtered)
	}
	return m.themeCount()
}

func (m Model) themePickerRawIndex(viewIdx int) (int, bool) {
	if m.themePicker.filter != "" {
		if viewIdx < 0 || viewIdx >= len(m.themePicker.filtered) {
			return 0, false
		}
		return m.themePicker.filtered[viewIdx], true
	}
	if viewIdx < 0 || viewIdx >= m.themeCount() {
		return 0, false
	}
	return viewIdx, true
}

func (m Model) themePickerName(rawIdx int) string {
	if rawIdx == 0 {
		return theme.DefaultName
	}
	if rawIdx > 0 && rawIdx <= len(m.themes) {
		return m.themes[rawIdx-1].Name
	}
	return ""
}

func (m *Model) themePickerRecomputeFilter() {
	m.themePicker.filtered = nil
	m.themePicker.cursor = 0
	m.themePicker.scroll = 0
	if m.themePicker.filter == "" {
		return
	}
	query := strings.ToLower(m.themePicker.filter)
	for rawIdx := range m.themeCount() {
		if strings.Contains(strings.ToLower(m.themePickerName(rawIdx)), query) {
			m.themePicker.filtered = append(m.themePicker.filtered, rawIdx)
		}
	}
}

// openVisPicker opens the visualizer picker, which renders the mode list in the
// playlist region while keeping the visualizer live above it for preview. The
// cursor starts on the currently active mode.
func (m *Model) openVisPicker() {
	m.visPicker.visible = true
	m.visPicker.savedMode = int(m.vis.Mode)
	m.visPicker.cursor = int(m.vis.Mode)
	m.visPicker.scroll = 0
	m.visPicker.filtering = false
	m.visPicker.filter = ""
	m.visPicker.filtered = nil
	m.visPicker.savedCursor = 0
	m.visPicker.savedScroll = 0
	// Capture the mode list once; it is stable while the picker is open (Lua
	// visualizers are registered at startup), so callers avoid re-allocating it.
	m.visPicker.modes = m.vis.AllModeNames()
	// Recompute chrome/height for the picker layout (its header + help differ
	// from the playlist), then fit the cursor into the visible window.
	m.refreshChrome()
	m.applyHeightMode()
	m.visPickerMaybeAdjustScroll(m.visPickerVisible())
}

// visPickerApply switches to the visualizer mode under the cursor. Run on every
// cursor move so the live preview updates as the user scrolls. Only recompute
// the layout when crossing the VisNone boundary, since that is the sole mode
// change that adds/removes the spectrum block (all other modes share a height).
func (m *Model) visPickerApply() bool {
	rawIdx, ok := m.visPickerRawIndex(m.visPicker.cursor)
	if !ok {
		return false
	}
	wasNone := m.vis.Mode == ui.VisNone
	m.vis.SetMode(ui.VisMode(rawIdx))
	if wasNone != (m.vis.Mode == ui.VisNone) {
		m.refreshChrome()
		m.applyHeightMode()
	}
	return true
}

// visPickerClose restores playlist sizing after the picker layout is dismissed.
func (m *Model) visPickerClose() {
	m.visPicker.visible = false
	m.visPicker.modes = nil
	m.visPicker.filtering = false
	m.visPicker.filter = ""
	m.visPicker.filtered = nil
	m.refreshChrome()
	m.applyHeightMode()
	m.adjustScroll()
}

// visPickerSelect confirms the current selection, persists it, and closes.
func (m *Model) visPickerSelect() {
	if !m.visPickerApply() {
		return
	}
	if err := m.configSaver.Save("visualizer", fmt.Sprintf("%q", m.vis.ModeName())); err != nil {
		m.status.Errorf(statusTTLDefault, "Config save failed: %s", err)
	}
	m.visPickerClose()
}

// visPickerCancel restores the mode from before the picker was opened.
func (m *Model) visPickerCancel() {
	m.vis.SetMode(ui.VisMode(m.visPicker.savedMode))
	m.visPickerClose()
}

func (m *Model) visPickerHelpLine() string {
	if m.visPicker.filtering {
		return m.commandHelp(commandModeVisPickerFilter)
	}
	return m.commandHelp(commandModeVisPicker)
}

func (m *Model) visPickerVisible() int {
	return m.effectivePlaylistVisible()
}

func (m *Model) visPickerMaybeAdjustScroll(visible int) {
	clampScroll(&m.visPicker.cursor, &m.visPicker.scroll, m.visPickerViewCount(), visible)
}

func (m Model) visPickerViewCount() int {
	if m.visPicker.filter != "" {
		return len(m.visPicker.filtered)
	}
	return len(m.visPicker.modes)
}

func (m Model) visPickerRawIndex(viewIdx int) (int, bool) {
	if m.visPicker.filter != "" {
		if viewIdx < 0 || viewIdx >= len(m.visPicker.filtered) {
			return 0, false
		}
		return m.visPicker.filtered[viewIdx], true
	}
	if viewIdx < 0 || viewIdx >= len(m.visPicker.modes) {
		return 0, false
	}
	return viewIdx, true
}

func (m *Model) visPickerRecomputeFilter() {
	m.visPicker.filtered = nil
	m.visPicker.cursor = 0
	m.visPicker.scroll = 0
	if m.visPicker.filter == "" {
		return
	}
	query := strings.ToLower(m.visPicker.filter)
	for rawIdx, name := range m.visPicker.modes {
		if strings.Contains(strings.ToLower(name), query) {
			m.visPicker.filtered = append(m.visPicker.filtered, rawIdx)
		}
	}
}

func (m *Model) devicePickerHelpLine() string {
	return m.commandHelp(commandModeDevicePicker)
}

func (m *Model) devicePickerVisible() int {
	return m.effectivePlaylistVisible()
}

func (m *Model) queueHelpLine() string {
	return m.commandHelp(commandModeQueue)
}

func (m *Model) queueVisible() int {
	return m.effectivePlaylistVisible()
}

func (m *Model) searchHelpLine() string {
	return m.commandHelp(commandModeSearch)
}

func (m *Model) searchVisible() int {
	return m.effectivePlaylistVisible()
}

// closeSearchLayout restores playlist sizing after the inline search header and
// help line are dismissed, then refits the playlist cursor into view.
func (m *Model) closeSearchLayout() {
	m.refreshChrome()
	m.applyHeightMode()
	m.adjustScroll()
}

func (m *Model) netSearchResultsHelpLine() string {
	return m.commandHelp(commandModeNetSearch)
}

func (m *Model) netSearchResultsVisible() int {
	return m.effectivePlaylistVisible()
}

func (m *Model) spotSearchResultsHelpLine() string {
	return m.commandHelp(commandModeSpotSearch)
}

func (m *Model) spotSearchResultsVisible() int {
	return m.effectivePlaylistVisible()
}

func (m *Model) spotSearchPlaylistHelpLine() string {
	return m.commandHelp(commandModeSpotSearch)
}

func (m *Model) spotSearchPlaylistVisible() int {
	return m.effectivePlaylistVisible()
}

// navVisible returns the nav-browser list height. The nav browser renders
// inline in the playlist region, so it shares the playlist's row budget.
func (m *Model) navVisible() int {
	return m.effectivePlaylistVisible()
}

func (m *Model) plMgrListHelpLine() string {
	return m.commandHelp(commandModePlaylistManager)
}

func (m *Model) plMgrListVisible() int {
	return m.effectivePlaylistVisible()
}

func (m *Model) plMgrListMaybeAdjustScroll(visible int) {
	clampScroll(&m.plManager.cursor, &m.plManager.scroll, m.plMgrListViewCount(), visible)
}

func (m *Model) plMgrTracksHelpLine() string {
	return m.commandHelp(commandModePlaylistManager)
}

func (m *Model) plMgrTracksVisible() int {
	return m.effectivePlaylistVisible()
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
	m.plManager.renameOldName = ""
	m.plManager.renameName = ""
	m.plManager.visible = true
	m.plMgrListMaybeAdjustScroll(m.plMgrListVisible())
}

// plMgrEnterTrackList loads the tracks for a playlist and switches to screen 1.
func (m *Model) plMgrEnterTrackList(name string) {
	tracks, err := m.localProvider.Tracks(name)
	if err != nil {
		m.status.Errorf(statusTTLDefault, "Load failed: %s", err)
		return
	}
	m.plManager.selPlaylist = name
	m.plManager.tracks = tracks
	m.plManager.marked = make(map[int]bool)
	m.plManager.sortMode = 0
	m.setHeaderStateFromTracks(tracks)
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
		m.status.Errorf(statusTTLDefault, "Load failed: %s", err)
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
