package luaplugin

import (
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// newStoreState returns an LState with cliamp.store registered for pluginName.
func newStoreState(t *testing.T, pluginName string) *lua.LState {
	t.Helper()
	L := lua.NewState()
	t.Cleanup(L.Close)
	cliamp := L.NewTable()
	registerStoreAPI(L, cliamp, pluginName)
	L.SetGlobal("cliamp", cliamp)
	return L
}

func TestStoreSetGetRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	L := newStoreState(t, "rt")

	if err := L.DoString(`
		cliamp.store.set("str", "hello")
		cliamp.store.set("num", 42)
		cliamp.store.set("flag", true)
		cliamp.store.set("tbl", {a = 1, b = {2, 3}})
		_G.s = cliamp.store.get("str")
		_G.n = cliamp.store.get("num")
		_G.f = cliamp.store.get("flag")
		_G.t_a = cliamp.store.get("tbl").a
		_G.t_b2 = cliamp.store.get("tbl").b[2]
		_G.missing = cliamp.store.get("nope")
	`); err != nil {
		t.Fatal(err)
	}

	if got := L.GetGlobal("s").String(); got != "hello" {
		t.Errorf("str = %q", got)
	}
	if got := float64(L.GetGlobal("n").(lua.LNumber)); got != 42 {
		t.Errorf("num = %v", got)
	}
	if got := bool(L.GetGlobal("f").(lua.LBool)); !got {
		t.Errorf("flag = %v", got)
	}
	if got := float64(L.GetGlobal("t_a").(lua.LNumber)); got != 1 {
		t.Errorf("tbl.a = %v", got)
	}
	if got := float64(L.GetGlobal("t_b2").(lua.LNumber)); got != 3 {
		t.Errorf("tbl.b[2] = %v", got)
	}
	if L.GetGlobal("missing") != lua.LNil {
		t.Errorf("missing key should be nil, got %v", L.GetGlobal("missing"))
	}
}

func TestStorePersistsAcrossInstances(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	L1 := newStoreState(t, "persist")
	if err := L1.DoString(`cliamp.store.set("count", 7)`); err != nil {
		t.Fatal(err)
	}

	// Fresh LState + fresh store object for the same plugin name reads from disk.
	L2 := newStoreState(t, "persist")
	if err := L2.DoString(`_G.v = cliamp.store.get("count")`); err != nil {
		t.Fatal(err)
	}
	if got := float64(L2.GetGlobal("v").(lua.LNumber)); got != 7 {
		t.Fatalf("persisted count = %v, want 7", got)
	}
}

func TestStoreNamespaceIsolation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	La := newStoreState(t, "plugin-a")
	if err := La.DoString(`cliamp.store.set("secret", "a-only")`); err != nil {
		t.Fatal(err)
	}

	Lb := newStoreState(t, "plugin-b")
	if err := Lb.DoString(`_G.v = cliamp.store.get("secret")`); err != nil {
		t.Fatal(err)
	}
	if Lb.GetGlobal("v") != lua.LNil {
		t.Fatalf("plugin-b read plugin-a key: %v", Lb.GetGlobal("v"))
	}
}

func TestStoreKeysAndClear(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	L := newStoreState(t, "kc")

	if err := L.DoString(`
		cliamp.store.set("b", 1)
		cliamp.store.set("a", 2)
		cliamp.store.set("c", 3)
		_G.keys = cliamp.store.keys()
		cliamp.store.delete("b")
		_G.afterDelete = #cliamp.store.keys()
		cliamp.store.clear()
		_G.afterClear = #cliamp.store.keys()
	`); err != nil {
		t.Fatal(err)
	}

	keys := L.GetGlobal("keys").(*lua.LTable)
	if keys.Len() != 3 {
		t.Fatalf("keys len = %d, want 3", keys.Len())
	}
	// keys() is sorted.
	if keys.RawGetInt(1).String() != "a" || keys.RawGetInt(3).String() != "c" {
		t.Errorf("keys not sorted: %v, %v", keys.RawGetInt(1), keys.RawGetInt(3))
	}
	if got := float64(L.GetGlobal("afterDelete").(lua.LNumber)); got != 2 {
		t.Errorf("after delete = %v, want 2", got)
	}
	if got := float64(L.GetGlobal("afterClear").(lua.LNumber)); got != 0 {
		t.Errorf("after clear = %v, want 0", got)
	}
}

// JSON cannot hold NaN or an infinity. set rejects such a value, and the
// store keeps working. Before, the value stayed in the map, and each later
// set failed to save.
func TestStoreRejectsValuesThatJSONCannotHold(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"NaN", "0/0"},
		{"Inf", "1/0"},
		{"-Inf", "-1/0"},
		{"NaN in a table", "{avg = 0/0}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			L := newStoreState(t, "nan")
			if err := L.DoString(`
				_G.bad_ok, _G.bad_err = cliamp.store.set("avg", ` + tt.value + `)
				_G.good_ok, _G.good_err = cliamp.store.set("name", "x")
				_G.avg = cliamp.store.get("avg")
			`); err != nil {
				t.Fatal(err)
			}
			if got := L.GetGlobal("bad_ok"); got != lua.LNil {
				t.Errorf("set(%s) = %v, want nil and an error", tt.value, got)
			}
			if got := L.GetGlobal("bad_err"); got == lua.LNil {
				t.Errorf("set(%s) error = nil, want an error", tt.value)
			}
			if got := L.GetGlobal("good_ok"); got != lua.LTrue {
				t.Errorf("set after the bad value = %v, %v, want true", got, L.GetGlobal("good_err"))
			}
			if got := L.GetGlobal("avg"); got != lua.LNil {
				t.Errorf("get(avg) = %v, want nil", got)
			}

			// The valid value reached the disk.
			L2 := newStoreState(t, "nan")
			if err := L2.DoString(`_G.v = cliamp.store.get("name")`); err != nil {
				t.Fatal(err)
			}
			if got := L2.GetGlobal("v"); got != lua.LString("x") {
				t.Errorf("persisted name = %v, want x", got)
			}
		})
	}
}
