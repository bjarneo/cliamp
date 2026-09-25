package luaplugin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bjarneo/cliamp/internal/plugintrust"
)

// loadReservedTestPlugin loads one trusted plugin through New(), the same path
// main.go uses, so bind() runs while the Manager is being built.
func loadReservedTestPlugin(t *testing.T, src string) *Manager {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLIAMP_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "") // appdir checks it before HOME
	dir := filepath.Join(home, ".config", "cliamp", "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "kb.lua")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := plugintrust.Approve(dir, "kb", path); err != nil {
		t.Fatalf("approve: %v", err)
	}

	m, err := New(nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(m.Close)
	if len(m.plugins) != 1 {
		t.Fatalf("loaded %d plugins, want only the test plugin", len(m.plugins))
	}
	return m
}

// Plugins bind from their top-level chunk, which runs inside New(). The
// reserved set must already apply then; SetReservedKeys is only called after
// New() returns, too late for those binds.
func TestDefaultReservedKeysApplyDuringLoad(t *testing.T) {
	t.Cleanup(func() { SetDefaultReservedKeys(nil) })
	SetDefaultReservedKeys(map[string]bool{"x": true})

	m := loadReservedTestPlugin(t, `
		local p = plugin.register({name = "kb", type = "hook", permissions = {"keymap"}})
		p:bind("x", function() end)
	`)

	if !m.reservedKeys["x"] {
		t.Fatal("Manager from New() did not pick up the default reserved keys")
	}
	if m.EmitKey("x") {
		t.Error("a binding was registered on a reserved key during load")
	}
}

func TestSetDefaultReservedKeysCopies(t *testing.T) {
	t.Cleanup(func() { SetDefaultReservedKeys(nil) })

	src := map[string]bool{"a": true}
	SetDefaultReservedKeys(src)
	src["b"] = true // must not leak into the stored defaults

	got := initialReservedKeys()
	if got["b"] {
		t.Error("SetDefaultReservedKeys retained the caller's map")
	}
	if !got["a"] {
		t.Error("stored defaults lost an entry")
	}
}
