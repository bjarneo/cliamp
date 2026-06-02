package luaplugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"

	lua "github.com/yuin/gopher-lua"

	"cliamp/internal/appdir"
)

// pluginStore is a per-plugin key/value store persisted as a single JSON file
// at ~/.local/share/cliamp/plugins/<name>/store.json. Values round-trip through
// the same Lua<->JSON conversion as cliamp.json, so tables, numbers, strings,
// and booleans all survive a restart.
//
// The store is scoped to one plugin: a plugin can never read another plugin's
// keys, which preserves the no-inter-plugin-communication invariant.
type pluginStore struct {
	mu     sync.Mutex
	path   string         // store.json path; empty if the data dir is unavailable
	data   map[string]any // in-memory state, lazily loaded on first access
	loaded bool
}

// newPluginStore resolves the store path for a plugin. A failure to resolve the
// data dir yields a store with an empty path: reads return nil and writes are
// silently dropped, so plugins degrade gracefully when there is no home dir.
func newPluginStore(pluginName string) *pluginStore {
	s := &pluginStore{data: map[string]any{}}
	if dir, err := appdir.DataDir(); err == nil {
		s.path = filepath.Join(dir, "plugins", pluginName, "store.json")
	}
	return s
}

// load reads the JSON file into memory on first access. Caller must hold s.mu.
func (s *pluginStore) load() {
	if s.loaded {
		return
	}
	s.loaded = true
	if s.path == "" {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return // missing file (first run) or unreadable — start empty
	}
	var m map[string]any
	if json.Unmarshal(data, &m) == nil && m != nil {
		s.data = m
	}
}

// save writes the in-memory state back to disk. Caller must hold s.mu.
//
// The store may hold plugin credentials (API keys, tokens), so the directory
// and file are owner-only (0o700/0o600). The write is atomic: a temp file in
// the same directory is renamed into place so a crash mid-write can never leave
// a truncated or partially written store.
func (s *pluginStore) save() error {
	if s.path == "" {
		return nil
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(s.data)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".store-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed; cleans up on any error path
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}

// registerStoreAPI adds cliamp.store.{get,set,delete,keys,clear} to the cliamp
// table, backed by a per-plugin JSON file. No permission is required: a plugin
// can only ever touch its own namespace.
func registerStoreAPI(L *lua.LState, cliamp *lua.LTable, pluginName string) {
	store := newPluginStore(pluginName)
	tbl := L.NewTable()

	// cliamp.store.get(key) -> value or nil
	L.SetField(tbl, "get", L.NewFunction(func(L *lua.LState) int {
		key := L.CheckString(1)
		store.mu.Lock()
		store.load()
		v, ok := store.data[key]
		store.mu.Unlock()
		if !ok {
			L.Push(lua.LNil)
			return 1
		}
		L.Push(jsonToLua(L, v))
		return 1
	}))

	// cliamp.store.set(key, value) -> true or (nil, error)
	L.SetField(tbl, "set", L.NewFunction(func(L *lua.LState) int {
		key := L.CheckString(1)
		val := luaToGo(L.CheckAny(2))
		store.mu.Lock()
		store.load()
		store.data[key] = val
		err := store.save()
		store.mu.Unlock()
		return pushStoreResult(L, err)
	}))

	// cliamp.store.delete(key) -> true or (nil, error)
	L.SetField(tbl, "delete", L.NewFunction(func(L *lua.LState) int {
		key := L.CheckString(1)
		store.mu.Lock()
		store.load()
		delete(store.data, key)
		err := store.save()
		store.mu.Unlock()
		return pushStoreResult(L, err)
	}))

	// cliamp.store.keys() -> array of keys (sorted for stable iteration)
	L.SetField(tbl, "keys", L.NewFunction(func(L *lua.LState) int {
		store.mu.Lock()
		store.load()
		keys := make([]string, 0, len(store.data))
		for k := range store.data {
			keys = append(keys, k)
		}
		store.mu.Unlock()
		sort.Strings(keys)
		out := L.NewTable()
		for i, k := range keys {
			out.RawSetInt(i+1, lua.LString(k))
		}
		L.Push(out)
		return 1
	}))

	// cliamp.store.clear() -> true or (nil, error)
	L.SetField(tbl, "clear", L.NewFunction(func(L *lua.LState) int {
		store.mu.Lock()
		store.data = map[string]any{}
		store.loaded = true
		err := store.save()
		store.mu.Unlock()
		return pushStoreResult(L, err)
	}))

	L.SetField(cliamp, "store", tbl)
}

// pushStoreResult pushes true on success or (nil, error) on failure, matching
// the convention used by cliamp.fs.
func pushStoreResult(L *lua.LState, err error) int {
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	L.Push(lua.LTrue)
	return 1
}
