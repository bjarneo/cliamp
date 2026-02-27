// Package ui implements the Bubbletea TUI for the CLIAMP terminal music player.
package ui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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

type tickMsg time.Time

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
	providerLists []playlist.PlaylistInfo
	provCursor    int
	provLoading   bool
	// EQ preset state (-1 = custom, 0+ = index into eqPresets)
	eqPresetIdx int

	// Keymap overlay
	showKeymap bool

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
	themes       []theme.Theme
	themeIdx     int
	showThemes   bool // theme picker overlay visible
	themeCursor  int  // cursor in theme picker (0 = Default, 1+ = themes[i-1])
	themeSavedIdx int // themeIdx before opening picker, for cancel/restore
}

// NewModel creates a Model wired to the given player and playlist.
func NewModel(p *player.Player, pl *playlist.Playlist, prov playlist.Provider, themes []theme.Theme) Model {
	m := Model{
		player:      p,
		playlist:    pl,
		vis:         NewVisualizer(44100),
		plVisible:   5,
		eqPresetIdx: -1, // custom until a preset is selected
		themes:      themes,
		themeIdx:    -1, // Default (ANSI)
	}
	if prov != nil {
		m.provider = prov
		m.focus = focusProvider
		m.provLoading = true
	}
	return m
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

// Init starts the tick timer and requests the terminal size.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tickCmd(), tea.WindowSize()}
	if m.provider != nil {
		cmds = append(cmds, fetchPlaylistsCmd(m.provider))
	}
	if len(m.pendingURLs) > 0 {
		cmds = append(cmds, resolveRemoteCmd(m.pendingURLs))
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

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

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
		// Check if gapless drained (end of playlist, no preloaded next)
		if m.player.IsPlaying() && !m.player.IsPaused() && m.player.Drained() {
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
		m.playlist.Add(msg...)
		m.focus = focusPlaylist
		m.provLoading = false
		if m.playlist.Len() > 0 {
			cmd := m.playCurrentTrack()
			m.notifyMPRIS()
			return m, cmd
		}
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
		Title:  track.Title,
		Artist: track.Artist,
		Length: m.player.Duration().Microseconds(),
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
	m.mpris.Update(status, info, m.player.Volume())
}

// togglePlayPause starts playback if stopped, or toggles pause if playing.
func (m *Model) togglePlayPause() tea.Cmd {
	if !m.player.IsPlaying() {
		return m.playCurrentTrack()
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
