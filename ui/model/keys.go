package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/internal/fileutil"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// quit shuts down the player and signals the TUI to exit.
func (m *Model) quit() tea.Cmd {
	// Only save resume for seekable tracks:
	// - local files (not stream)
	// - HTTP streams with known duration (podcast MP3s, seek-by-reconnect)
	// Exclude YTDL (position unreliable) and real-time live streams.
	if track, _ := m.currentPlaybackTrack(); track.Path != "" &&
		!playlist.IsYTDL(track.Path) && !track.IsLive() &&
		m.player.IsPlaying() {
		if secs := int(m.player.Position().Seconds()); secs > 0 {
			m.exitResume.path = track.Path
			m.exitResume.secs = secs
			m.exitResume.playlist = m.loadedPlaylist
		}
	}

	m.flushPendingSpeedSave()
	m.flushPendingEQSave()
	m.player.Close()
	m.clearPlaybackTrack()
	m.quitting = true
	return tea.Quit
}

// scrobbleCurrent fires a scrobble for the currently playing track if applicable.
func (m *Model) scrobbleCurrent() {
	if track, idx := m.currentPlaybackTrack(); idx >= 0 {
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
		m.focus = m.nextMainFocus(focusSpeed)
	case "esc", "backspace":
		m.focus = m.previousMainFocus(focusSpeed)
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

	if m.provScroll >= total {
		m.provScroll = max(0, total-1)
	}

	// Provider lists can add radio-prefix or PlaylistInfo.Section headers. Keep
	// the logical cursor visible in their rendered-row viewport.
	for m.provScroll < total && m.providerRowsFromScroll(m.provScroll, m.provCursor) > visible {
		m.provScroll++
	}
}

func (m *Model) providerRowsFromScroll(scroll, cursor int) int {
	total := len(m.providerLists)
	if total == 0 || cursor < scroll || scroll < 0 || cursor >= total {
		return 0
	}

	rows := 0
	sl, isRadio := m.provider.(provider.SectionedList)
	prevHeader := ""
	if scroll > 0 {
		if isRadio {
			prevHeader = sl.IDPrefix(m.providerLists[scroll-1].ID)
		} else {
			prevHeader = m.providerLists[scroll-1].Section
		}
	}

	for i := scroll; i <= cursor && i < total; i++ {
		header := m.providerLists[i].Section
		if isRadio {
			header = sl.IDPrefix(m.providerLists[i].ID)
		}
		if header != "" && header != prevHeader {
			rows++ // section header row
		}
		rows++ // item row
		prevHeader = header
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
	if msg.String() == "ctrl+c" {
		return m.quit()
	}
	if msg.String() == "ctrl+z" {
		m.undoPlaylistMutation()
		return nil
	}
	if msg.String() == "ctrl+k" && !m.keymap.visible {
		if m.fullVis {
			m.exitFullVisualizer()
		}
		m.openKeymap()
		return nil
	}
	if m.width > 0 && m.layout.tooSmall() {
		if msg.String() == "q" {
			return m.quit()
		}
		return nil
	}
	if m.fullVis {
		return m.handleFullVisualizerKey(msg)
	}
	if m.keymap.visible {
		return m.handleKeymapKey(msg)
	}

	// Audio device picker overlay
	if m.devicePicker.visible {
		return m.handleDeviceKey(msg)
	}

	// Provider search overlay sits on top of the nav browser, so it must
	// claim keys first when both are visible.
	if m.plPicker.visible {
		return m.handlePlaylistPickerKey(msg)
	}

	// File browser overlay can be opened from inside the playlist manager, so it
	// must take precedence when both states are visible.
	if m.fileBrowser.visible {
		return m.handleFileBrowserKey(msg)
	}

	// Provider search overlay sits on top of the nav browser, so it must
	// claim keys first when both are visible.
	if m.spotSearch.visible {
		return m.handleSpotSearchKey(msg)
	}

	// Navidrome explore browser overlay
	if m.navBrowser.visible {
		return m.handleNavBrowserKey(msg)
	}

	// Theme picker overlay — interactive navigation
	if m.themePicker.visible {
		return m.handleThemeKey(msg)
	}

	// Visualizer picker overlay — interactive navigation
	if m.visPicker.visible {
		return m.handleVisPickerKey(msg)
	}

	// Playlist manager overlay (browse, add, remove, delete)
	if m.plManager.visible {
		return m.handlePlaylistManagerKey(msg)
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
		case "up", "k":
			if m.infoScroll > 0 {
				m.infoScroll--
			}
		case "down", "j":
			m.infoScroll++
			m.infoMaybeAdjustScroll()
		}
		return nil
	}

	// Lyrics overlay
	if m.lyrics.visible {
		switch msg.String() {
		case "ctrl+c":
			return m.quit()
		case "esc", "y":
			nextRequest(&m.requests.lyrics)
			m.lyrics.loading = false
			m.lyrics.query = ""
			m.lyrics.visible = false
		case "r":
			return m.retryLyrics()
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
		case "ctrl+x":
			m.toggleExpandedView()
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
					return authenticateProviderCmd(auth, m.provider.Name(), nextRequest(&m.requests.auth))
				}
			}
			if len(m.providerLists) > 0 && !m.provLoading {
				m.provLoading = true
				m.activeProviderPlaylistID = m.providerLists[m.provCursor].ID
				return m.fetchProviderTracks(m.providerLists[m.provCursor].ID)
			}
		case "tab":
			m.focus = m.nextMainFocus(focusPlaylist)
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
			m.provSearch.scroll = 0
		case "ctrl+r":
			if m.provider != nil && !m.provLoading {
				if r, ok := m.provider.(playlist.Refresher); ok {
					r.Refresh()
				}
				m.provLoading = true
				m.activeProviderPlaylistID = ""
				m.status.Activityf(statusTTLShort, "Refreshing %s…", m.provider.Name())
				return m.fetchProviderPlaylists()
			}
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
		case "E":
			return m.switchToProvider("emby")
		case "B":
			return m.switchToProvider("audiobookshelf")
		case "S":
			return m.switchToProvider("spotify")
		case "P":
			return m.switchToProvider("plex")
		case "Y":
			return m.switchToProvider("yt")
		case "C":
			return m.switchToProvider("soundcloud")
		case "M":
			return m.switchToProvider("netease")
		case "Q":
			return m.switchToProvider("qobuz")
		case "L":
			return m.switchToProvider("local")
		case "R":
			return m.switchToProvider("radio")
		case "ctrl+x":
			m.toggleExpandedView()
		case "ctrl+f":
			m.openProviderSearch()
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
			m.focus = m.nextMainFocus(focusProvPill)
		case "esc", "backspace":
			m.focus = m.previousMainFocus(focusProvPill)
		case "space":
			return m.togglePlayPause()
		}
		return nil
	}

	// Vim-style count prefix: a digit primes a pending percentage; the next `j`
	// jumps there (e.g. `7j` → 70%). Any other key cancels and runs normally.
	if s := msg.String(); m.focus == focusPlaylist && len(s) == 1 && s[0] >= '0' && s[0] <= '9' {
		m.pendingSeekActive = true
		m.pendingSeekPct = int(s[0] - '0')
		m.pendingSeekExpiresAt = time.Now().Add(time.Duration(statusTTLMedium))
		m.status.Activityf(statusTTLMedium, "%dj -> seek to %d%%", m.pendingSeekPct, m.pendingSeekPct*10)
		return nil
	}
	if m.pendingSeekActive {
		pct := m.pendingSeekPct
		m.pendingSeekActive = false
		m.pendingSeekExpiresAt = time.Time{}
		m.status.Clear()
		if msg.String() == "j" && m.focus == focusPlaylist {
			if dur := m.player.Duration(); dur > 0 {
				return m.seekAbsolute(dur * time.Duration(pct) / 10)
			}
			return nil
		}
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m.quit()
	case "esc", "backspace", "b":
		if m.focus == focusPlaylist {
			// Keep current expanded/collapsed height mode when switching focus.
			m.focus = focusProvider
		}

	case "space":
		cmd := m.togglePlayPause()
		m.notifyPlayback()
		return cmd

	case "s":
		m.player.Stop()
		m.clearPlaybackTrack()
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
				tracks := m.playlist.Tracks()
				track := tracks[m.plCursor]
				if err := bs.SetBookmarkByPath(m.loadedPlaylist, track.Path); err != nil {
					m.status.Errorf(statusTTLDefault, "Save failed: %s", err)
					return nil
				}
				m.playlist.ToggleBookmark(m.plCursor)
				track = m.playlist.Tracks()[m.plCursor]
				if track.Bookmark {
					m.status.Showf(statusTTLDefault, "★ %s", track.DisplayName())
				} else {
					m.status.Showf(statusTTLDefault, "☆ %s", track.DisplayName())
				}
			}
		}

	case "shift+up":
		if m.focus == focusPlaylist && m.plCursor > 0 {
			if m.playlist.Move(m.plCursor, m.plCursor-1) {
				m.plCursor--
				m.persistLoadedPlaylistOrder()
				m.adjustScroll()
			}
		}

	case "shift+down":
		if m.focus == focusPlaylist && m.plCursor < m.playlist.Len()-1 {
			if m.playlist.Move(m.plCursor, m.plCursor+1) {
				m.plCursor++
				m.persistLoadedPlaylistOrder()
				m.adjustScroll()
			}
		}

	case "up", "k":
		if m.focus == focusEQ {
			bands := m.player.EQBands()
			m.setCustomEQBand(m.eqCursor, bands[m.eqCursor]+1)
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
			m.setCustomEQBand(m.eqCursor, bands[m.eqCursor]-1)
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
			m.status.Errorf(statusTTLDefault, "Config save failed: %s", err)
		}
		m.player.ClearPreload()
		return m.preloadNext()

	case "z":
		m.playlist.ToggleShuffle()
		if err := m.configSaver.Save("shuffle", fmt.Sprintf("%v", m.playlist.Shuffled())); err != nil {
			m.status.Errorf(statusTTLDefault, "Config save failed: %s", err)
		}
		m.player.ClearPreload()
		return m.preloadNext()

	case "tab":
		m.focus = m.nextMainFocus(m.focus)

	case "h":
		if m.focus == focusEQ && m.eqCursor > 0 {
			m.eqCursor--
		}

	case "l":
		if m.focus == focusEQ && m.eqCursor < eqBandCount-1 {
			m.eqCursor++
		}

	case "e":
		if m.layout.tier == layoutMinimal {
			break
		}
		m.cycleEQPreset()
		m.scheduleEQSave()

	case "a":
		if m.focus == focusPlaylist {
			if !m.playlist.Dequeue(m.plCursor) {
				m.playlist.Queue(m.plCursor)
			}
		}

	case "w":
		if m.focus == focusPlaylist && m.plCursor >= 0 && m.plCursor < m.playlist.Len() {
			track := m.playlist.Tracks()[m.plCursor]
			m.openPlaylistPicker([]playlist.Track{track}, "Track: "+track.DisplayName())
		}

	case "A":
		if m.focus == focusPlaylist {
			m.queue.visible = true
			m.queue.cursor = 0
			m.queue.scroll = 0
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
		m.search.scroll = 0
		m.prevFocus = m.focus
		m.focus = focusSearch
		// Search now renders in the playlist region; recompute chrome so the
		// search header/help are reflected in the visible-row budget.
		m.refreshChrome()
		m.applyHeightMode()

	case "ctrl+f":
		m.openProviderSearch()

	case "ctrl+j":
		m.openJumpMode()
	case "J":
		return m.switchToProvider("jellyfin")
	case "E":
		return m.switchToProvider("emby")
	case "B":
		return m.switchToProvider("audiobookshelf")
	case "p":
		if m.localProvider != nil {
			m.openPlaylistManager()
		}

	case "t":
		m.openThemePicker()

	case "i":
		m.showInfo = true
		m.infoScroll = 0

	case "y":
		m.lyrics.visible = !m.lyrics.visible
		if m.lyrics.visible {
			return m.retryLyrics()
		}

	case "o":
		m.openFileBrowser()

	case "u":
		m.urlInputting = true
		m.urlInput = ""
		m.urlErr = ""

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
	case "C":
		return m.switchToProvider("soundcloud")
	case "M":
		return m.switchToProvider("netease")
	case "Q":
		return m.switchToProvider("qobuz")

	case "ctrl+h":
		m.toggleAlbumHeadersManual()
		m.adjustScroll()

	case "v":
		m.vis.CycleMode()
		m.vis.RequestRefresh()
		m.refreshChrome()
		m.applyHeightMode()
		m.adjustScroll()
		if err := m.configSaver.Save("visualizer", fmt.Sprintf("%q", m.vis.ModeName())); err != nil {
			m.status.Errorf(statusTTLDefault, "Config save failed: %s", err)
		}

	case "ctrl+v":
		m.openVisPicker()

	case "V":
		m.fullVis = !m.fullVis
		m.recomputeLayout()

	case "ctrl+x":
		if m.focus == focusPlaylist {
			m.toggleExpandedView()
		}

	case "x":
		if m.focus == focusPlaylist {
			m.removeSelectedFromPlaylist()
		}

	case "d":
		m.devicePicker.visible = true
		m.devicePicker.cursor = 0
		m.devicePicker.scroll = 0
		if len(m.devicePicker.devices) == 0 {
			m.devicePicker.loading = true
			return listDevicesCmd()
		}

	case "]":
		m.changeSpeed(0.25)

	case "[":
		m.changeSpeed(-0.25)

	case "?":
		m.openKeymap()

	default:
		if m.luaMgr != nil {
			m.luaMgr.EmitKey(msg.String())
		}
	}

	return nil
}

