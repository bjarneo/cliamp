package luaplugin

import lua "github.com/yuin/gopher-lua"

// registerPlayerAPI adds the read-only cliamp.player.* table.
func registerPlayerAPI(L *lua.LState, cliamp *lua.LTable, state *StateProvider) {
	tbl := L.NewTable()

	// cliamp.player.state() -> "playing" | "paused" | "stopped"
	L.SetField(tbl, "state", L.NewFunction(func(L *lua.LState) int {
		if state.PlayerState != nil {
			L.Push(lua.LString(state.PlayerState()))
		} else {
			L.Push(lua.LString("stopped"))
		}
		return 1
	}))

	// cliamp.player.position() -> number (seconds)
	L.SetField(tbl, "position", L.NewFunction(func(L *lua.LState) int {
		if state.Position != nil {
			L.Push(lua.LNumber(state.Position()))
		} else {
			L.Push(lua.LNumber(0))
		}
		return 1
	}))

	// cliamp.player.duration() -> number (seconds)
	L.SetField(tbl, "duration", L.NewFunction(func(L *lua.LState) int {
		if state.Duration != nil {
			L.Push(lua.LNumber(state.Duration()))
		} else {
			L.Push(lua.LNumber(0))
		}
		return 1
	}))

	// cliamp.player.volume() -> number (dB)
	L.SetField(tbl, "volume", L.NewFunction(func(L *lua.LState) int {
		if state.Volume != nil {
			L.Push(lua.LNumber(state.Volume()))
		} else {
			L.Push(lua.LNumber(0))
		}
		return 1
	}))

	// cliamp.player.speed() -> number (ratio)
	L.SetField(tbl, "speed", L.NewFunction(func(L *lua.LState) int {
		if state.Speed != nil {
			L.Push(lua.LNumber(state.Speed()))
		} else {
			L.Push(lua.LNumber(1))
		}
		return 1
	}))

	// cliamp.player.mono() -> boolean
	L.SetField(tbl, "mono", L.NewFunction(func(L *lua.LState) int {
		if state.Mono != nil {
			L.Push(lua.LBool(state.Mono()))
		} else {
			L.Push(lua.LFalse)
		}
		return 1
	}))

	// cliamp.player.repeat_mode() -> "off" | "all" | "one"
	L.SetField(tbl, "repeat_mode", L.NewFunction(func(L *lua.LState) int {
		if state.RepeatMode != nil {
			L.Push(lua.LString(state.RepeatMode()))
		} else {
			L.Push(lua.LString("off"))
		}
		return 1
	}))

	// cliamp.player.shuffle() -> boolean
	L.SetField(tbl, "shuffle", L.NewFunction(func(L *lua.LState) int {
		if state.Shuffle != nil {
			L.Push(lua.LBool(state.Shuffle()))
		} else {
			L.Push(lua.LFalse)
		}
		return 1
	}))

	// cliamp.player.eq_bands() -> table of 10 dB values
	L.SetField(tbl, "eq_bands", L.NewFunction(func(L *lua.LState) int {
		t := L.NewTable()
		if state.EQBands != nil {
			bands := state.EQBands()
			for i, b := range bands {
				t.RawSetInt(i+1, lua.LNumber(b))
			}
		}
		L.Push(t)
		return 1
	}))

	L.SetField(cliamp, "player", tbl)
}
