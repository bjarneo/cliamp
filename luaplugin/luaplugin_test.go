package luaplugin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"

	"github.com/bjarneo/cliamp/internal/plugintrust"
)

// newTestManager returns a Manager ready for testing (no disk I/O).
func newTestManager() *Manager {
	return newManager(defaultAllowedBinaries, nil)
}

// fixed returns a provider loader that always returns v.
func fixed[T any](v *T) func() *T {
	return func() *T { return v }
}

// setRenderTimeout sets renderTimeout until the test ends. A test that needs
// a render to succeed sets a long limit, so a slow runner cannot fail it.
func setRenderTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := renderTimeout
	renderTimeout = d
	t.Cleanup(func() { renderTimeout = old })
}

// loadTestPlugin writes a Lua script to a temp file and loads it into the
// manager. loadPlugin adds the plugin to m.plugins if registration succeeded.
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
	p, err := m.loadPlugin(path, name, cfg, knownPermissions)
	if err != nil {
		t.Fatalf("loadPlugin(%s): %v", name, err)
	}
	return p
}

func loadTestPluginExpectError(t *testing.T, m *Manager, name, code string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name+".lua")
	if err := os.WriteFile(path, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.loadPlugin(path, name, nil, knownPermissions); err == nil {
		t.Fatalf("expected error for %s", name)
	}
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

func TestRegisterWithoutTypeReportsError(t *testing.T) {
	tests := []struct {
		name string
		code string
	}{
		{"no type", `plugin.register({name = "y"})`},
		{"empty type", `plugin.register({name = "y", type = ""})`},
		{"unknown type", `plugin.register({name = "y", type = "visualiser"})`},
		{"number type", `plugin.register({name = "y", type = 1})`},
		{"no type with a hook", `
			local p = plugin.register({name = "y"})
			p:on("track.change", function() end)
		`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestManager()
			path := filepath.Join(t.TempDir(), "y.lua")
			if err := os.WriteFile(path, []byte(tt.code), 0o644); err != nil {
				t.Fatal(err)
			}
			p, err := m.loadPlugin(path, "y", nil, knownPermissions)
			if p != nil {
				t.Errorf("loadPlugin() plugin = %+v, want nil", p)
			}
			if err == nil || !strings.Contains(err.Error(), `needs type = "hook" or "visualizer"`) {
				t.Errorf("loadPlugin() error = %v, want the missing type", err)
			}
			if n := len(m.hooks["track.change"]); n != 0 {
				t.Errorf("hooks[track.change] = %d, want 0", n)
			}
		})
	}
}

// A display name belongs to the first plugin that registers it. A later
// plugin with the same name fails to load. A plugin that registers again under
// a new name keeps the old name too.
func TestDuplicateDisplayNameRejected(t *testing.T) {
	setRenderTimeout(t, time.Second)
	type file struct{ name, code string }
	tests := []struct {
		name     string
		first    file
		second   file
		wantErr  string
		wantVis  []string
		wantName string
	}{
		{
			name:    "same name",
			first:   file{"a", `plugin.register({name = "dup", type = "hook"})`},
			second:  file{"b", `plugin.register({name = "dup", type = "hook"})`},
			wantErr: `plugin name "dup" is already used by plugin "a"`,
		},
		{
			name:    "name matches the default name of another plugin",
			first:   file{"dup", `plugin.register({type = "hook"})`},
			second:  file{"b", `plugin.register({name = "dup", type = "hook"})`},
			wantErr: `plugin name "dup" is already used by plugin "dup"`,
		},
		{
			name:    "default name matches the name of another plugin",
			first:   file{"a", `plugin.register({name = "dup", type = "hook"})`},
			second:  file{"dup", `plugin.register({type = "hook"})`},
			wantErr: `plugin name "dup" is already used by plugin "a"`,
		},
		{
			name: "visualizers with the same name",
			first: file{"a", `
				local p = plugin.register({name = "bars", type = "visualizer"})
				function p:render() return "a" end`},
			second: file{"b", `
				local p = plugin.register({name = "bars", type = "visualizer"})
				function p:render() return "b" end`},
			wantErr: `plugin name "bars" is already used by plugin "a"`,
			wantVis: []string{"bars"},
		},
		{
			// The second call fails before it claims a name.
			name:     "register again under a new name",
			first:    file{"a", `plugin.register({name = "one", type = "hook"}); pcall(plugin.register, {name = "two", type = "hook"})`},
			second:   file{"b", `plugin.register({name = "two", type = "hook"})`},
			wantName: "two",
		},
		{
			name:     "different names",
			first:    file{"a", `plugin.register({name = "one", type = "hook"})`},
			second:   file{"b", `plugin.register({name = "two", type = "hook"})`},
			wantName: "two",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestManager()
			loadTestPlugin(t, m, tt.first.name, tt.first.code)
			path := filepath.Join(t.TempDir(), tt.second.name+".lua")
			if err := os.WriteFile(path, []byte(tt.second.code), 0o644); err != nil {
				t.Fatal(err)
			}
			p, err := m.loadPlugin(path, tt.second.name, nil, knownPermissions)
			if tt.wantErr != "" {
				if p != nil || err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("loadPlugin() = %v, %v, want an error with %q", p, err, tt.wantErr)
				}
			} else if err != nil || p == nil || p.Name != tt.wantName {
				t.Fatalf("loadPlugin() = %v, %v, want a plugin named %q", p, err, tt.wantName)
			}
			m.finalizeVisualizers()
			if got := m.Visualizers(); tt.wantVis != nil && !slices.Equal(got, tt.wantVis) {
				t.Errorf("Visualizers() = %v, want %v", got, tt.wantVis)
			}
			if tt.wantVis != nil {
				if got := m.RenderVis("bars", [10]float64{}, 1, 1, 1); got != "a" {
					t.Errorf("RenderVis() = %q, want the frame of the first plugin", got)
				}
			}
		})
	}
}

func TestLoadPluginSyntaxError(t *testing.T) {
	m := newTestManager()
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.lua")
	os.WriteFile(path, []byte(`this is not valid lua!!!`), 0o644)

	_, err := m.loadPlugin(path, "bad", nil, knownPermissions)
	if err == nil {
		t.Fatal("expected error for invalid Lua syntax")
	}
}

