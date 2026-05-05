// Package cmd implements interactive subcommands invoked from the CLI.
// setup.go contains the provider onboarding wizard reachable via
// `cliamp setup`. It walks the user through configuring each remote
// provider (Navidrome, Plex, Jellyfin, Spotify, YouTube Music),
// validates the connection where possible, and writes the resulting
// TOML section to ~/.config/cliamp/config.toml.
//
// The UI is a small standalone Bubbletea+Lipgloss program — separate from
// the main player Model — because setup runs to completion and exits.
package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"cliamp/external/emby"
	"cliamp/external/jellyfin"
	"cliamp/external/navidrome"
	"cliamp/external/plex"
	"cliamp/internal/appdir"
)

// Setup launches the interactive wizard. Returns nil on clean exit.
func Setup() error {
	prog := tea.NewProgram(newSetupModel())
	_, err := prog.Run()
	return err
}

// ----- Provider specs -----------------------------------------------------

type fieldSpec struct {
	key      string
	label    string
	help     string // shown faintly under the field
	required bool
	secret   bool
	defaultV string
	// onlyIf, when non-nil, hides the field unless the predicate returns true
	// against the current values map. Used by Jellyfin for token-vs-password.
	onlyIf func(map[string]string) bool
}

type providerSpec struct {
	key     string
	name    string
	section string
	intro   []string // pre-form blurb (with help URLs)
	picker  *pickerSpec
	fields  []fieldSpec
	// validate runs a probe against the live server. Nil skips validation.
	validate func(map[string]string) error
	// body returns the TOML block body (no header).
	body func(map[string]string) string
	// extraValidate runs after the form before validate, e.g. to enforce
	// "token OR (user+password)" for Jellyfin.
	extraValidate func(map[string]string) error
}

// pickerSpec is an optional radio choice shown before the fields. The
// selected option's key is stored in values[key]. fieldSpec.onlyIf can
// reference this to show different fields per choice.
type pickerSpec struct {
	key     string
	label   string
	options []pickerOption
}

type pickerOption struct {
	value string
	label string
}

// Picker keys are stored in the values map alongside real field keys; the
// leading underscore distinguishes them from TOML field names.
const (
	keyJellyfinAuth = "_auth"
	keyEmbyAuth     = "_emby_auth"
	keyYTMusicMode  = "_mode"
)

