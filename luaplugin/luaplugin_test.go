package luaplugin

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// newTestManager returns a Manager ready for testing (no disk I/O).
func newTestManager() *Manager {
	return &Manager{
		hooks:  make(map[string][]*luaHook),
		visMap: make(map[string]*luaVis),
		timers: newTimerManager(),
	}
}

// loadTestPlugin writes a Lua script to a temp file, loads it into the manager,
// and appends it to m.plugins if registration succeeded.
func loadTestPlugin(t *testing.T, m *Manager, name, code string) *Plugin {
	return loadTestPluginWithConfig(t, m, name, code, nil)
}

func loadTestPluginWithConfig(t *testing.T, m *Manager, name, code string, cfg map[string]string) *Plugin {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name+".lua")
	if err := os.WriteFile(path, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := m.loadPlugin(path, name, cfg)
	if err != nil {
		t.Fatalf("loadPlugin(%s): %v", name, err)
	}
	if p != nil {
		m.plugins = append(m.plugins, p)
	}
	return p
}

func TestLoadPluginRegistersHookPlugin(t *testing.T) {
	m := newTestManager()
	p := loadTestPlugin(t, m, "test-hook", `
		local p = plugin.register({
			name = "test-hook",
			type = "hook",
			version = "1.0",
			description = "a test plugin",
		})
		p:on("track.change", function(data) end)
	`)

	if p == nil {
		t.Fatal("plugin is nil")
	}
	if p.Name != "test-hook" {
		t.Fatalf("Name = %q, want %q", p.Name, "test-hook")
	}
	if p.Type != "hook" {
		t.Fatalf("Type = %q, want %q", p.Type, "hook")
	}
	if p.Version != "1.0" {
		t.Fatalf("Version = %q, want %q", p.Version, "1.0")
	}
	if len(m.hooks["track.change"]) != 1 {
		t.Fatalf("hooks[track.change] = %d, want 1", len(m.hooks["track.change"]))
	}
}

func TestLoadPluginWithoutRegisterReturnsNil(t *testing.T) {
	m := newTestManager()
	p := loadTestPlugin(t, m, "no-register", `-- does nothing`)

	if p != nil {
		t.Fatalf("expected nil plugin for script without register, got %+v", p)
	}
}

func TestLoadPluginSyntaxError(t *testing.T) {
	m := newTestManager()
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.lua")
	os.WriteFile(path, []byte(`this is not valid lua!!!`), 0o644)

	_, err := m.loadPlugin(path, "bad", nil)
	if err == nil {
		t.Fatal("expected error for invalid Lua syntax")
	}
}

func TestPluginConfig(t *testing.T) {
	m := newTestManager()
	p := loadTestPluginWithConfig(t, m, "cfg", `
		local p = plugin.register({name = "cfg", type = "hook"})
		_G.got_url = p:config("url")
		_G.got_missing = p:config("missing")
	`, map[string]string{"url": "https://example.com"})

	gotURL := p.L.GetGlobal("got_url")
	if gotURL.String() != "https://example.com" {
		t.Fatalf("config('url') = %q, want %q", gotURL.String(), "https://example.com")
	}
	gotMissing := p.L.GetGlobal("got_missing")
	if gotMissing != lua.LNil {
		t.Fatalf("config('missing') = %v, want nil", gotMissing)
	}
}

func TestPluginPermissions(t *testing.T) {
	m := newTestManager()
	p := loadTestPlugin(t, m, "perm", `
		plugin.register({
			name = "perm",
			type = "hook",
			permissions = {"control"},
		})
	`)

	if !p.perms["control"] {
		t.Fatal("perms[control] = false, want true")
	}
}

func TestEmitSync(t *testing.T) {
	m := newTestManager()
	loadTestPlugin(t, m, "sync-test", `
		local p = plugin.register({name = "sync-test", type = "hook"})
		_G.events = {}
		p:on("test.event", function(data)
			table.insert(_G.events, data.msg)
		end)
	`)
	m.EmitSync("test.event", map[string]any{"msg": "hello"})

	p := m.plugins[0]
	events := p.L.GetGlobal("events").(*lua.LTable)
	if events.Len() != 1 {
		t.Fatalf("events length = %d, want 1", events.Len())
	}
	if events.RawGetInt(1).String() != "hello" {
		t.Fatalf("events[1] = %q, want %q", events.RawGetInt(1).String(), "hello")
	}
}

