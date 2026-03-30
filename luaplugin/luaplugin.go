// Package luaplugin provides a Lua 5.1 scripting engine for cliamp plugins.
// Each plugin runs in an isolated GopherLua VM. Plugins are loaded from
// ~/.config/cliamp/plugins/*.lua at startup.
package luaplugin

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	lua "github.com/yuin/gopher-lua"

	"cliamp/internal/appdir"
)

// Plugin represents a single loaded Lua plugin.
type Plugin struct {
	Name        string
	Version     string
	Description string
	Type        string // "hook" or "visualizer"
	L           *lua.LState
	mu          sync.Mutex        // serializes all LState access (LState is not thread-safe)
	config      map[string]string // per-plugin config from config.toml
	perms       map[string]bool   // declared permissions (e.g. "control")
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
	EQBands     func() [10]float64
	TrackTitle  func() string
	TrackArtist   func() string
	TrackAlbum    func() string
	TrackGenre    func() string
	TrackYear     func() int
	TrackNumber   func() int
	TrackPath     func() string
	TrackIsStream func() bool
	TrackDuration func() int // seconds
	PlaylistCount func() int
	CurrentIndex  func() int // 0-based
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
	Seek         func(secs float64)
	SetEQPreset  func(name string) // injected via prog.Send
	Next         func()            // injected via prog.Send
	Prev         func()            // injected via prog.Send
}

// Manager owns all loaded plugins and dispatches events to them.
type Manager struct {
	plugins  []*Plugin
	hooks    map[string][]*luaHook // event name -> handlers
	visPlugs []*luaVis            // Lua visualizers in registration order
	visMap   map[string]*luaVis   // name -> Lua visualizer
	state    StateProvider
	control  ControlProvider
	timers   *timerManager
	logger   *pluginLogger
	mu       sync.RWMutex
}

// New scans the plugin directory and loads all .lua files.
// pluginCfg maps plugin names to their [plugins.<name>] config keys.
// Returns a Manager (possibly with 0 plugins) and any non-fatal load error.
func New(pluginCfg map[string]map[string]string) (*Manager, error) {
	m := &Manager{
		hooks:  make(map[string][]*luaHook),
		visMap: make(map[string]*luaVis),
		timers: newTimerManager(),
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
		} else if strings.HasSuffix(e.Name(), ".lua") {
			files = append(files, pluginFile{
				name: strings.TrimSuffix(e.Name(), ".lua"),
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
				for _, name := range strings.Split(list, ",") {
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
		Name:   name,
		L:      L,
		config: cfg,
	}

	// Register the plugin.register() global.
	m.registerPluginAPI(L, p)

	// Register all cliamp.* API tables.
	m.registerCliampAPI(L, p)

	if err := L.DoFile(path); err != nil {
		L.Close()
		return nil, err
	}

	// If plugin.register() was never called, skip this file.
	// Remove any hooks that may have been registered during DoFile
	// before closing the LState to prevent dangling references.
	if p.Type == "" {
		m.mu.Lock()
		for event, hooks := range m.hooks {
			filtered := hooks[:0]
			for _, h := range hooks {
				if h.plugin != p {
					filtered = append(filtered, h)
				}
			}
			m.hooks[event] = filtered
		}
		m.mu.Unlock()
		L.Close()
		return nil, nil
	}

	return p, nil
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
					p.perms[v.String()] = true
				})
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
	registerCryptoAPI(L, cliamp)
	registerFSAPI(L, cliamp)
	registerHTTPAPI(L, cliamp)
	registerPlayerAPI(L, cliamp, &m.state)
	registerTrackAPI(L, cliamp, &m.state)
	registerTimerAPI(L, cliamp, m.timers, p)
	registerNotifyAPI(L, cliamp, m.logger, p.Name)
	registerControlAPI(L, cliamp, &m.control, p, m.logger)
	registerSleepAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)
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

// Close fires the "app.quit" event synchronously and shuts down all Lua VMs.
func (m *Manager) Close() {
	m.EmitSync(EventAppQuit, nil)
	m.timers.stopAll()
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
