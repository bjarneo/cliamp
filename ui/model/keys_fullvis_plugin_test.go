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

// newKeyTestPlugin loads a plugin that reports the key it was given.
func newKeyTestPlugin(t *testing.T, key string) (*luaplugin.Manager, <-chan string) {
	t.Helper()
	configDir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", configDir)
	pluginDir := filepath.Join(configDir, "plugins")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("creating the plugin directory: %v", err)
	}
	pluginPath := filepath.Join(pluginDir, "key-spy.lua")
	script := `
local p = plugin.register({name = "key-spy", type = "hook", permissions = {"keymap"}})
p:bind("` + key + `", "spy", function() cliamp.message("pressed") end)
`
	if err := os.WriteFile(pluginPath, []byte(script), 0o644); err != nil {
		t.Fatalf("writing the test plugin: %v", err)
	}
	if _, err := plugintrust.Approve(pluginDir, "key-spy", pluginPath); err != nil {
		t.Fatalf("approving the test plugin: %v", err)
	}
	mgr, err := luaplugin.New(nil, nil)
	if err != nil {
		t.Fatalf("loading plugins: %v", err)
	}
	t.Cleanup(sync.OnceFunc(mgr.Close))

	pressed := make(chan string, 4)
	ctx := t.Context()
	mgr.SetUIProvider(luaplugin.UIProvider{
		ShowMessage: func(text string, _ time.Duration) {
			select {
			case pressed <- text:
			case <-ctx.Done():
			}
		},
	})
	return mgr, pressed
}

// The full-screen visualizer is where a plugin visualizer is actually
// watched, so the plugin's own keys have to keep working there.
func TestFullVisualizerForwardsUnhandledKeysToPlugins(t *testing.T) {
	mgr, pressed := newKeyTestPlugin(t, "H")
	m := Model{luaMgr: mgr}

	m.handleFullVisualizerKey(tea.KeyPressMsg{Code: 'H', Text: "H", Mod: tea.ModShift})

	select {
	case <-pressed:
	case <-time.After(2 * time.Second):
		t.Fatal("the plugin never saw the key")
	}
}
