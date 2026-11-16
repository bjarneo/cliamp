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

func TestLuaInputCursorAfterTab(t *testing.T) {
	m := Model{}
	m.openLuaInput("Message", "", "", []luaplugin.SelectOption{{Title: "/help", Description: "Help"}}, nil, nil, nil, nil)
	m.luaInput.value = "/h"
	m.luaInput.filter()
	m.luaInput.choice = 0
	m.handleLuaInputKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m.handleLuaInputKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if got := m.luaInput.value; got != "/help x" {
		t.Fatalf("value = %q, want %q", got, "/help x")
	}
}

func TestLuaInputCursorOnPrefilledValue(t *testing.T) {
	m := Model{}
	m.openLuaInput("Message", "", "abc", nil, nil, nil, nil, nil)
	m.handleLuaInputKey(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if got := m.luaInput.value; got != "abcd" {
		t.Fatalf("value = %q, want %q", got, "abcd")
	}
}

func TestPasteIgnoredUnderTextPanel(t *testing.T) {
	m := Model{}
	m.openLuaText("Reply", "body", nil)
	m.openLuaInput("Message", "", "", nil, nil, nil, nil, nil)
	m.handlePaste("hidden")
	if got := m.luaInput.value; got != "" {
		t.Fatalf("value = %q, want empty", got)
	}
}

func TestLuaInputLiveRowsAreOneLine(t *testing.T) {
	m := Model{}
	m.openLuaInput("Message", "", "", nil, nil, nil, nil, nil)
	m.luaInput.live = true
	m.luaInput.suggestions = []luaplugin.SelectOption{{Title: "one\ntwo"}}
	m.luaInput.filter()
	box := m.renderLuaInput()
	if strings.Contains(box, "one\ntwo") {
		t.Fatalf("live row kept a newline:\n%s", box)
	}
}

func TestLuaInputTabNotifiesChange(t *testing.T) {
	var got string
	m := Model{}
	m.openLuaInput("Message", "", "", []luaplugin.SelectOption{{Title: "/help", Description: "Help"}}, func(text string) {
		got = text
	}, nil, nil, nil)
	m.luaInput.value = "/h"
	m.luaInput.filter()
	m.luaInput.choice = 0
	m.handleLuaInputKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if got != "/help " {
		t.Fatalf("onChange = %q, want %q", got, "/help ")
	}
}

func TestLuaInputCapsTallResultImage(t *testing.T) {
	m := Model{height: 24}
	m.openLuaInput("Message", "", "", nil, nil, nil, nil, nil)
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "IMG"
	}
	m.luaInput.live = true
	m.luaInput.suggestions = []luaplugin.SelectOption{{Title: "Title", Image: strings.Join(lines, "\n")}}
	m.luaInput.filter()
	var b strings.Builder
	m.renderLiveInputRows(&b, m.luaInput, 40)
	out := b.String()
	if strings.Count(out, "IMG") > 9 {
		t.Fatalf("image was not capped:\n%s", out)
	}
	if !strings.Contains(out, "Title") {
		t.Fatalf("selected title missing:\n%s", out)
	}
}
