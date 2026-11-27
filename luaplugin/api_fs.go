package luaplugin

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	lua "github.com/yuin/gopher-lua"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/ipc"
)

const (
	// maxFSWriteBytes caps the bytes that cliamp.fs.write and cliamp.fs.append
	// write to a single file.
	maxFSWriteBytes = 16 << 20 // 16 MiB

	// maxSymlinkHops caps manual symlink resolution hops in canonicalExistingPath.
	maxSymlinkHops = 40
)

var (
	// activeWriteRules stores the write rules captured before any plugin runs.
	activeWriteRules atomic.Pointer[writeRules]

	// cachedWriteRules provides a lazy fallback for direct unit tests that do
	// not call New.
	cachedWriteRules = sync.OnceValue(loadWriteRules)
)

// initWriteRules resolves and stores write rules before plugin code executes.
func initWriteRules() writeRules {
	r := loadWriteRules()
	activeWriteRules.Store(&r)
	return r
}

// currentWriteRules returns the active write rules for this process.
func currentWriteRules() writeRules {
	if r := activeWriteRules.Load(); r != nil {
		return *r
	}
	return cachedWriteRules()
}

// writeRules bounds the paths that plugins can write. Every entry is
// canonicalized so a symlinked directory cannot bypass prefix checks.
type writeRules struct {
	allow     []string // directories that plugins can write inside
	deny      []string // paths inside allow that plugins cannot write, with their subtrees
	configDir string   // canonical config directory for pattern-based denials
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
		if abs, ok := canonicalExistingPath(configDir); ok {
			r.allow = append(r.allow, abs)
			r.configDir = abs
		}
	}
	// The data dir holds the cliamp.store files, so it comes from the same
	// resolver as newPluginStore.
	if dataDir, err := appdir.DataDir(); err == nil {
		add(&r.allow, dataDir)
		add(&r.deny, filepath.Join(dataDir, "telegram.session"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(&r.allow, filepath.Join(home, "Music", "cliamp"))
	}

	// Deny sensitive cliamp configuration, credentials, trust, state, log,
	// lock, and IPC files inside the config directory. A plugin creating a
	// directory at cliamp.sock.lock would prevent IPC startup and cleanup.
	if pluginDir, err := appdir.PluginDir(); err == nil {
		add(&r.deny, pluginDir)
	}
	if configErr == nil {
		for _, name := range []string{
			"config.toml",
			"radios.toml",
			"favorites.toml",
			"history.toml",
			"radio_favorites.toml",
			"radio_countries.toml",
			"resume.json",
			"cliamp.log",
			"config.toml.lock",
			"favorites.toml.lock",
			"history.toml.lock",
			"radio_favorites.toml.lock",
			"radio_countries.toml.lock",
			"playlists.lock",
			pluginLogName,
		} {
			add(&r.deny, filepath.Join(configDir, name))
		}
	}
	add(&r.deny, ipc.DefaultSocketPath())
	add(&r.deny, ipc.DefaultSocketPath()+".pid")
	add(&r.deny, ipc.DefaultSocketPath()+".lock")
	return r
}

// canonicalExistingPath resolves symlinks along path, including dangling
// symlinks, and re-attaches any non-existent trailing path segments.
func canonicalExistingPath(path string) (string, bool) {
	return canonicalExistingPathDepth(path, 0)
}

func canonicalExistingPathDepth(rawPath string, depth int) (string, bool) {
	if depth > maxSymlinkHops || rawPath == "" {
		return "", false
	}
	// Make rawPath absolute without calling filepath.Clean, because lexical
	// cleaning collapses "<symlink>/.." before symlinks are resolved.
	full := rawPath
	if !filepath.IsAbs(full) {
		wd, err := os.Getwd()
		if err != nil {
			return "", false
		}
		full = wd + string(os.PathSeparator) + full
	}

	vol := filepath.VolumeName(full)
	rest := full[len(vol):]
	cur := vol + string(os.PathSeparator)

	for _, seg := range splitPathSegments(rest) {
		if seg == "" || seg == "." {
			continue
		}
		if seg == ".." {
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, seg)
		info, err := os.Lstat(next)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			target, rerr := os.Readlink(next)
			if rerr != nil {
				return "", false
			}
			if !filepath.IsAbs(target) {
				target = cur + string(os.PathSeparator) + target
			}
			resolved, ok := canonicalExistingPathDepth(target, depth+1)
			if !ok {
				return "", false
			}
			cur = resolved
			continue
		}
		if err != nil && !os.IsNotExist(err) && !errors.Is(err, syscall.ENOTDIR) {
			return "", false
		}
		cur = next
	}
	return filepath.Clean(cur), true
}

