package luaplugin

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	tests := []struct {
		path string
		want bool
	}{
		{fsAllowedPath("test.txt"), true},
		{fsDisallowedPath(), false},
	}

	for _, tt := range tests {
		if got := isWriteAllowed(tt.path); got != tt.want {
			t.Errorf("isWriteAllowed(%q) = %v, want %v", tt.path, got, tt.want)
		}
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

func TestMusicDirIsAllowed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "Music", "cliamp", "album", "01.mp3")
	if !isWriteAllowed(path) {
		t.Errorf("~/Music/cliamp/... should be writable")
	}
}
