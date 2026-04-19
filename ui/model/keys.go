package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"cliamp/internal/fileutil"
	"cliamp/playlist"
	"cliamp/provider"
	"cliamp/ui"
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
			m.exitResume.playlist = m.loadedPlaylist
		}
	}

	m.flushPendingSpeedSave()
	m.player.Close()
	m.quitting = true
	return tea.Quit
}

// scrobbleCurrent fires a scrobble for the currently playing track if applicable.
func (m *Model) scrobbleCurrent() {
	if track, idx := m.playlist.Current(); idx >= 0 {
		m.maybeScrobble(track, m.player.Position(), m.player.Duration())
	}
}

func (m *Model) handleSpeedKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "q", "ctrl+c":
		return m.quit()
	case "]", "right", "l", "up", "k":
		m.changeSpeed(0.25)
	case "[", "left", "h", "down", "j":
		m.changeSpeed(-0.25)
	case "tab":
		m.focus = focusPlaylist
	case "esc", "backspace":
		if len(m.providers) > 1 {
			m.focus = focusProvPill
		} else {
			m.focus = focusEQ
		}
	case "space":
		return m.togglePlayPause()
	}
	return nil
}

func (m *Model) providerScrollStep() int {
	return max(1, m.effectivePlaylistVisible())
}

func (m *Model) providerMaybeAdjustScroll() {
	visible := m.providerScrollStep()
	total := len(m.providerLists)
	if total == 0 {
		m.provScroll = 0
		return
	}

	if m.provCursor < m.provScroll {
		m.provScroll = m.provCursor
	}

	// Sectioned providers (e.g. radio) render extra header rows, so
	// cursor visibility must be computed in rendered rows, not item count.
	if sl, ok := m.provider.(provider.SectionedList); ok {
		if m.provScroll >= total {
			m.provScroll = max(0, total-1)
		}

		// Only push down when needed to keep the cursor visible.
		// Do not "pull up" aggressively, which can make paging feel jumpy
		// and keep the cursor stuck near the bottom of the viewport.
		for m.provScroll < total && m.providerRowsFromScroll(sl, m.provScroll, m.provCursor) > visible {
			m.provScroll++
		}
		return
	}

	// Non-sectioned providers: regular item-count based scrolling.
	if m.provCursor >= m.provScroll+visible {
		m.provScroll = m.provCursor - visible + 1
	}
	if m.provScroll+visible > total {
		m.provScroll = max(0, total-visible)
	}
}

func (m *Model) providerRowsFromScroll(sl provider.SectionedList, scroll, cursor int) int {
	total := len(m.providerLists)
	if total == 0 || cursor < scroll || scroll < 0 || cursor >= total {
		return 0
	}

	rows := 0
	prevPrefix := ""
	if scroll > 0 {
		prevPrefix = sl.IDPrefix(m.providerLists[scroll-1].ID)
	}

	for i := scroll; i <= cursor && i < total; i++ {
		pfx := sl.IDPrefix(m.providerLists[i].ID)
		if pfx != prevPrefix {
			rows++ // section header row
		}
		rows++ // item row
		prevPrefix = pfx
	}
	return rows
}

func (m *Model) providerMoveUp() {
	if m.provCursor > 0 {
		m.provCursor--
	} else if len(m.providerLists) > 0 {
		m.provCursor = len(m.providerLists) - 1
	}
	m.providerMaybeAdjustScroll()
}

func (m *Model) providerMoveDown() {
	if m.provCursor < len(m.providerLists)-1 {
		m.provCursor++
	} else if len(m.providerLists) > 0 {
		m.provCursor = 0
	}
	m.providerMaybeAdjustScroll()
}

func (m *Model) providerPageUp() {
	step := m.providerScrollStep()
	if m.provCursor > 0 {
		m.provCursor -= min(m.provCursor, step)
	}
	// Top-anchor behavior: place cursor at top of viewport when paging up.
	m.provScroll = m.provCursor
	m.providerMaybeAdjustScroll()
}

