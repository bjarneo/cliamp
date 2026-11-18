package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The keys below are what Bubbletea decodes from a Kitty keyboard protocol
// terminal with the alternate keys, all keys as escape codes and associated
// text flags on, e.g. "\x1b[1081:1049:113;2;1049u" for Shift+Й on a Russian
// layout. The parser copies BaseCode into ShiftedCode when both are sent, so
// ShiftedCode holds the US-layout key here too.
var (
	ruQ      = tea.KeyPressMsg{Code: 'й', ShiftedCode: 'q', BaseCode: 'q', Text: "й"}
	ruShiftQ = tea.KeyPressMsg{Code: 'й', ShiftedCode: 'q', BaseCode: 'q', Mod: tea.ModShift, Text: "Й"}
	ruShiftY = tea.KeyPressMsg{Code: 'н', ShiftedCode: 'y', BaseCode: 'y', Mod: tea.ModShift, Text: "Н"}
	ruCtrlK  = tea.KeyPressMsg{Code: 'л', ShiftedCode: 'k', BaseCode: 'k', Mod: tea.ModCtrl}
	ruCtrlA  = tea.KeyPressMsg{Code: 'ф', ShiftedCode: 'a', BaseCode: 'a', Mod: tea.ModCtrl}
	ruW      = tea.KeyPressMsg{Code: 'ц', ShiftedCode: 'w', BaseCode: 'w', Text: "ц"}
	arQ      = tea.KeyPressMsg{Code: 'ض', ShiftedCode: 'q', BaseCode: 'q', Text: "ض"}
	arQMark  = tea.KeyPressMsg{Code: 'ظ', ShiftedCode: '/', BaseCode: '/', Mod: tea.ModShift, Text: "؟"}
)

func TestPhysicalKey(t *testing.T) {
	for _, tt := range []struct {
		name     string
		key      tea.KeyPressMsg
		want     string
		wantText string
	}{
		{"Cyrillic letter is its US key", ruQ, "q", "й"},
		{"Cyrillic Shift+letter is the US capital", ruShiftQ, "Q", "Й"},
		{"Cyrillic letter with CapsLock is the US capital", tea.KeyPressMsg{Code: 'й', ShiftedCode: 'q', BaseCode: 'q', Mod: tea.ModCapsLock, Text: "Й"}, "Q", "Й"},
		{"Cyrillic Shift+letter with CapsLock is the US letter", tea.KeyPressMsg{Code: 'й', ShiftedCode: 'q', BaseCode: 'q', Mod: tea.ModShift | tea.ModCapsLock, Text: "й"}, "q", "й"},
		{"Arabic question mark with CapsLock is ?", tea.KeyPressMsg{Code: 'ظ', ShiftedCode: '/', BaseCode: '/', Mod: tea.ModShift | tea.ModCapsLock, Text: "؟"}, "?", "؟"},
		{"Ctrl with a Cyrillic letter", ruCtrlK, "ctrl+k", ""},
		{"Alt with a Cyrillic letter", tea.KeyPressMsg{Code: 'у', ShiftedCode: 'e', BaseCode: 'e', Mod: tea.ModAlt}, "alt+e", ""},
		{"Arabic letter is its US key", arQ, "q", "ض"},
		{"Arabic question mark is ?", arQMark, "?", "؟"},
		{"AZERTY a stays a", tea.KeyPressMsg{Code: 'a', ShiftedCode: 'q', BaseCode: 'q', Text: "a"}, "a", ""},
		{"AZERTY Shift+, stays ?", tea.KeyPressMsg{Code: ',', ShiftedCode: 'm', BaseCode: 'm', Mod: tea.ModShift, Text: "?"}, "?", ""},
		{"AZERTY Ctrl+z stays ctrl+z", tea.KeyPressMsg{Code: 'z', ShiftedCode: 'w', BaseCode: 'w', Mod: tea.ModCtrl}, "ctrl+z", ""},
		{"AZERTY é stays é", tea.KeyPressMsg{Code: 'é', ShiftedCode: '2', BaseCode: '2', Text: "é"}, "é", ""},
		{"QWERTZ Ctrl+ü stays ctrl+ü", tea.KeyPressMsg{Code: 'ü', ShiftedCode: '[', BaseCode: '[', Mod: tea.ModCtrl}, "ctrl+ü", ""},
		{"Greek letter is its US key", tea.KeyPressMsg{Code: 'ς', ShiftedCode: 'w', BaseCode: 'w', Text: "ς"}, "w", "ς"},
		{"Russian Shift+. key stays ,", tea.KeyPressMsg{Code: '.', ShiftedCode: '/', BaseCode: '/', Mod: tea.ModShift, Text: ","}, ",", ""},
		{"US key without a position", tea.KeyPressMsg{Code: 'q', Text: "q"}, "q", ""},
		{"no position reported", tea.KeyPressMsg{Code: 'й', Text: "й"}, "й", ""},
		{"special key", tea.KeyPressMsg{Code: tea.KeyEnter}, "enter", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, text := physicalKey(tt.key)
			if got.String() != tt.want || text != tt.wantText {
				t.Fatalf("physicalKey = %q, %q; want %q, %q", got.String(), text, tt.want, tt.wantText)
			}
		})
	}
}

