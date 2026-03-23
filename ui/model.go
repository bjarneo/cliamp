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
	"cliamp/external/local"
	"cliamp/external/navidrome"
	"cliamp/mpris"
	"cliamp/player"
	"cliamp/playlist"
	"cliamp/theme"
)

type focusArea int

const (
	focusPlaylist focusArea = iota
	focusEQ
	focusProvPill
	focusSearch
	focusProvider
	focusNetSearch
)

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
)

// statusTTL* constants define how many ticks a status message persists.
// At tickFast (50ms), 20 ticks ≈ 1 second.
const (
	statusTTLShort    = 40  // ~2s — brief confirmations
	statusTTLDefault  = 60  // ~3s — standard status messages
	statusTTLMedium   = 80  // ~4s — messages needing extra visibility
	statusTTLBatch    = 90  // ~4.5s — batch operation feedback
	statusTTLLong     = 120 // ~6s — loading indicators
	statusTTLDownload = 600 // ~30s — cleared manually by completion message
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
	focus     focusArea
	prevFocus focusArea // focus to restore on cancel (search, net search)
	eqCursor  int       // selected EQ band (0-9)
	plCursor  int       // selected playlist item
	plScroll  int       // scroll offset for playlist view
	plVisible int       // max visible playlist items
	titleOff        int       // scroll offset for long track titles
	titleLastScroll time.Time // last time the title scrolled
	err       error
	quitting  bool
	width     int
	height    int

	// Provider state
	provider      playlist.Provider
	localProvider *local.Provider // direct ref for write operations (add-to-playlist)
	providerLists []playlist.PlaylistInfo
	provCursor    int
	provLoading   bool
	provSignIn    bool            // true when provider needs interactive sign-in
	providers     []ProviderEntry // all available providers
	provPillIdx   int             // selected pill index
	eqPresetIdx   int             // -1 = custom, 0+ = index into eqPresets

	// Overlay / feature state (see state.go for struct definitions)
	search      searchState
	netSearch   netSearchState
	provSearch  provSearchState
	seek        seekState
	themePicker themePickerState
	lyrics      lyricsState
	keymap      keymapOverlay
	queue       queueOverlay
	plManager   plManagerState
	fileBrowser fileBrowserState
	navBrowser  navBrowserState
	ytdlBatch   ytdlBatchState
	reconnect   reconnectState
	status      statusMsg
	network     networkStats

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
	cachedPos time.Duration
	cachedDur time.Duration

	// Navidrome client (kept separate from navBrowser for non-browser operations)
	navClient          *navidrome.NavidromeClient
	navScrobbleEnabled bool
}

// NewModel creates a Model wired to the given player and playlist.
// providers is the ordered list of available providers (Radio, Navidrome, Spotify).
// defaultProvider is the config key of the provider to select initially ("radio", "navidrome", "spotify").
// localProv is an optional direct reference to the local provider for write ops.
// navCfg is the Navidrome config used to seed the initial browse sort preference.
// nav is the raw NavidromeClient (may be nil); stored directly so the browser
// key handler doesn't have to unwrap a provider.
func NewModel(p *player.Player, pl *playlist.Playlist, providers []ProviderEntry, defaultProvider string, localProv *local.Provider, themes []theme.Theme, navCfg config.NavidromeConfig, nav *navidrome.NavidromeClient) Model {
	sortType := navCfg.BrowseSort
	if sortType == "" {
		sortType = navidrome.SortAlphabeticalByName
	}
	m := Model{
		player:             p,
		playlist:           pl,
		vis:                NewVisualizer(float64(p.SampleRate())),
		seekStepLarge:      30 * time.Second,
		plVisible:          5,
		eqPresetIdx:        -1, // custom until a preset is selected
		themes:             themes,
		themeIdx:           -1, // Default (ANSI)
		localProvider:      localProv,
		providers:          providers,
		navBrowser:         navBrowserState{sortType: sortType},
		navClient:          nav,
		navScrobbleEnabled: navCfg.ScrobbleEnabled(),
	}
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
	return name == "" || strings.EqualFold(name, m.vis.ModeName())
}

