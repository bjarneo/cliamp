package luaplugin

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// newExecTestState builds a minimal plugin + manager wired up for exec tests.
// The binary allowlist is scoped to a few harmless OS-native tools so tests
// don't need yt-dlp or ffmpeg.
func newExecTestState(t *testing.T, perms []string) (*lua.LState, *Plugin, *execManager, func()) {
	t.Helper()
	L := lua.NewState()

	p := &Plugin{Name: "test", L: L}
	if len(perms) > 0 {
		p.perms = make(map[string]bool)
		for _, perm := range perms {
			p.perms[perm] = true
		}
	}

	em := newExecManager(execTestAllowedBinaries())
	cliamp := L.NewTable()
	registerExecAPI(L, cliamp, em, p, newPluginLogger(""))
	L.SetGlobal("cliamp", cliamp)

	return L, p, em, func() { em.stopAll(); L.Close() }
}

func execTestAllowedBinaries() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "powershell"}
	}
	return []string{"echo", "false", "sleep", "sh", "cat"}
}

func execOutputCommand() (binary string, args []string, wantLine string) {
	if runtime.GOOS == "windows" {
		// cmd exits immediately after echo and closes stdout cleanly; PowerShell's
		// slower startup and stdout-close timing left on_exit pending on CI.
		return "cmd", []string{"/c", "echo", "hello", "world"}, "hello world"
	}
	return "echo", []string{"hello", "world"}, "hello world"
}

func execFailureCommand() (binary string, args []string) {
	if runtime.GOOS == "windows" {
		return "powershell", []string{"-NoProfile", "-Command", "exit 1"}
	}
	return "false", nil
}

func execSleepCommand(seconds int) (binary string, args []string) {
	if runtime.GOOS == "windows" {
		return "powershell", []string{"-NoProfile", "-Command", fmt.Sprintf("Start-Sleep -Seconds %d", seconds)}
	}
	return "sleep", []string{strconv.Itoa(seconds)}
}

func execDisallowedCWD() string {
	if runtime.GOOS == "windows" {
		if root := os.Getenv("WINDIR"); root != "" {
			return root
		}
		return `C:\Windows`
	}
	return "/etc"
}

func luaStringList(items []string) string {
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, fmt.Sprintf("%q", item))
	}
	return strings.Join(parts, ", ")
}

// waitExec polls for a Lua global to be set. The plugin mutex must be held
// while touching LState — exec goroutines also take it before calling into
// Lua, so reading without the lock would race on LState itself.
func waitExec(t *testing.T, p *Plugin, L *lua.LState, name string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		v := L.GetGlobal(name)
		p.mu.Unlock()
		if v != lua.LNil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q to be set", name)
}