func TestEmitAsync(t *testing.T) {
	m := newTestManager()
	loadTestPlugin(t, m, "async-test", `
		local p = plugin.register({name = "async-test", type = "hook"})
		_G.called = false
		p:on("test.event", function(data)
			_G.called = true
		end)
	`)
	m.Emit("test.event", nil)

	// Wait for async handler to complete.
	time.Sleep(100 * time.Millisecond)

	p := m.plugins[0]
	p.mu.Lock()
	called := p.L.GetGlobal("called")
	p.mu.Unlock()

	if called != lua.LTrue {
		t.Fatal("async event handler was not called")
	}
}

func TestEmitMultipleHooks(t *testing.T) {
	m := newTestManager()
	loadTestPlugin(t, m, "multi", `
		local p = plugin.register({name = "multi", type = "hook"})
		_G.count = 0
		p:on("test.event", function() _G.count = _G.count + 1 end)
		p:on("test.event", function() _G.count = _G.count + 10 end)
	`)
	m.EmitSync("test.event", nil)

	p := m.plugins[0]
	count := p.L.GetGlobal("count")
	if count.(lua.LNumber) != 11 {
		t.Fatalf("count = %v, want 11", count)
	}
}

func TestPluginCountAndHasHooks(t *testing.T) {
	m := newTestManager()

	if m.PluginCount() != 0 {
		t.Fatalf("PluginCount() = %d, want 0", m.PluginCount())
	}
	if m.HasHooks() {
		t.Fatal("HasHooks() = true, want false")
	}

	loadTestPlugin(t, m, "counter", `
		local p = plugin.register({name = "counter", type = "hook"})
		p:on("app.start", function() end)
	`)

	if m.PluginCount() != 1 {
		t.Fatalf("PluginCount() = %d, want 1", m.PluginCount())
	}
	if !m.HasHooks() {
		t.Fatal("HasHooks() = false, want true")
	}
}

func TestClose(t *testing.T) {
	m := newTestManager()
	loadTestPlugin(t, m, "close-test", `
		local p = plugin.register({name = "close-test", type = "hook"})
		_G.quit = false
		p:on("app.quit", function() _G.quit = true end)
	`)

	m.Close()

	// After Close, the LState is shut down. We can't safely query it,
	// but we verified it doesn't panic.
}

func TestManagerWithStateProvider(t *testing.T) {
	m := newTestManager()
	m.SetStateProvider(StateProvider{
		PlayerState: func() string { return "playing" },
		Volume:      func() float64 { return -3.5 },
		TrackTitle:  func() string { return "Angel" },
		TrackArtist: func() string { return "Massive Attack" },
	})

	p := loadTestPlugin(t, m, "state-test", `
		local p = plugin.register({name = "state-test", type = "hook"})
		_G.state = cliamp.player.state()
		_G.vol = cliamp.player.volume()
		_G.title = cliamp.track.title()
		_G.artist = cliamp.track.artist()
	`)

	if p.L.GetGlobal("state").String() != "playing" {
		t.Fatalf("state = %q, want %q", p.L.GetGlobal("state").String(), "playing")
	}
	if float64(p.L.GetGlobal("vol").(lua.LNumber)) != -3.5 {
		t.Fatalf("vol = %v, want -3.5", p.L.GetGlobal("vol"))
	}
	if p.L.GetGlobal("title").String() != "Angel" {
		t.Fatalf("title = %q", p.L.GetGlobal("title").String())
	}
	if p.L.GetGlobal("artist").String() != "Massive Attack" {
		t.Fatalf("artist = %q", p.L.GetGlobal("artist").String())
	}
}

func TestManagerWithControlProvider(t *testing.T) {
	m := newTestManager()
	var gotVol float64
	m.SetControlProvider(ControlProvider{
		SetVolume: func(db float64) { gotVol = db },
	})

	loadTestPlugin(t, m, "ctrl-test", `
		plugin.register({
			name = "ctrl-test",
			type = "hook",
			permissions = {"control"},
		})
		cliamp.player.set_volume(-10)
	`)

	if gotVol != -10 {
		t.Fatalf("SetVolume called with %v, want -10", gotVol)
	}
}

