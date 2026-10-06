package luaplugin

import (
	"context"
	"errors"
	"fmt"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// hookTimeout bounds each event hook, key bind, timer and exec callback, and
// each visualizer init or destroy call. It is a var so tests can shorten it.
var hookTimeout = 5 * time.Second

// loadTimeout bounds the top-level chunk of a plugin file at load. It is a
// var so tests can shorten it.
var loadTimeout = 5 * time.Second

// errClosed is the error for a call into a plugin whose VM is closed.
var errClosed = errors.New("plugin is closed")

// eventQueueSize bounds the events and key presses that wait for one plugin.
// A plugin that falls this far behind drops new ones until it catches up.
const eventQueueSize = 256

// Event name constants.
const (
	EventAppStart      = "app.start"
	EventAppQuit       = "app.quit"
	EventPlaybackState = "playback.state"
	EventTrackChange   = "track.change"
	EventTrackScrobble = "track.scrobble"
	EventPlayerSeek    = "player.seek"   // data: position, duration (seconds)
	EventPlayerVolume  = "player.volume" // data: db
	EventPlayerEQ      = "player.eq"     // data: bands (10-array), preset
	EventPlayerMode    = "player.mode"   // data: shuffle (bool), repeat ("Off"/"All"/"One")
	EventQueueChange   = "queue.change"  // data: count, index, queued
	EventQueueEnd      = "queue.end"     // data: the finished track (same shape as track.change)
	EventPlaybackStop  = "playback.stop" // data: none; an explicit stop by the user, never a queue running out
)

// Permission strings declared via plugin.register({ permissions = {...} }).
// Kept as named constants so the guard call sites and docs don't drift.
const (
	PermControl = "control"
	PermExec    = "exec"
	PermKeymap  = "keymap"
)

// luaHook is a single event callback registered by a plugin.
type luaHook struct {
	plugin *Plugin
	fn     *lua.LFunction
}

// callBuilder returns the Lua function to call and its arguments. call runs
// it under the plugin lock, so it can make tables on L. A nil function skips
// the call.
type callBuilder func(L *lua.LState) (*lua.LFunction, []lua.LValue)

// fixedArgs returns a callBuilder for fn with arguments that need no LState.
func fixedArgs(fn *lua.LFunction, args ...lua.LValue) callBuilder {
	return func(*lua.LState) (*lua.LFunction, []lua.LValue) { return fn, args }
}

// call is the one way Go calls into a plugin's Lua VM. It holds p.mu for the
// whole call because an LState is not safe for concurrent use. It returns
// errClosed after the VM is closed. The call runs under m.ctx. See callLocked
// for the rest.
func (m *Manager) call(p *Plugin, label string, timeout time.Duration, nret int, build callBuilder) (lua.LValue, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return m.callLocked(m.ctx, p, label, timeout, nret, build)
}

// renderLabel is the call label of a visualizer render. See callLocked.
const renderLabel = "render"

// callLocked is call for a caller that already holds p.mu. The call stops
// after timeout, or when parent ends. parent is m.ctx, or for a command a
// child of m.cmdCtx. Close cancels both with the cause errClosed. When nret >
// 0, it returns the first result. After parent ends, it returns the cause of
// parent and logs nothing, because the stop is not an error of the plugin. It
// logs a Lua error under label, but only when the error differs from the last
// one logged for label. Thus a timer that fails each time logs once. A render runs on
// each frame, so it logs only its first error and its first timeout while the
// plugin is loaded. Otherwise a render that fails on some frames fills
// plugins.log. A timeout error names the limit.
func (m *Manager) callLocked(parent context.Context, p *Plugin, label string, timeout time.Duration, nret int, build callBuilder) (lua.LValue, error) {
	if p.closed {
		return lua.LNil, errClosed
	}
	if parent.Err() != nil {
		return lua.LNil, context.Cause(parent)
	}
	fn, args := build(p.L)
	if fn == nil {
		return lua.LNil, nil
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	p.L.SetContext(ctx)
	defer p.L.RemoveContext()

	if err := p.L.CallByParam(lua.P{Fn: fn, NRet: nret, Protect: true}, args...); err != nil {
		if parent.Err() != nil {
			return lua.LNil, context.Cause(parent)
		}
		// A timeout stops the VM at a different line each time, so key it by
		// the context error and not by the Lua message. A render timeout has
		// its own slot, so one slow frame does not hide a later Lua error.
		slot, key := label, err.Error()
		if ctx.Err() != nil {
			key = ctx.Err().Error()
			err = fmt.Errorf("did not finish in %v: %w", timeout, err)
			if label == renderLabel {
				slot = renderLabel + " timeout"
			}
		}
		prev, logged := p.lastErr[slot]
		if !logged || (prev != key && label != renderLabel) {
			if p.lastErr == nil {
				p.lastErr = make(map[string]string)
			}
			p.lastErr[slot] = key
			m.logHookErr(p.installName, label, err)
		}
		return lua.LNil, err
	}
	if label != renderLabel {
		delete(p.lastErr, label)
	}
	if nret == 0 {
		return lua.LNil, nil
	}
	ret := p.L.Get(-nret)
	p.L.Pop(nret)
	return ret, nil
}

// logHookErr records a callback error in plugins.log. It never writes to
// stderr, because stderr output corrupts the TUI.
func (m *Manager) logHookErr(name, label string, err error) {
	m.logger.log(name, "error", "%s error: %v", label, err)
}

// filterOutPlugin returns hooks with all entries owned by p removed. Reuses
// the existing backing slice and zeroes the tail so dropped LFunction pointers
// become garbage-collectible.
func filterOutPlugin(hooks []*luaHook, p *Plugin) []*luaHook {
	filtered := hooks[:0]
	for _, h := range hooks {
		if h.plugin != p {
			filtered = append(filtered, h)
		}
	}
	for i := len(filtered); i < len(hooks); i++ {
		hooks[i] = nil
	}
	return filtered
}

// Emit queues an event for every plugin that registered for it and returns
// without waiting. Each plugin runs its events one at a time, in the order
// they were emitted, and each callback times out after hookTimeout.
// Different plugins run in parallel.
func (m *Manager) Emit(event string, data map[string]any) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closing {
		return
	}
	label := event + " handler"
	for _, h := range m.hooks[event] {
		m.enqueue(h.plugin, label, func() { m.fire(h, label, data) })
	}
}

// enqueue adds fn to the queue of p without blocking and reports whether it
// did. When the queue is full, it drops fn and logs once until the queue
// accepts a call again. The caller holds m.mu for reading and saw closing
// false, so the queue is open.
func (m *Manager) enqueue(p *Plugin, label string, fn func()) bool {
	select {
	case p.queue <- fn:
		p.dropping.Store(false)
		return true
	default:
		if !p.dropping.Swap(true) {
			m.logger.log(p.installName, "warn", "%s dropped. %d events and key presses are waiting. cliamp drops new ones until the plugin catches up.", label, eventQueueSize)
		}
		return false
	}
}

// runQueue runs the queued calls of p one at a time until Close closes the
// queue. Thus the events and key presses of one plugin keep their order. When
// Close sets dropQueued, it drops the calls that still wait.
func (m *Manager) runQueue(p *Plugin) {
	defer m.queues.Done()
	for fn := range p.queue {
		if !m.dropQueued.Load() {
			fn()
		}
	}
}

// EmitSync dispatches an event synchronously, blocking until all callbacks
// finish or time out. Close uses it for app.quit before it closes the VMs.
func (m *Manager) EmitSync(event string, data map[string]any) {
	m.mu.RLock()
	hooks := m.hooks[event]
	m.mu.RUnlock()

	label := event + " handler"
	for _, h := range hooks {
		m.fire(h, label, data)
	}
}

// fire calls an event hook with data as its table argument.
func (m *Manager) fire(h *luaHook, label string, data map[string]any) {
	m.call(h.plugin, label, hookTimeout, 0, func(L *lua.LState) (*lua.LFunction, []lua.LValue) {
		return h.fn, []lua.LValue{dataToTable(L, data)}
	})
}

// dataToTable converts a Go map to a Lua table.
func dataToTable(L *lua.LState, data map[string]any) *lua.LTable {
	tbl := L.NewTable()
	if data == nil {
		return tbl
	}
	for k, v := range data {
		tbl.RawSetString(k, toLua(L, v))
	}
	return tbl
}

// toLua converts a Go value to a Lua value. It covers event payloads and the
// values that cliamp.json and cliamp.store decode. A map becomes a table with
// string keys, and a slice becomes an array. Any other type becomes its fmt
// string.
func toLua(L *lua.LState, v any) lua.LValue {
	switch val := v.(type) {
	case nil:
		return lua.LNil
	case string:
		return lua.LString(val)
	case int:
		return lua.LNumber(val)
	case int64:
		return lua.LNumber(val)
	case float64:
		return lua.LNumber(val)
	case bool:
		return lua.LBool(val)
	case map[string]any:
		return dataToTable(L, val)
	case []float64:
		return floatsToTable(L, val)
	case []string:
		tbl := L.NewTable()
		for i, s := range val {
			tbl.RawSetInt(i+1, lua.LString(s))
		}
		return tbl
	case []any:
		tbl := L.NewTable()
		for i, item := range val {
			tbl.RawSetInt(i+1, toLua(L, item))
		}
		return tbl
	default:
		return lua.LString(fmt.Sprintf("%v", val))
	}
}

// floatsToTable converts numbers such as the 10 EQ or spectrum bands to a Lua
// array.
func floatsToTable(L *lua.LState, vals []float64) *lua.LTable {
	tbl := L.CreateTable(len(vals), 0)
	for i, f := range vals {
		tbl.RawSetInt(i+1, lua.LNumber(f))
	}
	return tbl
}
