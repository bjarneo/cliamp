package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSaveSection(t *testing.T) {
	creds := []string{"url", "user", "password"}
	tests := []struct {
		name    string
		initial string // "" means no config file
		section string
		kv      []KeyValue
		owned   []string
		want    string
	}{
		{
			name:    "missing file",
			section: "navidrome",
			kv:      []KeyValue{{"url", `"u"`}, {"user", `"a"`}},
			want:    "[navidrome]\nurl = \"u\"\nuser = \"a\"\n",
		},
		{
			name:    "missing section appended after a blank line",
			initial: "volume = -6\n\n# plex below\n\n",
			section: "plex",
			kv:      []KeyValue{{"url", `"u"`}},
			want:    "volume = -6\n\n# plex below\n\n[plex]\nurl = \"u\"\n",
		},
		{
			name:    "keys it does not own and comments survive",
			initial: "[navidrome]\n# home server\nurl = \"old\"\nbrowse_sort = \"byYear\"\nformat = \"mp3\"\n\n[plex]\nurl = \"p\"\n",
			section: "navidrome",
			kv:      []KeyValue{{"url", `"new"`}, {"user", `"a"`}, {"password", `"p"`}},
			owned:   creds,
			want:    "[navidrome]\n# home server\nurl = \"new\"\nbrowse_sort = \"byYear\"\nformat = \"mp3\"\nuser = \"a\"\npassword = \"p\"\n\n[plex]\nurl = \"p\"\n",
		},
		{
			name:    "owned key absent from kv is removed",
			initial: "[jellyfin]\nurl = \"u\"\nuser = \"a\"\npassword = \"p\"\nbrowse = \"x\"\n",
			section: "jellyfin",
			kv:      []KeyValue{{"url", `"u"`}, {"token", `"t"`}},
			owned:   []string{"url", "token", "user", "password"},
			want:    "[jellyfin]\nurl = \"u\"\nbrowse = \"x\"\ntoken = \"t\"\n",
		},
		{
			name:    "youtube alias is edited in place",
			initial: "[youtube]\ncookies_from = \"chrome\"\n",
			section: "ytmusic",
			kv:      []KeyValue{{"enabled", "true"}, {"cookies_from", `"firefox"`}},
			want:    "[youtube]\ncookies_from = \"firefox\"\nenabled = true\n",
		},
		{
			name:    "yt alias in upper case",
			initial: "[YT]\nenabled = false\n",
			section: "ytmusic",
			kv:      []KeyValue{{"enabled", "true"}},
			want:    "[YT]\nenabled = true\n",
		},
		{
			name:    "header letter case and trailing comment",
			initial: "[Navidrome] # home\nurl = \"old\"\n",
			section: "navidrome",
			kv:      []KeyValue{{"url", `"new"`}},
			want:    "[Navidrome] # home\nurl = \"new\"\n",
		},
		{
			name:    "same key in another section is untouched",
			initial: "[plex]\nurl = \"p\"\n[navidrome]\n",
			section: "navidrome",
			kv:      []KeyValue{{"url", `"n"`}},
			owned:   creds,
			want:    "[plex]\nurl = \"p\"\n[navidrome]\nurl = \"n\"\n",
		},
		{
			name:    "top-level key with the same name is untouched",
			initial: "url = \"top\"\n",
			section: "navidrome",
			kv:      []KeyValue{{"url", `"n"`}},
			owned:   creds,
			want:    "url = \"top\"\n\n[navidrome]\nurl = \"n\"\n",
		},
		{
			name:    "commented-out key is not a key",
			initial: "[navidrome]\n# url = \"example\"\n",
			section: "navidrome",
			kv:      []KeyValue{{"url", `"n"`}},
			owned:   creds,
			want:    "[navidrome]\nurl = \"n\"\n# url = \"example\"\n",
		},
		{
			name:    "comment above the next header stays with it",
			initial: "[navidrome]\nurl = \"old\"\n\n# Plex server\n[plex]\n",
			section: "navidrome",
			kv:      []KeyValue{{"browse_sort", `"random"`}},
			want:    "[navidrome]\nurl = \"old\"\nbrowse_sort = \"random\"\n\n# Plex server\n[plex]\n",
		},
		{
			name:    "every block of the section is updated",
			initial: "[ytmusic]\ncookies_from = \"a\"\n\n[youtube]\ncookies_from = \"b\"\nclient_id = \"c\"\n",
			section: "ytmusic",
			kv:      []KeyValue{{"cookies_from", `"z"`}},
			owned:   []string{"cookies_from", "client_id"},
			want:    "[ytmusic]\ncookies_from = \"z\"\n\n[youtube]\ncookies_from = \"z\"\n",
		},
		{
			name:    "key alignment is kept",
			initial: "[navidrome]\nurl      = \"old\"\nuser=\"a\"\n",
			section: "navidrome",
			kv:      []KeyValue{{"url", `"new"`}, {"user", `"b"`}},
			want:    "[navidrome]\nurl      = \"new\"\nuser = \"b\"\n",
		},
		{
			name:    "no trailing newline",
			initial: "[navidrome]\nurl = \"old\"",
			section: "navidrome",
			kv:      []KeyValue{{"browse_sort", `"random"`}},
			want:    "[navidrome]\nurl = \"old\"\nbrowse_sort = \"random\"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := withHome(t)
			if tt.initial != "" {
				writeConfig(t, home, tt.initial)
			}
			if err := SaveSection(tt.section, tt.kv, tt.owned); err != nil {
				t.Fatalf("SaveSection: %v", err)
			}
			if got := readConfig(t, home); got != tt.want {
				t.Errorf("config =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestSaveSectionRejectsLineBreaks(t *testing.T) {
	tests := []struct {
		name string
		kv   KeyValue
	}{
		{"newline in value", KeyValue{"url", "\"a\"\nshuffle = true"}},
		{"carriage return in value", KeyValue{"url", "\"a\"\r"}},
		{"newline in key", KeyValue{"url\n[plex]", `"a"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := withHome(t)
			const initial = "[navidrome]\nurl = \"old\"\n"
			writeConfig(t, home, initial)
			if err := SaveSection("navidrome", []KeyValue{tt.kv}, nil); err == nil {
				t.Fatal("SaveSection accepted a line break")
			}
			if got := readConfig(t, home); got != initial {
				t.Errorf("config changed to\n%s", got)
			}
		})
	}
}

func TestSaveSectionSecuresConfigFile(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "config")
	t.Setenv("CLIAMP_CONFIG_DIR", configDir)
	save := func() {
		t.Helper()
		if err := SaveSection("mixcloud", []KeyValue{{"access_token", `"secret"`}}, nil); err != nil {
			t.Fatalf("SaveSection: %v", err)
		}
	}
	checkMode := func(path string, want os.FileMode) {
		t.Helper()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%q): %v", path, err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("mode for %q = %o, want %o", path, got, want)
		}
	}

	save()
	if runtime.GOOS == "windows" {
		return // Windows does not expose Unix permission bits.
	}
	path := filepath.Join(configDir, "config.toml")
	checkMode(configDir, 0o700)
	checkMode(path, 0o600)

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("Chmod(%q): %v", path, err)
	}
	save()
	checkMode(path, 0o600)
}

func writeConfig(t *testing.T, home, data string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "cliamp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}