func TestControlClampsBounds(t *testing.T) {
	m := newTestManager()
	var gotVol float64
	var gotSpeed float64
	m.SetControlProvider(ControlProvider{
		SetVolume: func(db float64) { gotVol = db },
		SetSpeed:  func(r float64) { gotSpeed = r },
	})

	loadTestPlugin(t, m, "clamp-test", `
		plugin.register({
			name = "clamp-test",
			type = "hook",
			permissions = {"control"},
		})
		cliamp.player.set_volume(100)
		cliamp.player.set_speed(10)
	`)

	if gotVol != 6 {
		t.Fatalf("set_volume(100) clamped to %v, want 6", gotVol)
	}
	if gotSpeed != 2.0 {
		t.Fatalf("set_speed(10) clamped to %v, want 2.0", gotSpeed)
	}
}

func TestControlWithoutPermissionIsNoop(t *testing.T) {
	m := newTestManager()
	m.logger = newPluginLogger(filepath.Join(t.TempDir(), "test.log"))
	called := false
	m.SetControlProvider(ControlProvider{
		SetVolume: func(db float64) { called = true },
	})

	loadTestPlugin(t, m, "no-perm", `
		plugin.register({name = "no-perm", type = "hook"})
		cliamp.player.set_volume(-10)
	`)

	if called {
		t.Fatal("SetVolume was called without control permission")
	}
}

func TestStateProviderDefaultsWhenNil(t *testing.T) {
	m := newTestManager()
	// No state provider set — all callbacks are nil.

	p := loadTestPlugin(t, m, "defaults", `
		local p = plugin.register({name = "defaults", type = "hook"})
		_G.state = cliamp.player.state()
		_G.vol = cliamp.player.volume()
		_G.speed = cliamp.player.speed()
		_G.pos = cliamp.player.position()
		_G.title = cliamp.track.title()
	`)

	if p.L.GetGlobal("state").String() != "stopped" {
		t.Fatalf("default state = %q, want 'stopped'", p.L.GetGlobal("state").String())
	}
	if float64(p.L.GetGlobal("vol").(lua.LNumber)) != 0 {
		t.Fatalf("default vol = %v, want 0", p.L.GetGlobal("vol"))
	}
	if float64(p.L.GetGlobal("speed").(lua.LNumber)) != 1 {
		t.Fatalf("default speed = %v, want 1", p.L.GetGlobal("speed"))
	}
	if p.L.GetGlobal("title").String() != "" {
		t.Fatalf("default title = %q, want empty", p.L.GetGlobal("title").String())
	}
}

func TestTimerAfter(t *testing.T) {
	m := newTestManager()
	p := loadTestPlugin(t, m, "timer-test", `
		local p = plugin.register({name = "timer-test", type = "hook"})
		_G.fired = false
		cliamp.timer.after(0.05, function()
			_G.fired = true
		end)
	`)

	time.Sleep(150 * time.Millisecond)

	p.mu.Lock()
	fired := p.L.GetGlobal("fired")
	p.mu.Unlock()

	if fired != lua.LTrue {
		t.Fatal("timer.after callback was not fired")
	}
}

func TestTimerCancel(t *testing.T) {
	m := newTestManager()
	p := loadTestPlugin(t, m, "cancel-test", `
		local p = plugin.register({name = "cancel-test", type = "hook"})
		_G.fired = false
		local id = cliamp.timer.after(0.2, function()
			_G.fired = true
		end)
		cliamp.timer.cancel(id)
	`)

	time.Sleep(300 * time.Millisecond)

	p.mu.Lock()
	fired := p.L.GetGlobal("fired")
	p.mu.Unlock()

	if fired == lua.LTrue {
		t.Fatal("timer.after callback fired after cancel")
	}
}

