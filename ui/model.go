// Package ui implements the Bubbletea TUI for the CLIAMP terminal music player.
package ui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"cliamp/config"
	"cliamp/luaplugin"
	"cliamp/mpris"
	"cliamp/player"
	"cliamp/playlist"
	"cliamp/provider"
	"cliamp/theme"
)

type focusArea int

const (
	focusPlaylist focusArea = iota
	focusEQ
	focusSpeed
	focusProvPill
	focusSearch
	focusProvider
	focusNetSearch
)

type topLevelScreen int

const (
	screenMain topLevelScreen = iota
	screenKeymap
	screenThemePicker
	screenFileBrowser
	screenNavBrowser
	screenPlaylistManager
	screenSpotSearch
	screenQueue
	screenInfo
	screenSearch
	screenNetSearch
	screenURLInput
	screenLyrics
	screenJump
	screenFullVisualizer
)

func (s topLevelScreen) hidesVisualizer() bool {
	return s != screenMain && s != screenFullVisualizer
}

// maxPlVisible caps the playlist at a readable height even on tall terminals.
// maxPlExpandVisible is the higher cap used when the user expands with 'x'.
const (
	maxPlVisible       = 12
	maxPlExpandVisible = 24
)

type plMgrScreenType int

const (
	plMgrScreenList plMgrScreenType = iota
	plMgrScreenTracks
	plMgrScreenNewName
)

// navBrowseModeType identifies which Navidrome browse mode is active.
type navBrowseModeType int

const (
	navBrowseModeMenu          navBrowseModeType = iota // top-level mode selector
	navBrowseModeByAlbum                                // paginated album list → track list
	navBrowseModeByArtist                               // artist list → track list (album-separated)
	navBrowseModeByArtistAlbum                          // artist list → album list → track list
)

// navBrowseScreenType identifies which screen within the active browse mode is shown.
type navBrowseScreenType int

const (
	navBrowseScreenList   navBrowseScreenType = iota // first-level list (artists or albums)
	navBrowseScreenAlbums                            // artist's albums (ArtistAlbum mode only)
	navBrowseScreenTracks                            // final song list in any mode
)

// ProviderEntry pairs a display name with a key and provider implementation.
type ProviderEntry struct {
	Key      string            // config key: "radio", "navidrome", "spotify"
	Name     string            // display name: "Radio", "Navidrome", "Spotify"
	Provider playlist.Provider // nil if not configured
}

type tickMsg time.Time
type autoPlayMsg struct{}

// Tick intervals: fast for visualizer animation, slow for time/seek display.
const (
	tickFast = 50 * time.Millisecond  // 20 FPS — visualizer active
	tickSlow = 200 * time.Millisecond // 5 FPS — visualizer off or overlay

	speedSaveDebounce = time.Second
)

// statusTTL* constants define how long a status message is shown.
const (
	statusTTLShort   statusTTL = statusTTL(2 * time.Second)         // brief confirmations
	statusTTLDefault statusTTL = statusTTL(3 * time.Second)         // standard status messages
	statusTTLMedium  statusTTL = statusTTL(4 * time.Second)         // messages needing extra visibility
	statusTTLBatch   statusTTL = statusTTL(4500 * time.Millisecond) // batch operation feedback
	statusTTLLong    statusTTL = statusTTL(6 * time.Second)         // loading indicators
)

// minPlVisible is the minimum playlist height when collapsed.
const minPlVisible = 5

// streamPreloadLeadTime is how far before the end of a stream we arm the
// gapless next pipeline. Opening the preload HTTP connection too early can
// cause the server to close the current stream (e.g., per-user concurrent
// stream limits on Navidrome), which makes the mp3 decoder error out and
// triggers a premature gapless transition. 3 seconds is short enough that
// most servers won't enforce a concurrency limit for such a brief overlap,
// and any resulting early skip is imperceptible (≤3 s from the true end).
const streamPreloadLeadTime = 3 * time.Second

// ytdlPreloadLeadTime is the lead time used for yt-dlp (YouTube/SoundCloud)
// URLs. These need longer because spinning up the yt-dlp | ffmpeg pipe chain
// takes 3-10 seconds, so we start preloading much earlier.
const ytdlPreloadLeadTime = 15 * time.Second

// Model is the Bubbletea model for the CLIAMP TUI.
type Model struct {
	// Core playback
	player        *player.Player
	playlist      *playlist.Playlist
	vis           *Visualizer
	seekStepLarge time.Duration

	// UI navigation
	focus           focusArea
	prevFocus       focusArea // focus to restore on cancel (search, net search)
	eqCursor        int       // selected EQ band (0-9)
	plCursor        int       // selected playlist item
	plScroll        int       // scroll offset for playlist view
	plVisible       int       // desired max visible playlist lines
	titleOff        int       // scroll offset for long track titles
	titleLastScroll time.Time // last time the title scrolled
	err             error
	quitting        bool
	width           int
	height          int

	// Provider state
	provider      playlist.Provider
	localProvider playlist.Provider // local playlist provider for file-based playlist management (always available)
	providerLists   []playlist.PlaylistInfo
	provCursor      int
	provLoading     bool
	provSignIn      bool            // true when provider needs interactive sign-in
	providers       []ProviderEntry // all available providers
	provPillIdx     int             // selected pill index
	eqPresetIdx     int             // -1 = custom, 0+ = index into eqPresets
	eqCustomLabel   string          // non-empty = plugin-defined preset label (shown instead of "Custom")

	// Overlay / feature state (see state.go for struct definitions)
	search         searchState
	netSearch      netSearchState
	provSearch     provSearchState
	seek           seekState
	themePicker    themePickerState
	lyrics         lyricsState
	keymap         keymapOverlay
	queue          queueOverlay
	plManager      plManagerState
	spotSearch     spotSearchState
	fileBrowser    fileBrowserState
	navBrowser     navBrowserState
	catalogBatch     catalogBatchState
	ytdlBatch      ytdlBatchState
	reconnect      reconnectState
	save           saveState
	status         statusMsg
	network        networkStats
	speedSaveAfter time.Duration
	termTitle      terminalTitleState

	// Jump to time mode
	jumping   bool
	jumpInput string

	// URL input mode (load playlist/stream URL at runtime)
	urlInputting bool
	urlInput     string

	// Async feed/M3U URL resolution
	pendingURLs []string
	feedLoading bool

	// Async stream buffering (true while HTTP connect is in progress)
	buffering   bool
	bufferingAt time.Time // when buffering started, for elapsed display

	// resume holds the path and position to seek to when the matching track
	// starts playing. Cleared after the seek is performed.
	resume struct {
		path string
		secs int
	}

	// exitResume holds the playback state captured just before player.Close()
	// so ResumeState() can read it after the player is shut down.
	exitResume struct {
		path string
		secs int
	}

	// preloading is true while a preloadStreamCmd goroutine is in-flight.
	preloading bool

	// Live stream title from ICY metadata (e.g., "Artist - Song")
	streamTitle string

	// MPRIS D-Bus service (nil on non-Linux or if D-Bus unavailable)
	mpris *mpris.Service

	// Lua plugin manager (nil if no plugins loaded)
	luaMgr *luaplugin.Manager

	// Theme state: -1 = Default (ANSI), 0+ = index into themes
	themes   []theme.Theme
	themeIdx int

	// Track info overlay (metadata details)
	showInfo bool

	// Full-screen visualizer mode (Shift+V)
	fullVis bool

	autoPlay bool // start playing immediately on launch
	compact  bool // compact mode: cap frame width at 80 columns

	// Cached per-tick to avoid repeated speaker.Lock() calls in View().
	cachedPos  time.Duration
	cachedDur  time.Duration
	lastTickAt time.Time // wall time of previous tickMsg; used for tick delta

}

