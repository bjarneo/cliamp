package luaplugin

import (
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func TestSandboxBlocksDofile(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	sandbox(L)

	if L.GetGlobal("dofile") != lua.LNil {
		t.Fatal("dofile should be nil after sandbox")
	}
}

func TestSandboxBlocksLoadfile(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	sandbox(L)

	if L.GetGlobal("loadfile") != lua.LNil {
		t.Fatal("loadfile should be nil after sandbox")
	}
}

func TestSandboxRemovesIOModule(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	sandbox(L)

	if L.GetGlobal("io") != lua.LNil {
		t.Fatal("io module should be nil after sandbox")
	}
}

func TestSandboxRestrictsOS(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	sandbox(L)

	os := L.GetGlobal("os").(*lua.LTable)

	blocked := []string{"execute", "remove", "rename", "exit", "setlocale", "tmpname"}
	for _, name := range blocked {
		if os.RawGetString(name) != lua.LNil {
			t.Errorf("os.%s should be nil after sandbox", name)
		}
	}

	// Safe functions should remain.
	allowed := []string{"time", "date", "clock"}
	for _, name := range allowed {
		if os.RawGetString(name) == lua.LNil {
			t.Errorf("os.%s should still be available after sandbox", name)
		}
	}
}

func TestSandboxProvidesUTF8Char(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	sandbox(L)

	err := L.DoString(`_G.result = utf8.char(72, 101, 108, 108, 111)`)
	if err != nil {
		t.Fatal(err)
	}

	if got := L.GetGlobal("result").String(); got != "Hello" {
		t.Fatalf("utf8.char(72,101,108,108,111) = %q, want %q", got, "Hello")
	}
}