func (m *Model) exitFullVisualizer() {
	m.fullVis = false
	m.recomputeLayout()
}

func (m *Model) handleFullVisualizerKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "q":
		return m.quit()
	case "esc", "backspace", "b", "V":
		m.exitFullVisualizer()
	case "space":
		cmd := m.togglePlayPause()
		m.notifyPlayback()
		return cmd
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
		return m.doSeek(-5 * time.Second)
	case "shift+left":
		return m.doSeek(-m.seekStepLarge)
	case "right":
		return m.doSeek(5 * time.Second)
	case "shift+right":
		return m.doSeek(m.seekStepLarge)
	case "+", "=":
		m.player.SetVolume(m.player.Volume() + 1)
		m.notifyPlayback()
	case "-":
		m.player.SetVolume(m.player.Volume() - 1)
		m.notifyPlayback()
	case "v":
		m.vis.CycleMode()
		m.vis.RequestRefresh()
		m.refreshChrome()
	case "ctrl+k", "?":
		m.exitFullVisualizer()
		m.openKeymap()
	}
	return nil
}

// saveTrack copies the current track to ~/Music/cliamp/ with a clean filename.
// For yt-dlp tracks (piped streams), triggers an async download via yt-dlp.
// For local temp files, copies synchronously.
func (m *Model) saveTrack() tea.Cmd {
	track, idx := m.currentPlaybackTrack()
	if idx < 0 {
		m.status.Warning("Nothing to save", statusTTLShort)
		return nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		m.status.Errorf(statusTTLShort, "Save failed: %s", err)
		return nil
	}

	saveDir := filepath.Join(home, "Music", "cliamp")
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		m.status.Errorf(statusTTLShort, "Save failed: %s", err)
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
		m.status.Warning("Only downloaded tracks can be saved", statusTTLShort)
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
		m.status.Errorf(statusTTLShort, "Save failed: %s", err)
		return nil
	}

	m.status.Showf(statusTTLDefault, "Saved to ~/Music/cliamp/%s", name+ext)
	return nil
}

func (m *Model) resetJumpInput() {
	m.jumpInput = ""
	m.jumpErr = ""
}

func (m *Model) openJumpMode() {
	m.jumping = true
	m.resetJumpInput()
}

// openProviderSearch opens the active provider's native search overlay if it
// implements provider.Searcher; otherwise it falls back to the YouTube net
// search overlay.
func (m *Model) openProviderSearch() {
	m.openProviderSearchWith(m.provider)
}

// openProviderSearchWith opens a search overlay against the given provider.
// Falls back to YouTube net search when prov doesn't implement Searcher.
func (m *Model) openProviderSearchWith(prov playlist.Provider) {
	if _, ok := prov.(provider.Searcher); ok {
		m.cancelSpotRequest()
		nextRequest(&m.requests.spotSearch)
		nextRequest(&m.requests.spotLists)
		nextRequest(&m.requests.spotMutation)
		m.spotSearch = spotSearchState{
			prov:    prov,
			visible: true,
			screen:  spotSearchInput,
		}
		return
	}
	nextRequest(&m.requests.netSearch)
	m.netSearch = netSearchState{
		active: true,
		screen: netSearchInput,
	}
	m.prevFocus = m.focus
	m.focus = focusNetSearch
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
			m.jumpErr = "Invalid jump: " + err.Error()
			m.status.Warning(m.jumpErr, statusTTLDefault)
			return nil
		}
		if !m.player.Seekable() {
			m.jumpErr = "This track cannot be seeked."
			m.status.Warning(m.jumpErr, statusTTLDefault)
			return nil
		}
		if dur := m.player.Duration(); dur > 0 && target > dur {
			m.jumpErr = fmt.Sprintf("Jump exceeds track duration (%s).", formatJumpClock(dur))
			m.status.Warning(m.jumpErr, statusTTLDefault)
			return nil
		}
		if err := m.player.Seek(target - m.player.Position()); err != nil {
			m.jumpErr = "Seek failed: " + err.Error()
			m.status.Warning(m.jumpErr, statusTTLDefault)
			return nil
		}
		// finishSeek notifies plugins as well as MPRIS, matching every other
		// completed seek; the previous manual block skipped Lua plugins.
		m.finishSeek()
		m.closeJumpMode()
		return nil
	}

	if m.editText("jump", &m.jumpInput, msg) {
		m.jumpErr = ""
	}
	return nil
}

