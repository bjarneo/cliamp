// Package pluginmgr implements the `cliamp plugins` CLI subcommands:
// list, install, and remove.
package pluginmgr

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/fileutil"
	"github.com/bjarneo/cliamp/internal/httpclient"
	"github.com/bjarneo/cliamp/internal/plugintrust"
	"github.com/bjarneo/cliamp/luaplugin"
)

var httpClient = httpclient.NewAPI(30 * time.Second)

const maxPluginSize = 1 << 20 // 1 MB

var (
	input  io.Reader = os.Stdin
	output io.Writer = os.Stdout
)

// pluginInfo holds metadata extracted from a plugin's register() call.
type pluginInfo struct {
	luaplugin.Metadata
	id    string // installed name, as luaplugin.Discover reports it
	path  string
	trust string
	err   error
}

// List prints all installed plugins with their metadata.
func List() error {
	dir, err := appdir.PluginDir()
	if err != nil {
		return err
	}

	plugins, err := scanPlugins(dir)
	if err != nil || len(plugins) == 0 {
		fmt.Fprintln(output, "No plugins installed.")
		return nil
	}

	// Like the player, treat every plugin as untrusted when the manifest
	// does not load, and return the error after the list.
	manifest, trustErr := plugintrust.Load(dir)
	if trustErr != nil {
		manifest = plugintrust.Manifest{}
	}
	for i := range plugins {
		switch err := plugintrust.Verify(manifest, plugins[i].id, plugins[i].path); {
		case err == nil:
			plugins[i].trust = "trusted"
		case err == plugintrust.ErrHashMismatch:
			plugins[i].trust = "changed"
		default:
			plugins[i].trust = "untrusted"
		}
	}

	// The metadata comes from plugin code that may be untrusted. Drop its
	// control characters, so it cannot move the cursor or hide text.
	for i := range plugins {
		plugins[i].Name = printable(plugins[i].Name)
		plugins[i].Version = printable(plugins[i].Version)
		plugins[i].Description = printable(plugins[i].Description)
	}

	// Calculate column widths.
	nameW, typeW, verW := 4, 4, 7 // "NAME", "TYPE", "VERSION"
	for _, p := range plugins {
		if len(p.Name) > nameW {
			nameW = len(p.Name)
		}
		if len(p.Type) > typeW {
			typeW = len(p.Type)
		}
		if len(p.Version) > verW {
			verW = len(p.Version)
		}
	}

	fmt.Fprintf(output, "%-*s  %-*s  %-*s  %-9s  %s\n", nameW, "NAME", typeW, "TYPE", verW, "VERSION", "TRUST", "DESCRIPTION")
	for _, p := range plugins {
		fmt.Fprintf(output, "%-*s  %-*s  %-*s  %-9s  %s\n", nameW, p.Name, typeW, p.Type, verW, p.Version, p.trust, p.Description)
	}
	if trustErr != nil {
		return manifestError(dir, trustErr)
	}
	return nil
}

