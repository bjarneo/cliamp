package luaplugin

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	lua "github.com/yuin/gopher-lua"

	"cliamp/internal/appdir"
)

var (
	allowDirsOnce sync.Once
	allowDirs     []string
)

// writeAllowDirs returns the directories where plugins can write files, with
// symlinks resolved so the prefix check in isWriteAllowed cannot be bypassed
// by a symlinked allow dir (e.g. /tmp -> /private/tmp on macOS). Entries are
// normalized via normalizeWritePath so isWriteAllowed can compare directly.
// The result is cached since these paths never change at runtime.
func writeAllowDirs() []string {
	allowDirsOnce.Do(func() {
		raw := []string{"/tmp", os.TempDir()}
		if configDir, err := appdir.Dir(); err == nil {
			raw = append(raw, configDir)
		}
		if home, err := os.UserHomeDir(); err == nil {
			raw = append(raw, filepath.Join(home, ".local", "share", "cliamp"))
			raw = append(raw, filepath.Join(home, "Music", "cliamp"))
		}
		for _, d := range raw {
			abs, err := filepath.Abs(d)
			if err != nil {
				continue
			}
			if resolved, err := filepath.EvalSymlinks(abs); err == nil {
				abs = resolved
			}
			allowDirs = append(allowDirs, normalizeWritePath(abs))
		}
	})
	return allowDirs
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
// directories, resolving symlinks on both sides first.
func isWriteAllowed(path string) bool {
	abs, ok := canonicalExistingPath(path)
	if !ok {
		return false
	}
	abs = normalizeWritePath(abs)
	for _, dir := range writeAllowDirs() {
		if abs == dir || strings.HasPrefix(abs, dir+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// normalizeWritePath canonicalizes an absolute path for prefix comparison:
// cleaned and, on Windows, case-folded (Windows paths are case-insensitive).
func normalizeWritePath(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
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
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
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
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		_, err = f.WriteString(content)
		f.Close()
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LTrue)
		return 1
	}))

	// cliamp.fs.read(path) -> string (max 1MB)
	L.SetField(tbl, "read", L.NewFunction(func(L *lua.LState) int {
		path := L.CheckString(1)
		f, err := os.Open(path)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		defer f.Close()
		const maxSize = 1 << 20 // 1MB
		// Read one byte past the cap so an oversized file is detected without
		// pulling the whole thing into memory, then reject it explicitly
		// rather than returning a silently truncated value.
		data, err := io.ReadAll(io.LimitReader(f, maxSize+1))
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		if len(data) > maxSize {
			L.Push(lua.LNil)
			L.Push(lua.LString("file exceeds 1MB read limit"))
			return 2
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
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
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
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
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
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
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