func (m *Model) provSearchMaybeAdjustScroll() {
	visible := m.providerScrollStep() - 1 // -1 for query line
	if visible < 1 {
		visible = 1
	}
	count := len(m.provSearch.results)
	clampScroll(&m.provSearch.cursor, &m.provSearch.scroll, count, visible)
}

// handleProvSearchKey processes key presses while filtering the provider playlist list.
// For the radio provider, Enter fires an API search; for others, Enter loads the
// selected result. Esc cancels and restores the normal catalog view.
func (m *Model) handleProvSearchKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+x":
		m.toggleExpandedView()
		m.provSearchMaybeAdjustScroll()
		return nil
	case "ctrl+n":
		if m.provSearch.cursor < len(m.provSearch.results)-1 {
			m.provSearch.cursor++
		} else if len(m.provSearch.results) > 0 {
			m.provSearch.cursor = 0
		}
		m.provSearchMaybeAdjustScroll()
		return nil
	case "ctrl+p":
		if m.provSearch.cursor > 0 {
			m.provSearch.cursor--
		} else if len(m.provSearch.results) > 0 {
			m.provSearch.cursor = len(m.provSearch.results) - 1
		}
		m.provSearchMaybeAdjustScroll()
		return nil
	case "ctrl+u":
		m.editText("provider-search", &m.provSearch.query, msg)
		m.updateProvSearch()
		return nil
	case "ctrl+d":
		step := m.providerScrollStep() - 1
		if step < 1 {
			step = 1
		}
		m.provSearch.cursor += step
		if m.provSearch.cursor >= len(m.provSearch.results) {
			m.provSearch.cursor = max(0, len(m.provSearch.results)-1)
		}
		m.provSearchMaybeAdjustScroll()
		return nil
	}

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
			m.activeProviderPlaylistID = m.providerLists[idx].ID
			return m.fetchProviderTracks(m.providerLists[idx].ID)
		}
	case tea.KeyUp:
		if m.provSearch.cursor > 0 {
			m.provSearch.cursor--
		} else if len(m.provSearch.results) > 0 {
			m.provSearch.cursor = len(m.provSearch.results) - 1
		}
		m.provSearchMaybeAdjustScroll()

	case tea.KeyDown:
		if m.provSearch.cursor < len(m.provSearch.results)-1 {
			m.provSearch.cursor++
		} else if len(m.provSearch.results) > 0 {
			m.provSearch.cursor = 0
		}
		m.provSearchMaybeAdjustScroll()
	default:
		if msg.Code == tea.KeySpace && msg.Text == "" {
			m.insertText("provider-search", &m.provSearch.query, " ")
			m.updateProvSearch()
		} else if m.editText("provider-search", &m.provSearch.query, msg) {
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
		return fetchCatalogSearchCmd(cs, m.provider.Name(), m.provSearch.query, nextRequest(&m.requests.catalog))
	default:
		if msg.Code == tea.KeySpace && msg.Text == "" {
			m.insertText("provider-search", &m.provSearch.query, " ")
		} else {
			m.editText("provider-search", &m.provSearch.query, msg)
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
	m.provSearch.scroll = 0
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

// toggleExpandedView toggles the UI between default and expanded height.
func (m *Model) toggleExpandedView() {
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
		m.insertText("keymap", &m.keymap.search, content)
		m.updateKeymapFilter()
		return nil
	}

	if m.themePicker.visible && m.themePicker.filtering {
		m.insertText("theme-picker-filter", &m.themePicker.filter, content)
		m.themePickerRecomputeFilter()
		return nil
	}

	if m.visPicker.visible && m.visPicker.filtering {
		m.insertText("visualizer-picker-filter", &m.visPicker.filter, content)
		m.visPickerRecomputeFilter()
		return nil
	}

	// Provider search can sit above the navigation browser.
	if m.spotSearch.visible {
		switch m.spotSearch.screen {
		case spotSearchInput:
			m.insertText("spot-search", &m.spotSearch.query, content)
		case spotSearchNewName:
			m.insertText("spot-playlist-name", &m.spotSearch.newName, content)
		}
		return nil
	}

	if m.plPicker.visible && m.plPicker.screen == plPickerNewName {
		m.insertText("playlist-picker-name", &m.plPicker.newName, content)
		m.plPicker.inputErr = ""
		return nil
	}

	if m.fileBrowser.visible && m.fileBrowser.searching {
		m.insertText("file-browser-search", &m.fileBrowser.search, content)
		m.fbUpdateFilter()
		return nil
	}

	// Nav browser search
	if m.navBrowser.visible && m.navBrowser.mode != navBrowseModeMenu && m.navBrowser.searching {
		m.insertText("nav-search", &m.navBrowser.search, content)
		m.navBrowser.cursor = 0
		m.navBrowser.scroll = 0
		m.navUpdateSearch()
		return nil
	}

	// Playlist manager name input
	if m.plManager.visible && m.plManager.screen == plMgrScreenNewName {
		m.insertText("playlist-manager-new-name", &m.plManager.newName, content)
		m.plManager.inputErr = ""
		return nil
	}
	if m.plManager.visible && m.plManager.screen == plMgrScreenRename {
		m.insertText("playlist-manager-rename", &m.plManager.renameName, content)
		m.plManager.inputErr = ""
		return nil
	}

	// Playlist manager `/` filter
	if m.plManager.visible && m.plManager.filtering {
		m.insertText("playlist-manager-filter", &m.plManager.filter, content)
		m.plManager.cursor = 0
		m.plMgrRecomputeFilter()
		return nil
	}

	if m.jumping {
		m.insertText("jump", &m.jumpInput, content)
		m.jumpErr = ""
		return nil
	}

	if m.urlInputting {
		m.insertText("url", &m.urlInput, content)
		m.urlErr = ""
		return nil
	}

	if m.search.active {
		m.insertText("playlist-search", &m.search.query, content)
		m.updateSearch()
		return nil
	}

	if m.netSearch.active {
		if m.netSearch.screen == netSearchInput {
			m.insertText("net-search", &m.netSearch.query, content)
		}
		return nil
	}

	if m.provSearch.active {
		m.insertText("provider-search", &m.provSearch.query, content)
		if _, ok := m.provider.(provider.CatalogSearcher); !ok {
			m.updateProvSearch()
		}
		return nil
	}

	return nil
}

func (m *Model) searchMaybeAdjustScroll(visible int) {
	clampScroll(&m.search.cursor, &m.search.scroll, len(m.search.results), visible)
}

func (m *Model) handleSearchKey(msg tea.KeyPressMsg) tea.Cmd {
	// Allow opening overlays during search (ctrl combos don't conflict with text input).
	switch msg.String() {
	case "ctrl+k":
		m.openKeymap()
		return nil
	case "ctrl+x":
		m.toggleExpandedView()
		m.searchMaybeAdjustScroll(m.searchVisible())
		return nil
	case "ctrl+n":
		if m.search.cursor < len(m.search.results)-1 {
			m.search.cursor++
		} else if len(m.search.results) > 0 {
			m.search.cursor = 0
		}
		m.searchMaybeAdjustScroll(m.searchVisible())
		return nil
	case "ctrl+p":
		if m.search.cursor > 0 {
			m.search.cursor--
		} else if len(m.search.results) > 0 {
			m.search.cursor = len(m.search.results) - 1
		}
		m.searchMaybeAdjustScroll(m.searchVisible())
		return nil
	case "ctrl+u":
		m.editText("playlist-search", &m.search.query, msg)
		m.updateSearch()
		return nil
	case "ctrl+d":
		step := m.searchVisible()
		if step < 1 {
			step = 1
		}
		m.search.cursor += step
		if m.search.cursor >= len(m.search.results) {
			m.search.cursor = max(0, len(m.search.results)-1)
		}
		m.searchMaybeAdjustScroll(m.searchVisible())
		return nil
	}

	switch msg.Code {
	case tea.KeyEscape:
		m.search.active = false
		m.focus = m.prevFocus
		m.closeSearchLayout()

	case tea.KeyEnter:
		var cmd tea.Cmd
		if len(m.search.results) > 0 {
			idx := m.search.results[m.search.cursor]
			m.playlist.SetIndex(idx)
			m.plCursor = idx
			cmd = m.playCurrentTrack()
			m.notifyPlayback()
		}
		m.search.active = false
		m.focus = focusPlaylist
		m.closeSearchLayout()
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
		} else if len(m.search.results) > 0 {
			m.search.cursor = len(m.search.results) - 1
		}
		m.searchMaybeAdjustScroll(m.searchVisible())

	case tea.KeyDown:
		if m.search.cursor < len(m.search.results)-1 {
			m.search.cursor++
		} else if len(m.search.results) > 0 {
			m.search.cursor = 0
		}
		m.searchMaybeAdjustScroll(m.searchVisible())

	default:
		if m.editText("playlist-search", &m.search.query, msg) {
			m.updateSearch()
		}
	}

	return nil
}

