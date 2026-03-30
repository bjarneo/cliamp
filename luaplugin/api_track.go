package luaplugin

import lua "github.com/yuin/gopher-lua"

// registerTrackAPI adds the read-only cliamp.track.* table.
func registerTrackAPI(L *lua.LState, cliamp *lua.LTable, state *StateProvider) {
	tbl := L.NewTable()

	L.SetField(tbl, "title", L.NewFunction(func(L *lua.LState) int {
		if state.TrackTitle != nil {
			L.Push(lua.LString(state.TrackTitle()))
		} else {
			L.Push(lua.LString(""))
		}
		return 1
	}))

	L.SetField(tbl, "artist", L.NewFunction(func(L *lua.LState) int {
		if state.TrackArtist != nil {
			L.Push(lua.LString(state.TrackArtist()))
		} else {
			L.Push(lua.LString(""))
		}
		return 1
	}))

	L.SetField(tbl, "album", L.NewFunction(func(L *lua.LState) int {
		if state.TrackAlbum != nil {
			L.Push(lua.LString(state.TrackAlbum()))
		} else {
			L.Push(lua.LString(""))
		}
		return 1
	}))

	L.SetField(tbl, "genre", L.NewFunction(func(L *lua.LState) int {
		if state.TrackGenre != nil {
			L.Push(lua.LString(state.TrackGenre()))
		} else {
			L.Push(lua.LString(""))
		}
		return 1
	}))

	L.SetField(tbl, "year", L.NewFunction(func(L *lua.LState) int {
		if state.TrackYear != nil {
			L.Push(lua.LNumber(state.TrackYear()))
		} else {
			L.Push(lua.LNumber(0))
		}
		return 1
	}))

	L.SetField(tbl, "track_number", L.NewFunction(func(L *lua.LState) int {
		if state.TrackNumber != nil {
			L.Push(lua.LNumber(state.TrackNumber()))
		} else {
			L.Push(lua.LNumber(0))
		}
		return 1
	}))

	L.SetField(tbl, "path", L.NewFunction(func(L *lua.LState) int {
		if state.TrackPath != nil {
			L.Push(lua.LString(state.TrackPath()))
		} else {
			L.Push(lua.LString(""))
		}
		return 1
	}))

	L.SetField(tbl, "is_stream", L.NewFunction(func(L *lua.LState) int {
		if state.TrackIsStream != nil {
			L.Push(lua.LBool(state.TrackIsStream()))
		} else {
			L.Push(lua.LFalse)
		}
		return 1
	}))

	L.SetField(tbl, "duration_secs", L.NewFunction(func(L *lua.LState) int {
		if state.TrackDuration != nil {
			L.Push(lua.LNumber(state.TrackDuration()))
		} else {
			L.Push(lua.LNumber(0))
		}
		return 1
	}))

	L.SetField(cliamp, "track", tbl)
}
