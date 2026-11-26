package luaplugin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

func TestLuaToGoBreaksTableCycles(t *testing.T) {
	L := lua.NewState()
	defer L.Close()

	self := L.NewTable()
	self.RawSetString("name", lua.LString("self"))
	self.RawSetString("self", self)

	left := L.NewTable()
	right := L.NewTable()
	left.RawSetString("other", right)
	right.RawSetString("other", left)
	self.RawSetString("left", left)

	// A cycle must not recurse until the Go stack overflows, which would kill
	// cliamp instead of failing inside the plugin sandbox.
	got, ok := luaToGo(self).(map[string]any)
	if !ok {
		t.Fatalf("luaToGo returned %T, want map", luaToGo(self))
	}
	if got["name"] != "self" {
		t.Fatalf("name = %v, want self", got["name"])
	}
	if got["self"] != nil {
		t.Fatalf("cyclic self reference = %v, want nil", got["self"])
	}
	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("marshal cyclic table: %v", err)
	}
}

func TestLuaToGoKeepsRepeatedReferences(t *testing.T) {
	L := lua.NewState()
	defer L.Close()

	shared := L.NewTable()
	shared.RawSetString("id", lua.LString("shared"))
	root := L.NewTable()
	root.RawSetString("first", shared)
	root.RawSetString("second", shared)

	// The same table reached twice on different paths is not a cycle.
	got, ok := luaToGo(root).(map[string]any)
	if !ok {
		t.Fatalf("luaToGo returned %T, want map", luaToGo(root))
	}
	for _, key := range []string{"first", "second"} {
		child, ok := got[key].(map[string]any)
		if !ok || child["id"] != "shared" {
			t.Fatalf("%s = %#v, want shared table", key, got[key])
		}
	}
}

func TestLuaToGoLimitsNestingDepth(t *testing.T) {
	L := lua.NewState()
	defer L.Close()

	root := L.NewTable()
	deepest := root
	for range maxLuaConvertDepth + 10 {
		next := L.NewTable()
		deepest.RawSetString("next", next)
		deepest = next
	}

	current, ok := luaToGo(root).(map[string]any)
	if !ok {
		t.Fatalf("luaToGo returned %T, want map", luaToGo(root))
	}
	depth := 1
	for {
		next, isTable := current["next"].(map[string]any)
		if !isTable {
			break
		}
		current = next
		depth++
	}
	if depth != maxLuaConvertDepth {
		t.Fatalf("converted depth = %d, want %d", depth, maxLuaConvertDepth)
	}
}

func TestJSONEncodeSurvivesCyclicTable(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerJSONAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)

	if err := L.DoString(`
		local t = {name = "loop"}
		t.self = t
		_G.result = cliamp.json.encode(t)
	`); err != nil {
		t.Fatal(err)
	}
	// The cyclic reference becomes null instead of crashing the process.
	if got := L.GetGlobal("result").String(); got != `{"name":"loop","self":null}` {
		t.Fatalf("encode(cyclic) = %q", got)
	}
}

func TestLuaToGoNodeBudgetAndContextCancel(t *testing.T) {
	tests := []struct {
		name    string
		build   func(L *lua.LState) lua.LValue
		cancel  bool
		wantErr error
	}{
		{
			name: "exponential DAG hits node budget",
			build: func(L *lua.LState) lua.LValue {
				cur := L.NewTable()
				cur.RawSetString("leaf", lua.LNumber(1))
				for range 30 {
					next := L.NewTable()
					next.RawSetInt(1, cur)
					next.RawSetInt(2, cur)
					cur = next
				}
				return cur
			},
			wantErr: errLuaConvertLimit,
		},
		{
			name: "wide table hits node budget",
			build: func(L *lua.LState) lua.LValue {
				tbl := L.NewTable()
				for i := 1; i <= maxLuaConvertNodes+10; i++ {
					tbl.RawSetInt(i, lua.LNumber(i))
				}
				return tbl
			},
			wantErr: errLuaConvertLimit,
		},
		{
			name: "cancelled context aborts conversion",
			build: func(L *lua.LState) lua.LValue {
				tbl := L.NewTable()
				for i := 1; i <= 512; i++ {
					tbl.RawSetInt(i, lua.LNumber(i))
				}
				return tbl
			},
			cancel:  true,
			wantErr: context.Canceled,
		},
		{
			name: "small DAG succeeds",
			build: func(L *lua.LState) lua.LValue {
				cur := L.NewTable()
				cur.RawSetString("leaf", lua.LNumber(1))
				for range 4 {
					next := L.NewTable()
					next.RawSetInt(1, cur)
					next.RawSetInt(2, cur)
					cur = next
				}
				return cur
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			L := lua.NewState()
			defer L.Close()
			ctx, cancel := context.WithCancel(context.Background())
			if tt.cancel {
				cancel()
			} else {
				defer cancel()
			}
			_, err := luaToGoCtx(ctx, tt.build(L))
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("luaToGoCtx() err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("luaToGoCtx() unexpected error: %v", err)
			}
		})
	}

	// Verify cliamp.json.encode returns nil and an error on an exponential DAG.
	L := newSandboxedState()
	defer L.Close()
	cliamp := L.NewTable()
	registerJSONAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)
	if err := L.DoString(`
		local t = {a = 1}
		for i = 1, 30 do t = {t, t} end
		_G.res, _G.err = cliamp.json.encode(t)
	`); err != nil {
		t.Fatal(err)
	}
	if L.GetGlobal("res") != lua.LNil || !strings.Contains(L.GetGlobal("err").String(), "node limit") {
		t.Fatalf("encode(DAG) = %v, %v; want nil and node limit error", L.GetGlobal("res"), L.GetGlobal("err"))
	}
}
