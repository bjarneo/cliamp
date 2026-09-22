package luaplugin

import (
	lua "github.com/yuin/gopher-lua"
)

// clockMarker wraps the time a visualizer plugin wants drawn as images. It
// must match ui.ClockMarker, which is what recognizes it; the test in this
// package keeps the two from drifting.
const clockMarker = "\x00"

// registerClockAPI adds cliamp.clock(text, fallback), which marks a piece of
// a visualizer frame as a clock face.
//
// cliamp draws the marked text as real type — glyph images sized to the
// panel, transmitted through the terminal's graphics protocol — and draws the
// fallback instead where that is not possible: an older terminal, one that
// does not report its cell size, a panel too small to be worth it. The plugin
// cannot tell the difference and does not need to.
//
// The function is absent on versions of cliamp that cannot do this, which is
// how a plugin asks:
//
//	local face = block_characters(text)
//	return cliamp.clock and cliamp.clock(text, face) or face
func registerClockAPI(L *lua.LState, cliamp *lua.LTable) {
	L.SetField(cliamp, "clock", L.NewFunction(func(L *lua.LState) int {
		text := L.CheckString(1)
		fallback := L.OptString(2, "")
		L.Push(lua.LString(clockMarker + text + clockMarker + fallback))
		return 1
	}))
}