func providers() []providerSpec {
	return []providerSpec{
		{
			key:     "navidrome",
			name:    "Navidrome / Subsonic",
			section: "navidrome",
			intro: []string{
				"Self-hosted music server using the Subsonic API.",
				"Docs: cliamp.stream → docs/navidrome.md",
			},
			fields: []fieldSpec{
				{key: "url", label: "Server URL", help: "e.g. https://music.example.com", required: true},
				{key: "user", label: "Username", required: true},
				{key: "password", label: "Password", required: true, secret: true},
			},
			validate: func(v map[string]string) error {
				return navidrome.New(v["url"], v["user"], v["password"]).Ping()
			},
			body: func(v map[string]string) string {
				return strings.Join([]string{
					fmt.Sprintf("url      = %q", v["url"]),
					fmt.Sprintf("user     = %q", v["user"]),
					fmt.Sprintf("password = %q", v["password"]),
				}, "\n")
			},
		},
		{
			key:     "plex",
			name:    "Plex Media Server",
			section: "plex",
			intro: []string{
				"Stream from your Plex server using an X-Plex-Token.",
				"Find a token: https://support.plex.tv/articles/204059436",
			},
			fields: []fieldSpec{
				{key: "url", label: "Server URL", help: "e.g. http://192.168.1.10:32400", required: true},
				{key: "token", label: "X-Plex-Token", required: true, secret: true},
			},
			validate: func(v map[string]string) error {
				return plex.NewClient(v["url"], v["token"]).Ping()
			},
			body: func(v map[string]string) string {
				return strings.Join([]string{
					fmt.Sprintf("url   = %q", v["url"]),
					fmt.Sprintf("token = %q", v["token"]),
				}, "\n")
			},
		},
		{
			key:     "jellyfin",
			name:    "Jellyfin",
			section: "jellyfin",
			intro: []string{
				"Authenticate with an API token (Dashboard → API Keys)",
				"or with your username and password.",
			},
			picker: &pickerSpec{
				key:   keyJellyfinAuth,
				label: "Authentication",
				options: []pickerOption{
					{value: "token", label: "API token"},
					{value: "password", label: "Username + password"},
				},
			},
			fields: []fieldSpec{
				{key: "url", label: "Server URL", help: "e.g. https://jellyfin.example.com", required: true},
				{key: "token", label: "API token", required: true, secret: true,
					onlyIf: func(v map[string]string) bool { return v[keyJellyfinAuth] == "token" }},
				{key: "user", label: "Username", required: true,
					onlyIf: func(v map[string]string) bool { return v[keyJellyfinAuth] == "password" }},
				{key: "password", label: "Password", required: true, secret: true,
					onlyIf: func(v map[string]string) bool { return v[keyJellyfinAuth] == "password" }},
			},
			validate: func(v map[string]string) error {
				return jellyfin.NewClient(v["url"], v["token"], "", v["user"], v["password"]).Ping()
			},
			body: func(v map[string]string) string {
				lines := []string{fmt.Sprintf("url      = %q", v["url"])}
				if v[keyJellyfinAuth] == "token" {
					lines = append(lines, fmt.Sprintf("token    = %q", v["token"]))
				} else {
					lines = append(lines,
						fmt.Sprintf("user     = %q", v["user"]),
						fmt.Sprintf("password = %q", v["password"]),
					)
				}
				return strings.Join(lines, "\n")
			},
		},
		{
			key:     "emby",
			name:    "Emby",
			section: "emby",
			intro: []string{
				"Authenticate with an API key (Dashboard → API Keys)",
				"or with your username and password.",
			},
			picker: &pickerSpec{
				key:   keyEmbyAuth,
				label: "Authentication",
				options: []pickerOption{
					{value: "token", label: "API key"},
					{value: "password", label: "Username + password"},
				},
			},
			fields: []fieldSpec{
				{key: "url", label: "Server URL", help: "e.g. https://emby.example.com", required: true},
				{key: "token", label: "API key", required: true, secret: true,
					onlyIf: func(v map[string]string) bool { return v[keyEmbyAuth] == "token" }},
				{key: "user", label: "Username (optional)", help: "multi-user servers: picks your account from /Users",
					onlyIf: func(v map[string]string) bool { return v[keyEmbyAuth] == "token" }},
				{key: "user", label: "Username", required: true,
					onlyIf: func(v map[string]string) bool { return v[keyEmbyAuth] == "password" }},
				{key: "password", label: "Password", required: true, secret: true,
					onlyIf: func(v map[string]string) bool { return v[keyEmbyAuth] == "password" }},
			},
			validate: func(v map[string]string) error {
				if err := emby.NewClient(v["url"], v["token"], "", v["user"], v["password"]).Ping(); err != nil {
					return fmt.Errorf("emby: validation: %w", err)
				}
				return nil
			},
			body: func(v map[string]string) string {
				lines := []string{fmt.Sprintf("url      = %q", v["url"])}
				if v[keyEmbyAuth] == "token" {
					lines = append(lines, fmt.Sprintf("token    = %q", v["token"]))
					if v["user"] != "" {
						lines = append(lines, fmt.Sprintf("user     = %q", v["user"]))
					}
				} else {
					lines = append(lines,
						fmt.Sprintf("user     = %q", v["user"]),
						fmt.Sprintf("password = %q", v["password"]),
					)
				}
				return strings.Join(lines, "\n")
			},
		},
		{
			key:     "spotify",
			name:    "Spotify (Premium)",
			section: "spotify",
			intro: []string{
				"Requires Spotify Premium and a developer app.",
				"1. Open https://developer.spotify.com/dashboard",
				"2. Create app → Redirect URI: http://127.0.0.1:19872/login",
				"3. Enable Web API. Copy the Client ID from app Settings.",
				"OAuth runs the first time you pick Spotify in cliamp.",
			},
			fields: []fieldSpec{
				{key: "client_id", label: "Client ID", required: true},
				{key: "bitrate", label: "Bitrate", help: "96, 160, or 320 kbps", defaultV: "320"},
			},
			extraValidate: func(v map[string]string) error {
				if v["bitrate"] == "" {
					return nil
				}
				if _, err := strconv.Atoi(v["bitrate"]); err != nil {
					return fmt.Errorf("bitrate must be a number")
				}
				return nil
			},
			body: func(v map[string]string) string {
				br := v["bitrate"]
				if br == "" {
					br = "320"
				}
				return strings.Join([]string{
					fmt.Sprintf("client_id = %q", v["client_id"]),
					fmt.Sprintf("bitrate   = %s", br),
				}, "\n")
			},
		},
		{
			key:     "ytmusic",
			name:    "YouTube Music",
			section: "ytmusic",
			intro: []string{
				"Works out of the box with built-in fallback credentials.",
				"Provide your own OAuth client to skip the shared pool, and/or",
				"a browser name for cookie-based age-gated playback.",
			},
			picker: &pickerSpec{
				key:   keyYTMusicMode,
				label: "Mode",
				options: []pickerOption{
					{value: "default", label: "Use built-in credentials (recommended)"},
					{value: "custom", label: "Provide my own OAuth credentials / cookies"},
					{value: "off", label: "Disable YouTube Music"},
				},
			},
			fields: []fieldSpec{
				{key: "client_id", label: "OAuth Client ID",
					onlyIf: func(v map[string]string) bool { return v[keyYTMusicMode] == "custom" }},
				{key: "client_secret", label: "OAuth Client Secret", secret: true,
					onlyIf: func(v map[string]string) bool { return v[keyYTMusicMode] == "custom" }},
				{key: "cookies_from", label: "Cookies from browser", help: "e.g. chrome, firefox; blank to skip",
					onlyIf: func(v map[string]string) bool { return v[keyYTMusicMode] == "custom" }},
			},
			body: func(v map[string]string) string {
				switch v[keyYTMusicMode] {
				case "off":
					return "enabled = false"
				case "custom":
					lines := []string{"enabled = true"}
					if v["client_id"] != "" {
						lines = append(lines, fmt.Sprintf("client_id     = %q", v["client_id"]))
					}
					if v["client_secret"] != "" {
						lines = append(lines, fmt.Sprintf("client_secret = %q", v["client_secret"]))
					}
					if v["cookies_from"] != "" {
						lines = append(lines, fmt.Sprintf("cookies_from  = %q", v["cookies_from"]))
					}
					return strings.Join(lines, "\n")
				default:
					return "enabled = true"
				}
			},
		},
	}
}

