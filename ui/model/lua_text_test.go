package model

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestLuaTextKeepsLinesAndScrolls(t *testing.T) {
	m := Model{height: 12}
	m.openLuaText("Reply", "line one\nline two\nline three\nline four\nline five", nil)
	if got := m.luaText.lines[0]; got != "line one" {
		t.Fatalf("first line = %q", got)
	}
	m.handleLuaTextKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m.handleLuaTextKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.luaText.scroll != 2 {
		t.Fatalf("scroll = %d, want 2", m.luaText.scroll)
	}
	m.handleLuaTextKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.luaText != nil {
		t.Fatal("esc did not close the panel")
	}
}

func TestWrapBlockKeepsBlankLines(t *testing.T) {
	lines := wrapBlock("one\n\ntwo", 20)
	if strings.Join(lines, "|") != "one||two" {
		t.Fatalf("lines = %#v", lines)
	}
}

func TestLuaTextReplacementClosesPrevious(t *testing.T) {
	m := Model{}
	closed := false
	m.openLuaText("First", "one", func() { closed = true })
	next, _ := m.Update(LuaTextMsg{Title: "Second", Text: "two"})
	got := next.(Model)
	if !closed {
		t.Fatal("replacing the panel did not call the previous on_close")
	}
	if got.luaText == nil || got.luaText.title != "Second" {
		t.Fatalf("panel = %#v", got.luaText)
	}
}
