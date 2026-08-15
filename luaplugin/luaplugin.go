// Package luaplugin provides a Lua 5.1 scripting engine for cliamp plugins.
// Each plugin runs in an isolated GopherLua VM. Plugins are loaded from
// ~/.config/cliamp/plugins/*.lua at startup.
package luaplugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/plugintrust"
)

// Plugin represents a single loaded Lua plugin.
type Plugin struct {
	Name           string
	Version        string
	Description    string
	Type           string // "hook" or "visualizer"
	L              *lua.LState
	mu             sync.Mutex        // serializes all LState access (LState is not thread-safe)
	config         map[string]string // per-plugin config from config.toml
	perms          map[string]bool   // declared permissions (e.g. "control")
	namespaceOwner string            // installed filename; plugin.register() cannot change it
	namespace      string            // namespaceOwner reduced to one event topic segment
	namespaceErr   error             // set when another plugin claimed that namespace first
}

// StateProvider supplies read-only access to player/playlist state.
// Functions are set by the caller after model construction so the Lua API
// can query live state without importing the ui package.
type StateProvider struct {
	PlayerState   func() string  // "playing", "paused", "stopped"
	Position      func() float64 // seconds
	Duration      func() float64 // seconds
	Volume        func() float64 // dB
	Speed         func() float64 // ratio (1.0 = normal)
	Mono          func() bool
	RepeatMode    func() string // "off", "all", "one"
	Shuffle       func() bool
	EQBands       func() [10]float64
	TrackTitle    func() string
	TrackArtist   func() string
	TrackAlbum    func() string
	TrackGenre    func() string
	TrackYear     func() int
	TrackNumber   func() int
	TrackPath     func() string
	TrackIsStream func() bool
	TrackDuration func() int // seconds
	PlaylistCount func() int
	CurrentIndex  func() int          // 0-based
	QueueList     func() []QueueEntry // full playlist in play order
}

// QueueEntry is one track in the playlist as exposed to plugins via
// cliamp.queue.list(). Index is 0-based and matches CurrentIndex; Queued is
// true when the track sits in the explicit play-next queue.
type QueueEntry struct {
	Title  string
	Artist string
	Album  string
	Path   string
	Index  int
	Queued bool
}

// ControlProvider supplies write access to player controls.
// Only available to plugins that declare permissions = {"control"}.
type ControlProvider struct {
	SetVolume   func(db float64)
	SetSpeed    func(ratio float64)
	SetEQBand   func(band int, db float64)
	ToggleMono  func()
	TogglePause func()
	Stop        func()
	Seek        func(secs float64)
	SetEQPreset func(name string, bands *[10]float64) // injected via prog.Send
	Next        func()                                // injected via prog.Send
	Prev        func()                                // injected via prog.Send
	// Queue mutators, all injected via prog.Send so the model's Update loop
	// applies them and keeps derived state (cursor, current index) consistent.
	QueueAdd    func(path string)  // resolve path/URL and append
	QueueJump   func(index int)    // make index current and play it
	QueueRemove func(index int)    // remove track at index
	QueueMove   func(from, to int) // reorder
}

// UIProvider supplies callbacks that surface plugin output in the TUI.
// Not permission-gated — these are low-risk, output-only operations.
type UIProvider struct {
	ShowMessage func(text string, duration time.Duration) // injected via prog.Send
}

// EventPublisher accepts namespaced JSON events emitted by Lua plugins.
type EventPublisher interface {
	Publish(topic string, data json.RawMessage, retain bool) error
	ClearPrefix(prefix string)
}

