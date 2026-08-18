// Package ui implements the Bubbletea TUI for the CLIAMP terminal music player.
package model

import (
	"time"

	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/theme"
	"github.com/bjarneo/cliamp/ui"
)

// ConfigSaver persists individual config key-value pairs.
// Satisfied by config.SaveFunc (the default) or a test stub.
type ConfigSaver interface {
	Save(key, value string) error
}

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

func (f focusArea) label() string {
	switch f {
	case focusPlaylist:
		return "Playlist"
	case focusEQ:
		return "Equalizer"
	case focusSpeed:
		return "Speed"
	case focusProvPill:
		return "Source"
	case focusProvider:
		return "Provider"
	case focusSearch:
		return "Search"
	case focusNetSearch:
		return "Online Search"
	default:
		return ""
	}
}

// mainFocusAreas returns only controls rendered in the current density tier.
// Secondary screens own their input independently, so this applies to the
// main playback screen and the provider playlist pane.
func (m Model) mainFocusAreas() []focusArea {
	areas := []focusArea{focusPlaylist}
	if m.layout.tier == layoutMinimal || m.layout.tier == layoutTooSmall {
		return areas
	}
	areas = append(areas, focusEQ)
	if len(m.providers) > 1 {
		areas = append(areas, focusProvPill)
	}
	return append(areas, focusSpeed)
}

func (m Model) mainFocusAllowed(focus focusArea) bool {
	for _, area := range m.mainFocusAreas() {
		if area == focus {
			return true
		}
	}
	return false
}

func (m Model) nextMainFocus(current focusArea) focusArea {
	areas := m.mainFocusAreas()
	for i, area := range areas {
		if area == current {
			return areas[(i+1)%len(areas)]
		}
	}
	return areas[0]
}

func (m Model) previousMainFocus(current focusArea) focusArea {
	areas := m.mainFocusAreas()
	for i, area := range areas {
		if area == current {
			return areas[(i+len(areas)-1)%len(areas)]
		}
	}
	return areas[0]
}

// normalizeMainFocus clears a focus restored from a wider terminal when its
// control is not rendered at the current size.
func (m *Model) normalizeMainFocus() {
	if (m.focus == focusEQ || m.focus == focusSpeed || m.focus == focusProvPill) && !m.mainFocusAllowed(m.focus) {
		m.focus = focusPlaylist
	}
	if (m.prevFocus == focusEQ || m.prevFocus == focusSpeed || m.prevFocus == focusProvPill) && !m.mainFocusAllowed(m.prevFocus) {
		m.prevFocus = focusPlaylist
	}
}

type topLevelScreen int

const (
	screenMain topLevelScreen = iota
	screenKeymap
	screenThemePicker
	screenVisPicker
	screenDevicePicker
	screenPlaylistPicker
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
	// Every overlay now renders inline in the playlist region while the
	// now-playing, visualizer, and controls chrome stays visible above it, so
	// the visualizer is never fully hidden.
	return false
}

func (s topLevelScreen) label() string {
	switch s {
	case screenKeymap:
		return "Keys"
	case screenThemePicker:
		return "Themes"
	case screenVisPicker:
		return "Visualizers"
	case screenDevicePicker:
		return "Audio Device"
	case screenPlaylistPicker:
		return "Save to Playlist"
	case screenFileBrowser:
		return "Files"
	case screenNavBrowser:
		return "Browse"
	case screenPlaylistManager:
		return "Playlists"
	case screenSpotSearch, screenNetSearch:
		return "Search"
	case screenQueue:
		return "Queue"
	case screenInfo:
		return "Track Info"
	case screenSearch:
		return "Filter"
	case screenURLInput:
		return "Load URL"
	case screenLyrics:
		return "Lyrics"
	case screenJump:
		return "Jump to Time"
	case screenFullVisualizer:
		return "Visualizer"
	default:
		return ""
	}
}

// maxPlVisible caps the playlist at a readable height even on tall terminals.
// maxPlExpandVisible is the higher cap used by content-first list screens.
const (
	maxPlVisible       = 12
	maxPlExpandVisible = 24
)

type plMgrScreenType int

