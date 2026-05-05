package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSoundCloudSection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	data := []byte(`
[soundcloud]
user = "alice"
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SoundCloud.User != "alice" {
		t.Errorf("SoundCloud.User = %q, want alice", cfg.SoundCloud.User)
	}
	if cfg.SoundCloud.Disabled {
		t.Error("SoundCloud.Disabled = true, want false (default)")
	}
	if !cfg.SoundCloud.IsSet() {
		t.Error("SoundCloud.IsSet() = false, want true")
	}
}

func TestLoadSoundCloudExplicitlyDisabled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	data := []byte(`
[soundcloud]
enabled = false
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.SoundCloud.Disabled {
		t.Error("SoundCloud.Disabled = false, want true")
	}
	if cfg.SoundCloud.IsSet() {
		t.Error("SoundCloud.IsSet() = true, want false")
	}
}

func TestLoadSoundCloudCookiesFrom(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	data := []byte(`
[soundcloud]
user = "alice"
cookies_from = "firefox"
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SoundCloud.CookiesFrom != "firefox" {
		t.Errorf("SoundCloud.CookiesFrom = %q, want firefox", cfg.SoundCloud.CookiesFrom)
	}
}

func TestLoadSoundCloudInterpolatesUserFromEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLIAMP_TEST_SC_USER", "carol")

	path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	data := []byte(`
[soundcloud]
user = "${CLIAMP_TEST_SC_USER}"
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SoundCloud.User != "carol" {
		t.Errorf("SoundCloud.User = %q, want carol (from env)", cfg.SoundCloud.User)
	}
}