// ----- Bubbletea model ----------------------------------------------------

type stage int

const (
	stageMenu stage = iota
	stagePicker
	stageForm
	stageValidating
	stageResult
)

type setupModel struct {
	provs []providerSpec
	w, h  int

	stage      stage
	menuCursor int

	// active provider (index into provs); -1 means none
	pidx int

	// picker state (within a provider)
	pickerCursor int

	// form state
	values  map[string]string
	visible []int // indices into provs[pidx].fields that are currently visible
	fcursor int

	// validating
	spinFrame int

	// result state
	resultErr     error
	resultWarning bool   // validation failed but config still valid
	resultText    string // overall message (e.g. "Saved [navidrome] section.")
	saveFailed    error  // io error writing config (rare; shown as error result)
	awaitingSave  bool   // true after a failed validate: waiting for y/n

	// cached so we don't hit os.UserHomeDir per-frame
	cfgPath string
}

func newSetupModel() *setupModel {
	cfgPath, _ := configFilePath()
	return &setupModel{
		provs:   providers(),
		stage:   stageMenu,
		pidx:    -1,
		values:  map[string]string{},
		cfgPath: cfgPath,
	}
}

func (m *setupModel) Init() tea.Cmd {
	return func() tea.Msg { return tea.RequestWindowSize() }
}

// ----- Messages -----------------------------------------------------------

type validateDoneMsg struct{ err error }

type spinTickMsg struct{}

func spinTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return spinTickMsg{} })
}

func runValidateCmd(spec providerSpec, values map[string]string) tea.Cmd {
	return func() tea.Msg {
		if spec.validate == nil {
			return validateDoneMsg{}
		}
		return validateDoneMsg{err: spec.validate(values)}
	}
}

// ----- Update -------------------------------------------------------------

func (m *setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.PasteMsg:
		return m.handlePaste(msg.Content)

	case spinTickMsg:
		if m.stage == stageValidating {
			m.spinFrame++
			return m, spinTickCmd()
		}
		return m, nil

	case validateDoneMsg:
		return m.onValidateDone(msg.err)
	}
	return m, nil
}

