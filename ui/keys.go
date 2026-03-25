package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"cliamp/config"
	"cliamp/internal/fileutil"
	"cliamp/playlist"
)

// quit shuts down the player and signals the TUI to exit.
func (m *Model) quit() tea.Cmd {
	// Only save resume for seekable tracks:
	// - local files (not stream)
	// - HTTP streams with known duration (podcast MP3s, seek-by-reconnect)
	// Exclude YTDL (position unreliable) and real-time live streams.
	if track, _ := m.playlist.Current(); track.Path != "" &&
		!playlist.IsYTDL(track.Path) && !track.IsLive() &&
		m.player.IsPlaying() {
		if secs := int(m.player.Position().Seconds()); secs > 0 {
			m.exitResume.path = track.Path
			m.exitResume.secs = secs
		}
	}

	m.player.Close()
	m.quitting = true
	return tea.Quit
}

// scrobbleCurrent fires a scrobble for the currently playing track if applicable.
func (m *Model) scrobbleCurrent() {
	if track, _ := m.playlist.Current(); track.NavidromeID != "" {
		m.maybeScrobble(track, m.player.Position(), m.player.Duration())
	}
}

// handleKey processes a single key press and returns an optional command.
func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	if m.keymap.visible {
		return m.handleKeymapKey(msg)
	}

	// Navidrome explore browser overlay
	if m.navBrowser.visible {
		return m.handleNavBrowserKey(msg)
	}

	// Radio catalog overlay
	if m.radioCatalog.visible {
		return m.handleRadioCatalogKey(msg)
	}

	// Theme picker overlay — interactive navigation
	if m.themePicker.visible {
		return m.handleThemeKey(msg)
	}

	// Playlist manager overlay (browse, add, remove, delete)
	if m.plManager.visible {
		return m.handlePlaylistManagerKey(msg)
	}

	// File browser overlay
	if m.fileBrowser.visible {
		return m.handleFileBrowserKey(msg)
	}

	// Queue manager overlay
	if m.queue.visible {
		return m.handleQueueKey(msg)
	}

	// Track info overlay
	if m.showInfo {
		switch msg.String() {
		case "ctrl+c":
			return m.quit()
		case "esc", "i":
			m.showInfo = false
		}
		return nil
	}

	// Lyrics overlay
	if m.lyrics.visible {
		switch msg.String() {
		case "ctrl+c":
			return m.quit()
		case "esc", "y":
			m.lyrics.visible = false
		case "up", "k":
			if !(m.lyricsSyncable() && m.lyricsHaveTimestamps()) && m.lyrics.scroll > 0 {
				m.lyrics.scroll--
			}
		case "down", "j":
			if !(m.lyricsSyncable() && m.lyricsHaveTimestamps()) {
				maxScroll := len(m.lyrics.lines) - 1
				if maxScroll < 0 {
					maxScroll = 0
				}
				if m.lyrics.scroll < maxScroll {
					m.lyrics.scroll++
				}
			}
		}
		return nil
	}

	if m.jumping {
		return m.handleJumpKey(msg)
	}

	if m.urlInputting {
		return m.handleURLInputKey(msg)
	}

	if m.search.active {
		return m.handleSearchKey(msg)
	}

	if m.netSearch.active {
		return m.handleNetSearchKey(msg)
	}

	if m.provSearch.active {
		return m.handleProvSearchKey(msg)
	}

	if m.focus == focusProvider {
		switch msg.String() {
		case "q", "ctrl+c":
			return m.quit()
		case "up", "k":
			if m.provCursor > 0 {
				m.provCursor--
			} else if len(m.providerLists) > 0 {
				m.provCursor = len(m.providerLists)-1
			}
		case " ":
			return m.togglePlayPause()
		case "down", "j":
			if m.provCursor < len(m.providerLists)-1 {
				m.provCursor++
			} else if len(m.providerLists) > 0 {
				m.provCursor = 0
			}
		case "enter":
			if m.provSignIn {
				if auth, ok := m.provider.(playlist.Authenticator); ok {
					m.provSignIn = false
					m.provLoading = true
					return authenticateProviderCmd(auth)
				}
			}
			if len(m.providerLists) > 0 && !m.provLoading {
				m.provLoading = true
				return fetchTracksCmd(m.provider, m.providerLists[m.provCursor].ID)
			}
		case "tab":
			m.focus = focusEQ
		case "esc", "backspace", "b":
			if m.playlist.Len() > 0 {
				m.focus = focusPlaylist
			}
		case "/":
			m.provSearch.active = true
			m.provSearch.query = ""
			m.provSearch.results = nil
			m.provSearch.cursor = 0
		case "o":
			m.openFileBrowser()
		case "N":
			if m.navClient != nil {
				m.openNavBrowser()
			}
		case "R":
			return m.openRadioCatalog()
		case "J":
			m.openJumpMode()
		}
		return nil
	}

	if m.focus == focusProvPill {
		switch msg.String() {
		case "q", "ctrl+c":
			return m.quit()
		case "left", "h":
			if m.provPillIdx > 0 {
				m.provPillIdx--
			}
		case "right", "l":
			if m.provPillIdx < len(m.providers)-1 {
				m.provPillIdx++
			}
		case "enter":
			return m.switchProvider(m.provPillIdx)
		case "tab":
			m.focus = focusPlaylist
		case "esc", "backspace":
			m.focus = focusEQ
		case " ":
			return m.togglePlayPause()
		}
		return nil
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m.quit()
	case "esc", "backspace", "b":
		if m.fullVis {
			m.fullVis = false
			m.vis.Rows = defaultVisRows
		} else if m.focus == focusPlaylist {
			m.focus = focusProvider
		}

	case " ":
		cmd := m.togglePlayPause()
		m.notifyMPRIS()
		return cmd

	case "s":
		m.player.Stop()
		m.notifyMPRIS()

	case ">", ".":
		m.scrobbleCurrent()
		cmd := m.nextTrack()
		m.notifyMPRIS()
		return cmd

	case "<", ",":
		m.scrobbleCurrent()
		cmd := m.prevTrack()
		m.notifyMPRIS()
		return cmd

	case "left":
		if m.focus == focusEQ {
			if m.eqCursor > 0 {
				m.eqCursor--
			}
		} else {
			m.doSeek(-5 * time.Second)
		}

	case "shift+left":
		m.doSeek(-m.seekStepLarge)

	case "right":
		if m.focus == focusEQ {
			if m.eqCursor < numBands-1 {
				m.eqCursor++
			}
		} else {
			m.doSeek(5 * time.Second)
		}

	case "shift+right":
		m.doSeek(m.seekStepLarge)

	case "shift+up":
		if m.focus == focusPlaylist && m.plCursor > 0 {
			if m.playlist.Move(m.plCursor, m.plCursor-1) {
				m.plCursor--
				m.adjustScroll()
			}
		}

	case "shift+down":
		if m.focus == focusPlaylist && m.plCursor < m.playlist.Len()-1 {
			if m.playlist.Move(m.plCursor, m.plCursor+1) {
				m.plCursor++
				m.adjustScroll()
			}
		}

	case "up", "k":
		if m.focus == focusEQ {
			bands := m.player.EQBands()
			m.player.SetEQBand(m.eqCursor, bands[m.eqCursor]+1)
			m.eqPresetIdx = -1 // manual tweak → custom
			m.saveEQ()
		} else {
			if m.plCursor > 0 {
				m.plCursor--
				m.adjustScroll()
			} else if m.playlist.Len() > 0 {
				m.plCursor = m.playlist.Len()-1
				m.adjustScroll()
			}
		}

	case "down", "j":
		if m.focus == focusEQ {
			bands := m.player.EQBands()
			m.player.SetEQBand(m.eqCursor, bands[m.eqCursor]-1)
			m.eqPresetIdx = -1 // manual tweak → custom
			m.saveEQ()
		} else {
			if m.plCursor < m.playlist.Len()-1 {
				m.plCursor++
				m.adjustScroll()
			} else if m.playlist.Len() > 0 {
				m.plCursor = 0
				m.adjustScroll()
			}
		}

	case "pgup", "ctrl+u":
		if m.focus == focusPlaylist && m.plCursor > 0 {
			m.plCursor -= min(m.plCursor, m.plVisible)
			m.adjustScroll()
		}

	case "pgdown", "ctrl+d":
		if m.focus == focusPlaylist && m.plCursor < m.playlist.Len()-1 {
			m.plCursor = min(m.playlist.Len()-1, m.plCursor + m.plVisible)
			m.adjustScroll()
		}

	case "g", "home":
		if m.focus == focusPlaylist && m.plCursor != 0 {
			m.plCursor = 0
			m.adjustScroll()
		}

	case "G", "end":
		if m.focus == focusPlaylist && m.playlist.Len() > 0 && m.plCursor != m.playlist.Len()-1 {
			m.plCursor = m.playlist.Len()-1
			m.adjustScroll()
		}

	case "enter":
		if m.focus == focusPlaylist {
			// No-op only if this exact track is still buffering.
			if m.buffering && m.plCursor == m.playlist.Index() {
				break
			}
			m.scrobbleCurrent()
			m.playlist.SetIndex(m.plCursor)
			cmd := m.playCurrentTrack()
			m.notifyMPRIS()
			return cmd
		}

	case "+", "=":
		m.player.SetVolume(m.player.Volume() + 1)
		m.notifyMPRIS()

	case "-":
		m.player.SetVolume(m.player.Volume() - 1)
		m.notifyMPRIS()

	case "r":
		m.playlist.CycleRepeat()
		if err := config.Save("repeat", fmt.Sprintf("%q", m.playlist.Repeat().String())); err != nil {
			m.status.text = fmt.Sprintf("Config save failed: %s", err)
			m.status.ttl = statusTTLDefault
		}
		m.player.ClearPreload()
		return m.preloadNext()

	case "z":
		m.playlist.ToggleShuffle()
		if err := config.Save("shuffle", fmt.Sprintf("%v", m.playlist.Shuffled())); err != nil {
			m.status.text = fmt.Sprintf("Config save failed: %s", err)
			m.status.ttl = statusTTLDefault
		}
		m.player.ClearPreload()
		return m.preloadNext()

	case "tab":
		switch m.focus {
		case focusPlaylist:
			m.focus = focusEQ
		case focusEQ:
			if len(m.providers) > 1 {
				m.focus = focusProvPill
			} else {
				m.focus = focusPlaylist
			}
		default:
			m.focus = focusPlaylist
		}

	case "h":
		if m.focus == focusEQ && m.eqCursor > 0 {
			m.eqCursor--
		}

	case "l":
		if m.focus == focusEQ && m.eqCursor < numBands-1 {
			m.eqCursor++
		}

	case "e":
		m.eqPresetIdx++
		if m.eqPresetIdx >= len(eqPresets) {
			m.eqPresetIdx = 0
		}
		m.applyEQPreset()
		m.saveEQ()

	case "a":
		if m.focus == focusPlaylist {
			if !m.playlist.Dequeue(m.plCursor) {
				m.playlist.Queue(m.plCursor)
			}
		}

	case "A":
		if m.focus == focusPlaylist {
			m.queue.visible = true
			m.queue.cursor = 0
		}

	case "S":
		return m.saveTrack()

	case "m":
		m.player.ToggleMono()

	case "/":
		m.search.active = true
		m.search.query = ""
		m.search.results = nil
		m.search.cursor = 0
		m.prevFocus = m.focus
		m.focus = focusSearch

	case "f", "F":
		m.netSearch.active = true
		m.netSearch.query = ""
		m.netSearch.soundcloud = msg.String() == "F"
		m.prevFocus = m.focus
		m.focus = focusNetSearch

	case "J":
		m.openJumpMode()
	case "p":
		if m.localProvider != nil {
			m.openPlaylistManager()
		}

	case "t":
		m.openThemePicker()

	case "i":
		m.showInfo = true

	case "y":
		m.lyrics.visible = !m.lyrics.visible
		if m.lyrics.visible && !m.lyrics.loading {
			artist, title := m.lyricsArtistTitle()
			if artist != "" && title != "" {
				q := artist + "\n" + title
				if q != m.lyrics.query {
					m.lyrics.query = q
					m.lyrics.loading = true
					m.lyrics.lines = nil
					m.lyrics.err = nil
					return fetchLyricsCmd(artist, title)
				}
			}
		}

	case "o":
		m.openFileBrowser()

	case "u":
		m.urlInputting = true
		m.urlInput = ""

	case "N":
		if m.navClient != nil {
			m.openNavBrowser()
		}

	case "R":
		return m.openRadioCatalog()

	case "v":
		m.vis.CycleMode()
		if err := config.Save("visualizer", fmt.Sprintf("%q", m.vis.ModeName())); err != nil {
			m.status.text = fmt.Sprintf("Config save failed: %s", err)
			m.status.ttl = statusTTLDefault
		}

	case "V":
		m.fullVis = !m.fullVis
		if m.fullVis {
			m.vis.Rows = max(defaultVisRows, (m.height-10)*4/5)
		} else {
			m.vis.Rows = defaultVisRows
		}

	case "x":
		if m.focus == focusPlaylist {
			if m.plVisible <= minPlVisible {
				// Expand: recalculate dynamic max from terminal height.
				probe := strings.Join([]string{
					m.renderTitle(), m.renderTrackInfo(), m.renderTimeStatus(), "",
					m.renderSpectrum(), m.renderSeekBar(), "",
					m.renderControls(), "", m.renderPlaylistHeader(),
					"x", "", m.renderHelp(), m.renderStreamStatus(),
				}, "\n")
				fixedLines := lipgloss.Height(frameStyle.Render(probe)) - 1
				m.plVisible = max(minPlVisible, min(maxPlExpandVisible, m.height-fixedLines))
			} else {
				m.plVisible = minPlVisible
			}
			m.adjustScroll()
		}

	case "ctrl+k":
		m.keymap.visible = true
	}

	return nil
}

