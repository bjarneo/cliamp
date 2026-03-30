package model

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"cliamp/playlist"
	"cliamp/ui"
)

// renderedLineCount returns how many rendered lines tracks[from..to) would
// take, including album separator lines between different albums.
func renderedLineCount(tracks []playlist.Track, from, to int) int {
	lines := 0
	prevAlbum := ""
	if from > 0 {
		prevAlbum = tracks[from-1].Album
	}
	for i := from; i < to && i < len(tracks); i++ {
		if album := tracks[i].Album; album != "" && album != prevAlbum {
			lines++ // album separator
		}
		prevAlbum = tracks[i].Album
		lines++ // track line
	}
	return lines
}

// defaultPlVisible recalculates the natural plVisible for the current terminal
// height (same logic as the window-resize handler, capped at maxPlVisible).
func (m *Model) defaultPlVisible() int {
	saved := m.plVisible
	m.plVisible = 3 // temporary minimal value for measurement
	defer func() { m.plVisible = saved }()
	probe := strings.Join([]string{
		m.renderTitle(), m.renderTrackInfo(), m.renderTimeStatus(), "",
		m.renderSpectrum(), m.renderSeekBar(), "",
		m.renderControls(), "", m.renderPlaylistHeader(),
		"x", "", m.renderHelp(), m.renderBottomStatus(),
	}, "\n")
	fixedLines := lipgloss.Height(ui.FrameStyle.Render(probe)) - 1
	return max(3, min(maxPlVisible, m.height-fixedLines))
}

// adjustScroll ensures plCursor is visible in the playlist view.
// It accounts for album separator lines that reduce the number of
// tracks that fit in the visible window.
func (m *Model) adjustScroll() {
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
	lines := renderedLineCount(tracks, scroll, m.plCursor+1)
	if lines <= visible {
		return scroll
	}
	scroll = m.plCursor
	lines = 1 // the cursor track itself
	for i := m.plCursor - 1; i >= 0; i-- {
		add := 1 // track line
		if tracks[i+1].Album != "" && tracks[i+1].Album != tracks[i].Album {
			add++ // separator above track i+1
		}
		if lines+add > visible {
			break
		}
		lines += add
		scroll = i
	}
	if scroll > 0 && tracks[scroll].Album != "" && tracks[scroll].Album != tracks[scroll-1].Album {
		if lines+1 > visible {
			scroll++
		}
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
