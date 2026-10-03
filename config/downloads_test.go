package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDownloads(t *testing.T) {
	for _, directory := range []string{"", "/media/usb/CLAPt/Music"} {
		t.Run(directory, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CLIAMP_CONFIG_DIR", dir)
			if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[downloads]\ndirectory = \""+directory+"\"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Downloads.Directory != directory {
				t.Fatalf("directory=%q", cfg.Downloads.Directory)
			}
		})
	}
}