// saveTrack copies the current track to ~/Music/cliamp/ with a clean filename.
// For yt-dlp tracks (piped streams), triggers an async download via yt-dlp.
// For local temp files, copies synchronously.
func (m *Model) saveTrack() tea.Cmd {
	track, idx := m.playlist.Current()
	if idx < 0 {
		m.status.text = "Nothing to save"
		m.status.ttl = statusTTLShort
		return nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		m.status.text = fmt.Sprintf("Save failed: %s", err)
		m.status.ttl = statusTTLShort
		return nil
	}

	saveDir := filepath.Join(home, "Music", "cliamp")
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		m.status.text = fmt.Sprintf("Save failed: %s", err)
		m.status.ttl = statusTTLShort
		return nil
	}

	// YouTube/yt-dlp tracks: async download directly to ~/Music/cliamp/.
	if playlist.IsYouTubeURL(track.Path) || playlist.IsYTDL(track.Path) {
		m.status.text = "Downloading..."
		m.status.ttl = statusTTLDownload // cleared by ytdlSavedMsg
		return saveYTDLCmd(track.Path, saveDir)
	}

	// Only save local temp files (yt-dlp downloads), not streams or user's own files.
	if track.Stream || !strings.HasPrefix(track.Path, os.TempDir()) {
		m.status.text = "Only downloaded tracks can be saved"
		m.status.ttl = statusTTLShort
		return nil
	}

	ext := filepath.Ext(track.Path)
	name := track.Title
	if track.Artist != "" {
		name = track.Artist + " - " + name
	}
	// Sanitize filename: remove path separators and other problematic chars.
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' {
			return '_'
		}
		return r
	}, name)

	dest := filepath.Join(saveDir, name+ext)

	if err := fileutil.CopyFile(track.Path, dest); err != nil {
		m.status.text = fmt.Sprintf("Save failed: %s", err)
		m.status.ttl = statusTTLShort
		return nil
	}

	m.status.text = fmt.Sprintf("Saved to ~/Music/cliamp/%s", name+ext)
	m.status.ttl = statusTTLDefault
	return nil
}

