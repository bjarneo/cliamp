package luaplugin

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func fsAllowedPath(name string) string {
	return filepath.Join(os.TempDir(), name)
}

func fsDisallowedPath() string {
	if runtime.GOOS == "windows" {
		if root := os.Getenv("WINDIR"); root != "" {
			return filepath.Join(root, "System32", "drivers", "etc", "hosts")
		}
		return `C:\Windows\System32\drivers\etc\hosts`
	}
	return "/etc/passwd"
}

func TestFSWriteAndRead(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerFSAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)

	tmp := fsAllowedPath("cliamp-test-" + t.Name())
	defer os.Remove(tmp)

	L.SetGlobal("path", lua.LString(tmp))
	err := L.DoString(`
		local ok = cliamp.fs.write(path, "hello world")
		_G.write_ok = ok
		local content = cliamp.fs.read(path)
		_G.content = content
	`)
	if err != nil {
		t.Fatal(err)
	}

	if L.GetGlobal("write_ok") != lua.LTrue {
		t.Fatal("fs.write returned non-true")
	}
	if L.GetGlobal("content").String() != "hello world" {
		t.Fatalf("fs.read = %q, want %q", L.GetGlobal("content").String(), "hello world")
	}
}

func TestFSAppend(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerFSAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)

	tmp := fsAllowedPath("cliamp-test-append-" + t.Name())
	defer os.Remove(tmp)

	L.SetGlobal("path", lua.LString(tmp))
	err := L.DoString(`
		cliamp.fs.write(path, "hello")
		cliamp.fs.append(path, " world")
		_G.content = cliamp.fs.read(path)
	`)
	if err != nil {
		t.Fatal(err)
	}

	if got := L.GetGlobal("content").String(); got != "hello world" {
		t.Fatalf("after append, content = %q, want %q", got, "hello world")
	}
}

func TestFSExists(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerFSAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)

	tmp := fsAllowedPath("cliamp-test-exists-" + t.Name())
	os.WriteFile(tmp, []byte("x"), 0o644)
	defer os.Remove(tmp)

	L.SetGlobal("path", lua.LString(tmp))
	L.SetGlobal("fake", lua.LString(fsAllowedPath("cliamp-definitely-not-here")))
	err := L.DoString(`
		_G.exists = cliamp.fs.exists(path)
		_G.not_exists = cliamp.fs.exists(fake)
	`)
	if err != nil {
		t.Fatal(err)
	}

	if L.GetGlobal("exists") != lua.LTrue {
		t.Fatal("fs.exists returned false for existing file")
	}
	if L.GetGlobal("not_exists") != lua.LFalse {
		t.Fatal("fs.exists returned true for non-existing file")
	}
}

func TestFSRemove(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerFSAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)

	tmp := fsAllowedPath("cliamp-test-remove-" + t.Name())
	os.WriteFile(tmp, []byte("x"), 0o644)

	L.SetGlobal("path", lua.LString(tmp))
	err := L.DoString(`
		_G.remove_ok = cliamp.fs.remove(path)
		_G.exists_after = cliamp.fs.exists(path)
	`)
	if err != nil {
		t.Fatal(err)
	}

	if L.GetGlobal("remove_ok") != lua.LTrue {
		t.Fatal("fs.remove returned non-true")
	}
	if L.GetGlobal("exists_after") != lua.LFalse {
		t.Fatal("file still exists after remove")
	}
}

