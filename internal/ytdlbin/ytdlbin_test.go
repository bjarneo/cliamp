package ytdlbin

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestName(t *testing.T) {
	home := t.TempDir()

	tests := []struct {
		name       string
		env        string
		configured string
		want       string
	}{
		{name: "default is PATH lookup", want: DefaultName},
		{name: "configured path wins over default", configured: "/opt/yt-dlp", want: "/opt/yt-dlp"},
		{name: "env wins over configured path", env: "/usr/local/bin/yt-dlp", configured: "/opt/yt-dlp", want: "/usr/local/bin/yt-dlp"},
		{name: "blank config is ignored", configured: "   ", want: DefaultName},
		{name: "blank env falls back to config", env: "  ", configured: "/opt/yt-dlp", want: "/opt/yt-dlp"},
		{name: "surrounding space is trimmed", configured: " /opt/yt-dlp ", want: "/opt/yt-dlp"},
		{name: "tilde in config expands to home", configured: "~/.local/bin/yt-dlp", want: filepath.Join(home, ".local/bin/yt-dlp")},
		{name: "tilde in env expands to home", env: "~/bin/yt-dlp", want: filepath.Join(home, "bin/yt-dlp")},
		{name: "bare tilde expands to home", configured: "~", want: home},
		{name: "tilde user is left untouched", configured: "~other/yt-dlp", want: "~other/yt-dlp"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", home)
			if runtime.GOOS == "windows" {
				t.Setenv("USERPROFILE", home)
			}
			t.Setenv(EnvVar, tt.env)
			Configure(tt.configured)
			t.Cleanup(func() { Configure("") })

			if got := Name(); got != tt.want {
				t.Fatalf("Name() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLookPathUsesConfiguredBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX executable bits")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "yt-dlp-next")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv(EnvVar, "")
	Configure(bin)
	t.Cleanup(func() { Configure("") })

	got, err := LookPath()
	if err != nil {
		t.Fatalf("LookPath() error = %v", err)
	}
	if got != bin {
		t.Fatalf("LookPath() = %q, want %q", got, bin)
	}
	if !Available() {
		t.Fatal("Available() = false, want true for an executable path")
	}
}

func TestLookPathMissingConfiguredBinary(t *testing.T) {
	t.Setenv(EnvVar, "")
	Configure(filepath.Join(t.TempDir(), "absent"))
	t.Cleanup(func() { Configure("") })

	if _, err := LookPath(); err == nil {
		t.Fatal("LookPath() error = nil, want error for a missing binary")
	}
	if Available() {
		t.Fatal("Available() = true, want false for a missing binary")
	}
}

func TestCommandUsesResolvedName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec resolves .exe extensions on Windows")
	}
	t.Setenv(EnvVar, "/opt/yt-dlp")
	Configure("")

	if got := Command("--version").Path; got != "/opt/yt-dlp" {
		t.Fatalf("Command().Path = %q, want /opt/yt-dlp", got)
	}
	if got := CommandContext(t.Context(), "--version").Path; got != "/opt/yt-dlp" {
		t.Fatalf("CommandContext().Path = %q, want /opt/yt-dlp", got)
	}
}
