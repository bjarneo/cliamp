package config

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestLoadVisRows(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{name: "keeps a usable height", in: 12, want: 12},
		{name: "zero keeps the built-in default", in: 0, want: 0},
		{name: "clamps negative to one row", in: -4, want: 1},
		{name: "clamps high to the maximum", in: 999, want: maxVisRows},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())

			path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			data := []byte("vis_rows = " + strconv.Itoa(tt.in) + "\n")
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.VisRows != tt.want {
				t.Fatalf("VisRows = %d, want %d", cfg.VisRows, tt.want)
			}
		})
	}
}