// handlePaste appends bracketed-paste content into the active form field.
// Newlines and other control characters are stripped — the wizard's fields
// are all single-line, and pasted credentials often have a trailing newline
// from the source app.
func (m *setupModel) handlePaste(content string) (tea.Model, tea.Cmd) {
	if content == "" || m.stage != stageForm || len(m.visible) == 0 {
		return m, nil
	}
	clean := sanitizePaste(content)
	if clean == "" {
		return m, nil
	}
	field := m.provs[m.pidx].fields[m.visible[m.fcursor]]
	m.values[field.key] += clean
	return m, nil
}

func sanitizePaste(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		// Drop newlines and other ASCII control chars; keep spaces and tabs (tabs as space).
		switch {
		case r == '\n' || r == '\r':
			continue
		case r == '\t':
			b.WriteRune(' ')
		case r < 0x20:
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (m *setupModel) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}

	switch m.stage {
	case stageMenu:
		return m.menuKey(msg)
	case stagePicker:
		return m.pickerKey(msg)
	case stageForm:
		return m.formKey(msg)
	case stageValidating:
		// Ignore input while probing.
		return m, nil
	case stageResult:
		return m.resultKey(msg)
	}
	return m, nil
}

func (m *setupModel) menuKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		return m, tea.Quit
	case "up", "k":
		if m.menuCursor > 0 {
			m.menuCursor--
		}
	case "down", "j":
		if m.menuCursor < len(m.provs)-1 {
			m.menuCursor++
		}
	case "enter":
		m.startProvider(m.menuCursor)
	}
	return m, nil
}

func (m *setupModel) startProvider(idx int) {
	m.pidx = idx
	m.values = map[string]string{}
	m.fcursor = 0
	m.pickerCursor = 0
	m.resultErr = nil
	m.resultText = ""
	m.resultWarning = false
	m.awaitingSave = false
	m.saveFailed = nil

	if m.provs[idx].picker != nil {
		m.stage = stagePicker
	} else {
		m.refreshVisibleFields()
		m.stage = stageForm
	}
}

func (m *setupModel) pickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.provs[m.pidx].picker
	switch msg.String() {
	case "esc":
		m.stage = stageMenu
		return m, nil
	case "up", "k":
		if m.pickerCursor > 0 {
			m.pickerCursor--
		}
	case "down", "j":
		if m.pickerCursor < len(p.options)-1 {
			m.pickerCursor++
		}
	case "enter":
		m.values[p.key] = p.options[m.pickerCursor].value
		// Pickers can short-circuit with no fields (e.g. ytmusic "default" / "off").
		m.refreshVisibleFields()
		if len(m.visible) == 0 {
			return m.submitForm()
		}
		m.stage = stageForm
	}
	return m, nil
}

func (m *setupModel) formKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if len(m.visible) == 0 {
		// Defensive: shouldn't happen; submit anyway.
		return m.submitForm()
	}
	field := m.provs[m.pidx].fields[m.visible[m.fcursor]]
	cur := m.values[field.key]

	switch msg.Code {
	case tea.KeyEscape:
		m.stage = stageMenu
		return m, nil
	case tea.KeyUp:
		if m.fcursor > 0 {
			m.fcursor--
		}
		return m, nil
	case tea.KeyTab, tea.KeyDown:
		// Shift+Tab arrives with ModShift; treat it as "previous field".
		if msg.Mod&tea.ModShift != 0 {
			if m.fcursor > 0 {
				m.fcursor--
			}
			return m, nil
		}
		if m.fcursor < len(m.visible)-1 {
			m.fcursor++
		}
		return m, nil
	case tea.KeyEnter:
		// Enter on the last field submits; otherwise advance.
		if m.fcursor < len(m.visible)-1 {
			m.fcursor++
			return m, nil
		}
		return m.submitForm()
	case tea.KeyBackspace:
		if cur != "" {
			m.values[field.key] = removeLastRune(cur)
		}
		return m, nil
	case tea.KeySpace:
		m.values[field.key] = cur + " "
		return m, nil
	}

	// Treat ctrl+s / ctrl+d as submit shortcuts.
	if s := msg.String(); s == "ctrl+s" || s == "ctrl+d" {
		return m.submitForm()
	}

	if len(msg.Text) > 0 {
		m.values[field.key] = cur + msg.Text
	}
	return m, nil
}

