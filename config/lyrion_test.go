package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLyrionConfigIsSet(t *testing.T) {
	tests := []struct {
		name string
		cfg  LyrionConfig
		want bool
	}{
		{"url only", LyrionConfig{URL: "http://nas.local:9000"}, true},
		{"url with credentials", LyrionConfig{URL: "http://nas.local:9000", User: "bob", Password: "pw"}, true},
		{"empty", LyrionConfig{}, false},
		{"credentials without url", LyrionConfig{User: "bob", Password: "pw"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.IsSet(); got != tt.want {
				t.Errorf("IsSet() = %v, want %v", got, tt.want)
			}
		})
	}
}

// writeConfig writes data as the user's config.toml inside a temporary HOME.
func writeLyrionConfig(t *testing.T, data string) Config {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return cfg
}

func TestLoadLyrionSection(t *testing.T) {
	t.Setenv("CLIAMP_TEST_LYRION_PASS", "s3cret!")

	cfg := writeLyrionConfig(t, `
[lyrion]
url = "http://nas.local:9000"
user = "bob"
password = "${CLIAMP_TEST_LYRION_PASS}"
`)

	if cfg.Lyrion.URL != "http://nas.local:9000" {
		t.Errorf("URL = %q", cfg.Lyrion.URL)
	}
	if cfg.Lyrion.User != "bob" {
		t.Errorf("User = %q", cfg.Lyrion.User)
	}
	// The shared $VAR interpolation keeps the password out of the file.
	if cfg.Lyrion.Password != "s3cret!" {
		t.Errorf("Password = %q, want interpolated value", cfg.Lyrion.Password)
	}
	if !cfg.Lyrion.IsSet() {
		t.Error("IsSet() = false for a fully configured section")
	}
}

func TestLoadLyrionShowUnplayable(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want bool
	}{
		{
			name: "omitted defaults to hidden",
			toml: "[lyrion]\nurl = \"http://nas.local:9000\"\n",
			want: false,
		},
		{
			name: "explicitly enabled",
			toml: "[lyrion]\nurl = \"http://nas.local:9000\"\nshow_unplayable = true\n",
			want: true,
		},
		{
			name: "explicitly disabled",
			toml: "[lyrion]\nurl = \"http://nas.local:9000\"\nshow_unplayable = false\n",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := writeLyrionConfig(t, tt.toml)
			if cfg.Lyrion.ShowUnplayable != tt.want {
				t.Errorf("ShowUnplayable = %v, want %v", cfg.Lyrion.ShowUnplayable, tt.want)
			}
		})
	}
}

func TestLoadLyrionAbsent(t *testing.T) {
	cfg := writeLyrionConfig(t, "theme = \"\"\n")
	if cfg.Lyrion.IsSet() {
		t.Error("IsSet() = true with no [lyrion] section")
	}
}
