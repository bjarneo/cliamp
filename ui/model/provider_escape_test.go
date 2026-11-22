package model

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

// StartInProvider opens the provider browser on an empty launch when a default
// provider is set. The empty playlist that opens the view must not also be what
// refuses to leave it.
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

func TestEscFromPlaylistWithoutProviderStaysOnQueue(t *testing.T) {
	m := keybindingTestModel()
	m.provider = nil
	m.focus = focusPlaylist

	m.handleMainKey(tea.KeyPressMsg{Code: tea.KeyEscape})

	if m.focus != focusPlaylist {
		t.Fatalf("focus = %v, want focusPlaylist", m.focus)
	}
	_ = m.View()
}

func TestMainHelpBarHidesEscBeforeProviderVisit(t *testing.T) {
	m := keybindingTestModel()
	m.layout.panelWidth = 120
	m.focus = focusPlaylist
	help := m.helpBar(commandModeMain)
	if strings.Contains(help, "Esc") {
		t.Fatalf("help before provider visit should not list Esc: %q", help)
	}
	m.noteProviderPaneVisit()
	help = m.helpBar(commandModeMain)
	if !strings.Contains(help, "Esc") {
		t.Fatalf("help after provider visit should list Esc: %q", help)
	}
}
