package luaplugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	lua "github.com/yuin/gopher-lua"

	"github.com/bjarneo/cliamp/internal/appdir"
)

// maxStoreBytes caps the serialized JSON size of one plugin store.
const maxStoreBytes = 1 << 20 // 1 MiB

// pluginStore is a per-plugin key/value store persisted as a single JSON file
// at ~/.local/share/cliamp/plugins/<name>/store.json. Values round-trip through
// the same Lua<->JSON conversion as cliamp.json, so tables, numbers, strings,
// and booleans all survive a restart.
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
		return // missing file or unreadable: start empty
	}
	var m map[string]any
	if json.Unmarshal(data, &m) == nil && m != nil {
		s.data = m
	}
}

// save marshals the store once, enforces maxStoreBytes, and writes the file
// atomically. Caller must hold s.mu.
func (s *pluginStore) save() error {
	data, err := json.Marshal(s.data)
	if err != nil {
		return err
	}
	if len(data) > maxStoreBytes {
		return fmt.Errorf("store exceeds %d byte limit", maxStoreBytes)
	}
	if s.path == "" {
		return nil
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
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
// table, backed by a per-plugin JSON file.
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
		L.Push(toLua(L, v))
		return 1
	}))

	// cliamp.store.set(key, value) -> true or (nil, error)
	L.SetField(tbl, "set", L.NewFunction(func(L *lua.LState) int {
		key := L.CheckString(1)
		val, err := luaToGoCtx(callContext(L), L.CheckAny(2))
		if err != nil {
			return pushErr(L, err.Error())
		}
		store.mu.Lock()
		store.load()
		prev, hadPrev := store.data[key]
		store.data[key] = val
		err = store.save()
		if err != nil {
			// Restore the previous value so memory matches disk.
			if hadPrev {
				store.data[key] = prev
			} else {
				delete(store.data, key)
			}
		}
		store.mu.Unlock()
		return pushResult(L, err)
	}))

	// cliamp.store.delete(key) -> true or (nil, error)
	L.SetField(tbl, "delete", L.NewFunction(func(L *lua.LState) int {
		key := L.CheckString(1)
		store.mu.Lock()
		store.load()
		prev, hadPrev := store.data[key]
		if hadPrev {
			delete(store.data, key)
		}
		err := store.save()
		if err != nil && hadPrev {
			store.data[key] = prev
		}
		store.mu.Unlock()
		return pushResult(L, err)
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
		out := L.CreateTable(len(keys), 0)
		for i, k := range keys {
			out.RawSetInt(i+1, lua.LString(k))
		}
		L.Push(out)
		return 1
	}))

	// cliamp.store.clear() -> true or (nil, error)
	L.SetField(tbl, "clear", L.NewFunction(func(L *lua.LState) int {
		store.mu.Lock()
		store.load()
		prev := store.data
		store.data = map[string]any{}
		err := store.save()
		if err != nil {
			store.data = prev
		}
		store.mu.Unlock()
		return pushResult(L, err)
	}))

	L.SetField(cliamp, "store", tbl)
}
