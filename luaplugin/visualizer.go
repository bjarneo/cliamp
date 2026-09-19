package luaplugin

import (
	"time"

	lua "github.com/yuin/gopher-lua"
)

// luaVis wraps a Lua visualizer plugin, caching function references
// for render() and optional init()/destroy() callbacks.
type luaVis struct {
	name    string
	plugin  *Plugin // owns the LState and mutex
	obj     *lua.LTable
	render  *lua.LFunction
	init    *lua.LFunction
	destroy *lua.LFunction
	last    string // previous frame output (reused on error)
}

// registerVisPlugin is called during plugin.register() for type="visualizer".
func (m *Manager) registerVisPlugin(L *lua.LState, obj *lua.LTable, p *Plugin) {
	vis := &luaVis{
		name:   p.Name,
		plugin: p,
		obj:    obj,
	}

	m.mu.Lock()
	m.visPlugs = append(m.visPlugs, vis)
	m.visMap[p.Name] = vis
	m.mu.Unlock()
}

// finalizeVisualizers is called after all plugins are loaded to resolve
// render/init/destroy function references from the plugin objects.
func (m *Manager) finalizeVisualizers() {
	for _, vis := range m.visPlugs {
		if fn, ok := vis.obj.RawGetString("render").(*lua.LFunction); ok {
			vis.render = fn
		}
		if fn, ok := vis.obj.RawGetString("init").(*lua.LFunction); ok {
			vis.init = fn
		}
		if fn, ok := vis.obj.RawGetString("destroy").(*lua.LFunction); ok {
			vis.destroy = fn
		}
	}
}

// Visualizers returns the names of all Lua visualizer plugins.
func (m *Manager) Visualizers() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, len(m.visPlugs))
	for i, v := range m.visPlugs {
		names[i] = v.name
	}
	return names
}

// lockPluginBounded waits briefly for a plugin's Lua state instead of forever.
// InitVis and DestroyVis run on the UI loop, and a hook goroutine can be
// holding this lock while blocked on a call back into that same loop, so an
// unbounded wait would freeze the program. These are one-shot on a mode
// switch, so a short wait almost always succeeds; giving up merely skips
// optional setup or teardown.
func lockPluginBounded(v *luaVis) bool {
	const deadline = 100 * time.Millisecond
	const step = 2 * time.Millisecond
	for waited := time.Duration(0); waited < deadline; waited += step {
		if v.plugin.mu.TryLock() {
			return true
		}
		time.Sleep(step)
	}
	return false
}

// InitVis calls a Lua visualizer's init(rows, cols) if it exists.
func (m *Manager) InitVis(name string, rows, cols int) {
	m.mu.RLock()
	vis, ok := m.visMap[name]
	m.mu.RUnlock()
	if !ok || vis.init == nil {
		return
	}

	if !lockPluginBounded(vis) {
		return
	}
	defer vis.plugin.mu.Unlock()

	_ = vis.plugin.callBounded(0, vis.init, vis.obj, lua.LNumber(rows), lua.LNumber(cols))
}

// DestroyVis calls a Lua visualizer's destroy() if it exists.
func (m *Manager) DestroyVis(name string) {
	m.mu.RLock()
	vis, ok := m.visMap[name]
	m.mu.RUnlock()
	if !ok || vis.destroy == nil {
		return
	}

	if !lockPluginBounded(vis) {
		return
	}
	defer vis.plugin.mu.Unlock()

	_ = vis.plugin.callBounded(0, vis.destroy, vis.obj)
}

// RenderVis calls a Lua visualizer's render(bands, frame) and returns
// the terminal text. On error, the previous frame is reused.
func (m *Manager) RenderVis(name string, bands [10]float64, rows, cols int, frame uint64) string {
	m.mu.RLock()
	vis, ok := m.visMap[name]
	m.mu.RUnlock()
	if !ok || vis.render == nil {
		return ""
	}

	// Never block the UI loop on the plugin's Lua state. A hook running in its
	// own goroutine holds this lock while it calls back into the UI, and those
	// calls block until the UI loop consumes them — so waiting here deadlocks
	// the program whenever a plugin is both the active visualizer and handling
	// an event. Reusing the previous frame is already how a slow render is
	// handled, so a busy plugin simply holds its last frame for a tick.
	if !vis.plugin.mu.TryLock() {
		return vis.last
	}
	defer vis.plugin.mu.Unlock()

	L := vis.plugin.L

	// Build bands table (1-indexed).
	tbl := L.NewTable()
	for i, b := range bands {
		tbl.RawSetInt(i+1, lua.LNumber(b))
	}

	err := vis.plugin.callBounded(1, vis.render, vis.obj, tbl, lua.LNumber(frame), lua.LNumber(rows), lua.LNumber(cols))
	if err != nil {
		return vis.last
	}

	result := L.Get(-1)
	L.Pop(1)

	if str, ok := result.(lua.LString); ok {
		vis.last = string(str)
		return vis.last
	}
	return vis.last
}
