package luaplugin

import (
	"errors"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// errActionExists is returned when a plugin registers the same action id
// twice.
var errActionExists = errors.New("p:action: id already registered")

// Action is one entry a plugin contributes to the "!" action pane. Prompt is
// the label of the text input; when it is empty the action runs without
// asking for text. Run calls the plugin callback with the typed text.
type Action struct {
	ID          string
	Plugin      string
	Label       string
	Description string
	Provider    string
	Key         string
	Prompt      string

	run func(text string)
}

// Run invokes the action callback. It is safe to call from the UI goroutine:
// the manager queues the Lua call.
func (a Action) Run(text string) {
	if a.run != nil {
		a.run(text)
	}
}

// registerActionAPI adds p:action() to the plugin object.
func (m *Manager) registerActionAPI(L *lua.LState, obj *lua.LTable, p *Plugin) {
	L.SetField(obj, "action", L.NewFunction(func(L *lua.LState) int {
		if !p.permitted(PermUI, "p:action") {
			L.Push(lua.LFalse)
			L.Push(lua.LString("ui permission required"))
			return 2
		}
		// p:action{...} passes the plugin object first.
		opts, ok := L.Get(2).(*lua.LTable)
		if !ok {
			L.Push(lua.LFalse)
			L.Push(lua.LString("p:action: options table required"))
			return 2
		}
		id := strings.TrimSpace(rawString(L, opts, "id"))
		label := strings.TrimSpace(rawString(L, opts, "label"))
		fn, ok := opts.RawGetString("on_submit").(*lua.LFunction)
		if !ok {
			L.Push(lua.LFalse)
			L.Push(lua.LString("p:action: on_submit is required"))
			return 2
		}
		if id == "" {
			id = label
		}
		if id == "" {
			L.Push(lua.LFalse)
			L.Push(lua.LString("p:action: id or label is required"))
			return 2
		}
		if label == "" {
			label = id
		}
		key := strings.TrimSpace(rawString(L, opts, "key"))
		if key == "" {
			key = "!"
		}
		action := Action{
			ID:          id,
			Plugin:      p.installName,
			Label:       label,
			Description: strings.TrimSpace(rawString(L, opts, "description")),
			Provider:    strings.TrimSpace(rawString(L, opts, "provider")),
			Key:         key,
			Prompt:      strings.TrimSpace(rawString(L, opts, "prompt")),
			run: func(text string) {
				label := "action " + id
				m.enqueue(p, label, func() {
					m.call(p, label, hookTimeout, 0, fixedArgs(fn, lua.LString(text)))
				})
			},
		}
		if err := m.addAction(action); err != nil {
			L.Push(lua.LFalse)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LTrue)
		return 1
	}))
}

// addAction stores an action. The plugin install name namespaces the ID so two
// plugins can use the same local id.
func (m *Manager) addAction(action Action) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := action.Plugin + "\x00" + action.ID
	if m.actions == nil {
		m.actions = make(map[string]Action)
	}
	if _, exists := m.actions[key]; exists {
		return errActionExists
	}
	m.actions[key] = action
	return nil
}

// Actions returns the actions registered for key. The slice is a copy and the
// order is unspecified; the caller sorts or filters it.
func (m *Manager) Actions(key string) []Action {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Action, 0, len(m.actions))
	for _, action := range m.actions {
		if key == "" || action.Key == key {
			out = append(out, action)
		}
	}
	return out
}

// removeActions drops every action of a plugin. It runs when a plugin is
// closed, so the pane never shows a callback into a dead VM.
func (m *Manager) removeActions(installName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, action := range m.actions {
		if action.Plugin == installName {
			delete(m.actions, key)
		}
	}
}
