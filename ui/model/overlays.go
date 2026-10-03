package model

import (
	"os"
	"strings"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
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
	m.themePicker.filterList = filterList{}
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
	m.themePickerMaybeAdjustScroll(m.effectivePlaylistVisible())
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

// themePickerSelect confirms the current selection, persists it, and closes the picker.
func (m *Model) themePickerSelect() {
	if !m.themePickerApply() {
		return
	}
	themeName := m.ThemeName()
	if theme.IsDefaultName(themeName) {
		themeName = ""
	}
	_ = m.saveConfigString("theme", themeName)
	m.themePicker.visible = false
	m.themePicker.clearFilter()
}

// themePickerCancel restores the theme from before the picker was opened.
func (m *Model) themePickerCancel() {
	if !m.SetTheme(m.themePicker.savedName) {
		m.themeIdx = -1
		applyThemeAll(theme.Default())
	}
	m.themePicker.visible = false
	m.themePicker.clearFilter()
}

func (m *Model) themePickerMaybeAdjustScroll(visible int) {
	clampScroll(&m.themePicker.cursor, &m.themePicker.scroll, m.themePickerViewCount(), visible)
}

func (m Model) themePickerViewCount() int {
	return m.themePicker.viewCount(m.themeCount())
}

func (m Model) themePickerRawIndex(viewIdx int) (int, bool) {
	return m.themePicker.rawIndex(viewIdx, m.themeCount())
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
	query := strings.ToLower(m.themePicker.filter)
	m.themePicker.recompute(m.themeCount(), func(rawIdx int) bool {
		return strings.Contains(strings.ToLower(m.themePickerName(rawIdx)), query)
	})
}

// openVisPicker opens the visualizer picker, which renders the mode list in the
// playlist region while keeping the visualizer live above it for preview. The
// cursor starts on the currently active mode.
func (m *Model) openVisPicker() {
	m.visPicker.visible = true
	m.visPicker.savedMode = int(m.vis.Mode)
	m.visPicker.filterList = filterList{cursor: int(m.vis.Mode)}
	// Capture the mode list once; it is stable while the picker is open (Lua
	// visualizers are registered at startup), so callers avoid re-allocating it.
	m.visPicker.modes = m.vis.AllModeNames()
	// Recompute chrome/height for the picker layout (its header + help differ
	// from the playlist), then fit the cursor into the visible window.
	m.refreshChrome()
	m.applyHeightMode()
	m.visPickerMaybeAdjustScroll(m.effectivePlaylistVisible())
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
	m.visPicker.clearFilter()
	m.refreshChrome()
	m.applyHeightMode()
	m.adjustScroll()
}

// visPickerSelect confirms the current selection, persists it, and closes.
func (m *Model) visPickerSelect() {
	if !m.visPickerApply() {
		return
	}
	_ = m.saveConfigString("visualizer", m.vis.ModeName())
	m.visPickerClose()
}

// visPickerCancel restores the mode from before the picker was opened.
func (m *Model) visPickerCancel() {
	m.vis.SetMode(ui.VisMode(m.visPicker.savedMode))
	m.visPickerClose()
}

func (m *Model) visPickerMaybeAdjustScroll(visible int) {
	clampScroll(&m.visPicker.cursor, &m.visPicker.scroll, m.visPickerViewCount(), visible)
}

func (m Model) visPickerViewCount() int {
	return m.visPicker.viewCount(len(m.visPicker.modes))
}

func (m Model) visPickerRawIndex(viewIdx int) (int, bool) {
	return m.visPicker.rawIndex(viewIdx, len(m.visPicker.modes))
}

func (m *Model) visPickerRecomputeFilter() {
	query := strings.ToLower(m.visPicker.filter)
	m.visPicker.recompute(len(m.visPicker.modes), func(rawIdx int) bool {
		return strings.Contains(strings.ToLower(m.visPicker.modes[rawIdx]), query)
	})
}

// closeSearchLayout restores playlist sizing after the inline search header and
// help line are dismissed, then refits the playlist cursor into view.
func (m *Model) closeSearchLayout() {
	m.refreshChrome()
	m.applyHeightMode()
	m.adjustScroll()
}

func (m *Model) searchOverlayResultsVisible() int {
	visible := m.effectivePlaylistVisible()
	if m.searchOverlay.err != "" {
		visible--
	}
	return max(0, visible)
}

func (m *Model) plMgrListMaybeAdjustScroll(visible int) {
	clampScroll(&m.plManager.cursor, &m.plManager.scroll, m.plMgrListViewCount(), visible)
}

func (m *Model) plMgrDirsMaybeAdjustScroll(visible int) {
	clampScroll(&m.plManager.cursor, &m.plManager.scroll, len(m.plManager.dirs), visible)
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
	m.plManager.scroll = m.fitHeaderScroll(tracks, m.plManager.scroll, m.plManager.cursor, visible, true)
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
	m.plMgrListMaybeAdjustScroll(m.effectivePlaylistVisible())
}

// localManagerInline reports whether the Local source shows the playlist
// manager in place of the provider pane list. The Local pane list (Favorites,
// Recently Played, saved lists as a read-only picker) is removed; selecting
// the Local source shows the manager with its full add/remove/rename/edit
// actions instead of requiring a separate `p` step.
func (m Model) localManagerInline() bool {
	return m.activeProviderKey() == providerKeyLocal && m.localProvider != nil
}

// ensureLocalManager opens the manager when the Local source is active but
// its list is not showing. Call after landing on the Local provider pane so
// the old read-only list never appears.
func (m *Model) ensureLocalManager() {
	if m.localManagerInline() && !m.plManager.visible {
		m.openPlaylistManager()
	}
}

// closePlManagerListInline leaves the manager list for the live queue when
// the manager is the Local source view. The old provider pane list is gone,
// so closing must not reveal it.
func (m *Model) closePlManagerListInline() {
	m.plManager.visible = false
	m.focus = focusPlaylist
}

// plMgrEnterTrackList loads the tracks for a playlist and switches to screen 1.
func (m *Model) plMgrEnterTrackList(name string) {
	tracks, err := m.localProvider.Tracks(name)
	if err != nil {
		m.status.Errorf(statusTTLDefault, "Load failed: %s", err)
		return
	}
	m.plManager.selPlaylist = name
	m.plMgrLoadTracks(tracks)
	m.plManager.marked = make(map[int]bool)
	m.plManager.sortMode = 0
	m.setHeaderStateFromTracks(tracks)
	m.plManager.screen = plMgrScreenTracks
	m.plManager.cursor = 0
	m.plManager.scroll = 0
	m.plManager.confirmDel = false
	m.plMgrResetFilter()
	m.plMgrTracksMaybeAdjustScroll(m.effectivePlaylistVisible())
}

// plMgrReloadTracks re-reads the open track list in place so store changes
// (fresh history entries, favorite toggles) appear without leaving the
// screen. The cursor is clamped rather than reset, stale row marks are
// dropped, and any active filter is re-applied.
func (m *Model) plMgrReloadTracks(name string) {
	tracks, err := m.localProvider.Tracks(name)
	if err != nil {
		return
	}
	m.plMgrLoadTracks(tracks)
	m.setHeaderStateFromTracks(tracks)
	m.plManager.marked = make(map[int]bool)
	if m.plManager.filter != "" {
		m.plMgrRecomputeFilter()
	}
	newCount := m.plMgrTracksViewCount()
	if newCount == 0 {
		// An emptied list (e.g. the last favorite removed) can leave a
		// stale scroll offset: the adjust helper returns early at zero.
		m.plManager.cursor = 0
		m.plManager.scroll = 0
		return
	}
	if m.plManager.cursor >= newCount {
		m.plManager.cursor = newCount - 1
	}
	if m.plManager.cursor < 0 {
		m.plManager.cursor = 0
	}
	m.plMgrTracksMaybeAdjustScroll(m.effectivePlaylistVisible())
}

// plMgrLoadTracks refreshes missing-file state only at an explicit list load.
// The synchronous stats stay out of render and in-memory edit paths.
func (m *Model) plMgrLoadTracks(tracks []playlist.Track) {
	m.plManager.tracks = tracks
	m.plMgrRefreshMissingLocal()
}

func (m *Model) plMgrRestoreTracks(tracks []playlist.Track, missingLocal []bool) {
	m.plManager.tracks = cloneTracks(tracks)
	m.plManager.missingLocal = append([]bool(nil), missingLocal...)
	m.plMgrEnsureMissingLocal()
}

func (m *Model) plMgrEnsureMissingLocal() {
	if len(m.plManager.missingLocal) == len(m.plManager.tracks) {
		return
	}
	missingLocal := make([]bool, len(m.plManager.tracks))
	copy(missingLocal, m.plManager.missingLocal)
	m.plManager.missingLocal = missingLocal
}

func (m *Model) plMgrRefreshMissingLocal() {
	m.plManager.missingLocal = make([]bool, len(m.plManager.tracks))
	for i, track := range m.plManager.tracks {
		m.plManager.missingLocal[i] = missingLocalTrack(track)
	}
}

func missingLocalTrack(track playlist.Track) bool {
	if track.Path == "" || track.Stream || playlist.IsURL(track.Path) || strings.HasPrefix(track.Path, "ssh://") {
		return false
	}
	_, err := os.Stat(track.Path)
	return os.IsNotExist(err)
}

// plMgrOpenDirs loads the [[dir]] sources for the open playlist and switches
// to the directory-sources screen. Playlists whose provider does not implement
// provider.PlaylistDirSourceManager show a notice instead of switching.
func (m *Model) plMgrOpenDirs() {
	if name := plMgrVirtualPlaylistName(m.plManager.selPlaylist); name != "" {
		m.status.Showf(statusTTLDefault, "%q is a virtual playlist with no directory sources", name)
		return
	}
	dm, ok := m.localProvider.(provider.PlaylistDirSourceManager)
	if !ok {
		m.status.Showf(statusTTLDefault, "%q does not support directory sources", m.plManager.selPlaylist)
		return
	}
	dirs, err := dm.DirSources(m.plManager.selPlaylist)
	if err != nil {
		m.status.Errorf(statusTTLDefault, "Load dir sources: %s", err)
		return
	}
	m.plManager.dirs = dirs
	m.plManager.screen = plMgrScreenDirs
	m.plManager.cursor = 0
	m.plManager.scroll = 0
	m.plManager.confirmDel = false
	m.plMgrResetFilter()
	m.plMgrDirsMaybeAdjustScroll(m.effectivePlaylistVisible())
}

// plMgrReloadDirs re-reads the [[dir]] sources for the open playlist and
// clamps the cursor so it stays valid after an add or remove.
func (m *Model) plMgrReloadDirs() {
	dm, ok := m.localProvider.(provider.PlaylistDirSourceManager)
	if !ok {
		return
	}
	dirs, err := dm.DirSources(m.plManager.selPlaylist)
	if err != nil {
		m.status.Errorf(statusTTLDefault, "Reload dir sources: %s", err)
		return
	}
	m.plManager.dirs = dirs
	if m.plManager.cursor >= len(dirs) {
		m.plManager.cursor = len(dirs) - 1
	}
	if m.plManager.cursor < 0 {
		m.plManager.cursor = 0
	}
	m.plMgrDirsMaybeAdjustScroll(m.effectivePlaylistVisible())
}

// plMgrRefreshTracksForSel reloads the tracks of the open playlist so changes
// to its [[dir]] sources (add/remove/toggle-recursive) are reflected in the
// tracks screen immediately.
func (m *Model) plMgrRefreshTracksForSel() {
	tracks, err := m.localProvider.Tracks(m.plManager.selPlaylist)
	if err != nil {
		return
	}
	// plMgrLoadTracks keeps the missingLocal cache in sync with the new
	// track slice; assigning tracks directly would leave stale per-track
	// missing-file indicators mapped onto the wrong entries.
	m.plMgrLoadTracks(tracks)
	m.setHeaderStateFromTracks(tracks)
}

// fbAddDirSource adds directories as [[dir]] sources on the target playlist:
// the selected folders when any are selected, otherwise the folder under the
// cursor, otherwise the directory being browsed. The browser stays open so
// several folders can be added in a row (Esc leaves). Invoked by the file
// browser's D key when a target playlist is set.
func (m *Model) fbAddDirSource() {
	target := m.fileBrowser.targetPlaylist
	if target == "" {
		return
	}
	if name := plMgrVirtualPlaylistName(target); name != "" {
		m.status.Showf(statusTTLDefault, "%q is a virtual playlist with no directory sources", name)
		return
	}
	var dirs []string
	for _, e := range m.fileBrowser.entries {
		if m.fileBrowser.selected[e.path] && e.isDir && !e.isParent {
			dirs = append(dirs, e.path)
		}
	}
	if len(dirs) == 0 && m.fileBrowser.cursor < m.fbCount() {
		if e := m.fbEntry(m.fileBrowser.cursor); e.isDir && !e.isParent {
			dirs = []string{e.path}
		}
	}
	if len(dirs) == 0 {
		dirs = []string{m.fileBrowser.dir}
	}
	m.plMgrAddDirSources(target, dirs)
}

// plMgrAddDirSources adds dirs to the target playlist via the local provider,
// refreshes an open manager screen for it, and reports the outcome in the
// status bar. It returns how many sources were added and skipped as already
// referenced.
func (m *Model) plMgrAddDirSources(target string, dirs []string) (added, skipped int) {
	dm, ok := m.localProvider.(provider.PlaylistDirSourceManager)
	if !ok {
		m.status.Showf(statusTTLDefault, "This provider does not support directory sources")
		return 0, 0
	}
	added, skipped = 0, 0
	var firstErr error
	for _, d := range dirs {
		a, err := dm.AddDirSource(target, d)
		if err != nil {
			firstErr = err
			break
		}
		if a {
			added++
		} else {
			skipped++
		}
	}
	// Reflect any successful additions in an open manager screen for this
	// playlist before reporting a partial failure, so the open screen never
	// shows stale sources or counts after an AddDirSource error mid-loop.
	if added > 0 && m.plManager.visible && m.plManager.selPlaylist == target {
		switch m.plManager.screen {
		case plMgrScreenDirs:
			m.plMgrReloadDirs()
			m.plMgrRefreshTracksForSel()
		case plMgrScreenTracks:
			m.plMgrRefreshTracksForSel()
		}
		m.plMgrRefreshList()
	}
	switch {
	case firstErr != nil && added > 0:
		m.status.Showf(statusTTLDefault, "Added %d dir source(s) to %q; then failed: %s", added, target, firstErr)
	case firstErr != nil:
		m.status.Errorf(statusTTLDefault, "Add dir source failed: %s", firstErr)
	case added > 0 && skipped > 0:
		m.status.Showf(statusTTLDefault, "Added %d dir source(s) to %q (%d already referenced)", added, target, skipped)
	case added > 0:
		m.status.Showf(statusTTLDefault, "Added %d dir source(s) to %q", added, target)
	default:
		m.status.Showf(statusTTLDefault, "%q already references that directory", target)
	}
	return added, skipped
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
		m.plMgrListMaybeAdjustScroll(m.effectivePlaylistVisible())
	} else if m.plManager.screen == plMgrScreenTracks {
		m.plMgrTracksMaybeAdjustScroll(m.effectivePlaylistVisible())
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
	// Cursor/scroll clamping is list-screen-specific: the tracks and
	// directory-sources screens own their own cursor and re-adjust after
	// refreshing. Only the list screen re-clamps here.
	if m.plManager.screen == plMgrScreenList {
		total := m.plMgrListViewCount()
		if m.plManager.cursor >= total {
			m.plManager.cursor = total - 1
		}
		if m.plManager.cursor < 0 {
			m.plManager.cursor = 0
		}
		m.plMgrListMaybeAdjustScroll(m.effectivePlaylistVisible())
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
