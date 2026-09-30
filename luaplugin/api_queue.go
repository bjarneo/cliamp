package luaplugin

import (
	"fmt"
	"math"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// registerQueueAPI adds cliamp.queue.* to the cliamp table.
//
// Reads need no permission and pull from the StateProvider.
// Mutators (add/jump/remove/move) require permissions = {"control"} and route
// through the ControlProvider, which dispatches them onto the UI loop.
//
// All indices are 0-based, matching cliamp.queue.current().
func registerQueueAPI(L *lua.LState, cliamp *lua.LTable, state *StateProvider, ctrl *ControlProvider, p *Plugin, logger *pluginLogger) {
	tbl := L.NewTable()

	// cliamp.queue.list() -> array of {title, artist, album, genre, year, path,
	// duration, stream, index, queued}
	L.SetField(tbl, "list", L.NewFunction(func(L *lua.LState) int {
		out := L.NewTable()
		if state.QueueList != nil {
			for i, e := range state.QueueList() {
				row := L.NewTable()
				row.RawSetString("title", lua.LString(e.Title))
				row.RawSetString("artist", lua.LString(e.Artist))
				row.RawSetString("album", lua.LString(e.Album))
				row.RawSetString("genre", lua.LString(e.Genre))
				row.RawSetString("year", lua.LNumber(e.Year))
				row.RawSetString("path", lua.LString(e.Path))
				row.RawSetString("duration", lua.LNumber(e.Duration))
				row.RawSetString("stream", lua.LBool(e.Stream))
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

	// cliamp.queue.has_next() -> whether a playable track follows the current one
	L.SetField(tbl, "has_next", L.NewFunction(func(L *lua.LState) int {
		if state.HasNext != nil {
			L.Push(lua.LBool(state.HasNext()))
		} else {
			L.Push(lua.LFalse)
		}
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
	// cliamp.queue.add(track) -> true | nil, err — append the track a table
	// describes, as given, without resolving its path.
	L.SetField(tbl, "add", L.NewFunction(func(L *lua.LState) int {
		if t, ok := L.Get(1).(*lua.LTable); ok {
			var track QueueTrack
			var err error
			switch {
			case !guard("add"):
				err = fmt.Errorf("requires permissions = {\"control\"}")
			case ctrl.QueueAddTrack == nil:
				err = fmt.Errorf("unavailable")
			default:
				track, err = queueTrackFromTable(t)
			}
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString("queue.add: " + err.Error()))
				return 2
			}
			ctrl.QueueAddTrack(track)
			L.Push(lua.LTrue)
			return 1
		}
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

// maxTrackNumber caps year and duration in a track table. It fits an int on
// every platform, and a duration this long still fits a time.Duration.
const maxTrackNumber = math.MaxInt32

// queueTrackFromTable reads a track table in the shape plugins receive in
// events ({title, artist, album, genre, year, path, duration, stream}).
// Only path is required. Other keys are ignored, so a table from an event or
// from queue.list can be passed straight back.
func queueTrackFromTable(t *lua.LTable) (QueueTrack, error) {
	var track QueueTrack
	path, ok := t.RawGetString("path").(lua.LString)
	if !ok || strings.TrimSpace(string(path)) == "" {
		return track, fmt.Errorf("path must be a non-empty string")
	}
	track.Path = string(path)
	for _, f := range []struct {
		key string
		dst *string
	}{{"title", &track.Title}, {"artist", &track.Artist}, {"album", &track.Album}, {"genre", &track.Genre}} {
		switch v := t.RawGetString(f.key).(type) {
		case *lua.LNilType:
		case lua.LString:
			*f.dst = string(v)
		default:
			return track, fmt.Errorf("%s must be a string, got %s", f.key, v.Type())
		}
	}
	for _, f := range []struct {
		key string
		dst *int
	}{{"year", &track.Year}, {"duration", &track.Duration}} {
		switch v := t.RawGetString(f.key).(type) {
		case *lua.LNilType:
		case lua.LNumber:
			n := float64(v)
			if !(n >= 0 && n <= maxTrackNumber) {
				return track, fmt.Errorf("%s must be a number from 0 to %d", f.key, maxTrackNumber)
			}
			*f.dst = int(n)
		default:
			return track, fmt.Errorf("%s must be a number, got %s", f.key, v.Type())
		}
	}
	switch v := t.RawGetString("stream").(type) {
	case *lua.LNilType:
	case lua.LBool:
		track.Stream = bool(v)
	default:
		return track, fmt.Errorf("stream must be a boolean, got %s", v.Type())
	}
	return track, nil
}
