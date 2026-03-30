package luaplugin

import (
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func TestJSONEncodeDecode(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerJSONAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)

	err := L.DoString(`
		local encoded = cliamp.json.encode({name = "test", count = 42})
		local decoded = cliamp.json.decode(encoded)
		_G.name = decoded.name
		_G.count = decoded.count
	`)
	if err != nil {
		t.Fatal(err)
	}

	if L.GetGlobal("name").String() != "test" {
		t.Fatalf("name = %q", L.GetGlobal("name").String())
	}
	if float64(L.GetGlobal("count").(lua.LNumber)) != 42 {
		t.Fatalf("count = %v", L.GetGlobal("count"))
	}
}

func TestJSONDecodeInvalid(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerJSONAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)

	err := L.DoString(`
		local result, errmsg = cliamp.json.decode("not json")
		_G.result = result
		_G.errmsg = errmsg
	`)
	if err != nil {
		t.Fatal(err)
	}

	if L.GetGlobal("result") != lua.LNil {
		t.Fatalf("result = %v, want nil", L.GetGlobal("result"))
	}
	if L.GetGlobal("errmsg") == lua.LNil {
		t.Fatal("errmsg should not be nil")
	}
}

func TestJSONEncodeArray(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerJSONAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)

	err := L.DoString(`
		_G.result = cliamp.json.encode({1, 2, 3})
	`)
	if err != nil {
		t.Fatal(err)
	}

	if got := L.GetGlobal("result").String(); got != "[1,2,3]" {
		t.Fatalf("encode([1,2,3]) = %q, want %q", got, "[1,2,3]")
	}
}

func TestLuaToGoRoundtrip(t *testing.T) {
	L := lua.NewState()
	defer L.Close()

	tests := []struct {
		name string
		val  lua.LValue
		want any
	}{
		{"nil", lua.LNil, nil},
		{"bool", lua.LTrue, true},
		{"number", lua.LNumber(3.14), 3.14},
		{"string", lua.LString("hi"), "hi"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := luaToGo(tt.val)
			switch w := tt.want.(type) {
			case nil:
				if got != nil {
					t.Fatalf("got %v, want nil", got)
				}
			case bool:
				if got != w {
					t.Fatalf("got %v, want %v", got, w)
				}
			case float64:
				if got != w {
					t.Fatalf("got %v, want %v", got, w)
				}
			case string:
				if got != w {
					t.Fatalf("got %v, want %v", got, w)
				}
			}
		})
	}
}
