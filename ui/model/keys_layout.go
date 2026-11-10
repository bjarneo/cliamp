package model

import (
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// keyboardEnhancements asks the terminal (Kitty keyboard protocol) to send
// every key as an escape code that carries its position on a US layout, which
// physicalKey needs to match commands from a non-Latin layout, and the text
// the key typed. Terminals without the protocol ignore the request and send
// keys as before. It asks for nothing more once layoutKeysOff is set.
func (m Model) keyboardEnhancements() tea.KeyboardEnhancements {
	if m.layoutKeysOff {
		return tea.KeyboardEnhancements{}
	}
	return tea.KeyboardEnhancements{
		ReportAlternateKeys:        true,
		ReportAllKeysAsEscapeCodes: true,
		ReportAssociatedText:       true,
	}
}

// handleKeyboardEnhancements turns the layout keys off when the terminal
// reports key positions but not the typed text. The key parser then fills the
// text of a shifted key with its US-layout position (Shift+A on AZERTY would
// type q), so keys are better left as the terminal sent them before.
func (m *Model) handleKeyboardEnhancements(msg tea.KeyboardEnhancementsMsg) {
	if msg.SupportsAlternateKeys() && !msg.SupportsAssociatedText() {
		m.layoutKeysOff = true
	}
}

// usShifted holds what the US layout types with Shift on its non-letter keys.
var usShifted = map[rune]rune{
	'`': '~', '1': '!', '2': '@', '3': '#', '4': '$', '5': '%', '6': '^',
	'7': '&', '8': '*', '9': '(', '0': ')', '-': '_', '=': '+',
	'[': '{', ']': '}', '\\': '|', ';': ':', '\'': '"', ',': '<', '.': '>', '/': '?',
}

// physicalKey lets the commands work from a keyboard layout whose keys do not
// type Latin text, such as Cyrillic, Greek, Arabic or Hebrew. When the terminal
// reports where the key sits on a US layout (BaseCode), a key that types such a
// character is matched by that position: й and ض are q, Shift+؟ is ?, and
// Ctrl+л is ctrl+k. layoutText is what the key typed, for text fields; it is
// empty when the key was not rewritten.
//
// Keys that type ASCII or a Latin letter keep their character, so Latin
// layouts like AZERTY, QWERTZ or Dvorak behave as before: a on AZERTY is still
// a, and so is Ctrl+a; é stays é.
func physicalKey(msg tea.KeyPressMsg) (key tea.KeyPressMsg, layoutText string) {
	base := msg.BaseCode
	if base == 0 || base >= utf8.RuneSelf || !unicode.IsPrint(base) || !unicode.IsPrint(msg.Code) {
		return msg, ""
	}
	if msg.Code < utf8.RuneSelf || unicode.Is(unicode.Latin, msg.Code) {
		// The layout's own character wins. Without the position, Keystroke
		// names the key by that character too, not by where it sits.
		msg.BaseCode = 0
		return msg, ""
	}

	layoutText = msg.Text
	msg.Code = base
	msg.ShiftedCode = 0
	if msg.Mod.Contains(tea.ModShift) {
		if shifted, ok := usShifted[base]; ok {
			msg.ShiftedCode = shifted
		} else {
			msg.ShiftedCode = unicode.ToUpper(base)
		}
	}
	if msg.Text != "" {
		msg.Text = string(base)
		if msg.ShiftedCode != 0 {
			msg.Text = string(msg.ShiftedCode)
		}
	}
	return msg, layoutText
}

// isModifierKey reports whether code is a modifier or lock key on its own.
// Terminals report those as key presses once every key is sent as an escape
// code, and none of them is a command.
func isModifierKey(code rune) bool {
	switch code {
	case tea.KeyCapsLock, tea.KeyScrollLock, tea.KeyNumLock,
		tea.KeyLeftShift, tea.KeyLeftAlt, tea.KeyLeftCtrl, tea.KeyLeftSuper, tea.KeyLeftHyper, tea.KeyLeftMeta,
		tea.KeyRightShift, tea.KeyRightAlt, tea.KeyRightCtrl, tea.KeyRightSuper, tea.KeyRightHyper, tea.KeyRightMeta,
		tea.KeyIsoLevel3Shift, tea.KeyIsoLevel5Shift:
		return true
	}
	return false
}