// VisualizerName returns the current visualizer mode's display name.
func (m *Model) VisualizerName() string {
	return m.vis.ModeName()
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

// isOverlayActive reports whether a full-screen overlay is shown instead of
// the main player view. When true, the visualizer is not visible and we can
// use the slower tick rate.
func (m *Model) isOverlayActive() bool {
	return m.keymap.visible || m.themePicker.visible ||
		m.fileBrowser.visible || m.navBrowser.visible || m.plManager.visible ||
		m.queue.visible || m.showInfo || m.search.active || m.netSearch.active ||
		m.jumping || m.urlInputting
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
		m.status.text = fmt.Sprintf("Load failed: %s", err)
		m.status.ttl = statusTTLDefault
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
		m.status.text = fmt.Sprintf("Load failed: %s", err)
		m.status.ttl = statusTTLDefault
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
	m.focus = focusProvider
	return fetchPlaylistsCmd(m.provider)
}

// SetPendingURLs stores remote URLs (feeds, M3U) for async resolution after Init.
func (m *Model) SetPendingURLs(urls []string) {
	m.pendingURLs = urls
	m.feedLoading = len(urls) > 0
}

// SetEQPreset sets the preset index by name. Returns true if found.
func (m *Model) SetEQPreset(name string) bool {
	for i, p := range eqPresets {
		if strings.EqualFold(p.Name, name) {
			m.eqPresetIdx = i
			m.applyEQPreset()
			return true
		}
	}
	return false
}

// EQPresetName returns the current preset name, or "Custom".
func (m Model) EQPresetName() string {
	if m.eqPresetIdx < 0 || m.eqPresetIdx >= len(eqPresets) {
		return "Custom"
	}
	return eqPresets[m.eqPresetIdx].Name
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
		m.status.text = fmt.Sprintf("Config save failed: %s", err)
		m.status.ttl = statusTTLDefault
	}
	bands := m.player.EQBands()
	parts := make([]string, len(bands))
	for i, g := range bands {
		parts[i] = strconv.FormatFloat(g, 'f', -1, 64)
	}
	eqVal := "[" + strings.Join(parts, ", ") + "]"
	if err := config.Save("eq", eqVal); err != nil {
		m.status.text = fmt.Sprintf("Config save failed: %s", err)
		m.status.ttl = statusTTLDefault
	}
}

