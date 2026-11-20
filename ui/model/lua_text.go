package model

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// LuaTextMsg asks the UI to show a scrollable text block requested by a
// plugin. OnClose runs when the user dismisses it with Esc.
type LuaTextMsg struct {
	Title   string
	Text    string
	OnClose func()
}

// luaTextState is the open text panel.
type luaTextState struct {
	title   string
	lines   []string
	scroll  int
	onClose func()
}

func (m *Model) openLuaText(title, text string, onClose func()) {
	width := m.luaTextWidth()
	m.luaText = &luaTextState{
		title:   title,
		lines:   wrapBlock(strings.TrimSpace(text), width-4),
		onClose: onClose,
	}
}

// wrapBlock wraps each source line separately, so the lists and paragraphs a
// bot sends keep their shape.
func wrapBlock(text string, width int) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			out = append(out, "")
			continue
		}
		out = append(out, wrapText(line, width)...)
	}
	return out
}

func (m Model) luaTextWidth() int {
	width := max(40, m.layout.frameWidth-8)
	return min(width, max(20, m.layout.frameWidth-4))
}

func (m Model) luaTextVisible() int {
	if m.luaText == nil {
		return 0
	}
	return min(len(m.luaText.lines), max(3, m.height-10))
}

// handleLuaTextKey drives the open text panel. It returns true when the key
// was consumed.
func (m *Model) handleLuaTextKey(msg tea.KeyPressMsg) bool {
	text := m.luaText
	if text == nil {
		return false
	}
	visible := m.luaTextVisible()
	maxScroll := max(0, len(text.lines)-visible)
	page := max(1, visible-1)
	switch msg.String() {
	case "esc", "q":
		m.luaText = nil
		if text.onClose != nil {
			text.onClose()
		}
	case "up", "k":
		text.scroll = max(0, text.scroll-1)
	case "down", "j":
		text.scroll = min(maxScroll, text.scroll+1)
	case "pgup", "ctrl+u":
		text.scroll = max(0, text.scroll-page)
	case "pgdown", "ctrl+d":
		text.scroll = min(maxScroll, text.scroll+page)
	case "home", "g":
		text.scroll = 0
	case "end", "G":
		text.scroll = maxScroll
	}
	return true
}

// renderLuaText draws the text panel as a centered box.
func (m Model) renderLuaText() string {
	text := m.luaText
	if text == nil {
		return ""
	}
	width := m.luaTextWidth()
	visible := m.luaTextVisible()
	start := min(text.scroll, max(0, len(text.lines)-visible))
	end := min(start+visible, len(text.lines))

	var b strings.Builder
	b.WriteString(truncate(text.title, width-2))
	b.WriteString("\n\n")
	for i := start; i < end; i++ {
		b.WriteString(truncate(text.lines[i], width-4))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	if len(text.lines) > visible {
		b.WriteString("\n")
		if start > 0 {
			b.WriteString(dimStyle.Render("  ↑ more"))
		}
		if end < len(text.lines) {
			if start > 0 {
				b.WriteString("   ")
			} else {
				b.WriteString("  ")
			}
			b.WriteString(dimStyle.Render(fmt.Sprintf("↓ %d more", len(text.lines)-end)))
		}
	}
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render(truncate("  ↑/↓ scroll   esc close", width-4)))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		Width(width).
		Render(b.String())
}
