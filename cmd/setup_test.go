package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/config"
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

// TestSubmitFormEnvRef checks that setup rejects a $NAME value that names
// an unset or empty variable, which config.Load reads as empty. It also
// checks that the probe gets the value of a set variable and that the form
// keeps the reference for the save. The URL check reads a url variable.
func TestSubmitFormEnvRef(t *testing.T) {
	t.Setenv("CLIAMP_TEST_SETUP_PASS", "from-env")
	t.Setenv("CLIAMP_TEST_SETUP_EMPTY", "")
	t.Setenv("CLIAMP_TEST_SETUP_URL", "https://env.example.com/")
	t.Setenv("CLIAMP_TEST_SETUP_BAD_URL", "env.example.com")
	t.Setenv("Secret1", "")
	os.Unsetenv("Secret1")

	const typedURL = "https://music.example.com/"
	tests := []struct {
		name         string
		url          string // "" means typedURL
		password     string
		wantErr      string
		wantProbe    string
		wantProbeURL string
		wantURL      string // url that the form keeps for the save
	}{
		{name: "unset variable", password: "$Secret1", wantErr: "environment variable"},
		{name: "unset variable in braces", password: "${CLIAMP_TEST_SETUP_UNSET}", wantErr: "environment variable"},
		{name: "empty variable", password: "$CLIAMP_TEST_SETUP_EMPTY", wantErr: "environment variable"},
		{name: "set variable", password: "${CLIAMP_TEST_SETUP_PASS}", wantProbe: "from-env",
			wantProbeURL: "https://music.example.com", wantURL: "https://music.example.com"},
		{name: "literal dollar", password: "p@$$w0rd", wantProbe: "p@$$w0rd",
			wantProbeURL: "https://music.example.com", wantURL: "https://music.example.com"},
		{name: "url from a variable", url: "${CLIAMP_TEST_SETUP_URL}", password: "pw", wantProbe: "pw",
			wantProbeURL: "https://env.example.com/", wantURL: "${CLIAMP_TEST_SETUP_URL}"},
		{name: "url variable without a scheme", url: "$CLIAMP_TEST_SETUP_BAD_URL", password: "pw", wantErr: "http://"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newSetupModel()
			for i, p := range m.provs {
				if p.section == "navidrome" {
					m.menuCursor = i
					break
				}
			}
			m.handleKey(keyPress(tea.KeyEnter, "")) // open form (no picker)
			probed, probedURL := "not probed", ""
			m.provs[m.pidx].validate = func(v map[string]string) error {
				probed, probedURL = v["password"], v["url"]
				return nil
			}
			m.values["url"] = typedURL
			if tt.url != "" {
				m.values["url"] = tt.url
			}
			m.values["user"] = "alice"
			m.values["password"] = tt.password

			_, cmd := m.submitForm()
			if tt.wantErr != "" {
				if m.stage != stageResult || m.resultErr == nil || !strings.Contains(m.resultErr.Error(), tt.wantErr) {
					t.Fatalf("stage = %v, resultErr = %v, want an error with %q", m.stage, m.resultErr, tt.wantErr)
				}
				if tt.wantErr == "environment variable" {
					if key := m.provs[m.pidx].fields[m.visible[m.fcursor]].key; key != "password" {
						t.Errorf("cursor on %q, want password", key)
					}
				}
				if cmd != nil {
					t.Error("submitForm started a probe")
				}
				return
			}
			if m.stage != stageValidating {
				t.Fatalf("stage = %v, want stageValidating; resultErr = %v", m.stage, m.resultErr)
			}
			for _, c := range cmd().(tea.BatchMsg) {
				if msg, ok := c().(validateDoneMsg); ok && msg.err != nil {
					t.Fatalf("probe error: %v", msg.err)
				}
			}
			if probed != tt.wantProbe {
				t.Errorf("probe got password %q, want %q", probed, tt.wantProbe)
			}
			if probedURL != tt.wantProbeURL {
				t.Errorf("probe got url %q, want %q", probedURL, tt.wantProbeURL)
			}
			if m.values["password"] != tt.password {
				t.Errorf("form password = %q, want %q for the save", m.values["password"], tt.password)
			}
			if m.values["url"] != tt.wantURL {
				t.Errorf("form url = %q, want %q for the save", m.values["url"], tt.wantURL)
			}
		})
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

func TestBandcampSetupBody(t *testing.T) {
	spec := providerSpec{}
	for _, p := range providers() {
		if p.section == "bandcamp" {
			spec = p
			break
		}
	}
	if spec.section == "" {
		t.Fatal("bandcamp spec missing")
	}

	// Fan Settings copies credentials with a stray space; the Bandcamp spec
	// trims them itself (the wizard stores other providers' values verbatim).
	body := spec.body(map[string]string{"user": " fan", "password": "secret "})
	checkBody(t, body, map[string]string{"user": `"fan"`, "password": `"secret"`})

	// Bandcamp's ping does not validate credentials, so the spec must define
	// a live validate probe (it uses an authenticated call instead).
	if spec.validate == nil {
		t.Fatal("bandcamp spec must define a validate probe")
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
	checkBody(t, body, map[string]string{"enabled": "true", "cookies_from": `"chrome"`, "user_id": `"42"`})
}

// TestSaveAnywayClearsStaleValidationError covers a regression where
// choosing "Save anyway" after a failed validation probe saved the config
// correctly but left the stale connection error in place, so the result
// screen kept showing "Validation failed" / the raw error instead of the
// intended "Saved without verification" message.
func TestSaveAnywayClearsStaleValidationError(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))

	m := newSetupModel()
	m.pidx = -1
	for i, p := range m.provs {
		if p.section == "navidrome" {
			m.pidx = i
			break
		}
	}
	if m.pidx < 0 {
		t.Fatal("navidrome spec missing")
	}
	m.values = map[string]string{"url": "http://example.com", "user": "alice", "password": "secret"}

	m.onValidateDone(errors.New("dial tcp: connection refused"), nil)
	if !m.awaitingSave || m.resultErr == nil {
		t.Fatalf("onValidateDone(err) should prompt to save anyway; awaitingSave=%v resultErr=%v", m.awaitingSave, m.resultErr)
	}

	m.resultKey(keyPress('y', "y"))
	if m.awaitingSave {
		t.Fatal("pressing y should clear awaitingSave")
	}
	if m.saveFailed != nil {
		t.Fatalf("saveFailed = %v, want nil", m.saveFailed)
	}
	if m.resultErr != nil {
		t.Fatalf("resultErr = %v, want nil after a successful save-anyway", m.resultErr)
	}
	if !m.resultWarning {
		t.Fatal("resultWarning should be true after save-anyway")
	}

	view := m.viewResult()
	if strings.Contains(view, "connection refused") || strings.Contains(view, "Validation failed") {
		t.Fatalf("view still shows the stale validation error: %q", view)
	}
	if !strings.Contains(view, "Saved without verification") {
		t.Fatalf("view missing the save-anyway success message: %q", view)
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
	checkBody(t, spec.body(map[string]string{keyQobuzQuality: "27"}), map[string]string{"enabled": "true", "quality": "27"})

	// Default quality when none picked.
	checkBody(t, spec.body(map[string]string{}), map[string]string{"quality": "6"})

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
	checkBody(t, spec.body(map[string]string{keyTidalQuality: "hires"}), map[string]string{"enabled": "true", "quality": `"hires"`})

	// Default quality when none picked.
	checkBody(t, spec.body(map[string]string{}), map[string]string{"quality": `"lossless"`})

	// No live probe (auth happens interactively in the TUI).
	if spec.validate != nil {
		t.Fatal("tidal spec should not define a validate probe")
	}
}

func TestTelegramSetupBody(t *testing.T) {
	var spec providerSpec
	for _, p := range providers() {
		if p.section == "telegram" {
			spec = p
			break
		}
	}
	if spec.section == "" {
		t.Fatal("telegram spec missing")
	}

	// The sign-in needs the terminal, so setup offers it after the wizard
	// instead of probing here.
	if spec.validate != nil {
		t.Fatal("telegram spec should not define a validate probe")
	}
	if spec.signIn == nil {
		t.Fatal("telegram spec should offer the sign-in after saving")
	}

	const hash = "0123456789abcdef0123456789abcdef"
	checkBody(t, spec.body(map[string]string{
		"api_id": "123456", "api_hash": hash, "phone": "+5491100000000",
		"password": "pw", "chats": "deezload2bot, @mygroup", "buffer_mb": "4",
	}), map[string]string{
		"api_id":    "123456",
		"api_hash":  `"` + hash + `"`,
		"phone":     `"+5491100000000"`,
		"password":  `"pw"`,
		"chats":     `["deezload2bot", "mygroup"]`,
		"buffer_mb": "4",
	})

	// An id written by the picker survives as typed.
	checkBody(t, spec.body(map[string]string{
		"api_id": "123456", "api_hash": hash, "phone": "+5491100000000",
		"chats": "ch:1:2, @mygroup",
	}), map[string]string{"chats": `["ch:1:2", "mygroup"]`})

	// A t.me link is accepted and reduced to the username.
	checkBody(t, spec.body(map[string]string{
		"api_id": "123456", "api_hash": hash, "phone": "+5491100000000",
		"chats": "https://t.me/deezload2bot",
	}), map[string]string{"chats": `["deezload2bot"]`})

	// The optional keys stay out, and the buffer falls back to 1 MiB.
	checkBody(t, spec.body(map[string]string{
		"api_id": "123456", "api_hash": hash, "phone": "+5491100000000",
	}), map[string]string{
		"api_id":    "123456",
		"api_hash":  `"` + hash + `"`,
		"phone":     `"+5491100000000"`,
		"buffer_mb": "1",
	})

	valid := map[string]string{"api_id": "123456", "api_hash": hash, "phone": "+5491100000000"}
	if err := spec.extraValidate(valid); err != nil {
		t.Fatalf("extraValidate rejected a valid config: %v", err)
	}
	// A number copied with spaces, dashes and parentheses still counts.
	spaced := map[string]string{"api_id": "123456", "api_hash": hash, "phone": "+54 (9) 11 1234-5678"}
	if err := spec.extraValidate(spaced); err != nil {
		t.Fatalf("extraValidate rejected a spaced phone: %v", err)
	}
	withChats := map[string]string{"api_id": "123456", "api_hash": hash, "phone": "+5491100000000", "chats": "deezload2bot, @my_group, ch:1:2"}
	if err := spec.extraValidate(withChats); err != nil {
		t.Fatalf("extraValidate rejected the chat list: %v", err)
	}
	for name, values := range map[string]map[string]string{
		"api_id is not a number": {"api_id": "abc", "api_hash": hash, "phone": "+1"},
		"api_id is zero":         {"api_id": "0", "api_hash": hash, "phone": "+1"},
		"api_hash is short":      {"api_id": "123456", "api_hash": "nope", "phone": "+1"},
		"buffer is zero":         {"api_id": "123456", "api_hash": hash, "phone": "+5491100000000", "buffer_mb": "0"},
		"phone is too short":     {"api_id": "123456", "api_hash": hash, "phone": "+1"},
		"chats has spaces":       {"api_id": "123456", "api_hash": hash, "phone": "+5491100000000", "chats": "my group"},
	} {
		if err := spec.extraValidate(values); err == nil {
			t.Errorf("extraValidate accepted %s", name)
		}
	}

	label, action := spec.signIn(map[string]string{
		"api_id": "123456", "api_hash": hash, "phone": "+5491100000000", "buffer_mb": "4",
	})
	if label == "" || action == nil {
		t.Fatalf("signIn label = %q, action set = %v; want both", label, action != nil)
	}
	cfg := telegramConfigFrom(map[string]string{
		"api_id": "123456", "api_hash": hash, "phone": "+5491100000000", "buffer_mb": "4",
	})
	if cfg.AppID != 123456 || cfg.AppHash != hash || cfg.Phone != "+5491100000000" || cfg.BufferMB != 4 {
		t.Fatalf("telegramConfigFrom = %+v", cfg)
	}
}

func TestChatKey(t *testing.T) {
	tests := map[string]string{
		"u:123:456":    "u:123",
		"ch:-100:99":   "ch:-100",
		"c:42":         "c:42",
		"c:42:9":       "",
		"u:123":        "",
		"deezload2bot": "",
		"@mygroup":     "",
	}
	for in, want := range tests {
		if got := chatKey(in); got != want {
			t.Errorf("chatKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckedChatsMatchesStaleHashes(t *testing.T) {
	options := []chatOption{
		{ID: "ch:1:111", Name: "A"},
		{ID: "u:2:222", Name: "B"},
		{ID: "c:3", Name: "C"},
	}
	checked := checkedChats(options, []string{"ch:1:999", "c:3"})
	if !checked[0] || checked[1] || !checked[2] {
		t.Fatalf("checked = %v, want options 0 and 2", checked)
	}
}

func TestApplyChatsAllMeansNoFilter(t *testing.T) {
	m := newSetupModel()
	m.pidx = 0
	m.stage = stageChats
	m.chatItems = []chatOption{{ID: "ch:1:1", Name: "A"}, {ID: "u:2:2", Name: "B"}}

	m.chatChecked = map[int]bool{0: true}
	m.chatTouched = true
	m.applyChats()
	if got := m.values["chats"]; got != "ch:1:1" {
		t.Fatalf("chats = %q, want ch:1:1", got)
	}

	m.chatChecked = map[int]bool{0: true, 1: true}
	m.chatTouched = true
	m.applyChats()
	if got := m.values["chats"]; got != "" {
		t.Fatalf("chats = %q, want empty for every chat", got)
	}
}

// TestApplyChatsWithoutSelectionKeepsTheList covers the trap of opening the
// checklist and pressing enter on a row: it must not clear the saved chats.
func TestApplyChatsWithoutSelectionKeepsTheList(t *testing.T) {
	m := newSetupModel()
	m.pidx = 0
	m.stage = stageChats
	m.chatItems = []chatOption{{ID: "ch:1:1", Name: "A"}, {ID: "u:2:2", Name: "B"}}
	m.values = map[string]string{"chats": "ch:1:1"}

	m.applyChats()

	if got := m.values["chats"]; got != "ch:1:1" {
		t.Fatalf("chats = %q, want the saved list untouched", got)
	}
	if m.chatNote == "" {
		t.Fatal("a no-op apply should explain that space selects")
	}
}

func TestTypedReplacesPrefilledSecret(t *testing.T) {
	secret := fieldSpec{key: "api_hash", secret: true}
	prefilled := map[string]string{"api_hash": "old"}
	if got := typed(secret, "old", "x", prefilled); got != "x" {
		t.Fatalf("first keystroke = %q, want the new value only", got)
	}
	if got := typed(secret, "x", "y", prefilled); got != "xy" {
		t.Fatalf("second keystroke = %q, want the value appended", got)
	}
	plain := fieldSpec{key: "phone"}
	if got := typed(plain, "+54", "9", map[string]string{"phone": "+54"}); got != "+549" {
		t.Fatalf("plain field = %q, want the value appended", got)
	}
}

func TestTelegramSetupPrefill(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const hash = "0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(path, []byte(`
[telegram]
api_id = 123456
api_hash = "`+hash+`"
phone = "+5491100000000"
password = "pw"
chats = ["ch:1:2"]
buffer_mb = 4
`), 0o600); err != nil {
		t.Fatal(err)
	}

	m := newSetupModel()
	idx := -1
	for i, p := range m.provs {
		if p.section == "telegram" {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("telegram spec missing")
	}
	m.startProvider(idx)

	want := map[string]string{
		"api_id":    "123456",
		"api_hash":  hash,
		"phone":     "+5491100000000",
		"password":  "pw",
		"chats":     "ch:1:2",
		"buffer_mb": "4",
	}
	for key, value := range want {
		if got := m.values[key]; got != value {
			t.Errorf("prefill %s = %q, want %q", key, got, value)
		}
	}
}

// TestTelegramSetupOffersSignInAfterSave checks the success screen offers the
// sign-in and that pressing s asks Setup to run it once the wizard exits.
func TestTelegramSetupOffersSignInAfterSave(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))

	m := newSetupModel()
	m.pidx = -1
	for i, p := range m.provs {
		if p.section == "telegram" {
			m.pidx = i
			break
		}
	}
	if m.pidx < 0 {
		t.Fatal("telegram spec missing")
	}
	m.values = map[string]string{
		"api_id": "123456", "api_hash": "0123456789abcdef0123456789abcdef",
		"phone": "+5491100000000", "buffer_mb": "1",
	}
	m.persistAndDone(false)
	if m.signInAction == nil {
		t.Fatal("a successful save should offer the sign-in")
	}
	if view := m.viewResult(); !strings.Contains(view, "Press s to sign in now") {
		t.Fatalf("result view missing the sign-in hint: %q", view)
	}

	m.resultKey(keyPress('s', "s"))
	if !m.signInRequested {
		t.Fatal("pressing s should request the sign-in after the wizard exits")
	}
}

func TestPlexSetupBody(t *testing.T) {
	var spec providerSpec
	for _, p := range providers() {
		if p.section == "plex" {
			spec = p
			break
		}
	}
	if spec.section == "" {
		t.Fatal("plex spec missing")
	}

	withLibraries := spec.body(map[string]string{
		"url":       "http://192.168.1.10:32400",
		"token":     "tok",
		"libraries": "Music, Jazz",
	})
	checkBody(t, withLibraries, map[string]string{
		"url": `"http://192.168.1.10:32400"`, "token": `"tok"`,
		"libraries": `["Music", "Jazz"]`,
	})

	noFilter := spec.body(map[string]string{"url": "http://x", "token": "tok"})
	if _, ok := bodyValues(noFilter)["libraries"]; ok {
		t.Fatalf("blank libraries field must not write a libraries key: %q", noFilter)
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
	checkBody(t, spec.body(values), map[string]string{
		"enabled": "true", "username": `"alice"`, "access_token": `"token"`,
		"cookies_from": `"firefox"`, "styles": `["ambient", "deep-house"]`,
		"max_items": "75", "stream_creators": "15",
	})
	publicOnly := spec.body(map[string]string{
		keyMixcloudBrowser: "none",
		"max_items":        "100",
		"stream_creators":  "20",
	})
	if _, ok := bodyValues(publicOnly)["cookies_from"]; ok {
		t.Fatalf("public-only session must not write cookies_from: %q", publicOnly)
	}
	custom := spec.body(map[string]string{
		keyMixcloudBrowser: "custom",
		"cookies_from":     "chrome:Profile 1",
		"max_items":        "100",
		"stream_creators":  "20",
	})
	checkBody(t, custom, map[string]string{"cookies_from": `"chrome:Profile 1"`})
	if spec.validate != nil {
		t.Fatal("mixcloud setup should not claim a live validation probe")
	}

	err := spec.extraValidate(map[string]string{"max_items": "bad", "stream_creators": "also bad"})
	if err == nil || !strings.Contains(err.Error(), "items per view") {
		t.Fatalf("validation order error = %v, want items per view first", err)
	}
}

// TestSetupValidateMatchesDocs checks the providers that docs/configuration.md
// and docs/cli.md name as checked during setup.
func TestSetupValidateMatchesDocs(t *testing.T) {
	want := map[string]bool{
		"navidrome": true, "lyrion": true, "bandcamp": true, "plex": true, "jellyfin": true,
		"emby": true, "audiobookshelf": true, "netease": true,
		"spotify": false, "qobuz": false, "tidal": false, "mixcloud": false,
		"ytmusic": false, "telegram": false,
	}
	specs := providers()
	if len(specs) != len(want) {
		t.Fatalf("setup has %d providers, the table has %d", len(specs), len(want))
	}
	for _, spec := range specs {
		t.Run(spec.section, func(t *testing.T) {
			wantValidate, ok := want[spec.section]
			if !ok {
				t.Fatalf("section %q is missing from the table", spec.section)
			}
			if got := spec.validate != nil; got != wantValidate {
				t.Fatalf("validate set = %v, want %v", got, wantValidate)
			}
		})
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

	checkBody(t, spec.body(values), map[string]string{"cookies_from": `"firefox"`})
}