func (m *Model) providerPageDown() {
	step := m.providerScrollStep()
	if m.provCursor < len(m.providerLists)-1 {
		m.provCursor = min(len(m.providerLists)-1, m.provCursor+step)
	}
	// Bottom-anchor behavior: bias viewport so cursor lands near bottom when paging down.
	m.provScroll = max(0, m.provCursor-step+1)
	m.providerMaybeAdjustScroll()
}

func (m *Model) providerToTop() {
	m.provCursor = 0
	m.providerMaybeAdjustScroll()
}

func (m *Model) providerToBottom() {
	if len(m.providerLists) > 0 {
		m.provCursor = len(m.providerLists) - 1
	}
	m.providerMaybeAdjustScroll()
}

// handleKey processes a single key press and returns an optional command.
func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.keymap.visible {
		return m.handleKeymapKey(msg)
	}

	// Audio device picker overlay
	if m.devicePicker.visible {
		return m.handleDeviceKey(msg)
	}

	// Navidrome explore browser overlay
	if m.navBrowser.visible {
		return m.handleNavBrowserKey(msg)
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
				maxScroll := max(len(m.lyrics.lines)-1, 0)
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

	if m.spotSearch.visible {
		return m.handleSpotSearchKey(msg)
	}

	if m.provSearch.active {
		return m.handleProvSearchKey(msg)
	}

	if m.focus == focusProvider {
		switch msg.String() {
		case "q", "ctrl+c":
			return m.quit()
		case "up", "k":
			m.providerMoveUp()
		case "space":
			return m.togglePlayPause()
		case "down", "j":
			m.providerMoveDown()
			// Auto-load next catalog page when scrolling near the bottom.
			return m.maybeLoadCatalogBatch()
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
			// If viewing catalog search results, clear them first.
			if cs, ok := m.provider.(provider.CatalogSearcher); ok && cs.IsSearching() {
				m.restoreCatalog(cs)
				return nil
			}
			if m.playlist.Len() > 0 {
				m.focus = focusPlaylist
			}
		case "/":
			m.provSearch.active = true
			m.provSearch.query = ""
			m.provSearch.results = nil
			m.provSearch.cursor = 0
		case "f":
			return m.toggleProviderFavorite()
		case "o":
			m.openFileBrowser()
		case "N":
			if prov := m.findBrowseProvider(); prov != nil {
				m.openNavBrowserWith(prov)
			}
		case "pgup", "ctrl+u":
			m.providerPageUp()
		case "pgdown", "ctrl+d":
			m.providerPageDown()
			return m.maybeLoadCatalogBatch()
		case "g", "home":
			m.providerToTop()
		case "G", "end":
			m.providerToBottom()
			return m.maybeLoadCatalogBatch()
		case "ctrl+j":
			m.openJumpMode()
		case "J":
			return m.switchToProvider("jellyfin")
		case "ctrl+x":
			m.toggleExpandPlaylist()
		}
		return nil
	}

	if m.focus == focusSpeed {
		return m.handleSpeedKey(msg)
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
			m.focus = focusSpeed
		case "esc", "backspace":
			m.focus = focusEQ
		case "space":
			return m.togglePlayPause()
		}
		return nil
	}

	// Shift+0-9: percentage seeking.
	shiftedDigits := map[string]int{
		")": 0, "!": 1, "@": 2, "#": 3, "$": 4,
		"%": 5, "^": 6, "&": 7, "*": 8, "(": 9,
	}
	if digit, ok := shiftedDigits[msg.Text]; ok {
		dur := m.player.Duration()
		if dur > 0 {
			target := dur * time.Duration(digit) / 10
			return m.seekAbsolute(target)
		}
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m.quit()
	case "esc", "backspace", "b":
		if m.fullVis {
			m.fullVis = false
			m.vis.Rows = ui.DefaultVisRows
			m.restorePanelWidth()
		} else if m.focus == focusPlaylist {
			// Keep current expanded/collapsed height mode when switching focus.
			m.focus = focusProvider
		}

	case "space":
		cmd := m.togglePlayPause()
		m.notifyPlayback()
		return cmd

	case "s":
		m.player.Stop()
		m.notifyPlayback()

	case ">", ".":
		m.scrobbleCurrent()
		cmd := m.nextTrack()
		m.notifyPlayback()
		return cmd

	case "<", ",":
		m.scrobbleCurrent()
		cmd := m.prevTrack()
		m.notifyPlayback()
		return cmd

	case "left":
		if m.focus == focusEQ {
			if m.eqCursor > 0 {
				m.eqCursor--
			}
		} else {
			return m.doSeek(-5 * time.Second)
		}

	case "shift+left":
		return m.doSeek(-m.seekStepLarge)

	case "right":
		if m.focus == focusEQ {
			if m.eqCursor < eqBandCount-1 {
				m.eqCursor++
			}
		} else {
			return m.doSeek(5 * time.Second)
		}

	case "shift+right":
		return m.doSeek(m.seekStepLarge)

	case "f":
		if m.focus == focusPlaylist && m.plCursor >= 0 && m.plCursor < m.playlist.Len() && m.loadedPlaylist != "" {
			if bs, ok := m.localProvider.(provider.BookmarkSetter); ok {
				m.playlist.ToggleBookmark(m.plCursor)
				if err := bs.SetBookmark(m.loadedPlaylist, m.plCursor); err != nil {
					m.status.Showf(statusTTLDefault, "Save failed: %s", err)
				}
				t := m.playlist.Tracks()[m.plCursor]
				if t.Bookmark {
					m.status.Showf(statusTTLDefault, "★ %s", t.DisplayName())
				} else {
					m.status.Showf(statusTTLDefault, "☆ %s", t.DisplayName())
				}
			}
		}

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
			m.eqCustomLabel = ""
			m.saveEQ()
		} else {
			if m.plCursor > 0 {
				m.plCursor--
				m.adjustScroll()
			} else if m.playlist.Len() > 0 {
				m.plCursor = m.playlist.Len() - 1
				m.adjustScroll()
			}
		}

	case "down", "j":
		if m.focus == focusEQ {
			bands := m.player.EQBands()
			m.player.SetEQBand(m.eqCursor, bands[m.eqCursor]-1)
			m.eqPresetIdx = -1 // manual tweak → custom
			m.eqCustomLabel = ""
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
			visible := max(1, m.effectivePlaylistVisible())
			m.plCursor -= min(m.plCursor, visible)
			m.adjustScroll()
		}

	case "pgdown", "ctrl+d":
		if m.focus == focusPlaylist && m.plCursor < m.playlist.Len()-1 {
			visible := max(1, m.effectivePlaylistVisible())
			m.plCursor = min(m.playlist.Len()-1, m.plCursor+visible)
			m.adjustScroll()
		}

	case "g", "home":
		if m.focus == focusPlaylist && m.plCursor != 0 {
			m.plCursor = 0
			m.adjustScroll()
		}

	case "G", "end":
		if m.focus == focusPlaylist && m.playlist.Len() > 0 && m.plCursor != m.playlist.Len()-1 {
			m.plCursor = m.playlist.Len() - 1
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
			m.notifyPlayback()
			return cmd
		}

	case "+", "=":
		m.player.SetVolume(m.player.Volume() + 1)
		m.notifyPlayback()

	case "-":
		m.player.SetVolume(m.player.Volume() - 1)
		m.notifyPlayback()

	case "r":
		m.playlist.CycleRepeat()
		if err := m.configSaver.Save("repeat", fmt.Sprintf("%q", m.playlist.Repeat().String())); err != nil {
			m.status.Showf(statusTTLDefault, "Config save failed: %s", err)
		}
		m.player.ClearPreload()
		return m.preloadNext()

	case "z":
		m.playlist.ToggleShuffle()
		if err := m.configSaver.Save("shuffle", fmt.Sprintf("%v", m.playlist.Shuffled())); err != nil {
			m.status.Showf(statusTTLDefault, "Config save failed: %s", err)
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
				m.focus = focusSpeed
			}
		case focusProvPill:
			m.focus = focusSpeed
		case focusSpeed:
			m.focus = focusPlaylist
		default:
			m.focus = focusPlaylist
		}

	case "h":
		if m.focus == focusEQ && m.eqCursor > 0 {
			m.eqCursor--
		}

	case "l":
		if m.focus == focusEQ && m.eqCursor < eqBandCount-1 {
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

	case "ctrl+s":
		return m.saveTrack()
	case "S":
		return m.switchToProvider("spotify")

	case "m":
		m.player.ToggleMono()

	case "/":
		m.search.active = true
		m.search.query = ""
		m.search.results = nil
		m.search.cursor = 0
		m.prevFocus = m.focus
		m.focus = focusSearch

	case "ctrl+f":
		// Context-aware search: active provider's native search if it supports
		// Searcher (e.g. Spotify); otherwise YouTube net search.
		if _, ok := m.provider.(provider.Searcher); ok {
			m.spotSearch = spotSearchState{
				prov:    m.provider,
				visible: true,
				screen:  spotSearchInput,
			}
		} else {
			m.netSearch.active = true
			m.netSearch.query = ""
			m.netSearch.soundcloud = false
			m.prevFocus = m.focus
			m.focus = focusNetSearch
		}

	case "ctrl+j":
		m.openJumpMode()
	case "J":
		return m.switchToProvider("jellyfin")
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
		if prov := m.findBrowseProvider(); prov != nil {
			m.openNavBrowserWith(prov)
		}

	case "L":
		return m.switchToProvider("local")
	case "R":
		return m.switchToProvider("radio")
	case "P":
		return m.switchToProvider("plex")
	case "Y":
		return m.switchToProvider("yt")

	case "v":
		m.vis.CycleMode()
		m.applyHeightMode()
		m.adjustScroll()
		if err := m.configSaver.Save("visualizer", fmt.Sprintf("%q", m.vis.ModeName())); err != nil {
			m.status.Showf(statusTTLDefault, "Config save failed: %s", err)
		}

	case "V":
		m.fullVis = !m.fullVis
		if m.fullVis {
			m.vis.Rows = max(ui.DefaultVisRows, (m.height-10)*4/5)
			ui.PanelWidth = max(0, m.width-2*ui.PaddingH)
		} else {
			m.vis.Rows = ui.DefaultVisRows
			m.restorePanelWidth()
		}

	case "ctrl+x":
		if m.focus == focusPlaylist {
			m.toggleExpandPlaylist()
		}

	case "d":
		m.devicePicker.visible = true
		m.devicePicker.cursor = 0
		if len(m.devicePicker.devices) == 0 {
			m.devicePicker.loading = true
			return listDevicesCmd()
		}

	case "]":
		m.changeSpeed(0.25)

	case "[":
		m.changeSpeed(-0.25)

	case "ctrl+k":
		m.openKeymap()
	}

	return nil
}

