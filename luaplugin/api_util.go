package luaplugin

import (
	"context"

	lua "github.com/yuin/gopher-lua"
)

// callContext returns the context of the Lua call that runs on L, or
// context.Background when L runs outside a call. A Go API that can block
// passes it on, so the time limit of the call and the stop at Close also end
// the wait. callLocked stops only the Lua instructions.
func callContext(L *lua.LState) context.Context {
	if ctx := L.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}

// permitted reports whether p declared perm. The first denial of each
// permission logs one warning that names api. It runs from Lua, under p.mu.
func (p *Plugin) permitted(perm, api string) bool {
	if p.perms[perm] {
		return true
	}
	if !p.warned[perm] {
		if p.warned == nil {
			p.warned = make(map[string]bool)
		}
		p.warned[perm] = true
		p.logger.log(p.installName, "warn", "%s requires permissions = {%q}. cliamp logs this warning once for each permission.", api, perm)
	}
	return false
}

// pushErr pushes nil and msg, the result of a Lua API call that failed.
func pushErr(L *lua.LState, msg string) int {
	L.Push(lua.LNil)
	L.Push(lua.LString(msg))
	return 2
}

// pushResult pushes true when err is nil, or nil and the error message.
func pushResult(L *lua.LState, err error) int {
	if err != nil {
		return pushErr(L, err.Error())
	}
	L.Push(lua.LTrue)
	return 1
}
