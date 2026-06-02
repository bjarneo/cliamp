package luaplugin

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBundledPluginsLoad is the backward-compatibility guard for the plugin
// API. It loads every first-party plugin shipped in the repo's plugins/
// directory through a real Manager and asserts they all register cleanly.
//
// Any change that renames or removes an existing cliamp.* function, event, or
// permission will break one of these plugins and fail here. Keep it green by
// only ever ADDING to the plugin surface, never altering the existing shape.
func TestBundledPluginsLoad(t *testing.T) {
	srcDir := filepath.Join("..", "plugins")
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		t.Fatalf("read bundled plugins dir: %v", err)
	}

	var luaFiles []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".lua" {
			luaFiles = append(luaFiles, e.Name())
		}
	}
	if len(luaFiles) == 0 {
		t.Fatal("no bundled .lua plugins found — expected at least one")
	}

	// Seed an isolated HOME so appdir.PluginDir() resolves into a temp tree.
	home := t.TempDir()
	t.Setenv("HOME", home)
	pluginDir := filepath.Join(home, ".config", "cliamp", "plugins")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir plugin dir: %v", err)
	}
	for _, name := range luaFiles {
		data, err := os.ReadFile(filepath.Join(srcDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(pluginDir, name), data, 0o644); err != nil {
			t.Fatalf("copy %s: %v", name, err)
		}
	}

	mgr, err := New(nil)
	if err != nil {
		t.Fatalf("bundled plugins failed to load (API compatibility broken): %v", err)
	}
	defer mgr.Close()

	if got, want := mgr.PluginCount(), len(luaFiles); got != want {
		t.Fatalf("loaded %d plugins, want %d (a bundled plugin failed to register)", got, want)
	}
	for _, p := range mgr.plugins {
		if p.Type == "" {
			t.Errorf("plugin %q registered with empty type", p.Name)
		}
	}
}