// Manager owns all loaded plugins and dispatches events to them.
type Manager struct {
	plugins      []*Plugin
	hooks        map[string][]*luaHook          // event name -> handlers
	keyBinds     map[string][]*luaHook          // key string -> handlers (global, non-overlay)
	keyBindDescs map[string]KeyBinding          // key string -> UI overlay entry (only for binds that supplied a description)
	reservedKeys map[string]bool                // core-reserved keys; plugins may not bind these
	commands     map[string]map[string]*luaHook // plugin name -> command name -> handler
	visPlugs     []*luaVis                      // Lua visualizers in registration order
	visMap       map[string]*luaVis             // name -> Lua visualizer
	namespaces   map[string]string              // event namespace -> owning plugin name
	state        StateProvider
	control      ControlProvider
	ui           UIProvider
	publisher    EventPublisher
	timers       *timerManager
	execs        *execManager
	logger       *pluginLogger
	mu           sync.RWMutex
	closing      bool           // set under mu.Lock during Close; blocks new async dispatch
	wg           sync.WaitGroup // tracks in-flight async Emit goroutines
}

// New scans the plugin directory and loads all .lua files.
// pluginCfg maps plugin names to their [plugins.<name>] config keys.
// publisher backs p:publish() and may be nil; it is installed before any plugin
// runs so a plugin can publish from its top-level chunk.
// Returns a Manager (possibly with 0 plugins) and any non-fatal load error.
func New(pluginCfg map[string]map[string]string, publisher EventPublisher) (*Manager, error) {
	m := &Manager{
		hooks:        make(map[string][]*luaHook),
		keyBinds:     make(map[string][]*luaHook),
		keyBindDescs: make(map[string]KeyBinding),
		commands:     make(map[string]map[string]*luaHook),
		visMap:       make(map[string]*luaVis),
		namespaces:   make(map[string]string),
		timers:       newTimerManager(),
		execs:        newExecManager(resolveAllowedBinaries(pluginCfg)),
		publisher:    publisher,
	}

	dir, err := appdir.PluginDir()
	if err != nil {
		return m, nil // no config dir — fine, just no plugins
	}

	// Initialize plugin logger.
	logDir, _ := appdir.Dir()
	m.logger = newPluginLogger(filepath.Join(logDir, "plugins.log"))

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return m, fmt.Errorf("read plugin dir: %w", err)
	}
	trustManifest, err := plugintrust.Load(dir)
	if err != nil {
		return m, err
	}

	// Collect plugin files: *.lua and directories with init.lua.
	type pluginFile struct {
		name string
		path string
	}
	var files []pluginFile
	for _, e := range entries {
		if e.IsDir() {
			init := filepath.Join(dir, e.Name(), "init.lua")
			if _, err := os.Stat(init); err == nil {
				files = append(files, pluginFile{name: e.Name(), path: init})
			}
		} else if before, ok := strings.CutSuffix(e.Name(), ".lua"); ok {
			files = append(files, pluginFile{
				name: before,
				path: filepath.Join(dir, e.Name()),
			})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })

	// Check disabled list.
	disabled := make(map[string]bool)
	if pluginCfg != nil {
		if topLevel, ok := pluginCfg[""]; ok {
			if list, ok := topLevel["disabled"]; ok {
				for name := range strings.SplitSeq(list, ",") {
					disabled[strings.TrimSpace(name)] = true
				}
			}
		}
	}

	var loadErrs []string
	for _, f := range files {
		if disabled[f.name] {
			continue
		}
		cfg := pluginCfg[f.name]
		// Check per-plugin enabled flag.
		if cfg != nil {
			if v, ok := cfg["enabled"]; ok && v == "false" {
				continue
			}
		}
		if err := plugintrust.Verify(trustManifest, f.name, f.path); err != nil {
			loadErrs = append(loadErrs, fmt.Sprintf("%s: %v; run `cliamp plugins trust %s`", f.name, err, f.name))
			continue
		}

		p, err := m.loadPlugin(f.path, f.name, cfg)
		if err != nil {
			loadErrs = append(loadErrs, fmt.Sprintf("%s: %v", f.name, err))
			continue
		}
		if p != nil {
			m.plugins = append(m.plugins, p)
		}
	}

	m.finalizeVisualizers()

	if len(loadErrs) > 0 {
		return m, fmt.Errorf("plugin load errors: %s", strings.Join(loadErrs, "; "))
	}
	return m, nil
}

