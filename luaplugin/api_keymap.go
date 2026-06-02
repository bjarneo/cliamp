package luaplugin

import (
	"context"
	"sort"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// SetReservedKeys records the set of keys owned by cliamp's core UI. Plugins
// attempting to bind one of these keys get a logged warning and their bind
// call returns false. Called once during startup from main.go.
func (m *Manager) SetReservedKeys(keys map[string]bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reservedKeys = keys
}

// KeyBinding describes a plugin-registered keybinding for the Ctrl+K overlay.
type KeyBinding struct {
	Key         string
	Plugin      string
	Description string
}

// KeyBindings returns a snapshot of every plugin-registered keybinding that
// has a description, sorted by key for stable overlay ordering. Bindings
// registered without a description are omitted — plugins can opt out of
// surfacing a key simply by skipping the description argument.
//
// Called once per Ctrl+K overlay open, so the sort cost is noise.
func (m *Manager) KeyBindings() []KeyBinding {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]KeyBinding, 0, len(m.keyBindDescs))
	for _, b := range m.keyBindDescs {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// EmitKey invokes every plugin callback registered for the given key string.
// Returns true if at least one callback fired. Called by the UI's main key
// dispatcher for keys the core doesn't handle.
func (m *Manager) EmitKey(key string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closing {
		return false
	}
	hooks := m.keyBinds[key]
	if len(hooks) == 0 {
		return false
	}

	label := "keybind " + key
	for _, h := range hooks {
		// Tracked in wg and gated on closing (same as Emit) so a keypress
		// during shutdown can't call into a closed LState.
		m.wg.Add(1)
		go func(h *luaHook) {
			defer m.wg.Done()
			m.invokeHook(h, label, lua.LString(key))
		}(h)
	}
	return true
}

// normalizeKey lowercases and strips whitespace so "Ctrl+X" and "ctrl+x"
// collide at registration and dispatch.
func normalizeKey(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}

// registerKeymapAPI attaches :bind() / :unbind() to the plugin object returned
// by plugin.register(). Gated on permissions = {"keymap"}.
func (m *Manager) registerKeymapAPI(L *lua.LState, obj *lua.LTable, p *Plugin) {
	warned := false
	guard := func() bool {
		if p.perms[PermKeymap] {
			return true
		}
		if !warned && m.logger != nil {
			m.logger.log(p.Name, "warn", "plugin:bind requires permissions = {\"keymap\"} — further warnings suppressed")
			warned = true
		}
		return false
	}

	// p:bind(key, fn)                     → no entry in Ctrl+K overlay
	// p:bind(key, description, fn)        → with description (shown in overlay)
	// Returns true on success; false, reason on failure.
	L.SetField(obj, "bind", L.NewFunction(func(L *lua.LState) int {
		key := normalizeKey(L.CheckString(2))

		// Lua has no function overloading, so disambiguate by inspecting arg 3:
		// function → old 2-arg form; anything else → (key, description, fn).
		var description string
		var fn *lua.LFunction
		if L.Get(3).Type() == lua.LTFunction {
			fn = L.CheckFunction(3)
		} else {
			description = strings.TrimSpace(L.CheckString(3))
			fn = L.CheckFunction(4)
		}

		if !guard() {
			L.Push(lua.LFalse)
			L.Push(lua.LString("keymap permission required"))
			return 2
		}
		if key == "" {
			L.Push(lua.LFalse)
			L.Push(lua.LString("empty key"))
			return 2
		}

		m.mu.Lock()
		if m.reservedKeys[key] {
			m.mu.Unlock()
			if m.logger != nil {
				m.logger.log(p.Name, "warn", "refusing to bind %q: reserved by cliamp core", key)
			}
			L.Push(lua.LFalse)
			L.Push(lua.LString("key reserved by cliamp: " + key))
			return 2
		}
		m.keyBinds[key] = append(m.keyBinds[key], &luaHook{plugin: p, fn: fn})
		if description != "" {
			m.keyBindDescs[key] = KeyBinding{Key: key, Plugin: p.Name, Description: description}
		}
		m.mu.Unlock()

		L.Push(lua.LTrue)
		return 1
	}))

	// p:unbind(key)
	L.SetField(obj, "unbind", L.NewFunction(func(L *lua.LState) int {
		key := normalizeKey(L.CheckString(2))
		m.mu.Lock()
		m.keyBinds[key] = filterOutPlugin(m.keyBinds[key], p)
		if len(m.keyBinds[key]) == 0 {
			delete(m.keyBinds, key)
		}
		if desc, ok := m.keyBindDescs[key]; ok && desc.Plugin == p.Name {
			delete(m.keyBindDescs, key)
		}
		m.mu.Unlock()
		return 0
	}))
}

// EmitCommand dispatches a plugin command invoked over IPC and blocks up to
// commandTimeout for the handler to return a result. A missing plugin/command
// returns ("", err); a handler error returns ("", err); success returns
// (result, nil). The result is whatever the handler returned as a string
// (nil or false stringifies to "").
func (m *Manager) EmitCommand(pluginName, cmdName string, args []string) (string, error) {
	m.mu.RLock()
	plugCmds, ok := m.commands[pluginName]
	var hook *luaHook
	if ok {
		hook = plugCmds[cmdName]
	}
	m.mu.RUnlock()
	if hook == nil {
		return "", errCommandNotFound(pluginName, cmdName)
	}

	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)

	go func() {
		hook.plugin.mu.Lock()
		defer hook.plugin.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		hook.plugin.L.SetContext(ctx)
		defer hook.plugin.L.RemoveContext()

		argsTbl := hook.plugin.L.NewTable()
		for i, a := range args {
			argsTbl.RawSetInt(i+1, lua.LString(a))
		}

		err := hook.plugin.L.CallByParam(lua.P{
			Fn:      hook.fn,
			NRet:    1,
			Protect: true,
		}, argsTbl)
		if err != nil {
			done <- result{err: err}
			return
		}
		ret := hook.plugin.L.Get(-1)
		hook.plugin.L.Pop(1)
		done <- result{out: luaValueToString(ret)}
	}()

	select {
	case r := <-done:
		return r.out, r.err
	case <-time.After(commandTimeout + time.Second):
		return "", errCommandTimeout(pluginName, cmdName)
	}
}

