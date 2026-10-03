package luaplugin

import (
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func newTrackState(t *testing.T, state *StateProvider) *lua.LState {
	t.Helper()
	L := lua.NewState()
	t.Cleanup(L.Close)
	cliamp := L.NewTable()
	registerTrackAPI(L, cliamp, fixed(state))
	L.SetGlobal("cliamp", cliamp)
	return L
}

// Each cliamp.track function returns one field of CurrentTrack, and the zero
// value when no state provider is set.
func TestTrackGetters(t *testing.T) {
	track := Track{
		Title: "Angel", Artist: "Massive Attack", Album: "Mezzanine", Genre: "Trip hop",
		Path: "/angel.flac", Year: 1998, Number: 1, Duration: 379, Stream: true, Live: true,
	}
	full := &StateProvider{CurrentTrack: func() Track { return track }}
	tests := []struct {
		fn       string
		want     lua.LValue
		fallback lua.LValue
	}{
		{"title", lua.LString("Angel"), lua.LString("")},
		{"artist", lua.LString("Massive Attack"), lua.LString("")},
		{"album", lua.LString("Mezzanine"), lua.LString("")},
		{"genre", lua.LString("Trip hop"), lua.LString("")},
		{"year", lua.LNumber(1998), lua.LNumber(0)},
		{"track_number", lua.LNumber(1), lua.LNumber(0)},
		{"path", lua.LString("/angel.flac"), lua.LString("")},
		{"is_stream", lua.LTrue, lua.LFalse},
		{"is_live", lua.LTrue, lua.LFalse},
		{"duration_secs", lua.LNumber(379), lua.LNumber(0)},
	}
	if len(tests) != len(trackGetters) {
		t.Fatalf("the test covers %d getters, want all %d", len(tests), len(trackGetters))
	}
	for _, tt := range tests {
		for _, c := range []struct {
			name  string
			state *StateProvider
			want  lua.LValue
		}{{"track", full, tt.want}, {"no provider", &StateProvider{}, tt.fallback}} {
			t.Run(tt.fn+"/"+c.name, func(t *testing.T) {
				L := newTrackState(t, c.state)
				if err := L.DoString(`_G.got = cliamp.track.` + tt.fn + `()`); err != nil {
					t.Fatal(err)
				}
				if got := L.GetGlobal("got"); got != c.want {
					t.Errorf("cliamp.track.%s() = %v, want %v", tt.fn, got, c.want)
				}
			})
		}
	}
}

// Event data, queue.list rows and queue.add share one track table, so a
// table built from TrackData reads back as the same track. The table has no
// track number and no live flag.
func TestTrackDataRoundTrips(t *testing.T) {
	tests := []struct {
		name  string
		track Track
	}{
		{"local file", Track{Title: "A", Artist: "B", Album: "C", Genre: "D", Path: "/a.mp3", Year: 1999, Duration: 60}},
		{"stream", Track{Title: "Radio", Path: "https://radio.example.com/live", Stream: true}},
		{"path only", Track{Path: "/b.mp3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			L := lua.NewState()
			defer L.Close()
			withExtras := tt.track
			withExtras.Number, withExtras.Live = 7, true
			data := TrackData(withExtras)
			if len(data) != len(trackFields) {
				t.Fatalf("TrackData has %d keys, want %d", len(data), len(trackFields))
			}
			got, err := trackFromTable(dataToTable(L, data))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.track {
				t.Errorf("round trip = %+v, want %+v", got, tt.track)
			}
		})
	}
}
