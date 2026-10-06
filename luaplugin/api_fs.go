package luaplugin

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"

	lua "github.com/yuin/gopher-lua"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/ipc"
)

// cachedWriteRules holds the write rules for this process. The paths they
// name never change at runtime.
var cachedWriteRules = sync.OnceValue(loadWriteRules)

// writeRules bounds the paths that plugins can write. Every entry is
// canonicalized like the paths that isWriteAllowed checks, so a symlinked
// dir (e.g. /tmp -> /private/tmp on macOS) cannot bypass the prefix checks.
type writeRules struct {
	allow []string // directories that plugins can write inside
	deny  []string // paths inside allow that plugins cannot write, with their subtrees
}

// loadWriteRules builds the write rules from the current environment.
func loadWriteRules() writeRules {
	var r writeRules
	add := func(list *[]string, path string) {
		if abs, ok := canonicalExistingPath(path); ok {
			*list = append(*list, abs)
		}
	}
	add(&r.allow, "/tmp")
	add(&r.allow, os.TempDir())
	configDir, configErr := appdir.Dir()
	if configErr == nil {
		add(&r.allow, configDir)
	}
	// The data dir holds the cliamp.store files, so it comes from the same
	// resolver as newPluginStore.
	if dataDir, err := appdir.DataDir(); err == nil {
		add(&r.allow, dataDir)
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(&r.allow, filepath.Join(home, "Music", "cliamp"))
	}

	// A plugin that writes these could approve its own code in
	// plugins/.trust.json, add a binary to the exec allowlist in config.toml,
	// change the stations in radios.toml, break IPC, or erase what it logged.
	// The IPC server reads the PID file at start. A live PID in it makes the
	// next start fail with "cliamp is already running".
	if pluginDir, err := appdir.PluginDir(); err == nil {
		add(&r.deny, pluginDir)
	}
	if configErr == nil {
		add(&r.deny, filepath.Join(configDir, "config.toml"))
		add(&r.deny, filepath.Join(configDir, "radios.toml"))
		add(&r.deny, filepath.Join(configDir, pluginLogName))
	}
	add(&r.deny, ipc.DefaultSocketPath())
	add(&r.deny, ipc.DefaultSocketPath()+".pid")
	return r
}

// canonicalExistingPath resolves symlinks on the deepest existing ancestor of
// path, re-appending any non-existent tail (e.g. a file about to be created).
// This prevents a symlink planted inside an allowed dir from redirecting a
// write to a target outside it; a purely lexical check cannot catch that.
func canonicalExistingPath(path string) (string, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	suffix := ""
	cur := abs
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			if suffix != "" {
				resolved = filepath.Join(resolved, suffix)
			}
			return resolved, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs, true // nothing along the path exists yet
		}
		suffix = filepath.Join(filepath.Base(cur), suffix)
		cur = parent
	}
}

// isWriteAllowed checks if a path is within one of the allowed write
// directories and outside every denied path, resolving symlinks on both
// sides first.
func isWriteAllowed(path string) bool {
	return cachedWriteRules().allows(path)
}

// allows reports whether r permits a write to path.
func (r writeRules) allows(path string) bool {
	abs, ok := canonicalExistingPath(path)
	if !ok {
		return false
	}
	// An NTFS stream name such as config.toml::$DATA writes to config.toml.
	if runtime.GOOS == "windows" && strings.Contains(abs[len(filepath.VolumeName(abs)):], ":") {
		return false
	}
	for _, dir := range r.deny {
		if isWithin(abs, dir) {
			return false
		}
	}
	if r.sameAsDenied(abs) {
		return false
	}
	for _, dir := range r.allow {
		if isWithin(abs, dir) {
			return true
		}
	}
	return false
}

