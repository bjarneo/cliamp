package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShowAlbumHeadersOptional(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ShowAlbumHeadersSet {
		t.Fatal("ShowAlbumHeadersSet = true without key, want false")
	}

	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("show_album_headers = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.ShowAlbumHeadersSet || cfg.ShowAlbumHeaders {
		t.Fatalf("show_album_headers = false: set=%v headers=%v", cfg.ShowAlbumHeadersSet, cfg.ShowAlbumHeaders)
	}
}