// saveTrack copies the current track to ~/Music/cliamp/ with a clean filename.
// For yt-dlp tracks (piped streams), triggers an async download via yt-dlp.
// For local temp files, copies synchronously.
func (m *Model) saveTrack() tea.Cmd {
	track, idx := m.playlist.Current()
	if idx < 0 {
		m.status.Show("Nothing to save", statusTTLShort)
		return nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		m.status.Showf(statusTTLShort, "Save failed: %s", err)
		return nil
	}

	saveDir := filepath.Join(home, "Music", "cliamp")
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		m.status.Showf(statusTTLShort, "Save failed: %s", err)
		return nil
	}

	// YouTube/yt-dlp tracks: async download directly to ~/Music/cliamp/.
	if playlist.IsYouTubeURL(track.Path) || playlist.IsYTDL(track.Path) {
		m.status.Clear()
		m.save.startDownload()
		return saveYTDLCmd(track.Path, saveDir)
	}

	// Only save local temp files (yt-dlp downloads), not streams or user's own files.
	if track.Stream || !strings.HasPrefix(track.Path, os.TempDir()) {
		m.status.Show("Only downloaded tracks can be saved", statusTTLShort)
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
		m.status.Showf(statusTTLShort, "Save failed: %s", err)
		return nil
	}

	m.status.Showf(statusTTLDefault, "Saved to ~/Music/cliamp/%s", name+ext)
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
func (m *Model) handleJumpKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.closeJumpMode()
		return m.quit()
	}

	switch msg.Code {
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
		m.notifyPlayback()
		if m.notifier != nil {
			m.notifier.Seeked(m.player.Position())
		}
		m.closeJumpMode()
		return nil
	case tea.KeyBackspace:
		m.jumpInput = removeLastRune(m.jumpInput)
		return nil
	}

	if len(msg.Text) > 0 {
		m.jumpInput += msg.Text
	}
	return nil
}

