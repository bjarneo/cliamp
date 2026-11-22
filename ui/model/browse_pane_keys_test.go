package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestBrowsePaneKeysFromMainFocuses(t *testing.T) {
	local := commandsTestProvider{name: "Local"}
	for _, tc := range []struct {
		name  string
		focus focusArea
		key   string
		want  func(*Model) bool
	}{
		{"playlist o", focusPlaylist, "o", func(m *Model) bool { return m.fileBrowser.visible }},
		{"playlist p", focusPlaylist, "p", func(m *Model) bool { return m.plManager.visible }},
		{"source o", focusProvPill, "o", func(m *Model) bool { return m.fileBrowser.visible }},
		{"source p", focusProvPill, "p", func(m *Model) bool { return m.plManager.visible }},
		{"volume o", focusVolume, "o", func(m *Model) bool { return m.fileBrowser.visible }},
		{"volume p", focusVolume, "p", func(m *Model) bool { return m.plManager.visible }},
		{"eq o", focusEQ, "o", func(m *Model) bool { return m.fileBrowser.visible }},
		{"eq p", focusEQ, "p", func(m *Model) bool { return m.plManager.visible }},
		{"shuffle o", focusShuffle, "o", func(m *Model) bool { return m.fileBrowser.visible }},
		{"shuffle p", focusShuffle, "p", func(m *Model) bool { return m.plManager.visible }},
		{"repeat o", focusRepeat, "o", func(m *Model) bool { return m.fileBrowser.visible }},
		{"repeat p", focusRepeat, "p", func(m *Model) bool { return m.plManager.visible }},
		{"speed o", focusSpeed, "o", func(m *Model) bool { return m.fileBrowser.visible }},
		{"speed p", focusSpeed, "p", func(m *Model) bool { return m.plManager.visible }},
		{"provider o", focusProvider, "o", func(m *Model) bool { return m.fileBrowser.visible }},
		{"provider p", focusProvider, "p", func(m *Model) bool { return m.plManager.visible }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := keybindingTestModel()
			m.localProvider = local
			m.focus = tc.focus
			m.handleKey(tea.KeyPressMsg{Text: tc.key})
			if !tc.want(&m) {
				t.Fatalf("%s with %q did not open the expected overlay", tc.focus.label(), tc.key)
			}
		})
	}
}

func TestFileBrowserPOpensPlaylistManager(t *testing.T) {
	local := commandsTestProvider{name: "Local"}
	m := keybindingTestModel()
	m.localProvider = local
	m.openFileBrowser()
	if !m.fileBrowser.visible {
		t.Fatal("file browser did not open")
	}
	m.handleFileBrowserKey(tea.KeyPressMsg{Text: "p"})
	if m.fileBrowser.visible {
		t.Fatal("file browser still open after p")
	}
	if !m.plManager.visible {
		t.Fatal("playlist manager did not open")
	}
}

func TestBrowsePanePRequiresLocalProvider(t *testing.T) {
	m := keybindingTestModel()
	m.focus = focusPlaylist
	m.localProvider = nil
	m.handleKey(tea.KeyPressMsg{Text: "p"})
	if m.plManager.visible {
		t.Fatal("p opened playlist manager without a local provider")
	}
}
