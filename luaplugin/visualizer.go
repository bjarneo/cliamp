package luaplugin

import (
	"sync/atomic"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// renderTimeout bounds one render call. RenderVis runs on the UI goroutine,
// so a slow render must not delay the frame for long. The limit leaves room
// for a full-screen render on a slow CPU, which can take more than 20 ms. It
// is a var so tests can change it.
var renderTimeout = 50 * time.Millisecond

// luaVis wraps a Lua visualizer plugin, caching function references
// for render() and optional init()/destroy() callbacks.
type luaVis struct {
	name    string
	plugin  *Plugin // owns the LState and mutex
	obj     *lua.LTable
	render  *lua.LFunction
	init    *lua.LFunction
	destroy *lua.LFunction
	last    atomic.Value // string: the previous frame, reused on error or while the plugin is busy
	pending atomic.Int32 // init and destroy calls that wait in the queue of the plugin
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
// render/init/destroy function references from the plugin objects. It reads
// each object under the plugin lock, because a timer that the top-level chunk
// started can write the object at the same time.
func (m *Manager) finalizeVisualizers() {
	for _, vis := range m.visPlugs {
		vis.plugin.mu.Lock()
		if fn, ok := vis.obj.RawGetString("render").(*lua.LFunction); ok {
			vis.render = fn
		}
		if fn, ok := vis.obj.RawGetString("init").(*lua.LFunction); ok {
			vis.init = fn
		}
		if fn, ok := vis.obj.RawGetString("destroy").(*lua.LFunction); ok {
			vis.destroy = fn
		}
		vis.plugin.mu.Unlock()
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

// InitVis queues a call of a Lua visualizer's init(rows, cols) if it exists.
// It returns at once, so it is safe to call from the UI goroutine.
func (m *Manager) InitVis(name string, rows, cols int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if vis, ok := m.visMap[name]; ok && vis.init != nil {
		m.queueVis(vis, "init", vis.init, lua.LNumber(rows), lua.LNumber(cols))
	}
}

// DestroyVis queues a call of a Lua visualizer's destroy() if it exists. It
// returns at once, so it is safe to call from the UI goroutine.
func (m *Manager) DestroyVis(name string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if vis, ok := m.visMap[name]; ok && vis.destroy != nil {
		m.queueVis(vis, "destroy", vis.destroy)
	}
}

// queueVis queues a call of fn, the init or destroy of vis, in order with the
// events of the plugin. It never waits for the plugin lock, because a hook of
// the plugin can hold that lock while it waits for the UI goroutine. RenderVis
// returns the last frame while the call waits, so render never runs before
// init. The caller holds m.mu for reading.
func (m *Manager) queueVis(vis *luaVis, label string, fn *lua.LFunction, args ...lua.LValue) {
	if m.closing {
		return
	}
	args = append([]lua.LValue{vis.obj}, args...)
	vis.pending.Add(1)
	queued := m.enqueue(vis.plugin, label, func() {
		defer vis.pending.Add(-1)
		m.call(vis.plugin, label, hookTimeout, 0, fixedArgs(fn, args...))
	})
	if !queued {
		vis.pending.Add(-1)
	}
}

// RenderVis calls a Lua visualizer's render(bands, frame, rows, cols) and
// returns the terminal text. It runs on the UI goroutine, so it never waits:
// it returns the previous frame when another callback of the plugin holds the
// lock, when an init or destroy call waits in the queue, when render fails,
// or when render runs past renderTimeout.
func (m *Manager) RenderVis(name string, bands [10]float64, rows, cols int, frame uint64) string {
	m.mu.RLock()
	vis, ok := m.visMap[name]
	m.mu.RUnlock()
	if !ok || vis.render == nil {
		return ""
	}

	if vis.pending.Load() == 0 && vis.plugin.mu.TryLock() {
		ret, _ := m.callLocked(m.ctx, vis.plugin, renderLabel, renderTimeout, 1, func(L *lua.LState) (*lua.LFunction, []lua.LValue) {
			return vis.render, []lua.LValue{vis.obj, floatsToTable(L, bands[:]), lua.LNumber(frame), lua.LNumber(rows), lua.LNumber(cols)}
		})
		vis.plugin.mu.Unlock()
		if str, ok := ret.(lua.LString); ok {
			vis.last.Store(string(str))
		}
	}
	last, _ := vis.last.Load().(string)
	return last
}
