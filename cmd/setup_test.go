package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// keyPress builds a synthetic key event matching what the runtime sends.
func keyPress(code rune, text string) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code, Text: text})
}

func TestMenuNavigation(t *testing.T) {
	m := newSetupModel()

	// Down twice from index 0.
	m.handleKey(keyPress(tea.KeyDown, ""))
	m.handleKey(keyPress(tea.KeyDown, ""))
	if m.menuCursor != 2 {
		t.Fatalf("menuCursor = %d, want 2", m.menuCursor)
	}

	// Up once.
	m.handleKey(keyPress(tea.KeyUp, ""))
	if m.menuCursor != 1 {
		t.Fatalf("after up: menuCursor = %d, want 1", m.menuCursor)
	}

	// Down past the end clamps.
	for i := 0; i < 99; i++ {
		m.handleKey(keyPress(tea.KeyDown, ""))
	}
	if want := len(m.provs) - 1; m.menuCursor != want {
		t.Fatalf("clamped menuCursor = %d, want %d", m.menuCursor, want)
	}
}

// TestPickerSelectionFiltersFields verifies that picking the Jellyfin
// "API token" option hides the user/password fields and vice versa.
func TestPickerSelectionFiltersFields(t *testing.T) {
	m := newSetupModel()

	// Find Jellyfin's index.
	jfIdx := -1
	for i, p := range m.provs {
		if p.section == "jellyfin" {
			jfIdx = i
			break
		}
	}
	if jfIdx < 0 {
		t.Fatal("jellyfin spec missing")
	}

	m.menuCursor = jfIdx
	m.handleKey(keyPress(tea.KeyEnter, "")) // open picker
	if m.stage != stagePicker {
		t.Fatalf("stage = %v, want stagePicker", m.stage)
	}

	// Pick "API token" (option 0).
	m.handleKey(keyPress(tea.KeyEnter, ""))
	if m.stage != stageForm {
		t.Fatalf("stage = %v, want stageForm", m.stage)
	}

	// Visible fields should be url + token, not user + password.
	visibleKeys := map[string]bool{}
	for _, idx := range m.visible {
		visibleKeys[m.provs[jfIdx].fields[idx].key] = true
	}
	if !visibleKeys["url"] || !visibleKeys["token"] {
		t.Fatalf("token mode missing url/token; got %v", visibleKeys)
	}
	if visibleKeys["user"] || visibleKeys["password"] {
		t.Fatalf("token mode should hide user/password; got %v", visibleKeys)
	}

	// Switch back, pick password mode, verify the inverse.
	m.stage = stagePicker
	m.values = map[string]string{}
	m.pickerCursor = 1
	m.handleKey(keyPress(tea.KeyEnter, ""))
	visibleKeys = map[string]bool{}
	for _, idx := range m.visible {
		visibleKeys[m.provs[jfIdx].fields[idx].key] = true
	}
	if !visibleKeys["user"] || !visibleKeys["password"] {
		t.Fatalf("password mode missing user/password; got %v", visibleKeys)
	}
	if visibleKeys["token"] {
		t.Fatalf("password mode should hide token; got %v", visibleKeys)
	}
}

// TestEmbyPickerSelectionFiltersFields mirrors TestPickerSelectionFiltersFields
// for the Emby provider, which uses the same token/password picker shape.
func TestEmbyPickerSelectionFiltersFields(t *testing.T) {
	m := newSetupModel()

	embyIdx := -1
	for i, p := range m.provs {
		if p.section == "emby" {
			embyIdx = i
			break
		}
	}
	if embyIdx < 0 {
		t.Fatal("emby spec missing")
	}

	tests := []struct {
		name         string
		pickerCursor int
		wantVisible  []string
		wantHidden   []string
	}{
		{"API key", 0, []string{"url", "token", "user"}, []string{"password"}},
		{"password", 1, []string{"url", "user", "password"}, []string{"token"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m.menuCursor = embyIdx
			m.stage = stageMenu
			m.values = map[string]string{}
			m.handleKey(keyPress(tea.KeyEnter, "")) // open picker
			if m.stage != stagePicker {
				t.Fatalf("stage = %v, want stagePicker", m.stage)
			}
			m.pickerCursor = tc.pickerCursor
			m.handleKey(keyPress(tea.KeyEnter, "")) // select picker option
			if m.stage != stageForm {
				t.Fatalf("stage = %v, want stageForm", m.stage)
			}
			visible := map[string]bool{}
			for _, idx := range m.visible {
				visible[m.provs[embyIdx].fields[idx].key] = true
			}
			for _, k := range tc.wantVisible {
				if !visible[k] {
					t.Errorf("field %q not visible; got %v", k, visible)
				}
			}
			for _, k := range tc.wantHidden {
				if visible[k] {
					t.Errorf("field %q should be hidden; got %v", k, visible)
				}
			}
		})
	}
}