func (m *Model) resetJumpInput() {
	m.jumpInput = ""
}

func (m *Model) openJumpMode() {
	m.jumping = true
	m.resetJumpInput()
}

func (m *Model) closeJumpMode() {
	m.jumping = false
	m.resetJumpInput()
}

// handleJumpKey processes key presses while in jump-time mode.
func (m *Model) handleJumpKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.closeJumpMode()
		return m.quit()
	}

	switch msg.Type {
	case tea.KeyEscape:
		m.closeJumpMode()
		return nil
	case tea.KeyEnter:
		target, err := parseJumpTarget(m.jumpInput)
		if err != nil {
			m.resetJumpInput()
			return nil
		}
		if dur := m.player.Duration(); dur > 0 && target > dur {
			m.resetJumpInput()
			return nil
		}
		m.player.Seek(target - m.player.Position())
		m.notifyMPRIS()
		if m.mpris != nil {
			m.mpris.EmitSeeked(m.player.Position().Microseconds())
		}
		m.closeJumpMode()
		return nil
	case tea.KeyBackspace:
		m.jumpInput = removeLastRune(m.jumpInput)
		return nil
	}

	if msg.Type == tea.KeyRunes {
		m.jumpInput += string(msg.Runes)
	}
	return nil
}

// handleProvSearchKey processes key presses while filtering the provider playlist list.
func (m *Model) handleProvSearchKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEscape:
		m.provSearch.active = false
	case tea.KeyEnter:
		if len(m.provSearch.results) > 0 && !m.provLoading {
			idx := m.provSearch.results[m.provSearch.cursor]
			m.provCursor = idx
			m.provLoading = true
			m.provSearch.active = false
			return fetchTracksCmd(m.provider, m.providerLists[idx].ID)
		}
	case tea.KeyUp:
		if m.provSearch.cursor > 0 {
			m.provSearch.cursor--
		}
	case tea.KeyDown:
		if m.provSearch.cursor < len(m.provSearch.results)-1 {
			m.provSearch.cursor++
		}
	case tea.KeyBackspace:
		if m.provSearch.query != "" {
			m.provSearch.query = removeLastRune(m.provSearch.query)
			m.updateProvSearch()
		}
	case tea.KeySpace:
		m.provSearch.query += " "
		m.updateProvSearch()
	default:
		if msg.Type == tea.KeyRunes {
			m.provSearch.query += string(msg.Runes)
			m.updateProvSearch()
		}
	}
	return nil
}

