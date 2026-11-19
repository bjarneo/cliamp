package model

import (
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// textEditor shares a cursor between mutually exclusive inline text inputs.
// Switching fields starts at that field's end, while reopening a cleared field
// naturally clamps the cursor back to zero.
type textEditor struct {
	field  string
	cursor int // byte offset at a UTF-8 rune boundary
}

func (e *textEditor) begin(field, value string) {
	if e.field != field {
		e.field = field
		e.cursor = len(value)
	}
	e.cursor = min(e.cursor, len(value))
	for e.cursor > 0 && e.cursor < len(value) && !utf8.RuneStart(value[e.cursor]) {
		e.cursor--
	}
}

// editText applies a text-editing key to value. It returns true when it owns
// the key, leaving Enter, Escape, and overlay-specific shortcuts to callers.
func (m *Model) editText(field string, value *string, msg tea.KeyPressMsg) bool {
	m.textInput.begin(field, *value)
	editor := &m.textInput

	switch msg.String() {
	case "left":
		if editor.cursor > 0 {
			_, size := utf8.DecodeLastRuneInString((*value)[:editor.cursor])
			editor.cursor -= size
		}
	case "right":
		if editor.cursor < len(*value) {
			_, size := utf8.DecodeRuneInString((*value)[editor.cursor:])
			editor.cursor += size
		}
	case "home", "ctrl+a":
		editor.cursor = 0
	case "end", "ctrl+e":
		editor.cursor = len(*value)
	case "backspace":
		if editor.cursor > 0 {
			_, size := utf8.DecodeLastRuneInString((*value)[:editor.cursor])
			start := editor.cursor - size
			*value = (*value)[:start] + (*value)[editor.cursor:]
			editor.cursor = start
		}
	case "delete":
		if editor.cursor < len(*value) {
			_, size := utf8.DecodeRuneInString((*value)[editor.cursor:])
			*value = (*value)[:editor.cursor] + (*value)[editor.cursor+size:]
		}
	case "ctrl+w":
		start := strings.TrimRight((*value)[:editor.cursor], " \t")
		start = strings.TrimRightFunc(start, func(r rune) bool { return r != ' ' && r != '\t' })
		*value = start + (*value)[editor.cursor:]
		editor.cursor = len(start)
	case "ctrl+u":
		*value = (*value)[editor.cursor:]
		editor.cursor = 0
	default:
		if msg.Text == "" {
			return false
		}
		// physicalKey matched the key by its position; type what the
		// keyboard layout typed, so non-Latin text still works here.
		text := msg.Text
		if m.layoutText != "" {
			text = m.layoutText
		}
		*value = (*value)[:editor.cursor] + text + (*value)[editor.cursor:]
		editor.cursor += len(text)
	}
	return true
}

func (m *Model) insertText(field string, value *string, text string) {
	m.textInput.begin(field, *value)
	*value = (*value)[:m.textInput.cursor] + text + (*value)[m.textInput.cursor:]
	m.textInput.cursor += len(text)
}

// textWithCursor renders the shared editor cursor at its current rune boundary.
// Callers use this in their prompt instead of assuming text always appends.
func (m Model) textWithCursor(field, value string) string {
	cursor := len(value)
	if m.textInput.field == field {
		cursor = min(m.textInput.cursor, len(value))
		for cursor > 0 && cursor < len(value) && !utf8.RuneStart(value[cursor]) {
			cursor--
		}
	}
	return value[:cursor] + "_" + value[cursor:]
}
