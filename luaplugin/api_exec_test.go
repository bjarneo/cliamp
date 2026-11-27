package luaplugin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
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

	m := newManager(execTestAllowedBinaries(), nil)
	m.logger = newPluginLogger("")
	cliamp := L.NewTable()
	m.registerExecAPI(L, cliamp, p)
	L.SetGlobal("cliamp", cliamp)

	return L, p, m.execs, func() { m.execs.stopAll(); L.Close() }
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

// A line longer than the scanner buffer stops the scan. The pipe must still
// drain, or the process blocks on a full pipe until its timeout.
func TestExecDrainsOverlongLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	tests := []struct {
		name   string
		script string
	}{
		{"stdout", `printf "%2097152s" x`},
		{"stderr", `printf "%2097152s" x >&2`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			L, p, _, cleanup := newExecTestState(t, []string{"exec"})
			defer cleanup()

			p.mu.Lock()
			err := L.DoString(fmt.Sprintf(`
				_G.exit_code = nil
				cliamp.exec.run("sh", {"-c", %q}, {
					on_stdout = function() end,
					on_stderr = function() end,
					on_exit = function(code) _G.exit_code = code end,
					timeout = 10,
				})
			`, tt.script))
			p.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}

			// The process timeout is 10 s, so an exit before 8 s shows that
			// the pipe drained.
			waitExec(t, p, L, "exit_code", 8*time.Second)
			p.mu.Lock()
			defer p.mu.Unlock()
			if code := L.GetGlobal("exit_code").(lua.LNumber); code != 0 {
				t.Fatalf("exit_code = %v, want 0", code)
			}
		})
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