// handleNetSearchKey dispatches key presses to the active net search screen.
func (m *Model) handleNetSearchKey(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "ctrl+k" {
		m.openKeymap()
		return nil
	}
	switch m.netSearch.screen {
	case netSearchInput:
		return m.handleNetSearchInputKey(msg)
	case netSearchResults:
		return m.handleNetSearchResultsKey(msg)
	}
	return nil
}

// handleNetSearchInputKey handles text entry on the net search overlay.
func (m *Model) handleNetSearchInputKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.closeNetSearch()

	case tea.KeyEnter:
		if strings.TrimSpace(m.netSearch.query) == "" {
			m.netSearch.err = "Enter a search query."
			return nil
		}
		if !m.netSearch.loading {
			prefix := "ytsearch10:"
			if m.netSearch.soundcloud {
				prefix = "scsearch10:"
			}
			m.netSearch.loading = true
			m.netSearch.err = ""
			query := prefix + strings.TrimSpace(m.netSearch.query)
			m.netSearch.request = query
			return fetchNetSearchCmd(query, nextRequest(&m.requests.netSearch))
		}

	default:
		if m.editText("net-search", &m.netSearch.query, msg) {
			m.netSearch.err = ""
		}
	}
	return nil
}

func (m *Model) netSearchResultsMaybeAdjustScroll(visible int) {
	clampScroll(&m.netSearch.cursor, &m.netSearch.scroll, len(m.netSearch.results), visible)
}

// handleNetSearchResultsKey handles navigation through net search results.
func (m *Model) handleNetSearchResultsKey(msg tea.KeyPressMsg) tea.Cmd {
	count := len(m.netSearch.results)

	switch msg.String() {
	case "ctrl+x":
		m.toggleExpandedView()
		m.netSearchResultsMaybeAdjustScroll(m.netSearchResultsVisible())
	case "up", "k", "ctrl+p":
		if m.netSearch.cursor > 0 {
			m.netSearch.cursor--
		} else if count > 0 {
			m.netSearch.cursor = count - 1
		}
		m.netSearchResultsMaybeAdjustScroll(m.netSearchResultsVisible())
	case "down", "j", "ctrl+n":
		if m.netSearch.cursor < count-1 {
			m.netSearch.cursor++
		} else if count > 0 {
			m.netSearch.cursor = 0
		}
		m.netSearchResultsMaybeAdjustScroll(m.netSearchResultsVisible())
	case "enter":
		if count > 0 && !m.netSearch.loading {
			track := m.netSearch.results[m.netSearch.cursor]
			m.closeNetSearch()
			return m.playTrackImmediate(track)
		}
	case "a":
		if count > 0 && !m.netSearch.loading {
			track := m.netSearch.results[m.netSearch.cursor]
			m.closeNetSearch()
			return m.appendTrack(track)
		}
	case "q":
		if count > 0 && !m.netSearch.loading {
			track := m.netSearch.results[m.netSearch.cursor]
			m.closeNetSearch()
			return m.queueTrackNext(track)
		}
	case "esc", "backspace":
		m.netSearch.screen = netSearchInput
		m.netSearch.results = nil
		m.netSearch.cursor = 0
		m.netSearch.scroll = 0
		m.netSearch.err = ""
	case "ctrl+u":
		step := m.netSearchResultsVisible()
		if step < 1 {
			step = 1
		}
		if m.netSearch.cursor >= step {
			m.netSearch.cursor -= step
		} else {
			m.netSearch.cursor = 0
		}
		m.netSearchResultsMaybeAdjustScroll(m.netSearchResultsVisible())
	case "ctrl+d":
		step := m.netSearchResultsVisible()
		if step < 1 {
			step = 1
		}
		m.netSearch.cursor += step
		if m.netSearch.cursor >= count {
			m.netSearch.cursor = max(0, count-1)
		}
		m.netSearchResultsMaybeAdjustScroll(m.netSearchResultsVisible())
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
			m.status.Activity("Loading URL...", statusTTLLong)
			return resolveRemoteCmd([]string{input}, true)
		}
		m.urlInputting = true
		m.urlErr = "Enter a stream, track, or playlist URL."
	default:
		if m.editText("url", &m.urlInput, msg) {
			m.urlErr = ""
		}
	}
	return nil
}

// handlePlaylistManagerKey dispatches keys to the active manager screen.
func (m *Model) handlePlaylistManagerKey(msg tea.KeyPressMsg) tea.Cmd {
	// Quick-switch (Shift+letter) jumps to another provider. Only honored when
	// the manager isn't currently capturing text input (filter, new-name).
	if m.plManager.screen != plMgrScreenNewName && m.plManager.screen != plMgrScreenRename && !m.plManager.filtering {
		if cmd := m.quickSwitchProvider(msg.String()); cmd != nil {
			return cmd
		}
	}
	switch m.plManager.screen {
	case plMgrScreenList:
		return m.handlePlMgrListKey(msg)
	case plMgrScreenTracks:
		return m.handlePlMgrTracksKey(msg)
	case plMgrScreenNewName:
		return m.handlePlMgrNewNameKey(msg)
	case plMgrScreenRename:
		return m.handlePlMgrRenameKey(msg)
	}
	return nil
}

