package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/playlist"
)

func TestCtrlCQuitsOverPluginModals(t *testing.T) {
	cases := []struct {
		name string
		open func(*Model)
	}{
		{"action pane", func(m *Model) { m.actionPane.visible = true }},
		{"lua text", func(m *Model) { m.openLuaText("Reply", "body", nil) }},
		{"lua input", func(m *Model) { m.openLuaInput("Message", "", "", nil, nil, nil, nil, nil) }},
		{"lua select", func(m *Model) {
			m.luaSelect = &luaSelectState{title: "Pick", options: []luaplugin.SelectOption{{Title: "One"}}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Model{player: &playbackFakeEngine{}, playlist: playlist.New()}
			tc.open(&m)
			if cmd := m.handleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); cmd == nil || !m.quitting {
				t.Fatal("Ctrl+C did not quit")
			}
		})
	}
}