// A background child of the binary inherits its output pipes. stopAll must
// not wait for that child. Before, the readers waited for the child to close
// the pipes, and stopAll and Close waited for the readers.
func TestExecStopAllWithBackgroundChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	tests := []struct {
		name   string
		script string // starts a child that writes its PID to $1 and runs for 5 s
		need   string // a binary that the script needs
		killed bool   // the group kill stops the child
	}{
		{"child in the process group", `sh -c 'echo $$ > "$1"; exec sleep 5' sh "$1" &`, "", true},
		{"child in its own session", `setsid sh -c 'echo $$ > "$1"; exec sleep 5' sh "$1" &`, "setsid", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.need != "" {
				if _, err := exec.LookPath(tt.need); err != nil {
					t.Skipf("%s is not available", tt.need)
				}
			}
			pidFile := filepath.Join(t.TempDir(), "pid")
			L, p, em, cleanup := newExecTestState(t, []string{"exec"})
			defer cleanup()

			p.mu.Lock()
			err := L.DoString(fmt.Sprintf(`
				_G.started = nil
				local h, err = cliamp.exec.run("sh", {"-c", %q, "sh", %q}, {
					on_stdout = function(line) _G.started = line end,
				})
				assert(h, tostring(err))
			`, tt.script+" echo started", pidFile))
			p.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			waitExec(t, p, L, "started", 5*time.Second)
			var pid int
			for deadline := time.Now().Add(5 * time.Second); pid == 0; {
				data, _ := os.ReadFile(pidFile)
				pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
				if pid == 0 && time.Now().After(deadline) {
					t.Fatal("the child did not write its PID")
				}
				time.Sleep(5 * time.Millisecond)
			}
			child, err := os.FindProcess(pid)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { child.Kill() })

			start := time.Now()
			em.stopAll()
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Errorf("stopAll() took %v, want less than 1s", elapsed)
			}
			if !tt.killed {
				return
			}
			for deadline := time.Now().Add(2 * time.Second); child.Signal(syscall.Signal(0)) == nil; {
				if time.Now().After(deadline) {
					t.Fatal("the child still runs after stopAll")
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
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
	m := newManager([]string{"definitely-not-a-real-binary-xyz"}, nil)
	m.logger = newPluginLogger("")
	cliamp := L.NewTable()
	m.registerExecAPI(L, cliamp, p)
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

func TestExecBlocksDangerousYTDLPFlags(t *testing.T) {
	tests := []struct {
		name    string
		binary  string
		args    []string
		blocked string
	}{
		{"--exec", "yt-dlp", []string{"--exec", "id", "https://example.com"}, "--exec"},
		{"--exec=", "yt-dlp", []string{"--exec=id", "https://example.com"}, "--exec"},
		{"--exec-before-download", "yt-dlp", []string{"--exec-before-download", "id"}, "--exec-before-download"},
		{"--exec-before-download=", "yt-dlp", []string{"--exec-before-download=id"}, "--exec-before-download"},
		{"--config-locations", "yt-dlp", []string{"--config-locations", "/tmp/cfg"}, "--config-locations"},
		{"--config-locations=", "yt-dlp", []string{"--config-locations=/tmp/cfg"}, "--config-locations"},
		{"--config-location", "yt-dlp", []string{"--config-location", "/tmp/cfg"}, "--config-location"},
		{"--config-location=", "yt-dlp", []string{"--config-location=/tmp/cfg"}, "--config-location"},
		{"--plugin-dirs", "yt-dlp", []string{"--plugin-dirs", "/tmp/plugs"}, "--plugin-dirs"},
		{"--plugin-dirs=", "yt-dlp", []string{"--plugin-dirs=/tmp/plugs"}, "--plugin-dirs"},
		{"flag after url", "yt-dlp", []string{"https://example.com", "--exec=id"}, "--exec"},
		{"allowed flag passes flag check", "yt-dlp", []string{"--version"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			L := newSandboxedState()
			defer L.Close()
			p := &Plugin{Name: "test", L: L, perms: map[string]bool{PermExec: true}}
			m := newManager([]string{"yt-dlp"}, nil)
			m.logger = newPluginLogger("")
			cliamp := L.NewTable()
			m.registerExecAPI(L, cliamp, p)
			L.SetGlobal("cliamp", cliamp)

			// Clear PATH so allowed commands stop at LookPath instead of launching yt-dlp.
			t.Setenv("PATH", t.TempDir())
			if err := L.DoString(fmt.Sprintf(`
				_G.h, _G.err = cliamp.exec.run(%q, {%s}, {})
			`, tt.binary, luaStringList(tt.args))); err != nil {
				t.Fatal(err)
			}
			if L.GetGlobal("h") != lua.LNil {
				t.Fatal("expected nil handle")
			}
			gotErr := L.GetGlobal("err").String()
			if tt.blocked != "" {
				if !strings.Contains(gotErr, "flag not allowed") || !strings.Contains(gotErr, tt.blocked) {
					t.Errorf("err = %q, want flag not allowed for %q", gotErr, tt.blocked)
				}
			} else if strings.Contains(gotErr, "flag not allowed") {
				t.Errorf("allowed flag was blocked: %q", gotErr)
			}
		})
	}
}

func TestExecArgsIteratedSequentially(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses echo")
	}
	L, p, _, cleanup := newExecTestState(t, []string{"exec"})
	defer cleanup()

	p.mu.Lock()
	err := L.DoString(`
		_G.out = nil
		_G.done = nil
		local args = {"one", "two", "three", extra = "ignored"}
		cliamp.exec.run("echo", args, {
			on_stdout = function(line) _G.out = line end,
			on_exit = function(code) _G.done = code end,
		})
	`)
	p.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	waitExec(t, p, L, "done", 10*time.Second)

	p.mu.Lock()
	defer p.mu.Unlock()
	if got := strings.TrimSpace(L.GetGlobal("out").String()); got != "one two three" {
		t.Fatalf("stdout = %q, want %q", got, "one two three")
	}
}

func TestExecPipeStreamStopsCallbacksAfterCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	L, p, em, cleanup := newExecTestState(t, []string{"exec"})
	defer cleanup()

	p.mu.Lock()
	err := L.DoString(`
		_G.lines = 0
		_G.done = nil
		_G.h = cliamp.exec.run("sh", {"-c", "i=0; while [ $i -lt 5000 ]; do echo line-$i; i=$((i+1)); done"}, {
			on_stdout = function()
				_G.lines = _G.lines + 1
				if _G.lines == 1 then
					_G.h:cancel()
				end
			end,
			on_exit = function(code) _G.done = code end,
		})
	`)
	p.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	waitExec(t, p, L, "done", 10*time.Second)
	em.stopAll()

	p.mu.Lock()
	defer p.mu.Unlock()
	lines := int(L.GetGlobal("lines").(lua.LNumber))
	if lines >= 5000 {
		t.Fatalf("on_stdout ran %d times after cancel, expected early stop", lines)
	}
}

