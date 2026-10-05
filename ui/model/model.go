// Package ui implements the Bubbletea TUI for the CLIAMP terminal music player.
package model

import (
	"strings"
	"sync/atomic"
	"time"

	"github.com/bjarneo/cliamp/external/radio"
	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/theme"
	"github.com/bjarneo/cliamp/ui"
)

// ConfigSaver persists top-level config keys with typed values.
// Satisfied by config.SaveFunc (the default) or a test stub.
type ConfigSaver interface {
	SaveString(key, value string) error
	SaveBool(key string, value bool) error
	SaveFloat(key string, value float64, prec int) error
	SaveFloats(key string, values []float64) error
}

// ResumeSaver persists the active track, timeline position, and source context.
type ResumeSaver func(track playlist.Track, positionSec int, context []playlist.Track, contextIndex int)

// The saveConfig* helpers persist a top-level config key and show a write
// failure in the status line. They return the error, so an IPC job can fail
// too. They do nothing when no saver is wired, so tests can change settings
// without touching the config file.

func (m *Model) saveConfigString(key, value string) error {
	if m.configSaver == nil {
		return nil
	}
	return m.reportConfigSave(m.configSaver.SaveString(key, value))
}

func (m *Model) saveConfigBool(key string, value bool) error {
	if m.configSaver == nil {
		return nil
	}
	return m.reportConfigSave(m.configSaver.SaveBool(key, value))
}

func (m *Model) saveConfigFloat(key string, value float64, prec int) error {
	if m.configSaver == nil {
		return nil
	}
	return m.reportConfigSave(m.configSaver.SaveFloat(key, value, prec))
}

func (m *Model) saveConfigFloats(key string, values []float64) error {
	if m.configSaver == nil {
		return nil
	}
	return m.reportConfigSave(m.configSaver.SaveFloats(key, values))
}

func (m *Model) reportConfigSave(err error) error {
	if err != nil {
		m.status.Errorf(statusTTLDefault, "Config save failed: %s", err)
	}
	return err
}

type focusArea int

const (
	focusPlaylist focusArea = iota
	focusProvPill
	focusVolume
	focusEQ
	focusShuffle
	focusRepeat
	focusSpeed
	focusSearch
	focusProvider
	focusNetSearch
)

