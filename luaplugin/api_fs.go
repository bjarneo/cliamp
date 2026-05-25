package luaplugin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	lua "github.com/yuin/gopher-lua"

	"cliamp/internal/appdir"
)

// writeAllowDirs returns the directories where plugins can write files.
func writeAllowDirs() []string {
	dirs := []string{os.TempDir()}
	if configDir, err := appdir.Dir(); err == nil {
		dirs = append(dirs, configDir)
	}
	home := ""
	if v, ok := os.LookupEnv("HOME"); ok && v != "" {
		home = v
	} else if v, err := os.UserHomeDir(); err == nil {
		home = v
	}
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".local", "share", "cliamp"),
			filepath.Join(home, "Music", "cliamp"),
		)
	}
	return dirs
}

// isWriteAllowed checks if a path is within one of the allowed write directories.
func isWriteAllowed(path string) bool {
	abs, err := normalizeWritePath(path)
	if err != nil {
		return false
	}
	for _, dir := range writeAllowDirs() {
		allowed, err := normalizeWritePath(dir)
		if err != nil {
			continue
		}
		if abs == allowed || strings.HasPrefix(abs, allowed+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func normalizeWritePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	return abs, nil
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
		data, err := os.ReadFile(path)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		const maxSize = 1 << 20 // 1MB
		data = data[:min(len(data), maxSize)]
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
