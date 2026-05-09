package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadNetEaseDisabledByDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.NetEase.Enabled {
		t.Error("NetEase.Enabled = true with no config, want false")
	}
	if cfg.NetEase.IsSet() {
		t.Error("NetEase.IsSet() = true with no config, want false")
	}
}

func TestLoadNetEaseConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	configDir := filepath.Join(dir, ".config", "cliamp")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`[netease]
enabled = true
cookies_from = "chrome"
user_id = "42"
`)
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.NetEase.Enabled {
		t.Error("NetEase.Enabled = false, want true")
	}
	if cfg.NetEase.CookiesFrom != "chrome" {
		t.Fatalf("CookiesFrom = %q, want chrome", cfg.NetEase.CookiesFrom)
	}
	if cfg.NetEase.UserID != "42" {
		t.Fatalf("UserID = %q, want 42", cfg.NetEase.UserID)
	}
}

func TestLoadNetEaseCookiesFromEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("NETEASE_BROWSER", "chrome")
	configDir := filepath.Join(dir, ".config", "cliamp")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`[netease]
enabled = true
cookies_from = "$NETEASE_BROWSER"
`)
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.NetEase.CookiesFrom != "chrome" {
		t.Fatalf("CookiesFrom = %q, want chrome", cfg.NetEase.CookiesFrom)
	}
}
