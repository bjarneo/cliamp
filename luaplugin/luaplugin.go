// Package luaplugin provides a Lua 5.1 scripting engine for cliamp plugins.
// Each plugin runs in an isolated GopherLua VM. Plugins are loaded from
// ~/.config/cliamp/plugins/*.lua at startup.
package luaplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	lua "github.com/yuin/gopher-lua"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/plugintrust"
)

// Plugin represents a single loaded Lua plugin.
//
// A plugin has two names. installName is the file name without .lua, or the
// directory name. Config, trust, the store, the event namespace and
// plugins.log use it, and plugin.register() cannot change it. Name is the
// display name from plugin.register(). Commands, key binding descriptions and
// visualizers use it, and Manager.claimName keeps it unique.
type Plugin struct {
	Name         string
	Version      string
	Description  string
	Type         string // "hook" or "visualizer"
	L            *lua.LState
	mu           sync.Mutex        // serializes all LState access (LState is not thread-safe)
	closed       bool              // guarded by mu; set when L is closed, so no callback runs after it
	lastErr      map[string]string // guarded by mu; call label -> last logged error
	warned       map[string]bool   // guarded by mu; permissions whose denial was logged
	logger       *pluginLogger     // the Manager's logger; nil in tests
	config       map[string]string // per-plugin config from config.toml
	perms        map[string]bool   // declared permissions (e.g. "control")
	approved     map[string]bool   // permissions that the user approved; see loadPlugin
	installName  string            // installed name; see the type comment
	namespace    string            // installName reduced to one event topic segment
	namespaceErr error             // set when another plugin claimed that namespace first
	queue        chan func()       // events and key presses in arrival order; see runQueue
	dropping     atomic.Bool       // set while the queue is full, so one burst of drops logs once
}

// StateProvider supplies read-only access to player/playlist state.
// Functions are set by the caller after model construction so the Lua API
// can query state without importing the ui package. luaStateProvider in
// lua_wiring.go reads the state that the Model publishes before each event
// and after each Update. Plugin goroutines call the functions, so each one
// must be safe for concurrent use.
type StateProvider struct {
	PlayerState   func() string  // "playing", "paused", "stopped"
	Position      func() float64 // seconds
	Duration      func() float64 // seconds
	Volume        func() float64 // dB
	Speed         func() float64 // ratio (1.0 = normal)
	Mono          func() bool
	RepeatMode    func() string // "Off", "All", "One"
	Shuffle       func() bool
	EQBands       func() [10]float64
	CurrentTrack  func() Track // the track of cliamp.track.*; the zero Track when there is none
	PlaylistCount func() int
	CurrentIndex  func() int          // 0-based
	HasNext       func() bool         // a track follows in play order (queue, repeat, shuffle)
	QueueList     func() []QueueEntry // full playlist in track order; Index is the track index
}

// Track is a track as plugins see it. cliamp.track.* reads every field.
// Track tables, which events, cliamp.queue.list rows and cliamp.queue.add
// share, carry the fields that trackFields names.
type Track struct {
	Title    string
	Artist   string
	Album    string
	Genre    string
	Path     string
	Year     int
	Number   int // track number
	Duration int // seconds
	Stream   bool
	Live     bool // live stream with no track boundary
}

// QueueEntry is one track in the playlist as exposed to plugins via
// cliamp.queue.list(). Index is 0-based and matches CurrentIndex; Queued is
// true when the track sits in the explicit play-next queue. The track fields
// match event track tables, so a row can be passed back to cliamp.queue.add.
type QueueEntry struct {
	Track
	Index  int
	Queued bool
}

// ControlProvider supplies write access to player controls.
// Only available to plugins that declare permissions = {"control"}.
// A plugin calls each func while it holds its lock, so a func must return
// at once. luaControlProvider in lua_wiring.go queues a message for the
// Update loop in each one, so the Model applies every change and keeps
// derived state consistent.
type ControlProvider struct {
	SetVolume     func(db float64)
	SetSpeed      func(ratio float64)
	SetEQBand     func(band int, db float64)
	ToggleMono    func()
	TogglePause   func()
	Stop          func()
	Seek          func(secs float64)
	SetEQPreset   func(name string, bands *[10]float64)
	Next          func()
	Prev          func()
	QueueAdd      func(path string)  // resolve path/URL and append
	QueueAddTrack func(track Track)  // append a track table as given, without resolving its path
	QueueJump     func(index int)    // make index current and play it
	QueueRemove   func(index int)    // remove track at index
	QueueMove     func(from, to int) // reorder
}

