// Package ui implements the Bubbletea TUI for the CLIAMP terminal music player.
package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"cliamp/config"
	"cliamp/external/local"
	"cliamp/external/navidrome"
	"cliamp/mpris"
	"cliamp/player"
	"cliamp/playlist"
	"cliamp/resolve"
	"cliamp/theme"
)

type focusArea int

const (
	focusPlaylist focusArea = iota
	focusEQ
	focusSearch
	focusProvider
	focusNetSearch
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

type tickMsg time.Time
type autoPlayMsg struct{}

// Tick intervals: fast for visualizer animation, slow for time/seek display.
const (
	tickFast = 50 * time.Millisecond  // 20 FPS — visualizer active
	tickSlow = 200 * time.Millisecond // 5 FPS — visualizer off or overlay
)

// streamPreloadLeadTime is how far before the end of a stream we arm the
// gapless next pipeline. Opening the preload HTTP connection too early can
// cause the server to close the current stream (e.g., per-user concurrent
// stream limits on Navidrome), which makes the mp3 decoder error out and
// triggers a premature gapless transition. 3 seconds is short enough that
// most servers won't enforce a concurrency limit for such a brief overlap,
// and any resulting early skip is imperceptible (≤3 s from the true end).
const streamPreloadLeadTime = 3 * time.Second

// Model is the Bubbletea model for the CLIAMP TUI.
type Model struct {
	player    *player.Player
	playlist  *playlist.Playlist
	vis       *Visualizer
	focus     focusArea
	eqCursor  int // selected EQ band (0-9)
	plCursor  int // selected playlist item
	plScroll  int // scroll offset for playlist view
	plVisible int // max visible playlist items
	titleOff  int // scroll offset for long track titles
	err       error
	quitting  bool
	width     int
	height    int

	provider      playlist.Provider
	localProvider *local.Provider // direct ref for write operations (add-to-playlist)
	providerLists []playlist.PlaylistInfo
	provCursor    int
	provLoading   bool
	// EQ preset state (-1 = custom, 0+ = index into eqPresets)
	eqPresetIdx int

	// Keymap overlay
	showKeymap     bool
	keymapCursor   int
	keymapSearch   string
	keymapFiltered []int // indices into keymapEntries

	// Search mode state
	searching     bool
	searchQuery   string
	searchResults []int // indices into playlist tracks
	searchCursor  int
	prevFocus     focusArea // focus to restore on cancel

	// Dynamic internet search
	netSearching   bool
	netSearchQuery string

	// Async feed/M3U URL resolution
	pendingURLs []string
	feedLoading bool

	// Async stream buffering (true while HTTP connect is in progress)
	buffering bool

	// preloading is true while a preloadStreamCmd goroutine is in-flight.
	// It prevents the tick-loop from dispatching a second concurrent preload
	// for the same next track before the first HTTP connection is armed in
	// the gapless streamer.
	preloading bool

	// Live stream title from ICY metadata (e.g., "Artist - Song")
	streamTitle string

	// Temporary status message (e.g., "Saved to ...")
	saveMsg    string
	saveMsgTTL int // ticks remaining before clearing

	// Network throughput tracking for stream status bar.
	networkSpeed   float64 // bytes per second (smoothed)
	lastSpeedBytes int64
	lastSpeedTick  int // tick counter for sampling interval

	// MPRIS D-Bus service (nil on non-Linux or if D-Bus unavailable)
	mpris *mpris.Service

	// Theme state: -1 = Default (ANSI), 0+ = index into themes
	themes        []theme.Theme
	themeIdx      int
	showThemes    bool // theme picker overlay visible
	themeCursor   int  // cursor in theme picker (0 = Default, 1+ = themes[i-1])
	themeSavedIdx int  // themeIdx before opening picker, for cancel/restore

	// Track info overlay (metadata details)
	showInfo bool

	// Full-screen visualizer mode (Shift+V)
	fullVis bool

	// Queue manager overlay
	showQueue   bool
	queueCursor int

	// Playlist manager overlay (browse, add, remove, delete playlists)
	showPlManager    bool // overlay visible
	plMgrScreen      plMgrScreenType
	plMgrCursor      int
	plMgrPlaylists   []playlist.PlaylistInfo
	plMgrSelPlaylist string           // playlist name open in screen 1
	plMgrTracks      []playlist.Track // tracks in the selected playlist
	plMgrNewName     string
	plMgrConfirmDel  bool

	autoPlay bool // start playing immediately on launch

	// File browser overlay
	showFileBrowser bool
	fbDir           string
	fbEntries       []fbEntry
	fbCursor        int
	fbSelected      map[string]bool
	fbErr           string

	// Navidrome explore browser overlay
	showNavBrowser     bool
	navClient          *navidrome.NavidromeClient // nil when Navidrome is not configured
	navScrobbleEnabled bool                       // mirrors NavidromeConfig.ScrobbleEnabled(); set at init
	navMode            navBrowseModeType
	navScreen          navBrowseScreenType
	navCursor          int
	navScroll          int
	navArtists         []navidrome.Artist
	navAlbums          []navidrome.Album
	navTracks          []playlist.Track
	navSelArtist       navidrome.Artist
	navSelAlbum        navidrome.Album
	navSortType        string // current album sort type, persisted to config
	navAlbumLoading    bool   // true while an album page fetch is in progress
	navAlbumDone       bool   // true once the server signals no more pages (isLast)
	navLoading         bool   // general loading flag for artists/tracks
	navSearching       bool   // true while the nav search bar is open
	navSearch          string // current nav search query
	navSearchIdx       []int  // filtered indices into the active list (nil = no filter)
}

// NewModel creates a Model wired to the given player and playlist.
// localProv is an optional direct reference to the local provider for write ops.
// navCfg is the Navidrome config used to seed the initial browse sort preference.
// nav is the raw NavidromeClient (may be nil); stored directly so the browser
// key handler doesn't have to unwrap a CompositeProvider.
func NewModel(p *player.Player, pl *playlist.Playlist, prov playlist.Provider, localProv *local.Provider, themes []theme.Theme, navCfg config.NavidromeConfig, nav *navidrome.NavidromeClient) Model {
	sortType := navCfg.BrowseSort
	if sortType == "" {
		sortType = navidrome.SortAlphabeticalByName
	}
	m := Model{
		player:             p,
		playlist:           pl,
		vis:                NewVisualizer(float64(p.SampleRate())),
		plVisible:          5,
		eqPresetIdx:        -1, // custom until a preset is selected
		themes:             themes,
		themeIdx:           -1, // Default (ANSI)
		localProvider:      localProv,
		navSortType:        sortType,
		navClient:          nav,
		navScrobbleEnabled: navCfg.ScrobbleEnabled(),
	}
	if prov != nil {
		m.provider = prov
	}
	return m
}

// SetAutoPlay makes the player start playback immediately on Init.
func (m *Model) SetAutoPlay(v bool) { m.autoPlay = v }

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
	return m.showKeymap || m.showThemes || m.showFileBrowser ||
		m.showNavBrowser || m.showPlManager || m.showQueue ||
		m.showInfo || m.searching || m.netSearching
}