// TestRequiredFieldBlocksSubmit ensures pressing Enter on the last field
// without filling required values produces an error result rather than
// silently saving.
func TestRequiredFieldBlocksSubmit(t *testing.T) {
	m := newSetupModel()
	// Pick Navidrome.
	for i, p := range m.provs {
		if p.section == "navidrome" {
			m.menuCursor = i
			break
		}
	}
	m.handleKey(keyPress(tea.KeyEnter, "")) // open form (no picker)
	if m.stage != stageForm {
		t.Fatalf("stage = %v, want stageForm", m.stage)
	}

	// Submit immediately with all fields blank.
	m.submitForm()
	if m.stage != stageResult {
		t.Fatalf("stage = %v, want stageResult", m.stage)
	}
	if m.resultErr == nil || !strings.Contains(m.resultErr.Error(), "required") {
		t.Fatalf("resultErr = %v, want a 'required' error", m.resultErr)
	}
}

// TestPasteIntoActiveField checks that bracketed-paste content lands in
// the focused field, with newlines stripped (Spotify Client IDs sometimes
// arrive with a trailing newline from the source app).
func TestPasteIntoActiveField(t *testing.T) {
	m := newSetupModel()
	for i, p := range m.provs {
		if p.section == "spotify" {
			m.menuCursor = i
			break
		}
	}
	m.handleKey(keyPress(tea.KeyEnter, "")) // opens picker (custom is first, default cursor)
	if m.stage != stagePicker {
		t.Fatalf("stage = %v, want stagePicker", m.stage)
	}
	m.handleKey(keyPress(tea.KeyEnter, "")) // confirm "custom" → opens form
	if m.stage != stageForm {
		t.Fatalf("stage = %v, want stageForm after picker", m.stage)
	}

	m.handlePaste("abc123def\n")
	if got := m.values["client_id"]; got != "abc123def" {
		t.Fatalf("after paste: client_id = %q, want %q", got, "abc123def")
	}

	// A second paste appends.
	m.handlePaste("XYZ")
	if got := m.values["client_id"]; got != "abc123defXYZ" {
		t.Fatalf("after second paste: client_id = %q", got)
	}

	// Pasting outside the form (e.g. on the menu) is a no-op.
	m.stage = stageMenu
	before := m.values["client_id"]
	m.handlePaste("should not land")
	if m.values["client_id"] != before {
		t.Fatalf("paste leaked across stages: %q", m.values["client_id"])
	}
}

