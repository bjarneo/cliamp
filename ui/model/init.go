package model

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"cliamp/luaplugin"
	"cliamp/player"
	"cliamp/playlist"
	"cliamp/theme"
	"cliamp/ui"
)

// applyThemeAll updates colors, spectrum styles, and model-specific styles.
func applyThemeAll(t theme.Theme) {
	ui.ApplyThemeColors(t)
	rebuildModelStyles()
}

// New creates a Model wired to the given player and playlist.
// providers is the ordered list of available providers (Radio, Navidrome, Spotify, Jellyfin, etc.).
// defaultProvider is the config key of the provider to select initially.
// localProv is an optional direct reference to the local provider for write ops.
// cs persists config changes (pass config.SaveFunc{} for the real filesystem).
func New(p player.Engine, pl *playlist.Playlist, providers []ProviderEntry, defaultProvider string, localProv playlist.Provider, themes []theme.Theme, luaMgr *luaplugin.Manager, cs ConfigSaver) Model {
	m := Model{
		player:        p,
		playlist:      pl,
		configSaver:   cs,
		vis:           ui.NewVisualizer(float64(p.SampleRate())),
		seekStepLarge: 30 * time.Second,
		plVisible:     5,
		eqPresetIdx:   -1, // custom until a preset is selected
		themes:        themes,
		themeIdx:      -1, // Default (ANSI)
		localProvider: localProv,
		providers:     providers,
		navBrowser:    navBrowserState{},
		luaMgr:        luaMgr,
	}
	m.termTitle = initialTerminalTitleState()
	// Select the default provider pill.
	for i, pe := range providers {
		if pe.Key == defaultProvider {
			m.provPillIdx = i
			m.provider = pe.Provider
			break
		}
	}
	// Fallback: select first available provider.
	if m.provider == nil && len(providers) > 0 {
		m.provPillIdx = 0
		m.provider = providers[0].Provider
	}
	return m
}

// findProviderWith returns the first registered provider that satisfies the
// given capability check. This is used for cross-provider shortcuts like "N"
// (browse) and "F" (search) which should work regardless of the active provider.
func (m *Model) findProviderWith(check func(playlist.Provider) bool) playlist.Provider {
	// Prefer the active provider if it matches.
	if check(m.provider) {
		return m.provider
	}
	for _, pe := range m.providers {
		if pe.Provider != nil && check(pe.Provider) {
			return pe.Provider
		}
	}
	return nil
}

// SetAutoPlay makes the player start playback immediately on Init.
func (m *Model) SetAutoPlay(v bool) { m.autoPlay = v }

// SetCompact enables compact mode which caps the frame width at 80 columns.
func (m *Model) SetCompact(v bool) { m.compact = v }

// SetSeekStepLarge configures the Shift+Left/Right seek jump amount.
func (m *Model) SetSeekStepLarge(d time.Duration) {
	switch {
	case d <= 0:
		m.seekStepLarge = 30 * time.Second
	case d <= 5*time.Second:
		m.seekStepLarge = 6 * time.Second
	default:
		m.seekStepLarge = d
	}
}

// SetTheme finds a theme by name and applies it. Returns true if found.
func (m *Model) SetTheme(name string) bool {
	if name == "" || strings.EqualFold(name, "default") {
		m.themeIdx = -1
		applyThemeAll(theme.Default())
		return true
	}
	for i, t := range m.themes {
		if strings.EqualFold(t.Name, name) {
			m.themeIdx = i
			applyThemeAll(t)
			return true
		}
	}
	return false
}

// SetVisualizer sets the visualizer mode by name (case-insensitive).
// Returns true if a valid mode name was recognized. Does not modify state
// if the name is not found, matching the SetTheme guard pattern.
func (m *Model) SetVisualizer(name string) bool {
	mode, ok := ui.StringToVisModeExact(name)
	if !ok {
		return false
	}
	m.vis.Mode = mode
	m.vis.RequestRefresh()
	return true
}

// VisualizerName returns the current visualizer mode's display name.
func (m *Model) VisualizerName() string {
	return m.vis.ModeName()
}

// RegisterLuaVisualizers adds Lua visualizer plugins to the visualizer cycle.
func (m *Model) RegisterLuaVisualizers(names []string, renderer ui.LuaVisRenderer) {
	m.vis.RegisterLuaVisualizers(names, renderer)
}

// SetResume registers a path+position to seek to when that track first plays.
func (m *Model) SetResume(path string, secs int) {
	m.resume.path = path
	m.resume.secs = secs
}

// ResumePlaylist loads a playlist into the model for session resume.
func (m *Model) ResumePlaylist(name string, tracks []playlist.Track) {
	m.playlist.Replace(tracks)
	m.loadedPlaylist = name
}

// ResumeState returns the track path, playback position, and playlist name captured at exit.
// Called after prog.Run() returns (player already closed).
func (m Model) ResumeState() (path string, secs int, playlist string) {
	return m.exitResume.path, m.exitResume.secs, m.exitResume.playlist
}

// ThemeName returns the current theme name.
func (m Model) ThemeName() string {
	if m.themeIdx < 0 || m.themeIdx >= len(m.themes) {
		return theme.DefaultName
	}
	return m.themes[m.themeIdx].Name
}

// Init starts the tick timer and requests the terminal size.
func (m Model) Init() tea.Cmd {
	if m.luaMgr != nil {
		m.luaMgr.Emit(luaplugin.EventAppStart, nil)
	}
	cmds := []tea.Cmd{tickCmd(), func() tea.Msg { return tea.RequestWindowSize() }}
	if m.provider != nil {
		cmds = append(cmds, fetchPlaylistsCmd(m.provider))
	}
	if len(m.pendingURLs) > 0 {
		cmds = append(cmds, resolveRemoteCmd(m.pendingURLs, m.autoPlay))
	}
	if m.autoPlay && m.playlist.Len() > 0 {
		cmds = append(cmds, func() tea.Msg { return autoPlayMsg{} })
	}
	return tea.Batch(cmds...)
}
