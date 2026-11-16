package luaplugin

import (
	"context"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// ProviderInfo is the provider and playlist context a plugin sees. The
// playlist is the one open in the provider pane; selection is the row under
// the cursor in the provider list. Either id may be empty.
type ProviderInfo struct {
	Name          string
	PlaylistID    string
	PlaylistName  string
	SelectionID   string
	SelectionName string
}

// ProviderProvider exposes the active provider context and the chat actions a
// provider supports. Call returns a JSON-compatible map.
type ProviderProvider struct {
	Current func() ProviderInfo
	Call    func(ctx context.Context, action string, params map[string]any) (map[string]any, error)
}

// providerWrites reports whether an action changes the remote account. Write
// actions need permissions = {"provider-send"}; reads need "provider".
func providerWrites(action string) bool {
	switch action {
	case "telegram.send", "telegram.ask", "telegram.reply", "telegram.callback":
		return true
	}
	return false
}

// registerProviderAPI adds cliamp.provider.* to the cliamp table.
//
// cliamp.provider.current() -> {name, playlist_id, playlist_name,
// selection_id, selection_name}
// cliamp.provider.call(action, params[, {on_reply = fn, timeout = secs}])
func (m *Manager) registerProviderAPI(L *lua.LState, cliamp *lua.LTable, p *Plugin) {
	tbl := L.NewTable()

	L.SetField(tbl, "current", L.NewFunction(func(L *lua.LState) int {
		if !p.permitted(PermProvider, "cliamp.provider.current") {
			return pushErr(L, "provider permission required")
		}
		pp := m.provider.Load()
		if pp == nil || pp.Current == nil {
			return pushErr(L, "provider: not available")
		}
		info := pp.Current()
		L.Push(dataToTable(L, map[string]any{
			"name":           info.Name,
			"playlist_id":    info.PlaylistID,
			"playlist_name":  info.PlaylistName,
			"selection_id":   info.SelectionID,
			"selection_name": info.SelectionName,
		}))
		return 1
	}))

	L.SetField(tbl, "call", L.NewFunction(func(L *lua.LState) int {
		action := strings.TrimSpace(L.CheckString(1))
		if action == "" {
			return pushErr(L, "cliamp.provider.call: action is required")
		}
		if providerWrites(action) {
			if !p.permitted(PermProviderSend, "cliamp.provider.call") {
				return pushErr(L, "provider-send permission required")
			}
		} else if !p.permitted(PermProvider, "cliamp.provider.call") {
			return pushErr(L, "provider permission required")
		}
		params := map[string]any{}
		if value := L.Get(2); value != lua.LNil {
			table, ok := value.(*lua.LTable)
			if !ok {
				return pushErr(L, "cliamp.provider.call: params must be a table")
			}
			if converted, ok := luaToGo(table).(map[string]any); ok {
				params = converted
			}
		}
		pp := m.provider.Load()
		if pp == nil || pp.Call == nil {
			return pushErr(L, "provider: not available")
		}

		var opts *lua.LTable
		if value := L.Get(3); value != lua.LNil {
			opts, _ = value.(*lua.LTable)
		}
		var onReply *lua.LFunction
		timeout := 60 * time.Second
		if opts != nil {
			onReply, _ = opts.RawGetString("on_reply").(*lua.LFunction)
			if seconds := optFloatField(L, opts, "timeout"); seconds > 0 {
				timeout = time.Duration(seconds * float64(time.Second))
			}
		}

		if onReply != nil {
			go m.runProviderCall(p, pp.Call, action, params, timeout, onReply)
			L.Push(lua.LTrue)
			return 1
		}

		ctx := callContext(L)
		result, err := pp.Call(ctx, action, params)
		if err != nil {
			return pushErr(L, err.Error())
		}
		L.Push(dataToTable(L, result))
		return 1
	}))

	L.SetField(cliamp, "provider", tbl)
}

// runProviderCall runs a provider action off the Lua VM and delivers the
// result to the plugin callback. A key bind has a five-second hook budget, so
// a plugin that waits for a bot must use on_reply instead of blocking.
func (m *Manager) runProviderCall(p *Plugin, call func(context.Context, string, map[string]any) (map[string]any, error), action string, params map[string]any, timeout time.Duration, fn *lua.LFunction) {
	ctx, cancel := context.WithTimeout(m.ctx, timeout)
	defer cancel()
	result, err := call(ctx, action, params)
	label := "provider.call " + action
	m.enqueue(p, label, func() {
		m.call(p, label, hookTimeout, 0, func(L *lua.LState) (*lua.LFunction, []lua.LValue) {
			if err != nil {
				return fn, []lua.LValue{lua.LNil, lua.LString(err.Error())}
			}
			return fn, []lua.LValue{dataToTable(L, result)}
		})
	})
}

// optFloatField reads a number field from a Lua options table.
func optFloatField(L *lua.LState, table *lua.LTable, key string) float64 {
	if value, ok := table.RawGetString(key).(lua.LNumber); ok {
		return float64(value)
	}
	return 0
}
