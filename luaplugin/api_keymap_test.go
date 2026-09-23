package luaplugin

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// TestPluginBindAndEmit verifies that an uppercase binding matches lowercase input
// and invokes its registered callback.
func TestPluginBindAndEmit(t *testing.T) {
	m := newTestManager()
	m.SetReservedKeys(map[string]bool{"q": true, "ctrl+c": true})

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

// TestEmitKeyNormalizesLookup verifies that dispatch matches case and whitespace
// variants while preserving the original key string passed to callbacks.
func TestEmitKeyNormalizesLookup(t *testing.T) {
	for _, key := range []string{"h", "H", " H "} {
		// Each subtest checks lookup and callback input with an isolated plugin.
		t.Run(key, func(t *testing.T) {
			m := newTestManager()
			t.Cleanup(m.Close)
			p := loadTestPlugin(t, m, "kb", `
				local p = plugin.register({name = "kb", type = "hook", permissions = {"keymap"}})
				p:bind("H", function(key) report(key) end)
			`)
			if p == nil {
				t.Fatal("plugin failed to load")
			}

			received := make(chan string, 1)
			p.mu.Lock()
			// report captures the original key delivered to the Lua callback.
			p.L.SetGlobal("report", p.L.NewFunction(func(L *lua.LState) int {
				received <- L.CheckString(1)
				return 0
			}))
			p.mu.Unlock()

			if !m.EmitKey(key) {
				t.Fatalf("EmitKey(%q) returned false for bound key", key)
			}
			select {
			case got := <-received:
				if got != key {
					t.Fatalf("callback received %q, want original key %q", got, key)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("key callback did not run")
			}
		})
	}
}

// TestPluginBindRejectsReservedKey verifies that plugins cannot bind keys reserved
// by the core UI and receive an explanatory error.
func TestPluginBindRejectsReservedKey(t *testing.T) {
	m := newTestManager()
	m.SetReservedKeys(map[string]bool{"q": true})

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
	m.SetReservedKeys(map[string]bool{})

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
	m.SetReservedKeys(map[string]bool{})

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
	m.SetReservedKeys(map[string]bool{})
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
	m.SetReservedKeys(map[string]bool{})

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

	out, err := m.EmitCommand("cmd", "hello", []string{"friend"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "hi friend" {
		t.Fatalf("output = %q, want %q", out, "hi friend")
	}
}

func TestCommandNotFound(t *testing.T) {
	m := newTestManager()
	_, err := m.EmitCommand("nope", "nope", nil)
	if err == nil {
		t.Fatal("expected error for unknown command")
	}
	if !strings.Contains(err.Error(), "no such") {
		t.Fatalf("err = %q", err)
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
	p := loadTestPlugin(t, m, "cmd", `
		local p = plugin.register({name = "cmd", type = "hook"})
		p:command("a", function() end)
	`)
	if p == nil {
		t.Fatal("plugin failed to load")
	}
	m.cleanupPlugin(p)
	if len(m.CommandList()) != 0 {
		t.Fatal("commands should be removed when plugin is cleaned up")
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