// loadPlugin creates an isolated Lua VM, registers the cliamp API,
// and executes the plugin file. Returns nil (no error) if the file
// doesn't call plugin.register().
func (m *Manager) loadPlugin(path, name string, cfg map[string]string) (*Plugin, error) {
	L := lua.NewState(lua.Options{
		SkipOpenLibs: false,
	})
	sandbox(L)

	p := &Plugin{
		Name:           name,
		namespaceOwner: name,
		namespace:      eventNamespace(name),
		L:              L,
		config:         cfg,
	}
	m.claimNamespace(p)

	// Register the plugin.register() global.
	m.registerPluginAPI(L, p)

	// Register all cliamp.* API tables.
	m.registerCliampAPI(L, p)

	p.mu.Lock()
	err := L.DoFile(path)
	if err != nil {
		m.cleanupPlugin(p)
		p.mu.Unlock()
		L.Close()
		return nil, err
	}

	// If plugin.register() was never called, skip this file.
	if p.Type == "" {
		m.cleanupPlugin(p)
		p.mu.Unlock()
		L.Close()
		return nil, nil
	}
	p.mu.Unlock()

	return p, nil
}

func (m *Manager) cleanupPlugin(p *Plugin) {
	m.mu.Lock()
	// Release the event namespace only if this plugin owns it. Compare against
	// namespaceOwner, not Name: plugin.register() can rename Name after the
	// claim, and a renamed plugin that then fails to load must not keep the
	// namespace locked away from a later colliding plugin.
	if owner, ok := m.namespaces[p.namespace]; ok && owner == p.namespaceOwner {
		delete(m.namespaces, p.namespace)
	}
	for event, hooks := range m.hooks {
		m.hooks[event] = filterOutPlugin(hooks, p)
	}
	for key, hooks := range m.keyBinds {
		filtered := filterOutPlugin(hooks, p)
		if len(filtered) == 0 {
			delete(m.keyBinds, key)
		} else {
			m.keyBinds[key] = filtered
		}
	}
	for key, desc := range m.keyBindDescs {
		if desc.Plugin == p.Name {
			delete(m.keyBindDescs, key)
		}
	}

	delete(m.commands, p.Name)

	filteredVis := m.visPlugs[:0]
	for _, vis := range m.visPlugs {
		if vis.plugin != p {
			filteredVis = append(filteredVis, vis)
		}
	}
	for i := len(filteredVis); i < len(m.visPlugs); i++ {
		m.visPlugs[i] = nil
	}
	m.visPlugs = filteredVis

	for name, vis := range m.visMap {
		if vis.plugin == p {
			delete(m.visMap, name)
		}
	}
	m.mu.Unlock()

	m.timers.stopPlugin(p)
	m.execs.stopPlugin(p)
}

