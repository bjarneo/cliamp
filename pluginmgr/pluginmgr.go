// Package pluginmgr implements the `cliamp plugins` CLI subcommands:
// list, install, and remove.
package pluginmgr

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"

	"cliamp/internal/appdir"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

const maxPluginSize = 1 << 20 // 1 MB

// pluginInfo holds metadata extracted from a plugin's register() call.
type pluginInfo struct {
	file        string
	name        string
	version     string
	description string
	typ         string
}

// List prints all installed plugins with their metadata.
func List() error {
	dir, err := appdir.PluginDir()
	if err != nil {
		return err
	}

	plugins, err := scanPlugins(dir)
	if err != nil {
		fmt.Println("No plugins installed.")
		return nil
	}
	if len(plugins) == 0 {
		fmt.Println("No plugins installed.")
		return nil
	}

	// Calculate column widths.
	nameW, typeW, verW := 4, 4, 7 // "NAME", "TYPE", "VERSION"
	for _, p := range plugins {
		if len(p.name) > nameW {
			nameW = len(p.name)
		}
		if len(p.typ) > typeW {
			typeW = len(p.typ)
		}
		if len(p.version) > verW {
			verW = len(p.version)
		}
	}

	fmt.Printf("%-*s  %-*s  %-*s  %s\n", nameW, "NAME", typeW, "TYPE", verW, "VERSION", "DESCRIPTION")
	for _, p := range plugins {
		fmt.Printf("%-*s  %-*s  %-*s  %s\n", nameW, p.name, typeW, p.typ, verW, p.version, p.description)
	}
	return nil
}

// Install downloads a plugin from the given source and saves it to the plugins directory.
func Install(source string) error {
	urls, name, err := resolveSource(source)
	if err != nil {
		return err
	}

	dir, err := appdir.PluginDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating plugins directory: %w", err)
	}

	// Check if already installed (file or directory).
	dest := filepath.Join(dir, name+".lua")
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("plugin %q already exists at %s (remove it first with: cliamp plugins remove %s)", name, dest, name)
	}
	if info, err := os.Stat(filepath.Join(dir, name)); err == nil && info.IsDir() {
		return fmt.Errorf("plugin %q already exists as directory (remove it first with: cliamp plugins remove %s)", name, name)
	}

	// Try each candidate URL.
	var body []byte
	for _, u := range urls {
		fmt.Printf("Trying %s...\n", u)
		b, err := download(u)
		if err == nil {
			body = b
			break
		}
	}
	if body == nil {
		return fmt.Errorf("could not download plugin from any of: %s", strings.Join(urls, ", "))
	}

	if err := os.WriteFile(dest, body, 0o644); err != nil {
		return fmt.Errorf("writing plugin: %w", err)
	}

	fmt.Printf("Installed %s → %s\n", name, dest)
	return nil
}

// Remove deletes a plugin by name.
func Remove(name string) error {
	dir, err := appdir.PluginDir()
	if err != nil {
		return err
	}

	// Try single file first, then directory.
	filePath := filepath.Join(dir, name+".lua")
	if _, err := os.Stat(filePath); err == nil {
		if err := os.Remove(filePath); err != nil {
			return fmt.Errorf("removing plugin: %w", err)
		}
		fmt.Printf("Removed %s\n", filePath)
		return nil
	}

	dirPath := filepath.Join(dir, name)
	if info, err := os.Stat(dirPath); err == nil && info.IsDir() {
		if err := os.RemoveAll(dirPath); err != nil {
			return fmt.Errorf("removing plugin directory: %w", err)
		}
		fmt.Printf("Removed %s\n", dirPath)
		return nil
	}

	return fmt.Errorf("plugin %q not found", name)
}

func download(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPluginSize+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxPluginSize {
		return nil, fmt.Errorf("plugin too large (max %d bytes)", maxPluginSize)
	}
	return body, nil
}

// scanPlugins reads the plugin directory and extracts metadata from each plugin
// using a lightweight Lua VM.
func scanPlugins(dir string) ([]pluginInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var plugins []pluginInfo
	for _, e := range entries {
		var path, file string
		if e.IsDir() {
			init := filepath.Join(dir, e.Name(), "init.lua")
			if _, err := os.Stat(init); err != nil {
				continue
			}
			path = init
			file = e.Name() + "/"
		} else if strings.HasSuffix(e.Name(), ".lua") {
			path = filepath.Join(dir, e.Name())
			file = e.Name()
		} else {
			continue
		}

		info := extractMetadata(path)
		info.file = file
		if info.name == "" {
			info.name = strings.TrimSuffix(e.Name(), ".lua")
		}
		plugins = append(plugins, info)
	}
	return plugins, nil
}

// extractMetadata runs a Lua file in a minimal VM to capture the plugin.register() call.
func extractMetadata(path string) pluginInfo {
	L := lua.NewState(lua.Options{SkipOpenLibs: false})
	defer L.Close()

	var info pluginInfo

	// Stub out plugin.register() to capture metadata without side effects.
	pluginTbl := L.NewTable()
	L.SetField(pluginTbl, "register", L.NewFunction(func(L *lua.LState) int {
		opts := L.CheckTable(1)
		if v := opts.RawGetString("name"); v != lua.LNil {
			info.name = v.String()
		}
		if v := opts.RawGetString("version"); v != lua.LNil {
			info.version = v.String()
		}
		if v := opts.RawGetString("description"); v != lua.LNil {
			info.description = v.String()
		}
		if v := opts.RawGetString("type"); v != lua.LNil {
			info.typ = v.String()
		}
		// Return a dummy object with stub on/config methods.
		obj := L.NewTable()
		noop := L.NewFunction(func(L *lua.LState) int {
			L.Push(lua.LNil)
			return 1
		})
		L.SetField(obj, "on", noop)
		L.SetField(obj, "config", noop)
		L.Push(obj)
		return 1
	}))
	L.SetGlobal("plugin", pluginTbl)

	// Stub cliamp global so plugins don't error on API calls.
	L.SetGlobal("cliamp", L.NewTable())

	// Ignore errors — we just want the metadata from register().
	_ = L.DoFile(path)

	return info
}
