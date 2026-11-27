package luaplugin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/ipc"
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
				filepath.Join(cfg, "favorites.toml"),
				filepath.Join(cfg, "history.toml"),
				filepath.Join(cfg, "resume.json"),
				filepath.Join(cfg, "cliamp.log"),
				filepath.Join(cfg, "spotify_credentials.json"),
				filepath.Join(cfg, "plugins.log"),
			} {
				if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// Symlinks inside an allowed dir that point at denied paths,
			// including dangling symlinks and a symlink loop.
			linked := true
			for name, target := range map[string]string{
				"to-plugins":          plugins,
				"to-config.toml":      filepath.Join(cfg, "config.toml"),
				"to-etc":              filepath.Dir(fsDisallowedPath()),
				"dangling-plugin":     filepath.Join(plugins, "pwn.lua"),
				"dangling-outside":    filepath.Join(home, "nonexistent-secret.txt"),
				"dangling-dir":        filepath.Join(home, "nonexistent-dir"),
				"dangling-rel":        filepath.Join("..", "..", "..", "rel-escape.txt"),
				"dangling-chain":      filepath.Join(data, "dangling-plugin"),
				"loop-a":              filepath.Join(data, "loop-b"),
				"loop-b":              filepath.Join(data, "loop-a"),
				"dangling-creds-file": filepath.Join(cfg, "tidal_credentials.json"),
			} {
				if err := os.Symlink(target, filepath.Join(data, name)); err != nil {
					linked = false
				}
			}

			// A hard link stands in for a name that the file system maps to
			// a denied path but that isWithin does not match.
			hardLinked := os.Link(filepath.Join(cfg, "config.toml"), filepath.Join(data, "config-link.toml")) == nil &&
				os.Link(filepath.Join(cfg, "spotify_credentials.json"), filepath.Join(data, "creds-link.json")) == nil

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
				{name: "config.toml lock file", path: filepath.Join(cfg, "config.toml.lock")},
				{name: "favorites.toml lock file", path: filepath.Join(cfg, "favorites.toml.lock")},
				{name: "history.toml lock file", path: filepath.Join(cfg, "history.toml.lock")},
				{name: "radio_favorites.toml lock file", path: filepath.Join(cfg, "radio_favorites.toml.lock")},
				{name: "radio_countries.toml lock file", path: filepath.Join(cfg, "radio_countries.toml.lock")},
				{name: "playlists lock file", path: filepath.Join(cfg, "playlists.lock")},
				{name: "radios.toml", path: filepath.Join(cfg, "radios.toml")},
				{name: "IPC socket", path: filepath.Join(cfg, "cliamp.sock")},
				{name: "IPC PID file", path: filepath.Join(cfg, "cliamp.sock.pid")},
				{name: "IPC socket lock", path: filepath.Join(cfg, "cliamp.sock.lock")},
				{name: "IPC socket lock subtree", path: filepath.Join(cfg, "cliamp.sock.lock", "blocking")},
				{name: "plugin log", path: filepath.Join(cfg, "plugins.log")},
				{name: "cliamp log", path: filepath.Join(cfg, "cliamp.log")},
				{name: "favorites.toml", path: filepath.Join(cfg, "favorites.toml")},
				{name: "history.toml", path: filepath.Join(cfg, "history.toml")},
				{name: "resume.json", path: filepath.Join(cfg, "resume.json")},
				{name: "spotify credentials", path: filepath.Join(cfg, "spotify_credentials.json")},
				{name: "new qobuz credentials", path: filepath.Join(cfg, "qobuz_credentials.json")},
				{name: "new ytmusic credentials", path: filepath.Join(cfg, "ytmusic_credentials.json")},
				{name: "credentials with other case", path: filepath.Join(cfg, "Spotify_Credentials.JSON"), want: !caseFolded},
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
				{name: "dangling symlink to plugin", path: filepath.Join(data, "dangling-plugin"), link: true},
				{name: "dangling symlink outside allowlist", path: filepath.Join(data, "dangling-outside"), link: true},
				{name: "dangling directory symlink child", path: filepath.Join(data, "dangling-dir", "child.txt"), link: true},
				{name: "relative dangling symlink", path: filepath.Join(data, "dangling-rel"), link: true},
				{name: "chained dangling symlink", path: filepath.Join(data, "dangling-chain"), link: true},
				{name: "dangling symlink to credentials", path: filepath.Join(data, "dangling-creds-file"), link: true},
				{name: "symlink loop", path: filepath.Join(data, "loop-a"), link: true},
				{name: "hard link to config.toml", path: filepath.Join(data, "config-link.toml"), hardLink: true},
				{name: "hard link to credentials", path: filepath.Join(data, "creds-link.json"), hardLink: true},
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

func TestFSMkdirRejectsIPCLock(t *testing.T) {
	cfgDir := filepath.Join(t.TempDir(), "cliamp")
	t.Setenv("CLIAMP_CONFIG_DIR", cfgDir)
	prev := activeWriteRules.Swap(nil)
	t.Cleanup(func() { activeWriteRules.Store(prev) })
	initWriteRules()

	lockPath := ipc.DefaultSocketPath() + ".lock"
	if isWriteAllowed(lockPath) {
		t.Fatalf("IPC socket lock path %q must be denied", lockPath)
	}

	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerFSAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)
	L.SetGlobal("lockPath", lua.LString(lockPath))

	if err := L.DoString(`cliamp.fs.mkdir(lockPath)`); err == nil || !strings.Contains(err.Error(), "mkdir not allowed for this path") {
		t.Fatalf("fs.mkdir on IPC socket lock returned %v, want access denial", err)
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

func TestFSWriteAndAppendSafety(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerFSAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)

	dir, err := os.MkdirTemp(os.TempDir(), "cliamp-fs-safety-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	// Write truncates shorter content properly.
	shortPath := filepath.Join(dir, "trunc.txt")
	L.SetGlobal("shortPath", lua.LString(shortPath))
	if err := L.DoString(`
		assert(cliamp.fs.write(shortPath, "hello world"))
		assert(cliamp.fs.write(shortPath, "hi"))
		_G.got = cliamp.fs.read(shortPath)
	`); err != nil {
		t.Fatal(err)
	}
	if got := L.GetGlobal("got").String(); got != "hi" {
		t.Fatalf("after shorter write, content = %q, want %q", got, "hi")
	}

	// Write size cap rejects payloads over 16 MiB.
	bigPath := filepath.Join(dir, "big.txt")
	L.SetGlobal("bigPath", lua.LString(bigPath))
	L.SetGlobal("bigData", lua.LString(strings.Repeat("a", maxFSWriteBytes+1)))
	if err := L.DoString(`
		_G.w_ok, _G.w_err = cliamp.fs.write(bigPath, bigData)
		_G.a_ok, _G.a_err = cliamp.fs.append(bigPath, bigData)
	`); err != nil {
		t.Fatal(err)
	}
	if L.GetGlobal("w_ok") != lua.LNil || !strings.Contains(L.GetGlobal("w_err").String(), "limit") {
		t.Errorf("oversized write = %v, %v; want nil and limit error", L.GetGlobal("w_ok"), L.GetGlobal("w_err"))
	}
	if L.GetGlobal("a_ok") != lua.LNil || !strings.Contains(L.GetGlobal("a_err").String(), "limit") {
		t.Errorf("oversized append = %v, %v; want nil and limit error", L.GetGlobal("a_ok"), L.GetGlobal("a_err"))
	}

	// Append rejects pushing an existing file past 16 MiB.
	appendPath := filepath.Join(dir, "append-cap.txt")
	if err := os.WriteFile(appendPath, []byte(strings.Repeat("x", maxFSWriteBytes-10)), 0o644); err != nil {
		t.Fatal(err)
	}
	L.SetGlobal("appendPath", lua.LString(appendPath))
	if err := L.DoString(`_G.ap_ok, _G.ap_err = cliamp.fs.append(appendPath, "01234567890")`); err != nil {
		t.Fatal(err)
	}
	if L.GetGlobal("ap_ok") != lua.LNil || !strings.Contains(L.GetGlobal("ap_err").String(), "limit") {
		t.Errorf("cumulative oversized append = %v, %v; want nil and limit error", L.GetGlobal("ap_ok"), L.GetGlobal("ap_err"))
	}

	if runtime.GOOS != "windows" {
		// O_NOFOLLOW rejects writing directly through a symlink in an allowed dir.
		target := filepath.Join(dir, "real.txt")
		link := filepath.Join(dir, "link.txt")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		L.SetGlobal("linkPath", lua.LString(link))
		if err := L.DoString(`
			_G.sw_ok, _G.sw_err = cliamp.fs.write(linkPath, "x")
			_G.sa_ok, _G.sa_err = cliamp.fs.append(linkPath, "x")
		`); err != nil {
			t.Fatal(err)
		}
		if L.GetGlobal("sw_ok") != lua.LNil {
			t.Errorf("write through symlink succeeded unexpectedly")
		}
		if L.GetGlobal("sa_ok") != lua.LNil {
			t.Errorf("append through symlink succeeded unexpectedly")
		}

		// O_NONBLOCK prevents blocking when writing or appending to a FIFO.
		if mkfifo, err := exec.LookPath("mkfifo"); err == nil {
			fifo := filepath.Join(dir, "pipe")
			if out, err := exec.Command(mkfifo, fifo).CombinedOutput(); err == nil {
				L.SetGlobal("fifoPath", lua.LString(fifo))
				if err := L.DoString(`
					_G.fw_ok, _G.fw_err = cliamp.fs.write(fifoPath, "x")
					_G.fa_ok, _G.fa_err = cliamp.fs.append(fifoPath, "x")
				`); err != nil {
					t.Fatal(err)
				}
				if L.GetGlobal("fw_ok") != lua.LNil {
					t.Errorf("write to FIFO succeeded unexpectedly")
				}
				if L.GetGlobal("fa_ok") != lua.LNil {
					t.Errorf("append to FIFO succeeded unexpectedly")
				}
			} else {
				t.Logf("mkfifo skipped: %v: %s", err, out)
			}
		}
	}
}

func TestNewInitializesWriteRulesEagerly(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".config", "cliamp")
	t.Setenv("CLIAMP_CONFIG_DIR", cfgDir)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	prev := activeWriteRules.Swap(nil)
	t.Cleanup(func() { activeWriteRules.Store(prev) })

	m, err := New(nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Close()

	rules := activeWriteRules.Load()
	if rules == nil {
		t.Fatal("activeWriteRules was not initialized during New")
	}
	wantCfg, ok := canonicalExistingPath(cfgDir)
	if !ok {
		t.Fatal("canonicalExistingPath failed")
	}
	if rules.configDir != wantCfg {
		t.Errorf("activeWriteRules.configDir = %q, want %q", rules.configDir, wantCfg)
	}
}

func TestFSCwdAndAbs(t *testing.T) {
	t.Chdir(t.TempDir())
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	absolute := filepath.Join(wd, "music", "song.mp3")

	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerFSAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)
	L.SetGlobal("relative", lua.LString(filepath.Join("album", "..", "music", "song.mp3")))
	L.SetGlobal("absolute", lua.LString(absolute))

	if err := L.DoString(`
		_G.cwd = cliamp.fs.cwd()
		_G.from_relative = cliamp.fs.abs(relative)
		_G.from_absolute = cliamp.fs.abs(absolute)
		_G.empty_val, _G.empty_err = cliamp.fs.abs("")
	`); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		global string
		want   string
	}{
		{"cwd", wd},
		{"from_relative", absolute},
		{"from_absolute", absolute},
	} {
		if got := L.GetGlobal(tt.global).String(); got != tt.want {
			t.Errorf("%s = %q, want %q", tt.global, got, tt.want)
		}
	}
	if L.GetGlobal("empty_val") != lua.LNil {
		t.Errorf("abs(\"\") value = %v, want nil", L.GetGlobal("empty_val"))
	}
	if L.GetGlobal("empty_err").String() == "" {
		t.Errorf("abs(\"\") error = empty, want non-empty error")
	}
}