func (f focusArea) label() string {
	switch f {
	case focusPlaylist:
		return "Playlist"
	case focusVolume:
		return "Volume"
	case focusEQ:
		return "Equalizer"
	case focusShuffle:
		return "Shuffle"
	case focusRepeat:
		return "Repeat"
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
	if m.simplified || m.layout.tier == layoutMinimal || m.layout.tier == layoutTooSmall {
		return areas
	}
	if m.layout.twoColumn {
		rows := m.effectivePlaylistVisible()
		for _, row := range m.settingsPaneRows(rows - m.metadataPaneRows(rows)) {
			if row.focus != focusPlaylist {
				areas = append(areas, row.focus)
			}
		}
		return areas
	}
	if len(m.providers) > 1 {
		areas = append(areas, focusProvPill)
	}
	areas = append(areas, focusVolume)
	// The closed pane keeps source/volume and the playlist mode badges, but
	// draws no EQ or speed readout. Both stay reachable by their global keys.
	if !m.layout.closedSettings {
		areas = append(areas, focusEQ)
	}
	if m.playlist != nil {
		// Probe each focused badge: its extra label must fit too, independent
		// of the current focus or an overlay temporarily replacing the header.
		for _, area := range []focusArea{focusShuffle, focusRepeat} {
			m.focus = area
			if strings.Contains(m.renderPlaybackHeader(), "["+area.label()+" ") {
				areas = append(areas, area)
			}
		}
	}
	if !m.layout.closedSettings {
		areas = append(areas, focusSpeed)
	}
	return areas
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
	for _, focus := range []*focusArea{&m.focus, &m.prevFocus} {
		switch *focus {
		case focusProvPill, focusVolume, focusEQ, focusShuffle, focusRepeat, focusSpeed:
			if !m.mainFocusAllowed(*focus) {
				*focus = focusPlaylist
			}
		}
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
	screenSearchOverlay
	screenQueue
	screenSubs
	screenInfo
	screenSearch
	screenNetSearch
	screenURLInput
	screenLyrics
	screenJump
	// screenFullVisualizer stays last. TestOverlayStackOrder checks that
	// overlayStack holds each screen up to it.
	screenFullVisualizer
)

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
		return "Playlist"
	case screenSearchOverlay, screenNetSearch:
		return "Search"
	case screenQueue:
		return "Queue"
	case screenSubs:
		return "Subscriptions"
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
	plMgrScreenDirs
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
	navBrowseModeByGenre                                // genre list → latest/popular → track list
)

// navBrowseScreenType identifies which screen within the active browse mode is shown.
type navBrowseScreenType int

const (
	navBrowseScreenList   navBrowseScreenType = iota // first-level list (artists or albums)
	navBrowseScreenAlbums                            // artist's albums (ArtistAlbum mode only)
	navBrowseScreenTracks                            // final song list in any mode
)

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
	downloadsDirectory string
	// audioDevice names the output device that the last device switch or
	// list reported. The runtime snapshot shows it.
	audioDevice string
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
	titleOff        int       // scroll offset for the now-playing marquee
	titleLastScroll time.Time // last time the title scrolled
	err             error
	quitting        bool
	width           int
	height          int
	layout          frameLayout
	textInput       textEditor
	playlistUndo    playlistUndo

	// Provider state
	provider                playlist.Provider
	localProvider           playlist.Provider // local playlist provider for file-based playlist management (always available)
	provPane                providerPane
	openDefaultProviderOnce bool             // open the provider's preferred hierarchy after Init
	providers               []provider.Entry // all available providers
	provPillIdx             int              // selected pill index
	eqPresetIdx             int              // -1 = custom, 0+ = index into eqPresets
	eqCustomLabel           string           // non-empty = plugin-defined preset label (shown instead of "Custom")
	eqCustomBands           [eqBandCount]float64

	// Overlay / feature state (see state.go for struct definitions)
	search          searchState
	netSearch       netSearchState
	provSearch      provSearchState
	seek            seekState
	themePicker     themePickerState
	visPicker       visPickerState
	lyrics          lyricsState
	keymap          keymapOverlay
	queue           queueOverlay
	subs            subsOverlay
	plManager       plManagerState
	plPicker        playlistPickerState
	searchOverlay   searchOverlayState
	fileBrowser     fileBrowserState
	navBrowser      navBrowserState
	catalogBatch    catalogBatchState
	ytdlBatch       ytdlBatchState
	reconnect       reconnectState
	save            saveState
	status          statusMsg
	logLines        []logLine
	network         networkStats
	requests        requestState
	speedSaveAfter  time.Duration
	eqSaveAfter     time.Duration
	volumeSaveAfter time.Duration
	termTitle       terminalTitleState

	jump     jumpState
	urlInput urlInputState

	// Async feed/M3U URL resolution
	pendingURLs []string
	feedLoading bool

	spinnerTicking bool // a spinnerTickMsg is pending

	visVolumeLinked bool // when true, visualizer samples are scaled by volume gain
	visRows         int  // configured visualizer height at the full tier; 0 uses ui.DefaultVisRows
	paddingH        int  // configured frame padding left and right; used when paddingSet
	paddingV        int  // configured frame padding above and below; used when paddingSet
	paddingSet      bool // false uses defaultPaddingH and defaultPaddingV

	// Async stream buffering (true while HTTP connect is in progress)
	buffering   bool
	bufferingAt time.Time // when buffering started, for elapsed display

	// resume holds the path and position to seek to when the matching track
	// starts playing. Cleared after the seek is performed.
	resume struct {
		path string
		secs int
	}

	// playbackContext is the complete list the active track was chosen from,
	// such as every track in an album opened through provider navigation.
	playbackContext      []playlist.Track
	playbackContextIndex int
	resumeSaver          ResumeSaver
	lastResumeSave       time.Time

	lastProgressReport time.Time // last interim provider progress report

	// loadedPlaylist names the saved local list that the queue mirrors, for
	// resume. When it is set, the ♥ rule treats station rows as tracks.
	// Write-backs read writableLoadedPlaylist, which excludes Favorites.
	loadedPlaylist string

	// playlistSource names the provider list that an IPC load put in the
	// queue, as key:id or key:album:id, when loadedPlaylist is empty. Only
	// the runtime snapshot reads it.
	playlistSource string

	// activeProviderPlaylistID is the ID of the most recently loaded playlist
	// from a non-local provider (Spotify, Navidrome, …). Used to highlight that
	// row in the provider browser. Empty when no provider playlist is active.
	activeProviderPlaylistID string

	// radioListeners holds live listener counts per cliamp radio channel slug.
	// Nil means unknown: the fetch never ran or failed, and rows show no
	// counts rather than zero. radioListenersAt stamps the last fetch so a
	// failed fetch backs off instead of retrying on every pane load.
	radioListeners   map[string]int
	radioListenersAt time.Time

	// exitResume holds the playback state captured just before player.Close()
	// so ResumeState() can read it after the player is shut down.
	exitResume struct {
		path         string
		secs         int
		playlist     string
		context      []playlist.Track
		contextIndex int
	}

	// preloading is true while a preloadStreamCmd goroutine is in-flight.
	preloading bool
	// preloadFor is the path of the armed or in-flight preload.
	preloadFor string
	// preloadFailed is the path of a track whose preload failed. It is not
	// retried until a new track starts.
	preloadFailed string

	// Live stream title from ICY metadata (e.g., "Artist - Song")
	streamTitle string

	// playingTrack is the track currently owned by the audio engine. It can differ
	// from playlist.Current() after browsing loads a new provider playlist while
	// the old track keeps playing.
	playingTrack       playlist.Track
	playingTrackActive bool
	// playingTrackStarted is set once the engine has started playingTrack and
	// track.change has fired. It stays false while a stream buffers or after a
	// start failed, so those never count as a finished track.
	playingTrackStarted bool
	// playingTrackLeft is set once leaveTrack has reported playingTrack, so
	// a stop or a start that follows does not report it again.
	playingTrackLeft bool
	playbackDetached bool
	// playingProvider names the provider that was active when the playing
	// track started, so a label for it stays right after the listener
	// switches providers while it keeps playing.
	playingProvider string

	notifier playback.Notifier
	// notice is the playback state that the notifier and the playback.state
	// plugin event got last. Update keeps it in the Model that it returns.
	notice playbackNotice

	// Lua plugin manager (nil if no plugins loaded)
	luaMgr *luaplugin.Manager

	// pluginEmit tracks last-emitted player/queue state so Update can fire
	// delta events to plugins from one place. Held behind a pointer so the
	// snapshot survives Update's value-receiver copy.
	pluginEmit *pluginEmitState

	// pluginState holds the state that Lua plugins read. It is nil when no
	// plugin is loaded. See publishPluginState.
	pluginState *atomic.Pointer[PluginState]

	// ipcRuntime publishes GUI-facing runtime snapshots from the Update owner.
	// It is shared by value-receiver copies of Model.
	ipcRuntime *ipcRuntimeState

	// historyStore records Recently Played. It is nil when the config
	// directory is unavailable. buildProviders in package main shares it with
	// the local provider.
	historyStore *history.Store

	// favStore holds the ♥ favorites. It is nil when the config directory is
	// unavailable. buildProviders in package main shares it with the local
	// provider, which lists it as the Favorites playlist.
	favStore *favorites.Store
	// favSync orders the calls that copy favorite changes to providers. It
	// is created on first use and shared across Model value copies.
	favSync *favorites.SyncQueue
	// reports orders the now-playing, progress and scrobble reports to
	// providers. It is shared across Model value copies.
	reports *reportQueue

	// Local station favorites. A directory radio station row shows these
	// instead of the track favorites in favSet.
	radioFavorites *radio.Favorites

	// favSet is a cached set of favorited paths for O(1) lookup during
	// rendering. Refreshed on init and after every toggle.
	favSet map[string]struct{}

	// initialDir is the starting path for the file browser ('o' key).
	initialDir string

	// Theme state: -1 = Default (ANSI), 0+ = index into themes
	themes   []theme.Theme
	themeIdx int

	info infoOverlay

	showAlbumHeaders bool
	headerManual     bool
	// tracksPaging is true while a progressive track load still has pages in
	// flight. Each page remixes the upcoming order, so preloading is held off
	// until the order settles. A frontier-EOF deferral would use this too.
	tracksPaging bool
	// Running counters for the cohesion heuristic so Add can update header
	// visibility in O(k) instead of walking the whole playlist on each call.
	headerLastAlbum string
	headerSegments  int
	headerTracks    int

	// Audio device picker overlay
	devicePicker devicePickerState

	// Full-screen visualizer mode (Shift+V)
	fullVis bool

	autoPlay        bool // start playing immediately on launch
	lowPower        bool // lower UI/render cadences in low-power mode
	headless        bool // no screen: cliamp --daemon runs without a renderer
	visualizer60FPS bool // render a visible visualizer at the animation cadence
	simplified      bool // simplified playback view: track summary and time strip
	hideTrackInfo   bool // full-screen visualizer: show the source instead of the track
	hideHelpBar     bool // hide the key-binding hint bar above the status line
	hideSettings    bool // close the two-column settings pane beside the playlist
	showMetadata    bool // expand highlighted-track metadata below settings
	heightExpanded  bool // tracks whether manual 'x' expansion is active

	// Cached per-tick to avoid repeated speaker.Lock() calls in View().
	cachedPos  time.Duration
	cachedDur  time.Duration
	lastTickAt time.Time // wall time of previous tickMsg; used for tick delta

}

// activeScreen returns the screen of the top open overlay, or screenMain.
func (m Model) activeScreen() topLevelScreen {
	if spec, ok := m.topOverlay(); ok {
		return spec.screen
	}
	return screenMain
}

// usesContentFirstLayout gives list-heavy tasks more room while preserving a
// compact now-playing summary. The top overlay decides, as it decides the
// keys and the render. With no overlay, the provider pane is content-first.
func (m Model) usesContentFirstLayout() bool {
	screen := m.activeScreen()
	if screen == screenMain {
		return m.focus == focusProvider
	}
	return m.overlayContentFirst(screen)
}

// usesSimplifiedLayout applies the sparse playback chrome only to the main
// playlist view. Provider browsing and overlays retain their normal space.
func (m Model) usesSimplifiedLayout() bool {
	return m.simplified && m.activeScreen() == screenMain && m.focus != focusProvider
}

func (m Model) visualizerDisabled() bool {
	return m.vis == nil || m.vis.Mode == ui.VisNone
}

func (m Model) isPlaying() bool {
	return m.player != nil && m.player.IsPlaying()
}

func (m Model) isPaused() bool {
	return m.player != nil && m.player.IsPaused()
}
