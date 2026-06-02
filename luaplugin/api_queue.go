package luaplugin

import lua "github.com/yuin/gopher-lua"

// registerQueueAPI adds cliamp.queue.* to the cliamp table.
//
// Reads (list/count/current) need no permission and pull from the StateProvider.
// Mutators (add/jump/remove/move) require permissions = {"control"} and route
// through the ControlProvider, which dispatches them onto the UI loop.
//
// All indices are 0-based, matching cliamp.queue.current().
func registerQueueAPI(L *lua.LState, cliamp *lua.LTable, state *StateProvider, ctrl *ControlProvider, p *Plugin, logger *pluginLogger) {
	tbl := L.NewTable()

	// cliamp.queue.list() -> array of {title, artist, album, path, index, queued}
	L.SetField(tbl, "list", L.NewFunction(func(L *lua.LState) int {
		out := L.NewTable()
		if state.QueueList != nil {
			for i, e := range state.QueueList() {
				row := L.NewTable()
				row.RawSetString("title", lua.LString(e.Title))
				row.RawSetString("artist", lua.LString(e.Artist))
				row.RawSetString("album", lua.LString(e.Album))
				row.RawSetString("path", lua.LString(e.Path))
				row.RawSetString("index", lua.LNumber(e.Index))
				row.RawSetString("queued", lua.LBool(e.Queued))
				out.RawSetInt(i+1, row)
			}
		}
		L.Push(out)
		return 1
	}))

	// cliamp.queue.count() -> number of tracks
	L.SetField(tbl, "count", L.NewFunction(func(L *lua.LState) int {
		n := 0
		if state.PlaylistCount != nil {
			n = state.PlaylistCount()
		}
		L.Push(lua.LNumber(n))
		return 1
	}))

	// cliamp.queue.current() -> 0-based index of the current track
	L.SetField(tbl, "current", L.NewFunction(func(L *lua.LState) int {
		idx := 0
		if state.CurrentIndex != nil {
			idx = state.CurrentIndex()
		}
		L.Push(lua.LNumber(idx))
		return 1
	}))

	warned := false
	guard := func(name string) bool {
		if !p.perms[PermControl] {
			if !warned {
				logger.log(p.Name, "warn", "queue.%s requires permissions = {\"control\"} — further warnings suppressed", name)
				warned = true
			}
			return false
		}
		return true
	}

	// cliamp.queue.add(path) — resolve a file/dir/URL and append to the playlist.
	L.SetField(tbl, "add", L.NewFunction(func(L *lua.LState) int {
		path := L.CheckString(1)
		if guard("add") && ctrl.QueueAdd != nil {
			ctrl.QueueAdd(path)
		}
		return 0
	}))

	// cliamp.queue.jump(index) — make index the current track and play it.
	L.SetField(tbl, "jump", L.NewFunction(func(L *lua.LState) int {
		index := L.CheckInt(1)
		if guard("jump") && ctrl.QueueJump != nil {
			ctrl.QueueJump(index)
		}
		return 0
	}))

	// cliamp.queue.remove(index) — remove the track at index.
	L.SetField(tbl, "remove", L.NewFunction(func(L *lua.LState) int {
		index := L.CheckInt(1)
		if guard("remove") && ctrl.QueueRemove != nil {
			ctrl.QueueRemove(index)
		}
		return 0
	}))

	// cliamp.queue.move(from, to) — reorder a track.
	L.SetField(tbl, "move", L.NewFunction(func(L *lua.LState) int {
		from := L.CheckInt(1)
		to := L.CheckInt(2)
		if guard("move") && ctrl.QueueMove != nil {
			ctrl.QueueMove(from, to)
		}
		return 0
	}))

	L.SetField(cliamp, "queue", tbl)
}
