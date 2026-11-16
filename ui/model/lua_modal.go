package model

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/luaplugin"
)

// LuaSelectMsg asks the UI to show a selector requested by a plugin. OnDone
// runs with the chosen index, or -1 when the user cancels.
type LuaSelectMsg struct {
	Title   string
	Body    string
	Options []luaplugin.SelectOption
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
	body     string
	options  []luaplugin.SelectOption
	cursor   int
	onDone   func(index int)
	onCancel func()
}

// handleLuaSelectKey drives the open selector. It returns true when the key was
// consumed.
func (m *Model) handleLuaSelectKey(msg tea.KeyPressMsg) bool {
	sel := m.luaSelect
	if sel == nil {
		return false
	}
	switch msg.String() {
	case "esc", "q":
		m.luaSelect = nil
		if sel.onCancel != nil {
			sel.onCancel()
			return true
		}
		sel.onDone(-1)
	case "enter", " ":
		index := sel.cursor
		m.luaSelect = nil
		sel.onDone(index)
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

// renderLuaSelect draws the selector as a centered box over the frame. An
// option can be one line or three: title, subtitle and description.
func (m Model) renderLuaSelect() string {
	sel := m.luaSelect
	if sel == nil || len(sel.options) == 0 {
		return ""
	}
	width := 0
	for _, option := range sel.options {
		width = max(width, lipgloss.Width(oneLine(option.Title)))
		width = max(width, lipgloss.Width(oneLine(option.Subtitle)))
		width = max(width, lipgloss.Width(oneLine(option.Description)))
		for _, line := range imageLines(option.Image) {
			width = max(width, lipgloss.Width(line)+4)
		}
	}
	for _, line := range wrapBlock(sel.body, 40) {
		width = max(width, lipgloss.Width(line))
	}
	width = min(max(width+8, 40), max(20, m.layout.frameWidth-4))

	title := truncate(sel.title, width-2)
	bodyLines := wrapBlock(sel.body, width-4)
	if maxBody := max(1, (m.height-10)/2); len(bodyLines) > maxBody {
		bodyLines = append(bodyLines[:maxBody], "…")
	}

	linesOf := func(option luaplugin.SelectOption) int {
		lines := len(imageLines(option.Image)) + 1
		if option.Subtitle != "" {
			lines++
		}
		if option.Description != "" {
			lines++
		}
		return lines
	}
	maxRows := max(3, m.height-8-len(bodyLines))
	start := max(0, min(sel.cursor, len(sel.options)-1))
	rows := linesOf(sel.options[start])
	for start > 0 && rows+linesOf(sel.options[start-1]) <= maxRows {
		start--
		rows += linesOf(sel.options[start])
	}
	end := start
	rows = 0
	for end < len(sel.options) && rows+linesOf(sel.options[end]) <= maxRows {
		rows += linesOf(sel.options[end])
		end++
	}
	if end <= sel.cursor {
		end = min(len(sel.options), sel.cursor+1)
		if start > sel.cursor {
			start = sel.cursor
		}
	}

	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n\n")
	for _, line := range bodyLines {
		b.WriteString(truncate(line, width-4))
		b.WriteString("\n")
	}
	if len(bodyLines) > 0 {
		b.WriteString("\n")
	}
	if start > 0 {
		b.WriteString(dimStyle.Render("  ↑ ") + truncate(fmt.Sprintf("%d more", start), width-6))
		b.WriteString("\n")
	}
	for i := start; i < end; i++ {
		option := sel.options[i]
		if i > start {
			b.WriteString("\n")
		}
		for _, line := range imageLines(option.Image) {
			b.WriteString("  " + line + imageResetSuffix() + "\n")
		}
		marker := "  "
		if i == sel.cursor {
			marker = "▸ "
		}
		b.WriteString(marker + truncate(oneLine(option.Title), width-6))
		if option.Subtitle != "" {
			b.WriteString("\n  " + dimStyle.Render(truncate(oneLine(option.Subtitle), width-6)))
		}
		if option.Description != "" {
			b.WriteString("\n  " + dimStyle.Render(truncate(oneLine(option.Description), width-6)))
		}
	}
	if end < len(sel.options) {
		b.WriteString("\n" + dimStyle.Render("  ↓ ") + truncate(fmt.Sprintf("%d more", len(sel.options)-end), width-6))
	}
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render("  ↑/↓ move   enter   esc"))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		Width(width).
		Render(b.String())
	return box
}

// oneLine folds the line breaks a plugin may put in an option. A selector
// counts options, not rendered lines, so an embedded newline would make the
// box taller than the terminal and hide the rows below.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, s)
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
