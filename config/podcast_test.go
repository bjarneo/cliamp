package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPodcastConfig(t *testing.T) {
	for _, tt := range []struct {
		name, content, country string
	}{
		{"default", "", ""},
		{"country", "[podcast]\ncountry = \" NO \"\n", "NO"},
		{"independent radio country", "[radio]\ncountry = \"gb\"\n[podcast]\ncountry = \"us\"\n", "us"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CLIAMP_CONFIG_DIR", dir)
			if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Podcast.Country != tt.country {
				t.Errorf("podcast country = %q, want %q", cfg.Podcast.Country, tt.country)
			}
		})
	}
}
