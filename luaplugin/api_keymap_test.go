package luaplugin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"

	"github.com/bjarneo/cliamp/internal/plugintrust"
)

func TestPluginBindAndEmit(t *testing.T) {
	m := newTestManager()
	m.reservedKeys = map[string]bool{"q": true, "ctrl+c": true}

	var fired atomic.Int64
	p := loadTestPlugin(t, m, "kb", `
		local p = plugin.register({name = "kb", type = "hook", permissions = {"keymap"}})
		_G.result = p:bind("X", function(key) bump() end)
	`)
	if p == nil {
		t.Fatal("plugin failed to load")
	}

	p.mu.Lock()
	p.L.SetGlobal("bump", p.L.NewFunction(func(L *lua.LState) int {
		fired.Add(1)
		return 0
	}))
	p.mu.Unlock()

	if ok := m.EmitKey("x"); !ok {
		t.Fatal("EmitKey returned false for bound key")
	}

	waitAtomic(t, &fired, 1, 2*time.Second)
}

func TestPluginBindRejectsReservedKey(t *testing.T) {
	m := newTestManager()
	m.reservedKeys = map[string]bool{"q": true}

	p := loadTestPlugin(t, m, "kb", `
		local p = plugin.register({name = "kb", type = "hook", permissions = {"keymap"}})
		_G.ok, _G.err = p:bind("q", function() end)
	`)
	if p == nil {
		t.Fatal("plugin failed to load")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.L.GetGlobal("ok") != lua.LFalse {
		t.Fatal("expected ok=false for reserved key")
	}
	if s := p.L.GetGlobal("err").String(); !strings.Contains(s, "reserved") {
		t.Fatalf("err = %q, want reserved", s)
	}
}

func TestPluginBindRequiresKeymapPermission(t *testing.T) {
	m := newTestManager()

	p := loadTestPlugin(t, m, "kb", `
		local p = plugin.register({name = "kb", type = "hook"})
		_G.ok, _G.err = p:bind("x", function() end)
	`)
	if p == nil {
		t.Fatal("plugin failed to load")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.L.GetGlobal("ok") != lua.LFalse {
		t.Fatal("expected ok=false without permission")
	}
}

func TestEmitKeyUnboundReturnsFalse(t *testing.T) {
	m := newTestManager()
	if m.EmitKey("nothing-bound-here") {
		t.Fatal("EmitKey should return false when nothing is bound")
	}
}

func TestPluginUnbind(t *testing.T) {
	m := newTestManager()

	p := loadTestPlugin(t, m, "kb", `
		local p = plugin.register({name = "kb", type = "hook", permissions = {"keymap"}})
		p:bind("x", function() end)
		p:unbind("x")
	`)
	if p == nil {
		t.Fatal("plugin failed to load")
	}
	if m.EmitKey("x") {
		t.Fatal("key should be unbound")
	}
}

func TestPluginBindWithDescriptionSurfacesInKeyBindings(t *testing.T) {
	m := newTestManager()

	loadTestPlugin(t, m, "kb", `
		local p = plugin.register({name = "kb", type = "hook", permissions = {"keymap"}})
		p:bind("x", "Extract chapters", function() end)
		p:bind("y", function() end)  -- no description → not surfaced
	`)

	bindings := m.KeyBindings()
	if len(bindings) != 1 {
		t.Fatalf("KeyBindings() returned %d entries, want 1: %+v", len(bindings), bindings)
	}
	if bindings[0].Key != "x" || bindings[0].Plugin != "kb" || bindings[0].Description != "Extract chapters" {
		t.Fatalf("unexpected binding: %+v", bindings[0])
	}
}

func TestKeyBindingsRemovedOnCleanup(t *testing.T) {
	m := newTestManager()
	p := loadTestPlugin(t, m, "kb", `
		local p = plugin.register({name = "kb", type = "hook", permissions = {"keymap"}})
		p:bind("x", "Do thing", function() end)
	`)
	if p == nil {
		t.Fatal("plugin failed to load")
	}
	if len(m.KeyBindings()) != 1 {
		t.Fatal("expected 1 binding before cleanup")
	}
	m.cleanupPlugin(p)
	if len(m.KeyBindings()) != 0 {
		t.Fatal("expected 0 bindings after cleanup")
	}
}

func TestCleanupPluginRemovesBinds(t *testing.T) {
	m := newTestManager()

	p := loadTestPlugin(t, m, "kb", `
		local p = plugin.register({name = "kb", type = "hook", permissions = {"keymap"}})
		p:bind("x", function() end)
	`)
	if p == nil {
		t.Fatal("plugin failed to load")
	}
	if !m.EmitKey("x") {
		t.Fatal("expected x to be bound")
	}
	m.cleanupPlugin(p)
	if m.EmitKey("x") {
		t.Fatal("bind should be removed when plugin is cleaned up")
	}
}

func TestCommandRegisterAndEmit(t *testing.T) {
	m := newTestManager()
	p := loadTestPlugin(t, m, "cmd", `
		local p = plugin.register({name = "cmd", type = "hook"})
		p:command("hello", function(args)
			return "hi " .. (args[1] or "world")
		end)
	`)
	if p == nil {
		t.Fatal("plugin failed to load")
	}

	out, err := m.EmitCommand(context.Background(), "cmd", "hello", []string{"friend"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "hi friend" {
		t.Fatalf("output = %q, want %q", out, "hi friend")
	}
}

func TestCommandNotFound(t *testing.T) {
	m := newTestManager()
	_, err := m.EmitCommand(context.Background(), "nope", "nope", nil)
	if err == nil {
		t.Fatal("expected error for unknown command")
	}
	if !strings.Contains(err.Error(), "no such") {
		t.Fatalf("err = %q", err)
	}
}

// A caller that ends ctx stops the command. runV2PluginJob passes the job
// context, so a canceled IPC job stops its Lua. Before, the Lua ran on for
// up to commandTimeout and held the plugin lock.
func TestEmitCommandStopsWhenContextEnds(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"loop", "while true do end"},
		{"sleep", "while true do cliamp.sleep(10) end"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestManager()
			t.Cleanup(m.Close)
			p := loadTestPlugin(t, m, "spin", `
				local p = plugin.register({name = "spin", type = "hook"})
				p:command("run", function() `+tt.body+` end)
			`)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cmdErr := make(chan error, 1)
			go func() {
				_, err := m.EmitCommand(ctx, "spin", "run", nil)
				cmdErr <- err
			}()
			// Wait until the command holds the plugin lock.
			deadline := time.Now().Add(time.Second)
			for p.mu.TryLock() {
				p.mu.Unlock()
				if time.Now().After(deadline) {
					t.Fatal("the command did not start")
				}
				time.Sleep(time.Millisecond)
			}

			cancel()
			select {
			case err := <-cmdErr:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("EmitCommand() error = %v, want %v", err, context.Canceled)
				}
			case <-time.After(time.Second):
				t.Fatal("EmitCommand did not return after the context ended")
			}
			// The Lua stopped, so the plugin lock is free again.
			deadline = time.Now().Add(time.Second)
			for !p.mu.TryLock() {
				if time.Now().After(deadline) {
					t.Fatal("the command still holds the plugin lock")
				}
				time.Sleep(time.Millisecond)
			}
			p.mu.Unlock()
		})
	}
}