func (m *Model) updateProvSearch() {
	m.provSearch.results = nil
	m.provSearch.cursor = 0
	if m.provSearch.query == "" {
		return
	}
	q := strings.ToLower(m.provSearch.query)
	for i, pl := range m.providerLists {
		if strings.Contains(strings.ToLower(pl.Name), q) {
			m.provSearch.results = append(m.provSearch.results, i)
		}
	}
}

func (m *Model) handleSearchKey(msg tea.KeyMsg) tea.Cmd {
	// Allow opening overlays during search (ctrl combos don't conflict with text input).
	switch msg.String() {
	case "ctrl+k":
		m.keymap.visible = true
		return nil
	}

	switch msg.Type {
	case tea.KeyEscape:
		m.search.active = false
		m.focus = m.prevFocus

	case tea.KeyEnter:
		var cmd tea.Cmd
		if len(m.search.results) > 0 {
			idx := m.search.results[m.search.cursor]
			m.playlist.SetIndex(idx)
			m.plCursor = idx
			m.adjustScroll()
			cmd = m.playCurrentTrack()
			m.notifyMPRIS()
		}
		m.search.active = false
		m.focus = focusPlaylist
		return cmd

	case tea.KeyTab:
		// Toggle queue for selected search result.
		if len(m.search.results) > 0 && m.search.cursor < len(m.search.results) {
			idx := m.search.results[m.search.cursor]
			if !m.playlist.Dequeue(idx) {
				m.playlist.Queue(idx)
			}
		}

	case tea.KeyUp:
		if m.search.cursor > 0 {
			m.search.cursor--
		}

	case tea.KeyDown:
		if m.search.cursor < len(m.search.results)-1 {
			m.search.cursor++
		}

	case tea.KeyBackspace:
		if m.search.query != "" {
			m.search.query = removeLastRune(m.search.query)
			m.updateSearch()
		}

	case tea.KeySpace:
		m.search.query += " "
		m.updateSearch()

	default:
		if msg.Type == tea.KeyRunes {
			m.search.query += string(msg.Runes)
			m.updateSearch()
		}
	}

	return nil
}

