package luaplugin

import (
	"context"
	"fmt"
	"log"
	"time"

	lua "github.com/yuin/gopher-lua"
)

const hookTimeout = 5 * time.Second

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

// callBounded runs fn on the plugin's LState under hookTimeout so a runaway
// callback cannot hold the plugin mutex forever. The caller must already hold
// p.mu. Results (if nret > 0) are left on the stack for the caller to read.
func (p *Plugin) callBounded(nret int, fn *lua.LFunction, args ...lua.LValue) error {
	ctx, cancel := context.WithTimeout(context.Background(), hookTimeout)
	defer cancel()
	p.L.SetContext(ctx)
	defer p.L.RemoveContext()
	return p.L.CallByParam(lua.P{Fn: fn, NRet: nret, Protect: true}, args...)
}

// logHookErr records a callback error to stderr and the plugin log.
func (m *Manager) logHookErr(name, label string, err error) {
	log.Printf("[lua:%s] %s error: %v", name, label, err)
	if m.logger != nil {
		m.logger.log(name, "error", "%s error: %v", label, err)
	}
}

// invokeHook calls a plugin's Lua callback under the plugin's mutex with a
// bounded context. Logs any error to the plugin log. Used by every dispatch
// site that fires Lua from Go (events, key binds, command handlers).
func (m *Manager) invokeHook(h *luaHook, label string, args ...lua.LValue) {
	h.plugin.mu.Lock()
	defer h.plugin.mu.Unlock()

	if err := h.plugin.callBounded(0, h.fn, args...); err != nil {
		m.logHookErr(h.plugin.Name, label, err)
	}
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

// Emit dispatches an event to all plugins that registered for it.
// Each callback runs in its own goroutine with a timeout. The plugin's
// mutex serializes all LState access so concurrent events are safe.
func (m *Manager) Emit(event string, data map[string]any) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closing {
		return
	}
	hooks := m.hooks[event]
	label := event + " handler"
	for _, h := range hooks {
		// Add under RLock so Close (which sets closing under Lock, then Wait)
		// can never miss an in-flight goroutine: either we register it before
		// closing is set, or we observe closing and skip.
		m.wg.Add(1)
		go func(h *luaHook) {
			defer m.wg.Done()
			m.invokeHookWithData(h, label, data)
		}(h)
	}
}

// invokeHookWithData is Emit's per-hook goroutine: builds the arg table on the
// plugin's LState (which requires holding plugin.mu) and then fires the
// callback under the same lock via invokeHook's contract.
func (m *Manager) invokeHookWithData(h *luaHook, label string, data map[string]any) {
	h.plugin.mu.Lock()
	defer h.plugin.mu.Unlock()

	arg := dataToTable(h.plugin.L, data)
	if err := h.plugin.callBounded(0, h.fn, arg); err != nil {
		m.logHookErr(h.plugin.Name, label, err)
	}
}

// EmitSync dispatches an event synchronously, blocking until all callbacks finish.
// Used during shutdown to ensure all handlers complete before LStates are closed.
func (m *Manager) EmitSync(event string, data map[string]any) {
	m.mu.RLock()
	hooks := m.hooks[event]
	m.mu.RUnlock()

	for _, h := range hooks {
		h.plugin.mu.Lock()
		arg := dataToTable(h.plugin.L, data)
		_ = h.plugin.L.CallByParam(lua.P{
			Fn:      h.fn,
			NRet:    0,
			Protect: true,
		}, arg)
		h.plugin.mu.Unlock()
	}
}

// dataToTable converts a Go map to a Lua table.
func dataToTable(L *lua.LState, data map[string]any) *lua.LTable {
	tbl := L.NewTable()
	if data == nil {
		return tbl
	}
	for k, v := range data {
		tbl.RawSetString(k, goToLua(L, v))
	}
	return tbl
}

// goToLua converts a Go value to a Lua value.
func goToLua(L *lua.LState, v any) lua.LValue {
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
		tbl := L.NewTable()
		for i, f := range val {
			tbl.RawSetInt(i+1, lua.LNumber(f))
		}
		return tbl
	default:
		return lua.LString(fmt.Sprintf("%v", val))
	}
}