// handleProvSearchKey processes key presses while filtering the provider playlist list.
// For the radio provider, Enter fires an API search; for others, Enter loads the
// selected result. Esc cancels and restores the normal catalog view.
func (m *Model) handleProvSearchKey(msg tea.KeyPressMsg) tea.Cmd {
	// Catalog search: API-based search (no live client-side filtering).
	if cs, ok := m.provider.(provider.CatalogSearcher); ok {
		return m.handleCatalogSearchKey(msg, cs)
	}
	switch msg.Code {
	case tea.KeyEscape:
		m.provSearch.active = false
	case tea.KeyEnter:
		if len(m.provSearch.results) > 0 && !m.provLoading {
			idx := m.provSearch.results[m.provSearch.cursor]
			m.provCursor = idx
			m.providerMaybeAdjustScroll()
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
		if len(msg.Text) > 0 {
			m.provSearch.query += msg.Text
			m.updateProvSearch()
		}
	}
	return nil
}

// handleCatalogSearchKey handles search input for providers with catalog search.
// Types a query, Enter fires API search, Esc cancels/clears.
func (m *Model) handleCatalogSearchKey(msg tea.KeyPressMsg, cs provider.CatalogSearcher) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.provSearch.active = false
		m.restoreCatalog(cs)
	case tea.KeyEnter:
		m.provSearch.active = false
		if m.provSearch.query == "" {
			m.restoreCatalog(cs)
			return nil
		}
		m.provLoading = true
		return fetchCatalogSearchCmd(cs, m.provSearch.query)
	case tea.KeyBackspace, tea.KeyDelete:
		if m.provSearch.query != "" {
			m.provSearch.query = removeLastRune(m.provSearch.query)
		}
	case tea.KeySpace:
		m.provSearch.query += " "
	default:
		if len(msg.Text) > 0 {
			m.provSearch.query += msg.Text
		}
	}
	return nil
}

