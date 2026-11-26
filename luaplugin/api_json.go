package luaplugin

import (
	"context"
	"encoding/json"
	"errors"

	lua "github.com/yuin/gopher-lua"
)

const (
	// maxLuaConvertDepth bounds table nesting accepted by luaToGo.
	maxLuaConvertDepth = 64

	// maxLuaConvertNodes caps total nodes visited during one Lua-to-Go
	// conversion to block exponential DAG denial of service.
	maxLuaConvertNodes = 10000
)

var errLuaConvertLimit = errors.New("table exceeds maximum node limit")

// registerJSONAPI adds cliamp.json.{encode,decode} to the cliamp table.
func registerJSONAPI(L *lua.LState, cliamp *lua.LTable) {
	tbl := L.NewTable()

	// cliamp.json.decode(str) -> table
	L.SetField(tbl, "decode", L.NewFunction(func(L *lua.LState) int {
		str := L.CheckString(1)
		var v any
		if err := json.Unmarshal([]byte(str), &v); err != nil {
			return pushErr(L, err.Error())
		}
		L.Push(toLua(L, v))
		return 1
	}))

	// cliamp.json.encode(table) -> string
	L.SetField(tbl, "encode", L.NewFunction(func(L *lua.LState) int {
		val := L.Get(1)
		goVal, err := luaToGoCtx(callContext(L), val)
		if err != nil {
			return pushErr(L, err.Error())
		}
		data, err := json.Marshal(goVal)
		if err != nil {
			return pushErr(L, err.Error())
		}
		L.Push(lua.LString(string(data)))
		return 1
	}))

	L.SetField(cliamp, "json", tbl)
}

func luaToGo(val lua.LValue) any {
	v, _ := luaToGoCtx(context.Background(), val)
	return v
}

// luaToGoCtx converts val to a Go value while enforcing depth, node budget,
// and context cancellation limits.
func luaToGoCtx(ctx context.Context, val lua.LValue) (any, error) {
	c := luaConverter{ctx: ctx}
	out := c.convert(val, 0)
	if c.err != nil {
		return nil, c.err
	}
	return out, nil
}

type luaConverter struct {
	ctx   context.Context
	path  map[*lua.LTable]struct{}
	nodes int
	err   error
}

func (c *luaConverter) step() bool {
	if c.err != nil {
		return false
	}
	c.nodes++
	if c.nodes > maxLuaConvertNodes {
		c.err = errLuaConvertLimit
		return false
	}
	if c.ctx != nil && c.nodes&0xff == 0 {
		if err := c.ctx.Err(); err != nil {
			c.err = err
			return false
		}
	}
	return true
}

// convert converts val to its Go equivalent. Cycles and tables nested deeper
// than maxLuaConvertDepth become nil.
func (c *luaConverter) convert(val lua.LValue, depth int) any {
	if !c.step() {
		return nil
	}
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
		if _, cyclic := c.path[v]; cyclic {
			return nil
		}
		if c.path == nil {
			c.path = make(map[*lua.LTable]struct{})
		}
		// Track only the active path so repeated sibling references stay
		// convertible within the total node budget.
		c.path[v] = struct{}{}
		defer delete(c.path, v)

		maxN := v.MaxN()
		if maxN > 0 {
			capHint := min(maxN, maxLuaConvertNodes-c.nodes+1)
			if capHint < 0 {
				capHint = 0
			}
			arr := make([]any, 0, capHint)
			for i := 1; i <= maxN; i++ {
				elem := c.convert(v.RawGetInt(i), depth+1)
				if c.err != nil {
					return nil
				}
				arr = append(arr, elem)
			}
			return arr
		}
		m := make(map[string]any)
		for k, value := v.Next(lua.LNil); k != lua.LNil; k, value = v.Next(k) {
			ks, ok := k.(lua.LString)
			if !ok {
				if !c.step() {
					return nil
				}
				continue
			}
			m[string(ks)] = c.convert(value, depth+1)
			if c.err != nil {
				return nil
			}
		}
		return m
	default:
		return nil
	}
}
