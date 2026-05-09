package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadNetEase(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		tomlContent string
		wantEnabled bool
		wantIsSet   bool
		wantCookies string
		wantUserID  string
	}{
		{
			name: "disabled by default",
		},
		{
			name: "enabled with explicit values",
			tomlContent: `[netease]
enabled = true
cookies_from = "chrome"
user_id = "42"
`,
			wantEnabled: true,
			wantIsSet:   true,
			wantCookies: "chrome",
			wantUserID:  "42",
		},
		{
			name: "cookies_from interpolated from env",
			env:  map[string]string{"NETEASE_BROWSER": "chrome"},
			tomlContent: `[netease]
enabled = true
cookies_from = "$NETEASE_BROWSER"
`,
			wantEnabled: true,
			wantIsSet:   true,
			wantCookies: "chrome",
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
			if cfg.NetEase.Enabled != tc.wantEnabled {
				t.Errorf("NetEase.Enabled = %v, want %v", cfg.NetEase.Enabled, tc.wantEnabled)
			}
			if cfg.NetEase.IsSet() != tc.wantIsSet {
				t.Errorf("NetEase.IsSet() = %v, want %v", cfg.NetEase.IsSet(), tc.wantIsSet)
			}
			if cfg.NetEase.CookiesFrom != tc.wantCookies {
				t.Errorf("CookiesFrom = %q, want %q", cfg.NetEase.CookiesFrom, tc.wantCookies)
			}
			if cfg.NetEase.UserID != tc.wantUserID {
				t.Errorf("UserID = %q, want %q", cfg.NetEase.UserID, tc.wantUserID)
			}
		})
	}
}
