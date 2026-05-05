package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseStringEnvInterpolation(t *testing.T) {
	t.Setenv("CLIAMP_TEST_VAR", "from-env")
	t.Setenv("CLIAMP_TEST_EMPTY", "")

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain quoted string", `"hello"`, "hello"},
		{"plain single-quoted", `'hello'`, "hello"},
		{"unquoted plain", `hello`, "hello"},
		{"dollar braces set", `"${CLIAMP_TEST_VAR}"`, "from-env"},
		{"dollar bare set", `"$CLIAMP_TEST_VAR"`, "from-env"},
		{"unquoted dollar braces", `${CLIAMP_TEST_VAR}`, "from-env"},
		{"unset var returns empty", `"${CLIAMP_NOT_SET_XYZ}"`, ""},
		{"empty var returns empty", `"${CLIAMP_TEST_EMPTY}"`, ""},
		{"literal dollar in middle preserved", `"p@$$w0rd"`, "p@$$w0rd"},
		{"literal dollar at start with non-name", `"$1abc"`, "$1abc"},
		{"unmatched brace left alone", `"${UNCLOSED"`, "${UNCLOSED"},
		{"only dollar", `"$"`, "$"},
		{"interpolation only on whole value", `"prefix-$CLIAMP_TEST_VAR"`, "prefix-$CLIAMP_TEST_VAR"},
		{"underscore-leading name", `"$_CLIAMP_TEST"`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseString(tt.in)
			if got != tt.want {
				t.Fatalf("parseString(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLoadInterpolatesSecretsFromEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLIAMP_TEST_NAVI_PASS", "s3cret!")
	t.Setenv("CLIAMP_TEST_PLEX_TOKEN", "tok-abc")
	t.Setenv("CLIAMP_TEST_JELLY_TOKEN", "jelly-tok")
	t.Setenv("CLIAMP_TEST_YT_SECRET", "yt-secret")

	path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data := []byte(`
[navidrome]
url = "https://music.example.com"
user = "alice"
password = "${CLIAMP_TEST_NAVI_PASS}"

[plex]
url = "http://plex.local:32400"
token = "$CLIAMP_TEST_PLEX_TOKEN"

[jellyfin]
url = "https://jelly.example.com"
token = "${CLIAMP_TEST_JELLY_TOKEN}"

[ytmusic]
client_id = "literal-id"
client_secret = "${CLIAMP_TEST_YT_SECRET}"
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Navidrome.Password != "s3cret!" {
		t.Errorf("Navidrome.Password = %q, want %q", cfg.Navidrome.Password, "s3cret!")
	}
	if cfg.Plex.Token != "tok-abc" {
		t.Errorf("Plex.Token = %q, want %q", cfg.Plex.Token, "tok-abc")
	}
	if cfg.Jellyfin.Token != "jelly-tok" {
		t.Errorf("Jellyfin.Token = %q, want %q", cfg.Jellyfin.Token, "jelly-tok")
	}
	if cfg.YouTubeMusic.ClientID != "literal-id" {
		t.Errorf("YouTubeMusic.ClientID = %q, want %q", cfg.YouTubeMusic.ClientID, "literal-id")
	}
	if cfg.YouTubeMusic.ClientSecret != "yt-secret" {
		t.Errorf("YouTubeMusic.ClientSecret = %q, want %q", cfg.YouTubeMusic.ClientSecret, "yt-secret")
	}
}

func TestLoadPreservesLiteralDollarInPassword(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data := []byte(`
[navidrome]
url = "https://music.example.com"
user = "alice"
password = "p@$$w0rd"
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Navidrome.Password != "p@$$w0rd" {
		t.Errorf("Navidrome.Password = %q, want literal %q", cfg.Navidrome.Password, "p@$$w0rd")
	}
}

func TestLoadInterpolatesPluginSecrets(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLIAMP_TEST_LASTFM_KEY", "lastfm-abc")

	path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data := []byte(`
[plugins.lastfm]
api_key = "${CLIAMP_TEST_LASTFM_KEY}"
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got := cfg.Plugins["lastfm"]["api_key"]
	if got != "lastfm-abc" {
		t.Errorf("plugins.lastfm.api_key = %q, want %q", got, "lastfm-abc")
	}
}