// CommandList returns a flat list of "<plugin> <command>" strings. Used by
// `cliamp plugin commands`. Order is unspecified.
func (m *Manager) CommandList() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []string
	for plug, cmds := range m.commands {
		for cmd := range cmds {
			out = append(out, plug+" "+cmd)
		}
	}
	return out
}

// commandTimeout caps how long a plugin command handler may run before we
// return an error to the IPC client. Five minutes is generous — enough for
// yt-dlp downloads — while ensuring the socket client doesn't hang forever.
const commandTimeout = 5 * time.Minute

func errCommandNotFound(plug, cmd string) error {
	return &commandError{msg: "no such plugin command: " + plug + " " + cmd}
}
func errCommandTimeout(plug, cmd string) error {
	return &commandError{msg: "plugin command timed out: " + plug + " " + cmd}
}

type commandError struct{ msg string }

func (e *commandError) Error() string { return e.msg }

func luaValueToString(v lua.LValue) string {
	if v == lua.LNil || v == lua.LFalse {
		return ""
	}
	return v.String()
}

// registerCommandAPI attaches :command() to the plugin object. Unlike keymap,
// commands don't need a permission — they're user-initiated from the shell.
func (m *Manager) registerCommandAPI(L *lua.LState, obj *lua.LTable, p *Plugin) {
	// p:command(name, fn) — fn(args) -> optional result string
	L.SetField(obj, "command", L.NewFunction(func(L *lua.LState) int {
		name := strings.TrimSpace(L.CheckString(2))
		fn := L.CheckFunction(3)
		if name == "" {
			L.ArgError(2, "empty command name")
			return 0
		}
		m.mu.Lock()
		if m.commands[p.Name] == nil {
			m.commands[p.Name] = make(map[string]*luaHook)
		}
		m.commands[p.Name][name] = &luaHook{plugin: p, fn: fn}
		m.mu.Unlock()
		return 0
	}))
}
