package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

// Launching with nothing to play opens the provider browser (StartInProvider),
// so the empty playlist that opens the view must not also be what refuses to
// leave it.
func TestProviderEscapeLeavesWithEmptyPlaylist(t *testing.T) {
	tests := []struct {
		name  string
		key   tea.KeyPressMsg
		empty bool
	}{
		{"esc, empty playlist", tea.KeyPressMsg{Code: tea.KeyEscape}, true},
		{"esc, loaded playlist", tea.KeyPressMsg{Code: tea.KeyEscape}, false},
		{"backspace, empty playlist", tea.KeyPressMsg{Code: tea.KeyBackspace}, true},
		{"b, empty playlist", tea.KeyPressMsg{Text: "b"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := keybindingTestModel()
			m.provPane.lists = []playlist.PlaylistInfo{{ID: "a", Name: "Alpha"}}
			if !tc.empty {
				m.playlist.Replace([]playlist.Track{{Title: "T", Path: "/tmp/t.mp3"}})
			}
			m.focus = focusProvider

			m.handleKey(tc.key)

			if m.focus != focusPlaylist {
				t.Errorf("focus = %v, want focusPlaylist", m.focus)
			}
		})
	}
}
