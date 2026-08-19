package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAllIncludesBuiltinThemes(t *testing.T) {
	// Point HOME somewhere empty so only embedded themes load.
	t.Setenv("HOME", t.TempDir())

	themes := LoadAll()
	if len(themes) == 0 {
		t.Fatal("LoadAll() returned no themes, expected built-in set")
	}

	// Check a well-known theme is present (dracula ships with the project).
	var hasDracula bool
	for _, th := range themes {
		if th.Name == "dracula" {
			hasDracula = true
			if th.Accent == "" {
				t.Error("dracula theme has empty Accent — embed/parse failed")
			}
			break
		}
	}
	if !hasDracula {
		t.Error("built-in themes missing dracula")
	}
}

func TestLoadAllIncludesWinampPalette(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	want := Theme{
		Name:     "winamp",
		BG:       "#000000",
		Accent:   "#00FF00",
		BrightFG: "#FFFFFF",
		FG:       "#969696",
		Green:    "#29CE10",
		Yellow:   "#D6B521",
		Red:      "#EF3110",
	}

	for _, th := range LoadAll() {
		if th.Name == want.Name {
			if th != want {
				t.Errorf("winamp theme = %+v, want %+v", th, want)
			}
			return
		}
	}
	t.Fatal("built-in themes missing winamp")
}

func TestLoadAllSortedCaseInsensitive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	themes := LoadAll()
	for i := 1; i < len(themes); i++ {
		a := strings.ToLower(themes[i-1].Name)
		b := strings.ToLower(themes[i].Name)
		if a > b {
			t.Errorf("themes not sorted: %q before %q", themes[i-1].Name, themes[i].Name)
		}
	}
}

func TestLoadAllUserThemeOverridesBuiltin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Put a user override file named "dracula.toml" with a distinctive accent color.
	userDir := filepath.Join(home, ".config", "cliamp", "themes")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	overridden := `accent = "#ff00ff"
bright_fg = "#f8f8f2"
fg = "#123456"
green = "#50fa7b"
yellow = "#f1fa8c"
red = "#ff5555"
`
	if err := os.WriteFile(filepath.Join(userDir, "dracula.toml"), []byte(overridden), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	themes := LoadAll()
	var got Theme
	for _, th := range themes {
		if strings.EqualFold(th.Name, "dracula") {
			got = th
			break
		}
	}
	if got.Name == "" {
		t.Fatal("dracula theme not present after override")
	}
	if got.Accent != "#ff00ff" {
		t.Errorf("Accent = %q, want #ff00ff (user override)", got.Accent)
	}
	if got.FG != "#123456" {
		t.Errorf("FG = %q, want #123456 (user override)", got.FG)
	}
}

func TestLoadAllAddsUserOnlyTheme(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	userDir := filepath.Join(home, ".config", "cliamp", "themes")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	custom := `accent = "#abcdef"
bright_fg = "#ffffff"
fg = "#112233"
green = "#44aa55"
yellow = "#ddcc44"
red = "#cc4455"
`
	if err := os.WriteFile(filepath.Join(userDir, "mytheme.toml"), []byte(custom), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	themes := LoadAll()
	var found bool
	for _, th := range themes {
		if th.Name == "mytheme" {
			found = true
			if th.Accent != "#abcdef" {
				t.Errorf("Accent = %q, want #abcdef", th.Accent)
			}
		}
	}
	if !found {
		t.Error("user theme mytheme not loaded")
	}
}

func TestLoadAllIgnoresInvalidUserTheme(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	userDir := filepath.Join(home, ".config", "cliamp", "themes")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "broken.toml"), []byte(`accent = "blue"`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	for _, th := range LoadAll() {
		if th.Name == "broken" {
			t.Fatal("invalid custom theme was loaded")
		}
	}
}

func TestBuiltinThemesKeepStateColorsDistinct(t *testing.T) {
	for _, th := range LoadAll() {
		if th.IsDefault() {
			continue
		}
		if err := th.Validate(); err != nil {
			t.Fatalf("built-in theme %q is invalid: %v", th.Name, err)
		}
		for _, state := range []struct {
			name  string
			color string
		}{
			{"selection", th.Accent},
			{"focus", th.BrightFG},
			{"warning", th.Yellow},
			{"error", th.Red},
		} {
			if state.color == th.FG {
				t.Errorf("theme %q %s color matches disabled text", th.Name, state.name)
			}
		}
	}
}

func TestLoadAllIgnoresNonTomlFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	userDir := filepath.Join(home, ".config", "cliamp", "themes")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "notatheme.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Subdirectory should also be ignored.
	if err := os.MkdirAll(filepath.Join(userDir, "nested"), 0o755); err != nil {
		t.Fatalf("MkdirAll nested: %v", err)
	}

	themes := LoadAll()
	for _, th := range themes {
		if th.Name == "notatheme" || th.Name == "nested" {
			t.Errorf("non-toml entry %q leaked into LoadAll()", th.Name)
		}
	}
}

func TestLoadAllMissingUserDir(t *testing.T) {
	// HOME points at a dir where ~/.config/cliamp/themes doesn't exist.
	t.Setenv("HOME", t.TempDir())
	themes := LoadAll()
	if len(themes) == 0 {
		t.Error("LoadAll() with missing user dir should still return built-in themes")
	}
}