// printable returns s without control characters, such as ESC or a newline.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// manifestError explains how to recover from a trust manifest that does not
// load. cliamp plugins trust cannot add to such a file.
func manifestError(dir string, err error) error {
	return fmt.Errorf("%w: delete %s, then run `cliamp plugins trust <name>` for each plugin", err, plugintrust.ManifestPath(dir))
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
	// Approve needs the manifest. Check it before the download and the prompt.
	if _, err := plugintrust.Load(dir); err != nil {
		return manifestError(dir, err)
	}

	// Try each candidate URL.
	var body []byte
	for _, u := range urls {
		fmt.Fprintf(output, "Trying %s...\n", u)
		b, err := download(u)
		if err == nil {
			body = b
			break
		}
	}
	if body == nil {
		return fmt.Errorf("could not download plugin from any of: %s", strings.Join(urls, ", "))
	}

	md, err := luaplugin.ReadMetadata(string(body))
	if err != nil {
		return fmt.Errorf("inspect plugin metadata: %w", err)
	}
	hash := plugintrust.Hash(body)
	fmt.Fprintf(output, "Source: %s\nSHA-256: %s\nDeclared permissions: %s\nImplicit access: unrestricted reads; allowlisted writes; public HTTP\n",
		source, hash, displayPermissions(md.Permissions))
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
	if err := plugintrust.ApproveHash(dir, name, dest, hash, md.Permissions); err != nil {
		_ = os.Remove(dest)
		return fmt.Errorf("recording plugin trust: %w", err)
	}

	fmt.Fprintf(output, "Installed %s → %s\n", name, dest)
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
	files, err := luaplugin.Discover(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	i := slices.IndexFunc(files, func(f luaplugin.PluginFile) bool { return f.Name == name })
	if i < 0 {
		return fmt.Errorf("plugin %q not found", name)
	}
	path := files[i].Path
	if _, err := plugintrust.Load(dir); err != nil {
		return manifestError(dir, err)
	}
	// Read the file once, so the permissions and the hash that the prompt
	// shows come from the same content.
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	md, err := luaplugin.ReadMetadata(string(data))
	if err != nil {
		return fmt.Errorf("inspect plugin metadata: %w", err)
	}
	hash := plugintrust.Hash(data)
	fmt.Fprintf(output, "Plugin: %s\nSHA-256: %s\nDeclared permissions: %s\nImplicit access: unrestricted reads; allowlisted writes; public HTTP\n",
		name, hash, displayPermissions(md.Permissions))
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
	if err := plugintrust.ApproveHash(dir, name, path, hash, md.Permissions); err != nil {
		if errors.Is(err, plugintrust.ErrHashMismatch) {
			return fmt.Errorf("plugin %q changed after cliamp showed it; run `cliamp plugins trust %s` again", name, name)
		}
		return err
	}
	return nil
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

// Remove deletes a plugin by name and revokes its approval.
func Remove(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	dir, err := appdir.PluginDir()
	if err != nil {
		return err
	}
	// Revoke needs the manifest. Check it before the plugin is gone.
	if _, err := plugintrust.Load(dir); err != nil {
		return manifestError(dir, err)
	}

	// Try single file first, then directory.
	filePath := filepath.Join(dir, name+".lua")
	dirPath := filepath.Join(dir, name)
	var removed string
	if _, err := os.Stat(filePath); err == nil {
		if err := os.Remove(filePath); err != nil {
			return fmt.Errorf("removing plugin: %w", err)
		}
		removed = filePath
	} else if info, err := os.Stat(dirPath); err == nil && info.IsDir() {
		if err := os.RemoveAll(dirPath); err != nil {
			return fmt.Errorf("removing plugin directory: %w", err)
		}
		removed = dirPath
	} else {
		return fmt.Errorf("plugin %q not found", name)
	}
	fmt.Fprintf(output, "Removed %s\n", removed)

	// A stale approval would trust a later file with the same content.
	if err := plugintrust.Revoke(dir, name); err != nil {
		return fmt.Errorf("revoke plugin approval: %w", err)
	}
	return nil
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

// scanPlugins lists the plugins that luaplugin.Discover finds in dir, with
// the metadata of each one.
func scanPlugins(dir string) ([]pluginInfo, error) {
	files, err := luaplugin.Discover(dir)
	if err != nil {
		return nil, err
	}
	plugins := make([]pluginInfo, 0, len(files))
	for _, f := range files {
		info := extractMetadata(f.Path)
		info.id, info.path = f.Name, f.Path
		if info.Name == "" {
			info.Name = f.Name
		}
		plugins = append(plugins, info)
	}
	return plugins, nil
}

// extractMetadata reads a plugin file and inspects it with
// luaplugin.ReadMetadata, which checks plugin.register() as the player does.
func extractMetadata(path string) pluginInfo {
	data, err := os.ReadFile(path)
	if err != nil {
		return pluginInfo{err: err}
	}
	md, err := luaplugin.ReadMetadata(string(data))
	return pluginInfo{Metadata: md, err: err}
}
