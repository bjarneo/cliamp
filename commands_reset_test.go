package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestProviderResetCommands(t *testing.T) {
	tests := []struct {
		command string
		file    string
	}{
		{command: "spotify", file: "spotify_credentials.json"},
		{command: "qobuz", file: "qobuz_credentials.json"},
		{command: "tidal", file: "tidal_credentials.json"},
		{command: "ytmusic", file: "ytmusic_credentials.json"},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CLIAMP_CONFIG_DIR", dir)
			path := filepath.Join(dir, tt.file)
			if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}

			args := []string{"cliamp", tt.command, "reset"}
			if err := buildApp().Run(t.Context(), args); err != nil {
				t.Fatalf("%s reset: %v", tt.command, err)
			}
			if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%s still exists after reset: stat error = %v", tt.file, err)
			}
			// A second reset finds no file and still succeeds.
			if err := buildApp().Run(t.Context(), args); err != nil {
				t.Errorf("%s reset without a file: %v", tt.command, err)
			}
		})
	}
}
