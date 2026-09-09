package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestMetadataConfigLoad(t *testing.T) {
	if defaultConfig().ShowMetadata {
		t.Fatal("ShowMetadata defaults to true, want false")
	}
	for _, tt := range []struct {
		name, content string
		want          bool
	}{
		{"missing file", "", false},
		{"unset", "volume = -9\n", false},
		{"enabled", "show_metadata = true\n", true},
		{"disabled", "show_metadata = false\n", false},
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
			if cfg.ShowMetadata != tt.want {
				t.Fatalf("ShowMetadata = %v, want %v", cfg.ShowMetadata, tt.want)
			}
		})
	}
}

func TestMetadataConfigSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	path := filepath.Join(dir, "config.toml")
	const original = "# Keep unrelated settings and formatting\nvolume = -9\nvis_rows = 12\nhide_settings_pane = true\n\n[radio]\ncountry = \"no\"\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, want := range []bool{true, false} {
		value := strconv.FormatBool(want)
		if err := Save("show_metadata", value); err != nil {
			t.Fatalf("Save(%s): %v", value, err)
		}
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ShowMetadata != want || cfg.Volume != -9 || cfg.VisRows != 12 || !cfg.HideSettingsPane || cfg.Radio.Country != "no" {
			t.Fatalf("round trip %s: metadata=%v volume=%v vis_rows=%d hide_settings_pane=%v country=%q", value, cfg.ShowMetadata, cfg.Volume, cfg.VisRows, cfg.HideSettingsPane, cfg.Radio.Country)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		line := "show_metadata = " + value + "\n"
		if strings.Count(string(data), line) != 1 || strings.Replace(string(data), line, "", 1) != original {
			t.Fatalf("Save changed unrelated content or duplicated metadata:\n%s", data)
		}
	}
}