func TestLoadPluginCleanupStopsPendingTimers(t *testing.T) {
	cases := []struct {
		name      string
		expectErr bool
		code      string
	}{
		{
			name: "without register",
			code: `
				cliamp.timer.after(0.01, function()
					cliamp.fs.write(%q, "fired")
				end)
				cliamp.sleep(0.05)
			`,
		},
		{
			name: "every",
			code: `
				cliamp.timer.every(0.01, function()
					cliamp.fs.write(%q, "fired")
				end)
				cliamp.sleep(0.05)
			`,
		},
		{
			name:      "on load error",
			expectErr: true,
			code: `
				local p = plugin.register({name = "bad", type = "hook"})
				cliamp.timer.after(0.01, function()
					cliamp.fs.write(%q, "fired")
				end)
				cliamp.sleep(0.05)
				error("boom")
			`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestManager()
			path := filepath.Join(t.TempDir(), "fired")
			code := fmt.Sprintf(tc.code, path)

			if tc.expectErr {
				loadTestPluginExpectError(t, m, "bad", code)
			} else {
				p := loadTestPlugin(t, m, "no-register-expired-timer", code)
				if p != nil {
					t.Fatalf("expected nil plugin for script without register, got %+v", p)
				}
			}

			time.Sleep(50 * time.Millisecond)

			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("timer callback wrote %q after cleanup, err=%v", path, err)
			}
		})
	}
}

// A plugin that fails to load after it started a process must not deadlock
// startup. Cleanup waits for the process, and its callbacks need the lock.
func TestLoadFailureWithPendingExecDoesNotHang(t *testing.T) {
	sleepBin, sleepArgs := execSleepCommand(10)
	echoBin, echoArgs, _ := execOutputCommand()
	tests := []struct {
		name string
		code string
	}{
		{"on_exit of a process that cleanup stops", fmt.Sprintf(`
			cliamp.exec.run(%q, {%s}, {on_exit = function() end})
			error("boom")
		`, sleepBin, luaStringList(sleepArgs))},
		{"on_stdout line that waits for the lock", fmt.Sprintf(`
			cliamp.exec.run(%q, {%s}, {on_stdout = function() end, on_exit = function() end})
			cliamp.sleep(0.2)
			error("boom")
		`, echoBin, luaStringList(echoArgs))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestManager()
			m.execs = newExecManager(execTestAllowedBinaries())
			path := filepath.Join(t.TempDir(), "failing.lua")
			code := `plugin.register({name = "failing", type = "hook", permissions = {"exec"}})` + tt.code
			if err := os.WriteFile(path, []byte(code), 0o644); err != nil {
				t.Fatal(err)
			}

			done := make(chan error, 1)
			go func() {
				_, err := m.loadPlugin(path, "failing", nil, knownPermissions)
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "boom") {
					t.Fatalf("loadPlugin() error = %v, want boom", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("loadPlugin did not return after a load error with a pending exec callback")
			}
		})
	}
}

// The top-level chunk of a plugin has a time limit, so a plugin that loops
// or sleeps at load cannot hang startup.
func TestLoadTimesOut(t *testing.T) {
	defer func(d time.Duration) { loadTimeout = d }(loadTimeout)
	loadTimeout = 200 * time.Millisecond

	tests := []struct {
		name    string
		code    string
		wantErr bool
	}{
		{"busy loop before register", `while true do end`, true},
		{"busy loop after register", `plugin.register({name = "slow", type = "hook"}) while true do end`, true},
		{"sleep after register", `plugin.register({name = "slow", type = "hook"}) cliamp.sleep(10)`, true},
		{"sleep before register", `cliamp.sleep(10) plugin.register({name = "slow", type = "hook"})`, true},
		{"short sleep within the limit", `cliamp.sleep(0.01) plugin.register({name = "slow", type = "hook"})`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestManager()
			path := filepath.Join(t.TempDir(), "slow.lua")
			if err := os.WriteFile(path, []byte(tt.code), 0o644); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := m.loadPlugin(path, "slow", nil, knownPermissions)
				done <- err
			}()
			select {
			case err := <-done:
				if tt.wantErr && (err == nil || !strings.Contains(err.Error(), "load did not finish")) {
					t.Fatalf("loadPlugin() error = %v, want the load time limit", err)
				}
				if !tt.wantErr && err != nil {
					t.Fatalf("loadPlugin() error = %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("loadPlugin did not return after the load time limit")
			}
			if !tt.wantErr && m.plugins[0].L.Context() != nil {
				t.Error("the loaded plugin keeps the load context")
			}
			m.Close()
		})
	}
}

func TestLoadPluginErrorRemovesHooks(t *testing.T) {
	m := newTestManager()
	loadTestPluginExpectError(t, m, "bad", `
		local p = plugin.register({name = "bad", type = "hook"})
		p:on("track.change", function() end)
		error("boom")
	`)
	if len(m.hooks["track.change"]) != 0 {
		t.Fatalf("hooks[track.change] = %d, want 0", len(m.hooks["track.change"]))
	}
}