// handleNetSearchKey processes key presses while in net search mode.
func (m *Model) handleNetSearchKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+k":
		m.keymap.visible = true
		return nil
	}

	switch msg.Type {
	case tea.KeyEscape:
		m.netSearch.active = false
		m.focus = m.prevFocus

	case tea.KeyEnter:
		var cmd tea.Cmd
		m.netSearch.active = false
		m.focus = m.prevFocus
		if strings.TrimSpace(m.netSearch.query) != "" {
			prefix := "ytsearch1:"
			if m.netSearch.soundcloud {
				prefix = "scsearch1:"
			}
			m.status.text = "Queuing search..."
			m.status.ttl = statusTTLShort
			cmd = fetchNetSearchCmd(prefix + strings.TrimSpace(m.netSearch.query))
		}
		return cmd

	case tea.KeyBackspace:
		m.netSearch.query = removeLastRune(m.netSearch.query)

	case tea.KeySpace:
		m.netSearch.query += " "

	default:
		if msg.Type == tea.KeyRunes {
			m.netSearch.query += string(msg.Runes)
		}
	}

	return nil
}

// handleURLInputKey processes key presses while in URL input mode.
func (m *Model) handleURLInputKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEscape:
		m.urlInputting = false
	case tea.KeyEnter:
		m.urlInputting = false
		input := strings.TrimSpace(m.urlInput)
		if input != "" {
			m.feedLoading = true
			m.status.text = "Loading URL..."
			m.status.ttl = statusTTLLong
			return resolveRemoteCmd([]string{input}, true)
		}
	case tea.KeyBackspace:
		m.urlInput = removeLastRune(m.urlInput)
	default:
		if msg.Type == tea.KeyRunes {
			m.urlInput += string(msg.Runes)
		}
	}
	return nil
}

