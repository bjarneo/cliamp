package pluginmgr

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if got.name != "hello" {
		t.Errorf("name = %q, want hello", got.name)
	}
	if got.version != "1.2" {
		t.Errorf("version = %q, want 1.2", got.version)
	}
	if got.description != "says hi" {
		t.Errorf("description = %q, want 'says hi'", got.description)
	}
	if got.typ != "visualizer" {
		t.Errorf("typ = %q, want visualizer", got.typ)
	}
	if got.file != "hello.lua" {
		t.Errorf("file = %q, want hello.lua", got.file)
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
	if plugins[0].name != "nameless" {
		t.Errorf("name = %q, want 'nameless' (filename fallback)", plugins[0].name)
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
	if plugins[0].file != "myplug/" {
		t.Errorf("file = %q, want 'myplug/'", plugins[0].file)
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
		_, _ = w.Write([]byte(`plugin.register({ name = "example", version = "1" })`))
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
