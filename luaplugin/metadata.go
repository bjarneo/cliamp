package luaplugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// Metadata is what a plugin declares in plugin.register().
type Metadata struct {
	Name        string // empty when the plugin sets no name
	Version     string
	Description string
	Type        string   // "hook" or "visualizer"
	Permissions []string // in the declared order
}

// knownPermissions holds the names that plugin.register() accepts in
// permissions.
var knownPermissions = map[string]bool{PermControl: true, PermExec: true, PermKeymap: true}

// errRegisteredTwice is the error of a second plugin.register() call. A
// later call could otherwise replace the permissions that install and trust
// showed.
var errRegisteredTwice = errors.New("plugin.register() can be called only once")

// parseRegisterOpts reads and checks the table passed to plugin.register().
// The runtime and ReadMetadata both use it, so `cliamp plugins` accepts and
// rejects the same plugins as the player.
func parseRegisterOpts(opts *lua.LTable) (Metadata, error) {
	var md Metadata
	for _, f := range []struct {
		key string
		dst *string
	}{{"name", &md.Name}, {"version", &md.Version}, {"description", &md.Description}, {"type", &md.Type}} {
		if v := opts.RawGetString(f.key); v != lua.LNil {
			*f.dst = v.String()
		}
	}
	if md.Type != "hook" && md.Type != "visualizer" {
		return md, errors.New(`plugin.register() needs type = "hook" or "visualizer"`)
	}
	v := opts.RawGetString("permissions")
	if v == lua.LNil {
		return md, nil
	}
	tbl, ok := v.(*lua.LTable)
	if !ok {
		return md, errors.New("permissions must be an array")
	}
	var err error
	tbl.ForEach(func(_, v lua.LValue) {
		permission := v.String()
		if err == nil && !knownPermissions[permission] {
			err = fmt.Errorf("unknown permission %q", permission)
		}
		md.Permissions = append(md.Permissions, permission)
	})
	return md, err
}

// metadataTimeout bounds ReadMetadata, which runs the whole plugin source.
const metadataTimeout = 250 * time.Millisecond

// ReadMetadata runs a plugin source in the sandbox and returns what it passes
// to plugin.register(). It checks the call the same way the player does.
// The cliamp table and the plugin object are stubs that do nothing, because
// a plugin is inspected before the user trusts it. An error after a
// plugin.register() call that passed the checks comes from a stub, so
// ReadMetadata ignores it. A source that never calls plugin.register()
// returns empty Metadata and no error, because the player skips such a file.
func ReadMetadata(source string) (Metadata, error) {
	L := lua.NewState()
	defer L.Close()
	sandbox(L)
	ctx, cancel := context.WithTimeout(context.Background(), metadataTimeout)
	defer cancel()
	L.SetContext(ctx)

	stub := newStub(L)
	var md Metadata
	var registered bool
	var regErr error
	pluginTbl := L.NewTable()
	L.SetField(pluginTbl, "register", L.NewFunction(func(L *lua.LState) int {
		got, err := parseRegisterOpts(L.CheckTable(1))
		if err == nil && registered {
			err = errRegisteredTwice
		}
		if err != nil {
			regErr = err
			L.RaiseError("%v", err)
		}
		md, registered = got, true
		L.Push(stub)
		return 1
	}))
	L.SetGlobal("plugin", pluginTbl)
	L.SetGlobal("cliamp", stub)

	err := L.DoString(source)
	switch {
	case regErr != nil:
		return md, regErr
	case registered:
		return md, nil
	default:
		return Metadata{}, err
	}
}

// newStub returns a table that stands in for the cliamp table and the plugin
// object. Each field of the stub is the stub, and a call to it returns
// nothing. Thus cliamp.log.info("x") and p:bind("x", fn) run and do nothing.
func newStub(L *lua.LState) *lua.LTable {
	stub := L.NewTable()
	meta := L.NewTable()
	L.SetField(meta, "__index", L.NewFunction(func(L *lua.LState) int {
		L.Push(stub)
		return 1
	}))
	L.SetField(meta, "__call", L.NewFunction(func(*lua.LState) int { return 0 }))
	L.SetMetatable(stub, meta)
	return stub
}

// PluginFile is one installed plugin: a .lua file, or a directory with an
// init.lua file.
type PluginFile struct {
	Name string // installed name: the file name without .lua, or the directory name
	Path string // the file that cliamp runs
}

// Discover lists the plugins in dir, sorted by name. When name.lua and
// name/init.lua both exist, name.lua is the plugin, because trust keys by
// name and `cliamp plugins trust` and `remove` pick name.lua first.
func Discover(dir string) ([]PluginFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []PluginFile
	isFile := make(map[string]bool)
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".lua"); ok && !e.IsDir() {
			files = append(files, PluginFile{Name: name, Path: filepath.Join(dir, e.Name())})
			isFile[name] = true
		}
	}
	for _, e := range entries {
		if !e.IsDir() || isFile[e.Name()] {
			continue
		}
		init := filepath.Join(dir, e.Name(), "init.lua")
		if _, err := os.Stat(init); err == nil {
			files = append(files, PluginFile{Name: e.Name(), Path: init})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, nil
}
