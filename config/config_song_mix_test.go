package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSongMixSize(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   int
	}{
		{name: "default", config: "", want: 30},
		{name: "set", config: "song_mix_size = 50\n", want: 50},
		{name: "clamps low", config: "song_mix_size = 0\n", want: 1},
		{name: "clamps high", config: "song_mix_size = 500\n", want: 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())

			path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.WriteFile(path, []byte(tt.config), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.SongMixSize != tt.want {
				t.Fatalf("SongMixSize = %d, want %d", cfg.SongMixSize, tt.want)
			}
		})
	}
}
