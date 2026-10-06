package luaplugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// A permission denial keeps the result shape each function had, so plugins
// that check it keep working. Each permission logs one warning, whichever
// function is denied first.
func TestPermissionDenials(t *testing.T) {
	m := newTestManager()
	logPath := filepath.Join(t.TempDir(), pluginLogName)
	m.logger = newPluginLogger(logPath)
	var calls int
	m.SetControlProvider(ControlProvider{
		Next:          func() { calls++ },
		QueueJump:     func(int) { calls++ },
		QueueAddTrack: func(Track) { calls++ },
	})
	p := loadTestPlugin(t, m, "denied", `
		_G.p = plugin.register({name = "denied", type = "hook"})
		function _G.results(...) return {n = select("#", ...), ...} end
	`)

	tests := []struct {
		name string
		call string
		want []lua.LValue
	}{
		{"player control returns nothing", `cliamp.player.next()`, nil},
		{"queue.jump returns nothing", `cliamp.queue.jump(0)`, nil},
		{"queue.add with a table returns nil and a message", `cliamp.queue.add({path = "/a.mp3"})`,
			[]lua.LValue{lua.LNil, lua.LString(`queue.add: requires permissions = {"control"}`)}},
		{"exec.run returns nil and a message", `cliamp.exec.run("yt-dlp", {})`,
			[]lua.LValue{lua.LNil, lua.LString("exec permission required")}},
		{"exec.run again", `cliamp.exec.run("yt-dlp", {})`,
			[]lua.LValue{lua.LNil, lua.LString("exec permission required")}},
		{"bind returns false and a message", `p:bind("ctrl+y", function() end)`,
			[]lua.LValue{lua.LFalse, lua.LString("keymap permission required")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p.mu.Lock()
			defer p.mu.Unlock()
			if err := p.L.DoString(`_G.got = results(` + tt.call + `)`); err != nil {
				t.Fatal(err)
			}
			got := p.L.GetGlobal("got").(*lua.LTable)
			if n := int(got.RawGetString("n").(lua.LNumber)); n != len(tt.want) {
				t.Fatalf("%s returned %d values, want %d", tt.call, n, len(tt.want))
			}
			for i, want := range tt.want {
				if v := got.RawGetInt(i + 1); v != want {
					t.Errorf("result %d = %v, want %v", i+1, v, want)
				}
			}
		})
	}
	if calls != 0 {
		t.Errorf("control provider ran %d times without the permission", calls)
	}

	m.Close()
	data, _ := os.ReadFile(logPath)
	log := string(data)
	for _, want := range []string{
		`cliamp.player.next requires permissions = {"control"}`,
		`cliamp.exec.run requires permissions = {"exec"}`,
		`p:bind requires permissions = {"keymap"}`,
	} {
		if n := strings.Count(log, want); n != 1 {
			t.Errorf("plugins.log has %d lines with %q, want 1:\n%s", n, want, log)
		}
	}
	if n := strings.Count(log, "requires permissions"); n != 3 {
		t.Errorf("plugins.log has %d permission warnings, want 1 for each of 3 permissions:\n%s", n, log)
	}
}