func TestVisualizerPlugin(t *testing.T) {
	m := newTestManager()
	loadTestPlugin(t, m, "test-vis", `
		local v = plugin.register({name = "test-vis", type = "visualizer"})
		_G.init_called = false
		_G.destroy_called = false
		v.init = function(self, rows, cols)
			_G.init_called = true
			_G.init_rows = rows
		end
		v.render = function(self, bands, frame, rows, cols)
			return "frame-" .. tostring(frame)
		end
		v.destroy = function(self)
			_G.destroy_called = true
		end
	`)
	m.finalizeVisualizers()

	names := m.Visualizers()
	if len(names) != 1 || names[0] != "test-vis" {
		t.Fatalf("Visualizers() = %v, want [test-vis]", names)
	}

	m.InitVis("test-vis", 8, 40)

	vis := m.visMap["test-vis"]
	vis.plugin.mu.Lock()
	initCalled := vis.plugin.L.GetGlobal("init_called")
	vis.plugin.mu.Unlock()

	if initCalled != lua.LTrue {
		t.Fatal("init callback was not called")
	}

	got := m.RenderVis("test-vis", [10]float64{}, 8, 40, 42)
	if got != "frame-42" {
		t.Fatalf("RenderVis() = %q, want %q", got, "frame-42")
	}

	m.DestroyVis("test-vis")

	vis.plugin.mu.Lock()
	destroyCalled := vis.plugin.L.GetGlobal("destroy_called")
	vis.plugin.mu.Unlock()

	if destroyCalled != lua.LTrue {
		t.Fatal("destroy callback was not called")
	}
}

func TestRenderVisReusesLastOnError(t *testing.T) {
	m := newTestManager()
	loadTestPlugin(t, m, "err-vis", `
		local v = plugin.register({name = "err-vis", type = "visualizer"})
		local calls = 0
		v.render = function(self, bands, frame, rows, cols)
			calls = calls + 1
			if calls == 2 then
				error("boom")
			end
			return "ok-" .. tostring(calls)
		end
	`)
	m.finalizeVisualizers()

	got1 := m.RenderVis("err-vis", [10]float64{}, 8, 40, 1)
	if got1 != "ok-1" {
		t.Fatalf("frame 1 = %q, want %q", got1, "ok-1")
	}

	// Frame 2 errors — should reuse last frame.
	got2 := m.RenderVis("err-vis", [10]float64{}, 8, 40, 2)
	if got2 != "ok-1" {
		t.Fatalf("frame 2 (error) = %q, want %q (reused)", got2, "ok-1")
	}
}

func TestDataToTableConversion(t *testing.T) {
	L := lua.NewState()
	defer L.Close()

	tbl := dataToTable(L, map[string]any{
		"str":   "hello",
		"num":   42,
		"float": 3.14,
		"bool":  true,
		"nil":   nil,
	})

	if tbl.RawGetString("str").String() != "hello" {
		t.Fatalf("str = %v", tbl.RawGetString("str"))
	}
	if float64(tbl.RawGetString("num").(lua.LNumber)) != 42 {
		t.Fatalf("num = %v", tbl.RawGetString("num"))
	}
	if float64(tbl.RawGetString("float").(lua.LNumber)) != 3.14 {
		t.Fatalf("float = %v", tbl.RawGetString("float"))
	}
	if tbl.RawGetString("bool") != lua.LTrue {
		t.Fatalf("bool = %v", tbl.RawGetString("bool"))
	}
	if tbl.RawGetString("nil") != lua.LNil {
		t.Fatalf("nil = %v", tbl.RawGetString("nil"))
	}
}

func TestDataToTableNested(t *testing.T) {
	L := lua.NewState()
	defer L.Close()

	tbl := dataToTable(L, map[string]any{
		"nested": map[string]any{"key": "value"},
		"floats": []float64{1.0, 2.0, 3.0},
	})

	nested := tbl.RawGetString("nested").(*lua.LTable)
	if nested.RawGetString("key").String() != "value" {
		t.Fatalf("nested.key = %v", nested.RawGetString("key"))
	}

	floats := tbl.RawGetString("floats").(*lua.LTable)
	if float64(floats.RawGetInt(1).(lua.LNumber)) != 1.0 {
		t.Fatalf("floats[1] = %v", floats.RawGetInt(1))
	}
	if floats.Len() != 3 {
		t.Fatalf("floats length = %d, want 3", floats.Len())
	}
}

func TestConcurrentEmitSafety(t *testing.T) {
	m := newTestManager()
	p := loadTestPlugin(t, m, "concurrent", `
		local p = plugin.register({name = "concurrent", type = "hook"})
		_G.count = 0
		p:on("inc", function() _G.count = _G.count + 1 end)
	`)

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			m.Emit("inc", nil)
		})
	}
	wg.Wait()
	time.Sleep(200 * time.Millisecond)

	p.mu.Lock()
	count := float64(p.L.GetGlobal("count").(lua.LNumber))
	p.mu.Unlock()

	if count != 20 {
		t.Fatalf("count after 20 concurrent emits = %v, want 20", count)
	}
}
