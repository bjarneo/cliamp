package model

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/luaplugin"
)

func TestLuaInputFiltersAndCompletesCommands(t *testing.T) {
	m := Model{}
	m.openLuaInput("Message", "", "", []luaplugin.SelectOption{
		{Title: "/start", Description: "Start"},
		{Title: "/settings", Description: "Settings"},
		{Title: "/help", Description: "Help"},
	}, nil, nil, nil, nil)

	if got := len(m.luaInput.filtered); got != 3 {
		t.Fatalf("filtered = %d, want 3", got)
	}
	m.luaInput.value = "/s"
	m.luaInput.filter()
	if got := len(m.luaInput.filtered); got != 2 {
		t.Fatalf("filtered for /s = %d, want 2", got)
	}
	m.luaInput.choice = 1
	m.handleLuaInputKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := m.luaInput.value; got != "/settings " {
		t.Fatalf("value after Tab = %q, want %q", got, "/settings ")
	}
}

func TestLuaInputHidesMenuForPlainText(t *testing.T) {
	m := Model{}
	m.openLuaInput("Message", "", "", []luaplugin.SelectOption{{Title: "/start"}}, nil, nil, nil, nil)
	m.luaInput.value = "get lucky"
	m.luaInput.filter()
	if got := len(m.luaInput.filtered); got != 0 {
		t.Fatalf("filtered = %d, want 0", got)
	}
}

func TestLuaInputSelectsLiveResult(t *testing.T) {
	m := Model{}
	selected := -1
	m.openLuaInput("Message", "", "", nil, nil, nil, func(index int) { selected = index }, nil)
	m.luaInput.suggestions = []luaplugin.SelectOption{
		{Title: "Get Lucky", Subtitle: "Artist: Daft Punk", Description: "Album: Random Access Memories"},
	}
	m.luaInput.live = true
	m.luaInput.filter()
	m.handleLuaInputKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if selected != 0 {
		t.Fatalf("selected = %d, want 0", selected)
	}
}

func TestLuaInputRendersImageLines(t *testing.T) {
	m := Model{}
	m.openLuaInput("Message", "", "", nil, nil, nil, nil, nil)
	m.luaInput.live = true
	m.luaInput.suggestions = []luaplugin.SelectOption{{Title: "Get Lucky", Image: "IMG\nART"}}
	m.luaInput.filter()
	box := m.renderLuaInput()
	if !strings.Contains(box, "IMG") || !strings.Contains(box, "ART") {
		t.Fatalf("box does not contain the image lines:\n%s", box)
	}
}

func TestLuaInputSuggestionsKeepImage(t *testing.T) {
	m := Model{}
	m.openLuaInput("Message", "", "", nil, nil, nil, nil, nil)
	next, _ := m.Update(LuaInputSuggestionsMsg{
		Options: []luaplugin.SelectOption{{Title: "Get Lucky", Image: "IMG\nART"}},
		Live:    true,
	})
	got := next.(Model)
	if len(got.luaInput.filtered) != 1 || got.luaInput.filtered[0].Image != "IMG\nART" {
		t.Fatalf("input = %#v", got.luaInput)
	}
	box := got.renderLuaInput()
	if !strings.Contains(box, "IMG") || !strings.Contains(box, "ART") {
		t.Fatalf("box does not contain the image lines:\n%s", box)
	}
}

func TestPasteGoesToOpenLuaInput(t *testing.T) {
	m := Model{}
	m.openLuaInput("Message", "", "", nil, nil, nil, nil, nil)
	m.handlePaste("daft punk")
	if m.luaInput == nil || m.luaInput.value != "daft punk" {
		t.Fatalf("input = %#v, want pasted text", m.luaInput)
	}
}