// registerPluginAPI sets up the global "plugin" table with register() and
// the plugin object's on() and config() methods.
func (m *Manager) registerPluginAPI(L *lua.LState, p *Plugin) {
	pluginTbl := L.NewTable()

	// plugin.register(opts) -> plugin object
	L.SetField(pluginTbl, "register", L.NewFunction(func(L *lua.LState) int {
		opts := L.CheckTable(1)

		if name := opts.RawGetString("name"); name != lua.LNil {
			p.Name = name.String()
		}
		if version := opts.RawGetString("version"); version != lua.LNil {
			p.Version = version.String()
		}
		if desc := opts.RawGetString("description"); desc != lua.LNil {
			p.Description = desc.String()
		}
		if typ := opts.RawGetString("type"); typ != lua.LNil {
			p.Type = typ.String()
		}
		// Parse permissions = {"control", ...}
		if perms := opts.RawGetString("permissions"); perms != lua.LNil {
			if tbl, ok := perms.(*lua.LTable); ok {
				p.perms = make(map[string]bool)
				tbl.ForEach(func(_, v lua.LValue) {
					permission := v.String()
					switch permission {
					case PermControl, PermExec, PermKeymap:
						p.perms[permission] = true
					default:
						L.RaiseError("unknown permission %q", permission)
					}
				})
			} else {
				L.RaiseError("permissions must be an array")
			}
		}

		// Return a plugin object with on() and config() methods.
		obj := L.NewTable()

		// p:on(event, callback) — colon call puts self at arg 1
		L.SetField(obj, "on", L.NewFunction(func(L *lua.LState) int {
			event := L.CheckString(2)
			fn := L.CheckFunction(3)
			m.mu.Lock()
			m.hooks[event] = append(m.hooks[event], &luaHook{
				plugin: p,
				fn:     fn,
			})
			m.mu.Unlock()
			return 0
		}))

		// p:config(key) -> string or nil — colon call puts self at arg 1
		L.SetField(obj, "config", L.NewFunction(func(L *lua.LState) int {
			key := L.CheckString(2)
			if p.config != nil {
				if v, ok := p.config[key]; ok {
					L.Push(lua.LString(v))
					return 1
				}
			}
			L.Push(lua.LNil)
			return 1
		}))

		// p:publish(topic, payload, {retain=true}) publishes only inside the
		// immutable namespace derived from the installed plugin filename.
		L.SetField(obj, "publish", L.NewFunction(func(L *lua.LState) int {
			topic := L.CheckString(2)
			payload := luaToGo(L.Get(3))
			retain := false
			if options, ok := L.Get(4).(*lua.LTable); ok {
				retain = lua.LVAsBool(options.RawGetString("retain"))
			}
			data, err := json.Marshal(payload)
			if err == nil {
				m.mu.RLock()
				publisher := m.publisher
				m.mu.RUnlock()
				if publisher == nil {
					err = fmt.Errorf("plugin event publisher is unavailable")
				} else if p.namespaceErr != nil {
					err = p.namespaceErr
				} else {
					fullTopic := "plugin." + p.namespace + "." + topic
					err = publisher.Publish(fullTopic, data, retain)
				}
			}
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			L.Push(lua.LTrue)
			return 1
		}))

		m.registerKeymapAPI(L, obj, p)
		m.registerCommandAPI(L, obj, p)

		// For visualizer plugins, add init/render registration.
		if p.Type == "visualizer" {
			m.registerVisPlugin(L, obj, p)
		}

		L.Push(obj)
		return 1
	}))

	L.SetGlobal("plugin", pluginTbl)
}

// registerCliampAPI sets up the "cliamp" global table with all sub-modules.
func (m *Manager) registerCliampAPI(L *lua.LState, p *Plugin) {
	cliamp := L.NewTable()
	registerLogAPI(L, cliamp, m.logger, p.Name)
	registerJSONAPI(L, cliamp)
	registerStoreAPI(L, cliamp, p.Name)
	registerCryptoAPI(L, cliamp)
	registerFSAPI(L, cliamp)
	registerHTTPAPI(L, cliamp)
	registerPlayerAPI(L, cliamp, &m.state)
	registerTrackAPI(L, cliamp, &m.state)
	registerTimerAPI(L, cliamp, m.timers, p)
	registerQueueAPI(L, cliamp, &m.state, &m.control, p, m.logger)
	registerNotifyAPI(L, cliamp, m.logger, p.Name)
	registerControlAPI(L, cliamp, &m.control, p, m.logger)
	registerMessageAPI(L, cliamp, &m.ui)
	registerSleepAPI(L, cliamp)
	registerExecAPI(L, cliamp, m.execs, p, m.logger)
	L.SetGlobal("cliamp", cliamp)
}

