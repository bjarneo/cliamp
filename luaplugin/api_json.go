package luaplugin

import (
	"encoding/json"

	lua "github.com/yuin/gopher-lua"
)

// registerJSONAPI adds cliamp.json.{encode,decode} to the cliamp table.
func registerJSONAPI(L *lua.LState, cliamp *lua.LTable) {
	tbl := L.NewTable()

	// cliamp.json.decode(str) -> table
	L.SetField(tbl, "decode", L.NewFunction(func(L *lua.LState) int {
		str := L.CheckString(1)
		var v any
		if err := json.Unmarshal([]byte(str), &v); err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(jsonToLua(L, v))
		return 1
	}))

	// cliamp.json.encode(table) -> string
	L.SetField(tbl, "encode", L.NewFunction(func(L *lua.LState) int {
		val := L.Get(1)
		goVal := luaToGo(val)
		data, err := json.Marshal(goVal)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LString(string(data)))
		return 1
	}))

	L.SetField(cliamp, "json", tbl)
}

func jsonToLua(L *lua.LState, v any) lua.LValue {
	switch val := v.(type) {
	case nil:
		return lua.LNil
	case bool:
		return lua.LBool(val)
	case float64:
		return lua.LNumber(val)
	case string:
		return lua.LString(val)
	case []any:
		tbl := L.NewTable()
		for i, item := range val {
			tbl.RawSetInt(i+1, jsonToLua(L, item))
		}
		return tbl
	case map[string]any:
		tbl := L.NewTable()
		for k, item := range val {
			tbl.RawSetString(k, jsonToLua(L, item))
		}
		return tbl
	default:
		return lua.LNil
	}
}

// maxLuaConvertDepth bounds table nesting accepted by luaToGo. Lua can build a
// deeply nested table cheaply, and unbounded recursion here would overflow the
// Go stack — a fatal error the plugin sandbox cannot contain.
const maxLuaConvertDepth = 64

func luaToGo(val lua.LValue) any {
	return luaToGoValue(val, nil, 0)
}

// luaToGoValue converts val to its Go equivalent. Tables already being
// converted on the current path (cycles) and tables nested deeper than
// maxLuaConvertDepth become nil instead of recursing forever.
func luaToGoValue(val lua.LValue, path map[*lua.LTable]struct{}, depth int) any {
	switch v := val.(type) {
	case *lua.LNilType:
		return nil
	case lua.LBool:
		return bool(v)
	case lua.LNumber:
		return float64(v)
	case lua.LString:
		return string(v)
	case *lua.LTable:
		if depth >= maxLuaConvertDepth {
			return nil
		}
		if _, cyclic := path[v]; cyclic {
			return nil
		}
		if path == nil {
			path = make(map[*lua.LTable]struct{})
		}
		// Tracking only the active path keeps repeated sibling references
		// (a DAG) convertible while still catching true cycles.
		path[v] = struct{}{}
		defer delete(path, v)

		// Detect if it's an array (sequential integer keys starting at 1).
		maxN := v.MaxN()
		if maxN > 0 {
			arr := make([]any, 0, maxN)
			for i := 1; i <= maxN; i++ {
				arr = append(arr, luaToGoValue(v.RawGetInt(i), path, depth+1))
			}
			return arr
		}
		m := make(map[string]any)
		v.ForEach(func(key, value lua.LValue) {
			if ks, ok := key.(lua.LString); ok {
				m[string(ks)] = luaToGoValue(value, path, depth+1)
			}
		})
		return m
	default:
		return nil
	}
}
