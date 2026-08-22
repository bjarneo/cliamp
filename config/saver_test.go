package config

import (
	"os"
	"path/filepath"
	"strings"
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

	if err := Save("volume", "-6"); err != nil {
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

	if err := Save("volume", "-3"); err != nil {
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

	if err := Save("volume", "-6"); err != nil {
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

	if err := Save("volume", "-6"); err != nil {
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

func TestSaveFuncDelegates(t *testing.T) {
	home := withHome(t)

	var f SaveFunc
	if err := f.Save("test_key", "123"); err != nil {
		t.Fatalf("SaveFunc.Save: %v", err)
	}

	got := readConfig(t, home)
	if !strings.Contains(got, "test_key = 123") {
		t.Errorf("config should contain 'test_key = 123':\n%s", got)
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
