package model

// mouse.go wires pointer input into the TUI: click/drag seeking on the
// progress bar, wheel scrolling, and right-click track context menus.
//
// The Model is a value receiver, so the screen geometry a MouseMsg is
// hit-tested against cannot live on the Model itself — View() renders from
// a copy. Instead it is recorded each frame into m.mouse, a shared pointer
// like ipcRuntimeState, and Update() reads it back. Terminals without mouse
// reporting simply never deliver MouseMsgs, so nothing here degrades a
// keyboard-only environment.

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/ui"
)

// mouseState is the per-frame hit geometry of the interactive regions:
// where the progress bar and the playlist-region body were drawn on screen.
// seekRow/bodyRow are -1 when the region is absent this frame (simplified
// layout, fullscreen visualizer, too-small terminal).
type mouseState struct {
	seekRow int // screen row of the seek bar
	seekX   int // first cell of the seek bar
	seekW   int // bar width in cells

	bodyRow  int // first screen row of the playlist-region body
	bodyRows int // body height in rows
	bodyX    int // first cell of the track-list column
	bodyW    int // track-list column width in cells

	dragging bool // left button held since a seek-bar click
}

// recordMouseGeometry maps this frame's content to absolute screen
// coordinates: padTop/padLeft mirror centerFrame's centering and the frame's
// own padding converts a content line index into a screen row. The seek bar
// and body are located inside the sections by re-rendering them — the
// renders are pure functions of model state, so string equality is exact.
func (m *Model) recordMouseGeometry(content string, sections []string, body string) {
	if m.mouse == nil {
		return
	}
	ms := m.mouse
	ms.seekRow, ms.bodyRow = -1, -1
	if m.fullVis || m.layout.tooSmall() {
		return
	}
	frame := ui.FrameStyle.Render(content)
	padTop := max(0, (m.height-lipgloss.Height(frame))/2)
	padLeft := max(0, (m.width-lipgloss.Width(frame))/2)
	x := padLeft + ui.PaddingH
	row := padTop + ui.VerticalPadding()
	seekBar := m.renderSeekBar()
	for _, sec := range sections {
		switch {
		case ms.seekRow < 0 && sec == seekBar && seekBar != "":
			ms.seekRow = row
			ms.seekX = x
			ms.seekW = ui.PanelWidth
		case ms.bodyRow < 0 && sec == body && body != "":
			ms.bodyRow = row
			ms.bodyRows = strings.Count(body, "\n") + 1
			ms.bodyX = x
			ms.bodyW = ui.PanelWidth
			// The two-column body splits the region: the left playlistWidth
			// cells hold the track list, then a gutter, then settings.
			if m.layout.twoColumn {
				ms.bodyW = m.layout.playlistWidth
			}
		}
		row += strings.Count(sec, "\n") + 1
	}
}

// handleMouseClick dispatches a button-down event: left on the seek bar
// starts a seek drag, left on a track row moves that surface's cursor, and
// right on a track row opens the context menu.
func (m *Model) handleMouseClick(msg tea.MouseClickMsg) tea.Cmd {
	if m.mouse == nil {
		return nil
	}
	ms := m.mouse
	x, y := msg.X, msg.Y
	switch {
	case ms.seekRow >= 0 && y == ms.seekRow && x >= ms.seekX && x < ms.seekX+ms.seekW:
		if msg.Button != tea.MouseLeft {
			return nil
		}
		ms.dragging = true
		return m.seekToBarCell(x - ms.seekX)
	case ms.bodyRow >= 0 && y >= ms.bodyRow && y < ms.bodyRow+ms.bodyRows &&
		x >= ms.bodyX && x < ms.bodyX+ms.bodyW:
		row, col := y-ms.bodyRow, x-ms.bodyX
		switch msg.Button {
		case tea.MouseRight:
			return m.openTrackMenuAt(row, col)
		case tea.MouseLeft:
			m.selectTrackRow(row, col)
		}
	}
	return nil
}

// handleMouseRelease ends a seek drag and flushes any debounced seek target
// so the final position lands at release, not a half-second later.
func (m *Model) handleMouseRelease() tea.Cmd {
	if m.mouse == nil || !m.mouse.dragging {
		return nil
	}
	m.mouse.dragging = false
	if m.seek.active && m.seek.timer > 0 {
		m.seek.timer = 0
		return m.commitPendingSeek()
	}
	return nil
}

// handleMouseMotion continues a seek drag; motion outside the bar still
// follows the pointer so fast drags land where the button is released.
func (m *Model) handleMouseMotion(msg tea.MouseMotionMsg) tea.Cmd {
	if m.mouse == nil || !m.mouse.dragging || m.mouse.seekW <= 0 {
		return nil
	}
	return m.seekToBarCell(msg.X - m.mouse.seekX)
}

// handleMouseWheel scrolls through the active surface's own key handler so
// every list (playlist, queue, overlays) scrolls the way j/k would.
func (m *Model) handleMouseWheel(msg tea.MouseWheelMsg) tea.Cmd {
	code := tea.KeyDown
	if msg.Button == tea.MouseWheelUp {
		code = tea.KeyUp
	}
	var cmd tea.Cmd
	for range 3 {
		cmd = m.handleKey(tea.KeyPressMsg{Code: code})
	}
	return cmd
}

// seekToBarCell seeks to the fraction of the track under bar cell col.
// Streamed/ytdl tracks go through the same debounced machinery as key
// seeks, so a drag does not restart decoding on every cell.
func (m *Model) seekToBarCell(col int) tea.Cmd {
	if m.mouse == nil || m.player == nil || !m.player.Seekable() || m.cachedDur <= 0 {
		return nil
	}
	frac := float64(col) / float64(max(1, m.mouse.seekW-1))
	frac = min(1.0, max(0.0, frac))
	return m.seekAbsolute(time.Duration(frac * float64(m.cachedDur)))
}