func splitPathSegments(p string) []string {
	if os.PathSeparator == '\\' {
		return strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' })
	}
	return strings.Split(p, "/")
}

// isWriteAllowed checks if a path is within one of the allowed write
// directories and outside every denied path, resolving symlinks on both
// sides first.
func isWriteAllowed(path string) bool {
	return currentWriteRules().allows(path)
}

// resolveAllowedWritePath resolves all symlinks and ".." components left to
// right and returns the canonical path when it is permitted by write rules.
func resolveAllowedWritePath(path string) (string, bool) {
	return currentWriteRules().resolveAllowed(path)
}

// resolveSafeWriteEntry resolves every parent directory component left to right
// without following a final symlink leaf, verifies the path against write rules,
// and returns the parent-canonicalized path for open/remove. Preserving a final
// symlink leaf lets O_NOFOLLOW reject writes through leaf symlinks and lets
// remove unlink the symlink itself rather than its target.
func resolveSafeWriteEntry(rawPath string) (string, bool) {
	if rawPath == "" {
		return "", false
	}
	trimmed := trimTrailingSeps(rawPath)
	vol := filepath.VolumeName(trimmed)
	rest := trimmed[len(vol):]
	lastSep := strings.LastIndexByte(rest, '/')
	if os.PathSeparator == '\\' {
		if bs := strings.LastIndexByte(rest, '\\'); bs > lastSep {
			lastSep = bs
		}
	}
	var rawParent, base string
	if lastSep < 0 {
		if vol != "" {
			return resolveAllowedWritePath(rawPath)
		}
		rawParent = "."
		base = rest
	} else {
		base = rest[lastSep+1:]
		rawParent = trimmed[:len(vol)+lastSep]
		if rawParent == vol {
			rawParent = vol + string(os.PathSeparator)
		}
	}
	if base == "" || base == "." || base == ".." {
		return resolveAllowedWritePath(rawPath)
	}
	resolvedParent, ok := canonicalExistingPath(rawParent)
	if !ok {
		return "", false
	}
	safePath := filepath.Join(resolvedParent, base)
	if !currentWriteRules().allows(safePath) {
		return "", false
	}
	return safePath, true
}

func trimTrailingSeps(p string) string {
	vol := filepath.VolumeName(p)
	rest := p[len(vol):]
	for len(rest) > 1 {
		last := rest[len(rest)-1]
		if last == '/' || (os.PathSeparator == '\\' && last == '\\') {
			rest = rest[:len(rest)-1]
			continue
		}
		break
	}
	return vol + rest
}

// allows reports whether r permits a write to path.
func (r writeRules) allows(path string) bool {
	_, ok := r.resolveAllowed(path)
	return ok
}

// resolveAllowed resolves path canonically and returns it when r permits writes.
func (r writeRules) resolveAllowed(path string) (string, bool) {
	abs, ok := canonicalExistingPath(path)
	if !ok {
		return "", false
	}
	// An NTFS stream name such as config.toml::$DATA writes to config.toml.
	if runtime.GOOS == "windows" && strings.Contains(abs[len(filepath.VolumeName(abs)):], ":") {
		return "", false
	}
	for _, dir := range r.deny {
		if isWithin(abs, dir) {
			return "", false
		}
	}
	if r.isDeniedConfigPattern(abs) {
		return "", false
	}
	if r.sameAsDenied(abs) {
		return "", false
	}
	for _, dir := range r.allow {
		if isWithin(abs, dir) {
			return abs, true
		}
	}
	return "", false
}

// isDeniedConfigPattern blocks provider credential files in the config directory.
func (r writeRules) isDeniedConfigPattern(abs string) bool {
	if r.configDir == "" || abs == r.configDir || !isWithin(abs, r.configDir) {
		return false
	}
	sep := string(os.PathSeparator)
	cfgParts := strings.Split(r.configDir, sep)
	pathParts := strings.Split(abs, sep)
	if len(pathParts) <= len(cfgParts) {
		return false
	}
	return isCredentialsFileName(pathParts[len(cfgParts)])
}

func isCredentialsFileName(name string) bool {
	const suffix = "_credentials.json"
	if len(name) < len(suffix) {
		return false
	}
	tail := name[len(name)-len(suffix):]
	if caseFolded {
		return strings.EqualFold(tail, suffix)
	}
	return tail == suffix
}