// UIProvider supplies callbacks that surface plugin output in the TUI.
// Not permission-gated — these are low-risk, output-only operations. Like
// the ControlProvider funcs, ShowMessage must return at once.
type UIProvider struct {
	ShowMessage func(text string, duration time.Duration)
}

// EventPublisher accepts namespaced JSON events emitted by Lua plugins.
type EventPublisher interface {
	Publish(topic string, data json.RawMessage, retain bool) error
	ClearPrefix(prefix string)
}

// Manager owns all loaded plugins and dispatches events to them.
type Manager struct {
	plugins      []*Plugin
	hooks        map[string][]*luaHook           // event name -> handlers
	keyBinds     map[string][]*luaHook           // key string -> handlers (global, non-overlay)
	keyBindDescs map[string]KeyBinding           // key string -> UI overlay entry (only for binds that supplied a description)
	reservedKeys map[string]bool                 // core-reserved keys; plugins may not bind these
	commands     map[string]map[string]*luaHook  // plugin name -> command name -> handler
	visPlugs     []*luaVis                       // Lua visualizers in registration order
	visMap       map[string]*luaVis              // name -> Lua visualizer
	namespaces   map[string]string               // event namespace -> owning plugin name
	names        map[string]*Plugin              // display name -> plugin that registered it
	state        atomic.Pointer[StateProvider]   // see SetStateProvider
	control      atomic.Pointer[ControlProvider] // see SetControlProvider
	ui           atomic.Pointer[UIProvider]      // see SetUIProvider
	publisher    EventPublisher
	timers       *timerManager
	execs        *execManager
	logger       *pluginLogger
	mu           sync.RWMutex
	closing      bool                    // set under mu.Lock during Close; blocks new async dispatch
	queues       sync.WaitGroup          // tracks the queue worker of each loaded plugin
	dropQueued   atomic.Bool             // set when closeDrainBudget ends; see runQueue
	wg           sync.WaitGroup          // tracks in-flight EmitCommand goroutines
	ctx          context.Context         // parent of each call context; see Close
	cancel       context.CancelCauseFunc // cancels ctx with errClosed
	cmdCtx       context.Context         // parent of each command call; a child of ctx
	cmdCancel    context.CancelCauseFunc // cancels cmdCtx with errClosed
}

// New scans the plugin directory and loads all .lua files.
// pluginCfg maps plugin names to their [plugins.<name>] config keys.
// publisher backs p:publish() and may be nil; it is installed before any plugin
// runs so a plugin can publish from its top-level chunk.
// reservedKeys holds the keys that cliamp's core UI owns. A p:bind of one of
// them logs a warning and returns false. It is also installed before any
// plugin runs, so a bind in the top-level chunk is refused too.
// Returns a Manager (possibly with 0 plugins) and any non-fatal load error.
func New(pluginCfg map[string]map[string]string, publisher EventPublisher, reservedKeys map[string]bool) (*Manager, error) {
	m := newManager(resolveAllowedBinaries(pluginCfg), publisher)
	m.reservedKeys = reservedKeys

	dir, err := appdir.PluginDir()
	if err != nil {
		return m, nil // no config dir — fine, just no plugins
	}

	// Initialize plugin logger.
	logDir, _ := appdir.Dir()
	m.logger = newPluginLogger(filepath.Join(logDir, pluginLogName))

	files, err := Discover(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return m, fmt.Errorf("read plugin dir: %w", err)
	}
	var loadErrs []string
	trustManifest, trustErr := plugintrust.Load(dir)
	if trustErr != nil {
		// Fail safe: a manifest that does not load approves no plugin.
		// Continue, so each plugin reports that it needs approval.
		// `cliamp plugins trust` also fails until the file is gone, so name
		// the file to delete.
		msg := fmt.Sprintf("%v; all plugins are untrusted; delete %s and approve each plugin again",
			trustErr, plugintrust.ManifestPath(dir))
		m.logger.log("cliamp", "error", "%s", msg)
		loadErrs = append(loadErrs, msg)
		trustManifest = plugintrust.Manifest{}
	}

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

	for _, f := range files {
		if disabled[f.Name] {
			continue
		}
		cfg := pluginCfg[f.Name]
		// Check per-plugin enabled flag.
		if cfg != nil {
			if v, ok := cfg["enabled"]; ok && v == "false" {
				continue
			}
		}
		if err := plugintrust.Verify(trustManifest, f.Name, f.Path); err != nil {
			if trustErr != nil {
				loadErrs = append(loadErrs, fmt.Sprintf("%s: %v", f.Name, err))
			} else {
				loadErrs = append(loadErrs, fmt.Sprintf("%s: %v; run `cliamp plugins trust %s`", f.Name, err, f.Name))
			}
			continue
		}

		if _, err := m.loadPlugin(f.Path, f.Name, cfg, approvedPermissions(trustManifest, f)); err != nil {
			loadErrs = append(loadErrs, fmt.Sprintf("%s: %v", f.Name, err))
		}
	}

	m.finalizeVisualizers()

	if len(loadErrs) > 0 {
		return m, fmt.Errorf("plugin load errors: %s", strings.Join(loadErrs, "; "))
	}
	return m, nil
}

