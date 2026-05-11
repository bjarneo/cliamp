package model

import (
	"strings"

	"charm.land/lipgloss/v2"

	"cliamp/ui"
)

// measureOverlayVisible returns the number of list rows that fit below an
// overlay's fixed chrome (header + footer). probeSections must include a
// 1-line list placeholder so the frame measurement reflects real content.
// defaultLimit caps the result in collapsed mode; expanded mode caps at m.height.
func (m *Model) measureOverlayVisible(probeSections []string, defaultLimit int) int {
	probeFrame := ui.FrameStyle.Render(strings.Join(probeSections, "\n"))
	fixedHeight := lipgloss.Height(probeFrame) - 1
	limit := defaultLimit
	if m.heightExpanded {
		limit = m.height
	}
	return max(3, min(limit, m.height-fixedHeight))
}

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

// measurePlVisible calculates playlist lines available for a given upper limit.
func (m *Model) measurePlVisible(limit int) int {
	saved := m.plVisible
	m.plVisible = 3 // temporary minimal value for measurement
	defer func() { m.plVisible = saved }()

	// Use mainSections to get all fixed chrome plus any active transient messages.
	probe := strings.Join(m.mainSections("x", true), "\n")
	fixedLines := lipgloss.Height(ui.FrameStyle.Render(probe)) - 1
	return max(3, min(limit, m.height-fixedLines))
}

// collapsedPlVisible returns the natural (non-expanded) playlist height.
func (m *Model) collapsedPlVisible() int {
	return m.measurePlVisible(maxPlVisible)
}

// expandedPlVisible returns the expanded playlist height with no cap.
func (m *Model) expandedPlVisible() int {
	return m.measurePlVisible(m.height)
}

// applyHeightMode sets plVisible based on the current heightExpanded state.
func (m *Model) applyHeightMode() {
	if m.playlist == nil {
		return
	}
	if m.heightExpanded {
		m.plVisible = m.expandedPlVisible()
	} else {
		m.plVisible = m.collapsedPlVisible()
	}
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
	content := strings.Join(m.mainSections("", includeTransient), "\n")
	return lipgloss.Height(ui.FrameStyle.Render(content))
}

func (m Model) effectivePlaylistVisible() int {
	available := m.height - m.mainFrameFixedLines(true)
	if available <= 0 {
		return 0
	}
	if m.plVisible <= 0 {
		return 0
	}
	return min(m.plVisible, available)
}