func TestIsWriteAllowed(t *testing.T) {
	layouts := []struct {
		name       string
		linkConfig bool // ~/.config/cliamp is a symlink to a dotfiles dir
	}{
		{name: "real config dir"},
		{name: "symlinked config dir", linkConfig: true},
	}
	for _, layout := range layouts {
		t.Run(layout.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home) // os.UserHomeDir reads it on Windows
			cfg := filepath.Join(home, ".config", "cliamp")
			if layout.linkConfig {
				target := filepath.Join(home, "dotfiles", "cliamp")
				mustMkdirAll(t, target)
				mustMkdirAll(t, filepath.Dir(cfg))
				if err := os.Symlink(target, cfg); err != nil {
					t.Skipf("symlink: %v", err)
				}
			}
			plugins := filepath.Join(cfg, "plugins")
			data := filepath.Join(home, ".local", "share", "cliamp")
			mustMkdirAll(t, filepath.Join(plugins, "pkg"))
			mustMkdirAll(t, data)
			for _, f := range []string{
				filepath.Join(plugins, ".trust.json"),
				filepath.Join(plugins, "hello.lua"),
				filepath.Join(plugins, "pkg", "init.lua"),
				filepath.Join(cfg, "config.toml"),
				filepath.Join(cfg, "radios.toml"),
				filepath.Join(cfg, "plugins.log"),
			} {
				if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// Symlinks inside an allowed dir that point at denied paths.
			linked := true
			for name, target := range map[string]string{
				"to-plugins":     plugins,
				"to-config.toml": filepath.Join(cfg, "config.toml"),
				"to-etc":         filepath.Dir(fsDisallowedPath()),
			} {
				if err := os.Symlink(target, filepath.Join(data, name)); err != nil {
					linked = false
				}
			}

			// A hard link stands in for a name that the file system maps to
			// a denied path but that isWithin does not match.
			hardLinked := os.Link(filepath.Join(cfg, "config.toml"), filepath.Join(data, "config-link.toml")) == nil

			rules := loadWriteRules()
			if tmp := fsAllowedPath("test.txt"); !rules.allows(tmp) {
				t.Errorf("allows(%q) = false, want true", tmp)
			}
			// HOME is in the temp dir. Drop the temp roots, so that only the
			// cliamp roots can make a path under HOME writable.
			var tempRoots []string
			for _, tmp := range []string{"/tmp", os.TempDir()} {
				if canon, ok := canonicalExistingPath(tmp); ok {
					tempRoots = append(tempRoots, canon)
				}
			}
			rules.allow = slices.DeleteFunc(rules.allow, func(dir string) bool {
				return slices.Contains(tempRoots, dir)
			})

			tests := []struct {
				name     string
				path     string
				want     bool
				link     bool // the path goes through a symlink in data
				hardLink bool // the path is a hard link in data
			}{
				{name: "system file", path: fsDisallowedPath()},
				{name: "home dotfile", path: filepath.Join(home, ".bashrc")},
				{name: "config dir", path: cfg, want: true},
				{name: "new file in config dir", path: filepath.Join(cfg, "notes.txt"), want: true},
				{name: "theme file", path: filepath.Join(cfg, "themes", "mine.toml"), want: true},
				{name: "own data dir", path: filepath.Join(data, "plugins", "hello", "store.json"), want: true},
				{name: "cliamp.store file", path: newPluginStore("hello").path, want: true},
				{name: "music dir", path: filepath.Join(home, "Music", "cliamp", "album", "01.mp3"), want: true},
				{name: "plugins dir", path: plugins},
				{name: "trust manifest", path: filepath.Join(plugins, ".trust.json")},
				{name: "existing plugin", path: filepath.Join(plugins, "hello.lua")},
				{name: "new plugin", path: filepath.Join(plugins, "evil.lua")},
				{name: "dir plugin", path: filepath.Join(plugins, "pkg", "init.lua")},
				{name: "new dir plugin", path: filepath.Join(plugins, "evil", "init.lua")},
				{name: "config.toml", path: filepath.Join(cfg, "config.toml")},
				{name: "config.toml through traversal", path: filepath.Join(cfg, "themes", "..", "config.toml")},
				{name: "radios.toml", path: filepath.Join(cfg, "radios.toml")},
				{name: "IPC socket", path: filepath.Join(cfg, "cliamp.sock")},
				{name: "IPC PID file", path: filepath.Join(cfg, "cliamp.sock.pid")},
				{name: "plugin log", path: filepath.Join(cfg, "plugins.log")},
				{name: "plugins dir with other case", path: filepath.Join(cfg, "PLUGINS", "evil.lua"), want: !caseFolded},
				{name: "config.toml with other case", path: filepath.Join(cfg, "Config.toml"), want: !caseFolded},
				{name: "plugins dir with long s", path: filepath.Join(cfg, "plugin\u017f", "evil.lua"), want: !caseFolded},
				{name: "trust manifest with long s", path: filepath.Join(cfg, "plugin\u017f", ".trust.json"), want: !caseFolded},
				{name: "radios.toml with long s", path: filepath.Join(cfg, "radio\u017f.toml"), want: !caseFolded},
				{name: "IPC socket with Kelvin sign", path: filepath.Join(cfg, "cliamp.soc\u212a"), want: !caseFolded},
				{name: "config.toml stream", path: filepath.Join(cfg, "config.toml::$DATA"), want: runtime.GOOS != "windows"},
				{name: "symlink to plugins dir", path: filepath.Join(data, "to-plugins", "evil.lua"), link: true},
				{name: "symlink to config.toml", path: filepath.Join(data, "to-config.toml"), link: true},
				{name: "symlink out of allowed dir", path: filepath.Join(data, "to-etc", "new.txt"), link: true},
				{name: "hard link to config.toml", path: filepath.Join(data, "config-link.toml"), hardLink: true},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					if tt.link && !linked {
						t.Skip("symlinks are not available")
					}
					if tt.hardLink && !hardLinked {
						t.Skip("hard links are not available")
					}
					if got := rules.allows(tt.path); got != tt.want {
						t.Errorf("allows(%q) = %v, want %v", tt.path, got, tt.want)
					}
				})
			}
		})
	}
}