// approvedPermissions returns the permissions that the user approved for f.
// install and trust record the permissions that their prompt showed. An
// approval without that record, such as one from an older cliamp, approves
// what ReadMetadata finds in the file. The hash matches the approval, so that
// is what the prompt showed for this content.
func approvedPermissions(manifest plugintrust.Manifest, f PluginFile) map[string]bool {
	perms, ok := manifest.Permissions[f.Name]
	if !ok {
		if data, err := os.ReadFile(f.Path); err == nil {
			if md, err := ReadMetadata(string(data)); err == nil {
				perms = md.Permissions
			}
		}
	}
	approved := make(map[string]bool, len(perms))
	for _, permission := range perms {
		approved[permission] = true
	}
	return approved
}

// newManager returns a Manager with no plugins and no logger. allowed is the
// binary allowlist for cliamp.exec.run.
func newManager(allowed []string, publisher EventPublisher) *Manager {
	ctx, cancel := context.WithCancelCause(context.Background())
	cmdCtx, cmdCancel := context.WithCancelCause(ctx)
	return &Manager{
		hooks:        make(map[string][]*luaHook),
		keyBinds:     make(map[string][]*luaHook),
		keyBindDescs: make(map[string]KeyBinding),
		commands:     make(map[string]map[string]*luaHook),
		visMap:       make(map[string]*luaVis),
		namespaces:   make(map[string]string),
		names:        make(map[string]*Plugin),
		timers:       newTimerManager(),
		execs:        newExecManager(allowed),
		publisher:    publisher,
		ctx:          ctx,
		cancel:       cancel,
		cmdCtx:       cmdCtx,
		cmdCancel:    cmdCancel,
	}
}

// loadPlugin creates an isolated Lua VM, registers the cliamp API,
// and executes the plugin file. Returns nil (no error) if the file
// doesn't call plugin.register(). On success it adds the plugin to m.plugins
// and starts its queue worker, so Close always stops the worker. approved
// holds the permissions that the user approved. A plugin.register() call
// with another permission fails.
func (m *Manager) loadPlugin(path, name string, cfg map[string]string, approved map[string]bool) (*Plugin, error) {
	L := lua.NewState(lua.Options{
		SkipOpenLibs: false,
	})
	sandbox(L)

	p := &Plugin{
		Name:        name,
		installName: name,
		namespace:   eventNamespace(name),
		L:           L,
		config:      cfg,
		approved:    approved,
		queue:       make(chan func(), eventQueueSize),
		logger:      m.logger,
	}
	m.claimNamespace(p)

	// Register the plugin.register() global.
	m.registerPluginAPI(L, p)

	// Register all cliamp.* API tables.
	m.registerCliampAPI(L, p)

	// The top-level chunk has a time limit, so a plugin that loops or
	// sleeps at load cannot hang startup.
	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	p.mu.Lock()
	L.SetContext(ctx)
	err := L.DoFile(path)
	if err != nil && ctx.Err() != nil {
		err = fmt.Errorf("load did not finish in %v: %w", loadTimeout, err)
	}
	L.RemoveContext()
	cancel()
	// If plugin.register() was never called, skip this file.
	failed := err != nil || p.Type == ""
	if failed {
		// Set closed before the lock is released, so a timer or exec
		// callback that waits for the lock returns without running.
		p.closed = true
	}
	p.mu.Unlock()
	if !failed {
		m.plugins = append(m.plugins, p)
		m.queues.Add(1)
		go m.runQueue(p)
		return p, nil
	}

	// cleanupPlugin waits for the plugin's processes, and their callbacks
	// take p.mu, so it must run without the lock.
	m.cleanupPlugin(p)
	p.mu.Lock()
	L.Close()
	p.mu.Unlock()
	return nil, err
}

