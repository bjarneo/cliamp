package luaplugin

import (
	"strings"

	"github.com/bjarneo/cliamp/ui"
	lua "github.com/yuin/gopher-lua"
)

// LayoutProvider is the UI side of cliamp.layout: a plugin asks for a pane in
// one of the frame's regions and the core owns the geometry.
type LayoutProvider interface {
	AddPane(region, pane string, weight int) error
	AddPaneAt(region, pane string, weight int, position string) error
	RemovePane(region, pane string)
	Panes() []ui.PaneSpec
	Builtin(region string, enabled bool)
	Weight(region, pane string, weight int)
}

// SetLayoutProvider wires the layout registry. Without it, cliamp.layout is a
// no-op table.
func (m *Manager) SetLayoutProvider(lp LayoutProvider) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.layoutProvider = lp
	m.mu.Unlock()
}

// registerLayoutAPI adds cliamp.layout.{add,remove,list}. A plugin can only add
// its own panes, so the pane name defaults to the plugin name.
// Gated on permissions = {"layout"}.
func registerLayoutAPI(L *lua.LState, cliamp *lua.LTable, p *Plugin, mgr *Manager) {
	tbl := L.NewTable()
	guard := func(name string) bool { return p.permitted(PermLayout, "cliamp.layout."+name) }

	// A plugin owns only its own pane; set_weight may also touch the builtin
	// content of the region.
	owned := func(name string, allowBuiltin bool) (string, bool) {
		if name == "" {
			return p.Name, true
		}
		if name == p.Name || (allowBuiltin && name == "builtin") {
			return name, true
		}
		return "", false
	}

	provider := func() LayoutProvider {
		mgr.mu.RLock()
		defer mgr.mu.RUnlock()
		return mgr.layoutProvider
	}

	// cliamp.layout.add({region = "visualizer", pane = "cover", weight = 1})
	L.SetField(tbl, "add", L.NewFunction(func(L *lua.LState) int {
		if !guard("add") {
			return pushErr(L, "layout permission required")
		}
		opts := L.CheckTable(1)
		region := strings.TrimSpace(rawString(L, opts, "region"))
		pane, ok := owned(strings.TrimSpace(rawString(L, opts, "pane")), false)
		if !ok {
			return pushErr(L, "layout: a plugin can only add its own pane")
		}
		weight := optIntField(L, opts, "weight")
		position := strings.TrimSpace(rawString(L, opts, "position"))
		lp := provider()
		if lp == nil {
			return pushErr(L, "layout: not available")
		}
		if err := lp.AddPaneAt(region, pane, weight, position); err != nil {
			return pushErr(L, err.Error())
		}
		L.Push(lua.LTrue)
		return 1
	}))

	// cliamp.layout.remove({region = "visualizer", pane = "cover"})
	L.SetField(tbl, "remove", L.NewFunction(func(L *lua.LState) int {
		if !guard("remove") {
			return pushErr(L, "layout permission required")
		}
		opts := L.CheckTable(1)
		region := strings.TrimSpace(rawString(L, opts, "region"))
		pane, ok := owned(strings.TrimSpace(rawString(L, opts, "pane")), false)
		if !ok {
			return pushErr(L, "layout: a plugin can only remove its own pane")
		}
		lp := provider()
		if lp == nil {
			return pushErr(L, "layout: not available")
		}
		lp.RemovePane(region, pane)
		L.Push(lua.LTrue)
		return 1
	}))

	// cliamp.layout.set_vis({region = "visualizer", pane = "cover", vis = "Scatter"})
	L.SetField(tbl, "set_vis", L.NewFunction(func(L *lua.LState) int {
		if !guard("set_vis") {
			return pushErr(L, "layout permission required")
		}
		opts := L.CheckTable(1)
		region := strings.TrimSpace(rawString(L, opts, "region"))
		pane, ok := owned(strings.TrimSpace(rawString(L, opts, "pane")), false)
		if !ok {
			return pushErr(L, "layout: a plugin can only set the visualizer of its own pane")
		}
		vis := strings.TrimSpace(rawString(L, opts, "vis"))
		if setter, ok := provider().(interface {
			SetVis(region, pane, vis string)
		}); ok {
			setter.SetVis(region, pane, vis)
			L.Push(lua.LTrue)
			return 1
		}
		return pushErr(L, "layout: set_vis not available")
	}))

	// cliamp.layout.list() -> {{region=, pane=, weight=}, ...}
	L.SetField(tbl, "list", L.NewFunction(func(L *lua.LState) int {
		if !guard("list") {
			return pushErr(L, "layout permission required")
		}
		lp := provider()
		if lp == nil {
			return pushErr(L, "layout: not available")
		}
		panes := lp.Panes()
		out := L.NewTable()
		for i, pane := range panes {
			row := L.NewTable()
			L.SetField(row, "region", lua.LString(pane.Region))
			L.SetField(row, "pane", lua.LString(pane.Pane))
			L.SetField(row, "weight", lua.LNumber(pane.Weight))
			L.SetField(row, "vis", lua.LString(pane.Vis))
			L.SetField(row, "position", lua.LString(pane.Position))
			out.RawSetInt(i+1, row)
		}
		L.Push(out)
		return 1
	}))

	// cliamp.layout.set_builtin({region = "visualizer", enabled = false})
	L.SetField(tbl, "set_builtin", L.NewFunction(func(L *lua.LState) int {
		if !guard("set_builtin") {
			return pushErr(L, "layout permission required")
		}
		opts := L.CheckTable(1)
		region := strings.TrimSpace(rawString(L, opts, "region"))
		enabled := true
		if value, ok := opts.RawGetString("enabled").(lua.LBool); ok {
			enabled = bool(value)
		}
		lp := provider()
		if lp == nil {
			return pushErr(L, "layout: not available")
		}
		lp.Builtin(region, enabled)
		L.Push(lua.LTrue)
		return 1
	}))

	// cliamp.layout.set_weight({region = "visualizer", pane = "cover", weight = 2})
	L.SetField(tbl, "set_weight", L.NewFunction(func(L *lua.LState) int {
		if !guard("set_weight") {
			return pushErr(L, "layout permission required")
		}
		opts := L.CheckTable(1)
		region := strings.TrimSpace(rawString(L, opts, "region"))
		pane, ok := owned(strings.TrimSpace(rawString(L, opts, "pane")), true)
		if !ok {
			return pushErr(L, "layout: a plugin can only set the weight of its own pane")
		}
		lp := provider()
		if lp == nil {
			return pushErr(L, "layout: not available")
		}
		lp.Weight(region, pane, optIntField(L, opts, "weight"))
		L.Push(lua.LTrue)
		return 1
	}))

	L.SetField(cliamp, "layout", tbl)
}

// rawString reads a string field from a Lua table.
func rawString(L *lua.LState, tbl *lua.LTable, key string) string {
	if value, ok := tbl.RawGetString(key).(lua.LString); ok {
		return string(value)
	}
	return ""
}