func TestIsWithin(t *testing.T) {
	sep := string(os.PathSeparator)
	dir := filepath.Join(sep+"cfg", "plugins")
	tests := []struct {
		name       string
		path       string
		want       bool
		wantFolded bool
	}{
		{name: "same path", path: dir, want: true, wantFolded: true},
		{name: "child", path: filepath.Join(dir, "a.lua"), want: true, wantFolded: true},
		{name: "parent", path: filepath.Dir(dir)},
		{name: "sibling with the same prefix", path: dir + "2"},
		{name: "other case", path: filepath.Join(sep+"cfg", "PLUGINS", "a.lua"), wantFolded: true},
		{name: "long s", path: filepath.Join(sep+"cfg", "plugin\u017f", "a.lua"), wantFolded: true},
		{name: "other name", path: filepath.Join(sep+"cfg", "plugin", "a.lua")},
	}
	defer func(v bool) { caseFolded = v }(caseFolded)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, folded := range []bool{false, true} {
				caseFolded = folded
				want := tt.want
				if folded {
					want = tt.wantFolded
				}
				if got := isWithin(tt.path, dir); got != want {
					t.Errorf("isWithin(%q, %q) with caseFolded %v = %v, want %v", tt.path, dir, folded, got, want)
				}
			}
		})
	}
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestFSMkdirAndListdir(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerFSAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)

	base := fsAllowedPath("cliamp-test-mkdir-" + t.Name())
	defer os.RemoveAll(base)

	L.SetGlobal("base", lua.LString(base))
	err := L.DoString(`
		_G.mkdir_ok = cliamp.fs.mkdir(base .. "/sub")
		cliamp.fs.write(base .. "/a.txt", "a")
		cliamp.fs.write(base .. "/b.txt", "b")
		local names, err = cliamp.fs.listdir(base)
		_G.names = names
		_G.err = err
	`)
	if err != nil {
		t.Fatal(err)
	}
	if L.GetGlobal("mkdir_ok") != lua.LTrue {
		t.Fatal("fs.mkdir returned non-true")
	}
	names, ok := L.GetGlobal("names").(*lua.LTable)
	if !ok {
		t.Fatalf("listdir returned %T, want table", L.GetGlobal("names"))
	}
	if n := names.Len(); n != 3 {
		t.Fatalf("listdir returned %d entries, want 3", n)
	}
}

func TestFSMkdirRejectsOutsideAllowlist(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerFSAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)

	err := L.DoString(fmt.Sprintf("cliamp.fs.mkdir(%q)", fsDisallowedPath()))
	if err == nil {
		t.Fatal("expected error for path outside allowlist")
	}
}