// resolveAllowedBinaries merges defaultAllowedBinaries with any user-supplied
// entries under [plugins] allowed_binaries = "name1,name2". An empty or
// missing value falls back to the default set.
func resolveAllowedBinaries(pluginCfg map[string]map[string]string) []string {
	if pluginCfg == nil {
		return defaultAllowedBinaries
	}
	topLevel, ok := pluginCfg[""]
	if !ok {
		return defaultAllowedBinaries
	}
	raw, ok := topLevel["allowed_binaries"]
	if !ok || strings.TrimSpace(raw) == "" {
		return defaultAllowedBinaries
	}
	seen := make(map[string]bool)
	var out []string
	for _, b := range defaultAllowedBinaries {
		if !seen[b] {
			seen[b] = true
			out = append(out, b)
		}
	}
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// eventNamespace reduces an installed plugin name to a single event topic
// segment. Without this, a dot in the name makes topics ambiguous: a plugin
// installed as "foo.bar" publishing "playback" and one installed as "foo"
// publishing "bar.playback" would both produce plugin.foo.bar.playback.
// Characters that IPC topics reject are folded the same way so every installed
// plugin can publish, whatever its filename.
//
// Folding is lossy: "foo.bar" and "foo_bar" both yield "foo_bar". loadPlugin
// therefore lets only the first plugin claim a namespace and disables
// publishing for later ones, so two plugins can never share a topic.
func eventNamespace(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// claimNamespace records p as the owner of its event namespace, or marks p as
// unable to publish when another plugin already owns that namespace. Load order
// is sorted by installed name, so the winner is deterministic. Ownership is
// tracked by installed filename because plugin.register() can rename p.Name.
func (m *Manager) claimNamespace(p *Plugin) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.namespaces == nil {
		m.namespaces = make(map[string]string)
	}
	if owner, taken := m.namespaces[p.namespace]; taken && owner != p.namespaceOwner {
		p.namespaceErr = fmt.Errorf("event namespace %q is already used by plugin %q; rename this plugin to publish events", p.namespace, owner)
		if m.logger != nil {
			m.logger.log(p.namespaceOwner, "warn", "%v", p.namespaceErr)
		}
		return
	}
	m.namespaces[p.namespace] = p.namespaceOwner
}

// SetEventPublisher replaces the publisher backing p:publish(). New installs
// the publisher before plugins run; this is for callers that wire it later.
func (m *Manager) SetEventPublisher(publisher EventPublisher) {
	m.mu.Lock()
	m.publisher = publisher
	m.mu.Unlock()
}

// SetStateProvider sets the function pointers used by the Lua API to
// query live player/playlist state.
func (m *Manager) SetStateProvider(sp StateProvider) {
	m.state = sp
}

// SetControlProvider sets the function pointers for player control.
// Only plugins with permissions = {"control"} can use these.
func (m *Manager) SetControlProvider(cp ControlProvider) {
	m.control = cp
}

// SetUIProvider sets the function pointers for UI output (status messages).
func (m *Manager) SetUIProvider(up UIProvider) {
	m.ui = up
}

// Close fires the "app.quit" event synchronously and shuts down all Lua VMs.
func (m *Manager) Close() {
	// Block new async dispatch before tearing anything down.
	m.mu.Lock()
	m.closing = true
	m.mu.Unlock()

	m.EmitSync(EventAppQuit, nil)
	m.timers.stopAll()
	m.execs.stopAll()
	// Wait for any in-flight async hook goroutines to finish before closing
	// the LStates they call into.
	m.wg.Wait()
	// Drop retained events only once every publisher has stopped, so a late
	// async handler cannot leave a retained value behind.
	m.mu.RLock()
	publisher := m.publisher
	m.mu.RUnlock()
	if publisher != nil {
		for _, p := range m.plugins {
			if p.namespaceErr != nil {
				continue // never owned the namespace, so nothing of its own is retained
			}
			publisher.ClearPrefix("plugin." + p.namespace + ".")
		}
	}
	if m.logger != nil {
		m.logger.close()
	}
	for _, p := range m.plugins {
		p.L.Close()
	}
}

// PluginCount returns the number of loaded plugins.
func (m *Manager) PluginCount() int {
	return len(m.plugins)
}

// HasHooks reports whether any plugins have registered hooks.
func (m *Manager) HasHooks() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, hooks := range m.hooks {
		if len(hooks) > 0 {
			return true
		}
	}
	return false
}

// HasHook reports whether any plugin registered for a specific event. Callers
// use this to skip building event payloads (and any locks they require) when no
// plugin is listening for that particular event.
func (m *Manager) HasHook(event string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.hooks[event]) > 0
}