// NewModel creates a Model wired to the given player and playlist.
// providers is the ordered list of available providers (Radio, Navidrome, Spotify).
// defaultProvider is the config key of the provider to select initially.
// localProv is an optional direct reference to the local provider for write ops.
// browseSortType seeds the initial album browse sort preference (empty = default).
func NewModel(p *player.Player, pl *playlist.Playlist, providers []ProviderEntry, defaultProvider string, localProv playlist.Provider, themes []theme.Theme, browseSortType string, luaMgr *luaplugin.Manager) Model {
	if browseSortType == "" {
		browseSortType = "alphabeticalByName"
	}
	m := Model{
		player:        p,
		playlist:      pl,
		vis:           NewVisualizer(float64(p.SampleRate())),
		seekStepLarge: 30 * time.Second,
		plVisible:     5,
		eqPresetIdx:   -1, // custom until a preset is selected
		themes:        themes,
		themeIdx:      -1, // Default (ANSI)
		localProvider: localProv,
		providers:     providers,
		navBrowser:    navBrowserState{sortType: browseSortType},
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
		applyTheme(theme.Default())
		return true
	}
	for i, t := range m.themes {
		if strings.EqualFold(t.Name, name) {
			m.themeIdx = i
			applyTheme(t)
			return true
		}
	}
	return false
}

// SetVisualizer sets the visualizer mode by name (case-insensitive).
// Returns true if a valid mode name was recognized.
func (m *Model) SetVisualizer(name string) bool {
	mode := StringToVisMode(name)
	m.vis.Mode = mode
	m.vis.requestRefresh()
	return name == "" || strings.EqualFold(name, m.vis.ModeName())
}

// VisualizerName returns the current visualizer mode's display name.
func (m *Model) VisualizerName() string {
	return m.vis.ModeName()
}

// RegisterLuaVisualizers adds Lua visualizer plugins to the visualizer cycle.
func (m *Model) RegisterLuaVisualizers(names []string, renderer luaVisRenderer) {
	m.vis.RegisterLuaVisualizers(names, renderer)
}

// SetResume registers a path+position to seek to when that track first plays.
func (m *Model) SetResume(path string, secs int) {
	m.resume.path = path
	m.resume.secs = secs
}

// ResumeState returns the track path and playback position captured at exit.
// Called after prog.Run() returns (player already closed).
func (m Model) ResumeState() (path string, secs int) {
	return m.exitResume.path, m.exitResume.secs
}

// ThemeName returns the current theme name.
func (m Model) ThemeName() string {
	if m.themeIdx < 0 || m.themeIdx >= len(m.themes) {
		return theme.DefaultName
	}
	return m.themes[m.themeIdx].Name
}

func (m Model) activeScreen() topLevelScreen {
	switch {
	case m.keymap.visible:
		return screenKeymap
	case m.themePicker.visible:
		return screenThemePicker
	case m.fileBrowser.visible:
		return screenFileBrowser
	case m.navBrowser.visible:
		return screenNavBrowser
	case m.plManager.visible:
		return screenPlaylistManager
	case m.spotSearch.visible:
		return screenSpotSearch
	case m.queue.visible:
		return screenQueue
	case m.showInfo:
		return screenInfo
	case m.search.active:
		return screenSearch
	case m.netSearch.active:
		return screenNetSearch
	case m.urlInputting:
		return screenURLInput
	case m.lyrics.visible:
		return screenLyrics
	case m.jumping:
		return screenJump
	case m.fullVis:
		return screenFullVisualizer
	default:
		return screenMain
	}
}

func (m Model) isOverlayActive() bool {
	return m.activeScreen().hidesVisualizer()
}

func (m Model) isPlaying() bool {
	return m.player != nil && m.player.IsPlaying()
}

func (m Model) isPaused() bool {
	return m.player != nil && m.player.IsPaused()
}

// openThemePicker re-loads themes from disk (picking up new user files)
// and opens the theme selector overlay.
func (m *Model) openThemePicker() {
	m.themes = theme.LoadAll()
	m.themePicker.visible = true
	m.themePicker.savedIdx = m.themeIdx
	// Position cursor on the currently active theme.
	// Picker list: 0 = Default, 1..N = themes[0..N-1]
	m.themePicker.cursor = m.themeIdx + 1
}

// themePickerApply applies the theme under the cursor for live preview.
func (m *Model) themePickerApply() {
	if m.themePicker.cursor == 0 {
		m.themeIdx = -1
		applyTheme(theme.Default())
	} else {
		m.themeIdx = m.themePicker.cursor - 1
		applyTheme(m.themes[m.themeIdx])
	}
}

// themePickerSelect confirms the current selection and closes the picker.
func (m *Model) themePickerSelect() {
	m.themePickerApply()
	m.themePicker.visible = false
}

// themePickerCancel restores the theme from before the picker was opened.
func (m *Model) themePickerCancel() {
	m.themeIdx = m.themePicker.savedIdx
	if m.themeIdx < 0 {
		applyTheme(theme.Default())
	} else {
		applyTheme(m.themes[m.themeIdx])
	}
	m.themePicker.visible = false
}

// openPlaylistManager loads playlist metadata and opens the manager overlay.
func (m *Model) openPlaylistManager() {
	m.plMgrRefreshList()
	m.plManager.screen = plMgrScreenList
	m.plManager.confirmDel = false
	m.plManager.visible = true
}

// plMgrEnterTrackList loads the tracks for a playlist and switches to screen 1.
func (m *Model) plMgrEnterTrackList(name string) {
	tracks, err := m.localProvider.Tracks(name)
	if err != nil {
		m.status.Showf(statusTTLDefault, "Load failed: %s", err)
		return
	}
	m.plManager.selPlaylist = name
	m.plManager.tracks = tracks
	m.plManager.screen = plMgrScreenTracks
	m.plManager.cursor = 0
	m.plManager.confirmDel = false
}

// plMgrRefreshList reloads playlist names and counts from disk and clamps the cursor.
func (m *Model) plMgrRefreshList() {
	if m.localProvider == nil {
		return
	}
	playlists, err := m.localProvider.Playlists()
	if err != nil {
		m.status.Showf(statusTTLDefault, "Load failed: %s", err)
	}
	m.plManager.playlists = playlists
	// +1 for the "+ New Playlist..." entry
	total := len(m.plManager.playlists) + 1
	if m.plManager.cursor >= total {
		m.plManager.cursor = total - 1
	}
	if m.plManager.cursor < 0 {
		m.plManager.cursor = 0
	}
}

// StartInProvider configures the model to begin in the provider browse view.
// Call this from main when no CLI tracks or pending URLs were given.
func (m *Model) StartInProvider() {
	if m.provider != nil {
		m.focus = focusProvider
		m.provLoading = true
	}
}

// switchProvider sets the active provider by pill index and fetches its playlists.
func (m *Model) switchProvider(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.providers) {
		return nil
	}
	m.provPillIdx = idx
	m.provider = m.providers[idx].Provider
	m.providerLists = nil
	m.provCursor = 0
	m.provLoading = true
	m.provSignIn = false
	m.provSearch.active = false
	m.catalogBatch = catalogBatchState{} // reset catalog batch for new provider
	m.focus = focusProvider
	return fetchPlaylistsCmd(m.provider)
}

// switchToProvider finds a provider by config key and switches to it.
// Returns nil if the provider is not configured.
func (m *Model) switchToProvider(key string) tea.Cmd {
	for i, pe := range m.providers {
		if pe.Key == key {
			return m.switchProvider(i)
		}
	}
	return nil
}

// SetPendingURLs stores remote URLs (feeds, M3U) for async resolution after Init.
func (m *Model) SetPendingURLs(urls []string) {
	m.pendingURLs = urls
	m.feedLoading = len(urls) > 0
}

// SetEQPreset sets the preset by name. If it matches a built-in preset,
// those bands are applied. Otherwise the name is used as a custom label.
// If bands is non-nil, they are applied regardless of whether the name matches.
func (m *Model) SetEQPreset(name string, bands *[10]float64) {
	m.eqCustomLabel = ""

	// Check built-in presets first.
	for i, p := range eqPresets {
		if strings.EqualFold(p.Name, name) {
			m.eqPresetIdx = i
			if bands != nil {
				for j, gain := range bands {
					m.player.SetEQBand(j, gain)
				}
			} else {
				m.applyEQPreset()
			}
			return
		}
	}

	// Custom label — set bands if provided, otherwise keep current.
	m.eqPresetIdx = -1
	m.eqCustomLabel = name
	if bands != nil {
		for i, gain := range bands {
			m.player.SetEQBand(i, gain)
		}
	}
}

