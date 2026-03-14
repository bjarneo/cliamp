package ui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"cliamp/config"
	"cliamp/playlist"
)

// quit shuts down the player and signals the TUI to exit.
func (m *Model) quit() tea.Cmd {
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
	if m.showKeymap {
		return m.handleKeymapKey(msg)
	}

	// Navidrome explore browser overlay
	if m.showNavBrowser {
		return m.handleNavBrowserKey(msg)
	}

	// Theme picker overlay — interactive navigation
	if m.showThemes {
		return m.handleThemeKey(msg)
	}

	// Playlist manager overlay (browse, add, remove, delete)
	if m.showPlManager {
		return m.handlePlaylistManagerKey(msg)
	}

	// File browser overlay
	if m.showFileBrowser {
		return m.handleFileBrowserKey(msg)
	}

	// Queue manager overlay
	if m.showQueue {
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
	if m.showLyrics {
		switch msg.String() {
		case "ctrl+c":
			return m.quit()
		case "esc", "y":
			m.showLyrics = false
		case "up", "k":
			if !(m.lyricsSyncable() && m.lyricsHaveTimestamps()) && m.lyricsScroll > 0 {
				m.lyricsScroll--
			}
		case "down", "j":
			if !(m.lyricsSyncable() && m.lyricsHaveTimestamps()) {
				maxScroll := len(m.lyricsLines) - 1
				if maxScroll < 0 {
					maxScroll = 0
				}
				if m.lyricsScroll < maxScroll {
					m.lyricsScroll++
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

	if m.searching {
		return m.handleSearchKey(msg)
	}

	if m.netSearching {
		return m.handleNetSearchKey(msg)
	}

	if m.provSearching {
		return m.handleProvSearchKey(msg)
	}

	if m.focus == focusProvider {
		switch msg.String() {
		case "q", "ctrl+c":
			return m.quit()
		case "up", "k":
			if m.provCursor > 0 {
				m.provCursor--
			}
		case " ":
			return m.togglePlayPause()
		case "down", "j":
			if m.provCursor < len(m.providerLists)-1 {
				m.provCursor++
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
			m.provSearching = true
			m.provSearchQuery = ""
			m.provSearchResults = nil
			m.provSearchCursor = 0
		case "o":
			m.openFileBrowser()
		case "N":
			if m.navClient != nil {
				m.openNavBrowser()
			}
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

	case "up", "k":
		if m.focus == focusEQ {
			bands := m.player.EQBands()
			m.player.SetEQBand(m.eqCursor, bands[m.eqCursor]+1)
			m.eqPresetIdx = -1 // manual tweak → custom
		} else {
			if m.plCursor > 0 {
				m.plCursor--
				m.adjustScroll()
			}
		}

	case "down", "j":
		if m.focus == focusEQ {
			bands := m.player.EQBands()
			m.player.SetEQBand(m.eqCursor, bands[m.eqCursor]-1)
			m.eqPresetIdx = -1 // manual tweak → custom
		} else {
			if m.plCursor < m.playlist.Len()-1 {
				m.plCursor++
				m.adjustScroll()
			}
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
			m.saveMsg = fmt.Sprintf("Config save failed: %s", err)
			m.saveMsgTTL = 60
		}
		m.player.ClearPreload()
		return m.preloadNext()

	case "z":
		m.playlist.ToggleShuffle()
		if err := config.Save("shuffle", fmt.Sprintf("%v", m.playlist.Shuffled())); err != nil {
			m.saveMsg = fmt.Sprintf("Config save failed: %s", err)
			m.saveMsgTTL = 60
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

	case "a":
		if m.focus == focusPlaylist {
			if !m.playlist.Dequeue(m.plCursor) {
				m.playlist.Queue(m.plCursor)
			}
		}

	case "A":
		if m.focus == focusPlaylist {
			m.showQueue = true
			m.queueCursor = 0
		}

	case "S":
		return m.saveTrack()

	case "m":
		m.player.ToggleMono()

	case "/":
		m.searching = true
		m.searchQuery = ""
		m.searchResults = nil
		m.searchCursor = 0
		m.prevFocus = m.focus
		m.focus = focusSearch

	case "f", "F":
		m.netSearching = true
		m.netSearchQuery = ""
		m.netSearchSC = msg.String() == "F"
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
		m.showLyrics = !m.showLyrics
		if m.showLyrics && !m.lyricsLoading {
			artist, title := m.lyricsArtistTitle()
			if artist != "" && title != "" {
				q := artist + "\n" + title
				if q != m.lyricsQuery {
					m.lyricsQuery = q
					m.lyricsLoading = true
					m.lyricsLines = nil
					m.lyricsErr = nil
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

	case "v":
		m.vis.CycleMode()

	case "V":
		m.fullVis = !m.fullVis
		if m.fullVis {
			m.vis.Rows = max(defaultVisRows, (m.height-10)*4/5)
		} else {
			m.vis.Rows = defaultVisRows
		}

	case "x":
		if m.focus == focusPlaylist {
			if m.plVisible <= 5 {
				// Expand: recalculate dynamic max from terminal height.
				probe := strings.Join([]string{
					m.renderTitle(), m.renderTrackInfo(), m.renderTimeStatus(), "",
					m.renderSpectrum(), m.renderSeekBar(), "",
					m.renderControls(), "", m.renderPlaylistHeader(),
					"x", "", m.renderHelp(), m.renderStreamStatus(),
				}, "\n")
				fixedLines := lipgloss.Height(frameStyle.Render(probe)) - 1
				m.plVisible = max(5, m.height-fixedLines)
			} else {
				m.plVisible = 5
			}
			m.adjustScroll()
		}

	case "ctrl+k":
		m.showKeymap = true
	}

	return nil
}

// saveTrack copies the current track to ~/Music/cliamp/ with a clean filename.
// For yt-dlp tracks (piped streams), triggers an async download via yt-dlp.
// For local temp files, copies synchronously.
func (m *Model) saveTrack() tea.Cmd {
	track, idx := m.playlist.Current()
	if idx < 0 {
		m.saveMsg = "Nothing to save"
		m.saveMsgTTL = 40 // ~2s at 50ms ticks
		return nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		m.saveMsg = fmt.Sprintf("Save failed: %s", err)
		m.saveMsgTTL = 40
		return nil
	}

	saveDir := filepath.Join(home, "Music", "cliamp")
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		m.saveMsg = fmt.Sprintf("Save failed: %s", err)
		m.saveMsgTTL = 40
		return nil
	}

	// YouTube/yt-dlp tracks: async download directly to ~/Music/cliamp/.
	if playlist.IsYouTubeURL(track.Path) || playlist.IsYTDL(track.Path) {
		m.saveMsg = "Downloading..."
		m.saveMsgTTL = 600 // cleared by ytdlSavedMsg
		return saveYTDLCmd(track.Path, saveDir)
	}

	// Only save local temp files (yt-dlp downloads), not streams or user's own files.
	if track.Stream || !strings.HasPrefix(track.Path, os.TempDir()) {
		m.saveMsg = "Only downloaded tracks can be saved"
		m.saveMsgTTL = 40
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

	if err := copyFile(track.Path, dest); err != nil {
		m.saveMsg = fmt.Sprintf("Save failed: %s", err)
		m.saveMsgTTL = 40
		return nil
	}

	m.saveMsg = fmt.Sprintf("Saved to ~/Music/cliamp/%s", name+ext)
	m.saveMsgTTL = 60 // ~3s
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		os.Remove(dst) // clean up partial file
		return copyErr
	}
	if closeErr != nil {
		os.Remove(dst)
		return closeErr
	}
	return nil
}

// removeLastRune trims the final UTF-8 rune from s, used by all text input handlers.
func removeLastRune(s string) string {
	if len(s) > 0 {
		_, size := utf8.DecodeLastRuneInString(s)
		return s[:len(s)-size]
	}
	return s
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

// handleSearchKey processes key presses while in search mode.
// handleProvSearchKey processes key presses while filtering the provider playlist list.
func (m *Model) handleProvSearchKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEscape:
		m.provSearching = false
	case tea.KeyEnter:
		if len(m.provSearchResults) > 0 && !m.provLoading {
			idx := m.provSearchResults[m.provSearchCursor]
			m.provCursor = idx
			m.provLoading = true
			m.provSearching = false
			return fetchTracksCmd(m.provider, m.providerLists[idx].ID)
		}
	case tea.KeyUp:
		if m.provSearchCursor > 0 {
			m.provSearchCursor--
		}
	case tea.KeyDown:
		if m.provSearchCursor < len(m.provSearchResults)-1 {
			m.provSearchCursor++
		}
	case tea.KeyBackspace:
		if m.provSearchQuery != "" {
			m.provSearchQuery = removeLastRune(m.provSearchQuery)
			m.updateProvSearch()
		}
	case tea.KeySpace:
		m.provSearchQuery += " "
		m.updateProvSearch()
	default:
		if msg.Type == tea.KeyRunes {
			m.provSearchQuery += string(msg.Runes)
			m.updateProvSearch()
		}
	}
	return nil
}

func (m *Model) updateProvSearch() {
	m.provSearchResults = nil
	m.provSearchCursor = 0
	if m.provSearchQuery == "" {
		return
	}
	q := strings.ToLower(m.provSearchQuery)
	for i, pl := range m.providerLists {
		if strings.Contains(strings.ToLower(pl.Name), q) {
			m.provSearchResults = append(m.provSearchResults, i)
		}
	}
}

func (m *Model) handleSearchKey(msg tea.KeyMsg) tea.Cmd {
	// Allow opening overlays during search (ctrl combos don't conflict with text input).
	switch msg.String() {
	case "ctrl+k":
		m.showKeymap = true
		return nil
	}

	switch msg.Type {
	case tea.KeyEscape:
		m.searching = false
		m.focus = m.prevFocus

	case tea.KeyEnter:
		var cmd tea.Cmd
		if len(m.searchResults) > 0 {
			idx := m.searchResults[m.searchCursor]
			m.playlist.SetIndex(idx)
			m.plCursor = idx
			m.adjustScroll()
			cmd = m.playCurrentTrack()
			m.notifyMPRIS()
		}
		m.searching = false
		m.focus = focusPlaylist
		return cmd

	case tea.KeyTab:
		// Toggle queue for selected search result.
		if len(m.searchResults) > 0 && m.searchCursor < len(m.searchResults) {
			idx := m.searchResults[m.searchCursor]
			if !m.playlist.Dequeue(idx) {
				m.playlist.Queue(idx)
			}
		}

	case tea.KeyUp:
		if m.searchCursor > 0 {
			m.searchCursor--
		}

	case tea.KeyDown:
		if m.searchCursor < len(m.searchResults)-1 {
			m.searchCursor++
		}

	case tea.KeyBackspace:
		if m.searchQuery != "" {
			m.searchQuery = removeLastRune(m.searchQuery)
			m.updateSearch()
		}

	case tea.KeySpace:
		m.searchQuery += " "
		m.updateSearch()

	default:
		if msg.Type == tea.KeyRunes {
			m.searchQuery += string(msg.Runes)
			m.updateSearch()
		}
	}

	return nil
}

// handleNetSearchKey processes key presses while in net search mode.
func (m *Model) handleNetSearchKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+k":
		m.showKeymap = true
		return nil
	}

	switch msg.Type {
	case tea.KeyEscape:
		m.netSearching = false
		m.focus = m.prevFocus

	case tea.KeyEnter:
		var cmd tea.Cmd
		m.netSearching = false
		m.focus = m.prevFocus
		if strings.TrimSpace(m.netSearchQuery) != "" {
			prefix := "ytsearch1:"
			if m.netSearchSC {
				prefix = "scsearch1:"
			}
			m.saveMsg = "Queuing search..."
			m.saveMsgTTL = 40
			cmd = fetchNetSearchCmd(prefix + strings.TrimSpace(m.netSearchQuery))
		}
		return cmd

	case tea.KeyBackspace:
		m.netSearchQuery = removeLastRune(m.netSearchQuery)

	case tea.KeySpace:
		m.netSearchQuery += " "

	default:
		if msg.Type == tea.KeyRunes {
			m.netSearchQuery += string(msg.Runes)
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
			m.saveMsg = "Loading URL..."
			m.saveMsgTTL = 120
			return resolveRemoteCmd([]string{input})
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
	switch m.plMgrScreen {
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
	if m.plMgrConfirmDel {
		switch msg.String() {
		case "y", "Y":
			if m.plMgrCursor < len(m.plMgrPlaylists) {
				name := m.plMgrPlaylists[m.plMgrCursor].Name
				if err := m.localProvider.DeletePlaylist(name); err != nil {
					m.saveMsg = fmt.Sprintf("Delete failed: %s", err)
					m.saveMsgTTL = 60
				} else {
					m.saveMsg = fmt.Sprintf("Deleted \"%s\"", name)
					m.saveMsgTTL = 60
				}
				m.plMgrRefreshList()
			}
			m.plMgrConfirmDel = false
		default:
			m.plMgrConfirmDel = false
		}
		return nil
	}

	count := len(m.plMgrPlaylists) + 1 // +1 for "+ New Playlist..."
	switch msg.String() {
	case "ctrl+c":
		m.showPlManager = false
		return m.quit()
	case "up", "k":
		if m.plMgrCursor > 0 {
			m.plMgrCursor--
		}
	case "down", "j":
		if m.plMgrCursor < count-1 {
			m.plMgrCursor++
		}
	case "enter", "l", "right":
		if m.plMgrCursor < len(m.plMgrPlaylists) {
			m.plMgrEnterTrackList(m.plMgrPlaylists[m.plMgrCursor].Name)
		} else {
			// "+ New Playlist..." selected
			m.plMgrScreen = plMgrScreenNewName
			m.plMgrNewName = ""
		}
	case "a":
		// Quick-add current track to the highlighted playlist.
		if m.plMgrCursor < len(m.plMgrPlaylists) {
			m.addToPlaylist(m.plMgrPlaylists[m.plMgrCursor].Name)
			m.plMgrRefreshList()
		}
	case "d":
		if m.plMgrCursor < len(m.plMgrPlaylists) {
			m.plMgrConfirmDel = true
		}
	case "esc", "p":
		m.showPlManager = false
	}
	return nil
}

// handlePlMgrTracksKey handles keys on screen 1 (track list inside a playlist).
func (m *Model) handlePlMgrTracksKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.showPlManager = false
		return m.quit()
	case "up", "k":
		if m.plMgrCursor > 0 {
			m.plMgrCursor--
		}
	case "down", "j":
		if m.plMgrCursor < len(m.plMgrTracks)-1 {
			m.plMgrCursor++
		}
	case "enter":
		// Replace playlist and start playback.
		if len(m.plMgrTracks) > 0 {
			m.player.Stop()
			m.player.ClearPreload()
			m.resetYTDLBatch()
			m.playlist.Replace(m.plMgrTracks)
			m.plCursor = 0
			m.playlist.SetIndex(0)
			m.adjustScroll()
			m.showPlManager = false
			m.focus = focusPlaylist
			cmd := m.playCurrentTrack()
			m.notifyMPRIS()
			return cmd
		}
	case "a":
		m.addToPlaylist(m.plMgrSelPlaylist)
		if tracks, err := m.localProvider.Tracks(m.plMgrSelPlaylist); err == nil {
			m.plMgrTracks = tracks
		}
	case "d":
		// Remove highlighted track.
		if len(m.plMgrTracks) > 0 && m.plMgrCursor < len(m.plMgrTracks) {
			err := m.localProvider.RemoveTrack(m.plMgrSelPlaylist, m.plMgrCursor)
			if err != nil {
				m.saveMsg = fmt.Sprintf("Remove failed: %s", err)
				m.saveMsgTTL = 60
			} else {
				m.saveMsg = "Track removed"
				m.saveMsgTTL = 60
			}
			// Reload tracks (or go back if playlist was deleted).
			tracks, err := m.localProvider.Tracks(m.plMgrSelPlaylist)
			if err != nil || len(tracks) == 0 {
				// Playlist was auto-deleted (empty). Return to list.
				m.plMgrRefreshList()
				m.plMgrScreen = plMgrScreenList
				m.plMgrCursor = 0
				return nil
			}
			m.plMgrTracks = tracks
			if m.plMgrCursor >= len(m.plMgrTracks) {
				m.plMgrCursor = len(m.plMgrTracks) - 1
			}
		}
	case "esc", "backspace", "h", "left":
		// Go back to playlist list.
		m.plMgrRefreshList()
		m.plMgrScreen = plMgrScreenList
		// Try to position cursor on the playlist we just left.
		for i, pl := range m.plMgrPlaylists {
			if pl.Name == m.plMgrSelPlaylist {
				m.plMgrCursor = i
				break
			}
		}
		m.plMgrConfirmDel = false
	}
	return nil
}

// handlePlMgrNewNameKey handles keys on screen 2 (new playlist name input).
func (m *Model) handlePlMgrNewNameKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEscape:
		m.plMgrScreen = plMgrScreenList
	case tea.KeyEnter:
		name := strings.TrimSpace(m.plMgrNewName)
		if name != "" {
			m.addToPlaylist(name)
			m.plMgrRefreshList()
			m.plMgrScreen = plMgrScreenList
		}
	case tea.KeyBackspace:
		m.plMgrNewName = removeLastRune(m.plMgrNewName)
	case tea.KeySpace:
		m.plMgrNewName += " "
	default:
		if msg.Type == tea.KeyRunes {
			m.plMgrNewName += string(msg.Runes)
		}
	}
	return nil
}

// addToPlaylist appends the current track to a local playlist and shows a status message.
func (m *Model) addToPlaylist(name string) {
	track, idx := m.playlist.Current()
	if idx < 0 {
		m.saveMsg = "No track to add"
		m.saveMsgTTL = 40
		return
	}
	if err := m.localProvider.AddTrack(name, track); err != nil {
		m.saveMsg = fmt.Sprintf("Failed: %s", err)
		m.saveMsgTTL = 60
		return
	}
	m.saveMsg = fmt.Sprintf("Added to \"%s\"", name)
	m.saveMsgTTL = 60 // ~3s
}

// handleThemeKey processes key presses while the theme picker is open.
func (m *Model) handleThemeKey(msg tea.KeyMsg) tea.Cmd {
	count := len(m.themes) + 1 // +1 for Default
	switch msg.String() {
	case "ctrl+c":
		m.themePickerCancel()
		return m.quit()
	case "up", "k":
		if m.themeCursor > 0 {
			m.themeCursor--
			m.themePickerApply() // live preview
		}
	case "down", "j":
		if m.themeCursor < count-1 {
			m.themeCursor++
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
		m.showQueue = false
		return m.quit()
	case "ctrl+k":
		m.showKeymap = true
	case "up", "k":
		if m.queueCursor > 0 {
			m.queueCursor--
		}
	case "down", "j":
		if m.queueCursor < qLen-1 {
			m.queueCursor++
		}
	case "d":
		if qLen > 0 {
			m.playlist.RemoveQueueAt(m.queueCursor)
			if m.queueCursor >= m.playlist.QueueLen() && m.queueCursor > 0 {
				m.queueCursor--
			}
		}
	case "c":
		m.playlist.ClearQueue()
		m.showQueue = false
	case "esc", "A":
		m.showQueue = false
	}
	return nil
}

// keymapEntry is a key-action pair for the keymap overlay.
type keymapEntry struct{ key, action string }

// keymapEntries is the full list of keybindings shown in the keymap overlay.
var keymapEntries = []keymapEntry{
	{"Space", "Play / Pause"},
	{"s", "Stop"},
	{"> .", "Next track"},
	{"< ,", "Previous track"},
	{"← →", "Seek ±5s"},
	{"Shift+← →", "Seek ±large step"},
	{"+ -", "Volume up/down"},
	{"z", "Toggle shuffle"},
	{"r", "Cycle repeat"},
	{"m", "Toggle mono"},
	{"e", "Cycle EQ preset"},
	{"t", "Choose theme"},
	{"v", "Cycle visualizer"},
	{"V", "Full-screen visualizer"},
	{"↑ ↓", "Playlist scroll / EQ adjust"},
	{"h l", "EQ cursor left/right"},
	{"Enter", "Play selected track"},
	{"a", "Toggle queue (play next)"},
	{"A", "Queue manager"},
	{"o", "Open file browser"},
	{"N", "Navidrome browser"},
	{"J", "Jump to time"},
	{"p", "Playlist manager"},
	{"i", "Track info / metadata"},
	{"S", "Save/download track to ~/Music"},
	{"x", "Expand/collapse playlist"},
	{"/", "Search playlist"},
	{"f", "Find on YouTube (queue play next)"},
	{"F", "Find on SoundCloud (queue play next)"},
	{"u", "Load URL (stream/playlist)"},
	{"y", "Show lyrics"},
	{"Tab", "Toggle focus"},
	{"Esc", "Back to provider"},
	{"Ctrl+K", "This keymap"},
	{"q", "Quit"},
}

// handleKeymapKey processes key presses while the keymap overlay is open.
func (m *Model) handleKeymapKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEscape:
		m.showKeymap = false
		m.keymapSearch = ""
		m.keymapFiltered = nil
		m.keymapCursor = 0
	case tea.KeyUp:
		if m.keymapCursor > 0 {
			m.keymapCursor--
		}
	case tea.KeyDown:
		count := len(keymapEntries)
		if m.keymapSearch != "" {
			count = len(m.keymapFiltered)
		}
		if m.keymapCursor < count-1 {
			m.keymapCursor++
		}
	case tea.KeyBackspace:
		if m.keymapSearch != "" {
			m.keymapSearch = removeLastRune(m.keymapSearch)
			m.updateKeymapFilter()
		}
	case tea.KeySpace:
		m.keymapSearch += " "
		m.updateKeymapFilter()
	default:
		switch msg.String() {
		case "ctrl+c":
			m.showKeymap = false
			return m.quit()
		default:
			if msg.Type == tea.KeyRunes {
				m.keymapSearch += string(msg.Runes)
				m.updateKeymapFilter()
			}
		}
	}
	return nil
}

// updateKeymapFilter rebuilds the filtered indices and clamps the cursor.
func (m *Model) updateKeymapFilter() {
	m.keymapFiltered = nil
	m.keymapCursor = 0
	if m.keymapSearch == "" {
		return
	}
	query := strings.ToLower(m.keymapSearch)
	for i, e := range keymapEntries {
		if strings.Contains(strings.ToLower(e.key), query) ||
			strings.Contains(strings.ToLower(e.action), query) {
			m.keymapFiltered = append(m.keymapFiltered, i)
		}
	}
}

