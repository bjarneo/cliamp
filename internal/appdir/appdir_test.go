package appdir

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDir verifies config dir resolution across environment combinations.
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
		{
			// An explicitly customized HOME keeps working as before, so
			// existing tests (which point HOME at a temp dir) and users
			// who overrode HOME are unaffected by the APPDATA preference.
			name:        "custom home on windows wins over appdata",
			windowsOnly: true,
			env:         map[string]string{"CLIAMP_CONFIG_DIR": "", "XDG_CONFIG_HOME": "", "HOME": "/some/custom/home", "APPDATA": "TEMPDIR"},
			want:        func(tmp string) string { return filepath.Join("/some/custom/home", ".config", "cliamp") },
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

// TestResolveWindowsDir covers the Windows selection logic directly (pure
// function, runs on every OS): a default HOME loses to APPDATA, a customized
// HOME falls through, and the legacy location is used on upgrade while the
// APPDATA location has no config yet.
func TestResolveWindowsDir(t *testing.T) {
	writeConfig := func(t *testing.T, dir string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[plugins]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("default home resolves to appdata", func(t *testing.T) {
		appData := t.TempDir()
		userHome := t.TempDir()
		got, ok := resolveWindowsDir(appData, userHome, true, userHome)
		if !ok {
			t.Fatal("expected ok=true for a default HOME")
		}
		if want := filepath.Join(appData, "cliamp"); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("unset home resolves to appdata", func(t *testing.T) {
		appData := t.TempDir()
		got, ok := resolveWindowsDir(appData, "", false, t.TempDir())
		if !ok {
			t.Fatal("expected ok=true for an unset HOME")
		}
		if want := filepath.Join(appData, "cliamp"); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("custom home falls through", func(t *testing.T) {
		got, ok := resolveWindowsDir(t.TempDir(), t.TempDir(), true, t.TempDir())
		if ok {
			t.Fatalf("expected ok=false for a customized HOME, got %q", got)
		}
	})

	t.Run("legacy fallback while appdata has no config", func(t *testing.T) {
		appData := t.TempDir()
		userHome := t.TempDir()
		legacy := filepath.Join(userHome, ".config", "cliamp")
		writeConfig(t, legacy)
		got, ok := resolveWindowsDir(appData, userHome, true, userHome)
		if !ok {
			t.Fatal("expected ok=true for the legacy fallback")
		}
		if got != legacy {
			t.Fatalf("got %q, want legacy %q", got, legacy)
		}
	})

	t.Run("appdata wins once it has a config", func(t *testing.T) {
		appData := t.TempDir()
		userHome := t.TempDir()
		writeConfig(t, filepath.Join(userHome, ".config", "cliamp"))
		writeConfig(t, filepath.Join(appData, "cliamp"))
		got, ok := resolveWindowsDir(appData, userHome, true, userHome)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if want := filepath.Join(appData, "cliamp"); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("fresh install resolves to appdata", func(t *testing.T) {
		appData := t.TempDir()
		userHome := t.TempDir()
		got, ok := resolveWindowsDir(appData, userHome, true, userHome)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if want := filepath.Join(appData, "cliamp"); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("equivalent home spelling resolves to appdata", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Skip("Windows path semantics")
		}
		appData := t.TempDir()
		got, ok := resolveWindowsDir(appData, `c:/users/ann`, true, `C:\Users\Ann`)
		if !ok {
			t.Fatal("expected ok=true for an equivalent HOME spelling")
		}
		if want := filepath.Join(appData, "cliamp"); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
}

// TestLegacyResourcesWithoutConfig verifies that persisted legacy user state
// under HOME/.config/cliamp (custom radio stations, favorites, playlists,
// themes, plugins, credentials, etc.) keeps the legacy dir selected even when
// config.toml is absent, while runtime logs or empty dirs alone do not.
func TestLegacyResourcesWithoutConfig(t *testing.T) {
	writeFile := func(t *testing.T, path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	resourceCases := []struct {
		name    string
		relPath string
	}{
		{name: "radios.toml", relPath: "radios.toml"},
		{name: "radio_favorites.toml", relPath: "radio_favorites.toml"},
		{name: "radio_countries.toml", relPath: "radio_countries.toml"},
		{name: "favorites.toml", relPath: "favorites.toml"},
		{name: "history.toml", relPath: "history.toml"},
		{name: "podcast_subscriptions.json", relPath: "podcast_subscriptions.json"},
		{name: "spotify_credentials.json", relPath: "spotify_credentials.json"},
		{name: "resume.json", relPath: "resume.json"},
		{name: "playlist file", relPath: filepath.Join("playlists", "chill.toml")},
		{name: "plugin file", relPath: filepath.Join("plugins", "custom.lua")},
		{name: "theme file", relPath: filepath.Join("themes", "custom.toml")},
	}

	for _, tc := range resourceCases {
		t.Run("preserves legacy "+tc.name, func(t *testing.T) {
			root := t.TempDir()
			profile := filepath.Join(root, "profile")
			legacy := filepath.Join(profile, ".config", "cliamp")
			appData := filepath.Join(root, "AppData", "Roaming")
			writeFile(t, filepath.Join(legacy, tc.relPath), "# persisted\n")

			got, ok := resolveWindowsDir(appData, profile, true, profile)
			if !ok {
				t.Fatal("expected ok=true")
			}
			if got != legacy {
				t.Fatalf("selected %q; expected legacy %q", got, legacy)
			}
		})
	}

	t.Run("runtime logs and empty dirs in appdata do not hide legacy radios", func(t *testing.T) {
		root := t.TempDir()
		profile := filepath.Join(root, "profile")
		legacy := filepath.Join(profile, ".config", "cliamp")
		appData := filepath.Join(root, "AppData", "Roaming")
		appDir := filepath.Join(appData, "cliamp")

		writeFile(t, filepath.Join(legacy, "radios.toml"), "# custom stations\n")
		writeFile(t, filepath.Join(appDir, "cliamp.log"), "started\n")
		writeFile(t, filepath.Join(appDir, "plugins.log"), "loaded\n")
		if err := os.MkdirAll(filepath.Join(appDir, "plugins"), 0o700); err != nil {
			t.Fatal(err)
		}

		got, ok := resolveWindowsDir(appData, profile, true, profile)
		if !ok || got != legacy {
			t.Fatalf("resolveWindowsDir() = (%q, %v), want (%q, true)", got, ok, legacy)
		}
	})

	t.Run("logs and empty subdirs alone in legacy do not keep legacy", func(t *testing.T) {
		root := t.TempDir()
		profile := filepath.Join(root, "profile")
		legacy := filepath.Join(profile, ".config", "cliamp")
		appData := filepath.Join(root, "AppData", "Roaming")

		writeFile(t, filepath.Join(legacy, "cliamp.log"), "old log\n")
		if err := os.MkdirAll(filepath.Join(legacy, "themes"), 0o700); err != nil {
			t.Fatal(err)
		}

		got, ok := resolveWindowsDir(appData, profile, true, profile)
		want := filepath.Join(appData, "cliamp")
		if !ok || got != want {
			t.Fatalf("resolveWindowsDir() = (%q, %v), want (%q, true)", got, ok, want)
		}
	})

	t.Run("appdata wins once it holds persisted resources", func(t *testing.T) {
		root := t.TempDir()
		profile := filepath.Join(root, "profile")
		legacy := filepath.Join(profile, ".config", "cliamp")
		appData := filepath.Join(root, "AppData", "Roaming")
		appDir := filepath.Join(appData, "cliamp")

		writeFile(t, filepath.Join(legacy, "radios.toml"), "# old stations\n")
		writeFile(t, filepath.Join(appDir, "radios.toml"), "# migrated stations\n")

		got, ok := resolveWindowsDir(appData, profile, true, profile)
		if !ok || got != appDir {
			t.Fatalf("resolveWindowsDir() = (%q, %v), want (%q, true)", got, ok, appDir)
		}
	})
}

// TestSameHomeDirEquivalentSpellings checks that Windows home paths differing
// only in case or separators count as the same directory, so a daemon with no
// HOME and a child with an equivalent HOME spelling agree on the config dir.
func TestSameHomeDirEquivalentSpellings(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path semantics")
	}
	pairs := [][2]string{
		{`C:\Users\Ann`, `c:/users/ann`},
		{`C:\Users\Ann\`, `C:/Users/Ann`},
	}
	for _, p := range pairs {
		if !sameHomeDir(p[0], p[1]) {
			t.Errorf("sameHomeDir(%q, %q) = false, want true", p[0], p[1])
		}
	}
	if sameHomeDir(`C:\Users\Ann`, `C:\Users\Bob`) {
		t.Error("sameHomeDir of different dirs = true, want false")
	}
	if sameHomeDir("", `C:\Users\Ann`) {
		t.Error("sameHomeDir with empty home = true, want false")
	}
	// Same existing directory through different spellings: exercises the
	// filesystem-identity comparison, not just the lexical fallback.
	dir := t.TempDir()
	alias := strings.ToUpper(filepath.ToSlash(dir))
	if alias == dir {
		alias = strings.ToLower(dir)
	}
	if !sameHomeDir(dir, alias) {
		t.Errorf("sameHomeDir(%q, %q) = false, want true", dir, alias)
	}
}

// TestDirWindowsDefaultHomeUsesAppdata checks Dir end to end for the case
// that matters in production: HOME mirrors the profile dir (Git Bash/MSYS
// default, or the HOME synthesized for plugin children), so APPDATA wins.
// The APPDATA config is created so the legacy fallback cannot interfere.
func TestDirWindowsDefaultHomeUsesAppdata(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific resolution")
	}
	userHome, err := os.UserHomeDir()
	if err != nil || userHome == "" {
		t.Skip("no home directory available")
	}
	appData := t.TempDir()
	appDir := filepath.Join(appData, "cliamp")
	if err := os.MkdirAll(appDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "config.toml"), []byte("[plugins]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLIAMP_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("APPDATA", appData)
	t.Setenv("HOME", userHome)

	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir() error: %v", err)
	}
	if got != appDir {
		t.Fatalf("Dir() = %q, want %q", got, appDir)
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
