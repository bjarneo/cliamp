package luaplugin

import (
	"os"
	"path/filepath"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func TestFSWriteAndRead(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerFSAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)

	tmp := filepath.Join("/tmp", "cliamp-test-"+t.Name())
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

	tmp := filepath.Join("/tmp", "cliamp-test-append-"+t.Name())
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

	tmp := filepath.Join("/tmp", "cliamp-test-exists-"+t.Name())
	os.WriteFile(tmp, []byte("x"), 0o644)
	defer os.Remove(tmp)

	L.SetGlobal("path", lua.LString(tmp))
	L.SetGlobal("fake", lua.LString("/tmp/cliamp-definitely-not-here"))
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

	tmp := filepath.Join("/tmp", "cliamp-test-remove-"+t.Name())
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
		{"/tmp/test.txt", true},
		{"/etc/passwd", false},
		{"/home/user/.ssh/id_rsa", false},
	}

	for _, tt := range tests {
		if got := isWriteAllowed(tt.path); got != tt.want {
			t.Errorf("isWriteAllowed(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