// restoreCatalog clears search results and restores the normal catalog view.
func (m *Model) restoreCatalog(cs provider.CatalogSearcher) {
	if !cs.IsSearching() {
		return
	}
	cs.ClearSearch()
	if lists, err := m.provider.Playlists(); err == nil {
		m.providerLists = lists
	}
	m.provCursor = 0
	m.provScroll = 0
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

// toggleExpandPlaylist toggles the playlist panel between default and expanded height.
func (m *Model) toggleExpandPlaylist() {
	m.heightExpanded = !m.heightExpanded
	m.applyHeightMode()
	m.adjustScroll()
}

// handlePaste routes pasted text to the active text input field.
// The priority order mirrors handleKey so the correct input receives the content.
func (m *Model) handlePaste(content string) tea.Cmd {
	if content == "" {
		return nil
	}

	// Keymap overlay search
	if m.keymap.visible {
		m.keymap.search += content
		m.updateKeymapFilter()
		return nil
	}

	// Nav browser search
	if m.navBrowser.visible && m.navBrowser.mode != navBrowseModeMenu && m.navBrowser.searching {
		m.navBrowser.search += content
		m.navBrowser.cursor = 0
		m.navBrowser.scroll = 0
		m.navUpdateSearch()
		return nil
	}

	// Playlist manager new-name input
	if m.plManager.visible && m.plManager.screen == plMgrScreenNewName {
		m.plManager.newName += content
		return nil
	}

	if m.jumping {
		m.jumpInput += content
		return nil
	}

	if m.urlInputting {
		m.urlInput += content
		return nil
	}

	if m.search.active {
		m.search.query += content
		m.updateSearch()
		return nil
	}

	if m.netSearch.active {
		m.netSearch.query += content
		return nil
	}

	if m.spotSearch.visible {
		switch m.spotSearch.screen {
		case spotSearchInput:
			m.spotSearch.query += content
		case spotSearchNewName:
			m.spotSearch.newName += content
		}
		return nil
	}

	if m.provSearch.active {
		m.provSearch.query += content
		if _, ok := m.provider.(provider.CatalogSearcher); !ok {
			m.updateProvSearch()
		}
		return nil
	}

	return nil
}

func (m *Model) handleSearchKey(msg tea.KeyPressMsg) tea.Cmd {
	// Allow opening overlays during search (ctrl combos don't conflict with text input).
	switch msg.String() {
	case "ctrl+k":
		m.openKeymap()
		return nil
	}

	switch msg.Code {
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
			m.notifyPlayback()
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
		if len(msg.Text) > 0 {
			m.search.query += msg.Text
			m.updateSearch()
		}
	}

	return nil
}

// handleNetSearchKey processes key presses while in net search mode.
func (m *Model) handleNetSearchKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+k":
		m.openKeymap()
		return nil
	}

	switch msg.Code {
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
			m.status.Show("Queuing search...", statusTTLShort)
			cmd = fetchNetSearchCmd(prefix + strings.TrimSpace(m.netSearch.query))
		}
		return cmd

	case tea.KeyBackspace:
		m.netSearch.query = removeLastRune(m.netSearch.query)

	case tea.KeySpace:
		m.netSearch.query += " "

	default:
		if len(msg.Text) > 0 {
			m.netSearch.query += msg.Text
		}
	}

	return nil
}