const (
	plMgrScreenList plMgrScreenType = iota
	plMgrScreenTracks
	plMgrScreenNewName
	plMgrScreenRename
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

// statusTTL* constants define how long a status message is shown.
const (
	statusTTLShort   statusTTL = statusTTL(2 * time.Second)         // brief confirmations
	statusTTLDefault statusTTL = statusTTL(3 * time.Second)         // standard status messages
	statusTTLMedium  statusTTL = statusTTL(4 * time.Second)         // messages needing extra visibility
	statusTTLBatch   statusTTL = statusTTL(4500 * time.Millisecond) // batch operation feedback
	statusTTLLong    statusTTL = statusTTL(6 * time.Second)         // loading indicators
)

// Model is the Bubbletea model for the CLIAMP TUI.
type Model struct {
	// Core playback
	player        player.Engine
	playlist      *playlist.Playlist
	configSaver   ConfigSaver
	vis           *ui.Visualizer
	seekStepLarge time.Duration
	pausedAt      time.Time

	// Primed Nj seek: digit sets pct, next `j` completes.
	pendingSeekActive    bool
	pendingSeekPct       int
	pendingSeekExpiresAt time.Time

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
	layout          frameLayout
	textInput       textEditor
	playlistUndo    playlistUndo

	// Provider state
	provider      playlist.Provider
	localProvider playlist.Provider // local playlist provider for file-based playlist management (always available)
	providerLists []playlist.PlaylistInfo
	provCursor    int
	provScroll    int
	provLoading   bool
	provSignIn    bool            // true when provider needs interactive sign-in
	provAuthURL   string          // OAuth URL to display while interactive auth is in flight
	providers     []ProviderEntry // all available providers
	provPillIdx   int             // selected pill index
	eqPresetIdx   int             // -1 = custom, 0+ = index into eqPresets
	eqCustomLabel string          // non-empty = plugin-defined preset label (shown instead of "Custom")

	// Overlay / feature state (see state.go for struct definitions)
	search         searchState
	netSearch      netSearchState
	provSearch     provSearchState
	seek           seekState
	themePicker    themePickerState
	visPicker      visPickerState
	lyrics         lyricsState
	keymap         keymapOverlay
	queue          queueOverlay
	plManager      plManagerState
	plPicker       playlistPickerState
	spotSearch     spotSearchState
	fileBrowser    fileBrowserState
	navBrowser     navBrowserState
	catalogBatch   catalogBatchState
	ytdlBatch      ytdlBatchState
	reconnect      reconnectState
	save           saveState
	status         statusMsg
	logLines       []logLine
	network        networkStats
	requests       requestState
	speedSaveAfter time.Duration
	eqSaveAfter    time.Duration
	termTitle      terminalTitleState

	// Jump to time mode
	jumping   bool
	jumpInput string
	jumpErr   string

	// URL input mode (load playlist/stream URL at runtime)
	urlInputting bool
	urlInput     string
	urlErr       string

	// Async feed/M3U URL resolution
	pendingURLs []string
	feedLoading bool

	visVolumeLinked bool // when true, visualizer samples are scaled by volume gain

	// Async stream buffering (true while HTTP connect is in progress)
	buffering   bool
	bufferingAt time.Time // when buffering started, for elapsed display

	// resume holds the path and position to seek to when the matching track
	// starts playing. Cleared after the seek is performed.
	resume struct {
		path string
		secs int
	}

	lastProgressReport time.Time // last interim provider progress report

	loadedPlaylist string // name of the currently loaded local playlist (for resume)

	// activeProviderPlaylistID is the ID of the most recently loaded playlist
	// from a non-local provider (Spotify, Navidrome, …). Used to highlight that
	// row in the provider browser. Empty when no provider playlist is active.
	activeProviderPlaylistID string

	// exitResume holds the playback state captured just before player.Close()
	// so ResumeState() can read it after the player is shut down.
	exitResume struct {
		path     string
		secs     int
		playlist string
	}

	// preloading is true while a preloadStreamCmd goroutine is in-flight.
	preloading bool

	// Live stream title from ICY metadata (e.g., "Artist - Song")
	streamTitle string

	// playingTrack is the track currently owned by the audio engine. It can differ
	// from playlist.Current() after browsing loads a new provider playlist while
	// the old track keeps playing.
	playingTrack       playlist.Track
	playingTrackActive bool
	playbackDetached   bool

	notifier playback.Notifier

	// Lua plugin manager (nil if no plugins loaded)
	luaMgr *luaplugin.Manager

	// pluginEmit tracks last-emitted player/queue state so Update can fire
	// delta events to plugins from one place. Held behind a pointer so the
	// snapshot survives Update's value-receiver copy.
	pluginEmit *pluginEmitState

	// History recorder (nil if config dir unavailable; safe to call when nil)
	historyStore *history.Store

	// initialDir is the starting path for the file browser ('o' key).
	initialDir string

	// Theme state: -1 = Default (ANSI), 0+ = index into themes
	themes   []theme.Theme
	themeIdx int

	// Track info overlay (metadata details)
	showInfo   bool
	infoScroll int

	showAlbumHeaders bool
	headerManual     bool
	// Running counters for the cohesion heuristic so Add can update header
	// visibility in O(k) instead of walking the whole playlist on each call.
	headerLastAlbum string
	headerSegments  int
	headerTracks    int

	// Audio device picker overlay
	devicePicker devicePickerState

	// Full-screen visualizer mode (Shift+V)
	fullVis bool

	autoPlay       bool // start playing immediately on launch
	lowPower       bool // lower UI/render cadences in low-power mode
	compact        bool // compact mode: cap frame width at 80 columns
	heightExpanded bool // tracks whether manual 'x' expansion is active

	// Cached per-tick to avoid repeated speaker.Lock() calls in View().
	cachedPos  time.Duration
	cachedDur  time.Duration
	lastTickAt time.Time // wall time of previous tickMsg; used for tick delta

}

func (m Model) activeScreen() topLevelScreen {
	switch {
	case m.fullVis:
		return screenFullVisualizer
	case m.keymap.visible:
		return screenKeymap
	case m.devicePicker.visible:
		return screenDevicePicker
	case m.plPicker.visible:
		return screenPlaylistPicker
	case m.fileBrowser.visible:
		return screenFileBrowser
	case m.spotSearch.visible:
		return screenSpotSearch
	case m.navBrowser.visible:
		return screenNavBrowser
	case m.themePicker.visible:
		return screenThemePicker
	case m.visPicker.visible:
		return screenVisPicker
	case m.plManager.visible:
		return screenPlaylistManager
	case m.queue.visible:
		return screenQueue
	case m.showInfo:
		return screenInfo
	case m.lyrics.visible:
		return screenLyrics
	case m.jumping:
		return screenJump
	case m.urlInputting:
		return screenURLInput
	case m.search.active:
		return screenSearch
	case m.netSearch.active:
		return screenNetSearch
	default:
		return screenMain
	}
}

// isOverlayActive reports whether an overlay suppresses the live main view.
// Overlays now render inline over the live view (see hidesVisualizer), so this
// is always false; it is kept as the single seam the tick loop gates on.
func (m Model) isOverlayActive() bool {
	return false
}

// usesContentFirstLayout gives list-heavy tasks more room while preserving a
// compact now-playing summary. The visualizer picker deliberately keeps the
// normal playback chrome for live previews.
func (m Model) usesContentFirstLayout() bool {
	if m.activeScreen() == screenMain && m.focus == focusProvider {
		return true
	}
	if m.keymap.visible || m.devicePicker.visible || m.fileBrowser.visible ||
		m.navBrowser.visible || m.themePicker.visible || m.queue.visible ||
		m.search.active {
		return true
	}
	if m.plPicker.visible && m.plPicker.screen == plPickerChoose {
		return true
	}
	if m.spotSearch.visible && (m.spotSearch.screen == spotSearchResults || m.spotSearch.screen == spotSearchPlaylist) {
		return true
	}
	if m.plManager.visible && (m.plManager.screen == plMgrScreenList || m.plManager.screen == plMgrScreenTracks) {
		return true
	}
	return m.netSearch.active && m.netSearch.screen == netSearchResults
}

func (m Model) isPlaying() bool {
	return m.player != nil && m.player.IsPlaying()
}

func (m Model) isPaused() bool {
	return m.player != nil && m.player.IsPaused()
}
