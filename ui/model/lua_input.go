package model

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// LuaInputMsg asks the UI to show a text input requested by a plugin.
// OnSubmit runs with the text the user typed; OnCancel runs on Esc.
type LuaInputMsg struct {
	Title       string
	Placeholder string
	Value       string
	OnSubmit    func(text string)
	OnCancel    func()
}

// luaInputState is the open plugin input.
type luaInputState struct {
	title       string
	placeholder string
	value       string
	cursor      int
	onSubmit    func(string)
	onCancel    func()
}

func (m *Model) openLuaInput(title, placeholder, value string, onSubmit func(string), onCancel func()) {
	m.luaInput = &luaInputState{
		title:       title,
		placeholder: placeholder,
		value:       value,
		cursor:      len(value),
		onSubmit:    onSubmit,
		onCancel:    onCancel,
	}
}

// handleLuaInputKey drives the open input. It returns true when the key was
// consumed.
func (m *Model) handleLuaInputKey(msg tea.KeyPressMsg) bool {
	input := m.luaInput
	if input == nil {
		return false
	}
	switch msg.String() {
	case "esc":
		m.luaInput = nil
		if input.onCancel != nil {
			input.onCancel()
		}
	case "enter":
		text := strings.TrimSpace(input.value)
		if text == "" {
			return true
		}
		m.luaInput = nil
		if input.onSubmit != nil {
			input.onSubmit(text)
		}
	default:
		if m.editText("lua-input", &input.value, msg) {
			input.cursor = m.textInput.cursor
		}
	}
	return true
}

// renderLuaInput draws the input as a centered box over the frame.
func (m Model) renderLuaInput() string {
	input := m.luaInput
	if input == nil {
		return ""
	}
	width := max(36, lipgloss.Width(input.title)+4)
	width = min(width, max(20, m.layout.frameWidth-4))

	shown := input.value
	if input.cursor >= 0 && input.cursor <= len(shown) {
		shown = shown[:input.cursor] + "▏" + shown[input.cursor:]
	}
	if strings.TrimSpace(input.value) == "" && input.placeholder != "" {
		shown = dimStyle.Render(input.placeholder)
	}

	var b strings.Builder
	b.WriteString(truncate(input.title, width-2))
	b.WriteString("\n\n")
	b.WriteString("> " + truncate(shown, width-4))
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render("  enter submit   esc cancel"))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		Width(width).
		Render(b.String())
}
