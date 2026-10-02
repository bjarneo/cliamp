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

// execPipeGrace is the time that the output pipes stay open after a cancel or
// a timeout. A process that left the process group of the binary can hold
// them open. Then the exec manager closes them, so the readers end and
// stopAll does not wait for that process.
const execPipeGrace = 500 * time.Millisecond

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
// The output and exit callbacks go through m.call.
func (m *Manager) registerExecAPI(L *lua.LState, cliamp *lua.LTable, p *Plugin) {
	em := m.execs
	tbl := L.NewTable()

	L.SetField(tbl, "run", L.NewFunction(func(L *lua.LState) int {
		if !p.permitted(PermExec, "cliamp.exec.run") {
			return pushErr(L, "exec permission required")
		}

		binary := L.CheckString(1)
		argsTbl := L.CheckTable(2)
		optsTbl := L.OptTable(3, nil)

		if !em.isAllowed(binary) {
			return pushErr(L, "binary not in allowlist: "+binary)
		}

		path, err := exec.LookPath(binary)
		if err != nil {
			return pushErr(L, "binary not found on PATH: "+binary)
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
			return pushErr(L, argErr.Error())
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
			return pushErr(L, "cwd not in write allowlist")
		}

		if !em.canStart(p) {
			return pushErr(L, "per-plugin exec concurrency cap reached")
		}

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		cmd := exec.CommandContext(ctx, path, argv...)
		if cwd != "" {
			cmd.Dir = cwd
		}
		// A minimal env gives each binary the same known environment, so
		// variables of the user's shell, such as LD_PRELOAD, do not change
		// how it runs. It does not hide secrets from the plugin, which can
		// read any variable with os.getenv and pass it in argv. yt-dlp and
		// ffmpeg both run fine with a minimal env.
		cmd.Env = minimalExecEnv()
		killProcessGroup(cmd)

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			cancel()
			return pushErr(L, err.Error())
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			cancel()
			return pushErr(L, err.Error())
		}
		if err := cmd.Start(); err != nil {
			cancel()
			return pushErr(L, err.Error())
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

		pipeStream := func(r io.Reader, fn *lua.LFunction, label string) {
			// Drain what the scan leaves: the rest after the output budget
			// ends, or after a line longer than the buffer stops the scan.
			// A pipe that is not drained blocks the process until its
			// timeout.
			defer io.Copy(io.Discard, r)
			scanner := bufio.NewScanner(r)
			// Allow longer lines than default 64KiB for noisy tools like ffmpeg.
			scanner.Buffer(make([]byte, 64*1024), 1<<20)
			for scanner.Scan() {
				line := scanner.Text()
				if outUsed.Add(int64(len(line)+1)) > execMaxOutputBytes {
					return // The budget is used up. Drop the rest silently.
				}
				if fn == nil {
					continue
				}
				m.call(p, label, hookTimeout, 0, fixedArgs(fn, lua.LString(line)))
			}
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); pipeStream(stdout, onStdout, "exec on_stdout") }()
		go func() { defer wg.Done(); pipeStream(stderr, onStderr, "exec on_stderr") }()

		readersDone := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
			case <-readersDone:
				return
			}
			select {
			case <-time.After(execPipeGrace):
				stdout.Close()
				stderr.Close()
			case <-readersDone:
			}
		}()

		go func() {
			wg.Wait()
			close(readersDone)
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
				m.call(p, "exec on_exit", hookTimeout, 0, fixedArgs(onExit, lua.LNumber(code)))
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
