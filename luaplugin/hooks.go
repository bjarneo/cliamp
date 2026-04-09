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
)

// luaHook is a single event callback registered by a plugin.
type luaHook struct {
	plugin *Plugin
	fn     *lua.LFunction
}

// Emit dispatches an event to all plugins that registered for it.
// Each callback runs in its own goroutine with a timeout. The plugin's
// mutex serializes all LState access so concurrent events are safe.
func (m *Manager) Emit(event string, data map[string]any) {
	m.mu.RLock()
	hooks := m.hooks[event]
	m.mu.RUnlock()

	for _, h := range hooks {
		go func() {
			h.plugin.mu.Lock()
			defer h.plugin.mu.Unlock()

			ctx, cancel := context.WithTimeout(context.Background(), hookTimeout)
			defer cancel()
			h.plugin.L.SetContext(ctx)
			defer h.plugin.L.RemoveContext()

			arg := dataToTable(h.plugin.L, data)
			if err := h.plugin.L.CallByParam(lua.P{
				Fn:      h.fn,
				NRet:    0,
				Protect: true,
			}, arg); err != nil {
				log.Printf("[lua:%s] %s handler error: %v", h.plugin.Name, event, err)
				if m.logger != nil {
					m.logger.log(h.plugin.Name, "error", "%s handler error: %v", event, err)
				}
			}
		}()
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