func TestExecFFmpegOutputPaths(t *testing.T) {
	prevRules := activeWriteRules.Load()
	t.Cleanup(func() { activeWriteRules.Store(prevRules) })

	cfgDir := filepath.Join(t.TempDir(), "cliamp-cfg")
	if err := os.MkdirAll(filepath.Join(cfgDir, "plugins"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLIAMP_CONFIG_DIR", cfgDir)
	initWriteRules()

	tmpOut := filepath.Join(os.TempDir(), "out.mp3")
	if runtime.GOOS != "windows" {
		tmpOut = "/tmp/out.mp3"
	}
	disallowed := execDisallowedCWD()
	disallowedFile := filepath.Join(disallowed, "evil")
	if runtime.GOOS != "windows" {
		disallowedFile = "/etc/evil"
	}

	tests := []struct {
		name      string
		args      []string
		cwd       string
		wantAllow bool
		wantSub   string
	}{
		{
			name:      "stdout dash output",
			args:      []string{"-i", "in.mp3", "-f", "s16le", "-"},
			wantAllow: true,
		},
		{
			name:      "stdout pipe output",
			args:      []string{"-i", "in.mp3", "-f", "s16le", "pipe:1"},
			wantAllow: true,
		},
		{
			name:      "dev null output",
			args:      []string{"-i", "in.mp3", "-f", "null", "/dev/null"},
			wantAllow: true,
		},
		{
			name:      "NUL output",
			args:      []string{"-i", "in.mp3", "-f", "null", "NUL"},
			wantAllow: true,
		},
		{
			name:      "temp file output",
			args:      []string{"-i", "in.mp3", tmpOut},
			wantAllow: true,
		},
		{
			name:      "file protocol temp output",
			args:      []string{"-i", "in.mp3", "file:" + tmpOut},
			wantAllow: true,
		},
		{
			name:      "transcode flags and temp output",
			args:      []string{"-y", "-hide_banner", "-loglevel", "error", "-i", disallowedFile, "-vn", "-ar", "44100", "-ac", "2", "-c:a", "libmp3lame", "-b:a", "192k", tmpOut},
			wantAllow: true,
		},
		{
			name:      "allowed progress and log flags",
			args:      []string{"-i", "in.mp3", "-progress", "-", "-passlogfile", filepath.Join(os.TempDir(), "pass"), "-vstats_file", filepath.Join(os.TempDir(), "vstats.log"), "-sdp_file", filepath.Join(os.TempDir(), "out.sdp"), "-af", "equalizer=f=1000:width_type=o:width=2:g=3", tmpOut},
			wantAllow: true,
		},
		{
			name:      "relative output inside allowed cwd",
			args:      []string{"-i", "in.mp3", "out.mp3"},
			cwd:       os.TempDir(),
			wantAllow: true,
		},
		{
			name:      "version query without output",
			args:      []string{"-version"},
			wantAllow: true,
		},
		{
			name:    "blocks system path output",
			args:    []string{"-i", "in.mp3", disallowedFile},
			wantSub: "output path not allowed for ffmpeg",
		},
		{
			name:    "blocks output after boolean flag",
			args:    []string{"-i", "in.mp3", "-y", disallowedFile},
			wantSub: "output path not allowed for ffmpeg",
		},
		{
			name:    "blocks output after unknown flag",
			args:    []string{"-i", "in.mp3", "-unknown_flag", disallowedFile},
			wantSub: "output path not allowed for ffmpeg",
		},
		{
			name:    "blocks second disallowed output",
			args:    []string{"-i", "in.mp3", tmpOut, disallowedFile},
			wantSub: "output path not allowed for ffmpeg",
		},
		{
			name:    "blocks config.toml output",
			args:    []string{"-i", "in.mp3", filepath.Join(cfgDir, "config.toml")},
			wantSub: "output path not allowed for ffmpeg",
		},
		{
			name:    "blocks trust manifest output",
			args:    []string{"-i", "in.mp3", filepath.Join(cfgDir, "plugins", ".trust.json")},
			wantSub: "output path not allowed for ffmpeg",
		},
		{
			name:    "blocks tilde config.toml output",
			args:    []string{"-i", "in.mp3", "~/.config/cliamp/config.toml"},
			wantSub: "output path not allowed for ffmpeg",
		},
		{
			name:    "blocks file protocol output",
			args:    []string{"-i", "in.mp3", "file:" + disallowedFile},
			wantSub: "output path not allowed for ffmpeg",
		},
		{
			name:    "blocks file triple slash protocol output",
			args:    []string{"-i", "in.mp3", "file://" + disallowedFile},
			wantSub: "output path not allowed for ffmpeg",
		},
		{
			name:    "blocks uppercase FILE protocol output",
			args:    []string{"-i", "in.mp3", "FILE:" + disallowedFile},
			wantSub: "output path not allowed for ffmpeg",
		},
		{
			name:    "blocks nested protocol wrapper",
			args:    []string{"-i", "in.mp3", "async:file:" + disallowedFile},
			wantSub: "output path not allowed for ffmpeg",
		},
		{
			name:    "blocks subfile protocol options",
			args:    []string{"-i", "in.mp3", "subfile,,start,0,,:file:" + disallowedFile},
			wantSub: "output path not allowed for ffmpeg",
		},
		{
			name:    "blocks unix socket protocol",
			args:    []string{"-i", "in.mp3", "unix:/tmp/cliamp.sock"},
			wantSub: "output path not allowed for ffmpeg",
		},
		{
			name:    "blocks arbitrary pipe descriptor",
			args:    []string{"-i", "in.mp3", "pipe:3"},
			wantSub: "output path not allowed for ffmpeg",
		},
		{
			name:    "blocks tee muxer",
			args:    []string{"-i", "in.mp3", "-f", "tee", tmpOut},
			wantSub: "output format not allowed for ffmpeg: tee",
		},
		{
			name:    "blocks -progress outside allowlist",
			args:    []string{"-progress", disallowedFile, "-i", "in.mp3", "-f", "null", "-"},
			wantSub: "output path not allowed for ffmpeg flag -progress",
		},
		{
			name:    "blocks -progress= outside allowlist",
			args:    []string{"-progress=" + disallowedFile, "-i", "in.mp3", "-f", "null", "-"},
			wantSub: "output path not allowed for ffmpeg flag -progress",
		},
		{
			name:    "blocks -vstats_file outside allowlist",
			args:    []string{"-i", "in.mp3", "-vstats_file", disallowedFile, "-f", "null", "-"},
			wantSub: "output path not allowed for ffmpeg flag -vstats_file",
		},
		{
			name:    "blocks -sdp_file outside allowlist",
			args:    []string{"-i", "in.mp3", "-sdp_file", disallowedFile, "-f", "null", "-"},
			wantSub: "output path not allowed for ffmpeg flag -sdp_file",
		},
		{
			name:    "blocks -passlogfile outside allowlist",
			args:    []string{"-i", "in.mp3", "-passlogfile", disallowedFile, "-f", "null", "-"},
			wantSub: "output path not allowed for ffmpeg flag -passlogfile",
		},
		{
			name:    "blocks -passlogfile:v outside allowlist",
			args:    []string{"-i", "in.mp3", "-passlogfile:v", disallowedFile, "-f", "null", "-"},
			wantSub: "output path not allowed for ffmpeg flag -passlogfile:v",
		},
		{
			name:    "blocks -filter_complex_script outside allowlist",
			args:    []string{"-i", "in.mp3", "-filter_complex_script", disallowedFile, "-f", "null", "-"},
			wantSub: "flag not allowed for ffmpeg: -filter_complex_script",
		},
		{
			name:    "blocks -filter_complex_script even inside allowlist",
			args:    []string{"-i", "in.mp3", "-filter_complex_script", filepath.Join(os.TempDir(), "graph.txt"), "-f", "null", "-"},
			wantSub: "flag not allowed for ffmpeg: -filter_complex_script",
		},
		{
			name:    "blocks -filter_script even inside allowlist",
			args:    []string{"-i", "in.mp3", "-filter_script", filepath.Join(os.TempDir(), "graph.txt"), "-f", "null", "-"},
			wantSub: "flag not allowed for ffmpeg: -filter_script",
		},
		{
			name:    "blocks -/option file loader outside allowlist",
			args:    []string{"-i", "in.mp3", "-/filter_complex", disallowedFile, "-f", "null", "-"},
			wantSub: "flag not allowed for ffmpeg: -/filter_complex",
		},
		{
			name:    "blocks -/progress file loader even inside allowlist",
			args:    []string{"-i", "in.mp3", "-/progress", filepath.Join(os.TempDir(), "arg.txt"), "-f", "null", "-"},
			wantSub: "flag not allowed for ffmpeg: -/progress",
		},
		{
			name:    "blocks ametadata file option in -af",
			args:    []string{"-i", "in.mp3", "-af", "ametadata=mode=print:file=" + disallowedFile, "-f", "null", "-"},
			wantSub: "file option not allowed in ffmpeg filter flag: -af",
		},
		{
			name:    "blocks ametadata positional file in -af",
			args:    []string{"-i", "in.mp3", "-af", "ametadata=print:::::" + disallowedFile, "-f", "null", "-"},
			wantSub: "file option not allowed in ffmpeg filter flag: -af",
		},
		{
			name:    "blocks ssim stats_file in -vf",
			args:    []string{"-i", "in.mp3", "-vf", "ssim=stats_file=" + disallowedFile, "-f", "null", "-"},
			wantSub: "file option not allowed in ffmpeg filter flag: -vf",
		},
		{
			name:    "blocks relative config.toml when cwd is config dir",
			args:    []string{"-i", "in.mp3", "config.toml"},
			cwd:     cfgDir,
			wantSub: "output path not allowed for ffmpeg",
		},
		{
			name:    "blocks relative trust file when cwd is config dir",
			args:    []string{"-i", "in.mp3", filepath.Join("plugins", ".trust.json")},
			cwd:     cfgDir,
			wantSub: "output path not allowed for ffmpeg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			L := newSandboxedState()
			defer L.Close()
			p := &Plugin{Name: "test", L: L, perms: map[string]bool{PermExec: true}}
			m := newManager([]string{"ffmpeg"}, nil)
			m.logger = newPluginLogger("")
			cliamp := L.NewTable()
			m.registerExecAPI(L, cliamp, p)
			L.SetGlobal("cliamp", cliamp)

			// Clear PATH so allowed invocations stop at LookPath instead of launching ffmpeg.
			t.Setenv("PATH", t.TempDir())
			opts := "{}"
			if tt.cwd != "" {
				opts = fmt.Sprintf("{cwd = %q}", tt.cwd)
			}
			if err := L.DoString(fmt.Sprintf(`
				_G.h, _G.err = cliamp.exec.run("ffmpeg", {%s}, %s)
			`, luaStringList(tt.args), opts)); err != nil {
				t.Fatal(err)
			}
			if L.GetGlobal("h") != lua.LNil {
				t.Fatal("expected nil handle")
			}
			gotErr := L.GetGlobal("err").String()
			if tt.wantAllow {
				if !strings.Contains(gotErr, "PATH") {
					t.Fatalf("allowed ffmpeg args were blocked: %q", gotErr)
				}
				return
			}
			if !strings.Contains(gotErr, tt.wantSub) {
				t.Fatalf("err = %q, want substring %q", gotErr, tt.wantSub)
			}
		})
	}
}

func TestValidateFFmpegArgsTildeAndCWD(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfg := filepath.Join(home, ".config", "cliamp")
	music := filepath.Join(home, "Music", "cliamp")
	t.Setenv("CLIAMP_CONFIG_DIR", cfg)

	rules := loadWriteRules()
	// Remove temp roots so fakeHome behaves like a real home directory outside /tmp.
	var tempRoots []string
	for _, tmp := range []string{"/tmp", os.TempDir()} {
		if canon, ok := canonicalExistingPath(tmp); ok {
			tempRoots = append(tempRoots, canon)
		}
	}
	filtered := make([]string, 0, len(rules.allow))
	for _, dir := range rules.allow {
		isTemp := false
		for _, tr := range tempRoots {
			if dir == tr {
				isTemp = true
				break
			}
		}
		if !isTemp {
			filtered = append(filtered, dir)
		}
	}
	rules.allow = filtered

	tests := []struct {
		name      string
		args      []string
		cwd       string
		wantAllow bool
	}{
		{"tilde bashrc blocked", []string{"-i", "in.mp3", "~/.bashrc"}, "", false},
		{"tilde config.toml blocked", []string{"-i", "in.mp3", "~/.config/cliamp/config.toml"}, "", false},
		{"tilde trust.json blocked", []string{"-i", "in.mp3", "~/.config/cliamp/plugins/.trust.json"}, "", false},
		{"file protocol tilde bashrc blocked", []string{"-i", "in.mp3", "file:~/.bashrc"}, "", false},
		{"other user tilde blocked", []string{"-i", "in.mp3", "~root/.bashrc"}, "", false},
		{"tilde music dir allowed", []string{"-i", "in.mp3", "~/Music/cliamp/out.mp3"}, "", true},
		{"report blocked outside writable cwd", []string{"-report", "-i", "in.mp3", "-f", "null", "-"}, home, false},
		{"vstats blocked outside writable cwd", []string{"-vstats", "-i", "in.mp3", "-f", "null", "-"}, home, false},
		{"report allowed inside writable cwd", []string{"-report", "-i", "in.mp3", "-f", "null", "-"}, music, true},
		{"vstats allowed inside writable cwd", []string{"-vstats", "-i", "in.mp3", "-f", "null", "-"}, music, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason := validateFFmpegArgsWith(tt.args, tt.cwd, rules.allows)
			if tt.wantAllow && reason != "" {
				t.Fatalf("validateFFmpegArgsWith(%v) rejected: %s", tt.args, reason)
			}
			if !tt.wantAllow && reason == "" {
				t.Fatalf("validateFFmpegArgsWith(%v) allowed, want rejection", tt.args)
			}
		})
	}
}
