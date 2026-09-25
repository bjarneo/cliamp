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
	registerTrackAPI(L, cliamp, state)
	L.SetGlobal("cliamp", cliamp)
	return L
}

func TestTrackIsLive(t *testing.T) {
	tests := []struct {
		name  string
		state *StateProvider
		want  bool
	}{
		{name: "no provider", state: &StateProvider{}, want: false},
		{name: "live", state: &StateProvider{TrackIsLive: func() bool { return true }}, want: true},
		{name: "not live", state: &StateProvider{TrackIsLive: func() bool { return false }}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			L := newTrackState(t, tt.state)
			if err := L.DoString(`_G.live = cliamp.track.is_live()`); err != nil {
				t.Fatal(err)
			}
			if got := bool(L.GetGlobal("live").(lua.LBool)); got != tt.want {
				t.Errorf("is_live = %v, want %v", got, tt.want)
			}
		})
	}
}