// EQPresetName returns the current preset name, or "Custom".
func (m Model) EQPresetName() string {
	if m.eqPresetIdx >= 0 && m.eqPresetIdx < len(eqPresets) {
		return eqPresets[m.eqPresetIdx].Name
	}
	if m.eqCustomLabel != "" {
		return m.eqCustomLabel
	}
	return "Custom"
}

// applyEQPreset writes the current preset's bands to the player.
func (m *Model) applyEQPreset() {
	if m.eqPresetIdx < 0 || m.eqPresetIdx >= len(eqPresets) {
		return
	}
	bands := eqPresets[m.eqPresetIdx].Bands
	for i, gain := range bands {
		m.player.SetEQBand(i, gain)
	}
}

// saveEQ persists the current EQ state (preset name and band values) to config.
func (m *Model) saveEQ() {
	name := m.EQPresetName()
	if err := config.Save("eq_preset", fmt.Sprintf("%q", name)); err != nil {
		m.status.Showf(statusTTLDefault, "Config save failed: %s", err)
	}
	bands := m.player.EQBands()
	parts := make([]string, len(bands))
	for i, g := range bands {
		parts[i] = strconv.FormatFloat(g, 'f', -1, 64)
	}
	eqVal := "[" + strings.Join(parts, ", ") + "]"
	if err := config.Save("eq", eqVal); err != nil {
		m.status.Showf(statusTTLDefault, "Config save failed: %s", err)
	}
}

// saveSpeed persists the current playback speed to the config file.
func (m *Model) saveSpeed() {
	speed := m.player.Speed()
	if err := config.Save("speed", fmt.Sprintf("%.2f", speed)); err != nil {
		m.status.Showf(statusTTLDefault, "Config save failed: %s", err)
	}
}

func (m *Model) changeSpeed(delta float64) {
	m.player.SetSpeed(m.player.Speed() + delta)
	m.speedSaveAfter = speedSaveDebounce
}

func (m *Model) tickPendingSpeedSave(dt time.Duration) {
	if m.speedSaveAfter <= 0 {
		return
	}
	m.speedSaveAfter -= dt
	if m.speedSaveAfter > 0 {
		return
	}
	m.speedSaveAfter = 0
	m.saveSpeed()
}

func (m *Model) flushPendingSpeedSave() {
	if m.speedSaveAfter <= 0 {
		return
	}
	m.speedSaveAfter = 0
	m.saveSpeed()
}

// fetchNavArtistAllTracksCmd first fetches the artist's album list, then fetches
// all tracks across every album. This is used by the "By Artist" browse mode.
// The provider must implement both ArtistBrowser and AlbumTrackLoader.
func (m *Model) fetchNavArtistAllTracksCmd(ab provider.ArtistBrowser, artistID string) tea.Cmd {
	loader, _ := m.navBrowser.prov.(provider.AlbumTrackLoader)
	return func() tea.Msg {
		albums, err := ab.ArtistAlbums(artistID)
		if err != nil {
			return err
		}
		if loader == nil {
			return navTracksLoadedMsg(nil)
		}
		var all []playlist.Track
		for _, album := range albums {
			tracks, err := loader.AlbumTracks(album.ID)
			if err != nil {
				return err
			}
			all = append(all, tracks...)
		}
		return navTracksLoadedMsg(all)
	}
}

// navUpdateSearch rebuilds navSearchIdx from the current navSearch query
// against whichever list is active on the current nav screen.
func (m *Model) navUpdateSearch() {
	q := strings.ToLower(m.navBrowser.search)
	if q == "" {
		m.navBrowser.searchIdx = nil
		return
	}
	m.navBrowser.searchIdx = nil
	switch {
	case m.navBrowser.mode == navBrowseModeByArtist && m.navBrowser.screen == navBrowseScreenList,
		m.navBrowser.mode == navBrowseModeByArtistAlbum && m.navBrowser.screen == navBrowseScreenList:
		for i, a := range m.navBrowser.artists {
			if strings.Contains(strings.ToLower(a.Name), q) {
				m.navBrowser.searchIdx = append(m.navBrowser.searchIdx, i)
			}
		}
	case m.navBrowser.mode == navBrowseModeByAlbum && m.navBrowser.screen == navBrowseScreenList,
		m.navBrowser.mode == navBrowseModeByArtistAlbum && m.navBrowser.screen == navBrowseScreenAlbums:
		for i, a := range m.navBrowser.albums {
			if strings.Contains(strings.ToLower(a.Name), q) ||
				strings.Contains(strings.ToLower(a.Artist), q) {
				m.navBrowser.searchIdx = append(m.navBrowser.searchIdx, i)
			}
		}
	case m.navBrowser.screen == navBrowseScreenTracks:
		for i, t := range m.navBrowser.tracks {
			if strings.Contains(strings.ToLower(t.Title), q) ||
				strings.Contains(strings.ToLower(t.Artist), q) ||
				strings.Contains(strings.ToLower(t.Album), q) {
				m.navBrowser.searchIdx = append(m.navBrowser.searchIdx, i)
			}
		}
	}
}

// navClearSearch resets the nav search state.
func (m *Model) navClearSearch() {
	m.navBrowser.searching = false
	m.navBrowser.search = ""
	m.navBrowser.searchIdx = nil
	m.navBrowser.cursor = 0
	m.navBrowser.scroll = 0
}

// findBrowseProvider returns the first provider that supports browsing
// (ArtistBrowser or AlbumBrowser), preferring the active provider.
func (m *Model) findBrowseProvider() playlist.Provider {
	return m.findProviderWith(func(p playlist.Provider) bool {
		if _, ok := p.(provider.ArtistBrowser); ok {
			return true
		}
		_, ok := p.(provider.AlbumBrowser)
		return ok
	})
}

func (m *Model) openNavBrowserWith(prov playlist.Provider) {
	m.navBrowser.prov = prov
	m.navBrowser.visible = true
	m.navBrowser.mode = navBrowseModeMenu
	m.navBrowser.screen = navBrowseScreenList
	m.navBrowser.cursor = 0
	m.navBrowser.scroll = 0
	m.navBrowser.artists = nil
	m.navBrowser.albums = nil
	m.navBrowser.tracks = nil
	m.navBrowser.loading = false
	m.navBrowser.albumLoading = false
	m.navBrowser.albumDone = false
	m.navBrowser.searching = false
	m.navBrowser.search = ""
	m.navBrowser.searchIdx = nil
}

