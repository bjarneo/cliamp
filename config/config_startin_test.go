package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadStartIn(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{name: "unset keeps the provider view", line: "", want: ""},
		{name: "provider is the default", line: `start_in = "provider"`, want: ""},
		{name: "home keeps the empty playlist screen", line: `start_in = "home"`, want: "home"},
		{name: "files opens the file browser", line: `start_in = 'files'`, want: "files"},
		{name: "case is ignored", line: `start_in = "FILES"`, want: "files"},
		{name: "an unknown value falls back to the provider view", line: `start_in = "nope"`, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())

			path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.WriteFile(path, []byte(tt.line+"\n"), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.StartIn != tt.want {
				t.Fatalf("StartIn = %q, want %q", cfg.StartIn, tt.want)
			}
		})
	}
}

func TestOverridesApplyStartIn(t *testing.T) {
	cfg := defaultConfig()
	startIn := "files"
	Overrides{StartIn: &startIn}.Apply(&cfg)
	if cfg.StartIn != "files" {
		t.Fatalf("StartIn = %q, want files", cfg.StartIn)
	}

	provider := "provider"
	Overrides{StartIn: &provider}.Apply(&cfg)
	if cfg.StartIn != "" {
		t.Fatalf("StartIn = %q, want empty (provider default)", cfg.StartIn)
	}

	invalid := "nope"
	Overrides{StartIn: &invalid}.Apply(&cfg)
	if cfg.StartIn != "" {
		t.Fatalf("StartIn = %q, want empty after clamp", cfg.StartIn)
	}
}
