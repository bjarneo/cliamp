package appdir

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDir(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		want        func(tempDir string) string
		windowsOnly bool
	}{
		{
			name: "home config",
			env:  map[string]string{"CLIAMP_CONFIG_DIR": "", "XDG_CONFIG_HOME": "", "APPDATA": "", "HOME": "TEMPDIR"},
			want: func(tmp string) string { return filepath.Join(tmp, ".config", "cliamp") },
		},
		{
			name: "xdg config",
			env:  map[string]string{"CLIAMP_CONFIG_DIR": "", "HOME": "", "APPDATA": "", "XDG_CONFIG_HOME": "TEMPDIR"},
			want: func(tmp string) string { return filepath.Join(tmp, "cliamp") },
		},
		{
			name:        "appdata on windows when home missing",
			windowsOnly: true,
			env:         map[string]string{"CLIAMP_CONFIG_DIR": "", "XDG_CONFIG_HOME": "", "HOME": "", "APPDATA": "TEMPDIR"},
			want:        func(tmp string) string { return filepath.Join(tmp, "cliamp") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.windowsOnly && runtime.GOOS != "windows" {
				t.Skip("Windows-specific fallback")
			}
			var tempDir string
			for k, v := range tt.env {
				if v == "TEMPDIR" {
					tempDir = t.TempDir()
					t.Setenv(k, tempDir)
				} else {
					t.Setenv(k, v)
				}
			}
			got, err := Dir()
			if err != nil {
				t.Fatalf("Dir() error: %v", err)
			}
			want := tt.want(tempDir)
			if got != want {
				t.Fatalf("Dir() = %q, want %q", got, want)
			}
		})
	}
}

func TestPluginDir(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("APPDATA", "")
	t.Setenv("HOME", t.TempDir())

	dir, err := PluginDir()
	if err != nil {
		t.Fatalf("PluginDir() error: %v", err)
	}

	if !strings.HasSuffix(dir, filepath.Join("cliamp", "plugins")) {
		t.Fatalf("PluginDir() = %q, expected to end with cliamp/plugins", dir)
	}
}

func TestPluginDirIsSubdirOfDir(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("APPDATA", "")
	t.Setenv("HOME", t.TempDir())

	base, _ := Dir()
	plugin, _ := PluginDir()

	if !strings.HasPrefix(plugin, base) {
		t.Fatalf("PluginDir %q should be under Dir %q", plugin, base)
	}
}