// handleURLInputKey processes key presses while in URL input mode.
func (m *Model) handleURLInputKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.urlInputting = false
	case tea.KeyEnter:
		m.urlInputting = false
		input := strings.TrimSpace(m.urlInput)
		if input != "" {
			m.feedLoading = true
			m.status.Show("Loading URL...", statusTTLLong)
			return resolveRemoteCmd([]string{input}, true)
		}
	case tea.KeyBackspace:
		m.urlInput = removeLastRune(m.urlInput)
	default:
		if len(msg.Text) > 0 {
			m.urlInput += msg.Text
		}
	}
	return nil
}

// handlePlaylistManagerKey dispatches keys to the active manager screen.
func (m *Model) handlePlaylistManagerKey(msg tea.KeyPressMsg) tea.Cmd {
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
func (m *Model) handlePlMgrListKey(msg tea.KeyPressMsg) tea.Cmd {
	// If waiting for delete confirmation, only accept y/n.
	if m.plManager.confirmDel {
		switch msg.String() {
		case "y", "Y":
			if m.plManager.cursor < len(m.plManager.playlists) {
				name := m.plManager.playlists[m.plManager.cursor].Name
				if d, ok := m.localProvider.(provider.PlaylistDeleter); ok {
					if err := d.DeletePlaylist(name); err != nil {
						m.status.Showf(statusTTLDefault, "Delete failed: %s", err)
					} else {
						m.status.Showf(statusTTLDefault, "Deleted %q", name)
					}
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
			m.plManager.cursor = count - 1
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
func (m *Model) handlePlMgrTracksKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.plManager.visible = false
		return m.quit()
	case "up", "k":
		if m.plManager.cursor > 0 {
			m.plManager.cursor--
		} else if len(m.plManager.tracks) > 0 {
			m.plManager.cursor = len(m.plManager.tracks) - 1
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
			m.loadedPlaylist = m.plManager.selPlaylist
			m.plCursor = 0
			m.playlist.SetIndex(0)
			m.adjustScroll()
			m.plManager.visible = false
			m.focus = focusPlaylist
			cmd := m.playCurrentTrack()
			m.notifyPlayback()
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
			err := m.localDeleter().RemoveTrack(m.plManager.selPlaylist, m.plManager.cursor)
			if err != nil {
				m.status.Showf(statusTTLDefault, "Remove failed: %s", err)
			} else {
				m.status.Show("Track removed", statusTTLDefault)
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
func (m *Model) handlePlMgrNewNameKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
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
		if len(msg.Text) > 0 {
			m.plManager.newName += msg.Text
		}
	}
	return nil
}

// localDeleter returns the PlaylistDeleter from the local provider.
func (m *Model) localDeleter() provider.PlaylistDeleter {
	d, _ := m.localProvider.(provider.PlaylistDeleter)
	return d
}

// addToPlaylist appends the current track to a local playlist and shows a status message.
func (m *Model) addToPlaylist(name string) {
	track, idx := m.playlist.Current()
	if idx < 0 {
		m.status.Show("No track to add", statusTTLShort)
		return
	}
	if w, ok := m.localProvider.(provider.PlaylistWriter); ok {
		if err := w.AddTrackToPlaylist(context.Background(), name, track); err != nil {
			m.status.Showf(statusTTLDefault, "Failed: %s", err)
		} else {
			m.status.Showf(statusTTLDefault, "Added to %q", name)
		}
	}
}

// handleThemeKey processes key presses while the theme picker is open.
func (m *Model) handleThemeKey(msg tea.KeyPressMsg) tea.Cmd {
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
			m.themePicker.cursor = count - 1
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
func (m *Model) handleQueueKey(msg tea.KeyPressMsg) tea.Cmd {
	qLen := m.playlist.QueueLen()

	switch msg.String() {
	case "ctrl+c":
		m.queue.visible = false
		return m.quit()
	case "ctrl+k":
		m.openKeymap()
	case "up", "k":
		if m.queue.cursor > 0 {
			m.queue.cursor--
		} else if qLen > 0 {
			m.queue.cursor = qLen - 1
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

// handleDeviceKey processes key presses while the audio device picker is open.
func (m *Model) handleDeviceKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.devicePicker.visible = false
		return m.quit()
	case "up", "k":
		if m.devicePicker.cursor > 0 {
			m.devicePicker.cursor--
		} else if len(m.devicePicker.devices) > 0 {
			m.devicePicker.cursor = len(m.devicePicker.devices) - 1
		}
	case "down", "j":
		if m.devicePicker.cursor < len(m.devicePicker.devices)-1 {
			m.devicePicker.cursor++
		} else {
			m.devicePicker.cursor = 0
		}
	case "enter":
		if len(m.devicePicker.devices) > 0 && m.devicePicker.cursor < len(m.devicePicker.devices) {
			dev := m.devicePicker.devices[m.devicePicker.cursor]
			m.devicePicker.visible = false
			return switchDeviceCmd(dev.Name)
		}
	case "esc", "d":
		m.devicePicker.visible = false
	}
	return nil
}