// handlePlMgrListKey handles keys on screen 0 (playlist list).
func (m *Model) handlePlMgrListKey(msg tea.KeyPressMsg) tea.Cmd {
	// Filter input mode swallows most keys.
	if m.plManager.filtering {
		return m.handlePlMgrFilterKey(msg)
	}

	// If waiting for delete confirmation, only accept y/n.
	if m.plManager.confirmDel {
		switch msg.String() {
		case "y", "Y":
			realIdx := m.plMgrPlaylistRealIndex(m.plManager.cursor)
			if realIdx >= 0 {
				name := m.plManager.playlists[realIdx].Name
				if tracks, err := m.localProvider.Tracks(name); err == nil {
					m.plManager.undo = plManagerUndo{kind: plUndoPlaylist, name: name, tracks: cloneTracks(tracks)}
				}
				if d, ok := m.localProvider.(provider.PlaylistDeleter); ok {
					if err := d.DeletePlaylist(name); err != nil {
						m.status.Errorf(statusTTLDefault, "Delete failed: %s", err)
					} else {
						m.status.Showf(statusTTLDefault, "Deleted %q (u to undo)", name)
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

	count := m.plMgrListViewCount()
	switch msg.String() {
	case "ctrl+c":
		m.plManager.visible = false
		return m.quit()
	case "/":
		m.plManager.filtering = true
		m.plManager.savedCursor = m.plManager.cursor
		m.plManager.savedScroll = m.plManager.scroll
		m.plManager.filter = ""
		m.plManager.filtered = nil
		m.plManager.cursor = 0
		m.plManager.scroll = 0
		return nil
	case "up", "k":
		if m.plManager.cursor > 0 {
			m.plManager.cursor--
		} else if count > 0 {
			m.plManager.cursor = count - 1
		}
		m.plMgrListMaybeAdjustScroll(m.plMgrListVisible())
	case "down", "j":
		if m.plManager.cursor < count-1 {
			m.plManager.cursor++
		} else if count > 0 {
			m.plManager.cursor = 0
		}
		m.plMgrListMaybeAdjustScroll(m.plMgrListVisible())
	case "ctrl+x":
		m.toggleExpandedView()
		m.plMgrListMaybeAdjustScroll(m.plMgrListVisible())
	case "pgup", "ctrl+u":
		if m.plManager.cursor > 0 {
			visible := m.plMgrListVisible()
			m.plManager.cursor -= min(m.plManager.cursor, visible)
			m.plMgrListMaybeAdjustScroll(visible)
		}
	case "pgdown", "ctrl+d":
		if m.plManager.cursor < count-1 {
			visible := m.plMgrListVisible()
			m.plManager.cursor = min(count-1, m.plManager.cursor+visible)
			m.plMgrListMaybeAdjustScroll(visible)
		}
	case "home", "g":
		m.plManager.cursor = 0
		m.plMgrListMaybeAdjustScroll(m.plMgrListVisible())
	case "end", "G":
		if count > 0 {
			m.plManager.cursor = count - 1
		}
		m.plMgrListMaybeAdjustScroll(m.plMgrListVisible())
	case "enter", "l", "right":
		realIdx := m.plMgrPlaylistRealIndex(m.plManager.cursor)
		if realIdx >= 0 {
			m.plMgrEnterTrackList(m.plManager.playlists[realIdx].Name)
		} else {
			// "+ New Playlist..." selected. Pre-fill the input with the
			// active filter so a no-match search doubles as "create this".
			m.plManager.screen = plMgrScreenNewName
			m.plManager.newName = m.plManager.filter
			m.plManager.inputErr = ""
		}
	case "a":
		// Quick-add current track to the highlighted playlist.
		realIdx := m.plMgrPlaylistRealIndex(m.plManager.cursor)
		if realIdx >= 0 {
			m.addToPlaylist(m.plManager.playlists[realIdx].Name)
			m.plMgrRefreshList()
		}
	case "w":
		tracks := m.playlist.Tracks()
		if len(tracks) == 0 {
			m.status.Warning("Queue is empty", statusTTLShort)
			return nil
		}
		m.openPlaylistPicker(tracks, fmt.Sprintf("Save %d queued tracks", len(tracks)))
	case "r":
		realIdx := m.plMgrPlaylistRealIndex(m.plManager.cursor)
		if realIdx < 0 {
			return nil
		}
		name := m.plManager.playlists[realIdx].Name
		if name == history.PlaylistName {
			m.status.Warning("Recently Played cannot be renamed", statusTTLDefault)
			return nil
		}
		m.plManager.renameOldName = name
		m.plManager.renameName = name
		m.plManager.inputErr = ""
		m.plManager.screen = plMgrScreenRename
	case "d":
		if m.plMgrPlaylistRealIndex(m.plManager.cursor) >= 0 {
			m.plManager.confirmDel = true
		}
	case "u":
		m.plMgrUndoLast()
	case "esc", "p":
		if m.plManager.filter != "" {
			// First Esc clears an active filter rather than closing.
			m.plMgrResetFilter()
			return nil
		}
		m.plManager.visible = false
	}
	return nil
}

// handlePlMgrFilterKey handles keys while typing into the `/` filter on either
// the list or tracks screen.
func (m *Model) handlePlMgrFilterKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.plManager.visible = false
		return m.quit()
	case "esc":
		// Cancel filter, restore cursor.
		m.plMgrResetFilter()
		m.plManager.cursor = m.plManager.savedCursor
		m.plManager.scroll = m.plManager.savedScroll
		clampCount := m.plMgrListViewCount()
		if m.plManager.screen == plMgrScreenTracks {
			clampCount = m.plMgrTracksViewCount()
		}
		if clampCount > 0 && m.plManager.cursor >= clampCount {
			m.plManager.cursor = clampCount - 1
		}
		return nil
	case "enter":
		// Commit filter; leave query in place but stop intercepting keys.
		m.plManager.filtering = false
		if m.plManager.filter == "" {
			m.plManager.cursor = m.plManager.savedCursor
			m.plManager.scroll = m.plManager.savedScroll
		}
		return nil
	case "down":
		// Drop into result navigation immediately.
		m.plManager.filtering = false
		count := m.plMgrListViewCount()
		if m.plManager.screen == plMgrScreenTracks {
			count = m.plMgrTracksViewCount()
		}
		if count > 0 {
			m.plManager.cursor = 0
			if m.plManager.screen == plMgrScreenList {
				m.plMgrListMaybeAdjustScroll(m.plMgrListVisible())
			} else {
				m.plMgrTracksMaybeAdjustScroll(m.plMgrTracksVisible())
			}
		}
		return nil
	case "backspace":
		if m.plManager.filter != "" {
			m.editText("playlist-manager-filter", &m.plManager.filter, msg)
			m.plManager.cursor = 0
			m.plMgrRecomputeFilter()
		} else {
			m.plManager.filtering = false
			m.plManager.cursor = m.plManager.savedCursor
			m.plManager.scroll = m.plManager.savedScroll
		}
		return nil
	}

	if msg.Code == tea.KeySpace && msg.Text == "" {
		m.insertText("playlist-manager-filter", &m.plManager.filter, " ")
		m.plManager.cursor = 0
		m.plMgrRecomputeFilter()
		return nil
	}
	if m.editText("playlist-manager-filter", &m.plManager.filter, msg) {
		m.plManager.cursor = 0
		m.plMgrRecomputeFilter()
	}
	return nil
}

// handlePlMgrTracksKey handles keys on screen 1 (track list inside a playlist).
func (m *Model) handlePlMgrTracksKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.plManager.filtering {
		return m.handlePlMgrFilterKey(msg)
	}

	count := m.plMgrTracksViewCount()
	switch msg.String() {
	case "ctrl+h":
		m.toggleAlbumHeadersManual()
		m.plMgrTracksMaybeAdjustScroll(m.plMgrTracksVisible())
		return nil
	case "ctrl+c":
		m.plManager.visible = false
		return m.quit()
	case "/":
		m.plManager.filtering = true
		m.plManager.savedCursor = m.plManager.cursor
		m.plManager.savedScroll = m.plManager.scroll
		m.plManager.filter = ""
		m.plManager.filtered = nil
		m.plManager.cursor = 0
		m.plManager.scroll = 0
		return nil
	case "up", "k":
		if m.plManager.cursor > 0 {
			m.plManager.cursor--
		} else if count > 0 {
			m.plManager.cursor = count - 1
		}
		m.plMgrTracksMaybeAdjustScroll(m.plMgrTracksVisible())
	case "down", "j":
		if m.plManager.cursor < count-1 {
			m.plManager.cursor++
		} else if count > 0 {
			m.plManager.cursor = 0
		}
		m.plMgrTracksMaybeAdjustScroll(m.plMgrTracksVisible())
	case "[":
		m.plMgrMoveTrack(-1)
	case "]":
		m.plMgrMoveTrack(1)
	case "ctrl+x":
		m.toggleExpandedView()
		m.plMgrTracksMaybeAdjustScroll(m.plMgrTracksVisible())
	case "pgup", "ctrl+u":
		if m.plManager.cursor > 0 {
			visible := m.plMgrTracksVisible()
			m.plManager.cursor -= min(m.plManager.cursor, visible)
			m.plMgrTracksMaybeAdjustScroll(visible)
		}
	case "pgdown", "ctrl+d":
		if m.plManager.cursor < count-1 {
			visible := m.plMgrTracksVisible()
			m.plManager.cursor = min(count-1, m.plManager.cursor+visible)
			m.plMgrTracksMaybeAdjustScroll(visible)
		}
	case "home", "g":
		m.plManager.cursor = 0
		m.plMgrTracksMaybeAdjustScroll(m.plMgrTracksVisible())
	case "end", "G":
		if count > 0 {
			m.plManager.cursor = count - 1
		}
		m.plMgrTracksMaybeAdjustScroll(m.plMgrTracksVisible())
	case "enter":
		// Play the highlighted track; the rest of the playlist follows.
		if len(m.plManager.tracks) > 0 {
			startIdx := m.plMgrTrackRealIndex(m.plManager.cursor)
			if startIdx < 0 {
				startIdx = 0
			}
			return m.plMgrLoadAndPlay(startIdx)
		}
	case "p":
		// Play all from the top, regardless of cursor.
		if len(m.plManager.tracks) > 0 {
			return m.plMgrLoadAndPlay(0)
		}
	case "space":
		realIdx := m.plMgrTrackRealIndex(m.plManager.cursor)
		m.plMgrToggleMark(realIdx)
		if m.plManager.cursor < count-1 {
			m.plManager.cursor++
			m.plMgrTracksMaybeAdjustScroll(m.plMgrTracksVisible())
		}
	case "a":
		m.plMgrToggleMarkAll()
	case "s":
		m.plMgrSortTracks()
	case "w":
		tracks := m.plMgrSelectedTracks()
		if len(tracks) > 0 {
			title := "Track: " + tracks[0].DisplayName()
			if len(tracks) > 1 {
				title = fmt.Sprintf("%d tracks selected", len(tracks))
			}
			m.openPlaylistPicker(tracks, title)
		}
	case "o":
		m.openFileBrowserForPlaylist(m.plManager.selPlaylist)
	case "d":
		m.plMgrRemoveSelectedTracks()
	case "u":
		m.plMgrUndoLast()
	case "esc", "backspace", "h", "left":
		if m.plManager.filter != "" {
			m.plMgrResetFilter()
			return nil
		}
		// Go back to playlist list.
		m.plMgrRefreshList()
		m.plManager.screen = plMgrScreenList
		m.plMgrResetFilter()
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

// plMgrLoadAndPlay replaces the live playlist with the manager's tracks and
// starts playback at startIdx.
func (m *Model) plMgrLoadAndPlay(startIdx int) tea.Cmd {
	m.player.Stop()
	m.player.ClearPreload()
	m.resetYTDLBatch()
	m.playlist.Replace(m.plManager.tracks)
	m.setHeaderStateFromTracks(m.plManager.tracks)
	m.loadedPlaylist = m.plManager.selPlaylist
	if startIdx < 0 || startIdx >= m.playlist.Len() {
		startIdx = 0
	}
	m.plCursor = startIdx
	m.playlist.SetIndex(startIdx)
	m.adjustScroll()
	m.plManager.visible = false
	m.plMgrResetFilter()
	m.focus = focusPlaylist
	cmd := m.playCurrentTrack()
	m.notifyPlayback()
	return cmd
}

// handlePlMgrNewNameKey handles keys on screen 2 (new playlist name input).
func (m *Model) handlePlMgrNewNameKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.plManager.screen = plMgrScreenList
	case tea.KeyEnter:
		name := strings.TrimSpace(m.plManager.newName)
		if name == "" {
			m.plManager.inputErr = "Playlist name is required."
		} else if m.createPlaylistFromManager(name) {
			m.plMgrRefreshList()
			m.plManager.screen = plMgrScreenList
		}
	default:
		if msg.Code == tea.KeySpace && msg.Text == "" {
			m.insertText("playlist-manager-new-name", &m.plManager.newName, " ")
			m.plManager.inputErr = ""
		} else if m.editText("playlist-manager-new-name", &m.plManager.newName, msg) {
			m.plManager.inputErr = ""
		}
	}
	return nil
}

// handlePlMgrRenameKey handles keys on screen 3 (rename playlist input).
func (m *Model) handlePlMgrRenameKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.plManager.screen = plMgrScreenList
	case tea.KeyEnter:
		if m.plMgrCommitRename() {
			m.plManager.screen = plMgrScreenList
		}
	default:
		if msg.Code == tea.KeySpace && msg.Text == "" {
			m.insertText("playlist-manager-rename", &m.plManager.renameName, " ")
			m.plManager.inputErr = ""
		} else if m.editText("playlist-manager-rename", &m.plManager.renameName, msg) {
			m.plManager.inputErr = ""
		}
	}
	return nil
}

