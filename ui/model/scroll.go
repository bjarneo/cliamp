package model

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
	tracks := m.playlist.Tracks()
	if len(tracks) == 0 {
		return
	}
	visible := m.effectivePlaylistVisible()
	if visible <= 0 {
		return
	}
	m.plScroll = m.playlistScroll(visible)
}

func (m Model) playlistScroll(visible int) int {
	tracks := m.playlist.Tracks()
	scroll := max(0, m.plScroll)
	if scroll >= len(tracks) {
		scroll = max(0, len(tracks)-1)
	}
	if m.plCursor < scroll {
		return m.plCursor
	}
	for scroll < m.plCursor && m.albumSeparatorRows(tracks, scroll, m.plCursor, m.showAlbumHeaders) > visible {
		scroll++
	}
	return scroll
}

func (m Model) mainFrameFixedLines(includeTransient bool) int {
	if m.layout.frameWidth == 0 {
		m.recomputeLayout()
	}
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
	if m.provSearch.active {
		m.provSearchMaybeAdjustScroll()
		return
	}
	switch m.activeScreen() {
	case screenKeymap:
		m.keymapMaybeAdjustScroll(m.keymapVisible())
	case screenThemePicker:
		m.themePickerMaybeAdjustScroll(m.themePickerVisible())
	case screenVisPicker:
		m.visPickerMaybeAdjustScroll(m.visPickerVisible())
	case screenDevicePicker:
		clampScroll(&m.devicePicker.cursor, &m.devicePicker.scroll, len(m.devicePicker.devices), m.devicePickerVisible())
	case screenPlaylistPicker:
		m.plPickerMaybeAdjustScroll(m.plPickerVisible())
	case screenFileBrowser:
		m.fbMaybeAdjustScroll(m.fbVisible())
	case screenNavBrowser:
		m.navMaybeAdjustScroll()
	case screenPlaylistManager:
		if m.plManager.screen == plMgrScreenList {
			m.plMgrListMaybeAdjustScroll(m.plMgrListVisible())
		} else if m.plManager.screen == plMgrScreenTracks {
			m.plMgrTracksMaybeAdjustScroll(m.plMgrTracksVisible())
		}
	case screenArtist:
		if len(m.artist.drill) > 0 {
			m.artistDrillMaybeAdjustScroll(&m.artist.drill[len(m.artist.drill)-1])
		} else {
			m.artistMaybeAdjustScroll()
		}
	case screenSpotSearch:
		if m.spotSearch.screen == spotSearchResults {
			m.spotSearchResultsMaybeAdjustScroll(m.spotSearchResultsVisible())
		} else if m.spotSearch.screen == spotSearchPlaylist {
			m.spotSearchPlaylistMaybeAdjustScroll(m.spotSearchPlaylistVisible())
		}
	case screenHome:
		m.homeMaybeAdjustScroll()
	case screenQueue:
		m.queueMaybeAdjustScroll(m.queueVisible())
	case screenInfo:
		m.infoMaybeAdjustScroll()
	case screenSearch:
		m.searchMaybeAdjustScroll(m.searchVisible())
	case screenNetSearch:
		if m.netSearch.screen == netSearchResults {
			m.netSearchResultsMaybeAdjustScroll(m.netSearchResultsVisible())
		}
	case screenLyrics:
		m.lyrics.scroll = min(m.lyrics.scroll, max(0, len(m.lyrics.lines)-m.effectivePlaylistVisible()))
	default:
		if m.focus == focusProvider {
			m.providerMaybeAdjustScroll()
		} else {
			m.adjustScroll()
		}
	}
}
