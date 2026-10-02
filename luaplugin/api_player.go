package luaplugin

import lua "github.com/yuin/gopher-lua"

// registerPlayerAPI adds the read-only cliamp.player.* table.
func registerPlayerAPI(L *lua.LState, cliamp *lua.LTable, loadState func() *StateProvider) {
	tbl := L.NewTable()

	// cliamp.player.state() -> "playing" | "paused" | "stopped"
	L.SetField(tbl, "state", L.NewFunction(func(L *lua.LState) int {
		state := loadState()
		if state.PlayerState != nil {
			L.Push(lua.LString(state.PlayerState()))
		} else {
			L.Push(lua.LString("stopped"))
		}
		return 1
	}))

	// cliamp.player.position() -> number (seconds)
	L.SetField(tbl, "position", L.NewFunction(func(L *lua.LState) int {
		state := loadState()
		if state.Position != nil {
			L.Push(lua.LNumber(state.Position()))
		} else {
			L.Push(lua.LNumber(0))
		}
		return 1
	}))

	// cliamp.player.duration() -> number (seconds)
	L.SetField(tbl, "duration", L.NewFunction(func(L *lua.LState) int {
		state := loadState()
		if state.Duration != nil {
			L.Push(lua.LNumber(state.Duration()))
		} else {
			L.Push(lua.LNumber(0))
		}
		return 1
	}))

	// cliamp.player.volume() -> number (dB)
	L.SetField(tbl, "volume", L.NewFunction(func(L *lua.LState) int {
		state := loadState()
		if state.Volume != nil {
			L.Push(lua.LNumber(state.Volume()))
		} else {
			L.Push(lua.LNumber(0))
		}
		return 1
	}))

	// cliamp.player.speed() -> number (ratio)
	L.SetField(tbl, "speed", L.NewFunction(func(L *lua.LState) int {
		state := loadState()
		if state.Speed != nil {
			L.Push(lua.LNumber(state.Speed()))
		} else {
			L.Push(lua.LNumber(1))
		}
		return 1
	}))

	// cliamp.player.mono() -> boolean
	L.SetField(tbl, "mono", L.NewFunction(func(L *lua.LState) int {
		state := loadState()
		if state.Mono != nil {
			L.Push(lua.LBool(state.Mono()))
		} else {
			L.Push(lua.LFalse)
		}
		return 1
	}))

	// cliamp.player.repeat_mode() -> "Off" | "All" | "One"
	L.SetField(tbl, "repeat_mode", L.NewFunction(func(L *lua.LState) int {
		state := loadState()
		if state.RepeatMode != nil {
			L.Push(lua.LString(state.RepeatMode()))
		} else {
			L.Push(lua.LString("Off"))
		}
		return 1
	}))

	// cliamp.player.shuffle() -> boolean
	L.SetField(tbl, "shuffle", L.NewFunction(func(L *lua.LState) int {
		state := loadState()
		if state.Shuffle != nil {
			L.Push(lua.LBool(state.Shuffle()))
		} else {
			L.Push(lua.LFalse)
		}
		return 1
	}))

	// cliamp.player.eq_bands() -> table of 10 dB values
	L.SetField(tbl, "eq_bands", L.NewFunction(func(L *lua.LState) int {
		state := loadState()
		var bands []float64
		if state.EQBands != nil {
			b := state.EQBands()
			bands = b[:]
		}
		L.Push(floatsToTable(L, bands))
		return 1
	}))

	L.SetField(cliamp, "player", tbl)
}