func (m *setupModel) submitForm() (tea.Model, tea.Cmd) {
	spec := m.provs[m.pidx]

	// Apply defaults for blank fields.
	for _, f := range spec.fields {
		if m.values[f.key] == "" && f.defaultV != "" {
			m.values[f.key] = f.defaultV
		}
	}

	// Required-field check (only for visible fields).
	for i, idx := range m.visible {
		f := spec.fields[idx]
		if f.required && strings.TrimSpace(m.values[f.key]) == "" {
			m.fcursor = i
			m.resultErr = fmt.Errorf("%s is required", f.label)
			m.resultText = ""
			m.stage = stageResult
			return m, nil
		}
	}

	if spec.extraValidate != nil {
		if err := spec.extraValidate(m.values); err != nil {
			m.resultErr = err
			m.stage = stageResult
			return m, nil
		}
	}

	// Light URL sanity check before any network call.
	if u, ok := m.values["url"]; ok && u != "" {
		clean := strings.TrimRight(u, "/")
		if !looksLikeHTTPURL(clean) {
			m.resultErr = fmt.Errorf("URL must start with http:// or https://")
			m.stage = stageResult
			return m, nil
		}
		m.values["url"] = clean
	}

	if spec.validate == nil {
		// No probe; save immediately.
		return m, m.persistAndDone(false)
	}

	m.stage = stageValidating
	m.spinFrame = 0
	return m, tea.Batch(spinTickCmd(), runValidateCmd(spec, m.values))
}

func (m *setupModel) onValidateDone(err error) (tea.Model, tea.Cmd) {
	if err == nil {
		return m, m.persistAndDone(false)
	}
	m.resultErr = err
	m.awaitingSave = true
	m.stage = stageResult
	return m, nil
}

// persistAndDone writes the section and transitions to result. warn=true
// indicates the user opted to save despite a failed probe.
func (m *setupModel) persistAndDone(warn bool) tea.Cmd {
	spec := m.provs[m.pidx]
	body := spec.body(m.values)
	if err := saveSection(spec.section, body); err != nil {
		m.saveFailed = err
		m.stage = stageResult
		m.awaitingSave = false
		return nil
	}
	m.stage = stageResult
	m.awaitingSave = false
	m.resultWarning = warn
	m.resultText = fmt.Sprintf("Saved [%s] section.", spec.section)
	return nil
}

func (m *setupModel) resultKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.awaitingSave {
		switch strings.ToLower(msg.String()) {
		case "y":
			m.persistAndDone(true)
			return m, nil
		case "n", "esc":
			m.awaitingSave = false
			m.resultErr = nil
			m.stage = stageMenu
			return m, nil
		}
		return m, nil
	}

	switch msg.String() {
	case "enter", "esc", "q", " ":
		m.stage = stageMenu
		return m, nil
	}
	return m, nil
}

// refreshVisibleFields recomputes the visible-field index list based on
// the current values map, then resets the cursor to a sensible position.
func (m *setupModel) refreshVisibleFields() {
	spec := m.provs[m.pidx]
	m.visible = m.visible[:0]
	for i, f := range spec.fields {
		if f.onlyIf == nil || f.onlyIf(m.values) {
			m.visible = append(m.visible, i)
		}
	}
	if m.fcursor >= len(m.visible) {
		m.fcursor = 0
	}
}

// ----- Rendering ----------------------------------------------------------

var (
	// Color choices match the existing player palette; ANSI numbers give
	// good contrast against any terminal theme.
	titleStyle  = lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(10)).Bold(true)
	hintStyle   = lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(7))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(8))
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(11)).Bold(true)
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(9)).Bold(true)
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(10)).Bold(true)
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(11)).Bold(true)
	cardStyle   = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.ANSIColor(8)).
			Padding(1, 2)
	activeFieldStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder(), false, false, false, true).
				BorderForeground(lipgloss.ANSIColor(11)).
				PaddingLeft(1)
	inactiveFieldStyle = lipgloss.NewStyle().PaddingLeft(2)
)

const (
	maxCardWidth = 78
	logoLine1    = "  cliamp setup"
	logoLine2    = "  configure remote providers"
)

