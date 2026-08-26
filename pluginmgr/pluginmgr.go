// Package pluginmgr implements the `cliamp plugins` CLI subcommands:
// list, install, and remove.
package pluginmgr

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/fileutil"
	"github.com/bjarneo/cliamp/internal/plugintrust"
	"github.com/bjarneo/cliamp/luaplugin"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

const maxPluginSize = 1 << 20 // 1 MB

const metadataTimeout = 250 * time.Millisecond

var (
	input  io.Reader = os.Stdin
	output io.Writer = os.Stdout
)

// pluginInfo holds metadata extracted from a plugin's register() call.
type pluginInfo struct {
	file        string
	name        string
	version     string
	description string
	typ         string
	permissions []string
	path        string
	trust       string
	err         error
}

func (p pluginInfo) trustName() string {
	if strings.HasSuffix(p.file, "/") {
		return strings.TrimSuffix(p.file, "/")
	}
	return strings.TrimSuffix(p.file, ".lua")
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

	manifest, trustErr := plugintrust.Load(dir)
	if trustErr != nil {
		return trustErr
	}
	for i := range plugins {
		switch err := plugintrust.Verify(manifest, plugins[i].trustName(), plugins[i].path); {
		case err == nil:
			plugins[i].trust = "trusted"
		case err == plugintrust.ErrHashMismatch:
			plugins[i].trust = "changed"
		default:
			plugins[i].trust = "untrusted"
		}
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

	fmt.Fprintf(output, "%-*s  %-*s  %-*s  %-9s  %s\n", nameW, "NAME", typeW, "TYPE", verW, "VERSION", "TRUST", "DESCRIPTION")
	for _, p := range plugins {
		fmt.Fprintf(output, "%-*s  %-*s  %-*s  %-9s  %s\n", nameW, p.name, typeW, p.typ, verW, p.version, p.trust, p.description)
	}
	return nil
}

// Install downloads a plugin from the given source and saves it to the plugins directory.
func Install(source string, assumeYes ...bool) error {
	urls, name, err := resolveSource(source)
	if err != nil {
		return err
	}
	if err := validateName(name); err != nil {
		return err
	}

	dir, err := appdir.PluginDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating plugins directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("securing plugins directory: %w", err)
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

	info := extractMetadataSource(string(body))
	if info.err != nil {
		return fmt.Errorf("inspect plugin metadata: %w", info.err)
	}
	h := sha256.Sum256(body)
	hash := hex.EncodeToString(h[:])
	fmt.Fprintf(output, "Source: %s\nSHA-256: %s\nDeclared permissions: %s\nImplicit access: unrestricted reads; allowlisted writes; public HTTP\n",
		source, hash, displayPermissions(info.permissions))
	yes := len(assumeYes) > 0 && assumeYes[0]
	if !yes {
		fmt.Fprint(output, "Trust and install this plugin? [y/N] ")
		answer, err := bufio.NewReader(input).ReadString('\n')
		if err != nil && len(answer) == 0 {
			return errors.New("approval required; rerun with --yes for non-interactive installation")
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer != "y" && answer != "yes" {
			return errors.New("plugin installation not approved")
		}
	}

	if err := fileutil.WriteFileAtomic(dest, body, 0o600); err != nil {
		return fmt.Errorf("writing plugin: %w", err)
	}
	if _, err := plugintrust.Approve(dir, name, dest); err != nil {
		_ = os.Remove(dest)
		return fmt.Errorf("recording plugin trust: %w", err)
	}

	fmt.Printf("Installed %s → %s\n", name, dest)
	return nil
}

// Trust approves the current contents of an installed plugin.
func Trust(name string, assumeYes bool) error {
	if err := validateName(name); err != nil {
		return err
	}
	dir, err := appdir.PluginDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, name+".lua")
	if st, statErr := os.Stat(path); statErr != nil {
		path = filepath.Join(dir, name, "init.lua")
	} else if st.IsDir() {
		return fmt.Errorf("plugin %q not found", name)
	}
	info := extractMetadata(path)
	if info.err != nil {
		return fmt.Errorf("inspect plugin metadata: %w", info.err)
	}
	hash, err := plugintrust.HashFile(path)
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "Plugin: %s\nSHA-256: %s\nDeclared permissions: %s\nImplicit access: unrestricted reads; allowlisted writes; public HTTP\n",
		name, hash, displayPermissions(info.permissions))
	if !assumeYes {
		fmt.Fprint(output, "Trust this plugin content? [y/N] ")
		answer, readErr := bufio.NewReader(input).ReadString('\n')
		if readErr != nil && len(answer) == 0 {
			return errors.New("approval required; rerun with --yes")
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer != "y" && answer != "yes" {
			return errors.New("plugin trust not approved")
		}
	}
	_, err = plugintrust.Approve(dir, name, path)
	return err
}

func validateName(name string) error {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		return fmt.Errorf("invalid plugin name %q", name)
	}
	return nil
}

func displayPermissions(perms []string) string {
	if len(perms) == 0 {
		return "none"
	}
	return strings.Join(perms, ", ")
}

// Remove deletes a plugin by name.
func Remove(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
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
	if len(body) == 0 {
		return nil, fmt.Errorf("empty response body")
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
		info.path = path
		if info.name == "" {
			info.name = strings.TrimSuffix(e.Name(), ".lua")
		}
		plugins = append(plugins, info)
	}
	return plugins, nil
}

// extractMetadata runs a Lua file in a minimal VM to capture the plugin.register() call.
func extractMetadata(path string) pluginInfo {
	data, err := os.ReadFile(path)
	if err != nil {
		return pluginInfo{err: err}
	}
	return extractMetadataSource(string(data))
}

func extractMetadataSource(source string) pluginInfo {
	L := lua.NewState(lua.Options{SkipOpenLibs: false})
	defer L.Close()

	// Apply the same sandbox as the runtime: extractMetadata runs DoFile on
	// the whole plugin file (not just register()), so top-level code must not
	// have access to os.execute/io/dofile when merely listing plugins.
	luaplugin.Sandbox(L)

	var info pluginInfo
	ctx, cancel := context.WithTimeout(context.Background(), metadataTimeout)
	defer cancel()
	L.SetContext(ctx)
	defer L.RemoveContext()

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
		if v := opts.RawGetString("permissions"); v != lua.LNil {
			tbl, ok := v.(*lua.LTable)
			if !ok {
				info.err = errors.New("permissions must be an array")
			} else {
				known := map[string]bool{"control": true, "exec": true, "keymap": true}
				tbl.ForEach(func(_, value lua.LValue) {
					permission := value.String()
					if !known[permission] && info.err == nil {
						info.err = fmt.Errorf("unknown permission %q", permission)
					}
					info.permissions = append(info.permissions, permission)
				})
			}
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

	// No cliamp API is installed: metadata inspection happens before trust.
	if err := L.DoString(source); err != nil && info.name == "" {
		info.err = err
	}

	return info
}
