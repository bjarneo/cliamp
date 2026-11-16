package model

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/plugintrust"
	"github.com/bjarneo/cliamp/luaplugin"
)

func TestActionPaneRunsPluginActionWithInput(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", configDir)
	pluginDir := filepath.Join(configDir, "plugins")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pluginPath := filepath.Join(pluginDir, "action-spy.lua")
	script := `
local p = plugin.register({
    name = "action-spy",
    type = "hook",
    permissions = {"ui"},
})
p:action{
    key = "!",
    id = "say",
    label = "Say something",
    prompt = "Message",
    on_submit = function(text)
        cliamp.message("got:" .. text)
    end,
}
`
	if err := os.WriteFile(pluginPath, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := plugintrust.Approve(pluginDir, "action-spy", pluginPath); err != nil {
		t.Fatal(err)
	}
	mgr, err := luaplugin.New(nil, nil, ReservedKeys())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sync.OnceFunc(mgr.Close))

	messages := make(chan string, 1)
	mgr.SetUIProvider(luaplugin.UIProvider{
		ShowMessage: func(text string, _ time.Duration) { messages <- text },
	})

	t.Logf("actions: %#v", mgr.Actions("!"))
	m := Model{luaMgr: mgr}
	m.openActionPane()
	if !m.actionPane.visible || len(m.actionPane.filtered) != 1 {
		t.Fatalf("pane = %#v", m.actionPane)
	}
	m.handleActionPaneKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.luaInput == nil {
		t.Fatal("enter did not open the prompt")
	}
	m.luaInput.value = "hello"
	m.handleLuaInputKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	select {
	case got := <-messages:
		if got != "got:hello" {
			t.Fatalf("message = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the action callback never ran")
	}
}

func TestActionPaneFiltersByLabel(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", configDir)
	pluginDir := filepath.Join(configDir, "plugins")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pluginPath := filepath.Join(pluginDir, "action-list.lua")
	script := `
local p = plugin.register({name = "action-list", type = "hook", permissions = {"ui"}})
for _, label in ipairs({"DeezLoad", "Other bot"}) do
    p:action{key = "!", label = label, on_submit = function() end}
end
`
	if err := os.WriteFile(pluginPath, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := plugintrust.Approve(pluginDir, "action-list", pluginPath); err != nil {
		t.Fatal(err)
	}
	mgr, err := luaplugin.New(nil, nil, ReservedKeys())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sync.OnceFunc(mgr.Close))

	m := Model{luaMgr: mgr}
	m.openActionPane()
	if got := len(m.actionPane.filtered); got != 2 {
		t.Fatalf("filtered = %d, want 2", got)
	}
	m.actionPane.query = "deez"
	m.filterActions()
	if got := len(m.actionPane.filtered); got != 1 || m.actionPane.filtered[0].Label != "DeezLoad" {
		t.Fatalf("filtered = %#v", m.actionPane.filtered)
	}
}

func TestActionPaneFilterKeepsEntries(t *testing.T) {
	m := Model{}
	m.actionPane = actionPaneState{
		entries: []luaplugin.Action{
			{Label: "Alpha"},
			{Label: "Beta"},
			{Label: "Gamma"},
		},
	}
	m.actionPane.filtered = m.actionPane.entries
	m.actionPane.query = "beta"
	m.filterActions()
	if len(m.actionPane.filtered) != 1 || m.actionPane.filtered[0].Label != "Beta" {
		t.Fatalf("filtered = %#v", m.actionPane.filtered)
	}
	m.actionPane.query = ""
	m.filterActions()
	if len(m.actionPane.filtered) != 3 || m.actionPane.filtered[0].Label != "Alpha" {
		t.Fatalf("cleared filter = %#v", m.actionPane.filtered)
	}
}

func TestActionPaneVisibleRowsFitsHeight(t *testing.T) {
	cases := map[int]int{0: 10, 10: 1, 15: 6, 55: 10}
	for height, want := range cases {
		pane := actionPaneState{height: height}
		if got := pane.visibleRows(); got != want {
			t.Fatalf("height %d: visibleRows = %d, want %d", height, got, want)
		}
	}
}