func TestExecRunsAllowedBinary(t *testing.T) {
	L, p, _, cleanup := newExecTestState(t, []string{"exec"})
	defer cleanup()
	binary, args, wantLine := execOutputCommand()

	// Drive callbacks into globals so the test can assert state, but acquire
	// the plugin mutex first — the exec goroutines also take it before calling
	// Lua, so without locking here we'd race on LState itself.
	p.mu.Lock()
	err := L.DoString(fmt.Sprintf(`
		_G.lines = {}
		_G.exit_code = nil
		local h, err = cliamp.exec.run(%q, {%s}, {
			on_stdout = function(line) table.insert(_G.lines, line) end,
			on_exit = function(code) _G.exit_code = code end,
		})
		assert(h, tostring(err))
	`, binary, luaStringList(args)))
	p.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	waitExec(t, p, L, "exit_code", 20*time.Second)

	p.mu.Lock()
	defer p.mu.Unlock()
	if code := L.GetGlobal("exit_code").(lua.LNumber); code != 0 {
		t.Fatalf("exit_code = %v, want 0", code)
	}
	lines := L.GetGlobal("lines").(*lua.LTable)
	matched := false
	for i := 1; i <= lines.Len(); i++ {
		if strings.TrimSpace(lines.RawGetInt(i).String()) == wantLine {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("unexpected stdout: %v", lines)
	}
}

func TestExecRejectsUnallowedBinary(t *testing.T) {
	L, _, _, cleanup := newExecTestState(t, []string{"exec"})
	defer cleanup()

	err := L.DoString(`
		local h, err = cliamp.exec.run("rm", {"-rf", "/"}, {})
		_G.handle = h
		_G.err = err
	`)
	if err != nil {
		t.Fatal(err)
	}
	if L.GetGlobal("handle") != lua.LNil {
		t.Fatal("expected nil handle for unallowed binary")
	}
	if s := L.GetGlobal("err").String(); !strings.Contains(s, "allowlist") {
		t.Fatalf("err = %q, want allowlist mention", s)
	}
}

func TestExecRequiresPermission(t *testing.T) {
	L, _, _, cleanup := newExecTestState(t, nil)
	defer cleanup()

	err := L.DoString(`
		local h, err = cliamp.exec.run("echo", {"hi"}, {})
		_G.handle = h
		_G.err = err
	`)
	if err != nil {
		t.Fatal(err)
	}
	if L.GetGlobal("handle") != lua.LNil {
		t.Fatal("expected nil handle without exec permission")
	}
	if s := L.GetGlobal("err").String(); !strings.Contains(s, "permission") {
		t.Fatalf("err = %q, want permission mention", s)
	}
}

func TestExecPropagatesExitCode(t *testing.T) {
	L, p, _, cleanup := newExecTestState(t, []string{"exec"})
	defer cleanup()
	binary, args := execFailureCommand()

	p.mu.Lock()
	err := L.DoString(fmt.Sprintf(`
		_G.exit_code = nil
		cliamp.exec.run(%q, {%s}, {
			on_exit = function(code) _G.exit_code = code end,
		})
	`, binary, luaStringList(args)))
	p.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	waitExec(t, p, L, "exit_code", 20*time.Second)
	p.mu.Lock()
	defer p.mu.Unlock()
	if code := L.GetGlobal("exit_code").(lua.LNumber); code != 1 {
		t.Fatalf("exit_code = %v, want 1", code)
	}
}

func TestExecCancel(t *testing.T) {
	L, p, _, cleanup := newExecTestState(t, []string{"exec"})
	defer cleanup()
	binary, args := execSleepCommand(10)

	p.mu.Lock()
	err := L.DoString(fmt.Sprintf(`
		_G.exit_code = nil
		_G.handle = cliamp.exec.run(%q, {%s}, {
			on_exit = function(code) _G.exit_code = code end,
		})
	`, binary, luaStringList(args)))
	p.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	// Cancel from Go side to avoid re-entering Lua while exec goroutines are running.
	time.Sleep(20 * time.Millisecond)
	p.mu.Lock()
	handle := L.GetGlobal("handle").(*lua.LTable)
	cancelFn := L.GetField(handle, "cancel").(*lua.LFunction)
	_ = L.CallByParam(lua.P{Fn: cancelFn, NRet: 0, Protect: true}, handle)
	p.mu.Unlock()

	waitExec(t, p, L, "exit_code", 20*time.Second)
	p.mu.Lock()
	defer p.mu.Unlock()
	if code := L.GetGlobal("exit_code").(lua.LNumber); code >= 0 {
		t.Fatalf("expected negative exit code for cancelled process, got %v", code)
	}
}

func TestExecConcurrencyCap(t *testing.T) {
	L, p, _, cleanup := newExecTestState(t, []string{"exec"})
	defer cleanup()
	binary, args := execSleepCommand(2)

	p.mu.Lock()
	err := L.DoString(fmt.Sprintf(`
		_G.errs = {}
		_G.handles = {}
		for i = 1, 6 do
			local h, err = cliamp.exec.run(%q, {%s}, {})
			if h then
				table.insert(_G.handles, h)
			else
				table.insert(_G.errs, err or "?")
			end
		end
	`, binary, luaStringList(args)))
	p.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	p.mu.Lock()
	errs := L.GetGlobal("errs").(*lua.LTable)
	handles := L.GetGlobal("handles").(*lua.LTable)
	p.mu.Unlock()

	if handles.Len() != execMaxPerPlugin {
		t.Fatalf("handles = %d, want %d", handles.Len(), execMaxPerPlugin)
	}
	if errs.Len() != 6-execMaxPerPlugin {
		t.Fatalf("errs = %d, want %d", errs.Len(), 6-execMaxPerPlugin)
	}

	// Cancel everything to release the goroutines before the test exits.
	for i := 1; i <= handles.Len(); i++ {
		p.mu.Lock()
		h := handles.RawGetInt(i).(*lua.LTable)
		cancelFn := L.GetField(h, "cancel").(*lua.LFunction)
		_ = L.CallByParam(lua.P{Fn: cancelFn, NRet: 0, Protect: true}, h)
		p.mu.Unlock()
	}
}

func TestExecStopPluginKillsChildren(t *testing.T) {
	L, p, em, cleanup := newExecTestState(t, []string{"exec"})
	defer cleanup()
	binary, args := execSleepCommand(10)

	var exits sync.WaitGroup
	exits.Add(1)

	p.mu.Lock()
	L.SetGlobal("notify", L.NewFunction(func(*lua.LState) int {
		exits.Done()
		return 0
	}))
	err := L.DoString(fmt.Sprintf(`
		cliamp.exec.run(%q, {%s}, {
			on_exit = function() notify() end,
		})
	`, binary, luaStringList(args)))
	p.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	// Give the goroutine a moment to actually start the process.
	time.Sleep(30 * time.Millisecond)
	em.stopPlugin(p)

	done := make(chan struct{})
	go func() { exits.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stopPlugin didn't terminate child process")
	}
}

func TestResolveAllowedBinaries(t *testing.T) {
	tests := []struct {
		name string
		cfg  map[string]map[string]string
		want []string
	}{
		{"nil cfg", nil, defaultAllowedBinaries},
		{"no top-level", map[string]map[string]string{"x": {"k": "v"}}, defaultAllowedBinaries},
		{"empty allowed_binaries", map[string]map[string]string{"": {"allowed_binaries": "  "}}, defaultAllowedBinaries},
		{"extends default", map[string]map[string]string{"": {"allowed_binaries": "ffprobe, yt-dlp ,curl"}},
			[]string{"yt-dlp", "ffmpeg", "ffprobe", "curl"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveAllowedBinaries(tt.cfg)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestExecCwdMustBeAllowed(t *testing.T) {
	L, p, _, cleanup := newExecTestState(t, []string{"exec"})
	defer cleanup()
	binary, args, _ := execOutputCommand()
	cwd := execDisallowedCWD()

	p.mu.Lock()
	err := L.DoString(fmt.Sprintf(`
		local h, err = cliamp.exec.run(%q, {%s}, {cwd = %q})
		_G.handle = h
		_G.err = err
	`, binary, luaStringList(args), cwd))
	p.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if L.GetGlobal("handle") != lua.LNil {
		t.Fatal("expected nil handle for disallowed cwd")
	}
	if s := L.GetGlobal("err").String(); !strings.Contains(s, "cwd") {
		t.Fatalf("err = %q, want cwd mention", s)
	}
}

// Guard against a regression where a missing binary silently returns a handle.
func TestExecBinaryNotOnPath(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	p := &Plugin{Name: "test", L: L, perms: map[string]bool{"exec": true}}
	em := newExecManager([]string{"definitely-not-a-real-binary-xyz"})
	cliamp := L.NewTable()
	registerExecAPI(L, cliamp, em, p, newPluginLogger(""))
	L.SetGlobal("cliamp", cliamp)

	err := L.DoString(`
		local h, err = cliamp.exec.run("definitely-not-a-real-binary-xyz", {}, {})
		_G.handle = h
		_G.err = err
	`)
	if err != nil {
		t.Fatal(err)
	}
	if L.GetGlobal("handle") != lua.LNil {
		t.Fatal("expected nil handle for missing binary")
	}
	if s := L.GetGlobal("err").String(); !strings.Contains(s, "PATH") {
		t.Fatalf("err = %q, want PATH mention", s)
	}
}

func TestNewPluginCommand(t *testing.T) {
	bin := "/usr/bin/some-binary"
	if runtime.GOOS == "windows" {
		bin = `C:\bin\some-binary.exe`
	}
	ctx := context.Background()

	tests := []struct {
		name    string
		path    string
		argv    []string
		cwd     string
		wantErr string
	}{
		{
			name: "absolute path pins the binary",
			path: bin,
			argv: []string{"-flag", "value", "--flag=value"},
			cwd:  "",
		},
		{
			name:    "relative path rejected",
			path:    "bin/some-binary",
			argv:    []string{"arg"},
			cwd:     "",
			wantErr: "absolute",
		},
		{
			name:    "bare name rejected",
			path:    "some-binary",
			argv:    []string{"arg"},
			cwd:     "",
			wantErr: "absolute",
		},
		{
			name:    "empty argv entry rejected",
			path:    bin,
			argv:    []string{"a", "", "b"},
			cwd:     "",
			wantErr: "empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := newPluginCommand(ctx, tt.path, tt.argv, tt.cwd)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("newPluginCommand(%q, %v) = cmd, want error", tt.path, tt.argv)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %q, want %q mention", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("newPluginCommand(%q, %v): %v", tt.path, tt.argv, err)
			}
			if cmd.Path != tt.path {
				t.Fatalf("cmd.Path = %q, want pinned %q", cmd.Path, tt.path)
			}
			wantArgs := append([]string{tt.path}, tt.argv...)
			if !reflect.DeepEqual(cmd.Args, wantArgs) {
				t.Fatalf("cmd.Args = %v, want %v", cmd.Args, wantArgs)
			}
			if tt.cwd != "" && cmd.Dir != tt.cwd {
				t.Fatalf("cmd.Dir = %q, want %q", cmd.Dir, tt.cwd)
			}
			if len(cmd.Env) == 0 {
				t.Fatal("cmd.Env = empty, want minimal env")
			}
		})
	}
}