func TestLoadVisualizerErrorRemovesVisualizer(t *testing.T) {
	m := newTestManager()
	loadTestPluginExpectError(t, m, "bad", `
		plugin.register({name = "bad", type = "visualizer"})
		error("boom")
	`)
	if len(m.visPlugs) != 0 {
		t.Fatalf("visualizer count = %d, want 0", len(m.visPlugs))
	}
	if _, ok := m.visMap["bad"]; ok {
		t.Fatal("expected visualizer to be removed from visMap")
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

// plugin.register() can run only once. The global stays callable after
// load, and a second call used to replace the permissions that install and
// trust showed.
func TestRegisterOnlyOnce(t *testing.T) {
	t.Run("second call at load", func(t *testing.T) {
		m := newTestManager()
		t.Cleanup(m.Close)
		dir := t.TempDir()
		path := filepath.Join(dir, "twice.lua")
		code := `
			plugin.register({name = "twice", type = "hook"})
			plugin.register({name = "twice", type = "hook", permissions = {"control"}})
		`
		if err := os.WriteFile(path, []byte(code), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := m.loadPlugin(path, "twice", nil, knownPermissions); err == nil || !strings.Contains(err.Error(), errRegisteredTwice.Error()) {
			t.Fatalf("loadPlugin() error = %v, want %v", err, errRegisteredTwice)
		}
	})
	t.Run("second call in a hook", func(t *testing.T) {
		m := newTestManager()
		m.logger = newPluginLogger(filepath.Join(t.TempDir(), pluginLogName))
		t.Cleanup(m.Close)
		var next atomic.Bool
		m.SetControlProvider(ControlProvider{Next: func() { next.Store(true) }})
		p := loadTestPlugin(t, m, "regrant", `
			plugin.register({name = "regrant", type = "hook"})
			local p2 = plugin.register
			cliamp.timer.after(0.001, function()
				pcall(p2, {name = "regrant", type = "hook", permissions = {"control"}})
				cliamp.player.next()
				_G.done = true
			end)
		`)
		waitGlobal(t, p, "done")
		if next.Load() {
			t.Error("a second plugin.register() granted the control permission")
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.perms[PermControl] {
			t.Error("perms[control] = true after a second plugin.register()")
		}
	})
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

func TestPluginCountAndHasHook(t *testing.T) {
	m := newTestManager()

	if m.PluginCount() != 0 {
		t.Fatalf("PluginCount() = %d, want 0", m.PluginCount())
	}
	if m.HasHook(EventAppStart) {
		t.Fatal("HasHook(app.start) = true, want false")
	}

	loadTestPlugin(t, m, "counter", `
		local p = plugin.register({name = "counter", type = "hook"})
		p:on("app.start", function() end)
	`)

	if m.PluginCount() != 1 {
		t.Fatalf("PluginCount() = %d, want 1", m.PluginCount())
	}
	if !m.HasHook(EventAppStart) {
		t.Fatal("HasHook(app.start) = false, want true")
	}
	if m.HasHook(EventTrackChange) {
		t.Fatal("HasHook(track.change) = true, want false")
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
		PlayerState:  func() string { return "playing" },
		Volume:       func() float64 { return -3.5 },
		CurrentTrack: func() Track { return Track{Title: "Angel", Artist: "Massive Attack"} },
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

// set_volume caps the volume at +6 dB and passes a low value on, so the
// player clamps it to its volume_min floor. set_speed clamps to 0.25 to 2.
func TestControlClampsBounds(t *testing.T) {
	tests := []struct {
		call      string
		wantVol   float64
		wantSpeed float64
	}{
		{"cliamp.player.set_volume(100)", 6, 0},
		{"cliamp.player.set_volume(-45)", -45, 0},
		{"cliamp.player.set_volume(-80)", -80, 0},
		{"cliamp.player.set_speed(10)", 0, 2},
		{"cliamp.player.set_speed(0.1)", 0, 0.25},
	}
	for _, tt := range tests {
		t.Run(tt.call, func(t *testing.T) {
			m := newTestManager()
			var gotVol, gotSpeed float64
			m.SetControlProvider(ControlProvider{
				SetVolume: func(db float64) { gotVol = db },
				SetSpeed:  func(r float64) { gotSpeed = r },
			})
			loadTestPlugin(t, m, "clamp-test", `
				plugin.register({name = "clamp-test", type = "hook", permissions = {"control"}})
				`+tt.call+`
			`)
			if gotVol != tt.wantVol || gotSpeed != tt.wantSpeed {
				t.Fatalf("volume, speed = %v, %v; want %v, %v", gotVol, gotSpeed, tt.wantVol, tt.wantSpeed)
			}
		})
	}
}

// main.go sets the ControlProvider after the top-level chunks ran. Until
// then, each control does nothing. Before, it called a nil func, and the
// plugin failed to load.
func TestControlBeforeProviderIsNoop(t *testing.T) {
	tests := []string{
		"cliamp.player.next()",
		"cliamp.player.prev()",
		"cliamp.player.play_pause()",
		"cliamp.player.stop()",
		"cliamp.player.set_volume(-10)",
		"cliamp.player.set_speed(1.5)",
		"cliamp.player.seek(30)",
		"cliamp.player.toggle_mono()",
		`cliamp.player.set_eq_preset("Rock")`,
		"cliamp.player.set_eq_band(1, 3)",
	}
	for _, call := range tests {
		t.Run(call, func(t *testing.T) {
			m := newTestManager()
			t.Cleanup(m.Close)
			loadTestPlugin(t, m, "early-control", `
				plugin.register({name = "early-control", type = "hook", permissions = {"control"}})
				`+call+`
			`)
		})
	}
}

func TestControlWithoutPermissionIsNoop(t *testing.T) {
	m := newTestManager()
	m.logger = newPluginLogger(filepath.Join(t.TempDir(), "test.log"))
	t.Cleanup(m.Close)
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
		_G.repeat_mode = cliamp.player.repeat_mode()
	`)

	tests := []struct {
		global string
		want   lua.LValue
	}{
		{"state", lua.LString("stopped")},
		{"vol", lua.LNumber(0)},
		{"speed", lua.LNumber(1)},
		{"title", lua.LString("")},
		// The same case as playlist.RepeatMode.String, which the provider returns.
		{"repeat_mode", lua.LString("Off")},
	}
	for _, tt := range tests {
		if got := p.L.GetGlobal(tt.global); got != tt.want {
			t.Errorf("default %s = %v, want %v", tt.global, got, tt.want)
		}
	}
}

// main.go sets the providers after the top-level chunks of the plugins ran,
// so a timer can read a provider while a set runs. Run with -race.
func TestSetProvidersWhilePluginsRun(t *testing.T) {
	m := newTestManager()
	p := loadTestPlugin(t, m, "race", `
		plugin.register({name = "race", type = "hook", permissions = {"control"}})
		cliamp.timer.every(0.001, function()
			_G.vol = cliamp.player.volume()
			cliamp.track.title()
			cliamp.queue.count()
			cliamp.message("tick")
			cliamp.player.next()
		end)
	`)
	defer m.Close()
	var nexts atomic.Int32
	for i := range 100 {
		m.SetStateProvider(StateProvider{Volume: func() float64 { return float64(-i) }})
		m.SetControlProvider(ControlProvider{Next: func() { nexts.Add(1) }})
		m.SetUIProvider(UIProvider{ShowMessage: func(string, time.Duration) {}})
		time.Sleep(200 * time.Microsecond)
	}
	deadline := time.Now().Add(time.Second)
	for nexts.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the timer never reached the control provider")
		}
		time.Sleep(time.Millisecond)
	}
	waitGlobal(t, p, "vol")
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
	setRenderTimeout(t, time.Second)
	m := newTestManager()
	loadTestPlugin(t, m, "test-vis", `
		local v = plugin.register({name = "test-vis", type = "visualizer"})
		v.init = function(self, rows, cols)
			_G.init_rows = rows
		end
		v.render = function(self, bands, frame, rows, cols)
			return "frame-" .. tostring(frame)
		end
		v.destroy = function(self)
			_G.destroyed = true
		end
	`)
	m.finalizeVisualizers()
	defer m.Close()

	names := m.Visualizers()
	if len(names) != 1 || names[0] != "test-vis" {
		t.Fatalf("Visualizers() = %v, want [test-vis]", names)
	}

	m.InitVis("test-vis", 8, 40)

	vis := m.visMap["test-vis"]
	waitGlobal(t, vis.plugin, "init_rows")

	got := m.RenderVis("test-vis", [10]float64{}, 8, 40, 42)
	if got != "frame-42" {
		t.Fatalf("RenderVis() = %q, want %q", got, "frame-42")
	}

	m.DestroyVis("test-vis")
	waitGlobal(t, vis.plugin, "destroyed")
}

// waitGlobal waits until the Lua global name of p is set.
// A timer that the top-level chunk started can write the plugin object while
// finalizeVisualizers reads it. Run with -race. Before finalizeVisualizers
// took the plugin lock, the race could abort the process.
func TestFinalizeVisualizersWhileTimerWrites(t *testing.T) {
	m := newTestManager()
	t.Cleanup(m.Close)
	p := loadTestPlugin(t, m, "busy-vis", `
		local p = plugin.register({name = "busy-vis", type = "visualizer"})
		local n = 0
		cliamp.timer.every(0.001, function()
			n = n + 1
			p["field" .. (n % 64)] = n
			_G.ticks = n
		end)
		function p:render() return "frame" end
	`)
	waitGlobal(t, p, "ticks")
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		m.finalizeVisualizers()
	}
	m.mu.RLock()
	vis := m.visMap["busy-vis"]
	m.mu.RUnlock()
	if vis.render == nil {
		t.Fatal("finalizeVisualizers did not find render")
	}
}

func waitGlobal(t *testing.T, p *Plugin, name string) {
	t.Helper()
	waitExec(t, p, p.L, name, time.Second)
}

// InitVis and DestroyVis run on the UI goroutine. They must return at once
// while a hook of the plugin holds the lock, because that hook can wait for
// the UI goroutine. The call runs after the lock is free.
func TestVisLifecycleCallsDoNotBlock(t *testing.T) {
	tests := []struct {
		name   string
		call   func(m *Manager)
		global string
	}{
		{"init", func(m *Manager) { m.InitVis("life-vis", 8, 40) }, "init_rows"},
		{"destroy", func(m *Manager) { m.DestroyVis("life-vis") }, "destroyed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestManager()
			p := loadTestPlugin(t, m, "life-vis", `
				local v = plugin.register({name = "life-vis", type = "visualizer"})
				function v:init(rows, cols) init_rows = rows end
				function v:render(bands, frame) return "frame-" .. frame end
				function v:destroy() destroyed = true end
			`)
			m.finalizeVisualizers()
			defer m.Close()

			p.mu.Lock()
			done := make(chan struct{})
			go func() {
				tt.call(m)
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(100 * time.Millisecond):
				t.Error("the call waited for the plugin lock")
			}
			p.mu.Unlock()
			waitGlobal(t, p, tt.global)
		})
	}
}

// RenderVis must not run render before a queued init has run.
func TestRenderVisWaitsForInit(t *testing.T) {
	setRenderTimeout(t, time.Second)
	m := newTestManager()
	p := loadTestPlugin(t, m, "init-vis", `
		local v = plugin.register({name = "init-vis", type = "visualizer"})
		local ready = false
		function v:init() ready = true end
		function v:render()
			if ready then return "ready" end
			return "early"
		end
	`)
	m.finalizeVisualizers()
	defer m.Close()

	// Hold the queue of the plugin, so init waits behind this call.
	release := make(chan struct{})
	m.mu.RLock()
	m.enqueue(p, "block", func() { <-release })
	m.mu.RUnlock()

	m.InitVis("init-vis", 8, 40)
	if got := m.RenderVis("init-vis", [10]float64{}, 8, 40, 1); got != "" {
		t.Errorf("RenderVis() = %q while init waits, want the empty last frame", got)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for m.RenderVis("init-vis", [10]float64{}, 8, 40, 2) != "ready" {
		if time.Now().After(deadline) {
			t.Fatal("RenderVis() did not render after init ran")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRenderVisReusesLastOnError(t *testing.T) {
	setRenderTimeout(t, time.Second)
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

// RenderVis runs on the UI goroutine. When another callback of the plugin
// holds the lock, it returns the last frame at once instead of waiting.
func TestRenderVisReturnsLastFrameWhenPluginBusy(t *testing.T) {
	setRenderTimeout(t, time.Second)
	m := newTestManager()
	p := loadTestPlugin(t, m, "busy-vis", `
		local v = plugin.register({name = "busy-vis", type = "visualizer"})
		function v:render(bands, frame) return "frame-" .. frame end
	`)
	m.finalizeVisualizers()
	defer m.Close()
	if got := m.RenderVis("busy-vis", [10]float64{}, 8, 40, 1); got != "frame-1" {
		t.Fatalf("RenderVis() = %q, want frame-1", got)
	}

	p.mu.Lock()
	done := make(chan string, 1)
	go func() { done <- m.RenderVis("busy-vis", [10]float64{}, 8, 40, 2) }()
	select {
	case got := <-done:
		if got != "frame-1" {
			t.Errorf("RenderVis() = %q, want the last frame frame-1", got)
		}
	case <-time.After(time.Second):
		t.Error("RenderVis waited for the plugin lock")
	}
	p.mu.Unlock()
	if got := m.RenderVis("busy-vis", [10]float64{}, 8, 40, 3); got != "frame-3" {
		t.Errorf("RenderVis() = %q, want frame-3 after the lock is free", got)
	}
}

// A hook of a visualizer plugin calls a control that waits for the event
// loop, as prog.Send does, while the event loop renders the same plugin.
// RenderVis must return without the plugin lock, so the loop can read the
// message and both sides go on.
func TestVisualizerHookControlDuringRender(t *testing.T) {
	setRenderTimeout(t, time.Second)
	m := newTestManager()
	calling := make(chan struct{})
	loop := make(chan string) // unbuffered, as the Bubbletea message channel
	m.SetControlProvider(ControlProvider{Next: func() {
		close(calling)
		loop <- "next"
	}})
	loadTestPlugin(t, m, "ctl-vis", `
		local v = plugin.register({name = "ctl-vis", type = "visualizer", permissions = {"control"}})
		function v:render(bands, frame) return "frame-" .. frame end
		v:on("track.change", function() cliamp.player.next() end)
	`)
	m.finalizeVisualizers()
	defer m.Close()

	m.Emit(EventTrackChange, nil)
	done := make(chan string, 1)
	go func() {
		// The hook holds the plugin lock from here until the loop reads
		// the message. The event loop renders a frame first.
		<-calling
		m.RenderVis("ctl-vis", [10]float64{}, 8, 40, 1)
		done <- <-loop
	}()
	select {
	case msg := <-done:
		if msg != "next" {
			t.Fatalf("event loop read %q, want next", msg)
		}
	case <-time.After(2 * time.Second):
		go func() { <-loop }() // free the hook, so Close does not wait for it
		t.Fatal("the render and the control call waited for each other")
	}
}

// A render that runs past renderTimeout stops, and RenderVis returns the last
// frame.
// A render that waits in a Go API also ends at renderTimeout, because each
// API that can block uses the call context. Before, an HTTP request, a
// notify-send run or the read of a FIFO held the UI goroutine.
func TestRenderVisTimeout(t *testing.T) {
	tests := []struct {
		name  string
		body  func(t *testing.T) string
		frame string // the frame that RenderVis returns
	}{
		{"busy loop", func(*testing.T) string { return "while true do end" }, "frame-1"},
		{"sleep", func(*testing.T) string { return "cliamp.sleep(10)" }, "frame-1"},
		{"http request", func(t *testing.T) string {
			release := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			t.Cleanup(srv.Close)
			t.Cleanup(func() { close(release) })
			// The test server is on loopback, which the real client blocks.
			old := httpClient
			httpClient = srv.Client()
			httpClient.Timeout = 2 * time.Second
			t.Cleanup(func() { httpClient = old })
			return fmt.Sprintf("cliamp.http.get(%q)", srv.URL)
		}, "frame-1"},
		{"notify", func(t *testing.T) string {
			if runtime.GOOS == "windows" {
				t.Skip("needs a shell script as notify-send")
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "notify-send"), []byte("#!/bin/sh\nexec sleep 3\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			return `cliamp.notify("title")`
		}, "frame-1"},
		{"read of a FIFO", func(t *testing.T) string {
			mkfifo, err := exec.LookPath("mkfifo")
			if err != nil {
				t.Skip("mkfifo is not available")
			}
			path := filepath.Join(t.TempDir(), "fifo")
			if out, err := exec.Command(mkfifo, path).CombinedOutput(); err != nil {
				t.Skipf("mkfifo: %v: %s", err, out)
			}
			// fs.read refuses the FIFO at once, so render finishes.
			return fmt.Sprintf("assert(not cliamp.fs.read(%q))", path)
		}, "frame-2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := tt.body(t)
			m := newTestManager()
			m.logger = newPluginLogger(filepath.Join(t.TempDir(), pluginLogName))
			loadTestPlugin(t, m, "slow-vis", `
				local v = plugin.register({name = "slow-vis", type = "visualizer"})
				function v:render(bands, frame)
					if frame > 1 then `+body+` end
					return "frame-" .. frame
				end
			`)
			m.finalizeVisualizers()
			defer m.Close()
			setRenderTimeout(t, time.Second)
			m.RenderVis("slow-vis", [10]float64{}, 8, 40, 1)

			renderTimeout = 20 * time.Millisecond
			start := time.Now()
			got := m.RenderVis("slow-vis", [10]float64{}, 8, 40, 2)
			if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
				t.Errorf("RenderVis() took %v, want about %v", elapsed, renderTimeout)
			}
			if got != tt.frame {
				t.Errorf("RenderVis() = %q, want %q", got, tt.frame)
			}
		})
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

func TestPlayerEQBands(t *testing.T) {
	bands := [10]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, -10}
	tests := []struct {
		name  string
		state StateProvider
		want  []any
	}{
		{"no provider", StateProvider{}, nil},
		{"provider", StateProvider{EQBands: func() [10]float64 { return bands }},
			[]any{1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0, 9.0, -10.0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			L := lua.NewState()
			defer L.Close()
			cliamp := L.NewTable()
			registerPlayerAPI(L, cliamp, fixed(&tt.state))
			L.SetGlobal("cliamp", cliamp)
			if err := L.DoString(`_G.bands = cliamp.player.eq_bands()`); err != nil {
				t.Fatal(err)
			}
			tbl := L.GetGlobal("bands").(*lua.LTable)
			if tt.want == nil {
				if n := tbl.Len(); n != 0 {
					t.Fatalf("eq_bands() has %d values, want 0", n)
				}
				return
			}
			if got := luaToGo(tbl); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("eq_bands() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Event payloads, JSON and the store share one converter, so a slice in an
// event becomes a Lua array and not its fmt string.
func TestDataToTableValues(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want any // the value after a round trip through luaToGo
	}{
		{"nil", nil, nil},
		{"bool", true, true},
		{"string", "hi", "hi"},
		{"int", 42, 42.0},
		{"int64", int64(-7), -7.0},
		{"float64", 3.5, 3.5},
		{"float slice", []float64{1, 2}, []any{1.0, 2.0}},
		{"string slice", []string{"a", "b"}, []any{"a", "b"}},
		{"any slice", []any{"a", 1.0, []any{true}}, []any{"a", 1.0, []any{true}}},
		{"map", map[string]any{"k": []any{"v"}}, map[string]any{"k": []any{"v"}}},
		{"other type", struct{ A int }{1}, "{1}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			L := lua.NewState()
			defer L.Close()
			tbl := dataToTable(L, map[string]any{"v": tt.in})
			if got := luaToGo(tbl.RawGetString("v")); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("value = %#v, want %#v", got, tt.want)
			}
		})
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

// recorder collects values that a plugin passes to the global record().
type recorder struct {
	mu   sync.Mutex
	seen []string
}

// install sets the global record() in the VM of p.
func (r *recorder) install(p *Plugin) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.L.SetGlobal("record", p.L.NewFunction(func(L *lua.LState) int {
		r.mu.Lock()
		r.seen = append(r.seen, L.CheckString(1))
		r.mu.Unlock()
		return 0
	}))
}

func (r *recorder) values() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.seen)
}

// wait returns the recorded values once there are n of them.
func (r *recorder) wait(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := r.values()
		if len(got) >= n || time.Now().After(deadline) {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A coroutine runs under the time limit of the call that resumes it. Before,
// it kept the context of the call that created it. That context ended with
// its call, so a later resume failed with "context canceled".
func TestCoroutineResumedInLaterCall(t *testing.T) {
	const counter = `function() local i = 0 while true do i = i + 1 coroutine.yield(i) end end`
	tests := []struct {
		name string
		code string
		want []string
	}{
		{"wrap made at load", `
			local gen = coroutine.wrap(` + counter + `)
			p:on("ev", function() record(tostring(gen())) end)
		`, []string{"1", "2"}},
		{"resume made at load", `
			local co = coroutine.create(` + counter + `)
			p:on("ev", function()
				local ok, v = coroutine.resume(co)
				record(tostring(ok) .. " " .. tostring(v))
			end)
		`, []string{"true 1", "true 2"}},
		{"wrap made in a callback", `
			local gen
			p:on("ev", function()
				gen = gen or coroutine.wrap(` + counter + `)
				record(tostring(gen()))
			end)
		`, []string{"1", "2"}},
		{"nested coroutines", `
			local inner = coroutine.wrap(` + counter + `)
			local outer = coroutine.wrap(function()
				while true do coroutine.yield(inner() * 10) end
			end)
			p:on("ev", function() record(tostring(outer())) end)
		`, []string{"10", "20"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestManager()
			m.logger = newPluginLogger(filepath.Join(t.TempDir(), pluginLogName))
			t.Cleanup(m.Close)
			p := loadTestPlugin(t, m, "co", `
				local p = plugin.register({name = "co", type = "hook"})
				`+tt.code)
			var rec recorder
			rec.install(p)
			m.Emit("ev", nil)
			m.Emit("ev", nil)
			if got := rec.wait(t, len(tt.want)); !slices.Equal(got, tt.want) {
				log, _ := os.ReadFile(m.logger.path)
				t.Fatalf("hooks recorded %v, want %v; plugins.log: %s", got, tt.want, log)
			}
		})
	}
}

// A coroutine that never yields still stops at the time limit of the call
// that resumes it.
func TestCoroutineKeepsCallTimeLimit(t *testing.T) {
	defer func(d time.Duration) { hookTimeout = d }(hookTimeout)
	hookTimeout = 100 * time.Millisecond

	m := newTestManager()
	m.logger = newPluginLogger(filepath.Join(t.TempDir(), pluginLogName))
	t.Cleanup(m.Close)
	p := loadTestPlugin(t, m, "co-loop", `
		local p = plugin.register({name = "co-loop", type = "hook"})
		local spin = coroutine.wrap(function() while true do end end)
		p:on("spin", function() spin() end)
		p:on("after", function() record("after") end)
	`)
	var rec recorder
	rec.install(p)
	m.Emit("spin", nil)
	m.Emit("after", nil)
	if got := rec.wait(t, 1); !slices.Equal(got, []string{"after"}) {
		t.Fatalf("hooks recorded %v, want the next event after the time limit", got)
	}
	log, _ := os.ReadFile(m.logger.path)
	if !strings.Contains(string(log), "did not finish in "+hookTimeout.String()) {
		t.Errorf("plugins.log = %q, want the time limit error", log)
	}
}

// Each plugin gets its events and key presses in the order cliamp sent them.
// Before the queue, each event ran in its own goroutine, and the plugin lock
// does not wake goroutines in order.
func TestEmitPreservesOrderPerPlugin(t *testing.T) {
	// Both rows send 200 calls, which fits the queue, so none may drop.
	tests := []struct {
		name string
		n    int
		keys bool
	}{
		{"events", 200, false},
		{"events and key presses", 100, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestManager()
			p := loadTestPlugin(t, m, "order", `
				local p = plugin.register({name = "order", type = "hook", permissions = {"keymap"}})
				p:on("ev", function(data) record(tostring(data.n)) end)
				p:bind("ctrl+y", function(key) record(key) end)
			`)
			var rec recorder
			rec.install(p)

			var want []string
			for i := 1; i <= tt.n; i++ {
				m.Emit("ev", map[string]any{"n": i})
				want = append(want, fmt.Sprint(i))
				if tt.keys {
					m.EmitKey("ctrl+y")
					want = append(want, "ctrl+y")
				}
			}
			got := rec.wait(t, len(want))
			if len(got) != len(want) {
				t.Fatalf("plugin saw %d calls, want %d", len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("call %d = %s, want %s: the plugin saw its calls out of order", i+1, got[i], want[i])
				}
			}
		})
	}
}

// A plugin that falls behind drops new events instead of growing a goroutine
// for each one. One burst of drops logs one line.
func TestEmitDropsWhenQueueFull(t *testing.T) {
	m := newTestManager()
	logPath := filepath.Join(t.TempDir(), pluginLogName)
	m.logger = newPluginLogger(logPath)
	p := loadTestPlugin(t, m, "slow", `
		local p = plugin.register({name = "slow", type = "hook"})
		p:on("ev", function(data) record(tostring(data.n)) end)
	`)
	var rec recorder
	rec.install(p)

	// Hold the plugin lock, so the worker blocks on the first event. Wait
	// until the worker took it, then fill the queue and send 10 more.
	p.mu.Lock()
	m.Emit("ev", map[string]any{"n": 1})
	for len(p.queue) > 0 {
		time.Sleep(time.Millisecond)
	}
	for i := 2; i <= eventQueueSize+11; i++ {
		m.Emit("ev", map[string]any{"n": i})
	}
	p.mu.Unlock()

	want := eventQueueSize + 1
	rec.wait(t, want)
	time.Sleep(20 * time.Millisecond) // let an extra event arrive if one was kept
	got := rec.values()
	if len(got) != want {
		t.Fatalf("plugin saw %d events, want %d", len(got), want)
	}
	for i, v := range got {
		if v != fmt.Sprint(i+1) {
			t.Fatalf("event %d = %s, want %d: the queue must keep the first events", i, v, i+1)
		}
	}

	m.Emit("ev", map[string]any{"n": "after"})
	if got := rec.wait(t, len(got)+1); got[len(got)-1] != "after" {
		t.Fatalf("last event = %s, want the event sent after the queue drained", got[len(got)-1])
	}
	m.Close()
	data, _ := os.ReadFile(logPath)
	if n := strings.Count(string(data), "ev handler dropped"); n != 1 {
		t.Fatalf("plugins.log has %d drop warnings, want 1:\n%s", n, data)
	}
}

// Close runs the events that are already queued, then app.quit.
func TestCloseRunsQueuedEventsBeforeQuit(t *testing.T) {
	m := newTestManager()
	p := loadTestPlugin(t, m, "quit-order", `
		local p = plugin.register({name = "quit-order", type = "hook"})
		p:on("ev", function(data) record(tostring(data.n)) end)
		p:on("app.quit", function() record("quit") end)
	`)
	var rec recorder
	rec.install(p)

	p.mu.Lock()
	var want []string
	for i := 1; i <= 10; i++ {
		m.Emit("ev", map[string]any{"n": i})
		want = append(want, fmt.Sprint(i))
	}
	done := make(chan struct{})
	go func() {
		m.Close()
		close(done)
	}()
	p.mu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return")
	}
	if got := rec.values(); !slices.Equal(got, append(want, "quit")) {
		t.Fatalf("plugin saw %v, want the queued events and then quit", got)
	}
}

// Close stops the run of queued events after closeDrainBudget, so a slow
// plugin with a full queue cannot hold up quit. app.quit still runs.
func TestCloseBoundsQueueDrain(t *testing.T) {
	defer func(d time.Duration) { hookTimeout = d }(hookTimeout)
	hookTimeout = 200 * time.Millisecond
	defer func(d time.Duration) { closeDrainBudget = d }(closeDrainBudget)
	closeDrainBudget = 50 * time.Millisecond

	tests := []struct {
		name string
		body string
	}{
		{"sleep", "cliamp.sleep(0.2)"},
		{"busy loop", "while true do end"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestManager()
			p := loadTestPlugin(t, m, "slow-events", `
				local p = plugin.register({name = "slow-events", type = "hook"})
				p:on("ev", function() record("ev") `+tt.body+` end)
				p:on("app.quit", function() record("quit") end)
			`)
			var rec recorder
			rec.install(p)
			const events = 20
			for range events {
				m.Emit("ev", nil)
			}

			start := time.Now()
			m.Close()
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Errorf("Close() took %v, want less than 1s", elapsed)
			}
			got := rec.values()
			if len(got) == 0 || got[len(got)-1] != "quit" {
				t.Fatalf("plugin saw %v, want app.quit last", got)
			}
			if n := len(got) - 1; n >= events {
				t.Errorf("plugin ran %d of %d queued events, want Close to drop the rest", n, events)
			}
		})
	}
}

func TestNewTreatsBadTrustManifestAsUntrusted(t *testing.T) {
	const code = `plugin.register({name = "hello", type = "hook"})`
	tests := []struct {
		name     string
		manifest func(hash string) string
		bad      bool
	}{
		{"not JSON", func(string) string { return "{" }, true},
		{"null plugins map", func(string) string { return `{"version":1,"plugins":null}` }, true},
		{"unsupported version with a matching hash", func(hash string) string {
			return `{"version":2,"plugins":{"hello":"` + hash + `"}}`
		}, true},
		{"valid manifest without the approval", func(string) string { return `{"version":1,"plugins":{}}` }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			cfg := filepath.Join(home, ".config", "cliamp")
			dir := filepath.Join(cfg, "plugins")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "hello.lua")
			if err := os.WriteFile(path, []byte(code), 0o644); err != nil {
				t.Fatal(err)
			}
			hash, err := plugintrust.HashFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".trust.json"), []byte(tt.manifest(hash)), 0o600); err != nil {
				t.Fatal(err)
			}

			m, err := New(nil, nil, nil)
			if m == nil {
				t.Fatal("New returned a nil Manager")
			}
			defer m.Close()
			if got := m.PluginCount(); got != 0 {
				t.Errorf("PluginCount() = %d, want 0", got)
			}
			if err == nil || !strings.Contains(err.Error(), "hello: "+plugintrust.ErrUntrusted.Error()) {
				t.Fatalf("New() error = %v, want hello as untrusted", err)
			}
			// cliamp plugins trust fails while the manifest does not load, so
			// the error names the file to delete instead.
			recovery := "delete " + filepath.Join(dir, ".trust.json") + " and approve each plugin again"
			hint := "run `cliamp plugins trust hello`"
			if got := strings.Contains(err.Error(), "trust manifest"); got != tt.bad {
				t.Errorf("New() error = %v, want the manifest error: %v", err, tt.bad)
			}
			if got := strings.Contains(err.Error(), recovery); got != tt.bad {
				t.Errorf("New() error = %v, want the recovery step %q: %v", err, recovery, tt.bad)
			}
			if got := strings.Contains(err.Error(), hint); got == tt.bad {
				t.Errorf("New() error = %v, want the hint %q: %v", err, hint, !tt.bad)
			}
			log, _ := os.ReadFile(filepath.Join(cfg, pluginLogName))
			if got := strings.Contains(string(log), "trust manifest"); got != tt.bad {
				t.Errorf("plugins.log = %q, want the manifest error: %v", log, tt.bad)
			}
		})
	}
}

// The player loads a plugin only with the permissions that the approval
// prompt showed. ReadMetadata runs the plugin against stubs, so a plugin can
// detect the stubs and register other permissions at runtime. An approval
// without a recorded list approves what ReadMetadata finds in the file.
func TestNewEnforcesApprovedPermissions(t *testing.T) {
	const (
		control     = `plugin.register({name = "p", type = "hook", permissions = {"control"}})`
		stubAware   = `if cliamp.player.state() == nil then plugin.register({name = "p", type = "hook"}) else plugin.register({name = "p", type = "hook", permissions = {"exec"}}) end`
		notApproved = `permission %q is not approved`
	)
	tests := []struct {
		name     string
		code     string
		recorded []string // nil approves with plugintrust.Approve, which records no list
		wantErr  string
	}{
		{"recorded list with the permission", control, []string{"control"}, ""},
		{"recorded list without the permission", control, []string{}, fmt.Sprintf(notApproved, "control")},
		{"no recorded list", control, nil, ""},
		{"stub-aware plugin with a recorded list", stubAware, []string{}, fmt.Sprintf(notApproved, "exec")},
		{"stub-aware plugin without a recorded list", stubAware, nil, fmt.Sprintf(notApproved, "exec")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			dir := filepath.Join(home, ".config", "cliamp", "plugins")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "p.lua")
			if err := os.WriteFile(path, []byte(tt.code), 0o644); err != nil {
				t.Fatal(err)
			}
			if tt.recorded == nil {
				if _, err := plugintrust.Approve(dir, "p", path); err != nil {
					t.Fatal(err)
				}
			} else if err := plugintrust.ApproveHash(dir, "p", path, plugintrust.Hash([]byte(tt.code)), tt.recorded); err != nil {
				t.Fatal(err)
			}

			m, err := New(nil, nil, nil)
			if m == nil {
				t.Fatal("New returned a nil Manager")
			}
			defer m.Close()
			if tt.wantErr == "" {
				if err != nil || m.PluginCount() != 1 {
					t.Fatalf("New() = %d plugins, %v, want the plugin", m.PluginCount(), err)
				}
				return
			}
			if m.PluginCount() != 0 || err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("New() = %d plugins, %v, want an error with %q", m.PluginCount(), err, tt.wantErr)
			}
		})
	}
}

// Close must wait for a timer callback that runs, then close the VM. Before
// it took the plugin lock, the callback kept running on the closed VM and
// crashed the process.
func TestCloseDuringTimerCallback(t *testing.T) {
	for i := range 20 {
		m := newTestManager()
		loadTestPlugin(t, m, fmt.Sprintf("busy-timer-%d", i), `
			plugin.register({name = "busy-timer", type = "hook"})
			cliamp.timer.every(0.001, function()
				local x = 0
				for n = 1, 20000 do x = x + n end
			end)
		`)
		time.Sleep(20 * time.Millisecond)
		m.Close()
	}
	// Give a callback that outlived Close the time to touch its VM.
	time.Sleep(50 * time.Millisecond)
}

func TestCloseBoundsQuitHook(t *testing.T) {
	defer func(d time.Duration) { hookTimeout = d }(hookTimeout)
	hookTimeout = 50 * time.Millisecond

	tests := []struct {
		name string
		body string
	}{
		{"busy loop", "while true do end"},
		{"sleep", "cliamp.sleep(10)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestManager()
			loadTestPlugin(t, m, "slow-quit", `
				local p = plugin.register({name = "slow-quit", type = "hook"})
				p:on("app.quit", function() `+tt.body+` end)
			`)
			done := make(chan struct{})
			go func() {
				m.Close()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Close did not return while an app.quit hook ran")
			}
		})
	}
}

// Close stops Lua that still runs: an IPC command, or a timer callback.
// Before, Close waited up to 5 minutes for a command and 5 seconds for a
// timer callback. Close stops a command before it runs the queued events and
// app.quit, because they wait for the plugin lock that the command holds.
// Before, a plugin with an app.quit hook or a queued event made Close wait
// for the command.
func TestCloseStopsRunningLua(t *testing.T) {
	const (
		cmdLoop  = `p:command("run", function() while true do end end)`
		cmdSleep = `p:command("run", function() while true do cliamp.sleep(10) end end)`
	)
	tests := []struct {
		name    string
		code    string
		command bool
		event   bool // queue an event while the Lua call runs
	}{
		{"command loop", cmdLoop, true, false},
		{"command sleep", cmdSleep, true, false},
		{"command loop with a quit hook", cmdLoop + ` p:on("app.quit", function() record("quit") end)`, true, false},
		{"command sleep with a quit hook", cmdSleep + ` p:on("app.quit", function() record("quit") end)`, true, false},
		{"command loop with a queued event", cmdLoop + ` p:on("ev", function() record("ev") end)`, true, true},
		{"command sleep with a queued event", cmdSleep + ` p:on("ev", function() record("ev") end)`, true, true},
		{"timer loop", `cliamp.timer.after(0.001, function() while true do end end)`, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestManager()
			p := loadTestPlugin(t, m, "spin", `
				local p = plugin.register({name = "spin", type = "hook"})
				`+tt.code)
			var rec recorder
			rec.install(p)
			cmdErr := make(chan error, 1)
			if tt.command {
				go func() {
					_, err := m.EmitCommand(context.Background(), "spin", "run", nil)
					cmdErr <- err
				}()
			}
			// Wait until the Lua call holds the plugin lock.
			deadline := time.Now().Add(time.Second)
			for p.mu.TryLock() {
				p.mu.Unlock()
				if time.Now().After(deadline) {
					t.Fatal("the Lua call did not start")
				}
				time.Sleep(time.Millisecond)
			}
			if tt.event {
				m.Emit("ev", nil)
			}

			done := make(chan struct{})
			go func() {
				m.Close()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("Close did not stop the running Lua call")
			}
			if tt.command {
				if err := <-cmdErr; !errors.Is(err, errClosed) {
					t.Errorf("EmitCommand() error = %v, want %v", err, errClosed)
				}
			}
			// The queued event and app.quit still run after the stop.
			if tt.event {
				if got := rec.values(); !slices.Equal(got, []string{"ev"}) {
					t.Errorf("plugin saw %v, want the queued event", got)
				}
			}
			if strings.Contains(tt.code, "app.quit") {
				if got := rec.values(); !slices.Equal(got, []string{"quit"}) {
					t.Errorf("plugin saw %v, want app.quit", got)
				}
			}
		})
	}
}

// Every entry point must be safe after Close and must not run Lua.
func TestCallsAfterClose(t *testing.T) {
	m := newTestManager()
	loadTestPlugin(t, m, "closed", `
		local p = plugin.register({name = "closed", type = "visualizer", permissions = {"keymap"}})
		p:on("test.event", function() end)
		p:bind("ctrl+y", function() end)
		p:command("ping", function() return "pong" end)
		function p:init() end
		function p:render() return "frame" end
		function p:destroy() end
	`)
	m.finalizeVisualizers()
	m.Close()

	tests := []struct {
		name string
		call func() error
	}{
		{"Emit", func() error { m.Emit("test.event", nil); return nil }},
		{"EmitSync", func() error { m.EmitSync("test.event", nil); return nil }},
		{"EmitKey", func() error {
			if m.EmitKey("ctrl+y") {
				return fmt.Errorf("EmitKey() = true, want false")
			}
			return nil
		}},
		{"EmitCommand", func() error {
			if out, err := m.EmitCommand(context.Background(), "closed", "ping", nil); err == nil {
				return fmt.Errorf("EmitCommand() = %q, nil, want an error", out)
			}
			return nil
		}},
		{"InitVis", func() error { m.InitVis("closed", 8, 40); return nil }},
		{"RenderVis", func() error {
			if got := m.RenderVis("closed", [10]float64{}, 8, 40, 1); got != "" {
				return fmt.Errorf("RenderVis() = %q, want the empty last frame", got)
			}
			return nil
		}},
		{"DestroyVis", func() error { m.DestroyVis("closed"); return nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); err != nil {
				t.Error(err)
			}
			m.wg.Wait()
		})
	}
}

// Timer callback errors reach plugins.log. An error that repeats is logged
// once, so a fast timer cannot flood the log.
func TestCallbackErrorsAreLogged(t *testing.T) {
	tests := []struct {
		name string
		code string
	}{
		{"timer.after", `cliamp.timer.after(0.001, function() error("callback failed") end)`},
		{"timer.every repeats one error", `cliamp.timer.every(0.001, function() error("callback failed") end)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestManager()
			logPath := filepath.Join(t.TempDir(), pluginLogName)
			m.logger = newPluginLogger(logPath)
			loadTestPlugin(t, m, "failing", `
				plugin.register({name = "failing", type = "hook"})
				`+tt.code)
			time.Sleep(50 * time.Millisecond)
			m.Close()

			data, _ := os.ReadFile(logPath)
			if got := strings.Count(string(data), "callback failed"); got != 1 {
				t.Fatalf("plugins.log has %d entries for the error, want 1:\n%s", got, data)
			}
		})
	}
}

// Callback errors go to plugins.log only. A write to stderr corrupts the TUI.
// A render runs on each frame, so it logs its first error once, even when it
// fails only on some frames. A render timeout has its own log entry, so it
// does not hide a later Lua error.
func TestRenderErrorsLogOnceAndSkipStderr(t *testing.T) {
	tests := []struct {
		name         string
		limit        time.Duration
		render       string
		wantTimeouts int
	}{
		{"fails on alternate frames", time.Second, `if frame % 2 == 0 then error("render failed " .. frame) end`, 0},
		{"fails on each frame", time.Second, `error("render failed")`, 0},
		{"a timeout does not hide a later error", 50 * time.Millisecond, `if frame == 0 then while true do end end error("render failed")`, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRenderTimeout(t, tt.limit)
			var stderr bytes.Buffer
			defer log.SetOutput(log.Writer())
			log.SetOutput(&stderr)

			m := newTestManager()
			logPath := filepath.Join(t.TempDir(), pluginLogName)
			m.logger = newPluginLogger(logPath)
			loadTestPlugin(t, m, "flaky-vis", `
				local v = plugin.register({name = "flaky-vis", type = "visualizer"})
				function v:render(bands, frame) `+tt.render+` return "ok" end
			`)
			m.finalizeVisualizers()
			for frame := range uint64(10) {
				m.RenderVis("flaky-vis", [10]float64{}, 8, 40, frame)
			}
			m.Close()

			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want nothing", stderr.String())
			}
			data, _ := os.ReadFile(logPath)
			if got := strings.Count(string(data), "render failed"); got != 1 {
				t.Errorf("plugins.log has %d render errors, want 1:\n%s", got, data)
			}
			timeout := "render error: did not finish in " + tt.limit.String()
			if got := strings.Count(string(data), timeout); got != tt.wantTimeouts {
				t.Errorf("plugins.log has %d render timeouts, want %d:\n%s", got, tt.wantTimeouts, data)
			}
		})
	}
}
