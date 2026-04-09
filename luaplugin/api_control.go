package luaplugin

import lua "github.com/yuin/gopher-lua"

// registerControlAPI adds cliamp.player control methods (next, prev, play_pause,
// stop, set_volume, set_speed, seek, toggle_mono, set_eq_band) to the cliamp table.
// These are only functional if the plugin declared permissions = {"control"}.
func registerControlAPI(L *lua.LState, cliamp *lua.LTable, ctrl *ControlProvider, p *Plugin, logger *pluginLogger) {
	playerTbl := L.GetField(cliamp, "player")
	tbl, ok := playerTbl.(*lua.LTable)
	if !ok {
		return
	}

	warned := false
	guard := func(name string) bool {
		if !p.perms["control"] {
			if !warned {
				logger.log(p.Name, "warn", "%s requires permissions = {\"control\"} — further warnings suppressed", name)
				warned = true
			}
			return false
		}
		return true
	}

	L.SetField(tbl, "next", L.NewFunction(func(L *lua.LState) int {
		if guard("next") {
			ctrl.Next()
		}
		return 0
	}))

	L.SetField(tbl, "prev", L.NewFunction(func(L *lua.LState) int {
		if guard("prev") {
			ctrl.Prev()
		}
		return 0
	}))

	L.SetField(tbl, "play_pause", L.NewFunction(func(L *lua.LState) int {
		if guard("play_pause") {
			ctrl.TogglePause()
		}
		return 0
	}))

	L.SetField(tbl, "stop", L.NewFunction(func(L *lua.LState) int {
		if guard("stop") {
			ctrl.Stop()
		}
		return 0
	}))

	L.SetField(tbl, "set_volume", L.NewFunction(func(L *lua.LState) int {
		if !guard("set_volume") {
			return 0
		}
		db := float64(L.CheckNumber(1))
		ctrl.SetVolume(max(min(db, 6), -30))
		return 0
	}))

	L.SetField(tbl, "set_speed", L.NewFunction(func(L *lua.LState) int {
		if !guard("set_speed") {
			return 0
		}
		ratio := float64(L.CheckNumber(1))
		ctrl.SetSpeed(max(min(ratio, 2.0), 0.25))
		return 0
	}))

	L.SetField(tbl, "seek", L.NewFunction(func(L *lua.LState) int {
		if !guard("seek") {
			return 0
		}
		ctrl.Seek(float64(L.CheckNumber(1)))
		return 0
	}))

	L.SetField(tbl, "toggle_mono", L.NewFunction(func(L *lua.LState) int {
		if guard("toggle_mono") {
			ctrl.ToggleMono()
		}
		return 0
	}))

	// set_eq_preset("name") or set_eq_preset("name", {band1, band2, ..., band10})
	L.SetField(tbl, "set_eq_preset", L.NewFunction(func(L *lua.LState) int {
		if !guard("set_eq_preset") {
			return 0
		}
		name := L.CheckString(1)
		var bands *[10]float64
		if tbl := L.OptTable(2, nil); tbl != nil {
			b := [10]float64{}
			for i := range 10 {
				if v := tbl.RawGetInt(i + 1); v != lua.LNil {
					b[i] = max(min(float64(lua.LVAsNumber(v)), 12), -12)
				}
			}
			bands = &b
		}
		ctrl.SetEQPreset(name, bands)
		return 0
	}))

	L.SetField(tbl, "set_eq_band", L.NewFunction(func(L *lua.LState) int {
		if !guard("set_eq_band") {
			return 0
		}
		band := L.CheckInt(1) - 1 // Lua 1-indexed → Go 0-indexed
		if band < 0 || band > 9 {
			L.ArgError(1, "band must be 1-10")
			return 0
		}
		db := float64(L.CheckNumber(2))
		ctrl.SetEQBand(band, max(min(db, 12), -12))
		return 0
	}))
}
