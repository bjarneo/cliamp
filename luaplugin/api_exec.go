package luaplugin

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// Default binaries plugins may invoke via cliamp.exec.run(). Users can widen
// this via [plugins] allowed_binaries = "yt-dlp,ffmpeg,ffprobe" in config.toml.
var defaultAllowedBinaries = []string{"yt-dlp", "ffmpeg"}

// Per-process output cap (stdout+stderr). Once exceeded, further output is
// dropped and the process is still allowed to run to completion. Prevents a
// chatty subprocess from OOMing the player.
const execMaxOutputBytes = 4 << 20 // 4 MiB

// Per-plugin concurrency cap. Runaway plugins can't fork-bomb past this.
const execMaxPerPlugin = 4

// Hard timeout cap. Plugins may pass a smaller value; larger values clamp.
const execMaxTimeout = 30 * time.Minute

// execEntry tracks a single running subprocess.
type execEntry struct {
	id     int64
	plugin *Plugin
	cmd    *exec.Cmd
	cancel context.CancelFunc
	done   chan struct{}
}

// execManager owns all running plugin subprocesses.
type execManager struct {
	mu      sync.Mutex
	entries map[int64]*execEntry
	nextID  atomic.Int64
	perPlug map[*Plugin]int
	allowed map[string]struct{}
	allowMu sync.RWMutex
}

func newExecManager(allowed []string) *execManager {
	em := &execManager{
		entries: make(map[int64]*execEntry),
		perPlug: make(map[*Plugin]int),
		allowed: make(map[string]struct{}),
	}
	em.setAllowed(allowed)
	return em
}

func (em *execManager) setAllowed(names []string) {
	em.allowMu.Lock()
	defer em.allowMu.Unlock()
	em.allowed = make(map[string]struct{}, len(names))
	for _, n := range names {
		em.allowed[n] = struct{}{}
	}
}

func (em *execManager) isAllowed(name string) bool {
	em.allowMu.RLock()
	defer em.allowMu.RUnlock()
	_, ok := em.allowed[name]
	return ok
}

func (em *execManager) add(e *execEntry) {
	em.mu.Lock()
	em.entries[e.id] = e
	em.perPlug[e.plugin]++
	em.mu.Unlock()
}

func (em *execManager) remove(e *execEntry) {
	em.mu.Lock()
	if _, ok := em.entries[e.id]; ok {
		delete(em.entries, e.id)
		em.perPlug[e.plugin]--
		if em.perPlug[e.plugin] <= 0 {
			delete(em.perPlug, e.plugin)
		}
	}
	em.mu.Unlock()
}

// canStart returns true if the plugin is under its concurrency cap.
func (em *execManager) canStart(p *Plugin) bool {
	em.mu.Lock()
	defer em.mu.Unlock()
	return em.perPlug[p] < execMaxPerPlugin
}

// stopPlugin cancels every process owned by the given plugin and blocks
// until each one has exited. Called from Manager.cleanupPlugin.
func (em *execManager) stopPlugin(p *Plugin) {
	em.mu.Lock()
	var victims []*execEntry
	for _, e := range em.entries {
		if e.plugin == p {
			victims = append(victims, e)
		}
	}
	em.mu.Unlock()

	for _, e := range victims {
		e.cancel()
	}
	for _, e := range victims {
		<-e.done
	}
}

// stopAll cancels every process. Called from Manager.Close.
func (em *execManager) stopAll() {
	em.mu.Lock()
	var all []*execEntry
	for _, e := range em.entries {
		all = append(all, e)
	}
	em.mu.Unlock()

	for _, e := range all {
		e.cancel()
	}
	for _, e := range all {
		<-e.done
	}
}

