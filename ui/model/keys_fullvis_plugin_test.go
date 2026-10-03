package model

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/bjarneo/cliamp/internal/plugintrust"
	"github.com/bjarneo/cliamp/luaplugin"
)

// newKeyTestPlugin loads a plugin that reports the key it was given. The
// manager reserves the core keys, as main.go does.
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
	mgr, err := luaplugin.New(nil, nil, ReservedKeys())
	if err != nil {
		t.Fatalf("loading plugins: %v", err)
	}
	t.Cleanup(mgr.Close)

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
	// Bare letters are not plugin keys: lowercase belongs to the core and
	// uppercase to providers (#547). Use a key a plugin can own.
	mgr, pressed := newKeyTestPlugin(t, "alt+h")
	m := Model{luaMgr: mgr}

	m.handleFullVisualizerKey(tea.KeyPressMsg{Code: 'h', Mod: tea.ModAlt})

	select {
	case <-pressed:
	case <-time.After(2 * time.Second):
		t.Fatal("the plugin never saw the key")
	}
}

// The core owns F and ctrl+r, so a plugin bind of either is refused and the
// key handlers have nothing to forward.
func TestPluginsCannotBindCoreKeys(t *testing.T) {
	for _, key := range []string{"F", "ctrl+r"} {
		t.Run(key, func(t *testing.T) {
			mgr, _ := newKeyTestPlugin(t, key)
			if got := mgr.KeyBindings(); len(got) != 0 {
				t.Fatalf("KeyBindings = %+v, want the bind refused", got)
			}
		})
	}
}