// sameAsDenied reports whether path or one of its parents is the same file
// as a denied path. It catches hard links and filesystem case folding.
func (r writeRules) sameAsDenied(path string) bool {
	var denied []os.FileInfo
	for _, dir := range r.deny {
		if info, err := os.Stat(dir); err == nil {
			denied = append(denied, info)
		}
	}
	if r.configDir != "" {
		if entries, err := os.ReadDir(r.configDir); err == nil {
			for _, e := range entries {
				if isCredentialsFileName(e.Name()) {
					if info, err := os.Stat(filepath.Join(r.configDir, e.Name())); err == nil {
						denied = append(denied, info)
					}
				}
			}
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
// strings.EqualFold.
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
// file. O_NONBLOCK prevents blocking when opening a FIFO without a writer.
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

// openRegularWrite opens path for writing without blocking on a FIFO or
// following a symlink at the final component, and verifies a regular file.
func openRegularWrite(path string, appendMode bool) (*os.File, os.FileInfo, error) {
	flags := os.O_CREATE | os.O_WRONLY | writeOpenFlags
	if appendMode {
		flags |= os.O_APPEND
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, fmt.Errorf("%s is not a regular file", path)
	}
	return f, info, nil
}

// registerFSAPI adds cliamp.fs.{write,append,read,remove,exists,mkdir,listdir,cwd,abs} to the cliamp table.
func registerFSAPI(L *lua.LState, cliamp *lua.LTable) {
	tbl := L.NewTable()

	// cliamp.fs.write(path, content)
	L.SetField(tbl, "write", L.NewFunction(func(L *lua.LState) int {
		path := L.CheckString(1)
		content := L.CheckString(2)
		target, ok := resolveSafeWriteEntry(path)
		if !ok {
			L.ArgError(1, "write not allowed to this path")
			return 0
		}
		if len(content) > maxFSWriteBytes {
			return pushErr(L, "content exceeds 16 MiB write limit")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return pushErr(L, err.Error())
		}
		f, _, err := openRegularWrite(target, false)
		if err != nil {
			return pushErr(L, err.Error())
		}
		if err := f.Truncate(0); err != nil {
			f.Close()
			return pushErr(L, err.Error())
		}
		_, writeErr := f.WriteString(content)
		closeErr := f.Close()
		if writeErr != nil {
			return pushErr(L, writeErr.Error())
		}
		if closeErr != nil {
			return pushErr(L, closeErr.Error())
		}
		L.Push(lua.LTrue)
		return 1
	}))

	// cliamp.fs.append(path, content)
	L.SetField(tbl, "append", L.NewFunction(func(L *lua.LState) int {
		path := L.CheckString(1)
		content := L.CheckString(2)
		target, ok := resolveSafeWriteEntry(path)
		if !ok {
			L.ArgError(1, "write not allowed to this path")
			return 0
		}
		if len(content) > maxFSWriteBytes {
			return pushErr(L, "content exceeds 16 MiB write limit")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return pushErr(L, err.Error())
		}
		f, info, err := openRegularWrite(target, true)
		if err != nil {
			return pushErr(L, err.Error())
		}
		if info.Size()+int64(len(content)) > maxFSWriteBytes {
			f.Close()
			return pushErr(L, "file exceeds 16 MiB write limit")
		}
		_, writeErr := f.WriteString(content)
		closeErr := f.Close()
		if writeErr != nil {
			return pushErr(L, writeErr.Error())
		}
		if closeErr != nil {
			return pushErr(L, closeErr.Error())
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
		target, ok := resolveSafeWriteEntry(path)
		if !ok {
			L.ArgError(1, "remove not allowed for this path")
			return 0
		}
		if err := os.Remove(target); err != nil {
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
		target, ok := resolveAllowedWritePath(path)
		if !ok {
			L.ArgError(1, "mkdir not allowed for this path")
			return 0
		}
		if err := os.MkdirAll(target, 0o755); err != nil {
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

	// cliamp.fs.cwd() -> string, err
	// The working directory of cliamp, which relative track paths, such as
	// those of files named on the command line, are relative to.
	L.SetField(tbl, "cwd", L.NewFunction(func(L *lua.LState) int {
		dir, err := os.Getwd()
		if err != nil {
			return pushErr(L, err.Error())
		}
		L.Push(lua.LString(dir))
		return 1
	}))

	// cliamp.fs.abs(path) -> string, err
	// The absolute form of a non-empty local path, resolved against cliamp.fs.cwd().
	L.SetField(tbl, "abs", L.NewFunction(func(L *lua.LState) int {
		path := L.CheckString(1)
		if path == "" {
			return pushErr(L, "empty path")
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return pushErr(L, err.Error())
		}
		L.Push(lua.LString(abs))
		return 1
	}))

	L.SetField(cliamp, "fs", tbl)
}
