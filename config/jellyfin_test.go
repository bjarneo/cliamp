package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadJellyfinSection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	data := []byte(`
[jellyfin]
url = "https://jellyfin.example.com"
token = "abc123"
user = "finamp"
password = "1qazxsw2"
user_id = "user-42"
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Jellyfin.URL != "https://jellyfin.example.com" {
		t.Fatalf("Jellyfin.URL = %q, want https://jellyfin.example.com", cfg.Jellyfin.URL)
	}
	if cfg.Jellyfin.Token != "abc123" {
		t.Fatalf("Jellyfin.Token = %q, want abc123", cfg.Jellyfin.Token)
	}
	if cfg.Jellyfin.User != "finamp" {
		t.Fatalf("Jellyfin.User = %q, want finamp", cfg.Jellyfin.User)
	}
	if cfg.Jellyfin.Password != "1qazxsw2" {
		t.Fatalf("Jellyfin.Password = %q, want 1qazxsw2", cfg.Jellyfin.Password)
	}
	if cfg.Jellyfin.UserID != "user-42" {
		t.Fatalf("Jellyfin.UserID = %q, want user-42", cfg.Jellyfin.UserID)
	}
	if !cfg.Jellyfin.IsSet() {
		t.Fatal("Jellyfin.IsSet() = false, want true")
	}
}
