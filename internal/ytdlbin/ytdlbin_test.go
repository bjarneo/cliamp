package ytdlbin

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

func TestNotFoundError(t *testing.T) {
	tests := []struct {
		name       string
		env        string
		configured string
		want       string
	}{
		{name: "default lookup", want: "yt-dlp not found in PATH"},
		{name: "config selection", configured: "/opt/yt-dlp", want: "yt-dlp not found at /opt/yt-dlp (selected by ytdlp_path)"},
		{name: "env selection", env: "/opt/next/yt-dlp", want: "yt-dlp not found at /opt/next/yt-dlp (selected by " + EnvVar + ")"},
		{name: "env selection wins over config", env: "/opt/next/yt-dlp", configured: "/opt/yt-dlp", want: "yt-dlp not found at /opt/next/yt-dlp (selected by " + EnvVar + ")"},
		// A selector holding a bare name still goes through PATH, but the
		// message must not read like an unconfigured install.
		{name: "config selection of the default name", configured: DefaultName, want: "yt-dlp not found in PATH (selected by ytdlp_path)"},
		{name: "env selection of the default name", env: DefaultName, want: "yt-dlp not found in PATH (selected by " + EnvVar + ")"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvVar, tt.env)
			Configure(tt.configured)
			t.Cleanup(func() { Configure("") })

			if got := NotFoundError(nil).Error(); got != tt.want {
				t.Fatalf("NotFoundError() = %q, want %q", got, tt.want)
			}
		})
	}
}

// The guards return this error instead of the os/exec failure, so unwrapping
// has to keep working for callers matching on exec.ErrNotFound or on
// *exec.Error.
func TestNotFoundErrorUnwrapsLookupFailure(t *testing.T) {
	t.Setenv(EnvVar, "")
	Configure("")
	cause := &exec.Error{Name: DefaultName, Err: exec.ErrNotFound}

	for _, err := range []error{NotFoundError(cause), NotFoundErrorWithAdvice(cause, "install: pip install yt-dlp")} {
		if !errors.Is(err, exec.ErrNotFound) {
			t.Errorf("errors.Is(%v, exec.ErrNotFound) = false", err)
		}
		var execErr *exec.Error
		if !errors.As(err, &execErr) || execErr.Name != DefaultName {
			t.Errorf("errors.As(%v, *exec.Error) failed", err)
		}
		if strings.Contains(err.Error(), "executable file not found") {
			t.Errorf("error = %q, want exec's wording kept out of the user-facing text", err)
		}
	}
}

func TestNotFoundErrorWithAdvice(t *testing.T) {
	tests := []struct {
		name       string
		env        string
		configured string
		advice     string
		want       string
	}{
		{
			name:   "default lookup takes install advice",
			advice: "install: sudo pacman -S yt-dlp",
			want:   "yt-dlp not found in PATH — install: sudo pacman -S yt-dlp",
		},
		{
			name: "default lookup without advice",
			want: "yt-dlp not found in PATH",
		},
		{
			// Installing yt-dlp on PATH cannot fix a broken selection: the
			// selected path keeps precedence, so the advice must be dropped.
			name:       "config selection drops install advice",
			configured: "/opt/yt-dlp",
			advice:     "install: sudo pacman -S yt-dlp",
			want:       "yt-dlp not found at /opt/yt-dlp (selected by ytdlp_path)",
		},
		{
			name:   "env selection drops install advice",
			env:    "/opt/next/yt-dlp",
			advice: "see https://github.com/yt-dlp/yt-dlp#installation",
			want:   "yt-dlp not found at /opt/next/yt-dlp (selected by " + EnvVar + ")",
		},
		{
			// A selector naming the bare default still resolves through PATH,
			// so installing yt-dlp does fix it: keep the advice.
			name:   "env selection of the default name keeps install advice",
			env:    DefaultName,
			advice: "install: sudo pacman -S yt-dlp",
			want:   "yt-dlp not found in PATH (selected by " + EnvVar + ") — install: sudo pacman -S yt-dlp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvVar, tt.env)
			Configure(tt.configured)
			t.Cleanup(func() { Configure("") })

			if got := NotFoundErrorWithAdvice(nil, tt.advice).Error(); got != tt.want {
				t.Fatalf("NotFoundErrorWithAdvice(%q) = %q, want %q", tt.advice, got, tt.want)
			}
		})
	}
}