// plMgrCommitRename applies the pending rename. No-op when the name is
// empty, unchanged, or the local provider doesn't support renaming.
func (m *Model) plMgrCommitRename() bool {
	newName := strings.TrimSpace(m.plManager.renameName)
	oldName := m.plManager.renameOldName
	if newName == "" {
		m.plManager.inputErr = "Playlist name is required."
		return false
	}
	if newName == oldName {
		return true
	}
	r, ok := m.localProvider.(provider.PlaylistRenamer)
	if !ok {
		m.plManager.inputErr = "Playlist renaming is not supported."
		return false
	}
	if err := r.RenamePlaylist(oldName, newName); err != nil {
		m.plManager.inputErr = "Rename failed: " + err.Error()
		return false
	}
	m.status.Showf(statusTTLDefault, "Renamed %q to %q", oldName, newName)
	if m.loadedPlaylist == oldName {
		m.loadedPlaylist = newName
	}
	m.plMgrRefreshList()
	return true
}

func (m *Model) localSaver() provider.PlaylistSaver {
	s, _ := m.localProvider.(provider.PlaylistSaver)
	return s
}

func cloneTracks(tracks []playlist.Track) []playlist.Track {
	return append([]playlist.Track(nil), tracks...)
}

func (m *Model) plMgrSetTrackUndo() {
	m.plManager.undo = plManagerUndo{
		kind:   plUndoTracks,
		name:   m.plManager.selPlaylist,
		tracks: cloneTracks(m.plManager.tracks),
	}
}

func (m *Model) plMgrUndoLast() {
	undo := m.plManager.undo
	if undo.kind == plUndoNone || undo.name == "" {
		m.status.Warning("Nothing to undo", statusTTLShort)
		return
	}
	saver := m.localSaver()
	if saver == nil {
		m.status.Warning("Undo unavailable", statusTTLDefault)
		return
	}
	if err := saver.SavePlaylist(undo.name, cloneTracks(undo.tracks)); err != nil {
		m.status.Errorf(statusTTLDefault, "Undo failed: %s", err)
		return
	}
	m.plManager.undo = plManagerUndo{}
	m.plMgrRefreshList()
	if m.plManager.screen == plMgrScreenTracks && m.plManager.selPlaylist == undo.name {
		m.plManager.tracks = cloneTracks(undo.tracks)
		m.plManager.marked = make(map[int]bool)
		m.plMgrRecomputeFilter()
		m.plMgrTracksMaybeAdjustScroll(m.plMgrTracksVisible())
	}
	m.status.Showf(statusTTLDefault, "Restored %q", undo.name)
}

func (m *Model) plMgrSelectedTrackIndices() []int {
	if len(m.plManager.marked) > 0 {
		indices := make([]int, 0, len(m.plManager.marked))
		for idx := range m.plManager.marked {
			if idx >= 0 && idx < len(m.plManager.tracks) {
				indices = append(indices, idx)
			}
		}
		sort.Ints(indices)
		return indices
	}
	realIdx := m.plMgrTrackRealIndex(m.plManager.cursor)
	if realIdx < 0 {
		return nil
	}
	return []int{realIdx}
}

func (m *Model) plMgrSelectedTracks() []playlist.Track {
	indices := m.plMgrSelectedTrackIndices()
	tracks := make([]playlist.Track, 0, len(indices))
	for _, idx := range indices {
		tracks = append(tracks, m.plManager.tracks[idx])
	}
	return tracks
}

func (m *Model) plMgrToggleMark(realIdx int) {
	if realIdx < 0 || realIdx >= len(m.plManager.tracks) {
		return
	}
	if m.plManager.marked == nil {
		m.plManager.marked = make(map[int]bool)
	}
	if m.plManager.marked[realIdx] {
		delete(m.plManager.marked, realIdx)
		return
	}
	m.plManager.marked[realIdx] = true
}

func (m *Model) plMgrToggleMarkAll() {
	count := m.plMgrTracksViewCount()
	if count == 0 {
		return
	}
	if m.plManager.marked == nil {
		m.plManager.marked = make(map[int]bool)
	}
	allMarked := true
	for i := 0; i < count; i++ {
		realIdx := m.plMgrTrackRealIndex(i)
		if realIdx >= 0 && !m.plManager.marked[realIdx] {
			allMarked = false
			break
		}
	}
	for i := 0; i < count; i++ {
		realIdx := m.plMgrTrackRealIndex(i)
		if realIdx < 0 {
			continue
		}
		if allMarked {
			delete(m.plManager.marked, realIdx)
		} else {
			m.plManager.marked[realIdx] = true
		}
	}
}

func (m *Model) plMgrSaveTracks(status string) bool {
	saver := m.localSaver()
	if saver == nil {
		m.status.Warning("Playlist saving is not supported", statusTTLDefault)
		return false
	}
	if err := saver.SavePlaylist(m.plManager.selPlaylist, m.plManager.tracks); err != nil {
		m.status.Errorf(statusTTLDefault, "Save failed: %s", err)
		return false
	}
	if status != "" {
		m.status.Show(status, statusTTLDefault)
	}
	return true
}

func (m *Model) plMgrRemoveSelectedTracks() {
	indices := m.plMgrSelectedTrackIndices()
	if len(indices) == 0 {
		return
	}
	for _, i := range indices {
		if m.plManager.tracks[i].DirSourced {
			m.status.Warningf(statusTTLDefault, "Can't remove %q: it's supplied by the playlist's directory source", m.plManager.tracks[i].DisplayName())
			return
		}
	}
	m.plMgrSetTrackUndo()
	for i := len(indices) - 1; i >= 0; i-- {
		idx := indices[i]
		m.plManager.tracks = append(m.plManager.tracks[:idx], m.plManager.tracks[idx+1:]...)
	}
	m.plManager.marked = make(map[int]bool)
	if !m.plMgrSaveTracks(fmt.Sprintf("Removed %d track(s) from %q", len(indices), m.plManager.selPlaylist)) {
		m.plManager.tracks = cloneTracks(m.plManager.undo.tracks)
		return
	}
	if m.plManager.filter != "" {
		m.plMgrRecomputeFilter()
	}
	newCount := m.plMgrTracksViewCount()
	if m.plManager.cursor >= newCount {
		m.plManager.cursor = newCount - 1
	}
	if m.plManager.cursor < 0 {
		m.plManager.cursor = 0
	}
	m.plMgrTracksMaybeAdjustScroll(m.plMgrTracksVisible())
}

