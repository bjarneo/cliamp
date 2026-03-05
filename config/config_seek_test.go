package config

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestLoadSeekLargeStepSec(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("seek_large_step_sec = 42\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SeekStepLarge != 42 {
		t.Fatalf("SeekStepLarge = %d, want 42", cfg.SeekStepLarge)
	}
}

func TestLoadSeekLargeStepSecClamp(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{name: "clamps low to minimum large step", in: 0, want: 6},
		{name: "clamps five seconds to minimum large step", in: 5, want: 6},
		{name: "clamps high", in: 999, want: 600},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())

			path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			data := []byte("seek_large_step_sec = " + strconv.Itoa(tt.in) + "\n")
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.SeekStepLarge != tt.want {
				t.Fatalf("SeekStepLarge = %d, want %d", cfg.SeekStepLarge, tt.want)
			}
		})
	}
}

func TestSeekStepLargeDuration(t *testing.T) {
	cfg := Config{SeekStepLarge: 45}
	if got, want := cfg.SeekStepLargeDuration(), 45*time.Second; got != want {
		t.Fatalf("SeekStepLargeDuration = %v, want %v", got, want)
	}
}