// handlePlaylistManagerKey dispatches keys to the active manager screen.
func (m *Model) handlePlaylistManagerKey(msg tea.KeyMsg) tea.Cmd {
	switch m.plManager.screen {
	case plMgrScreenList:
		return m.handlePlMgrListKey(msg)
	case plMgrScreenTracks:
		return m.handlePlMgrTracksKey(msg)
	case plMgrScreenNewName:
		return m.handlePlMgrNewNameKey(msg)
	}
	return nil
}

// handlePlMgrListKey handles keys on screen 0 (playlist list).
func (m *Model) handlePlMgrListKey(msg tea.KeyMsg) tea.Cmd {
	// If waiting for delete confirmation, only accept y/n.
	if m.plManager.confirmDel {
		switch msg.String() {
		case "y", "Y":
			if m.plManager.cursor < len(m.plManager.playlists) {
				name := m.plManager.playlists[m.plManager.cursor].Name
				if err := m.localProvider.DeletePlaylist(name); err != nil {
					m.status.text = fmt.Sprintf("Delete failed: %s", err)
					m.status.ttl = statusTTLDefault
				} else {
					m.status.text = fmt.Sprintf("Deleted \"%s\"", name)
					m.status.ttl = statusTTLDefault
				}
				m.plMgrRefreshList()
			}
			m.plManager.confirmDel = false
		default:
			m.plManager.confirmDel = false
		}
		return nil
	}

	count := len(m.plManager.playlists) + 1 // +1 for "+ New Playlist..."
	switch msg.String() {
	case "ctrl+c":
		m.plManager.visible = false
		return m.quit()
	case "up", "k":
		if m.plManager.cursor > 0 {
			m.plManager.cursor--
		} else if count > 0 {
			m.plManager.cursor = count-1
		}
	case "down", "j":
		if m.plManager.cursor < count-1 {
			m.plManager.cursor++
		} else if count > 0 {
			m.plManager.cursor = 0
		}
	case "enter", "l", "right":
		if m.plManager.cursor < len(m.plManager.playlists) {
			m.plMgrEnterTrackList(m.plManager.playlists[m.plManager.cursor].Name)
		} else {
			// "+ New Playlist..." selected
			m.plManager.screen = plMgrScreenNewName
			m.plManager.newName = ""
		}
	case "a":
		// Quick-add current track to the highlighted playlist.
		if m.plManager.cursor < len(m.plManager.playlists) {
			m.addToPlaylist(m.plManager.playlists[m.plManager.cursor].Name)
			m.plMgrRefreshList()
		}
	case "d":
		if m.plManager.cursor < len(m.plManager.playlists) {
			m.plManager.confirmDel = true
		}
	case "esc", "p":
		m.plManager.visible = false
	}
	return nil
}

