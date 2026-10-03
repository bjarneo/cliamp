package pluginmgr

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/bjarneo/cliamp/internal/plugintrust"
	"github.com/bjarneo/cliamp/luaplugin"
)

// redirectTransport rewrites every request's Host to point at target.
type redirectTransport struct {
	target *url.URL
	rt     http.RoundTripper
}

func (t redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = t.target.Scheme
	clone.URL.Host = t.target.Host
	clone.Host = t.target.Host
	return t.rt.RoundTrip(clone)
}

func installTestClient(t *testing.T, serverURL string) {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	old := httpClient
	httpClient = &http.Client{
		Timeout:   5 * time.Second,
		Transport: redirectTransport{target: u, rt: http.DefaultTransport},
	}
	t.Cleanup(func() { httpClient = old })
}

func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestScanPluginsEmptyDir(t *testing.T) {
	dir := t.TempDir()
	plugins, err := scanPlugins(dir)
	if err != nil {
		t.Fatalf("scanPlugins: %v", err)
	}
	if len(plugins) != 0 {
		t.Errorf("expected 0 plugins in empty dir, got %d", len(plugins))
	}
}

func TestScanPluginsSingleFile(t *testing.T) {
	dir := t.TempDir()
	src := `plugin.register({
  name = "hello",
  version = "1.2",
  description = "says hi",
  type = "visualizer",
})
`
	if err := os.WriteFile(filepath.Join(dir, "hello.lua"), []byte(src), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	plugins, err := scanPlugins(dir)
	if err != nil {
		t.Fatalf("scanPlugins: %v", err)
	}
	if len(plugins) != 1 {
		t.Fatalf("got %d plugins, want 1", len(plugins))
	}
	got := plugins[0]
	if got.Name != "hello" {
		t.Errorf("name = %q, want hello", got.Name)
	}
	if got.Version != "1.2" {
		t.Errorf("version = %q, want 1.2", got.Version)
	}
	if got.Description != "says hi" {
		t.Errorf("description = %q, want 'says hi'", got.Description)
	}
	if got.Type != "visualizer" {
		t.Errorf("type = %q, want visualizer", got.Type)
	}
	if got.id != "hello" || got.path != filepath.Join(dir, "hello.lua") {
		t.Errorf("id, path = %q, %q, want hello and hello.lua", got.id, got.path)
	}
}

func TestScanPluginsFallsBackToFilename(t *testing.T) {
	dir := t.TempDir()
	// Plugin without register() call — name should default to filename.
	if err := os.WriteFile(filepath.Join(dir, "nameless.lua"), []byte(`-- nothing`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	plugins, err := scanPlugins(dir)
	if err != nil {
		t.Fatalf("scanPlugins: %v", err)
	}
	if len(plugins) != 1 {
		t.Fatalf("got %d plugins, want 1", len(plugins))
	}
	if plugins[0].Name != "nameless" {
		t.Errorf("name = %q, want 'nameless' (filename fallback)", plugins[0].Name)
	}
}

func TestScanPluginsDirectoryEntry(t *testing.T) {
	dir := t.TempDir()
	// Create a directory plugin: <dir>/myplug/init.lua
	sub := filepath.Join(dir, "myplug")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "init.lua"), []byte(`plugin.register({ name = "myplug", version = "0.1" })`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	plugins, err := scanPlugins(dir)
	if err != nil {
		t.Fatalf("scanPlugins: %v", err)
	}
	if len(plugins) != 1 {
		t.Fatalf("got %d plugins, want 1", len(plugins))
	}
	if plugins[0].id != "myplug" || plugins[0].path != filepath.Join(sub, "init.lua") {
		t.Errorf("id, path = %q, %q, want myplug and myplug/init.lua", plugins[0].id, plugins[0].path)
	}
}

func TestScanPluginsIgnoresNonLua(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("nothing"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	plugins, err := scanPlugins(dir)
	if err != nil {
		t.Fatalf("scanPlugins: %v", err)
	}
	if len(plugins) != 0 {
		t.Errorf("non-lua file should be ignored, got %+v", plugins)
	}
}

func TestListNoPlugins(t *testing.T) {
	withTempHome(t)
	if err := List(); err != nil {
		t.Errorf("List on empty dir should not error, got %v", err)
	}
}

func TestListPluginsShowsInstalled(t *testing.T) {
	home := withTempHome(t)
	plugDir := filepath.Join(home, ".config", "cliamp", "plugins")
	if err := os.MkdirAll(plugDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(plugDir, "x.lua"), []byte(`plugin.register({ name = "x", version = "1.0" })`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// List writes to stdout — we just verify it doesn't error.
	if err := List(); err != nil {
		t.Errorf("List: %v", err)
	}
}

func TestTrustUsesPluginEntryName(t *testing.T) {
	for _, tt := range []struct {
		name string
		path string
	}{
		{name: "single file", path: "hello.lua"},
		{name: "directory", path: "hello/init.lua"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := withTempHome(t)
			pluginDir := filepath.Join(home, ".config", "cliamp", "plugins")
			path := filepath.Join(pluginDir, tt.path)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.WriteFile(path, []byte(`plugin.register({ name = "registered", version = "1.0", type = "hook" })`), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			var out bytes.Buffer
			oldOutput := output
			output = &out
			t.Cleanup(func() { output = oldOutput })

			if err := Trust("hello", true); err != nil {
				t.Fatalf("Trust: %v", err)
			}
			out.Reset()
			if err := List(); err != nil {
				t.Fatalf("List: %v", err)
			}
			if strings.Contains(out.String(), "untrusted") || !strings.Contains(out.String(), "trusted") {
				t.Errorf("List output = %q, want trusted", out.String())
			}
		})
	}
}

func TestRemoveFile(t *testing.T) {
	home := withTempHome(t)
	plugDir := filepath.Join(home, ".config", "cliamp", "plugins")
	if err := os.MkdirAll(plugDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(plugDir, "foo.lua")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := Remove("foo"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("plugin should be removed, stat err=%v", err)
	}
}

func TestRemoveDirectory(t *testing.T) {
	home := withTempHome(t)
	plugDir := filepath.Join(home, ".config", "cliamp", "plugins")
	nested := filepath.Join(plugDir, "bar", "init.lua")
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(nested, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := Remove("bar"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(nested)); !os.IsNotExist(err) {
		t.Errorf("plugin dir should be removed, stat err=%v", err)
	}
}

// Remove also revokes the approval, so a copy of the old file is untrusted.
func TestRemoveRevokesTrust(t *testing.T) {
	for _, tt := range []struct {
		name string
		path string
	}{
		{"single file", "foo.lua"},
		{"directory", "foo/init.lua"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := withTempHome(t)
			pluginDir := filepath.Join(home, ".config", "cliamp", "plugins")
			path := filepath.Join(pluginDir, tt.path)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(`plugin.register({name = "foo", type = "hook"})`), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := plugintrust.Approve(pluginDir, "foo", path); err != nil {
				t.Fatal(err)
			}
			silenceOutput(t)

			if err := Remove("foo"); err != nil {
				t.Fatalf("Remove: %v", err)
			}
			m, err := plugintrust.Load(pluginDir)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := m.Plugins["foo"]; ok {
				t.Errorf("trust manifest still approves foo after Remove")
			}
		})
	}
}

func TestRemoveMissing(t *testing.T) {
	withTempHome(t)
	err := Remove("ghost")
	if err == nil {
		t.Error("Remove non-existent plugin should error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want to mention 'not found'", err.Error())
	}
}

func TestInstallFromRawURL(t *testing.T) {
	home := withTempHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/example.lua") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`plugin.register({ name = "example", version = "1", type = "hook" })`))
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)

	if err := Install(srv.URL+"/example.lua", true); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Verify the installed file exists. Use the temp HOME (not os.UserHomeDir,
	// which ignores HOME on Windows) so this matches where Install wrote it.
	dest := filepath.Join(home, ".config", "cliamp", "plugins", "example.lua")
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("installed plugin missing: %v", err)
	}
}

func TestInstallAlreadyExists(t *testing.T) {
	home := withTempHome(t)
	plugDir := filepath.Join(home, ".config", "cliamp", "plugins")
	if err := os.MkdirAll(plugDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	existing := filepath.Join(plugDir, "mypl.lua")
	if err := os.WriteFile(existing, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`-- ok`))
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)

	err := Install(srv.URL+"/mypl.lua", true)
	if err == nil {
		t.Fatal("Install over existing plugin should error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want to mention 'already exists'", err.Error())
	}
}

func TestInstallAllURLsFail(t *testing.T) {
	withTempHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)

	err := Install(srv.URL+"/nonexistent.lua", true)
	if err == nil {
		t.Error("Install with all failing URLs should error")
	}
}

func TestInstallTooLarge(t *testing.T) {
	withTempHome(t)
	big := strings.Repeat("x", maxPluginSize+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(big))
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)

	err := Install(srv.URL+"/huge.lua", true)
	if err == nil {
		t.Error("Install of oversized plugin should fail")
	}
}

func TestDownloadErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr string
	}{
		{"404", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "missing", http.StatusNotFound)
		}, "HTTP"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()
			installTestClient(t, srv.URL)

			_, err := download(srv.URL + "/x.lua")
			if err == nil {
				t.Fatal("download should have errored")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// installForTest writes source as <name>.lua in the plugin dir of a temp HOME.
func installForTest(t *testing.T, name, source string) (pluginDir, path string) {
	t.Helper()
	home := withTempHome(t)
	pluginDir = filepath.Join(home, ".config", "cliamp", "plugins")
	path = filepath.Join(pluginDir, name+".lua")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return pluginDir, path
}

// silenceOutput sends the CLI output to a buffer for the rest of the test.
func silenceOutput(t *testing.T) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	old := output
	output = &out
	t.Cleanup(func() { output = old })
	return &out
}

// cliamp plugins trust accepts a plugin exactly when the player loads it
// without an error, because both use the luaplugin register() parser.
func TestTrustMatchesRuntime(t *testing.T) {
	tests := []struct {
		name   string
		source string
		accept bool
	}{
		{"hook", `plugin.register({name = "p", type = "hook"})`, true},
		{"bind at the top level without a name", `
			local p = plugin.register({type = "hook", permissions = {"keymap"}})
			p:bind("ctrl+y", "Say hi", function() end)`, true},
		{"cliamp call at the top level without a name", `
			plugin.register({type = "hook"})
			cliamp.log.info("loaded")`, true},
		{"cliamp call before register", `
			cliamp.log.info("loading")
			plugin.register({name = "p", type = "hook"})`, true},
		{"no register call", `local x = 1`, true},
		{"unknown permission", `plugin.register({name = "p", type = "hook", permissions = {"root"}})`, false},
		{"permissions not an array", `plugin.register({name = "p", type = "hook", permissions = "exec"})`, false},
		{"missing type", `plugin.register({name = "p"})`, false},
		{"unknown type", `plugin.register({name = "p", type = "visualiser"})`, false},
		{"syntax error", `plugin.register({`, false},
		{"second register call", `
			plugin.register({name = "p", type = "hook"})
			plugin.register({name = "p", type = "hook"})`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pluginDir, path := installForTest(t, "p", tt.source)
			silenceOutput(t)

			trustErr := Trust("p", true)
			if _, err := plugintrust.Approve(pluginDir, "p", path); err != nil {
				t.Fatal(err)
			}
			mgr, loadErr := luaplugin.New(nil, nil, nil)
			mgr.Close()

			if (trustErr == nil) != tt.accept {
				t.Errorf("Trust() error = %v, want accept = %v", trustErr, tt.accept)
			}
			if (trustErr == nil) != (loadErr == nil) {
				t.Errorf("Trust() error = %v, but the player load error = %v", trustErr, loadErr)
			}
		})
	}
}

// With p.lua and p/init.lua both installed, the CLI and the player use the
// same file, so the approval from cliamp plugins trust loads the plugin.
func TestTrustAndRuntimePickSameFile(t *testing.T) {
	pluginDir, _ := installForTest(t, "p", `plugin.register({name = "file", type = "hook"})`)
	init := filepath.Join(pluginDir, "p", "init.lua")
	if err := os.MkdirAll(filepath.Dir(init), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(init, []byte(`plugin.register({name = "dir", type = "hook"})`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := silenceOutput(t)

	if err := Trust("p", true); err != nil {
		t.Fatalf("Trust: %v", err)
	}
	mgr, err := luaplugin.New(nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()
	if mgr.PluginCount() != 1 {
		t.Fatalf("New() loaded %d plugins, want 1", mgr.PluginCount())
	}
	out.Reset()
	if err := List(); err != nil {
		t.Fatalf("List: %v", err)
	}
	if lines := strings.Split(strings.TrimSpace(out.String()), "\n"); len(lines) != 2 ||
		!strings.Contains(lines[1], "file") || !strings.Contains(lines[1], "trusted") {
		t.Errorf("List output = %q, want one trusted row for p.lua", out.String())
	}
}

// cliamp plugins list, trust, install and remove explain a trust manifest
// that does not load. list still shows each plugin, as untrusted like the
// player treats it. trust, install and remove fail before they download, ask
// or delete, and they leave the files as they are.
func TestBadTrustManifest(t *testing.T) {
	manifests := []struct {
		name    string
		content func(hash string) string
	}{
		{"not JSON", func(string) string { return "{" }},
		{"unsupported version with a matching hash", func(hash string) string {
			return `{"version":2,"plugins":{"hello":"` + hash + `"}}`
		}},
	}
	cmds := []struct {
		name string
		run  func(t *testing.T, pluginDir string, out *bytes.Buffer) error
	}{
		{"list", func(t *testing.T, _ string, out *bytes.Buffer) error {
			err := List()
			if !strings.Contains(out.String(), " untrusted ") {
				t.Errorf("List output = %q, want hello as untrusted", out.String())
			}
			return err
		}},
		{"trust", func(*testing.T, string, *bytes.Buffer) error { return Trust("hello", false) }},
		{"install", func(t *testing.T, pluginDir string, _ *bytes.Buffer) error {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("Install downloaded %s", r.URL.Path)
				_, _ = w.Write([]byte(`plugin.register({name = "fresh", type = "hook"})`))
			}))
			t.Cleanup(srv.Close)
			installTestClient(t, srv.URL)
			oldInput := input
			input = strings.NewReader("y\n")
			t.Cleanup(func() { input = oldInput })

			err := Install(srv.URL+"/fresh.lua", false)
			if _, statErr := os.Stat(filepath.Join(pluginDir, "fresh.lua")); !os.IsNotExist(statErr) {
				t.Errorf("Install wrote fresh.lua, stat err = %v", statErr)
			}
			return err
		}},
		{"remove", func(t *testing.T, pluginDir string, _ *bytes.Buffer) error {
			err := Remove("hello")
			if _, statErr := os.Stat(filepath.Join(pluginDir, "hello.lua")); statErr != nil {
				t.Errorf("Remove deleted hello.lua, stat err = %v", statErr)
			}
			return err
		}},
	}
	for _, mf := range manifests {
		for _, cmd := range cmds {
			t.Run(mf.name+"/"+cmd.name, func(t *testing.T) {
				pluginDir, path := installForTest(t, "hello", `plugin.register({name = "hello", type = "hook"})`)
				hash, err := plugintrust.HashFile(path)
				if err != nil {
					t.Fatal(err)
				}
				content := mf.content(hash)
				manifest := filepath.Join(pluginDir, ".trust.json")
				if err := os.WriteFile(manifest, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
				out := silenceOutput(t)
				err = cmd.run(t, pluginDir, out)
				if strings.Contains(out.String(), "[y/N]") {
					t.Errorf("%s asked for approval: %q", cmd.name, out.String())
				}
				hint := "delete " + manifest + ", then run `cliamp plugins trust <name>` for each plugin"
				if err == nil || !strings.Contains(err.Error(), hint) {
					t.Fatalf("%s error = %v, want the hint %q", cmd.name, err, hint)
				}
				if data, _ := os.ReadFile(manifest); string(data) != content {
					t.Errorf("manifest = %q, want it unchanged", data)
				}
			})
		}
	}
}

// cliamp plugins list prints metadata from plugin code that may be
// untrusted. It drops control characters, so the metadata cannot clear the
// screen, move the cursor or add lines.
func TestListDropsControlCharacters(t *testing.T) {
	tests := []struct {
		name  string
		field string
	}{
		{"name", `name = "evil\27[2J\nfake"`},
		{"version", `version = "1.0\27[8m"`},
		{"description", `description = "hidden\r\27]52;c;aGk=\7"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installForTest(t, "evil", `plugin.register({ type = "hook", `+tt.field+` })`)
			out := silenceOutput(t)
			if err := List(); err != nil {
				t.Fatalf("List: %v", err)
			}
			if i := strings.IndexFunc(out.String(), func(r rune) bool { return unicode.IsControl(r) && r != '\n' }); i >= 0 {
				t.Errorf("List output = %q, has a control character at %d", out.String(), i)
			}
			if lines := strings.Count(out.String(), "\n"); lines != 2 {
				t.Errorf("List output = %q, want 2 lines", out.String())
			}
		})
	}
}

// editOnRead stands for an edit of the plugin file while the trust prompt
// waits. At the first read, it writes content to path. Then it answers y.
type editOnRead struct {
	path, content string
	answer        io.Reader
}

func (r *editOnRead) Read(p []byte) (int, error) {
	if r.answer == nil {
		if r.content != "" {
			if err := os.WriteFile(r.path, []byte(r.content), 0o644); err != nil {
				return 0, err
			}
		}
		r.answer = strings.NewReader("y\n")
	}
	return r.answer.Read(p)
}

// cliamp plugins trust approves only the content that it showed. Before, it
// hashed the file again after the prompt, so an edit while the prompt waited
// was approved.
func TestTrustApprovesShownContent(t *testing.T) {
	const shown = `plugin.register({ name = "p", type = "hook" })`
	tests := []struct {
		name  string
		edit  string // the content that the file gets while the prompt waits
		trust bool
	}{
		{"unchanged", "", true},
		{"edited while the prompt waits", `plugin.register({ name = "p", type = "hook", permissions = {"exec"} })`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pluginDir, path := installForTest(t, "p", shown)
			out := silenceOutput(t)
			oldInput := input
			input = &editOnRead{path: path, content: tt.edit}
			t.Cleanup(func() { input = oldInput })

			err := Trust("p", false)
			if (err == nil) != tt.trust {
				t.Fatalf("Trust() error = %v, want an error: %v", err, !tt.trust)
			}
			if !strings.Contains(out.String(), plugintrust.Hash([]byte(shown))) {
				t.Errorf("Trust output = %q, want the hash of the shown content", out.String())
			}
			m, err := plugintrust.Load(pluginDir)
			if err != nil {
				t.Fatal(err)
			}
			_, approved := m.Plugins["p"]
			if approved != tt.trust {
				t.Errorf("manifest approves p: %v, want %v", approved, tt.trust)
			}
		})
	}
}

// install and trust record the permissions that their prompt showed. The
// player loads the plugin only with these permissions.
func TestApprovalRecordsShownPermissions(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   []string
	}{
		{"no permissions", `plugin.register({ name = "p", type = "hook" })`, []string{}},
		{"permissions", `plugin.register({ name = "p", type = "hook", permissions = {"keymap", "control"} })`, []string{"keymap", "control"}},
	}
	approvals := []struct {
		name    string
		approve func(t *testing.T, source string) string // returns the plugin dir
	}{
		{"trust", func(t *testing.T, source string) string {
			pluginDir, _ := installForTest(t, "p", source)
			if err := Trust("p", true); err != nil {
				t.Fatalf("Trust: %v", err)
			}
			return pluginDir
		}},
		{"install", func(t *testing.T, source string) string {
			home := withTempHome(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(source))
			}))
			t.Cleanup(srv.Close)
			installTestClient(t, srv.URL)
			if err := Install(srv.URL+"/p.lua", true); err != nil {
				t.Fatalf("Install: %v", err)
			}
			return filepath.Join(home, ".config", "cliamp", "plugins")
		}},
	}
	for _, approval := range approvals {
		for _, tt := range tests {
			t.Run(approval.name+"/"+tt.name, func(t *testing.T) {
				silenceOutput(t)
				pluginDir := approval.approve(t, tt.source)
				m, err := plugintrust.Load(pluginDir)
				if err != nil {
					t.Fatal(err)
				}
				got, ok := m.Permissions["p"]
				if !ok || !slices.Equal(got, tt.want) {
					t.Errorf("recorded permissions = %v, %v, want %v", got, ok, tt.want)
				}
			})
		}
	}
}