func (m *Manager) cleanupPlugin(p *Plugin) {
	m.mu.Lock()
	// Release the event namespace only if this plugin owns it. Compare against
	// installName, not Name: plugin.register() can rename Name after the
	// claim, and a renamed plugin that then fails to load must not keep the
	// namespace locked away from a later colliding plugin.
	if owner, ok := m.namespaces[p.namespace]; ok && owner == p.installName {
		delete(m.namespaces, p.namespace)
	}
	// Everything below is matched by owner, never by name. A plugin that
	// failed to claim a name must not remove what the owner of that name
	// registered.
	for name, owner := range m.names {
		if owner == p {
			delete(m.names, name)
		}
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
		if desc.owner == p {
			delete(m.keyBindDescs, key)
		}
	}
	for name, cmds := range m.commands {
		for cmd, h := range cmds {
			if h.plugin == p {
				delete(cmds, cmd)
			}
		}
		if len(cmds) == 0 {
			delete(m.commands, name)
		}
	}

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
		md, err := parseRegisterOpts(L.CheckTable(1))
		if err != nil {
			L.RaiseError("%v", err)
		}
		// The global stays callable after load. A second call, such as one
		// from a hook, must not grant other permissions.
		if p.Type != "" {
			L.RaiseError("%v", errRegisteredTwice)
		}
		for _, permission := range md.Permissions {
			if !p.approved[permission] {
				L.RaiseError("permission %q is not approved; run `cliamp plugins trust %s`", permission, p.installName)
			}
		}
		name := md.Name
		if name == "" {
			name = p.Name
		}
		if err := m.claimName(p, name); err != nil {
			L.RaiseError("%v", err)
		}
		p.Name = name
		p.Version = md.Version
		p.Description = md.Description
		p.Type = md.Type
		p.perms = make(map[string]bool, len(md.Permissions))
		for _, permission := range md.Permissions {
			p.perms[permission] = true
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

		m.registerPublishAPI(L, obj, p)
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
	registerLogAPI(L, cliamp, p)
	registerJSONAPI(L, cliamp)
	registerStoreAPI(L, cliamp, p.installName)
	registerCryptoAPI(L, cliamp)
	registerFSAPI(L, cliamp)
	registerHTTPAPI(L, cliamp)
	loadState, loadCtrl := loadProvider(&m.state), loadProvider(&m.control)
	registerPlayerAPI(L, cliamp, loadState)
	registerTrackAPI(L, cliamp, loadState)
	m.registerTimerAPI(L, cliamp, p)
	registerQueueAPI(L, cliamp, loadState, loadCtrl, p)
	registerNotifyAPI(L, cliamp, p)
	registerControlAPI(L, cliamp, loadCtrl, p)
	registerMessageAPI(L, cliamp, loadProvider(&m.ui))
	registerSleepAPI(L, cliamp)
	m.registerExecAPI(L, cliamp, p)
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
// tracked by installed name because plugin.register() can rename p.Name.
func (m *Manager) claimNamespace(p *Plugin) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if owner, taken := m.namespaces[p.namespace]; taken && owner != p.installName {
		p.namespaceErr = fmt.Errorf("event namespace %q is already used by plugin %q; rename this plugin to publish events", p.namespace, owner)
		if m.logger != nil {
			m.logger.log(p.installName, "warn", "%v", p.namespaceErr)
		}
		return
	}
	m.namespaces[p.namespace] = p.installName
}

// claimName records p as the owner of the display name. It fails when another
// plugin registered that name first, so a command, key binding description or
// visualizer name always belongs to one plugin. Load order is sorted by
// installed name, so the winner is deterministic. A plugin keeps every name
// it registered until cleanupPlugin releases them.
func (m *Manager) claimName(p *Plugin, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if owner, taken := m.names[name]; taken && owner != p {
		return fmt.Errorf("plugin name %q is already used by plugin %q; set another name in plugin.register()", name, owner.installName)
	}
	m.names[name] = p
	return nil
}

// SetEventPublisher replaces the publisher backing p:publish(). New installs
// the publisher before plugins run; this is for callers that wire it later.
func (m *Manager) SetEventPublisher(publisher EventPublisher) {
	m.mu.Lock()
	m.publisher = publisher
	m.mu.Unlock()
}

// SetStateProvider sets the function pointers used by the Lua API to
// query live player/playlist state. It is safe to call while plugins run,
// such as a timer that the top-level chunk of a plugin started.
func (m *Manager) SetStateProvider(sp StateProvider) {
	m.state.Store(&sp)
}

// SetControlProvider sets the function pointers for player control.
// Only plugins with permissions = {"control"} can use these. It is safe to
// call while plugins run.
func (m *Manager) SetControlProvider(cp ControlProvider) {
	m.control.Store(&cp)
}

// SetUIProvider sets the function pointers for UI output (status messages).
// It is safe to call while plugins run.
func (m *Manager) SetUIProvider(up UIProvider) {
	m.ui.Store(&up)
}

// loadProvider returns a func that loads the provider that p holds. Before
// the first set, the func returns an empty provider. Each Lua API call loads
// the provider once, so it sees one provider for the whole call.
func loadProvider[T any](p *atomic.Pointer[T]) func() *T {
	return func() *T {
		if v := p.Load(); v != nil {
			return v
		}
		return new(T)
	}
}

// closeDrainBudget bounds the run of queued events in Close. After it ends,
// the queue workers drop the calls that still wait. It is a var so tests can
// shorten it.
var closeDrainBudget = 2 * time.Second

// Close fires the "app.quit" event synchronously and shuts down all Lua VMs.
func (m *Manager) Close() {
	// Block new async dispatch before tearing anything down.
	m.mu.Lock()
	m.closing = true
	m.mu.Unlock()
	// Stop the commands that run. A command can hold the plugin lock for up
	// to commandTimeout, and the queued events and app.quit need that lock.
	m.cmdCancel(errClosed)

	// Run the events that are already queued, so app.quit is the last event
	// each plugin sees. Emit, EmitKey and queueVis send only under m.mu while
	// closing is false, so no send can reach a closed queue. After
	// closeDrainBudget, drop the calls that still wait. The call that runs
	// then stops within hookTimeout.
	for _, p := range m.plugins {
		close(p.queue)
	}
	drained := make(chan struct{})
	go func() {
		m.queues.Wait()
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(closeDrainBudget):
		m.dropQueued.Store(true)
		<-drained
	}

	m.EmitSync(EventAppQuit, nil)
	m.timers.stopAll()
	m.execs.stopAll()
	// Stop the Lua that still runs, such as a timer callback. It stops at
	// its next instruction, and each later call returns errClosed. Thus the
	// waits below do not take up to hookTimeout.
	m.cancel(errClosed)
	// Wait for any in-flight command goroutines to finish before closing
	// the LStates they call into.
	m.wg.Wait()
	// Close each VM under its lock. A timer or exec callback that runs now
	// finishes first, and each later call sees closed and returns errClosed.
	for _, p := range m.plugins {
		p.mu.Lock()
		p.closed = true
		p.L.Close()
		p.mu.Unlock()
	}
	// No Lua runs from here on. Stop the timers and processes that a
	// callback started after the first stop.
	m.timers.stopAll()
	m.execs.stopAll()
	// Drop retained events only once every publisher has stopped, so a late
	// callback cannot leave a retained value behind.
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
}

// PluginCount returns the number of loaded plugins.
func (m *Manager) PluginCount() int {
	return len(m.plugins)
}

// HasHook reports whether any plugin registered for a specific event. Callers
// use this to skip building event payloads (and any locks they require) when no
// plugin is listening for that particular event.
func (m *Manager) HasHook(event string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.hooks[event]) > 0
}