// registerExecAPI adds cliamp.exec.run(binary, args, opts?) -> handle, err.
// The exec API is only functional for plugins declaring permissions = {"exec"}.
// Without the permission, cliamp.exec is a no-op table that logs once.
func registerExecAPI(L *lua.LState, cliamp *lua.LTable, em *execManager, p *Plugin, logger *pluginLogger) {
	tbl := L.NewTable()

	warned := false
	guard := func() bool {
		if p.perms[PermExec] {
			return true
		}
		if !warned {
			logger.log(p.Name, "warn", "cliamp.exec requires permissions = {\"exec\"} — further warnings suppressed")
			warned = true
		}
		return false
	}

	L.SetField(tbl, "run", L.NewFunction(func(L *lua.LState) int {
		if !guard() {
			L.Push(lua.LNil)
			L.Push(lua.LString("exec permission required"))
			return 2
		}

		binary := L.CheckString(1)
		argsTbl := L.CheckTable(2)
		optsTbl := L.OptTable(3, nil)

		if !em.isAllowed(binary) {
			L.Push(lua.LNil)
			L.Push(lua.LString("binary not in allowlist: " + binary))
			return 2
		}

		path, err := exec.LookPath(binary)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString("binary not found on PATH: " + binary))
			return 2
		}

		// Flatten argv. Every entry must be a string; reject non-strings rather
		// than coercing, so plugins can't sneak nested tables into argv.
		var argv []string
		var argErr error
		argsTbl.ForEach(func(_, v lua.LValue) {
			if argErr != nil {
				return
			}
			if v.Type() != lua.LTString {
				argErr = errors.New("args must all be strings")
				return
			}
			argv = append(argv, v.String())
		})
		if argErr != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(argErr.Error()))
			return 2
		}

		var onStdout, onStderr, onExit *lua.LFunction
		cwd := ""
		timeout := execMaxTimeout

		if optsTbl != nil {
			if fn, ok := optsTbl.RawGetString("on_stdout").(*lua.LFunction); ok {
				onStdout = fn
			}
			if fn, ok := optsTbl.RawGetString("on_stderr").(*lua.LFunction); ok {
				onStderr = fn
			}
			if fn, ok := optsTbl.RawGetString("on_exit").(*lua.LFunction); ok {
				onExit = fn
			}
			if s, ok := optsTbl.RawGetString("cwd").(lua.LString); ok {
				cwd = string(s)
			}
			if n, ok := optsTbl.RawGetString("timeout").(lua.LNumber); ok && float64(n) > 0 {
				t := time.Duration(float64(n) * float64(time.Second))
				if t < timeout {
					timeout = t
				}
			}
		}

		if cwd != "" && !isWriteAllowed(cwd) {
			L.Push(lua.LNil)
			L.Push(lua.LString("cwd not in write allowlist"))
			return 2
		}

		if !em.canStart(p) {
			L.Push(lua.LNil)
			L.Push(lua.LString("per-plugin exec concurrency cap reached"))
			return 2
		}

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		cmd := exec.CommandContext(ctx, path, argv...)
		if cwd != "" {
			cmd.Dir = cwd
		}
		// Empty env by default — plugins should not inherit secrets like
		// AWS_*, SSH_*, etc. yt-dlp and ffmpeg both run fine with a minimal env.
		cmd.Env = minimalExecEnv()

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			cancel()
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			cancel()
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		if err := cmd.Start(); err != nil {
			cancel()
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}

		id := em.nextID.Add(1)
		entry := &execEntry{
			id:     id,
			plugin: p,
			cmd:    cmd,
			cancel: cancel,
			done:   make(chan struct{}),
		}
		em.add(entry)

		// Shared output budget across stdout+stderr.
		var outUsed atomic.Int64

		pipeStream := func(r io.Reader, fn *lua.LFunction) {
			scanner := bufio.NewScanner(r)
			// Allow longer lines than default 64KiB for noisy tools like ffmpeg.
			scanner.Buffer(make([]byte, 64*1024), 1<<20)
			for scanner.Scan() {
				line := scanner.Text()
				if outUsed.Add(int64(len(line)+1)) > execMaxOutputBytes {
					// Budget exhausted — drain silently.
					for scanner.Scan() {
					}
					return
				}
				if fn == nil {
					continue
				}
				p.mu.Lock()
				_ = p.L.CallByParam(lua.P{
					Fn:      fn,
					NRet:    0,
					Protect: true,
				}, lua.LString(line))
				p.mu.Unlock()
			}
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); pipeStream(stdout, onStdout) }()
		go func() { defer wg.Done(); pipeStream(stderr, onStderr) }()

		go func() {
			wg.Wait()
			waitErr := cmd.Wait()
			ctxErr := ctx.Err()
			cancel()
			em.remove(entry)

			code := 0
			if waitErr != nil {
				if ctxErr != nil {
					code = -1 // cancelled or timed out
				} else {
					var exitErr *exec.ExitError
					if errors.As(waitErr, &exitErr) {
						code = exitErr.ExitCode()
					} else {
						code = -2 // other error
					}
				}
			}

			if onExit != nil {
				p.mu.Lock()
				_ = p.L.CallByParam(lua.P{
					Fn:      onExit,
					NRet:    0,
					Protect: true,
				}, lua.LNumber(code))
				p.mu.Unlock()
			}
			close(entry.done)
		}()

		// Build a Lua handle with :cancel() and :alive().
		handle := L.NewTable()
		L.SetField(handle, "cancel", L.NewFunction(func(L *lua.LState) int {
			entry.cancel()
			return 0
		}))
		L.SetField(handle, "alive", L.NewFunction(func(L *lua.LState) int {
			select {
			case <-entry.done:
				L.Push(lua.LFalse)
			default:
				L.Push(lua.LTrue)
			}
			return 1
		}))
		L.SetField(handle, "id", lua.LNumber(id))

		L.Push(handle)
		return 1
	}))

	L.SetField(cliamp, "exec", tbl)
}

// homeEnv returns the user's home directory for subprocess HOME, preferring
// $HOME, then os.UserHomeDir(), falling back to os.TempDir() when unset.
// yt-dlp and ffmpeg both read HOME (~/.cache, ~/.config).
func homeEnv() string {
	if home, ok := os.LookupEnv("HOME"); ok && home != "" {
		return home
	}
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return os.TempDir()
}