// Init starts the tick timer and requests the terminal size.
func (m Model) Init() tea.Cmd {
	if m.luaMgr != nil {
		m.luaMgr.Emit(luaplugin.EventAppStart, nil)
	}
	cmds := []tea.Cmd{tickCmd(), tea.WindowSize()}
	if cmd := m.terminalTitleCmd(); cmd != nil {
		cmds = append(cmds, cmd)
	}
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

var teaTick = tea.Tick

func tickCmd() tea.Cmd {
	return tickCmdAt(tickFast)
}

func tickCmdAt(d time.Duration) tea.Cmd {
	return teaTick(d, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m *Model) visualizerPlaying() bool {
	return m.player != nil && m.vis != nil && m.vis.Mode != VisNone &&
		!m.isOverlayActive() && m.player.IsPlaying() && !m.player.IsPaused()
}

func (m *Model) visualizerPaused() bool {
	return m.player != nil && m.vis != nil && m.vis.Mode != VisNone &&
		!m.isOverlayActive() && m.player.IsPlaying() && m.player.IsPaused()
}

func (m *Model) visualizerTickContext(now time.Time) visTickContext {
	sampled := false
	samplesRead := 0
	sampledSize := 0
	cache := map[visAnalysisSpec][]float64{}

	return visTickContext{
		Now:           now,
		Playing:       m.visualizerPlaying(),
		Paused:        m.visualizerPaused(),
		OverlayActive: m.isOverlayActive(),
		Analyze: func(spec visAnalysisSpec) []float64 {
			spec = normalizeAnalysisSpec(spec)
			if m.player == nil || m.vis == nil || m.vis.Mode == VisNone {
				return nil
			}
			if bands, ok := cache[spec]; ok {
				return bands
			}
			if !sampled || spec.FFTSize > sampledSize {
				samplesRead = m.player.SamplesInto(m.vis.ensureSampleBuf(spec.FFTSize))
				sampled = true
				sampledSize = spec.FFTSize
			}
			start := max(0, samplesRead-spec.FFTSize)
			bands := m.vis.Analyze(m.vis.sampleBuf[start:samplesRead], spec)
			cache[spec] = bands
			return bands
		},
	}
}

func (m *Model) tickDelta(now time.Time) time.Duration {
	dt := m.tickInterval()
	if !now.IsZero() && !m.lastTickAt.IsZero() {
		dt = now.Sub(m.lastTickAt)
	}
	if dt <= 0 {
		dt = tickFast
	}
	if !now.IsZero() {
		m.lastTickAt = now
	}
	return dt
}

func advanceTickUnits(counter *int, elapsed *time.Duration, dt, quantum time.Duration) int {
	if *counter <= 0 {
		*elapsed = 0
		return 0
	}
	*elapsed += dt
	if *elapsed < quantum {
		return 0
	}
	steps := int(*elapsed / quantum)
	if steps > *counter {
		steps = *counter
	}
	*counter -= steps
	if *counter == 0 {
		*elapsed = 0
		return steps
	}
	*elapsed -= time.Duration(steps) * quantum
	return steps
}

func (m *Model) tickInterval() time.Duration {
	if m.termTitle.introActive {
		return tickFast
	}
	if m.vis == nil {
		return tickSlow
	}
	return m.vis.TickInterval(m.visualizerTickContext(time.Time{}))
}

func (m *Model) tickVisualizer(now time.Time) {
	if m.vis == nil {
		return
	}
	m.vis.Tick(m.visualizerTickContext(now))
}

func (m Model) refreshVisualizerIfPending() {
	if m.vis == nil || m.vis.Mode == VisNone || m.activeScreen().hidesVisualizer() || !m.vis.consumeRefresh() {
		return
	}
	m.tickVisualizer(time.Now())
}

func (m Model) maybeRequestVisualizerRefresh(msg tea.Msg, wasScreen topLevelScreen, wasMode VisMode, wasPlaying, wasPaused bool) {
	if m.vis == nil {
		return
	}
	if _, ok := msg.(tickMsg); ok {
		return
	}
	screen := m.activeScreen()
	if screen.hidesVisualizer() || m.vis.Mode == VisNone {
		return
	}

	playing := false
	paused := false
	if m.player != nil {
		playing = m.player.IsPlaying()
		paused = m.player.IsPaused()
	}

	if wasScreen != screen ||
		wasMode != m.vis.Mode ||
		(!wasPlaying && playing) ||
		(wasPaused && !paused) {
		m.vis.requestRefresh()
	}
}

func (m Model) mainFrameFixedLines(includeTransient bool) int {
	content := strings.Join(m.mainSections("", includeTransient), "\n")
	return lipgloss.Height(frameStyle.Render(content))
}

func (m Model) effectivePlaylistVisible() int {
	available := m.height - m.mainFrameFixedLines(true)
	if available <= 0 {
		return 0
	}
	if m.plVisible <= 0 {
		return 0
	}
	return min(m.plVisible, available)
}

// Update handles messages: key presses, ticks, and window resizes.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	wasScreen := m.activeScreen()
	wasMode := VisNone
	if m.vis != nil {
		wasMode = m.vis.Mode
	}
	wasPlaying := false
	wasPaused := false
	if m.player != nil {
		wasPlaying = m.player.IsPlaying()
		wasPaused = m.player.IsPaused()
	}
	defer func() {
		m.maybeRequestVisualizerRefresh(msg, wasScreen, wasMode, wasPlaying, wasPaused)
	}()

	switch msg := msg.(type) {
	case tea.KeyMsg:
		cmd := m.handleKey(msg)
		if m.quitting {
			return m, tea.Quit
		}
		return m, cmd

	case autoPlayMsg:
		if m.playlist.Len() > 0 && !m.player.IsPlaying() {
			cmd := m.playCurrentTrack()
			m.notifyAll()
			return m, cmd
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Dynamic frame width: use full terminal width, or cap at 80 in compact mode.
		frameW := msg.Width
		if m.compact {
			frameW = min(frameW, 80)
		}
		frameStyle = frameStyle.Width(frameW)
		panelWidth = max(0, frameW-2*paddingH)
		if m.fullVis {
			m.vis.Rows = max(defaultVisRows, (m.height-10)*4/5)
		}
		m.plVisible = m.defaultPlVisible()
		return m, m.terminalTitleCmd()

	case seekTickMsg:
		// Async yt-dlp seek completed.
		// Only clear seekActive if no new seek keypresses arrived during loading.
		if m.seek.timer <= 0 {
			m.seek.active = false
		}
		// Grace period: suppress reconnect for a few ticks after seek completes.
		m.seek.grace = 10
		m.seek.graceFor = 0
		if m.mpris != nil {
			m.mpris.EmitSeeked(m.player.Position().Microseconds())
		}
		return m, nil

	case tickMsg:
		now := time.Time(msg)
		dt := m.tickDelta(now)

		// Cache expensive player state once per tick so View() render
		// functions don't re-acquire speaker.Lock() multiple times.
		if !m.buffering {
			m.cachedPos = m.displayPosition()
			m.cachedDur = m.player.Duration()
		} else {
			track, _ := m.playlist.Current()
			m.cachedDur = time.Duration(track.DurationSecs) * time.Second
			m.cachedPos = 0
		}
		m.tickVisualizer(now)
		// Process debounced yt-dlp seek.
		var seekCmd tea.Cmd
		if cmd := m.tickSeek(dt); cmd != nil {
			seekCmd = cmd
		}
		// Expire temporary status messages.
		if !m.status.expiresAt.IsZero() && !now.Before(m.status.expiresAt) {
			m.status.Clear()
		}
		m.tickPendingSpeedSave(dt)
		// Decrement seek grace period.
		advanceTickUnits(&m.seek.grace, &m.seek.graceFor, dt, tickFast)
		// Surface stream errors (e.g., connection drops) and auto-reconnect streams.
		// Suppress during yt-dlp seek and grace period — killing the old pipeline
		// triggers a transient error that can persist for a few ticks.
		if err := m.player.StreamErr(); err != nil && !m.seek.active && m.seek.grace == 0 {
			track, idx := m.playlist.Current()
			isStream := idx >= 0 && (track.Stream || playlist.IsYouTubeURL(track.Path) || playlist.IsYTDL(track.Path))
			if isStream && m.reconnect.attempts < 5 {
				// Schedule reconnect with exponential backoff: 1s, 2s, 4s, 8s, 16s
				if m.reconnect.at.IsZero() {
					delay := time.Second << m.reconnect.attempts
					m.reconnect.at = now.Add(delay)
					m.reconnect.attempts++
					m.err = fmt.Errorf("Reconnecting in %s...", delay)
				}
			} else {
				m.err = err
				m.reconnect.at = time.Time{}
			}
		}
		var lyricCmd tea.Cmd
		// Poll ICY stream title for live radio display.
		if title := m.player.StreamTitle(); title != "" && title != m.streamTitle {
			m.streamTitle = title
			m.notifyAll()
			// Auto-fetch lyrics when the stream song changes and lyrics overlay is open.
			if m.lyrics.visible && !m.lyrics.loading {
				if artist, song, ok := strings.Cut(title, " - "); ok {
					q := artist + "\n" + song
					if q != m.lyrics.query {
						m.lyrics.query = q
						m.lyrics.loading = true
						m.lyrics.lines = nil
						m.lyrics.err = nil
						m.lyrics.scroll = 0
						lyricCmd = fetchLyricsCmd(artist, song)
					}
				}
			}
		}
		m.network.sampleFor += dt
		if m.network.sampleFor >= time.Second {
			m.notifyAll()
			downloaded, _ := m.player.StreamBytes()
			delta := downloaded - m.network.lastBytes
			if delta > 0 {
				// Exponential moving average for smooth display.
				instant := float64(delta) / m.network.sampleFor.Seconds() // bytes/sec
				if m.network.speed == 0 {
					m.network.speed = instant
				} else {
					m.network.speed = m.network.speed*0.6 + instant*0.4
				}
			} else if downloaded == 0 {
				m.network.speed = 0
			}
			m.network.lastBytes = downloaded
			m.network.sampleFor = 0
		}
		// Fire scheduled reconnect when the timer expires.
		if !m.reconnect.at.IsZero() && now.After(m.reconnect.at) {
			m.reconnect.at = time.Time{}
			m.player.Stop()
			if track, idx := m.playlist.Current(); idx >= 0 {
				return m, tea.Batch(m.playTrack(track), tickCmdAt(tickFast))
			}
		}
		var cmds []tea.Cmd
		if seekCmd != nil {
			cmds = append(cmds, seekCmd)
		}
		if lyricCmd != nil {
			cmds = append(cmds, lyricCmd)
		}
		// Check gapless transition (audio already playing next track)
		if m.player.GaplessAdvanced() {
			// Capture the track that just finished before advancing the playlist.
			// For gapless, the track played fully (100% ≥ 50%), so elapsed = duration.
			finishedTrack, _ := m.playlist.Current()
			fullDur := time.Duration(finishedTrack.DurationSecs) * time.Second
			m.maybeScrobble(finishedTrack, fullDur, fullDur)

			m.playlist.Next()
			m.plCursor = m.playlist.Index()
			m.adjustScroll()
			m.titleOff = 0
			// The preload that just fired is consumed — clear the in-flight flag
			// so the next track can be preloaded.
			m.preloading = false
			// A stream decoder error at the track boundary (e.g., server closing
			// the connection when the preload HTTP request opens) is expected and
			// not a user-visible problem. Clear any pending error so the red
			// message doesn't flash at every track transition.
			m.err = nil
			// Fire now-playing notification for the track the audio engine just
			// started. playTrack() is not called on this path, so we must notify
			// here explicitly.
			if newTrack, idx := m.playlist.Current(); idx >= 0 {
				m.nowPlaying(newTrack)
			}
			cmds = append(cmds, m.preloadNext())
			m.notifyAll()
		}
		// Check if gapless drained (end of playlist, no preloaded next).
		// Skip if already buffering a yt-dlp download to avoid advancing
		// the playlist on every tick while waiting for the resolve.
		if m.player.IsPlaying() && !m.player.IsPaused() && m.player.Drained() && !m.buffering && m.reconnect.at.IsZero() {
			// Track drained to end — always ≥ 50%.
			finishedTrack, _ := m.playlist.Current()
			drainDur := time.Duration(finishedTrack.DurationSecs) * time.Second
			m.maybeScrobble(finishedTrack, drainDur, drainDur)

			// Stop the player before dispatching the async nextTrack command.
			// This clears the gapless streamer so the finished track cannot
			// replay while waiting for a yt-dlp pipe chain to spin up.
			m.player.Stop()
			cmds = append(cmds, m.nextTrack())
			m.notifyAll()
		}
		if m.player.IsPlaying() && !m.player.IsPaused() {
			if now.Sub(m.titleLastScroll) >= 200*time.Millisecond {
				m.titleOff++
				m.titleLastScroll = now
			}
		}
		// Retry deferred stream preload: preloadNext() returns nil (defers) when
		// the current stream has >streamPreloadLeadTime remaining. Poll every tick
		// until we're within the window and the preload gets armed.
		// Guard with !m.preloading so we don't fire a second concurrent HTTP
		// connection while the first preloadStreamCmd goroutine is still running.
		if m.player.IsPlaying() && !m.player.IsPaused() && !m.buffering && !m.preloading && !m.player.HasPreload() {
			if cmd := m.preloadNext(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

		m.advanceTerminalTitle()
		if cmd := m.terminalTitleCmd(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		cmds = append(cmds, tickCmdAt(m.tickInterval()))
		return m, tea.Batch(cmds...)

	case []playlist.PlaylistInfo:
		m.providerLists = msg
		m.provLoading = false
		// Start loading catalog when the provider supports lazy catalog loading.
		if loader, ok := m.provider.(provider.CatalogLoader); ok && !m.catalogBatch.loading && !m.catalogBatch.done {
			m.catalogBatch.loading = true
			return m, fetchCatalogBatchCmd(loader, m.catalogBatch.offset, catalogBatchSize)
		}
		return m, nil

	case tracksLoadedMsg:
		wasPlaying := m.player.IsPlaying()
		if !wasPlaying {
			m.player.Stop()
			m.player.ClearPreload()
		}
		m.resetYTDLBatch()
		m.playlist.Replace(msg)
		m.plCursor = 0
		m.plScroll = 0
		m.focus = focusPlaylist
		m.provLoading = false
		if m.playlist.Len() > 0 && !wasPlaying {
			cmd := m.playCurrentTrack()
			m.notifyAll()
			return m, cmd
		}
		return m, nil

	case navArtistsLoadedMsg:
		m.navBrowser.artists = []provider.ArtistInfo(msg)
		m.navBrowser.loading = false
		m.navBrowser.cursor = 0
		m.navBrowser.scroll = 0
		return m, nil

	case navAlbumsLoadedMsg:
		if msg.offset == 0 {
			// Fresh load (new sort or drill-in): replace the list.
			m.navBrowser.albums = msg.albums
			m.navBrowser.albumDone = false
		} else {
			// Lazy-load page: append.
			m.navBrowser.albums = append(m.navBrowser.albums, msg.albums...)
		}
		if msg.isLast {
			m.navBrowser.albumDone = true
		}
		m.navBrowser.albumLoading = false
		if msg.offset == 0 {
			m.navBrowser.cursor = 0
			m.navBrowser.scroll = 0
		}
		// If we just loaded the first page and it was a full menu → list transition,
		// also clear the general loading flag.
		m.navBrowser.loading = false
		return m, nil

	case navTracksLoadedMsg:
		m.navBrowser.tracks = []playlist.Track(msg)
		m.navBrowser.loading = false
		m.navBrowser.cursor = 0
		m.navBrowser.scroll = 0
		m.navBrowser.screen = navBrowseScreenTracks
		return m, nil

	case catalogBatchMsg:
		m.catalogBatch.loading = false
		if msg.err != nil {
			m.catalogBatch.done = true
			m.status.Show("Catalog load failed", statusTTLDefault)
			return m, nil
		}
		if msg.added == 0 {
			m.catalogBatch.done = true
			return m, nil
		}
		if lists, err := m.provider.Playlists(); err == nil {
			m.providerLists = lists
		}
		m.catalogBatch.offset += msg.added
		if msg.added < catalogBatchSize {
			m.catalogBatch.done = true
		}
		return m, nil

	case catalogSearchMsg:
		m.provLoading = false
		if msg.err != nil {
			m.status.Show("Search failed", statusTTLDefault)
		} else {
			if lists, err := m.provider.Playlists(); err == nil {
				m.providerLists = lists
			}
			m.provCursor = 0
			if msg.count == 0 {
				m.status.Show("No stations found", statusTTLDefault)
			}
		}
		return m, nil

	case ytdlBatchMsg:
		// Discard stale responses from a previous batch session.
		if msg.gen != m.ytdlBatch.gen {
			return m, nil
		}
		m.ytdlBatch.loading = false
		if msg.err != nil {
			m.ytdlBatch.done = true
			m.status.Showf(statusTTLBatch, "Radio batch load failed: %v", msg.err)
			return m, nil
		}
		if len(msg.tracks) == 0 {
			m.ytdlBatch.done = true
			return m, nil
		}
		m.playlist.Add(msg.tracks...)
		m.ytdlBatch.offset += len(msg.tracks)
		if len(msg.tracks) < ytdlBatchSize {
			m.ytdlBatch.done = true
			return m, nil
		}
		// Immediately fetch the next batch.
		m.ytdlBatch.loading = true
		return m, fetchYTDLBatchCmd(m.ytdlBatch.gen, m.ytdlBatch.url, m.ytdlBatch.offset, ytdlBatchSize)

	case feedsLoadedMsg:
		m.feedLoading = false
		if len(msg.tracks) > 0 {
			m.playlist.Add(msg.tracks...)
			m.status.Showf(statusTTLDefault, "Loaded %d track(s)", len(msg.tracks))
		} else {
			m.status.Show("No tracks found at URL.", statusTTLDefault)
		}
		if len(msg.tracks) > 0 {
			// Set up incremental loading for YouTube Radio playlists.
			// The source URLs are carried in the message so we don't
			// need to re-scan pendingURLs (which misses interactive loads).
			batchCmd := m.initYTDLBatch(msg.urls)
			if msg.autoPlay && m.playlist.Len() > 0 && !m.player.IsPlaying() {
				playCmd := m.playCurrentTrack()
				m.notifyAll()
				if batchCmd != nil {
					return m, tea.Batch(playCmd, batchCmd)
				}
				return m, playCmd
			}
			if batchCmd != nil {
				return m, batchCmd
			}
		}
		return m, nil

	case netSearchLoadedMsg:
		if len(msg) == 0 {
			m.status.Show("No tracks found online.", statusTTLDefault)
			return m, nil
		}
		startIdx := m.playlist.Len()
		m.playlist.Add(msg...)
		for i := startIdx; i < m.playlist.Len(); i++ {
			m.playlist.Queue(i)
		}
		m.status.Showf(statusTTLDefault, "Added to Queue: %s", msg[0].DisplayName())
		if !m.player.IsPlaying() {
			cmd := m.playCurrentTrack()
			m.notifyAll()
			return m, cmd
		}
		return m, nil

	case lyricsLoadedMsg:
		m.lyrics.loading = false
		m.lyrics.err = msg.err
		m.lyrics.scroll = 0
		if msg.err == nil {
			m.lyrics.lines = msg.lines
		}
		return m, nil

	case fbTracksResolvedMsg:
		if len(msg.tracks) == 0 {
			m.status.Show("No audio files found", statusTTLDefault)
			return m, nil
		}
		if msg.replace {
			m.player.Stop()
			m.player.ClearPreload()
			m.resetYTDLBatch()
			m.playlist.Replace(msg.tracks)
			m.plCursor = 0
			m.plScroll = 0
		} else {
			m.playlist.Add(msg.tracks...)
		}
		m.focus = focusPlaylist
		m.status.Showf(statusTTLDefault, "Added %d track(s)", len(msg.tracks))
		if !m.player.IsPlaying() && m.playlist.Len() > 0 {
			if msg.replace {
				m.playlist.SetIndex(0)
			}
			cmd := m.playCurrentTrack()
			m.notifyAll()
			return m, cmd
		}
		return m, nil

	case streamPlayedMsg:
		m.buffering = false
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.err = nil
			m.reconnect.attempts = 0
			m.reconnect.at = time.Time{}
			m.applyResume()
		}
		m.notifyAll()
		return m, m.preloadNext()

	case streamPreloadedMsg:
		m.preloading = false
		return m, nil

	case ytdlSavedMsg:
		m.save.finishDownload()
		if msg.err != nil {
			m.status.Showf(statusTTLMedium, "Download failed: %s", msg.err)
		} else {
			m.status.Showf(statusTTLMedium, "Saved to %s", msg.path)
		}
		return m, nil

	case ytdlResolvedMsg:
		m.buffering = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		// Update the track with the downloaded local file and metadata.
		m.playlist.SetTrack(msg.index, msg.track)
		// Play the local file (seekable).
		cmd := m.playTrack(msg.track)
		m.notifyAll()
		return m, cmd

	case error:
		if errors.Is(msg, playlist.ErrNeedsAuth) {
			m.provLoading = false
			m.provSignIn = true
			m.err = nil
			return m, nil
		}
		m.err = msg
		m.provLoading = false
		m.feedLoading = false
		m.buffering = false
		return m, nil

	case spotSearchResultsMsg:
		m.spotSearch.loading = false
		if msg.err != nil {
			m.spotSearch.err = msg.err.Error()
			return m, nil
		}
		m.spotSearch.results = msg.tracks
		m.spotSearch.cursor = 0
		m.spotSearch.screen = spotSearchResults
		if len(msg.tracks) == 0 {
			m.spotSearch.err = "No results found"
		}
		return m, nil

	case spotPlaylistsMsg:
		m.spotSearch.loading = false
		if msg.err != nil {
			m.spotSearch.err = msg.err.Error()
			return m, nil
		}
		m.spotSearch.playlists = msg.playlists
		m.spotSearch.cursor = 0
		m.spotSearch.screen = spotSearchPlaylist
		return m, nil

	case spotAddedMsg:
		m.spotSearch.loading = false
		if msg.err != nil {
			m.spotSearch.err = "Add failed: " + msg.err.Error()
			return m, nil
		}
		m.status.Showf(statusTTLDefault, "Added to %q", msg.name)
		m.spotSearch.visible = false
		return m, nil

	case spotCreatedMsg:
		m.spotSearch.loading = false
		if msg.err != nil {
			m.spotSearch.err = "Create failed: " + msg.err.Error()
			return m, nil
		}
		m.status.Showf(statusTTLDefault, "Created %q & added track", msg.name)
		m.spotSearch.visible = false
		return m, nil

	case provAuthDoneMsg:
		if msg.err != nil {
			m.err = msg.err
			m.provLoading = false
			m.provSignIn = false
			return m, nil
		}
		m.provSignIn = false
		m.provLoading = true
		return m, fetchPlaylistsCmd(m.provider)

	case mpris.InitMsg:
		m.mpris = msg.Svc
		m.notifyAll()
		return m, nil

	case mpris.PlayPauseMsg:
		cmd := m.togglePlayPause()
		m.notifyAll()
		return m, cmd

	case mpris.NextMsg:
		m.scrobbleCurrent()
		cmd := m.nextTrack()
		m.notifyAll()
		return m, cmd

	case mpris.PrevMsg:
		m.scrobbleCurrent()
		cmd := m.prevTrack()
		m.notifyAll()
		return m, cmd

	case mpris.SeekMsg:
		offset := time.Duration(msg.Offset) * time.Microsecond
		m.player.Seek(offset)
		m.notifyAll()
		if m.mpris != nil {
			m.mpris.EmitSeeked(m.player.Position().Microseconds())
		}
		return m, nil

	case mpris.SetPositionMsg:
		pos := time.Duration(msg.Position) * time.Microsecond
		m.player.Seek(pos - m.player.Position())
		m.notifyAll()
		if m.mpris != nil {
			m.mpris.EmitSeeked(m.player.Position().Microseconds())
		}
		return m, nil

	case mpris.SetVolumeMsg:
		m.player.SetVolume(mpris.LinearToDb(msg.Volume))
		m.notifyAll()
		return m, nil

	case mpris.StopMsg:
		m.player.Stop()
		m.notifyAll()
		return m, nil

	case mpris.QuitMsg:
		m.flushPendingSpeedSave()
		m.player.Close()
		m.quitting = true
		return m, tea.Quit

	case SetEQPresetMsg:
		m.SetEQPreset(msg.Name, msg.Bands)
		return m, nil
	}

	return m, nil
}

// nextTrack advances to the next playlist track and starts playing it.
// Returns a tea.Cmd for async stream playback.
func (m *Model) nextTrack() tea.Cmd {
	track, ok := m.playlist.Next()
	if !ok {
		m.player.Stop()
		return nil
	}
	m.plCursor = m.playlist.Index()
	m.adjustScroll()
	return m.playTrack(track)
}

// prevTrack goes to the previous track, or restarts if >3s into the current one.
func (m *Model) prevTrack() tea.Cmd {
	if m.player.Position() > 3*time.Second {
		if m.player.Seekable() {
			// Local file or seekable stream: jump back to the beginning.
			m.player.Seek(-m.player.Position())
			return nil
		}
		// Non-seekable stream (e.g. Icecast radio): restart by replaying the URL.
		track, idx := m.playlist.Current()
		if idx >= 0 {
			return m.playTrack(track)
		}
		return nil
	}
	track, ok := m.playlist.Prev()
	if !ok {
		return nil
	}
	m.plCursor = m.playlist.Index()
	m.adjustScroll()
	return m.playTrack(track)
}

// playCurrentTrack starts playing whatever track the playlist cursor points to.
func (m *Model) playCurrentTrack() tea.Cmd {
	track, idx := m.playlist.Current()
	if idx < 0 {
		return nil
	}
	m.titleOff = 0
	return m.playTrack(track)
}

// playTrack plays a track, using async HTTP for streams and sync I/O for local files.
// yt-dlp URLs are streamed via a piped yt-dlp | ffmpeg chain for instant playback.
func (m *Model) playTrack(track playlist.Track) tea.Cmd {
	m.reconnect.attempts = 0
	m.reconnect.at = time.Time{}
	m.streamTitle = ""
	m.lyrics.lines = nil
	m.lyrics.err = nil
	m.lyrics.query = ""
	m.lyrics.scroll = 0
	m.seek.active = false
	m.seek.timer = 0
	m.seek.timerFor = 0
	m.seek.grace = 0
	m.seek.graceFor = 0
	var fetchCmd tea.Cmd
	if m.lyrics.visible && track.Artist != "" && track.Title != "" {
		m.lyrics.loading = true
		m.lyrics.query = track.Artist + "\n" + track.Title
		fetchCmd = fetchLyricsCmd(track.Artist, track.Title)
	}

	// Stream yt-dlp URLs (YouTube, SoundCloud, Bandcamp, etc.) via pipe chain.
	if playlist.IsYTDL(track.Path) {
		m.buffering = true
		m.bufferingAt = time.Now()
		m.err = nil
		dur := time.Duration(track.DurationSecs) * time.Second
		if fetchCmd != nil {
			return tea.Batch(playYTDLStreamCmd(m.player, track.Path, dur), fetchCmd)
		}
		return playYTDLStreamCmd(m.player, track.Path, dur)
	}
	// Fire now-playing notification for Navidrome tracks.
	m.nowPlaying(track)
	dur := time.Duration(track.DurationSecs) * time.Second
	if track.Stream {
		m.buffering = true
		m.bufferingAt = time.Now()
		m.err = nil
		return tea.Batch(playStreamCmd(m.player, track.Path, dur), fetchCmd)
	}
	if err := m.player.Play(track.Path, dur); err != nil {
		m.err = err
	} else {
		m.err = nil
		m.applyResume()
	}

	if fetchCmd != nil {
		return tea.Batch(m.preloadNext(), fetchCmd)
	}
	return m.preloadNext()
}

// applyResume seeks to the saved resume position if the current track matches.
// It clears the resume state after a successful seek so it only fires once.
func (m *Model) applyResume() {
	// secs == 0 is indistinguishable from "never played"; skip resume.
	if m.resume.path == "" || m.resume.secs <= 0 {
		return
	}
	track, _ := m.playlist.Current()
	if track.Path != m.resume.path {
		return
	}
	// Only seek if the player reports the stream is seekable; otherwise the
	// seek is a no-op that returns nil, which we must not mistake for success.
	if !m.player.Seekable() {
		return
	}
	target := time.Duration(m.resume.secs) * time.Second
	if err := m.player.Seek(target - m.player.Position()); err == nil {
		m.resume.path = ""
		m.resume.secs = 0
	}
}

// preloadNext looks ahead in the playlist and preloads the next track for
// gapless transition. Errors are silently ignored — playback falls back to
// non-gapless if preloading fails.
//
// For HTTP streams with a known duration, preloading is deferred until the
// current track is within streamPreloadLeadTime of its end. This prevents the
// gapless streamer from having a live HTTP connection armed too early, which
// would cause the player to skip to the next track if the decoder signals EOF
// prematurely (e.g. a mis-estimated Content-Length from a transcoding server).
// When position has not yet reached the threshold, this function returns nil
// and the tick loop will retry on the next pass.
func (m *Model) preloadNext() tea.Cmd {
	next, ok := m.playlist.PeekNext()
	if !ok {
		return nil
	}
	// Preload yt-dlp tracks with the same lead-time deferral as HTTP streams.
	if playlist.IsYTDL(next.Path) {
		dur := m.player.Duration()
		if dur > 0 {
			remaining := dur - m.player.Position()
			if remaining > ytdlPreloadLeadTime {
				return nil
			}
		}
		nextDur := time.Duration(next.DurationSecs) * time.Second
		m.preloading = true
		return preloadYTDLStreamCmd(m.player, next.Path, nextDur)
	}
	if next.Stream {
		// For streams, only arm gapless if we're within the lead-time window.
		// If we don't know the duration yet (0), preload immediately as before
		// so that streams without duration metadata still get gapless behaviour.
		dur := m.player.Duration()
		if dur > 0 {
			pos := m.player.Position()
			remaining := dur - pos
			if remaining > streamPreloadLeadTime {
				// Too early — caller should retry from the tick loop.
				return nil
			}
		}
		nextDur := time.Duration(next.DurationSecs) * time.Second
		// Mark in-flight so the tick loop doesn't dispatch a second concurrent
		// preload before this goroutine has finished arming gapless.SetNext.
		m.preloading = true
		return preloadStreamCmd(m.player, next.Path, nextDur)
	}
	nextDur := time.Duration(next.DurationSecs) * time.Second
	m.player.Preload(next.Path, nextDur)
	return nil
}

// renderedLineCount returns how many rendered lines tracks[from..to) would
// take, including album separator lines between different albums.
func renderedLineCount(tracks []playlist.Track, from, to int) int {
	lines := 0
	prevAlbum := ""
	if from > 0 {
		prevAlbum = tracks[from-1].Album
	}
	for i := from; i < to && i < len(tracks); i++ {
		if album := tracks[i].Album; album != "" && album != prevAlbum {
			lines++ // album separator
		}
		prevAlbum = tracks[i].Album
		lines++ // track line
	}
	return lines
}

// defaultPlVisible recalculates the natural plVisible for the current terminal
// height (same logic as the window-resize handler, capped at maxPlVisible).
func (m *Model) defaultPlVisible() int {
	saved := m.plVisible
	m.plVisible = 3 // temporary minimal value for measurement
	defer func() { m.plVisible = saved }()
	probe := strings.Join([]string{
		m.renderTitle(), m.renderTrackInfo(), m.renderTimeStatus(), "",
		m.renderSpectrum(), m.renderSeekBar(), "",
		m.renderControls(), "", m.renderPlaylistHeader(),
		"x", "", m.renderHelp(), m.renderBottomStatus(),
	}, "\n")
	fixedLines := lipgloss.Height(frameStyle.Render(probe)) - 1
	return max(3, min(maxPlVisible, m.height-fixedLines))
}

// adjustScroll ensures plCursor is visible in the playlist view.
// It accounts for album separator lines that reduce the number of
// tracks that fit in the visible window.
func (m *Model) adjustScroll() {
	tracks := m.playlist.Tracks()
	if len(tracks) == 0 {
		return
	}
	visible := m.effectivePlaylistVisible()
	if visible <= 0 {
		return
	}
	m.plScroll = m.playlistScroll(visible)
}

func (m Model) playlistScroll(visible int) int {
	tracks := m.playlist.Tracks()
	scroll := max(0, m.plScroll)
	if scroll >= len(tracks) {
		scroll = max(0, len(tracks)-1)
	}
	if m.plCursor < scroll {
		return m.plCursor
	}
	lines := renderedLineCount(tracks, scroll, m.plCursor+1)
	if lines <= visible {
		return scroll
	}
	scroll = m.plCursor
	lines = 1 // the cursor track itself
	for i := m.plCursor - 1; i >= 0; i-- {
		add := 1 // track line
		if tracks[i+1].Album != "" && tracks[i+1].Album != tracks[i].Album {
			add++ // separator above track i+1
		}
		if lines+add > visible {
			break
		}
		lines += add
		scroll = i
	}
	if scroll > 0 && tracks[scroll].Album != "" && tracks[scroll].Album != tracks[scroll-1].Album {
		if lines+1 > visible {
			scroll++
		}
	}
	return scroll
}

// notifyAll sends the current playback state to both MPRIS and Lua plugins.
func (m *Model) notifyAll() {
	m.notifyMPRIS()
	m.notifyPlugins()
}

// notifyPlugins emits a playback state event to Lua plugins.
func (m *Model) notifyPlugins() {
	if m.luaMgr == nil || !m.luaMgr.HasHooks() {
		return
	}
	track, _ := m.playlist.Current()
	artist, title := m.resolveTrackDisplay(track)
	status := "stopped"
	if m.player.IsPlaying() {
		if m.player.IsPaused() {
			status = "paused"
		} else {
			status = "playing"
		}
	}
	data := trackToMap(track)
	data["status"] = status
	data["title"] = title
	data["artist"] = artist
	data["position"] = m.player.Position().Seconds()
	m.luaMgr.Emit(luaplugin.EventPlaybackState, data)
}

// resolveTrackDisplay returns the display artist and title, applying ICY
// stream title override for radio streams.
func (m *Model) resolveTrackDisplay(track playlist.Track) (artist, title string) {
	artist, title = track.Artist, track.Title
	if m.streamTitle != "" && track.Stream {
		if a, t, ok := strings.Cut(m.streamTitle, " - "); ok {
			artist, title = a, t
		} else {
			title = m.streamTitle
		}
	}
	return
}

// trackToMap builds a metadata map from a track for Lua plugin events.
func trackToMap(track playlist.Track) map[string]any {
	return map[string]any{
		"title":    track.Title,
		"artist":   track.Artist,
		"album":    track.Album,
		"genre":    track.Genre,
		"year":     track.Year,
		"path":     track.Path,
		"duration": track.DurationSecs,
		"stream":   track.Stream,
	}
}

// notifyMPRIS sends the current playback state to the MPRIS service
// so desktop widgets and playerctl stay in sync.
func (m *Model) notifyMPRIS() {
	if m.mpris == nil {
		return
	}
	status := "Stopped"
	if m.player.IsPlaying() {
		if m.player.IsPaused() {
			status = "Paused"
		} else {
			status = "Playing"
		}
	}
	track, _ := m.playlist.Current()
	artist, title := m.resolveTrackDisplay(track)
	info := mpris.TrackInfo{
		Title:       title,
		Artist:      artist,
		Album:       track.Album,
		Genre:       track.Genre,
		TrackNumber: track.TrackNumber,
		URL:         track.Path,
		Length:      m.player.Duration().Microseconds(),
	}
	m.mpris.Update(status, info, m.player.Volume(),
		m.player.Position().Microseconds(), m.player.Seekable())
}

// togglePlayPause starts playback if stopped, or toggles pause if playing.
// For live streams, unpausing reconnects to get current audio instead of
// playing stale data sitting in OS/decoder buffers from before the pause.
func (m *Model) togglePlayPause() tea.Cmd {
	if m.buffering {
		return nil
	}
	if !m.player.IsPlaying() {
		return m.playCurrentTrack()
	}
	if m.player.IsPaused() {
		track, idx := m.playlist.Current()
		if shouldReconnectOnUnpause(track, idx) {
			m.player.Stop()
			return m.playTrack(track)
		}
	}
	m.player.TogglePause()
	return nil
}

// shouldReconnectOnUnpause reports whether unpausing should reconnect and
// restart instead of resuming buffered audio.
func shouldReconnectOnUnpause(track playlist.Track, idx int) bool {
	return idx >= 0 && track.IsLive()
}

// lyricsArtistTitle resolves the best artist and title for a lyrics lookup.
// For streams with ICY metadata ("Artist - Song"), it parses the stream title.
// For regular tracks, it uses the track's metadata fields.
func (m *Model) lyricsArtistTitle() (artist, title string) {
	track, idx := m.playlist.Current()
	if idx < 0 {
		return "", ""
	}
	// For streams, prefer the live ICY stream title which updates per-song.
	if m.streamTitle != "" && track.Stream {
		if a, t, ok := strings.Cut(m.streamTitle, " - "); ok {
			return strings.TrimSpace(a), strings.TrimSpace(t)
		}
	}
	return track.Artist, track.Title
}

// lyricsSyncable reports whether synced lyrics can track the current playback
// position. This is true for local files and Navidrome streams (which have
// accurate position tracking), but false for live radio (ICY — position is
// from stream start, not song start) and yt-dlp pipe streams (position is 0).
func (m *Model) lyricsSyncable() bool {
	track, idx := m.playlist.Current()
	if idx < 0 {
		return false
	}
	// YouTube/yt-dlp pipe streams report position 0.
	if playlist.IsYouTubeURL(track.Path) || playlist.IsYTDL(track.Path) {
		return false
	}
	// ICY radio streams: position counts from stream connect, not song start.
	// Provider streams with metadata (e.g. Navidrome) track position correctly.
	if track.Stream && len(track.ProviderMeta) == 0 {
		return false
	}
	return true
}

// lyricsHaveTimestamps reports whether the loaded lyrics have meaningful
// timestamps (i.e., not all lines at 0).
func (m *Model) lyricsHaveTimestamps() bool {
	for _, l := range m.lyrics.lines {
		if l.Start > 0 {
			return true
		}
	}
	return false
}

// updateSearch filters the playlist by the current search query.
func (m *Model) updateSearch() {
	m.search.results = nil
	m.search.cursor = 0
	if m.search.query == "" {
		return
	}
	query := strings.ToLower(m.search.query)
	for i, t := range m.playlist.Tracks() {
		if strings.Contains(strings.ToLower(t.DisplayName()), query) {
			m.search.results = append(m.search.results, i)
		}
	}
}

// maybeScrobble fires a submission scrobble for the given track if all
// conditions are met:
//   - navClient is configured
//   - scrobbling is enabled in config
//   - a registered provider implements Scrobbler
//   - elapsed is at least 50% of the track's known duration
//
// The call is dispatched in a goroutine so it never blocks the UI.
func (m *Model) maybeScrobble(track playlist.Track, elapsed, duration time.Duration) {
	// Emit scrobble event to Lua plugins for all tracks (not just Navidrome).
	if m.luaMgr != nil && m.luaMgr.HasHooks() {
		dur := duration
		if dur <= 0 {
			dur = time.Duration(track.DurationSecs) * time.Second
		}
		if dur > 0 && elapsed >= dur/2 {
			data := trackToMap(track)
			data["played_secs"] = elapsed.Seconds()
			m.luaMgr.Emit(luaplugin.EventTrackScrobble, data)
		}
	}

	scrobbler := m.findScrobbler()
	if scrobbler == nil {
		return
	}
	if duration <= 0 {
		// Unknown duration: use DurationSecs metadata as fallback.
		duration = time.Duration(track.DurationSecs) * time.Second
	}
	if duration <= 0 {
		return // still unknown — skip
	}
	if elapsed < duration/2 {
		return // less than 50% played
	}
	go scrobbler.Scrobble(track, true)
}

// nowPlaying fires a now-playing notification for the given track if configured.
func (m *Model) nowPlaying(track playlist.Track) {
	if m.luaMgr != nil && m.luaMgr.HasHooks() {
		m.luaMgr.Emit(luaplugin.EventTrackChange, trackToMap(track))
	}

	if scrobbler := m.findScrobbler(); scrobbler != nil {
		go scrobbler.Scrobble(track, false)
	}
}

// findScrobbler returns the first registered provider that implements Scrobbler.
func (m *Model) findScrobbler() provider.Scrobbler {
	prov := m.findProviderWith(func(p playlist.Provider) bool {
		_, ok := p.(provider.Scrobbler)
		return ok
	})
	if prov == nil {
		return nil
	}
	return prov.(provider.Scrobbler)
}
