package luaplugin

import (
	"strings"

	"github.com/bjarneo/cliamp/ui"
	lua "github.com/yuin/gopher-lua"
)

// registerUIAPI adds cliamp.ui.select. It lets a plugin ask the user to pick
// one of several strings, which is the piece a plugin needs to build its own
// settings menu: choose a visualizer, a weight, a layout pane, and so on.
// Gated on permissions = {"ui"}.
func registerUIAPI(L *lua.LState, cliamp *lua.LTable, p *Plugin, m *Manager) {
	tbl := L.NewTable()

	// cliamp.ui.select{title = "...", options = {"a", "b"}, on_select = fn}
	L.SetField(tbl, "select", L.NewFunction(func(L *lua.LState) int {
		if !p.permitted(PermUI, "cliamp.ui.select") {
			return pushErr(L, "ui permission required")
		}
		opts := L.CheckTable(1)
		title := strings.TrimSpace(rawString(L, opts, "title"))
		fn, ok := opts.RawGetString("on_select").(*lua.LFunction)
		if !ok {
			return pushErr(L, "cliamp.ui.select: on_select is required")
		}
		var options []string
		if list, ok := opts.RawGetString("options").(*lua.LTable); ok {
			n := list.Len()
			options = make([]string, 0, n)
			for i := 1; i <= n; i++ {
				options = append(options, list.RawGetInt(i).String())
			}
		}
		if len(options) == 0 {
			return pushErr(L, "cliamp.ui.select: options is empty")
		}
		up := m.ui.Load()
		if up == nil || up.ShowSelect == nil {
			return pushErr(L, "ui: not available")
		}
		cursor := optIntField(L, opts, "cursor")
		cancelFn, _ := opts.RawGetString("on_cancel").(*lua.LFunction)
		var onCancel func()
		if cancelFn != nil {
			onCancel = func() {
				m.enqueueSafe(p, "ui.select cancel", func() {
					m.call(p, "ui.select cancel", hookTimeout, 0, fixedArgs(cancelFn))
				})
			}
		}
		up.ShowSelect(title, options, cursor, func(index int) {
			arg := lua.LNumber(-1)
			if index >= 0 {
				arg = lua.LNumber(index + 1)
			}
			// Queue the callback under m.mu so Close cannot race with a send.
			m.enqueueSafe(p, "ui.select", func() {
				m.call(p, "ui.select", hookTimeout, 0, fixedArgs(fn, arg))
			})
		}, onCancel)
		L.Push(lua.LTrue)
		return 1
	}))

	vis := L.NewTable()
	// cliamp.vis.set("Scatter")
	L.SetField(vis, "set", L.NewFunction(func(L *lua.LState) int {
		if !p.permitted(PermUI, "cliamp.vis.set") {
			return pushErr(L, "ui permission required")
		}
		name := strings.TrimSpace(L.CheckString(1))
		up := m.ui.Load()
		if up == nil || up.SetVis == nil {
			return pushErr(L, "vis: not available")
		}
		up.SetVis(name)
		L.Push(lua.LTrue)
		return 1
	}))
	// cliamp.vis.list() -> {"Bars", "Rain", ...}
	L.SetField(vis, "list", L.NewFunction(func(L *lua.LState) int {
		if !p.permitted(PermUI, "cliamp.vis.list") {
			return pushErr(L, "ui permission required")
		}
		table := L.NewTable()
		names := ui.VisualizerNames()
		names = append(names, m.Visualizers()...)
		for i, name := range names {
			table.RawSetInt(i+1, lua.LString(name))
		}
		L.Push(table)
		return 1
	}))
	L.SetField(cliamp, "vis", vis)

	L.SetField(cliamp, "ui", tbl)
}