func TestNetEaseSetupBody(t *testing.T) {
	spec := providerSpec{}
	for _, p := range providers() {
		if p.section == "netease" {
			spec = p
			break
		}
	}
	if spec.section == "" {
		t.Fatal("netease spec missing")
	}
	body := spec.body(map[string]string{
		keyNetEaseBrowser: "chrome",
		"user_id":         "42",
	})
	for _, want := range []string{
		"enabled      = true",
		`cookies_from = "chrome"`,
		`user_id      = "42"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %q", want, body)
		}
	}
}

func TestSaveSectionSecuresConfigFile(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "config")
	t.Setenv("CLIAMP_CONFIG_DIR", configDir)

	if err := saveSection("mixcloud", "access_token = \"secret\""); err != nil {
		t.Fatalf("saveSection: %v", err)
	}
	if runtime.GOOS == "windows" {
		return // Windows does not expose Unix permission bits.
	}

	dirInfo, err := os.Stat(configDir)
	if err != nil {
		t.Fatalf("Stat(%q): %v", configDir, err)
	}
	if got, want := dirInfo.Mode().Perm(), os.FileMode(0o700); got != want {
		t.Errorf("mode for %q = %o, want %o", configDir, got, want)
	}

	path := filepath.Join(configDir, "config.toml")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q): %v", path, err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Errorf("mode for %q = %o, want %o", path, got, want)
	}

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("Chmod(%q): %v", path, err)
	}
	if err := saveSection("mixcloud", "access_token = \"secret\""); err != nil {
		t.Fatalf("rewrite saveSection: %v", err)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q): %v", path, err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Errorf("rewritten mode for %q = %o, want %o", path, got, want)
	}
}

func TestQobuzSetupBody(t *testing.T) {
	spec := providerSpec{}
	for _, p := range providers() {
		if p.section == "qobuz" {
			spec = p
			break
		}
	}
	if spec.section == "" {
		t.Fatal("qobuz spec missing")
	}

	// Explicit quality selection.
	body := spec.body(map[string]string{keyQobuzQuality: "27"})
	for _, want := range []string{"enabled = true", "quality = 27"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %q", want, body)
		}
	}

	// Default quality when none picked.
	if got := spec.body(map[string]string{}); !strings.Contains(got, "quality = 6") {
		t.Fatalf("default quality not 6: %q", got)
	}

	// No live probe (auth happens interactively in the TUI).
	if spec.validate != nil {
		t.Fatal("qobuz spec should not define a validate probe")
	}
}

func TestTidalSetupBody(t *testing.T) {
	spec := providerSpec{}
	for _, p := range providers() {
		if p.section == "tidal" {
			spec = p
			break
		}
	}
	if spec.section == "" {
		t.Fatal("tidal spec missing")
	}

	// Explicit quality selection.
	body := spec.body(map[string]string{keyTidalQuality: "hires"})
	for _, want := range []string{"enabled = true", `quality = "hires"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %q", want, body)
		}
	}

	// Default quality when none picked.
	if got := spec.body(map[string]string{}); !strings.Contains(got, `quality = "lossless"`) {
		t.Fatalf("default quality not lossless: %q", got)
	}

	// No live probe (auth happens interactively in the TUI).
	if spec.validate != nil {
		t.Fatal("tidal spec should not define a validate probe")
	}
}

func TestMixcloudSetupBody(t *testing.T) {
	var spec providerSpec
	for _, p := range providers() {
		if p.section == "mixcloud" {
			spec = p
			break
		}
	}
	if spec.section == "" {
		t.Fatal("mixcloud spec missing")
	}
	values := map[string]string{
		keyMixcloudBrowser: "firefox",
		"username":         "alice",
		"access_token":     "token",
		"styles":           "ambient, deep-house",
		"max_items":        " 75 ",
		"stream_creators":  "15",
	}
	if err := spec.extraValidate(values); err != nil {
		t.Fatalf("extraValidate: %v", err)
	}
	body := spec.body(values)
	for _, want := range []string{
		"enabled = true", `username = "alice"`, `access_token = "token"`,
		`cookies_from = "firefox"`, `styles = ["ambient", "deep-house"]`,
		"max_items = 75", "stream_creators = 15",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %q", want, body)
		}
	}
	publicOnly := spec.body(map[string]string{
		keyMixcloudBrowser: "none",
		"max_items":        "100",
		"stream_creators":  "20",
	})
	if strings.Contains(publicOnly, "cookies_from") {
		t.Fatalf("public-only session must not write cookies_from: %q", publicOnly)
	}
	custom := spec.body(map[string]string{
		keyMixcloudBrowser: "custom",
		"cookies_from":     "chrome:Profile 1",
		"max_items":        "100",
		"stream_creators":  "20",
	})
	if !strings.Contains(custom, `cookies_from = "chrome:Profile 1"`) {
		t.Fatalf("custom browser/profile was not written: %q", custom)
	}
	if spec.validate != nil {
		t.Fatal("mixcloud setup should not claim a live validation probe")
	}

	err := spec.extraValidate(map[string]string{"max_items": "bad", "stream_creators": "also bad"})
	if err == nil || !strings.Contains(err.Error(), "items per view") {
		t.Fatalf("validation order error = %v, want items per view first", err)
	}
}

