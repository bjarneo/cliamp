package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplayGainConfigLoad(t *testing.T) {
	for _, tt := range []struct {
		name, content string
		mode          string
		preamp        float64
	}{
		{"default", "", "off", 0},
		{"track", "replaygain = \"track\"\n", "track", 0},
		{"album, any case", "replaygain = \"Album\"\n", "album", 0},
		{"unknown keeps off", "replaygain = \"loud\"\n", "off", 0},
		{"preamp", "replaygain = \"track\"\nreplaygain_preamp = 3.5\n", "track", 3.5},
		{"preamp clamps high", "replaygain_preamp = 40\n", "off", 15},
		{"preamp clamps low", "replaygain_preamp = -40\n", "off", -15},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CLIAMP_CONFIG_DIR", dir)
			if tt.content != "" {
				if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(tt.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ReplayGain != tt.mode || cfg.ReplayGainPreamp != tt.preamp {
				t.Fatalf("ReplayGain = %q, %v; want %q, %v", cfg.ReplayGain, cfg.ReplayGainPreamp, tt.mode, tt.preamp)
			}
		})
	}
}
