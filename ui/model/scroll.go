package model

import "github.com/bjarneo/cliamp/playlist"

// clampScroll keeps cursor inside [0, count) and adjusts scroll so that
// the cursor sits within the visible window of `visible` rows.
func clampScroll(cursor, scroll *int, count, visible int) {
	if visible <= 0 {
		return
	}
	if *cursor < 0 {
		*cursor = 0
	}
	if *cursor >= count && count > 0 {
		*cursor = count - 1
	}
	if *cursor < *scroll {
		*scroll = *cursor
	} else if *cursor >= *scroll+visible {
		*scroll = *cursor - visible + 1
	}
	if *scroll+visible > count && count > 0 {
		*scroll = max(0, count-visible)
	}
	if *scroll < 0 {
		*scroll = 0
	}
}

// stepListCursor moves cursor for the list navigation keys: up and down
// wrap, page up and page down move by page rows, and home and end jump to the
// ends. count is the row count. It returns false for any other key.
func stepListCursor(key string, cursor *int, count, page int) bool {
	switch key {
	case "up", "k":
		if *cursor > 0 {
			*cursor--
		} else if count > 0 {
			*cursor = count - 1
		}
	case "down", "j":
		if *cursor < count-1 {
			*cursor++
		} else if count > 0 {
			*cursor = 0
		}
	case "pgup", "ctrl+u":
		*cursor -= min(max(0, *cursor), page)
	case "pgdown", "ctrl+d":
		if *cursor < count-1 {
			*cursor = min(count-1, *cursor+page)
		}
	case "home", "g":
		*cursor = 0
	case "end", "G":
		if count > 0 {
			*cursor = count - 1
		}
	default:
		return false
	}
	return true
}

// applyHeightMode sets plVisible based on the current heightExpanded state.
func (m *Model) applyHeightMode() {
	m.recomputeLayout()
	m.normalizeMainFocus()
}

// adjustScroll ensures plCursor is visible in the playlist view.
// It accounts for album separator lines that reduce the number of
// tracks that fit in the visible window.
func (m *Model) adjustScroll() {
	if m.playlist == nil {
		return
	}
	if m.playlist.Len() == 0 {
		return
	}
	visible := m.effectivePlaylistVisible()
	if visible <= 0 {
		return
	}
	m.plScroll = m.playlistScroll(visible)
}

func (m Model) playlistScroll(visible int) int {
	count := m.playlist.Len()
	if count == 0 {
		return 0
	}
	scroll := max(0, m.plScroll)
	if scroll >= count {
		scroll = count - 1
	}
	cursor := min(max(0, m.plCursorRow()), count-1)
	if cursor < scroll {
		return cursor
	}
	if visible <= 0 {
		return cursor
	}
	if !m.showAlbumHeaders {
		if cursor-scroll >= visible {
			return cursor - visible + 1
		}
		return scroll
	}

	// Every track consumes at least one row, so no earlier position can keep
	// the cursor visible. Include one lookback track for sticky album headers.
	scroll = max(scroll, cursor-visible+1)
	start := max(0, scroll-1)
	_, tracks := m.playlist.OrderWindow(start, cursor-start+1)
	return start + m.fitHeaderScroll(tracks, scroll-start, cursor-start, visible, m.showAlbumHeaders)
}

// fitHeaderScroll moves scroll down until the rows from scroll through cursor
// fit in visible rows. The album headers of tracks count as rows.
func (m Model) fitHeaderScroll(tracks []playlist.Track, scroll, cursor, visible int, showHeaders bool) int {
	for scroll < cursor && m.albumSeparatorRows(tracks, scroll, cursor, showHeaders) > visible {
		scroll++
	}
	return scroll
}

func (m Model) mainFrameFixedLines(includeTransient bool) int {
	fixed := 2*m.layout.paddingV + m.layout.fixedRows
	if includeTransient {
		fixed += m.layout.footerRows
	}
	return fixed
}

func (m Model) effectivePlaylistVisible() int {
	if m.layout.frameWidth == 0 {
		if m.plVisible > 0 {
			return m.plVisible
		}
		return 0
	}
	if m.layout.tooSmall() || m.layout.bodyRows <= 0 {
		return 0
	}
	return min(m.plVisible, m.layout.bodyRows)
}

func (m *Model) refreshChrome() {
	m.recomputeLayout()
}

func (m *Model) clampActiveScrollState() {
	if m.layout.tooSmall() {
		return
	}
	switch m.activeScreen() {
	case screenKeymap:
		m.keymapMaybeAdjustScroll(m.effectivePlaylistVisible())
	case screenThemePicker:
		m.themePickerMaybeAdjustScroll(m.effectivePlaylistVisible())
	case screenVisPicker:
		m.visPickerMaybeAdjustScroll(m.effectivePlaylistVisible())
	case screenDevicePicker:
		clampScroll(&m.devicePicker.cursor, &m.devicePicker.scroll, len(m.devicePicker.devices), m.effectivePlaylistVisible())
	case screenPlaylistPicker:
		m.plPickerMaybeAdjustScroll(m.plPickerVisible())
	case screenFileBrowser:
		m.fbMaybeAdjustScroll(m.fbVisible())
	case screenNavBrowser:
		m.navMaybeAdjustScroll()
	case screenPlaylistManager:
		if m.plManager.screen == plMgrScreenList {
			m.plMgrListMaybeAdjustScroll(m.effectivePlaylistVisible())
		} else if m.plManager.screen == plMgrScreenTracks {
			m.plMgrTracksMaybeAdjustScroll(m.effectivePlaylistVisible())
		} else if m.plManager.screen == plMgrScreenDirs {
			m.plMgrDirsMaybeAdjustScroll(m.effectivePlaylistVisible())
		}
	case screenSearchOverlay:
		if m.searchOverlay.screen == searchOverlayResults {
			m.searchOverlayResultsMaybeAdjustScroll(m.searchOverlayResultsVisible())
		} else if m.searchOverlay.screen == searchOverlayPlaylist {
			m.searchOverlayPlaylistMaybeAdjustScroll(m.effectivePlaylistVisible())
		}
	case screenQueue:
		m.normalizeQueueOverlay()
	case screenInfo:
		m.infoMaybeAdjustScroll()
	case screenSearch:
		m.searchMaybeAdjustScroll(m.effectivePlaylistVisible())
	case screenNetSearch:
		if m.netSearch.screen == netSearchResults {
			m.netSearchResultsMaybeAdjustScroll(m.effectivePlaylistVisible())
		}
	case screenLyrics:
		m.lyrics.scroll = min(m.lyrics.scroll, max(0, len(m.lyrics.lines)-m.effectivePlaylistVisible()))
	default:
		// The provider filter shows only in the provider pane.
		switch {
		case m.focus == focusProvider && m.provSearch.active:
			m.provSearchMaybeAdjustScroll()
		case m.focus == focusProvider:
			m.providerMaybeAdjustScroll()
		default:
			m.adjustScroll()
		}
	}
}