func (m *Model) plMgrMoveTrack(delta int) {
	if m.plManager.filter != "" {
		m.status.Warning("Clear filter before moving tracks", statusTTLDefault)
		return
	}
	from := m.plManager.cursor
	to := from + delta
	if from < 0 || from >= len(m.plManager.tracks) || to < 0 || to >= len(m.plManager.tracks) {
		return
	}
	m.plMgrSetTrackUndo()
	m.plManager.tracks[from], m.plManager.tracks[to] = m.plManager.tracks[to], m.plManager.tracks[from]
	m.plManager.cursor = to
	m.plManager.marked = make(map[int]bool)
	if m.plMgrSaveTracks(fmt.Sprintf("Reordered %q", m.plManager.selPlaylist)) {
		m.plMgrTracksMaybeAdjustScroll(m.plMgrTracksVisible())
	} else {
		m.plManager.tracks = cloneTracks(m.plManager.undo.tracks)
	}
}

var plMgrSortModes = []string{"track", "title", "artist", "album", "artist+album", "path"}

func (m *Model) plMgrSortTracks() {
	if len(m.plManager.tracks) < 2 {
		return
	}
	m.plMgrSetTrackUndo()
	mode := plMgrSortModes[m.plManager.sortMode%len(plMgrSortModes)]
	m.plManager.sortMode++
	sort.SliceStable(m.plManager.tracks, func(i, j int) bool {
		return compareUITracks(m.plManager.tracks[i], m.plManager.tracks[j], mode) < 0
	})
	m.plManager.marked = make(map[int]bool)
	if m.plMgrSaveTracks(fmt.Sprintf("Sorted %q by %s", m.plManager.selPlaylist, mode)) {
		m.plManager.cursor = 0
		m.plManager.scroll = 0
		m.plMgrRecomputeFilter()
	} else {
		m.plManager.tracks = cloneTracks(m.plManager.undo.tracks)
	}
}

func compareUITracks(a, b playlist.Track, mode string) int {
	cmpString := func(x, y string) int {
		return strings.Compare(strings.ToLower(x), strings.ToLower(y))
	}
	first := func(values ...int) int {
		for _, v := range values {
			if v != 0 {
				return v
			}
		}
		return 0
	}
	switch mode {
	case "track":
		return first(a.TrackNumber-b.TrackNumber, cmpString(a.Title, b.Title), cmpString(a.Path, b.Path))
	case "artist":
		return first(cmpString(a.Artist, b.Artist), cmpString(a.Album, b.Album), a.TrackNumber-b.TrackNumber, cmpString(a.Title, b.Title), cmpString(a.Path, b.Path))
	case "album":
		return first(cmpString(a.Album, b.Album), a.TrackNumber-b.TrackNumber, cmpString(a.Title, b.Title), cmpString(a.Path, b.Path))
	case "artist+album":
		return first(cmpString(a.Artist, b.Artist), cmpString(a.Album, b.Album), a.TrackNumber-b.TrackNumber, cmpString(a.Title, b.Title), cmpString(a.Path, b.Path))
	case "path":
		return cmpString(a.Path, b.Path)
	default:
		return first(cmpString(a.Title, b.Title), cmpString(a.Artist, b.Artist), cmpString(a.Path, b.Path))
	}
}

func (m *Model) persistLoadedPlaylistOrder() {
	if m.loadedPlaylist == "" {
		return
	}
	saver, ok := m.localProvider.(provider.PlaylistSaver)
	if !ok {
		return
	}
	hasDirTracks := false
	for _, t := range m.playlist.Tracks() {
		if t.DirSourced {
			hasDirTracks = true
			break
		}
	}
	if err := saver.SavePlaylist(m.loadedPlaylist, m.playlist.Tracks()); err != nil {
		m.status.Errorf(statusTTLDefault, "Save failed: %s", err)
		return
	}
	if hasDirTracks {
		m.status.Warningf(statusTTLDefault, "Reordered %q (directory-sourced tracks keep scan order)", m.loadedPlaylist)
		return
	}
	m.status.Showf(statusTTLDefault, "Reordered %q", m.loadedPlaylist)
}

// addToPlaylist appends the current track to a local playlist and shows a status message.
func (m *Model) addToPlaylist(name string) {
	track, idx := m.currentPlaybackTrack()
	if idx < 0 {
		m.status.Warning("No track to add", statusTTLShort)
		return
	}
	if bw, ok := m.localProvider.(provider.PlaylistBatchWriter); ok {
		added, skipped, err := bw.AddTracksToPlaylist(context.Background(), name, []playlist.Track{track})
		if err != nil {
			m.status.Errorf(statusTTLDefault, "Failed: %s", err)
			return
		}
		switch {
		case added > 0:
			m.status.Showf(statusTTLDefault, "Added to %q", name)
		case skipped > 0:
			m.status.Warningf(statusTTLDefault, "Already in %q", name)
		default:
			m.status.Warningf(statusTTLDefault, "Nothing added to %q", name)
		}
		return
	}
	if w, ok := m.localProvider.(provider.PlaylistWriter); ok {
		if err := w.AddTrackToPlaylist(context.Background(), name, track); err != nil {
			m.status.Errorf(statusTTLDefault, "Failed: %s", err)
		} else {
			m.status.Showf(statusTTLDefault, "Added to %q", name)
		}
	}
}

func (m *Model) createPlaylistFromManager(name string) bool {
	c, ok := m.localProvider.(provider.PlaylistCreator)
	if !ok {
		m.plManager.inputErr = "Playlist creation is not supported."
		return false
	}
	id, err := c.CreatePlaylist(context.Background(), name)
	if err != nil {
		m.plManager.inputErr = "Create failed: " + err.Error()
		return false
	}
	track, idx := m.currentPlaybackTrack()
	if idx < 0 {
		m.status.Showf(statusTTLDefault, "Created %q", name)
		return true
	}
	if bw, ok := m.localProvider.(provider.PlaylistBatchWriter); ok {
		added, skipped, err := bw.AddTracksToPlaylist(context.Background(), id, []playlist.Track{track})
		if err != nil {
			m.status.Errorf(statusTTLDefault, "Created %q, add failed: %s", name, err)
			return true
		}
		if added > 0 {
			m.status.Showf(statusTTLDefault, "Created %q & added track", name)
		} else if skipped > 0 {
			m.status.Warningf(statusTTLDefault, "Created %q; track was duplicate", name)
		}
		return true
	}
	if w, ok := m.localProvider.(provider.PlaylistWriter); ok {
		if err := w.AddTrackToPlaylist(context.Background(), id, track); err != nil {
			m.status.Errorf(statusTTLDefault, "Created %q, add failed: %s", name, err)
			return true
		}
		m.status.Showf(statusTTLDefault, "Created %q & added track", name)
	}
	return true
}

func (m *Model) handleThemeFilterKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.themePickerCancel()
		return m.quit()
	case "esc":
		m.themePicker.filtering = false
		m.themePicker.filter = ""
		m.themePicker.filtered = nil
		m.themePicker.cursor = m.themePicker.savedCursor
		m.themePicker.scroll = m.themePicker.savedScroll
		return nil
	case "enter":
		m.themePicker.filtering = false
		if m.themePicker.filter == "" {
			m.themePicker.cursor = m.themePicker.savedCursor
			m.themePicker.scroll = m.themePicker.savedScroll
		}
		return nil
	case "down":
		m.themePicker.filtering = false
		if m.themePickerViewCount() > 0 {
			m.themePicker.cursor = 0
			m.themePickerApply()
			m.themePickerMaybeAdjustScroll(m.themePickerVisible())
		}
		return nil
	case "backspace":
		if m.themePicker.filter == "" {
			m.themePicker.filtering = false
			m.themePicker.cursor = m.themePicker.savedCursor
			m.themePicker.scroll = m.themePicker.savedScroll
			return nil
		}
	}

	if m.editText("theme-picker-filter", &m.themePicker.filter, msg) {
		m.themePickerRecomputeFilter()
	}
	return nil
}