func TestCommandListIncludesRegistered(t *testing.T) {
	m := newTestManager()
	loadTestPlugin(t, m, "cmd", `
		local p = plugin.register({name = "cmd", type = "hook"})
		p:command("a", function() end)
		p:command("b", function() end)
	`)
	list := m.CommandList()
	if len(list) != 2 {
		t.Fatalf("expected 2 commands, got %d: %v", len(list), list)
	}
}

func TestCleanupPluginRemovesCommands(t *testing.T) {
	m := newTestManager()
	loadTestPlugin(t, m, "keep", `
		local p = plugin.register({name = "keep", type = "hook"})
		p:command("a", function() end)
	`)
	p := loadTestPlugin(t, m, "cmd", `
		local p = plugin.register({name = "cmd", type = "hook"})
		p:command("a", function() end)
	`)
	if p == nil {
		t.Fatal("plugin failed to load")
	}
	m.cleanupPlugin(p)
	if got := m.CommandList(); !slices.Equal(got, []string{"keep a"}) {
		t.Fatalf("CommandList() = %v, want only the commands of the other plugin", got)
	}
}

// A plugin that fails to load because its name is taken must not remove the
// commands and key bindings of the plugin that owns the name.
func TestFailedPluginKeepsOtherPluginsCommands(t *testing.T) {
	m := newTestManager()
	loadTestPlugin(t, m, "a", `
		local p = plugin.register({name = "dup", type = "hook", permissions = {"keymap"}})
		p:command("hi", function() return "from a" end)
		p:bind("ctrl+y", "Say hi", function() end)
	`)
	for _, name := range []string{"b", "c"} {
		loadTestPluginExpectError(t, m, name, `
			local p = plugin.register({name = "dup", type = "hook", permissions = {"keymap"}})
			p:command("hi", function() return "from `+name+`" end)
			p:bind("ctrl+y", "Say hi", function() end)
		`)
	}

	out, err := m.EmitCommand(context.Background(), "dup", "hi", nil)
	if err != nil || out != "from a" {
		t.Errorf("EmitCommand() = %q, %v, want %q", out, err, "from a")
	}
	if got := m.CommandList(); !slices.Equal(got, []string{"dup hi"}) {
		t.Errorf("CommandList() = %v, want [dup hi]", got)
	}
	if got := m.KeyBindings(); len(got) != 1 || got[0].Plugin != "dup" || got[0].Description != "Say hi" {
		t.Errorf("KeyBindings() = %+v, want the binding of plugin a", got)
	}
}

func waitAtomic(t *testing.T, counter *atomic.Int64, target int64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if counter.Load() >= target {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("counter reached %d, want %d", counter.Load(), target)
}

// New installs the reserved keys before any plugin runs, so a bind in the
// top-level chunk of a plugin is refused like a later one.
func TestNewRefusesReservedKeyAtTopLevel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	pluginDir := filepath.Join(dir, "plugins")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(pluginDir, "kb.lua")
	src := `
		local p = plugin.register({name = "kb", type = "hook", permissions = {"keymap"}})
		_G.ok, _G.err = p:bind("space", "Toggle", function() end)
	`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := plugintrust.Approve(pluginDir, "kb", path); err != nil {
		t.Fatal(err)
	}

	m, err := New(nil, nil, map[string]bool{"space": true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Close()

	if m.EmitKey("space") || len(m.KeyBindings()) != 0 {
		t.Fatalf("space stayed bound, KeyBindings = %+v", m.KeyBindings())
	}
	p := m.plugins[0]
	p.mu.Lock()
	ok := p.L.GetGlobal("ok")
	p.mu.Unlock()
	if ok != lua.LFalse {
		t.Fatalf("p:bind(space) = %v, want false", ok)
	}
	logged, err := os.ReadFile(filepath.Join(dir, pluginLogName))
	if err != nil || !strings.Contains(string(logged), `refusing to bind "space"`) {
		t.Fatalf("plugin log = %q, %v, want the reserved key warning", logged, err)
	}
}
