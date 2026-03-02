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

	// Async feed/M3U URL resolution
	pendingURLs []string
	feedLoading bool

	// Async stream buffering (true while HTTP connect is in progress)
	buffering bool

	// Live stream title from ICY metadata (e.g., "Artist - Song")
	streamTitle string

	// Temporary status message (e.g., "Saved to ...")
	saveMsg    string
	saveMsgTTL int // ticks remaining before clearing

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
	showNavBrowser  bool
	navClient       *navidrome.NavidromeClient // nil when Navidrome is not configured
	navMode         navBrowseModeType
	navScreen       navBrowseScreenType
	navCursor       int
	navScroll       int
	navArtists      []navidrome.Artist
	navAlbums       []navidrome.Album
	navTracks       []playlist.Track
	navSelArtist    navidrome.Artist
	navSelAlbum     navidrome.Album
	navSortType     string // current album sort type, persisted to config
	navAlbumLoading bool   // true while an album page fetch is in progress
	navAlbumDone    bool   // true once the server signals no more pages (isLast)
	navLoading      bool   // general loading flag for artists/tracks
	navSearching    bool   // true while the nav search bar is open
	navSearch       string // current nav search query
	navSearchIdx    []int  // filtered indices into the active list (nil = no filter)
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
		player:        p,
		playlist:      pl,
		vis:           NewVisualizer(float64(p.SampleRate())),
		plVisible:     5,
		eqPresetIdx:   -1, // custom until a preset is selected
		themes:        themes,
		themeIdx:      -1, // Default (ANSI)
		localProvider: localProv,
		navSortType:   sortType,
		navClient:     nav,
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

// ThemeName returns the current theme name.
func (m Model) ThemeName() string {
	if m.themeIdx < 0 || m.themeIdx >= len(m.themes) {
		return theme.DefaultName
	}
	return m.themes[m.themeIdx].Name
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

func playStreamCmd(p *player.Player, path string) tea.Cmd {
	return func() tea.Msg {
		return streamPlayedMsg{err: p.Play(path)}
	}
}

func preloadStreamCmd(p *player.Player, path string) tea.Cmd {
	return func() tea.Msg {
		p.Preload(path) // errors silently ignored
		return streamPreloadedMsg{}
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
	return tea.Tick(time.Millisecond*50, func(t time.Time) tea.Msg {
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
			m.vis.Rows = max(defaultVisRows, (m.height-10)*3/5)
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
		var cmds []tea.Cmd
		// Check gapless transition (audio already playing next track)
		if m.player.GaplessAdvanced() {
			m.playlist.Next()
			m.plCursor = m.playlist.Index()
			m.adjustScroll()
			m.titleOff = 0
			cmds = append(cmds, m.preloadNext())
			m.notifyMPRIS()
		}
		// Check if gapless drained (end of playlist, no preloaded next).
		// Skip if already buffering a yt-dlp download to avoid advancing
		// the playlist on every tick while waiting for the resolve.
		if m.player.IsPlaying() && !m.player.IsPaused() && m.player.Drained() && !m.buffering {
			cmds = append(cmds, m.nextTrack())
			m.notifyMPRIS()
		}
		m.titleOff++
		cmds = append(cmds, tickCmd())
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
		cmd := m.nextTrack()
		m.notifyMPRIS()
		return m, cmd

	case mpris.PrevMsg:
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
		m.player.Seek(-m.player.Position())
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
// yt-dlp URLs are lazily resolved to direct audio streams before playback.
func (m *Model) playTrack(track playlist.Track) tea.Cmd {
	m.streamTitle = ""
	// Lazy-resolve yt-dlp URLs (SoundCloud, YouTube, etc.) to direct audio streams.
	if playlist.IsYTDL(track.Path) {
		m.buffering = true
		m.err = nil
		_, idx := m.playlist.Current()
		return resolveYTDLCmd(idx, track.Path)
	}
	if track.Stream {
		m.buffering = true
		m.err = nil
		return playStreamCmd(m.player, track.Path)
	}
	if err := m.player.Play(track.Path); err != nil {
		m.err = err
	} else {
		m.err = nil
	}
	return m.preloadNext()
}

// preloadNext looks ahead in the playlist and preloads the next track for
// gapless transition. Errors are silently ignored — playback falls back to
// non-gapless if preloading fails.
func (m *Model) preloadNext() tea.Cmd {
	next, ok := m.playlist.PeekNext()
	if !ok {
		return nil
	}
	// Can't preload yt-dlp tracks — they need lazy resolution first.
	if playlist.IsYTDL(next.Path) {
		return nil
	}
	if next.Stream {
		return preloadStreamCmd(m.player, next.Path)
	}
	m.player.Preload(next.Path)
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
