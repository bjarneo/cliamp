package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func withHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func readConfig(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".config", "cliamp", "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return string(data)
}

func TestSaveCreatesConfigFile(t *testing.T) {
	home := withHome(t)

	if err := save("volume", "-6"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got := readConfig(t, home)
	if !strings.Contains(got, "volume = -6") {
		t.Errorf("config = %q, want 'volume = -6' line", got)
	}
}

func TestSaveReplacesExistingKey(t *testing.T) {
	home := withHome(t)
	dir := filepath.Join(home, ".config", "cliamp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	initial := "# a comment\nvolume = -12\nspeed = 1.0\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(initial), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := save("volume", "-3"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got := readConfig(t, home)
	if !strings.Contains(got, "volume = -3") {
		t.Errorf("config = %q, want volume = -3", got)
	}
	if strings.Contains(got, "volume = -12") {
		t.Errorf("config = %q, should have replaced old volume line", got)
	}
	if !strings.Contains(got, "speed = 1.0") {
		t.Errorf("config = %q, unrelated keys must be preserved", got)
	}
}

func TestSaveInsertsBeforeFirstSection(t *testing.T) {
	home := withHome(t)
	dir := filepath.Join(home, ".config", "cliamp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	initial := "[navidrome]\nurl = \"https://ex.com\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(initial), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := save("volume", "-6"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got := readConfig(t, home)
	// volume should appear before [navidrome]
	volIdx := strings.Index(got, "volume = -6")
	navIdx := strings.Index(got, "[navidrome]")
	if volIdx < 0 {
		t.Fatalf("volume line missing: %q", got)
	}
	if navIdx < 0 || volIdx > navIdx {
		t.Errorf("volume should appear before [navidrome], got:\n%s", got)
	}
}

func TestSaveDoesNotMatchKeyInSection(t *testing.T) {
	home := withHome(t)
	dir := filepath.Join(home, ".config", "cliamp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// A [navidrome] section with a 'volume' key (shouldn't be touched by
	// the top-level Save).
	initial := "[navidrome]\nvolume = \"old\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(initial), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := save("volume", "-6"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got := readConfig(t, home)
	// The navidrome.volume line stays, and a new top-level volume is added.
	if !strings.Contains(got, `volume = "old"`) {
		t.Errorf("section-local volume should be untouched:\n%s", got)
	}
	if !strings.Contains(got, "volume = -6") {
		t.Errorf("top-level volume should be added:\n%s", got)
	}
}

// TestSaveEndsWithNewline checks that a saved key lands next to the other
// top-level keys and that the file ends with one newline.
func TestSaveEndsWithNewline(t *testing.T) {
	tests := []struct {
		name    string
		initial string
		want    string
	}{
		{
			name:    "appended with a final newline",
			initial: "shuffle = true\n",
			want:    "shuffle = true\nrepeat = \"All\"\n",
		},
		{
			name:    "appended without a final newline",
			initial: "shuffle = true",
			want:    "shuffle = true\nrepeat = \"All\"\n",
		},
		{
			name:    "appended after a kept blank line",
			initial: "shuffle = true\n\n",
			want:    "shuffle = true\n\nrepeat = \"All\"\n",
		},
		{
			name:    "appended to an empty file",
			initial: "",
			want:    "repeat = \"All\"\n",
		},
		{
			name:    "inserted before a section with a final newline",
			initial: "shuffle = true\n[radio]\ncountry = \"none\"\n",
			want:    "shuffle = true\nrepeat = \"All\"\n[radio]\ncountry = \"none\"\n",
		},
		{
			name:    "inserted before a section without a final newline",
			initial: "shuffle = true\n[radio]\ncountry = \"none\"",
			want:    "shuffle = true\nrepeat = \"All\"\n[radio]\ncountry = \"none\"\n",
		},
		{
			name:    "replaced without a final newline",
			initial: "repeat = \"Off\"",
			want:    "repeat = \"All\"\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := withHome(t)
			writeConfig(t, home, tt.initial)
			if err := SaveString("repeat", "All"); err != nil {
				t.Fatalf("SaveString: %v", err)
			}
			if got := readConfig(t, home); got != tt.want {
				t.Errorf("config = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSaveNavidromeSortCreatesSection(t *testing.T) {
	home := withHome(t)

	if err := SaveNavidromeSort("alphabeticalByName"); err != nil {
		t.Fatalf("SaveNavidromeSort: %v", err)
	}

	got := readConfig(t, home)
	if !strings.Contains(got, "[navidrome]") {
		t.Errorf("config should contain [navidrome] section:\n%s", got)
	}
	if !strings.Contains(got, `browse_sort = "alphabeticalByName"`) {
		t.Errorf("config should contain browse_sort key:\n%s", got)
	}
}

func TestSaveNavidromeSortReplacesExisting(t *testing.T) {
	home := withHome(t)
	dir := filepath.Join(home, ".config", "cliamp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	initial := "[navidrome]\nurl = \"https://e.com\"\nbrowse_sort = \"old\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(initial), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := SaveNavidromeSort("byYear"); err != nil {
		t.Fatalf("SaveNavidromeSort: %v", err)
	}

	got := readConfig(t, home)
	if strings.Contains(got, "\"old\"") {
		t.Errorf("old browse_sort should be replaced:\n%s", got)
	}
	if !strings.Contains(got, `browse_sort = "byYear"`) {
		t.Errorf("new browse_sort missing:\n%s", got)
	}
}

func TestSaveNavidromeSortAppendsKeyInExistingSection(t *testing.T) {
	home := withHome(t)
	dir := filepath.Join(home, ".config", "cliamp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	initial := "[navidrome]\nurl = \"https://e.com\"\n[other]\nkey = \"val\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(initial), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := SaveNavidromeSort("random"); err != nil {
		t.Fatalf("SaveNavidromeSort: %v", err)
	}

	got := readConfig(t, home)
	if !strings.Contains(got, `browse_sort = "random"`) {
		t.Errorf("browse_sort line missing:\n%s", got)
	}
	// browse_sort should be within [navidrome] block, before [other].
	navIdx := strings.Index(got, "[navidrome]")
	sortIdx := strings.Index(got, "browse_sort")
	otherIdx := strings.Index(got, "[other]")
	if navIdx < 0 || sortIdx < navIdx || sortIdx > otherIdx {
		t.Errorf("browse_sort should be inside [navidrome] block:\n%s", got)
	}
}

func TestSaveMixcloudStylesReplacesOnlyMixcloudStyles(t *testing.T) {
	home := withHome(t)
	dir := filepath.Join(home, ".config", "cliamp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	initial := "# keep\n[mixcloud]\nenabled = true\nstyles = [\"ambient\"] # old\nusername = \"alice\"\n[other]\nstyles = [\"untouched\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(initial), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := SaveMixcloudStyles([]string{"deep-house", "drum-bass"}); err != nil {
		t.Fatalf("SaveMixcloudStyles: %v", err)
	}
	got := readConfig(t, home)
	if !strings.Contains(got, `styles = ["deep-house", "drum-bass"]`) {
		t.Fatalf("updated Mixcloud styles missing:\n%s", got)
	}
	if !strings.Contains(got, "[other]\nstyles = [\"untouched\"]") || !strings.Contains(got, "# keep") || !strings.Contains(got, `username = "alice"`) {
		t.Fatalf("unrelated config changed:\n%s", got)
	}
}

func TestSaveMixcloudStylesCreatesSection(t *testing.T) {
	home := withHome(t)
	if err := SaveMixcloudStyles([]string{"house"}); err != nil {
		t.Fatalf("SaveMixcloudStyles: %v", err)
	}
	got := readConfig(t, home)
	if !strings.Contains(got, "[mixcloud]") || !strings.Contains(got, `styles = ["house"]`) {
		t.Fatalf("Mixcloud section missing:\n%s", got)
	}
}

// TestSaveWithCommentedSectionHeaders checks that the savers see a header
// with a trailing comment, so they neither duplicate the section nor write
// a top-level key inside it.
func TestSaveWithCommentedSectionHeaders(t *testing.T) {
	tests := []struct {
		name    string
		initial string
		save    func() error
		want    string
	}{
		{
			name:    "section key replaced in place",
			initial: "[navidrome] # my server\nbrowse_sort = \"old\"\n",
			save:    func() error { return SaveNavidromeSort("byYear") },
			want:    "[navidrome] # my server\nbrowse_sort = \"byYear\"\n",
		},
		{
			name:    "section key appended to the section",
			initial: "[navidrome] # my server\nurl = \"https://e.com\"\n[plex] # nas\ntoken = \"t\"\n",
			save:    func() error { return SaveNavidromeSort("byYear") },
			want:    "[navidrome] # my server\nurl = \"https://e.com\"\nbrowse_sort = \"byYear\"\n[plex] # nas\ntoken = \"t\"\n",
		},
		{
			name:    "top-level key inserted before the header",
			initial: "[radio] # home\nvolume = 1\n",
			save:    func() error { return save("volume", "-6") },
			want:    "volume = -6\n[radio] # home\nvolume = 1\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := withHome(t)
			dir := filepath.Join(home, ".config", "cliamp")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(tt.initial), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if err := tt.save(); err != nil {
				t.Fatalf("save: %v", err)
			}
			if got := readConfig(t, home); got != tt.want {
				t.Errorf("config =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

// TestTypedSavers writes each value with a typed saver and checks the line
// in the file and the value that Load reads back.
func TestTypedSavers(t *testing.T) {
	tests := []struct {
		name     string
		save     func() error
		wantLine string
		got      func(Config) any
		want     any
	}{
		{
			name:     "string with quote and backslash",
			save:     func() error { return SaveString("theme", `Tokyo "Night" \ dark`) },
			wantLine: `theme = "Tokyo \"Night\" \\ dark"`,
			got:      func(c Config) any { return c.Theme },
			want:     `Tokyo "Night" \ dark`,
		},
		{
			name:     "string with spaces and #",
			save:     func() error { return SaveString("audio_device", "Built-in Audio #2") },
			wantLine: `audio_device = "Built-in Audio #2"`,
			got:      func(c Config) any { return c.AudioDevice },
			want:     "Built-in Audio #2",
		},
		{
			name:     "bool true",
			save:     func() error { return SaveBool("shuffle", true) },
			wantLine: "shuffle = true",
			got:      func(c Config) any { return c.Shuffle },
			want:     true,
		},
		{
			name:     "float with fixed precision",
			save:     func() error { return SaveFloat("speed", 1.25, 2) },
			wantLine: "speed = 1.25",
			got:      func(c Config) any { return c.Speed },
			want:     1.25,
		},
		{
			name:     "float with shortest precision",
			save:     func() error { return SaveFloat("volume", -6, -1) },
			wantLine: "volume = -6",
			got:      func(c Config) any { return c.Volume },
			want:     -6.0,
		},
		{
			name:     "floats",
			save:     func() error { return SaveFloats("eq", []float64{6, 4.5, -2, 0, 0, 0, 0, 0, 0, -12}) },
			wantLine: "eq = [6, 4.5, -2, 0, 0, 0, 0, 0, 0, -12]",
			got:      func(c Config) any { return c.EQ },
			want:     [10]float64{6, 4.5, -2, 0, 0, 0, 0, 0, 0, -12},
		},
		{
			name:     "SaveFunc string",
			save:     func() error { return SaveFunc{}.SaveString("theme", `a "b" \ c`) },
			wantLine: `theme = "a \"b\" \\ c"`,
			got:      func(c Config) any { return c.Theme },
			want:     `a "b" \ c`,
		},
		{
			name:     "SaveFunc bool",
			save:     func() error { return SaveFunc{}.SaveBool("mono", true) },
			wantLine: "mono = true",
			got:      func(c Config) any { return c.Mono },
			want:     true,
		},
		{
			name:     "SaveFunc float",
			save:     func() error { return SaveFunc{}.SaveFloat("speed", 0.75, 2) },
			wantLine: "speed = 0.75",
			got:      func(c Config) any { return c.Speed },
			want:     0.75,
		},
		{
			name:     "SaveFunc floats",
			save:     func() error { return SaveFunc{}.SaveFloats("eq", []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}) },
			wantLine: "eq = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]",
			got:      func(c Config) any { return c.EQ },
			want:     [10]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := withHome(t)
			if err := tt.save(); err != nil {
				t.Fatalf("save: %v", err)
			}
			if got := readConfig(t, home); got != tt.wantLine+"\n" {
				t.Errorf("config = %q, want %q", got, tt.wantLine+"\n")
			}
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := tt.got(cfg); got != tt.want {
				t.Errorf("Load = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestSaveRejectsLineBreaks(t *testing.T) {
	tests := []struct {
		name string
		save func() error
	}{
		{"newline in string", func() error { return SaveString("theme", "a\nshuffle = true") }},
		{"carriage return in raw value", func() error { return save("theme", "\"a\"\r") }},
		{"newline in key", func() error { return save("theme\n[plex]", `"a"`) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := withHome(t)
			const initial = "volume = -6\n"
			writeConfig(t, home, initial)
			if err := tt.save(); err == nil {
				t.Fatal("save accepted a line break")
			}
			if got := readConfig(t, home); got != initial {
				t.Errorf("config changed to %q", got)
			}
		})
	}
}

// TestSaveDuplicateTopLevelKey checks that a save reaches Load when the file
// holds a top-level key twice. Load uses the last line, so save must change
// every line of the key.
func TestSaveDuplicateTopLevelKey(t *testing.T) {
	tests := []struct {
		name    string
		initial string
		want    string
	}{
		{
			name:    "two lines",
			initial: "theme = \"a\"\ntheme = \"b\"\n",
			want:    "theme = \"c\"\ntheme = \"c\"\n",
		},
		{
			name:    "lines apart, and a key in a section",
			initial: "theme = \"a\"\nvolume = -6\ntheme = \"b\"\n[radio]\ntheme = \"x\"\n",
			want:    "theme = \"c\"\nvolume = -6\ntheme = \"c\"\n[radio]\ntheme = \"x\"\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := withHome(t)
			writeConfig(t, home, tt.initial)
			if err := SaveString("theme", "c"); err != nil {
				t.Fatalf("SaveString: %v", err)
			}
			if got := readConfig(t, home); got != tt.want {
				t.Errorf("config = %q, want %q", got, tt.want)
			}
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Theme != "c" {
				t.Errorf("Load Theme = %q, want c", cfg.Theme)
			}
		})
	}
}

// TestSaveKeepsCommentAboveFirstSection checks that a new top-level key does
// not land between a section header and the comment above that header.
func TestSaveKeepsCommentAboveFirstSection(t *testing.T) {
	tests := []struct {
		name    string
		initial string
		want    string
	}{
		{
			name:    "after the last top-level key",
			initial: "volume = 0\n\n# Destination for saves\n[downloads]\ndirectory = \"\"\n",
			want:    "volume = 0\nhide_help_bar = true\n\n# Destination for saves\n[downloads]\ndirectory = \"\"\n",
		},
		{
			name:    "after the last key, past comments",
			initial: "# Volume\nvolume = 0\n# shuffle = true\n\n# Destination for saves\n[downloads]\n",
			want:    "# Volume\nvolume = 0\nhide_help_bar = true\n# shuffle = true\n\n# Destination for saves\n[downloads]\n",
		},
		{
			name:    "no top-level key",
			initial: "# Destination for saves\n[downloads]\ndirectory = \"\"\n",
			want:    "hide_help_bar = true\n# Destination for saves\n[downloads]\ndirectory = \"\"\n",
		},
		{
			name:    "no top-level key, blank lines and comments",
			initial: "# cliamp\n\n# Destination for saves\n[downloads]\n",
			want:    "hide_help_bar = true\n# cliamp\n\n# Destination for saves\n[downloads]\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := withHome(t)
			writeConfig(t, home, tt.initial)
			if err := SaveBool("hide_help_bar", true); err != nil {
				t.Fatalf("SaveBool: %v", err)
			}
			if got := readConfig(t, home); got != tt.want {
				t.Errorf("config = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSaveConcurrentWritersKeepEveryKey runs top-level saves and section
// saves at the same time. Each save reads, edits and writes the whole file,
// so a save that another save overlaps must not lose its key.
func TestSaveConcurrentWritersKeepEveryKey(t *testing.T) {
	home := withHome(t)
	const writers = 8
	for round := range 20 {
		writeConfig(t, home, "volume = 0\n")
		var wg sync.WaitGroup
		errs := make(chan error, 2*writers)
		for i := range writers {
			wg.Add(2)
			go func() {
				defer wg.Done()
				errs <- SaveBool(fmt.Sprintf("k%d", i), true)
			}()
			go func() {
				defer wg.Done()
				errs <- SaveSection("radio", []KeyValue{{fmt.Sprintf("s%d", i), "true"}}, nil)
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("round %d: save: %v", round, err)
			}
		}
		got := readConfig(t, home)
		for i := range writers {
			for _, line := range []string{fmt.Sprintf("k%d = true\n", i), fmt.Sprintf("s%d = true\n", i)} {
				if !strings.Contains(got, line) {
					t.Fatalf("round %d: config lost %q:\n%s", round, line, got)
				}
			}
		}
	}
}
