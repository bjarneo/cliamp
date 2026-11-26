package model

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"charm.land/lipgloss/v2"
)

// LuaSelectMsg asks the UI to show a selector requested by a plugin. OnDone
// runs with the chosen index, or -1 when the user cancels.
type LuaSelectMsg struct {
	Title   string
	Options []string
	// Cursor is the 1-based option to start on; 0 starts at the first one.
	Cursor   int
	OnDone   func(index int)
	OnCancel func()
}

// SetVisMsg asks the UI to switch the active visualizer. A plugin cannot touch
// the model directly, so it sends this instead.
type SetVisMsg struct {
	Name string
}

// luaSelectState is the open plugin selector.
type luaSelectState struct {
	title    string
	options  []string
	cursor   int
	onDone   func(index int)
	onCancel func()
}

// handleLuaSelectKey drives the open selector. It returns true when the key was
// consumed.
func (m *Model) handleLuaSelectKey(msg tea.KeyPressMsg) bool {
	sel := m.luaSelect
	if sel == nil || len(sel.options) == 0 {
		m.luaSelect = nil
		return false
	}
	switch msg.String() {
	case "esc", "q":
		m.luaSelect = nil
		if sel.onCancel != nil {
			sel.onCancel()
			return true
		}
		if sel.onDone != nil {
			sel.onDone(-1)
		}
	case "enter", " ":
		index := sel.cursor
		m.luaSelect = nil
		if sel.onDone != nil {
			sel.onDone(index)
		}
	case "up", "k":
		sel.cursor = (sel.cursor - 1 + len(sel.options)) % len(sel.options)
	case "down", "j":
		sel.cursor = (sel.cursor + 1) % len(sel.options)
	case "home":
		sel.cursor = 0
	case "end":
		sel.cursor = len(sel.options) - 1
	}
	return true
}

// renderLuaSelect draws the selector as a centered box over the frame.
func (m Model) renderLuaSelect() string {
	sel := m.luaSelect
	if sel == nil {
		return ""
	}
	width := 0
	for _, option := range sel.options {
		width = max(width, lipgloss.Width(option))
	}
	width = min(max(width+6, 24), max(20, m.layout.frameWidth-4))

	title := truncate(sel.title, width-2)
	// The list can be longer than the screen (the visualizer picker lists every
	// mode). Show a window around the cursor so the selected option is always
	// visible, with markers when there is more above or below.
	// Title, blank line, options, up to two "more" indicators, blank line,
	// footer and the box borders: keep the whole box inside the terminal.
	visible := len(sel.options)
	if maxRows := max(1, m.height-8); visible > maxRows {
		visible = maxRows
	}
	start := 0
	if sel.cursor >= visible {
		start = sel.cursor - visible + 1
	}
	if end := start + visible; end > len(sel.options) {
		start = max(0, len(sel.options)-visible)
	}
	end := min(start+visible, len(sel.options))

	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n\n")
	if start > 0 {
		b.WriteString(dimStyle.Render("  ↑ ") + truncate(fmt.Sprintf("%d more", start), width-6))
		b.WriteString("\n")
	}
	for i := start; i < end; i++ {
		option := truncate(sel.options[i], width-4)
		line := "  " + option
		if i == sel.cursor {
			line = "▸ " + option
		}
		b.WriteString(line)
		if i < end-1 || end < len(sel.options) {
			b.WriteString("\n")
		}
	}
	if end < len(sel.options) {
		b.WriteString(dimStyle.Render("  ↓ ") + truncate(fmt.Sprintf("%d more", len(sel.options)-end), width-6))
	}
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render("  ↑/↓ move   enter select   esc cancel"))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		Width(width).
		Render(b.String())
	return box
}

// overlayCentered places box over frame, centered. Whole frame lines are
// replaced inside the box's band, so the underlay styling cannot leak into it.
func overlayCentered(frame, box string, width, height int) string {
	boxLines := strings.Split(box, "\n")
	boxWidth := 0
	for _, line := range boxLines {
		boxWidth = max(boxWidth, lipgloss.Width(line))
	}
	frameLines := strings.Split(frame, "\n")
	top := max(0, (height-len(boxLines))/2)
	left := max(0, (width-boxWidth)/2)
	for i, line := range boxLines {
		row := top + i
		if row >= len(frameLines) {
			break
		}
		pad := left
		if pad > 0 {
			line = strings.Repeat(" ", pad) + line
		}
		frameLines[row] = line
	}
	return strings.Join(frameLines, "\n")
}
