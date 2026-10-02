package luaplugin

import lua "github.com/yuin/gopher-lua"

// trackGetters are the cliamp.track.* functions. Each returns one field of
// the current track.
var trackGetters = []struct {
	name string
	get  func(Track) lua.LValue
}{
	{"title", func(t Track) lua.LValue { return lua.LString(t.Title) }},
	{"artist", func(t Track) lua.LValue { return lua.LString(t.Artist) }},
	{"album", func(t Track) lua.LValue { return lua.LString(t.Album) }},
	{"genre", func(t Track) lua.LValue { return lua.LString(t.Genre) }},
	{"year", func(t Track) lua.LValue { return lua.LNumber(t.Year) }},
	{"track_number", func(t Track) lua.LValue { return lua.LNumber(t.Number) }},
	{"path", func(t Track) lua.LValue { return lua.LString(t.Path) }},
	{"is_stream", func(t Track) lua.LValue { return lua.LBool(t.Stream) }},
	// is_live is true for live streams, which have no track boundary and
	// never advance on their own: radio stations, and streams that are live
	// now. A yt-dlp track flagged live stops counting once the player knows
	// its duration, as the broadcast has become a recording.
	{"is_live", func(t Track) lua.LValue { return lua.LBool(t.Live) }},
	{"duration_secs", func(t Track) lua.LValue { return lua.LNumber(t.Duration) }},
}

// registerTrackAPI adds the read-only cliamp.track.* table.
func registerTrackAPI(L *lua.LState, cliamp *lua.LTable, loadState func() *StateProvider) {
	tbl := L.NewTable()
	for _, g := range trackGetters {
		L.SetField(tbl, g.name, L.NewFunction(func(L *lua.LState) int {
			var track Track
			if state := loadState(); state.CurrentTrack != nil {
				track = state.CurrentTrack()
			}
			L.Push(g.get(track))
			return 1
		}))
	}
	L.SetField(cliamp, "track", tbl)
}

// trackFields are the keys of a track table. Events, the rows of
// cliamp.queue.list and the table that cliamp.queue.add takes share them.
// trackFromTable reads the same keys.
var trackFields = []struct {
	key string
	get func(Track) any
}{
	{"title", func(t Track) any { return t.Title }},
	{"artist", func(t Track) any { return t.Artist }},
	{"album", func(t Track) any { return t.Album }},
	{"genre", func(t Track) any { return t.Genre }},
	{"year", func(t Track) any { return t.Year }},
	{"path", func(t Track) any { return t.Path }},
	{"duration", func(t Track) any { return t.Duration }},
	{"stream", func(t Track) any { return t.Stream }},
}

// TrackData returns the track table of t as event data.
func TrackData(t Track) map[string]any {
	data := make(map[string]any, len(trackFields))
	for _, f := range trackFields {
		data[f.key] = f.get(t)
	}
	return data
}