// fetchNavArtistAllTracksCmd first fetches the artist's album list, then fetches
// all tracks across every album. This is used by the "By Artist" browse mode.
func (m *Model) fetchNavArtistAllTracksCmd(navClient *navidrome.NavidromeClient, artistID string) tea.Cmd {
	return func() tea.Msg {
		albums, err := navClient.ArtistAlbums(artistID)
		if err != nil {
			return err
		}
		var all []playlist.Track
		for _, album := range albums {
			tracks, err := navClient.AlbumTracks(album.ID)
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

func (m *Model) openNavBrowser() {
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
	cmds := []tea.Cmd{tickCmd(), tea.WindowSize()}
	if m.provider != nil {
		cmds = append(cmds, fetchPlaylistsCmd(m.provider))
	}
	if len(m.pendingURLs) > 0 {
		cmds = append(cmds, resolveRemoteCmd(m.pendingURLs))
	}
	if m.autoPlay && m.playlist.Len() > 0 {
		cmds = append(cmds, func() tea.Msg { return autoPlayMsg{} })
	}
	return tea.Batch(cmds...)
}

func tickCmd() tea.Cmd {
	return tickCmdAt(tickFast)
}

func tickCmdAt(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Update handles messages: key presses, ticks, and window resizes.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			m.notifyMPRIS()
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
		panelWidth = frameW - 6 // subtract horizontal padding (3 left + 3 right)
		if m.fullVis {
			m.vis.Rows = max(defaultVisRows, (m.height-10)*4/5)
		}
		// Dynamic playlist height: render all non-playlist sections, measure
		// total height, then give the remaining space to the playlist.
		// This avoids fragile manual line counting.
		m.plVisible = 3 // temporary minimal value for measurement
		probe := strings.Join([]string{
			m.renderTitle(),
			m.renderTrackInfo(),
			m.renderTimeStatus(),
			"",
			m.renderSpectrum(),
			m.renderSeekBar(),
			"",
			m.renderControls(),
			"",
			m.renderPlaylistHeader(),
			"x", // placeholder for playlist (1 line)
			"",
			m.renderHelp(),
			m.renderStreamStatus(),
		}, "\n")
		probeFrame := frameStyle.Render(probe)
		fixedLines := lipgloss.Height(probeFrame) - 1 // subtract the 1-line placeholder
		m.plVisible = max(3, min(maxPlVisible, m.height-fixedLines))

	case seekTickMsg:
		// Async yt-dlp seek completed.
		// Only clear seekActive if no new seek keypresses arrived during loading.
		if m.seek.timer <= 0 {
			m.seek.active = false
		}
		// Grace period: suppress reconnect for a few ticks after seek completes.
		m.seek.grace = 10
		if m.mpris != nil {
			m.mpris.EmitSeeked(m.player.Position().Microseconds())
		}
		return m, nil

	case tickMsg:
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
		// Process debounced yt-dlp seek.
		var seekCmd tea.Cmd
		if cmd := m.tickSeek(); cmd != nil {
			seekCmd = cmd
		}
		// Expire temporary status messages.
		if m.status.ttl > 0 {
			m.status.ttl--
			if m.status.ttl == 0 {
				m.status.text = ""
			}
		}
		// Decrement seek grace period.
		if m.seek.grace > 0 {
			m.seek.grace--
		}
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
					m.reconnect.at = time.Now().Add(delay)
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
		// Update network throughput every ~1 second (20 ticks at 50ms).
		m.network.lastTick++
		if m.network.lastTick >= 20 {
			downloaded, _ := m.player.StreamBytes()
			delta := downloaded - m.network.lastBytes
			if delta > 0 {
				// Exponential moving average for smooth display.
				instant := float64(delta) / (float64(m.network.lastTick) * 0.05) // bytes/sec
				if m.network.speed == 0 {
					m.network.speed = instant
				} else {
					m.network.speed = m.network.speed*0.6 + instant*0.4
				}
			} else if downloaded == 0 {
				m.network.speed = 0
			}
			m.network.lastBytes = downloaded
			m.network.lastTick = 0
		}
		// Fire scheduled reconnect when the timer expires.
		if !m.reconnect.at.IsZero() && time.Now().After(m.reconnect.at) {
			m.reconnect.at = time.Time{}
			m.player.Stop()
			if track, idx := m.playlist.Current(); idx >= 0 {
				return m, tea.Batch(m.playTrack(track), tickCmd())
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
			m.notifyMPRIS()
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
			m.notifyMPRIS()
		}
		if m.player.IsPlaying() && !m.player.IsPaused() {
			if time.Since(m.titleLastScroll) >= 200*time.Millisecond {
				m.titleOff++
				m.titleLastScroll = time.Now()
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

		// Use fast ticks only when audio is actively playing with a live
		// visualizer. Paused/stopped playback has no new audio samples, so
		// slow ticks are sufficient and save CPU/GPU repaints.
		interval := tickSlow
		if m.vis.Mode != VisNone && !m.isOverlayActive() &&
			m.player.IsPlaying() && !m.player.IsPaused() {
			interval = tickFast
		}
		cmds = append(cmds, tickCmdAt(interval))
		return m, tea.Batch(cmds...)

	case []playlist.PlaylistInfo:
		m.providerLists = msg
		m.provLoading = false
		return m, nil

	case tracksLoadedMsg:
		if !m.player.IsPlaying() {
			m.player.Stop()
			m.player.ClearPreload()
		}
		m.resetYTDLBatch()
		m.playlist.Replace(msg)
		m.plCursor = 0
		m.plScroll = 0
		m.focus = focusPlaylist
		m.provLoading = false
		if m.playlist.Len() > 0 && !m.player.IsPlaying() {
			cmd := m.playCurrentTrack()
			m.notifyMPRIS()
			return m, cmd
		}
		return m, nil

	case navArtistsLoadedMsg:
		m.navBrowser.artists = []navidrome.Artist(msg)
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

	case ytdlBatchMsg:
		// Discard stale responses from a previous batch session.
		if msg.gen != m.ytdlBatch.gen {
			return m, nil
		}
		m.ytdlBatch.loading = false
		if msg.err != nil {
			m.ytdlBatch.done = true
			m.status.text = fmt.Sprintf("Radio batch load failed: %v", msg.err)
			m.status.ttl = statusTTLBatch
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
			m.status.text = fmt.Sprintf("Loaded %d track(s)", len(msg.tracks))
			m.status.ttl = statusTTLDefault
			// Set up incremental loading for YouTube Radio playlists.
			// The source URLs are carried in the message so we don't
			// need to re-scan pendingURLs (which misses interactive loads).
			batchCmd := m.initYTDLBatch(msg.urls)
			if m.playlist.Len() > 0 && !m.player.IsPlaying() {
				playCmd := m.playCurrentTrack()
				m.notifyMPRIS()
				if batchCmd != nil {
					return m, tea.Batch(playCmd, batchCmd)
				}
				return m, playCmd
			}
			if batchCmd != nil {
				return m, batchCmd
			}
		} else {
			m.status.text = "No tracks found at URL."
			m.status.ttl = statusTTLDefault
		}
		return m, nil

	case netSearchLoadedMsg:
		if len(msg) > 0 {
			startIdx := m.playlist.Len()
			m.playlist.Add(msg...)
			for i := startIdx; i < m.playlist.Len(); i++ {
				m.playlist.Queue(i)
			}
			m.status.text = fmt.Sprintf("Added to Queue: %s", msg[0].DisplayName())
			m.status.ttl = statusTTLDefault
			if !m.player.IsPlaying() {

				cmd := m.playCurrentTrack()
				m.notifyMPRIS()
				return m, cmd
			}
		} else {
			m.status.text = "No tracks found online."
			m.status.ttl = statusTTLDefault
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
			m.status.text = "No audio files found"
			m.status.ttl = statusTTLDefault
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
		m.status.text = fmt.Sprintf("Added %d track(s)", len(msg.tracks))
		m.status.ttl = statusTTLDefault
		if !m.player.IsPlaying() && m.playlist.Len() > 0 {
			if msg.replace {
				m.playlist.SetIndex(0)
			}
			cmd := m.playCurrentTrack()
			m.notifyMPRIS()
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
		m.notifyMPRIS()
		return m, m.preloadNext()

	case streamPreloadedMsg:
		m.preloading = false
		return m, nil

	case ytdlSavedMsg:
		if msg.err != nil {
			m.status.text = fmt.Sprintf("Download failed: %s", msg.err)
		} else {
			m.status.text = fmt.Sprintf("Saved to %s", msg.path)
		}
		m.status.ttl = statusTTLMedium
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
		m.notifyMPRIS()
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
		return m, nil

	case mpris.PlayPauseMsg:
		cmd := m.togglePlayPause()
		m.notifyMPRIS()
		return m, cmd

	case mpris.NextMsg:
		m.scrobbleCurrent()
		cmd := m.nextTrack()
		m.notifyMPRIS()
		return m, cmd

	case mpris.PrevMsg:
		m.scrobbleCurrent()
		cmd := m.prevTrack()
		m.notifyMPRIS()
		return m, cmd

	case mpris.SeekMsg:
		offset := time.Duration(msg.Offset) * time.Microsecond
		m.player.Seek(offset)
		m.notifyMPRIS()
		if m.mpris != nil {
			m.mpris.EmitSeeked(m.player.Position().Microseconds())
		}
		return m, nil

	case mpris.SetPositionMsg:
		pos := time.Duration(msg.Position) * time.Microsecond
		m.player.Seek(pos - m.player.Position())
		m.notifyMPRIS()
		if m.mpris != nil {
			m.mpris.EmitSeeked(m.player.Position().Microseconds())
		}
		return m, nil

	case mpris.SetVolumeMsg:
		m.player.SetVolume(mpris.LinearToDb(msg.Volume))
		m.notifyMPRIS()
		return m, nil

	case mpris.StopMsg:
		m.player.Stop()
		m.notifyMPRIS()
		return m, nil

	case mpris.QuitMsg:
		m.player.Close()
		m.quitting = true
		return m, tea.Quit
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

// adjustScroll ensures plCursor is visible in the playlist view.
// It accounts for album separator lines that reduce the number of
// tracks that fit in the visible window.
func (m *Model) adjustScroll() {
	tracks := m.playlist.Tracks()
	if len(tracks) == 0 {
		return
	}
	// Scrolling up: cursor above the scroll window.
	if m.plCursor < m.plScroll {
		m.plScroll = m.plCursor
		return
	}
	// Scrolling down: check if cursor is still within the visible area.
	// Count rendered lines from plScroll up to and including plCursor.
	lines := renderedLineCount(tracks, m.plScroll, m.plCursor+1)
	if lines <= m.plVisible {
		return // cursor is visible, nothing to do
	}
	// Cursor has scrolled past the visible area. Walk backward from
	// plCursor to find the scroll offset that fits it on screen.
	m.plScroll = m.plCursor
	lines = 1 // the cursor track itself
	for i := m.plCursor - 1; i >= 0; i-- {
		add := 1 // track line
		if tracks[i+1].Album != "" && tracks[i+1].Album != tracks[i].Album {
			add++ // separator above track i+1
		}
		if lines+add > m.plVisible {
			break
		}
		lines += add
		m.plScroll = i
	}
	// Account for separator at the top of the window.
	if m.plScroll > 0 && tracks[m.plScroll].Album != "" && tracks[m.plScroll].Album != tracks[m.plScroll-1].Album {
		// There's a separator above plScroll — if it would overflow, bump scroll down.
		if lines+1 > m.plVisible {
			m.plScroll++
		}
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
	info := mpris.TrackInfo{
		Title:       track.Title,
		Artist:      track.Artist,
		Album:       track.Album,
		Genre:       track.Genre,
		TrackNumber: track.TrackNumber,
		URL:         track.Path,
		Length:      m.player.Duration().Microseconds(),
	}
	// Override with ICY stream title for radio streams (format: "Artist - Title").
	if m.streamTitle != "" && track.Stream {
		if artist, title, ok := strings.Cut(m.streamTitle, " - "); ok {
			info.Artist = artist
			info.Title = title
		} else {
			info.Title = m.streamTitle
		}
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
	// Navidrome streams have NavidromeID set — those track position correctly.
	if track.Stream && track.NavidromeID == "" {
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
//   - the track has a NavidromeID (i.e. it came from Navidrome)
//   - elapsed is at least 50% of the track's known duration
//
// The call is dispatched in a goroutine so it never blocks the UI.
func (m *Model) maybeScrobble(track playlist.Track, elapsed, duration time.Duration) {
	if m.navClient == nil || !m.navScrobbleEnabled {
		return
	}
	if track.NavidromeID == "" {
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
	id := track.NavidromeID
	go m.navClient.Scrobble(id, true)
}

// nowPlaying fires a now-playing notification for the given track if configured.
func (m *Model) nowPlaying(track playlist.Track) {
	if m.navClient == nil || !m.navScrobbleEnabled || track.NavidromeID == "" {
		return
	}
	go m.navClient.Scrobble(track.NavidromeID, false)
}
