// Package ui implements the Bubbletea TUI for the CLIAMP terminal music player.
package model

import (
	"time"

	"cliamp/luaplugin"
	"cliamp/mpris"
	"cliamp/player"
	"cliamp/playlist"
	"cliamp/theme"
	"cliamp/ui"
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

type topLevelScreen int

const (
	screenMain topLevelScreen = iota
	screenKeymap
	screenThemePicker
	screenDevicePicker
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

// Model is the Bubbletea model for the CLIAMP TUI.
type Model struct {
	// Core playback
	player        player.Engine
	playlist      *playlist.Playlist
	configSaver   ConfigSaver
	vis           *ui.Visualizer
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
	providerLists []playlist.PlaylistInfo
	provCursor    int
	provScroll    int
	provLoading   bool
	provSignIn    bool            // true when provider needs interactive sign-in
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
	lyrics         lyricsState
	keymap         keymapOverlay
	queue          queueOverlay
	plManager      plManagerState
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

	loadedPlaylist string // name of the currently loaded local playlist (for resume)

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

	// MPRIS D-Bus service (nil on non-Linux or if D-Bus unavailable)
	mpris *mpris.Service

	// Lua plugin manager (nil if no plugins loaded)
	luaMgr *luaplugin.Manager

	// Theme state: -1 = Default (ANSI), 0+ = index into themes
	themes   []theme.Theme
	themeIdx int

	// Track info overlay (metadata details)
	showInfo bool

	// Audio device picker overlay
	devicePicker devicePickerState

	// Full-screen visualizer mode (Shift+V)
	fullVis bool

	autoPlay       bool // start playing immediately on launch
	compact        bool // compact mode: cap frame width at 80 columns
	heightExpanded bool // tracks whether manual 'x' expansion is active

	// Cached per-tick to avoid repeated speaker.Lock() calls in View().
	cachedPos  time.Duration
	cachedDur  time.Duration
	lastTickAt time.Time // wall time of previous tickMsg; used for tick delta

}

func (m Model) activeScreen() topLevelScreen {
	switch {
	case m.keymap.visible:
		return screenKeymap
	case m.themePicker.visible:
		return screenThemePicker
	case m.devicePicker.visible:
		return screenDevicePicker
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