// handleThemeKey processes key presses while the theme picker is open.
func (m *Model) handleThemeKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.themePicker.filtering {
		return m.handleThemeFilterKey(msg)
	}

	count := m.themePickerViewCount()
	switch msg.String() {
	case "ctrl+c":
		m.themePickerCancel()
		return m.quit()

	case "up", "k":
		if m.themePicker.cursor > 0 {
			m.themePicker.cursor--
		} else if count > 0 {
			m.themePicker.cursor = count - 1
		}
		m.themePickerApply()
		m.themePickerMaybeAdjustScroll(m.themePickerVisible())

	case "down", "j":
		if m.themePicker.cursor < count-1 {
			m.themePicker.cursor++
		} else if count > 0 {
			m.themePicker.cursor = 0
		}
		m.themePickerApply()
		m.themePickerMaybeAdjustScroll(m.themePickerVisible())

	case "ctrl+x":
		m.toggleExpandedView()
		m.themePickerMaybeAdjustScroll(m.themePickerVisible())

	case "pgup", "ctrl+u":
		if m.themePicker.cursor > 0 {
			visible := m.themePickerVisible()
			m.themePicker.cursor -= min(m.themePicker.cursor, visible)
			m.themePickerApply()
			m.themePickerMaybeAdjustScroll(visible)
		}

	case "pgdown", "ctrl+d":
		if m.themePicker.cursor < count-1 {
			visible := m.themePickerVisible()
			m.themePicker.cursor = min(count-1, m.themePicker.cursor+visible)
			m.themePickerApply()
			m.themePickerMaybeAdjustScroll(visible)
		}

	case "home", "g":
		m.themePicker.cursor = 0
		m.themePickerApply()
		m.themePickerMaybeAdjustScroll(m.themePickerVisible())

	case "end", "G":
		if count > 0 {
			m.themePicker.cursor = count - 1
		}
		m.themePickerApply()
		m.themePickerMaybeAdjustScroll(m.themePickerVisible())

	case "enter":
		m.themePickerSelect()

	case "/":
		m.themePicker.savedCursor = m.themePicker.cursor
		m.themePicker.savedScroll = m.themePicker.scroll
		m.themePicker.filtering = true
		m.themePicker.filter = ""
		m.themePickerRecomputeFilter()
		return nil

	case "esc", "q", "t":
		m.themePickerCancel()
	}
	return nil
}

func (m *Model) handleVisPickerFilterKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.visPickerCancel()
		return m.quit()
	case "esc":
		m.visPicker.filtering = false
		m.visPicker.filter = ""
		m.visPicker.filtered = nil
		m.visPicker.cursor = m.visPicker.savedCursor
		m.visPicker.scroll = m.visPicker.savedScroll
		return nil
	case "enter":
		m.visPicker.filtering = false
		if m.visPicker.filter == "" {
			m.visPicker.cursor = m.visPicker.savedCursor
			m.visPicker.scroll = m.visPicker.savedScroll
		}
		return nil
	case "down":
		m.visPicker.filtering = false
		if m.visPickerViewCount() > 0 {
			m.visPicker.cursor = 0
			m.visPickerApply()
			m.visPickerMaybeAdjustScroll(m.visPickerVisible())
		}
		return nil
	case "backspace":
		if m.visPicker.filter == "" {
			m.visPicker.filtering = false
			m.visPicker.cursor = m.visPicker.savedCursor
			m.visPicker.scroll = m.visPicker.savedScroll
			return nil
		}
	}

	if m.editText("visualizer-picker-filter", &m.visPicker.filter, msg) {
		m.visPickerRecomputeFilter()
	}
	return nil
}

// handleVisPickerKey processes key presses while the visualizer picker is open.
func (m *Model) handleVisPickerKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.visPicker.filtering {
		return m.handleVisPickerFilterKey(msg)
	}

	count := m.visPickerViewCount()
	switch msg.String() {
	case "ctrl+c":
		m.visPickerCancel()
		return m.quit()

	case "up", "k":
		if m.visPicker.cursor > 0 {
			m.visPicker.cursor--
		} else if count > 0 {
			m.visPicker.cursor = count - 1
		}
		m.visPickerApply()
		m.visPickerMaybeAdjustScroll(m.visPickerVisible())

	case "down", "j":
		if m.visPicker.cursor < count-1 {
			m.visPicker.cursor++
		} else if count > 0 {
			m.visPicker.cursor = 0
		}
		m.visPickerApply()
		m.visPickerMaybeAdjustScroll(m.visPickerVisible())

	case "ctrl+x":
		m.toggleExpandedView()
		m.visPickerMaybeAdjustScroll(m.visPickerVisible())

	case "pgup", "ctrl+u":
		if m.visPicker.cursor > 0 {
			visible := m.visPickerVisible()
			m.visPicker.cursor -= min(m.visPicker.cursor, visible)
			m.visPickerApply()
			m.visPickerMaybeAdjustScroll(visible)
		}

	case "pgdown", "ctrl+d":
		if m.visPicker.cursor < count-1 {
			visible := m.visPickerVisible()
			m.visPicker.cursor = min(count-1, m.visPicker.cursor+visible)
			m.visPickerApply()
			m.visPickerMaybeAdjustScroll(visible)
		}

	case "home", "g":
		m.visPicker.cursor = 0
		m.visPickerApply()
		m.visPickerMaybeAdjustScroll(m.visPickerVisible())

	case "end", "G":
		if count > 0 {
			m.visPicker.cursor = count - 1
		}
		m.visPickerApply()
		m.visPickerMaybeAdjustScroll(m.visPickerVisible())

	case "enter":
		m.visPickerSelect()

	case "/":
		m.visPicker.savedCursor = m.visPicker.cursor
		m.visPicker.savedScroll = m.visPicker.scroll
		m.visPicker.filtering = true
		m.visPicker.filter = ""
		m.visPickerRecomputeFilter()
		return nil

	case "esc", "q", "ctrl+v":
		m.visPickerCancel()
	}
	return nil
}

// handleQueueKey processes key presses while the queue manager overlay is open.
func (m *Model) queueMaybeAdjustScroll(visible int) {
	clampScroll(&m.queue.cursor, &m.queue.scroll, m.playlist.QueueLen(), visible)
}

func (m *Model) handleQueueKey(msg tea.KeyPressMsg) tea.Cmd {
	qLen := m.playlist.QueueLen()

	switch msg.String() {
	case "ctrl+c":
		m.queue.visible = false
		return m.quit()
	case "ctrl+k", "?":
		m.openKeymap()
	case "ctrl+x":
		m.toggleExpandedView()
		m.queueMaybeAdjustScroll(m.queueVisible())
	case "up", "k":
		if m.queue.cursor > 0 {
			m.queue.cursor--
		} else if qLen > 0 {
			m.queue.cursor = qLen - 1
		}
		m.queueMaybeAdjustScroll(m.queueVisible())

	case "down", "j":
		if m.queue.cursor < qLen-1 {
			m.queue.cursor++
		} else if qLen > 0 {
			m.queue.cursor = 0
		}
		m.queueMaybeAdjustScroll(m.queueVisible())
	case "shift+up":
		if m.queue.cursor > 0 {
			if m.playlist.MoveQueue(m.queue.cursor, m.queue.cursor-1) {
				m.queue.cursor--
			}
		}
		m.queueMaybeAdjustScroll(m.queueVisible())
	case "shift+down":
		if m.queue.cursor < qLen-1 {
			if m.playlist.MoveQueue(m.queue.cursor, m.queue.cursor+1) {
				m.queue.cursor++
			}
		}
		m.queueMaybeAdjustScroll(m.queueVisible())
	case "d":
		if qLen > 0 {
			m.playlistUndo = playlistUndo{active: true, snapshot: m.playlist.Snapshot()}
			m.playlist.RemoveQueueAt(m.queue.cursor)
			if m.queue.cursor >= m.playlist.QueueLen() && m.queue.cursor > 0 {
				m.queue.cursor--
			}
			m.status.Show("Removed queued track (Ctrl+Z to undo)", statusTTLDefault)
		}
		m.queueMaybeAdjustScroll(m.queueVisible())
	case "c":
		if qLen > 0 {
			m.playlistUndo = playlistUndo{active: true, snapshot: m.playlist.Snapshot()}
			m.playlist.ClearQueue()
			m.status.Show("Cleared queue (Ctrl+Z to undo)", statusTTLDefault)
		}
		m.queue.visible = false
	case "esc", "A":
		m.queue.visible = false
	}
	return nil
}

func (m *Model) deviceMaybeAdjustScroll(visible int) {
	clampScroll(&m.devicePicker.cursor, &m.devicePicker.scroll, len(m.devicePicker.devices), visible)
}

// handleDeviceKey processes key presses while the audio device picker is open.
func (m *Model) handleDeviceKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.devicePicker.visible = false
		return m.quit()
	case "ctrl+x":
		m.toggleExpandedView()
		m.deviceMaybeAdjustScroll(m.devicePickerVisible())
	case "up", "k":
		if m.devicePicker.cursor > 0 {
			m.devicePicker.cursor--
		} else if len(m.devicePicker.devices) > 0 {
			m.devicePicker.cursor = len(m.devicePicker.devices) - 1
		}
		m.deviceMaybeAdjustScroll(m.devicePickerVisible())
	case "down", "j":
		if m.devicePicker.cursor < len(m.devicePicker.devices)-1 {
			m.devicePicker.cursor++
		} else if len(m.devicePicker.devices) > 0 {
			m.devicePicker.cursor = 0
		}
		m.deviceMaybeAdjustScroll(m.devicePickerVisible())
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
