package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadYandex(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		tomlContent string
		wantEnabled bool
		wantIsSet   bool
		wantToken   string
	}{
		{
			name: "disabled by default",
		},
		{
			name: "enabled without token is not set",
			tomlContent: `[yandex]
enabled = true
`,
			wantEnabled: true,
			wantIsSet:   false,
		},
		{
			name: "enabled with token",
			tomlContent: `[yandex]
enabled = true
token = "y0_AgAAAABC123"
`,
			wantEnabled: true,
			wantIsSet:   true,
			wantToken:   "y0_AgAAAABC123",
		},
		{
			name: "token interpolated from env",
			env:  map[string]string{"YANDEX_TOKEN": "y0_from_env"},
			tomlContent: `[yandex]
enabled = true
token = "$YANDEX_TOKEN"
`,
			wantEnabled: true,
			wantIsSet:   true,
			wantToken:   "y0_from_env",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			if tc.tomlContent != "" {
				configDir := filepath.Join(dir, ".config", "cliamp")
				if err := os.MkdirAll(configDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(tc.tomlContent), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.Yandex.Enabled != tc.wantEnabled {
				t.Errorf("Yandex.Enabled = %v, want %v", cfg.Yandex.Enabled, tc.wantEnabled)
			}
			if cfg.Yandex.IsSet() != tc.wantIsSet {
				t.Errorf("Yandex.IsSet() = %v, want %v", cfg.Yandex.IsSet(), tc.wantIsSet)
			}
			if cfg.Yandex.Token != tc.wantToken {
				t.Errorf("Token = %q, want %q", cfg.Yandex.Token, tc.wantToken)
			}
		})
	}
}