func TestNetEasePickerSelectionFiltersFields(t *testing.T) {
	base := newSetupModel()
	neteaseIdx := -1
	for i, p := range base.provs {
		if p.section == "netease" {
			neteaseIdx = i
			break
		}
	}
	if neteaseIdx < 0 {
		t.Fatal("netease spec missing")
	}

	tests := []struct {
		name        string
		browser     string
		wantVisible int
		wantKey     string
	}{
		{"chrome hides cookies_from", "chrome", 0, ""},
		{"custom shows cookies_from", "custom", 1, "cookies_from"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newSetupModel()
			m.pidx = neteaseIdx
			m.values = map[string]string{keyNetEaseBrowser: tc.browser}
			m.refreshVisibleFields()
			if len(m.visible) != tc.wantVisible {
				t.Fatalf("visible fields = %d, want %d", len(m.visible), tc.wantVisible)
			}
			if tc.wantVisible == 1 {
				field := m.provs[neteaseIdx].fields[m.visible[0]]
				if field.key != tc.wantKey {
					t.Fatalf("field = %q, want %q", field.key, tc.wantKey)
				}
			}
		})
	}
}

func TestYTMusicCustomModeIncludesOptionalCookies(t *testing.T) {
	var spec providerSpec
	for _, p := range providers() {
		if p.section == "ytmusic" {
			spec = p
			break
		}
	}
	if spec.section == "" {
		t.Fatal("ytmusic spec missing")
	}

	values := map[string]string{
		keyYTMusicMode:  "custom",
		"client_id":     "client",
		"client_secret": "secret",
		"cookies_from":  "firefox",
	}
	visible := make(map[string]bool)
	for _, field := range spec.fields {
		if field.onlyIf == nil || field.onlyIf(values) {
			visible[field.key] = true
		}
	}
	for _, key := range []string{"client_id", "client_secret", "cookies_from"} {
		if !visible[key] {
			t.Fatalf("custom mode hides %q", key)
		}
	}

	body := spec.body(values)
	if !strings.Contains(body, `cookies_from  = "firefox"`) {
		t.Fatalf("custom mode body omits cookies: %q", body)
	}
}

// TestSaveSection covers the three write paths: new file, append, replace.
func TestSaveSection(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	cfg := filepath.Join(dir, ".config", "cliamp", "config.toml")

	// 1. New file.
	if err := saveSection("plex", "url   = \"http://x\"\ntoken = \"t\""); err != nil {
		t.Fatalf("first save: %v", err)
	}
	got, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), "[plex]\n") {
		t.Fatalf("new file: %q", got)
	}

	// 2. Append a new section.
	if err := saveSection("ytmusic", "enabled = true"); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, _ = os.ReadFile(cfg)
	if !strings.Contains(string(got), "[plex]") || !strings.Contains(string(got), "[ytmusic]") {
		t.Fatalf("append: missing one of the sections: %q", got)
	}

	// 3. Replace the plex section in place.
	if err := saveSection("plex", "url   = \"http://NEW\"\ntoken = \"t2\""); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, _ = os.ReadFile(cfg)
	s := string(got)
	if !strings.Contains(s, "http://NEW") {
		t.Fatalf("replace did not write new value: %q", s)
	}
	if strings.Contains(s, "http://x") {
		t.Fatalf("replace left old value: %q", s)
	}
	// Ytmusic must still be present.
	if !strings.Contains(s, "[ytmusic]") {
		t.Fatalf("replace clobbered ytmusic: %q", s)
	}
}