func TestHandleKeyRunsCommandsFromNonLatinLayout(t *testing.T) {
	t.Run("й quits", func(t *testing.T) {
		m := keybindingTestModel()
		m.handleKey(ruQ)
		if !m.quitting {
			t.Fatal("й (the q key) did not quit")
		}
	})
	t.Run("й without a key position does nothing", func(t *testing.T) {
		m := keybindingTestModel()
		m.handleKey(tea.KeyPressMsg{Code: 'й', Text: "й"})
		if m.quitting {
			t.Fatal("й quit although the terminal sent no key position")
		}
	})
	t.Run("Shift+Н selects YouTube", func(t *testing.T) {
		m := keybindingTestModel()
		m.handleKey(ruShiftY)
		if got := m.provider.Name(); got != "YouTube" {
			t.Fatalf("active provider = %q, want YouTube", got)
		}
	})
	t.Run("Ctrl+л opens the keymap", func(t *testing.T) {
		m := keybindingTestModel()
		m.handleKey(ruCtrlK)
		if !m.keymap.visible {
			t.Fatal("Ctrl+л (Ctrl+K) did not open the keymap")
		}
	})
	t.Run("Arabic ؟ opens the keymap", func(t *testing.T) {
		m := keybindingTestModel()
		m.handleKey(arQMark)
		if !m.keymap.visible {
			t.Fatal("؟ (Shift+/) did not open the keymap")
		}
	})
}

func TestHandleKeyTypesLayoutTextInTextInputs(t *testing.T) {
	m := keybindingTestModel()
	m.search.active = true

	for _, key := range []tea.KeyPressMsg{ruQ, ruShiftQ, arQ, arQMark} {
		if cmd := m.handleKey(key); cmd != nil {
			t.Fatalf("typing %q scheduled a command", key.Text)
		}
	}
	if got := m.search.query; got != "йЙض؟" {
		t.Fatalf("search query = %q, want йЙض؟", got)
	}

	// Editing keys still work by position: Ctrl+ф is Ctrl+A (start of line).
	m.handleKey(ruCtrlA)
	m.handleKey(ruW)
	if got := m.search.query; got != "цйЙض؟" {
		t.Fatalf("search query = %q, want цйЙض؟", got)
	}
	if m.quitting || m.layoutText != "" {
		t.Fatalf("quitting = %v, layoutText = %q after typing", m.quitting, m.layoutText)
	}
}

func TestHandleKeyIgnoresModifierKeysAlone(t *testing.T) {
	m := keybindingTestModel()
	m.focus = focusProvider
	m.provPane.askLoc = true

	for _, code := range []rune{tea.KeyLeftShift, tea.KeyRightShift, tea.KeyLeftCtrl, tea.KeyLeftAlt, tea.KeyCapsLock, tea.KeyIsoLevel3Shift} {
		m.handleKey(tea.KeyPressMsg{Code: code})
	}

	if m.status.text != "" || !m.provPane.askLoc {
		t.Fatalf("a modifier key alone answered the prompt: status = %q, askLoc = %v", m.status.text, m.provPane.askLoc)
	}
}

func TestKeyboardEnhancements(t *testing.T) {
	want := tea.KeyboardEnhancements{ReportAlternateKeys: true, ReportAllKeysAsEscapeCodes: true, ReportAssociatedText: true}
	base := ansi.KittyDisambiguateEscapeCodes | ansi.KittyReportAlternateKeys | ansi.KittyReportAllKeysAsEscapeCodes

	for _, tt := range []struct {
		name  string
		flags int
		want  tea.KeyboardEnhancements
	}{
		{"all flags on", base | ansi.KittyReportAssociatedKeys, want},
		{"no associated text", base, tea.KeyboardEnhancements{}},
		{"all keys without associated text", ansi.KittyDisambiguateEscapeCodes | ansi.KittyReportAllKeysAsEscapeCodes, tea.KeyboardEnhancements{}},
		{"alternate keys without associated text", ansi.KittyDisambiguateEscapeCodes | ansi.KittyReportAlternateKeys, tea.KeyboardEnhancements{}},
		{"no alternate keys", ansi.KittyDisambiguateEscapeCodes, want},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			if got := m.keyboardEnhancements(); got != want {
				t.Fatalf("before the reply: %+v, want %+v", got, want)
			}

			m.handleKeyboardEnhancements(tea.KeyboardEnhancementsMsg{Flags: tt.flags})

			if got := m.keyboardEnhancements(); got != tt.want {
				t.Fatalf("after flags %d: %+v, want %+v", tt.flags, got, tt.want)
			}
		})
	}
}