// handlePlMgrTracksKey handles keys on screen 1 (track list inside a playlist).
func (m *Model) handlePlMgrTracksKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.plManager.visible = false
		return m.quit()
	case "up", "k":
		if m.plManager.cursor > 0 {
			m.plManager.cursor--
		} else if len(m.plManager.tracks) > 0 {
			m.plManager.cursor = len(m.plManager.tracks)-1
		}
	case "down", "j":
		if m.plManager.cursor < len(m.plManager.tracks)-1 {
			m.plManager.cursor++
		} else if len(m.plManager.tracks) > 0 {
			m.plManager.cursor = 0
		}
	case "enter":
		// Replace playlist and start playback.
		if len(m.plManager.tracks) > 0 {
			m.player.Stop()
			m.player.ClearPreload()
			m.resetYTDLBatch()
			m.playlist.Replace(m.plManager.tracks)
			m.plCursor = 0
			m.playlist.SetIndex(0)
			m.adjustScroll()
			m.plManager.visible = false
			m.focus = focusPlaylist
			cmd := m.playCurrentTrack()
			m.notifyMPRIS()
			return cmd
		}
	case "a":
		m.addToPlaylist(m.plManager.selPlaylist)
		if tracks, err := m.localProvider.Tracks(m.plManager.selPlaylist); err == nil {
			m.plManager.tracks = tracks
		}
	case "d":
		// Remove highlighted track.
		if len(m.plManager.tracks) > 0 && m.plManager.cursor < len(m.plManager.tracks) {
			err := m.localProvider.RemoveTrack(m.plManager.selPlaylist, m.plManager.cursor)
			if err != nil {
				m.status.text = fmt.Sprintf("Remove failed: %s", err)
				m.status.ttl = statusTTLDefault
			} else {
				m.status.text = "Track removed"
				m.status.ttl = statusTTLDefault
			}
			// Reload tracks (or go back if playlist was deleted).
			tracks, err := m.localProvider.Tracks(m.plManager.selPlaylist)
			if err != nil || len(tracks) == 0 {
				// Playlist was auto-deleted (empty). Return to list.
				m.plMgrRefreshList()
				m.plManager.screen = plMgrScreenList
				m.plManager.cursor = 0
				return nil
			}
			m.plManager.tracks = tracks
			if m.plManager.cursor >= len(m.plManager.tracks) {
				m.plManager.cursor = len(m.plManager.tracks) - 1
			}
		}
	case "esc", "backspace", "h", "left":
		// Go back to playlist list.
		m.plMgrRefreshList()
		m.plManager.screen = plMgrScreenList
		// Try to position cursor on the playlist we just left.
		for i, pl := range m.plManager.playlists {
			if pl.Name == m.plManager.selPlaylist {
				m.plManager.cursor = i
				break
			}
		}
		m.plManager.confirmDel = false
	}
	return nil
}

