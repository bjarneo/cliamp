package luaplugin

import (
	"time"

	lua "github.com/yuin/gopher-lua"
)

// messageMaxDuration caps how long a plugin-supplied status message can stay on
// screen. Plugins can request longer durations, but they get clamped so a
// runaway script cannot pin the status bar indefinitely.
const messageMaxDuration = 60 * time.Second

// registerMessageAPI adds cliamp.message(text, duration_secs?) which displays a
// temporary message in the status bar at the bottom of the UI. A missing or
// non-positive duration falls back to the default status TTL (set by the UI).
func registerMessageAPI(L *lua.LState, cliamp *lua.LTable, loadUI func() *UIProvider) {
	L.SetField(cliamp, "message", L.NewFunction(func(L *lua.LState) int {
		ui := loadUI()
		if ui.ShowMessage == nil {
			return 0
		}
		text := L.CheckString(1)
		var dur time.Duration
		if secs := float64(L.OptNumber(2, 0)); secs > 0 {
			dur = min(time.Duration(secs*float64(time.Second)), messageMaxDuration)
		}
		ui.ShowMessage(text, dur)
		return 0
	}))
}
