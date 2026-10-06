package luaplugin

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadMetadata(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		want    Metadata
		wantErr string
	}{
		{
			name: "all fields",
			source: `plugin.register({name = "full", version = "1.2", description = "d",
				type = "visualizer", permissions = {"keymap", "control", "exec"}})`,
			want: Metadata{Name: "full", Version: "1.2", Description: "d", Type: "visualizer",
				Permissions: []string{"keymap", "control", "exec"}},
		},
		{
			name: "bind at the top level without a name",
			source: `local p = plugin.register({type = "hook", permissions = {"keymap"}})
				p:bind("ctrl+y", "Say hi", function() end)`,
			want: Metadata{Type: "hook", Permissions: []string{"keymap"}},
		},
		{
			name: "cliamp call at the top level without a name",
			source: `local p = plugin.register({type = "hook"})
				cliamp.log.info("loaded")`,
			want: Metadata{Type: "hook"},
		},
		{
			name: "cliamp calls before register",
			source: `cliamp.log.info("loading")
				local saved = cliamp.store.get("x") or {}
				plugin.register({name = "early", type = "hook"})`,
			want: Metadata{Name: "early", Type: "hook"},
		},
		{
			name: "stub result used after register",
			source: `plugin.register({name = "late", type = "hook"})
				local v = cliamp.player.volume() + 1`,
			want: Metadata{Name: "late", Type: "hook"},
		},
		{
			name: "endless loop after register",
			source: `plugin.register({name = "loop", type = "hook"})
				while true do end`,
			want: Metadata{Name: "loop", Type: "hook"},
		},
		{name: "no register call", source: `local x = 1`},
		{
			name:    "unknown permission",
			source:  `plugin.register({name = "x", type = "hook", permissions = {"root"}})`,
			wantErr: `unknown permission "root"`,
		},
		{
			name:    "permissions not an array",
			source:  `plugin.register({name = "x", type = "hook", permissions = "control"})`,
			wantErr: "permissions must be an array",
		},
		{
			name:    "missing type",
			source:  `plugin.register({name = "x"})`,
			wantErr: `needs type = "hook" or "visualizer"`,
		},
		{
			name:    "unknown type",
			source:  `plugin.register({name = "x", type = "visualiser"})`,
			wantErr: `needs type = "hook" or "visualizer"`,
		},
		{
			name:    "error in a second register call",
			source:  `plugin.register({name = "x", type = "hook"}); plugin.register({name = "x"})`,
			wantErr: `needs type = "hook" or "visualizer"`,
		},
		{
			name: "second register call",
			source: `plugin.register({name = "x", type = "hook"})
				plugin.register({name = "x", type = "hook", permissions = {"exec"}})`,
			wantErr: "can be called only once",
		},
		{name: "syntax error", source: `plugin.register({`, wantErr: "syntax error"},
		{name: "endless loop before register", source: `while true do end`, wantErr: "context deadline exceeded"},
		{name: "sandbox", source: `os.execute("true")`, wantErr: "non-function"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ReadMetadata(tt.source)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ReadMetadata() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ReadMetadata() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ReadMetadata() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDiscover(t *testing.T) {
	tests := []struct {
		name  string
		files []string // relative paths to create; a trailing / makes a dir
		want  []PluginFile
	}{
		{"empty", nil, nil},
		{"single file", []string{"a.lua"}, []PluginFile{{"a", "a.lua"}}},
		{"directory with init.lua", []string{"d/init.lua", "d/lib.lua"}, []PluginFile{{"d", "d/init.lua"}}},
		{"directory without init.lua", []string{"d/main.lua"}, nil},
		{"other files", []string{"notes.txt", ".trust.json"}, nil},
		{"file wins over directory", []string{"x.lua", "x/init.lua"}, []PluginFile{{"x", "x.lua"}}},
		{"sorted by name", []string{"b.lua", "a/init.lua", "c.lua"},
			[]PluginFile{{"a", "a/init.lua"}, {"b", "b.lua"}, {"c", "c.lua"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tt.files {
				path := filepath.Join(dir, filepath.FromSlash(f))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := Discover(dir)
			if err != nil {
				t.Fatal(err)
			}
			var want []PluginFile
			for _, f := range tt.want {
				want = append(want, PluginFile{Name: f.Name, Path: filepath.Join(dir, filepath.FromSlash(f.Path))})
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Discover() = %v, want %v", got, want)
			}
		})
	}
}
