package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLoadMixcloudConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLIAMP_TEST_MIXCLOUD_TOKEN", "secret-token")
	path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`
[mixcloud]
enabled = true
username = "alice"
access_token = "${CLIAMP_TEST_MIXCLOUD_TOKEN}"
cookies_from = "firefox"
styles = ["ambient", "deep-house"]
max_items = 75
stream_creators = 15
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Mixcloud.IsSet() || cfg.Mixcloud.Username != "alice" || cfg.Mixcloud.AccessToken != "secret-token" || cfg.Mixcloud.CookiesFrom != "firefox" {
		t.Fatalf("Mixcloud config = %+v", cfg.Mixcloud)
	}
	if !slices.Equal(cfg.Mixcloud.Styles, []string{"ambient", "deep-house"}) || cfg.Mixcloud.MaxItems != 75 || cfg.Mixcloud.StreamCreators != 15 {
		t.Fatalf("Mixcloud limits/styles = %+v", cfg.Mixcloud)
	}
	if !cfg.Mixcloud.StylesSet {
		t.Fatal("explicit Mixcloud styles were not distinguished from defaults")
	}
}

func TestLoadExplicitEmptyMixcloudStyles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[mixcloud]\nenabled = true\nstyles = []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Mixcloud.StylesSet || len(cfg.Mixcloud.Styles) != 0 {
		t.Fatalf("explicit empty styles = %+v", cfg.Mixcloud)
	}
}

func TestMixcloudDisabledByDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mixcloud.IsSet() {
		t.Fatal("Mixcloud should be opt-in")
	}
}
