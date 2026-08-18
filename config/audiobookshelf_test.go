package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAudiobookshelfSection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	data := []byte(`
[audiobookshelf]
url = "https://abs.example.com"
token = "abc123"
user = "listener"
password = "1qazxsw2"
libraries = ["Audiobooks", "Podcasts"]
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	abs := cfg.Audiobookshelf
	if abs.URL != "https://abs.example.com" {
		t.Fatalf("URL = %q, want https://abs.example.com", abs.URL)
	}
	if abs.Token != "abc123" {
		t.Fatalf("Token = %q, want abc123", abs.Token)
	}
	if abs.User != "listener" {
		t.Fatalf("User = %q, want listener", abs.User)
	}
	if abs.Password != "1qazxsw2" {
		t.Fatalf("Password = %q, want 1qazxsw2", abs.Password)
	}
	if len(abs.Libraries) != 2 || abs.Libraries[0] != "Audiobooks" || abs.Libraries[1] != "Podcasts" {
		t.Fatalf("Libraries = %v, want [Audiobooks Podcasts]", abs.Libraries)
	}
	if !abs.IsSet() {
		t.Fatal("IsSet() = false, want true")
	}
}

func TestAudiobookshelfIsSet(t *testing.T) {
	tests := []struct {
		name string
		cfg  AudiobookshelfConfig
		want bool
	}{
		{name: "empty", cfg: AudiobookshelfConfig{}, want: false},
		{name: "url only", cfg: AudiobookshelfConfig{URL: "https://abs"}, want: false},
		{name: "token only", cfg: AudiobookshelfConfig{Token: "t"}, want: false},
		{name: "url and token", cfg: AudiobookshelfConfig{URL: "https://abs", Token: "t"}, want: true},
		{name: "url and user only", cfg: AudiobookshelfConfig{URL: "https://abs", User: "u"}, want: false},
		{name: "url and credentials", cfg: AudiobookshelfConfig{URL: "https://abs", User: "u", Password: "p"}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.IsSet(); got != tt.want {
				t.Fatalf("IsSet() = %v, want %v", got, tt.want)
			}
		})
	}
}
