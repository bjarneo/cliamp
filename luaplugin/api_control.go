package luaplugin

import lua "github.com/yuin/gopher-lua"

// registerControlAPI adds cliamp.player control methods (next, prev, play_pause,
// stop, set_volume, set_speed, seek, toggle_mono, set_eq_band) to the cliamp table.
// These are only functional if the plugin declared permissions = {"control"}.
// Before main sets the ControlProvider, such as in the top-level chunk, each
// control does nothing.
func registerControlAPI(L *lua.LState, cliamp *lua.LTable, loadCtrl func() *ControlProvider, p *Plugin) {
	playerTbl := L.GetField(cliamp, "player")
	tbl, ok := playerTbl.(*lua.LTable)
	if !ok {
		return
	}

	guard := func(name string) bool { return p.permitted(PermControl, "cliamp.player."+name) }

	L.SetField(tbl, "next", L.NewFunction(func(L *lua.LState) int {
		ctrl := loadCtrl()
		if guard("next") && ctrl.Next != nil {
			ctrl.Next()
		}
		return 0
	}))

	L.SetField(tbl, "prev", L.NewFunction(func(L *lua.LState) int {
		ctrl := loadCtrl()
		if guard("prev") && ctrl.Prev != nil {
			ctrl.Prev()
		}
		return 0
	}))

	L.SetField(tbl, "play_pause", L.NewFunction(func(L *lua.LState) int {
		ctrl := loadCtrl()
		if guard("play_pause") && ctrl.TogglePause != nil {
			ctrl.TogglePause()
		}
		return 0
	}))

	L.SetField(tbl, "stop", L.NewFunction(func(L *lua.LState) int {
		ctrl := loadCtrl()
		if guard("stop") && ctrl.Stop != nil {
			ctrl.Stop()
		}
		return 0
	}))

	L.SetField(tbl, "set_volume", L.NewFunction(func(L *lua.LState) int {
		ctrl := loadCtrl()
		if !guard("set_volume") {
			return 0
		}
		db := float64(L.CheckNumber(1))
		// The player clamps the low end to its volume_min floor.
		if ctrl.SetVolume != nil {
			ctrl.SetVolume(min(db, 6))
		}
		return 0
	}))

	L.SetField(tbl, "set_speed", L.NewFunction(func(L *lua.LState) int {
		ctrl := loadCtrl()
		if !guard("set_speed") {
			return 0
		}
		ratio := float64(L.CheckNumber(1))
		if ctrl.SetSpeed != nil {
			ctrl.SetSpeed(max(min(ratio, 2.0), 0.25))
		}
		return 0
	}))

	L.SetField(tbl, "seek", L.NewFunction(func(L *lua.LState) int {
		ctrl := loadCtrl()
		if !guard("seek") {
			return 0
		}
		secs := float64(L.CheckNumber(1))
		if ctrl.Seek != nil {
			ctrl.Seek(secs)
		}
		return 0
	}))

	L.SetField(tbl, "toggle_mono", L.NewFunction(func(L *lua.LState) int {
		ctrl := loadCtrl()
		if guard("toggle_mono") && ctrl.ToggleMono != nil {
			ctrl.ToggleMono()
		}
		return 0
	}))

	// set_eq_preset("name") or set_eq_preset("name", {band1, band2, ..., band10})
	L.SetField(tbl, "set_eq_preset", L.NewFunction(func(L *lua.LState) int {
		ctrl := loadCtrl()
		if !guard("set_eq_preset") {
			return 0
		}
		name := L.CheckString(1)
		var bands *[10]float64
		if tbl := L.OptTable(2, nil); tbl != nil {
			b := [10]float64{}
			for i := range 10 {
				v := tbl.RawGetInt(i + 1)
				if v == lua.LNil {
					// A partial table would silently zero the unset bands;
					// require all 10 so the caller's intent is explicit.
					L.ArgError(2, "eq bands table must contain all 10 values")
					return 0
				}
				b[i] = max(min(float64(lua.LVAsNumber(v)), 12), -12)
			}
			bands = &b
		}
		if ctrl.SetEQPreset != nil {
			ctrl.SetEQPreset(name, bands)
		}
		return 0
	}))

	L.SetField(tbl, "set_eq_band", L.NewFunction(func(L *lua.LState) int {
		ctrl := loadCtrl()
		if !guard("set_eq_band") {
			return 0
		}
		band := L.CheckInt(1) - 1 // Lua 1-indexed → Go 0-indexed
		if band < 0 || band > 9 {
			L.ArgError(1, "band must be 1-10")
			return 0
		}
		db := float64(L.CheckNumber(2))
		if ctrl.SetEQBand != nil {
			ctrl.SetEQBand(band, max(min(db, 12), -12))
		}
		return 0
	}))
}