// handlePlMgrNewNameKey handles keys on screen 2 (new playlist name input).
func (m *Model) handlePlMgrNewNameKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEscape:
		m.plManager.screen = plMgrScreenList
	case tea.KeyEnter:
		name := strings.TrimSpace(m.plManager.newName)
		if name != "" {
			m.addToPlaylist(name)
			m.plMgrRefreshList()
			m.plManager.screen = plMgrScreenList
		}
	case tea.KeyBackspace:
		m.plManager.newName = removeLastRune(m.plManager.newName)
	case tea.KeySpace:
		m.plManager.newName += " "
	default:
		if msg.Type == tea.KeyRunes {
			m.plManager.newName += string(msg.Runes)
		}
	}
	return nil
}

// addToPlaylist appends the current track to a local playlist and shows a status message.
func (m *Model) addToPlaylist(name string) {
	track, idx := m.playlist.Current()
	if idx < 0 {
		m.status.text = "No track to add"
		m.status.ttl = statusTTLShort
		return
	}
	if err := m.localProvider.AddTrack(name, track); err != nil {
		m.status.text = fmt.Sprintf("Failed: %s", err)
		m.status.ttl = statusTTLDefault
		return
	}
	m.status.text = fmt.Sprintf("Added to \"%s\"", name)
	m.status.ttl = statusTTLDefault
}

// handleThemeKey processes key presses while the theme picker is open.
func (m *Model) handleThemeKey(msg tea.KeyMsg) tea.Cmd {
	count := len(m.themes) + 1 // +1 for Default
	switch msg.String() {
	case "ctrl+c":
		m.themePickerCancel()
		return m.quit()
	case "up", "k":
		if m.themePicker.cursor > 0 {
			m.themePicker.cursor--
			m.themePickerApply() // live preview
		} else {
			m.themePicker.cursor = count-1
			m.themePickerApply() // live preview
		}
	case "down", "j":
		if m.themePicker.cursor < count-1 {
			m.themePicker.cursor++
			m.themePickerApply() // live preview
		} else {
			m.themePicker.cursor = 0
			m.themePickerApply() // live preview
		}
	case "enter":
		m.themePickerSelect()
	case "esc", "q", "t":
		m.themePickerCancel()
	}
	return nil
}

// handleQueueKey processes key presses while the queue manager overlay is open.
func (m *Model) handleQueueKey(msg tea.KeyMsg) tea.Cmd {
	qLen := m.playlist.QueueLen()

	switch msg.String() {
	case "ctrl+c":
		m.queue.visible = false
		return m.quit()
	case "ctrl+k":
		m.keymap.visible = true
	case "up", "k":
		if m.queue.cursor > 0 {
			m.queue.cursor--
		} else if qLen > 0 {
			m.queue.cursor = qLen-1
		}
	case "down", "j":
		if m.queue.cursor < qLen-1 {
			m.queue.cursor++
		} else if qLen > 0 {
			m.queue.cursor = 0
		}
	case "shift+up":
		if m.queue.cursor > 0 {
			if m.playlist.MoveQueue(m.queue.cursor, m.queue.cursor-1) {
				m.queue.cursor--
			}
		}
	case "shift+down":
		if m.queue.cursor < qLen-1 {
			if m.playlist.MoveQueue(m.queue.cursor, m.queue.cursor+1) {
				m.queue.cursor++
			}
		}
	case "d":
		if qLen > 0 {
			m.playlist.RemoveQueueAt(m.queue.cursor)
			if m.queue.cursor >= m.playlist.QueueLen() && m.queue.cursor > 0 {
				m.queue.cursor--
			}
		}
	case "c":
		m.playlist.ClearQueue()
		m.queue.visible = false
	case "esc", "A":
		m.queue.visible = false
	}
	return nil
}