func (m *setupModel) View() tea.View {
	header := titleStyle.Render(logoLine1) + "\n" +
		hintStyle.Render(logoLine2) + "\n"

	var body string
	switch m.stage {
	case stageMenu:
		body = m.viewMenu()
	case stagePicker:
		body = m.viewPicker()
	case stageForm:
		body = m.viewForm()
	case stageValidating:
		body = m.viewValidating()
	case stageResult:
		body = m.viewResult()
	}

	footer := "\n" + m.viewFooter()
	view := tea.NewView(header + "\n" + body + footer)
	view.AltScreen = true
	return view
}

// card wraps body in the standard rounded-border card sized to the terminal.
func (m *setupModel) card(body string) string {
	return cardStyle.Width(min(maxCardWidth, m.viewWidth())).Render(body)
}

func (m *setupModel) viewMenu() string {
	var b strings.Builder
	b.WriteString(accentStyle.Render("Pick a provider to configure"))
	b.WriteString("\n\n")
	for i, p := range m.provs {
		marker := "  "
		nameRender := p.name
		if i == m.menuCursor {
			marker = accentStyle.Render("▸ ")
			nameRender = accentStyle.Render(p.name)
		}
		b.WriteString(marker)
		b.WriteString(nameRender)
		b.WriteString("\n")
		// Render the first intro line as a one-liner per item, faintly.
		if len(p.intro) > 0 {
			indent := "    "
			b.WriteString(indent)
			b.WriteString(hintStyle.Render(p.intro[0]))
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("config: " + m.cfgPath))
	return m.card(b.String())
}

func (m *setupModel) viewPicker() string {
	spec := m.provs[m.pidx]
	p := spec.picker
	var b strings.Builder
	b.WriteString(accentStyle.Render(spec.name))
	b.WriteString("\n\n")
	for _, line := range spec.intro {
		b.WriteString(hintStyle.Render(line))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(accentStyle.Render(p.label))
	b.WriteString("\n\n")
	for i, opt := range p.options {
		marker := "  "
		text := opt.label
		if i == m.pickerCursor {
			marker = accentStyle.Render("▸ ")
			text = accentStyle.Render(opt.label)
		}
		b.WriteString(marker)
		b.WriteString(text)
		b.WriteString("\n")
	}
	return m.card(b.String())
}

func (m *setupModel) viewForm() string {
	spec := m.provs[m.pidx]
	var b strings.Builder
	b.WriteString(accentStyle.Render(spec.name))
	b.WriteString("\n\n")
	for _, line := range spec.intro {
		b.WriteString(hintStyle.Render(line))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	for i, idx := range m.visible {
		f := spec.fields[idx]
		val := m.values[f.key]
		display := val
		if f.secret {
			display = strings.Repeat("•", utf8.RuneCountInString(val))
		}
		labelLine := f.label
		if f.required {
			labelLine += accentStyle.Render(" *")
		}
		valueLine := display
		if i == m.fcursor {
			// Active: show cursor caret at end of value.
			valueLine = display + accentStyle.Render("▎")
			block := labelLine + "\n" + valueLine
			if f.help != "" {
				block += "\n" + dimStyle.Render(f.help)
			}
			b.WriteString(activeFieldStyle.Render(block))
		} else {
			if valueLine == "" {
				valueLine = dimStyle.Render("(empty)")
			}
			block := labelLine + "\n" + valueLine
			b.WriteString(inactiveFieldStyle.Render(block))
		}
		b.WriteString("\n\n")
	}
	return m.card(strings.TrimRight(b.String(), "\n"))
}

func (m *setupModel) viewValidating() string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	frame := frames[m.spinFrame%len(frames)]
	body := accentStyle.Render(frame) + " " +
		"Verifying connection to " + m.provs[m.pidx].name + "…"
	return cardStyle.Width(min(maxCardWidth, m.viewWidth())).Render(body)
}

func (m *setupModel) viewResult() string {
	spec := m.provs[m.pidx]
	var b strings.Builder
	b.WriteString(accentStyle.Render(spec.name))
	b.WriteString("\n\n")

	if m.saveFailed != nil {
		b.WriteString(errStyle.Render("✗ Failed to write config"))
		b.WriteString("\n\n")
		b.WriteString(m.saveFailed.Error())
		b.WriteString("\n\n")
		b.WriteString(hintStyle.Render("Press any key to return to the menu."))
		return m.card(b.String())
	}

	if m.awaitingSave {
		b.WriteString(errStyle.Render("✗ Validation failed"))
		b.WriteString("\n\n")
		b.WriteString(m.resultErr.Error())
		b.WriteString("\n\n")
		b.WriteString(hintStyle.Render("The config will still load on next launch — useful when the server is offline now."))
		b.WriteString("\n\n")
		b.WriteString(accentStyle.Render("Save anyway?  ") + "[y/N]")
		return m.card(b.String())
	}

	if m.resultErr != nil {
		b.WriteString(errStyle.Render("✗ "))
		b.WriteString(m.resultErr.Error())
		b.WriteString("\n\n")
		b.WriteString(hintStyle.Render("Press any key to return to the menu."))
		return m.card(b.String())
	}

	if m.resultWarning {
		b.WriteString(warnStyle.Render("⚠ Saved without verification"))
	} else {
		b.WriteString(okStyle.Render("✓ " + m.resultText))
	}
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render(m.cfgPath))
	b.WriteString("\n\n")
	b.WriteString(hintStyle.Render("Press any key to configure another provider, or q to quit."))
	return m.card(b.String())
}

func (m *setupModel) viewFooter() string {
	var keys string
	switch m.stage {
	case stageMenu:
		keys = "↑/↓ pick   enter select   q quit"
	case stagePicker:
		keys = "↑/↓ pick   enter confirm   esc back"
	case stageForm:
		keys = "↑/↓ field   enter submit   esc back"
	case stageValidating:
		keys = "ctrl+c cancel"
	case stageResult:
		if m.awaitingSave {
			keys = "y save anyway   n cancel"
		} else {
			keys = "any key continue   q quit"
		}
	}
	return dimStyle.Render(keys)
}

func (m *setupModel) viewWidth() int {
	if m.w <= 4 {
		return maxCardWidth
	}
	return m.w - 2
}

// ----- Helpers ------------------------------------------------------------

func looksLikeHTTPURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

func configFilePath() (string, error) {
	dir, err := appdir.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

// removeLastRune trims the final UTF-8 rune from s.
func removeLastRune(s string) string {
	if len(s) > 0 {
		_, size := utf8.DecodeLastRuneInString(s)
		return s[:len(s)-size]
	}
	return s
}

// saveSection rewrites or appends a [section] block in config.toml. The
// body is the raw TOML between the header and the next section/EOF; it
// must not contain a header line itself. Existing content for the same
// section is replaced; everything else is preserved as-is.
func saveSection(section, body string) error {
	path, err := configFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	header := "[" + section + "]"
	block := header + "\n" + strings.TrimRight(body, "\n") + "\n"

	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return os.WriteFile(path, []byte(block), 0o644)
	}

	lines := strings.Split(string(data), "\n")
	start, end := findSection(lines, section)
	if start < 0 {
		out := strings.TrimRight(string(data), "\n")
		if out != "" {
			out += "\n\n"
		}
		out += block
		return os.WriteFile(path, []byte(out), 0o644)
	}

	before := strings.TrimRight(strings.Join(lines[:start], "\n"), "\n")
	after := ""
	if end < len(lines) {
		after = strings.TrimLeft(strings.Join(lines[end:], "\n"), "\n")
	}
	var b strings.Builder
	if before != "" {
		b.WriteString(before)
		b.WriteString("\n\n")
	}
	b.WriteString(block)
	if after != "" {
		b.WriteString("\n")
		b.WriteString(after)
		if !strings.HasSuffix(after, "\n") {
			b.WriteString("\n")
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// findSection returns [start, end) line indices for the [name] block in
// lines, where start points at the header line and end points at the
// next section header (or len(lines) if it's the last block). Returns
// (-1, -1) if the section is absent.
func findSection(lines []string, name string) (int, int) {
	target := "[" + strings.ToLower(name) + "]"
	start := -1
	for i, l := range lines {
		t := strings.ToLower(strings.TrimSpace(l))
		if t == target {
			start = i
			break
		}
	}
	if start < 0 {
		return -1, -1
	}
	for i := start + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			return start, i
		}
	}
	return start, len(lines)
}