// openThemePicker re-loads themes from disk (picking up new user files)
// and opens the theme selector overlay.
func (m *Model) openThemePicker() {
	m.themes = theme.LoadAll()
	m.showThemes = true
	m.themeSavedIdx = m.themeIdx
	// Position cursor on the currently active theme.
	// Picker list: 0 = Default, 1..N = themes[0..N-1]
	m.themeCursor = m.themeIdx + 1
}

// themePickerApply applies the theme under the cursor for live preview.
func (m *Model) themePickerApply() {
	if m.themeCursor == 0 {
		m.themeIdx = -1
		applyTheme(theme.Default())
	} else {
		m.themeIdx = m.themeCursor - 1
		applyTheme(m.themes[m.themeIdx])
	}
}

// themePickerSelect confirms the current selection and closes the picker.
func (m *Model) themePickerSelect() {
	m.themePickerApply()
	m.showThemes = false
}

// themePickerCancel restores the theme from before the picker was opened.
func (m *Model) themePickerCancel() {
	m.themeIdx = m.themeSavedIdx
	if m.themeIdx < 0 {
		applyTheme(theme.Default())
	} else {
		applyTheme(m.themes[m.themeIdx])
	}
	m.showThemes = false
}

// openPlaylistManager loads playlist metadata and opens the manager overlay.
func (m *Model) openPlaylistManager() {
	m.plMgrRefreshList()
	m.plMgrScreen = plMgrScreenList
	m.plMgrConfirmDel = false
	m.showPlManager = true
}