// sameAsDenied reports whether path or one of its parents is the same file
// as a denied path. It catches a name that the file system maps to a denied
// path but that isWithin does not match, such as a case folding that differs
// from strings.EqualFold. It can match only paths that exist.
func (r writeRules) sameAsDenied(path string) bool {
	var denied []os.FileInfo
	for _, dir := range r.deny {
		if info, err := os.Stat(dir); err == nil {
			denied = append(denied, info)
		}
	}
	for cur := path; ; {
		if info, err := os.Stat(cur); err == nil {
			for _, d := range denied {
				if os.SameFile(info, d) {
					return true
				}
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return false
		}
		cur = parent
	}
}

// caseFolded is true where the default file systems ignore case, so
// plugins/ and Plugins/ name the same dir. It is a var so tests can check
// the folded comparison on each OS.
var caseFolded = runtime.GOOS == "windows" || runtime.GOOS == "darwin"

// isWithin reports whether path is dir or lies under dir. Both paths must be
// canonical. When caseFolded is true, it compares each path component with
// strings.EqualFold. Lowercasing the path is not enough: strings.ToLower
// keeps the long s, U+017F, but the file system folds it to s. Thus pluginſ
// names the plugins dir.
func isWithin(path, dir string) bool {
	if !caseFolded {
		return path == dir || strings.HasPrefix(path, dir+string(os.PathSeparator))
	}
	sep := string(os.PathSeparator)
	pathParts := strings.Split(path, sep)
	dirParts := strings.Split(dir, sep)
	if len(pathParts) < len(dirParts) {
		return false
	}
	for i, part := range dirParts {
		if !strings.EqualFold(pathParts[i], part) {
			return false
		}
	}
	return true
}

// openRegular opens path for reading and fails when it is not a regular
// file. A read of a FIFO or a device can block without limit, and the read
// runs under the plugin lock, maybe in a render on the UI goroutine.
// O_NONBLOCK keeps the open of a FIFO without a writer from blocking.
func openRegular(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	return f, nil
}

// registerFSAPI adds cliamp.fs.{write,append,read,remove,exists} to the cliamp table.
func registerFSAPI(L *lua.LState, cliamp *lua.LTable) {
	tbl := L.NewTable()

	// cliamp.fs.write(path, content)
	L.SetField(tbl, "write", L.NewFunction(func(L *lua.LState) int {
		path := L.CheckString(1)
		content := L.CheckString(2)
		if !isWriteAllowed(path) {
			L.ArgError(1, "write not allowed to this path")
			return 0
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return pushErr(L, err.Error())
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return pushErr(L, err.Error())
		}
		L.Push(lua.LTrue)
		return 1
	}))

	// cliamp.fs.append(path, content)
	L.SetField(tbl, "append", L.NewFunction(func(L *lua.LState) int {
		path := L.CheckString(1)
		content := L.CheckString(2)
		if !isWriteAllowed(path) {
			L.ArgError(1, "write not allowed to this path")
			return 0
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return pushErr(L, err.Error())
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return pushErr(L, err.Error())
		}
		_, err = f.WriteString(content)
		f.Close()
		if err != nil {
			return pushErr(L, err.Error())
		}
		L.Push(lua.LTrue)
		return 1
	}))

	// cliamp.fs.read(path) -> string (max 1MB)
	L.SetField(tbl, "read", L.NewFunction(func(L *lua.LState) int {
		path := L.CheckString(1)
		f, err := openRegular(path)
		if err != nil {
			return pushErr(L, err.Error())
		}
		defer f.Close()
		const maxSize = 1 << 20 // 1MB
		// Read one byte past the cap so an oversized file is detected without
		// pulling the whole thing into memory, then reject it explicitly
		// rather than returning a silently truncated value.
		data, err := io.ReadAll(io.LimitReader(f, maxSize+1))
		if err != nil {
			return pushErr(L, err.Error())
		}
		if len(data) > maxSize {
			return pushErr(L, "file exceeds 1MB read limit")
		}
		L.Push(lua.LString(string(data)))
		return 1
	}))

	// cliamp.fs.remove(path)
	L.SetField(tbl, "remove", L.NewFunction(func(L *lua.LState) int {
		path := L.CheckString(1)
		if !isWriteAllowed(path) {
			L.ArgError(1, "remove not allowed for this path")
			return 0
		}
		if err := os.Remove(path); err != nil {
			return pushErr(L, err.Error())
		}
		L.Push(lua.LTrue)
		return 1
	}))

	// cliamp.fs.exists(path) -> boolean
	L.SetField(tbl, "exists", L.NewFunction(func(L *lua.LState) int {
		path := L.CheckString(1)
		_, err := os.Stat(path)
		L.Push(lua.LBool(err == nil))
		return 1
	}))

	// cliamp.fs.mkdir(path) — recursive; path must be in write allowlist.
	L.SetField(tbl, "mkdir", L.NewFunction(func(L *lua.LState) int {
		path := L.CheckString(1)
		if !isWriteAllowed(path) {
			L.ArgError(1, "mkdir not allowed for this path")
			return 0
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			return pushErr(L, err.Error())
		}
		L.Push(lua.LTrue)
		return 1
	}))

	// cliamp.fs.listdir(path) -> {names}, err
	// Reading is unrestricted (matches cliamp.fs.read); returns entry names only.
	L.SetField(tbl, "listdir", L.NewFunction(func(L *lua.LState) int {
		path := L.CheckString(1)
		entries, err := os.ReadDir(path)
		if err != nil {
			return pushErr(L, err.Error())
		}
		result := L.NewTable()
		for i, e := range entries {
			result.RawSetInt(i+1, lua.LString(e.Name()))
		}
		L.Push(result)
		return 1
	}))

	L.SetField(cliamp, "fs", tbl)
}