// plMgrEnterTrackList loads the tracks for a playlist and switches to screen 1.
func (m *Model) plMgrEnterTrackList(name string) {
	tracks, err := m.localProvider.Tracks(name)
	if err != nil {
		m.saveMsg = fmt.Sprintf("Load failed: %s", err)
		m.saveMsgTTL = 60
		return
	}
	m.plMgrSelPlaylist = name
	m.plMgrTracks = tracks
	m.plMgrScreen = plMgrScreenTracks
	m.plMgrCursor = 0
	m.plMgrConfirmDel = false
}

// plMgrRefreshList reloads playlist names and counts from disk and clamps the cursor.
func (m *Model) plMgrRefreshList() {
	if m.localProvider == nil {
		return
	}
	playlists, err := m.localProvider.Playlists()
	if err != nil {
		m.saveMsg = fmt.Sprintf("Load failed: %s", err)
		m.saveMsgTTL = 60
	}
	m.plMgrPlaylists = playlists
	// +1 for the "+ New Playlist..." entry
	total := len(m.plMgrPlaylists) + 1
	if m.plMgrCursor >= total {
		m.plMgrCursor = total - 1
	}
	if m.plMgrCursor < 0 {
		m.plMgrCursor = 0
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

func fetchPlaylistsCmd(prov playlist.Provider) tea.Cmd {
	return func() tea.Msg {
		pls, err := prov.Playlists()
		if err != nil {
			return err
		}
		return pls
	}
}

type tracksLoadedMsg []playlist.Track

// feedsLoadedMsg carries tracks resolved from remote feed/M3U URLs.
type feedsLoadedMsg []playlist.Track

func resolveRemoteCmd(urls []string) tea.Cmd {
	return func() tea.Msg {
		tracks, err := resolve.Remote(urls)
		if err != nil {
			return err
		}
		return feedsLoadedMsg(tracks)
	}
}

// netSearchLoadedMsg carries tracks dynamically searched from the internet.
type netSearchLoadedMsg []playlist.Track

func fetchNetSearchCmd(query string) tea.Cmd {
	return func() tea.Msg {
		tracks, err := resolve.Remote([]string{query})
		if err != nil {
			return err
		}
		return netSearchLoadedMsg(tracks)
	}
}

// streamPlayedMsg signals that async stream Play() completed.
type streamPlayedMsg struct{ err error }

// streamPreloadedMsg signals that async stream Preload() completed.
type streamPreloadedMsg struct{}

// ytdlResolvedMsg carries a lazily resolved yt-dlp track (direct audio URL).
type ytdlResolvedMsg struct {
	index int
	track playlist.Track
	err   error
}

func resolveYTDLCmd(index int, pageURL string) tea.Cmd {
	return func() tea.Msg {
		track, err := resolve.ResolveYTDLTrack(pageURL)
		return ytdlResolvedMsg{index: index, track: track, err: err}
	}
}

func playStreamCmd(p *player.Player, path string, knownDuration time.Duration) tea.Cmd {
	return func() tea.Msg {
		return streamPlayedMsg{err: p.Play(path, knownDuration)}
	}
}

func preloadStreamCmd(p *player.Player, path string, knownDuration time.Duration) tea.Cmd {
	return func() tea.Msg {
		p.Preload(path, knownDuration) // errors silently ignored
		return streamPreloadedMsg{}
	}
}

func playYTDLStreamCmd(p *player.Player, pageURL string, knownDuration time.Duration) tea.Cmd {
	return func() tea.Msg {
		return streamPlayedMsg{err: p.PlayYTDL(pageURL, knownDuration)}
	}
}

func preloadYTDLStreamCmd(p *player.Player, pageURL string, knownDuration time.Duration) tea.Cmd {
	return func() tea.Msg {
		p.PreloadYTDL(pageURL, knownDuration) // errors silently ignored
		return streamPreloadedMsg{}
	}
}

// ytdlSavedMsg signals that an async yt-dlp download-to-disk completed.
type ytdlSavedMsg struct {
	path string
	err  error
}

func saveYTDLCmd(pageURL string, saveDir string) tea.Cmd {
	return func() tea.Msg {
		path, err := resolve.DownloadYTDL(pageURL, saveDir)
		return ytdlSavedMsg{path: path, err: err}
	}
}

func fetchTracksCmd(prov playlist.Provider, playlistID string) tea.Cmd {
	return func() tea.Msg {
		tracks, err := prov.Tracks(playlistID)
		if err != nil {
			return err
		}
		return tracksLoadedMsg(tracks)
	}
}

// — Navidrome browser message types —

// navArtistsLoadedMsg carries the full artist list from getArtists.
type navArtistsLoadedMsg []navidrome.Artist

// navAlbumsLoadedMsg carries one page of albums and the fetch offset.
type navAlbumsLoadedMsg struct {
	albums []navidrome.Album
	offset int  // the offset this page was requested at
	isLast bool // true when the server returned fewer than the requested page size
}

// navTracksLoadedMsg carries the track list for the selected album/artist.
type navTracksLoadedMsg []playlist.Track

func fetchNavArtistsCmd(c *navidrome.NavidromeClient) tea.Cmd {
	return func() tea.Msg {
		artists, err := c.Artists()
		if err != nil {
			return err
		}
		return navArtistsLoadedMsg(artists)
	}
}

func fetchNavArtistAlbumsCmd(c *navidrome.NavidromeClient, artistID string) tea.Cmd {
	return func() tea.Msg {
		albums, err := c.ArtistAlbums(artistID)
		if err != nil {
			return err
		}
		// Artist album lists are complete in one call — treat as last page.
		return navAlbumsLoadedMsg{albums: albums, offset: 0, isLast: true}
	}
}

const navAlbumPageSize = 100

func fetchNavAlbumListCmd(c *navidrome.NavidromeClient, sortType string, offset int) tea.Cmd {
	return func() tea.Msg {
		albums, err := c.AlbumList(sortType, offset, navAlbumPageSize)
		if err != nil {
			return err
		}
		return navAlbumsLoadedMsg{
			albums: albums,
			offset: offset,
			isLast: len(albums) < navAlbumPageSize,
		}
	}
}

func fetchNavAlbumTracksCmd(c *navidrome.NavidromeClient, albumID string) tea.Cmd {
	return func() tea.Msg {
		tracks, err := c.AlbumTracks(albumID)
		if err != nil {
			return err
		}
		return navTracksLoadedMsg(tracks)
	}
}

func fetchNavArtistTracksCmd(c *navidrome.NavidromeClient, albums []navidrome.Album) tea.Cmd {
	return func() tea.Msg {
		var all []playlist.Track
		for _, album := range albums {
			tracks, err := c.AlbumTracks(album.ID)
			if err != nil {
				return err
			}
			all = append(all, tracks...)
		}
		return navTracksLoadedMsg(all)
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
	q := strings.ToLower(m.navSearch)
	if q == "" {
		m.navSearchIdx = nil
		return
	}
	m.navSearchIdx = nil
	switch {
	case m.navMode == navBrowseModeByArtist && m.navScreen == navBrowseScreenList,
		m.navMode == navBrowseModeByArtistAlbum && m.navScreen == navBrowseScreenList:
		for i, a := range m.navArtists {
			if strings.Contains(strings.ToLower(a.Name), q) {
				m.navSearchIdx = append(m.navSearchIdx, i)
			}
		}
	case m.navMode == navBrowseModeByAlbum && m.navScreen == navBrowseScreenList,
		m.navMode == navBrowseModeByArtistAlbum && m.navScreen == navBrowseScreenAlbums:
		for i, a := range m.navAlbums {
			if strings.Contains(strings.ToLower(a.Name), q) ||
				strings.Contains(strings.ToLower(a.Artist), q) {
				m.navSearchIdx = append(m.navSearchIdx, i)
			}
		}
	case m.navScreen == navBrowseScreenTracks:
		for i, t := range m.navTracks {
			if strings.Contains(strings.ToLower(t.Title), q) ||
				strings.Contains(strings.ToLower(t.Artist), q) ||
				strings.Contains(strings.ToLower(t.Album), q) {
				m.navSearchIdx = append(m.navSearchIdx, i)
			}
		}
	}
}

// navClearSearch resets the nav search state.
func (m *Model) navClearSearch() {
	m.navSearching = false
	m.navSearch = ""
	m.navSearchIdx = nil
	m.navCursor = 0
	m.navScroll = 0
}

func (m *Model) openNavBrowser() {
	m.showNavBrowser = true
	m.navMode = navBrowseModeMenu
	m.navScreen = navBrowseScreenList
	m.navCursor = 0
	m.navScroll = 0
	m.navArtists = nil
	m.navAlbums = nil
	m.navTracks = nil
	m.navLoading = false
	m.navAlbumLoading = false
	m.navAlbumDone = false
	m.navSearching = false
	m.navSearch = ""
	m.navSearchIdx = nil
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
		if m.fullVis {
			m.vis.Rows = max(defaultVisRows, (m.height-10)*4/5)
		}

	case tickMsg:
		// Expire temporary status messages.
		if m.saveMsgTTL > 0 {
			m.saveMsgTTL--
			if m.saveMsgTTL == 0 {
				m.saveMsg = ""
			}
		}
		// Surface stream errors (e.g., connection drops) before checking track done
		if err := m.player.StreamErr(); err != nil {
			m.err = err
		}
		// Poll ICY stream title for live radio display.
		if title := m.player.StreamTitle(); title != "" {
			m.streamTitle = title
		}
		// Update network throughput every ~1 second (20 ticks at 50ms).
		m.lastSpeedTick++
		if m.lastSpeedTick >= 20 {
			downloaded, _ := m.player.StreamBytes()
			delta := downloaded - m.lastSpeedBytes
			if delta > 0 {
				// Exponential moving average for smooth display.
				instant := float64(delta) / (float64(m.lastSpeedTick) * 0.05) // bytes/sec
				if m.networkSpeed == 0 {
					m.networkSpeed = instant
				} else {
					m.networkSpeed = m.networkSpeed*0.6 + instant*0.4
				}
			} else if downloaded == 0 {
				m.networkSpeed = 0
			}
			m.lastSpeedBytes = downloaded
			m.lastSpeedTick = 0
		}
		var cmds []tea.Cmd
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
			cmds = append(cmds, m.preloadNext())
			m.notifyMPRIS()
		}
		// Check if gapless drained (end of playlist, no preloaded next).
		// Skip if already buffering a yt-dlp download to avoid advancing
		// the playlist on every tick while waiting for the resolve.
		if m.player.IsPlaying() && !m.player.IsPaused() && m.player.Drained() && !m.buffering {
			// Track drained to end — always ≥ 50%.
			finishedTrack, _ := m.playlist.Current()
			drainDur := time.Duration(finishedTrack.DurationSecs) * time.Second
			m.maybeScrobble(finishedTrack, drainDur, drainDur)

			cmds = append(cmds, m.nextTrack())
			m.notifyMPRIS()
		}
		if m.player.IsPlaying() && !m.player.IsPaused() {
			m.titleOff++
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

		// Use fast ticks only when the visualizer is rendering; otherwise
		// slow ticks are enough for time/seek updates and save significant
		// GPU repaints in terminal emulators.
		interval := tickSlow
		if m.vis.Mode != VisNone && !m.isOverlayActive() {
			interval = tickFast
		}
		cmds = append(cmds, tickCmdAt(interval))
		return m, tea.Batch(cmds...)

	case []playlist.PlaylistInfo:
		m.providerLists = msg
		m.provLoading = false
		return m, nil

	case tracksLoadedMsg:
		m.player.Stop()
		m.player.ClearPreload()
		m.playlist.Replace(msg)
		m.plCursor = 0
		m.plScroll = 0
		m.focus = focusPlaylist
		m.provLoading = false
		if m.playlist.Len() > 0 {
			cmd := m.playCurrentTrack()
			m.notifyMPRIS()
			return m, cmd
		}
		return m, nil

	case navArtistsLoadedMsg:
		m.navArtists = []navidrome.Artist(msg)
		m.navLoading = false
		m.navCursor = 0
		m.navScroll = 0
		return m, nil

	case navAlbumsLoadedMsg:
		if msg.offset == 0 {
			// Fresh load (new sort or drill-in): replace the list.
			m.navAlbums = msg.albums
			m.navAlbumDone = false
		} else {
			// Lazy-load page: append.
			m.navAlbums = append(m.navAlbums, msg.albums...)
		}
		if msg.isLast {
			m.navAlbumDone = true
		}
		m.navAlbumLoading = false
		if msg.offset == 0 {
			m.navCursor = 0
			m.navScroll = 0
		}
		// If we just loaded the first page and it was a full menu → list transition,
		// also clear the general loading flag.
		m.navLoading = false
		return m, nil

	case navTracksLoadedMsg:
		m.navTracks = []playlist.Track(msg)
		m.navLoading = false
		m.navCursor = 0
		m.navScroll = 0
		m.navScreen = navBrowseScreenTracks
		return m, nil

	case feedsLoadedMsg:
		m.feedLoading = false
		m.playlist.Add(msg...)
		if m.playlist.Len() > 0 && !m.player.IsPlaying() {
			cmd := m.playCurrentTrack()
			m.notifyMPRIS()
			return m, cmd
		}
		return m, nil

	case netSearchLoadedMsg:
		if len(msg) > 0 {
			startIdx := m.playlist.Len()
			m.playlist.Add(msg...)
			for i := startIdx; i < m.playlist.Len(); i++ {
				m.playlist.Queue(i)
			}
			m.saveMsg = fmt.Sprintf("Added to Queue: %s", msg[0].DisplayName())
			m.saveMsgTTL = 60
			if !m.player.IsPlaying() {
				cmd := m.playCurrentTrack()
				m.notifyMPRIS()
				return m, cmd
			}
		} else {
			m.saveMsg = "No tracks found online."
			m.saveMsgTTL = 60
		}
		return m, nil

	case fbTracksResolvedMsg:
		if len(msg.tracks) == 0 {
			m.saveMsg = "No audio files found"
			m.saveMsgTTL = 60
			return m, nil
		}
		if msg.replace {
			m.player.Stop()
			m.player.ClearPreload()
			m.playlist.Replace(msg.tracks)
			m.plCursor = 0
			m.plScroll = 0
		} else {
			m.playlist.Add(msg.tracks...)
		}
		m.focus = focusPlaylist
		m.saveMsg = fmt.Sprintf("Added %d track(s)", len(msg.tracks))
		m.saveMsgTTL = 60
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
		}
		m.notifyMPRIS()
		return m, m.preloadNext()

	case streamPreloadedMsg:
		m.preloading = false
		return m, nil

	case ytdlSavedMsg:
		if msg.err != nil {
			m.saveMsg = fmt.Sprintf("Download failed: %s", msg.err)
		} else {
			m.saveMsg = fmt.Sprintf("Saved to %s", msg.path)
		}
		m.saveMsgTTL = 80
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
		m.err = msg
		m.provLoading = false
		m.feedLoading = false
		m.buffering = false
		return m, nil

	case mpris.InitMsg:
		m.mpris = msg.Svc
		return m, nil

	case mpris.PlayPauseMsg:
		cmd := m.togglePlayPause()
		m.notifyMPRIS()
		return m, cmd

	case mpris.NextMsg:
		if track, _ := m.playlist.Current(); track.NavidromeID != "" {
			m.maybeScrobble(track, m.player.Position(), m.player.Duration())
		}
		cmd := m.nextTrack()
		m.notifyMPRIS()
		return m, cmd

	case mpris.PrevMsg:
		if track, _ := m.playlist.Current(); track.NavidromeID != "" {
			m.maybeScrobble(track, m.player.Position(), m.player.Duration())
		}
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
	m.streamTitle = ""
	// Stream yt-dlp URLs (SoundCloud, YouTube, etc.) via pipe chain.
	if playlist.IsYTDL(track.Path) {
		m.buffering = true
		m.err = nil
		dur := time.Duration(track.DurationSecs) * time.Second
		return playYTDLStreamCmd(m.player, track.Path, dur)
	}
	// Fire now-playing notification for Navidrome tracks.
	m.nowPlaying(track)
	dur := time.Duration(track.DurationSecs) * time.Second
	if track.Stream {
		m.buffering = true
		m.err = nil
		return playStreamCmd(m.player, track.Path, dur)
	}
	if err := m.player.Play(track.Path, dur); err != nil {
		m.err = err
	} else {
		m.err = nil
	}
	return m.preloadNext()
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
			if remaining > streamPreloadLeadTime {
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

// adjustScroll ensures plCursor is visible in the playlist view.
func (m *Model) adjustScroll() {
	if m.plCursor < m.plScroll {
		m.plScroll = m.plCursor
	}
	if m.plCursor >= m.plScroll+m.plVisible {
		m.plScroll = m.plCursor - m.plVisible + 1
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
		if idx >= 0 && track.Stream {
			m.player.Stop()
			return m.playTrack(track)
		}
	}
	m.player.TogglePause()
	return nil
}

// updateSearch filters the playlist by the current search query.
func (m *Model) updateSearch() {
	m.searchResults = nil
	m.searchCursor = 0
	if m.searchQuery == "" {
		return
	}
	query := strings.ToLower(m.searchQuery)
	for i, t := range m.playlist.Tracks() {
		if strings.Contains(strings.ToLower(t.DisplayName()), query) {
			m.searchResults = append(m.searchResults, i)
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
