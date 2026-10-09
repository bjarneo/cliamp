package model

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/tracksave"
)

// quit shuts down the player and signals the TUI to exit.
func (m *Model) quit() tea.Cmd {
	// Only save resume for seekable tracks:
	// - local files (not stream)
	// - HTTP streams with known duration (podcast MP3s)
	// - finite Mixcloud shows (yt-dlp tracks with a counted PCM position)
	// Other yt-dlp sites and live streams remain excluded. The live check
	// is currentPlaybackIsLive, so a stream the player finds live counts.
	if track, _ := m.currentPlaybackTrack(); track.Path != "" &&
		(!playlist.IsYTDL(track.Path) || playlist.IsMixcloudURL(track.Path)) &&
		!m.currentPlaybackIsLive(track) &&
		m.player.IsPlaying() && !m.buffering && !m.player.GaplessAdvanced() {
		if secs := int(m.player.Position().Seconds()); secs > 0 {
			context, contextIndex := m.playbackContextFor(track)
			m.exitResume.path = track.Path
			m.exitResume.secs = secs
			m.exitResume.playlist = m.loadedPlaylist
			m.exitResume.context = cloneTracks(context)
			m.exitResume.contextIndex = contextIndex
		}
	}

	m.flushPendingSpeedSave()
	m.flushPendingEQSave()
	m.flushPendingVolumeSave()
	// Quit leaves the track that plays, as s does, so it can scrobble.
	m.leaveTrack(m.player.PositionAndDuration())
	m.player.Close()
	m.clearPlaybackTrack()
	m.quitting = true
	return tea.Quit
}

func (m *Model) handleSpeedKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "q":
		return m.quit()
	case "]", "right", "l", "up", "k":
		m.changeSpeed(0.25)
	case "[", "left", "h", "down", "j":
		m.changeSpeed(-0.25)
	case "tab":
		m.focus = m.nextMainFocus(focusSpeed)
	case "shift+tab", "esc", "backspace":
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
	total := len(m.provPane.lists)
	if total == 0 {
		m.provPane.scroll = 0
		return
	}

	if m.provPane.cursor < m.provPane.scroll {
		m.provPane.scroll = m.provPane.cursor
	}

	if m.provPane.scroll >= total {
		m.provPane.scroll = max(0, total-1)
	}

	// Provider lists can add radio-prefix or PlaylistInfo.Section headers. Keep
	// the logical cursor visible in their rendered-row viewport.
	for m.provPane.scroll < total && m.providerRowsFromScroll(m.provPane.scroll, m.provPane.cursor) > visible {
		m.provPane.scroll++
	}
}

func (m *Model) providerRowsFromScroll(scroll, cursor int) int {
	total := len(m.provPane.lists)
	if total == 0 || cursor < scroll || scroll < 0 || cursor >= total {
		return 0
	}

	rows := 0
	sl, isRadio := m.provider.(provider.SectionedList)
	// Must resolve the same heading the renderer does, or the two disagree on
	// how many rows a window holds and the cursor scrolls out of view.
	headerAt := func(i int) string {
		if isRadio {
			return m.providerSectionTitle(sl.IDPrefix(m.provPane.lists[i].ID))
		}
		return m.provPane.lists[i].Section
	}

	prevHeader := ""
	if scroll > 0 {
		prevHeader = headerAt(scroll - 1)
	}

	for i := scroll; i <= cursor && i < total; i++ {
		header := headerAt(i)
		if header != "" && header != prevHeader {
			rows++ // section header row
		}
		rows++ // item row
		prevHeader = header
	}
	return rows
}

func (m *Model) providerMoveUp() {
	if m.provPane.cursor > 0 {
		m.provPane.cursor--
	} else if len(m.provPane.lists) > 0 {
		m.provPane.cursor = len(m.provPane.lists) - 1
	}
	m.providerMaybeAdjustScroll()
}

func (m *Model) providerMoveDown() {
	if m.provPane.cursor < len(m.provPane.lists)-1 {
		m.provPane.cursor++
	} else if len(m.provPane.lists) > 0 {
		m.provPane.cursor = 0
	}
	m.providerMaybeAdjustScroll()
}

func (m *Model) providerPageUp() {
	step := m.providerScrollStep()
	if m.provPane.cursor > 0 {
		m.provPane.cursor -= min(m.provPane.cursor, step)
	}
	// Top-anchor behavior: place cursor at top of viewport when paging up.
	m.provPane.scroll = m.provPane.cursor
	m.providerMaybeAdjustScroll()
}

func (m *Model) providerPageDown() {
	step := m.providerScrollStep()
	if m.provPane.cursor < len(m.provPane.lists)-1 {
		m.provPane.cursor = min(len(m.provPane.lists)-1, m.provPane.cursor+step)
	}
	// Bottom-anchor behavior: bias viewport so cursor lands near bottom when paging down.
	m.provPane.scroll = max(0, m.provPane.cursor-step+1)
	m.providerMaybeAdjustScroll()
}

func (m *Model) providerToTop() {
	m.provPane.cursor = 0
	m.providerMaybeAdjustScroll()
}

func (m *Model) providerToBottom() {
	if len(m.provPane.lists) > 0 {
		m.provPane.cursor = len(m.provPane.lists) - 1
	}
	m.providerMaybeAdjustScroll()
}

func normalizeShiftedLetter(msg tea.KeyPressMsg) tea.KeyPressMsg {
	if msg.Text != "" || msg.Mod != tea.ModShift ||
		msg.Code < 'a' || msg.Code > 'z' ||
		msg.ShiftedCode < 'A' || msg.ShiftedCode > 'Z' {
		return msg
	}
	msg.Text = string(msg.ShiftedCode)
	return msg
}

// handleKey processes a single key press and returns an optional command.
// The global keys run first. Then the top overlay, the provider filter or
// the focused area owns the key. The provider filter takes keys only while
// the provider pane has the focus, where commandContext shows it.
func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	msg = normalizeShiftedLetter(msg)

	if cmd, ok := m.handleGlobalKey(msg); ok {
		return cmd
	}
	// The top overlay owns the keys.
	if spec, ok := m.topOverlay(); ok {
		return spec.key(m, msg)
	}

	if m.provSearch.active && m.focus == focusProvider {
		return m.handleProvSearchKey(msg)
	}
	if m.focus != focusProvider {
		switch msg.String() {
		case "ctrl+i":
			m.toggleMetadata()
			return nil
		case "i":
			m.info = infoOverlay{visible: true}
			return nil
		}
	}

	switch m.focus {
	case focusProvider:
		return m.handleProviderPaneKey(msg)
	case focusSpeed:
		return m.handleSpeedKey(msg)
	case focusProvPill:
		return m.handleProvPillKey(msg)
	}
	return m.handleMainKey(msg)
}

// handleGlobalKey processes the keys that work over every overlay and focus:
// quit, undo and the keymap. While the terminal is too small, it takes every
// key and quits on q or ctrl+c. The screen shows only the too-small notice
// then, so no key may change hidden state. ok is false when the key goes on
// to the overlays and the focused area.
func (m *Model) handleGlobalKey(msg tea.KeyPressMsg) (cmd tea.Cmd, ok bool) {
	if msg.String() == "ctrl+c" {
		return m.quit(), true
	}
	if m.width > 0 && m.layout.tooSmall() {
		if msg.String() == "q" {
			return m.quit(), true
		}
		return nil, true
	}
	switch msg.String() {
	case "ctrl+z":
		m.keepPlCursorRow(func() { cmd = m.undoPlaylistMutation() })
		return cmd, true
	case "ctrl+k":
		if !m.keymap.visible {
			if m.fullVis {
				m.exitFullVisualizer()
			}
			m.openKeymap()
			return nil, true
		}
	}
	return nil, false
}

// handleProviderPaneKey processes a key press while the provider pane has
// the focus.
func (m *Model) handleProviderPaneKey(msg tea.KeyPressMsg) tea.Cmd {
	// The location question owns the keyboard until it is answered: it is
	// a yes/no about the listener's own data, so it must not be dismissed
	// by a stray key that happens to mean something else in this pane.
	if m.provPane.askLoc {
		switch msg.String() {
		case "y", "Y", "enter":
			return m.answerLocationPrompt(true)
		case "n", "N", "esc":
			return m.answerLocationPrompt(false)
		default:
			m.status.Show("Answer y for yes, n for no.", statusTTLShort)
		}
		return nil
	}

	if cmd, ok := m.providerShortcut(msg.String()); ok {
		return cmd
	}
	switch msg.String() {
	case "q":
		return m.quit()
	case "F":
		m.openSubsOverlay()
	case "l":
		return m.loadLatestFromProviderList()
	case "a":
		return m.appendShowFromProviderList()
	case "p":
		if m.localProvider != nil {
			m.openPlaylistManager()
		}
	case "up", "k":
		m.providerMoveUp()
	case "space":
		return m.togglePlayPause()
	case "down", "j":
		m.providerMoveDown()
		// Auto-load next catalog page when scrolling near the bottom.
		return m.maybeLoadCatalogBatch()
	case "enter":
		if m.provPane.signIn {
			if auth, ok := m.provider.(playlist.Authenticator); ok {
				m.provPane.signIn = false
				m.provPane.loading = true
				m.err = nil
				return authenticateProviderCmd(auth, m.provider.Name(), nextRequest(&m.requests.auth))
			}
		}
		if len(m.provPane.lists) > 0 && !m.provPane.loading {
			return m.openProviderList(m.provPane.cursor)
		}
	case "tab", "shift+tab":
		// Leave the content-first provider layout before choosing a control;
		// the playback pane may be closed or too short to show every setting.
		m.focus = focusPlaylist
		m.recomputeLayout()
		if msg.String() == "shift+tab" {
			m.focus = m.previousMainFocus(focusPlaylist)
		} else {
			m.focus = m.nextMainFocus(focusPlaylist)
		}
	case "esc", "backspace", "b":
		// Clear completed results or cancel a search still in flight.
		if m.providerCatalogSearching() {
			return m.restoreCatalog(m.provider.(provider.CatalogSearcher))
		}
		// Leave even with an empty playlist. Starting with nothing to play
		// is what opens this view (StartInProvider), so gating the way out
		// on a loaded playlist made the launch screen inescapable.
		m.focus = focusPlaylist
	case "/":
		m.provSearch.active = true
		m.provSearch.query = ""
		m.provSearch.results = nil
		m.provSearch.cursor = 0
		m.provSearch.scroll = 0
	case "ctrl+r":
		return m.refreshActiveProvider(false)
	case "f":
		return m.toggleProviderFavorite()
	case "o":
		m.openFileBrowser()
	case "N":
		// Provider-pane browsing must stay scoped to the provider being
		// viewed. Falling back to another registered browser can otherwise
		// send (for example) Spotify's pane into Mixcloud.
		if providerSupportsBrowse(m.provider) {
			m.openNavBrowserWith(m.provider)
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
	case "ctrl+x":
		m.toggleExpandedView()
	case "ctrl+f":
		m.openProviderSearch()
	}
	return nil
}

// handleProvPillKey processes a key press while the source pills have the
// focus.
func (m *Model) handleProvPillKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "q":
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
	case "shift+tab", "esc", "backspace":
		m.focus = m.previousMainFocus(focusProvPill)
	case "space":
		return m.togglePlayPause()
	}
	return nil
}

// handleMainKey processes a key press for the playlist and the focused
// playback controls. It forwards the keys that it does not handle to plugins.
func (m *Model) handleMainKey(msg tea.KeyPressMsg) tea.Cmd {
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

	// Focused settings reuse the global actions below, including notifications,
	// config persistence, and gapless rearming.
	key := msg.String()
	repeatStep := playlist.RepeatMode(1)
	switch m.focus {
	case focusVolume:
		switch key {
		case "left", "h", "down", "j":
			key = "-"
		case "right", "l", "up", "k":
			key = "+"
		}
	case focusShuffle:
		switch key {
		case "left", "h", "down", "j", "right", "l", "up", "k", "enter":
			key = "z"
		}
	case focusRepeat:
		switch key {
		case "left", "h", "down", "j":
			repeatStep = -1
			key = "r"
		case "right", "l", "up", "k", "enter":
			key = "r"
		}
	}

	if cmd, ok := m.providerShortcut(key); ok {
		return cmd
	}
	switch key {
	case "q":
		return m.quit()
	case "ctrl+r":
		// The playlist view reloads only an open provider playlist that
		// keeps its ID across Refresh.
		return m.refreshActiveProvider(true)
	case "esc", "backspace", "b":
		if m.focus == focusPlaylist {
			// Keep current expanded/collapsed height mode when switching focus.
			m.focus = focusProvider
			// The Local source has no provider pane list; show its manager.
			m.ensureLocalManager()
		} else {
			m.focus = m.previousMainFocus(m.focus)
		}

	case "space":
		return m.togglePlayPause()

	case "s":
		m.stopByUser()
		return nil

	case ">", ".":
		return m.skipNext()

	case "<", ",":
		return m.skipPrev()

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
		return m.togglePlaylistStar()

	case "shift+up", "shift+down":
		if m.focus == focusPlaylist {
			to := m.plCursor + 1
			if key == "shift+up" {
				to = m.plCursor - 1
			}
			cmd, _ := m.moveTrack(m.plCursor, to)
			return cmd
		}

	case "up", "k":
		if m.focus == focusEQ {
			bands := m.player.EQBands()
			m.setCustomEQBand(m.eqCursor, bands[m.eqCursor]+1)
		} else {
			if row := m.plCursorRow(); row > 0 {
				m.setPlCursorRow(row - 1)
			} else if m.playlist.Len() > 0 {
				m.setPlCursorRow(m.playlist.Len() - 1)
			}
		}

	case "down", "j":
		if m.focus == focusEQ {
			bands := m.player.EQBands()
			m.setCustomEQBand(m.eqCursor, bands[m.eqCursor]-1)
		} else {
			if row := m.plCursorRow(); row < m.playlist.Len()-1 {
				m.setPlCursorRow(row + 1)
			} else if m.playlist.Len() > 0 {
				m.setPlCursorRow(0)
			}
		}

	case "pgup", "ctrl+u":
		if row := m.plCursorRow(); m.focus == focusPlaylist && row > 0 {
			visible := max(1, m.effectivePlaylistVisible())
			m.setPlCursorRow(row - min(row, visible))
		}

	case "pgdown", "ctrl+d":
		if row := m.plCursorRow(); m.focus == focusPlaylist && row < m.playlist.Len()-1 {
			visible := max(1, m.effectivePlaylistVisible())
			m.setPlCursorRow(min(m.playlist.Len()-1, row+visible))
		}

	case "g", "home":
		if m.focus == focusPlaylist && m.plCursorRow() != 0 {
			m.setPlCursorRow(0)
		}

	case "G", "end":
		if m.focus == focusPlaylist && m.playlist.Len() > 0 && m.plCursorRow() != m.playlist.Len()-1 {
			m.setPlCursorRow(m.playlist.Len() - 1)
		}

	case "enter":
		if m.focus == focusPlaylist {
			// No-op only if this exact track is still buffering.
			if m.buffering && m.plCursor == m.playlist.Index() {
				break
			}
			return m.playIndex(m.plCursor)
		}

	case "+", "=":
		m.adjustVolume(1)

	case "-":
		m.adjustVolume(-1)

	case "r":
		const repeatModes = playlist.RepeatOne + 1
		return m.setRepeat((m.playlist.Repeat() + repeatStep + repeatModes) % repeatModes)

	case "z":
		return m.setShuffle(!m.playlist.Shuffled())

	case "tab":
		m.focus = m.nextMainFocus(m.focus)
	case "shift+tab":
		m.focus = m.previousMainFocus(m.focus)

	case "h":
		if m.focus == focusEQ && m.eqCursor > 0 {
			m.eqCursor--
		}

	case "l":
		if m.focus == focusEQ && m.eqCursor < eqBandCount-1 {
			m.eqCursor++
		}

	case "e":
		if m.simplified || m.layout.tier == layoutMinimal {
			break
		}
		m.cycleEQPreset()
		m.scheduleEQSave()

	case "a":
		if m.focus == focusPlaylist {
			if !m.playlist.Dequeue(m.plCursor) {
				m.playlist.Queue(m.plCursor)
			}
			m.normalizeQueueOverlay()
			return m.rearmStalePreload()
		}

	case "w":
		if m.focus == focusPlaylist && m.plCursor >= 0 && m.plCursor < m.playlist.Len() {
			if track, ok := m.playlist.Track(m.plCursor); ok {
				m.openPlaylistPicker([]playlist.Track{track}, "Track: "+track.DisplayName())
			}
		}

	case "A":
		if m.focus == focusPlaylist {
			m.queue.visible = true
			m.queue.cursor = 0
			m.queue.scroll = 0
			m.queue.confirmClear = false
		}

	case "F":
		m.openSubsOverlay()

	case "ctrl+s":
		return m.saveTrack()

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
	case "p":
		if m.localProvider != nil {
			m.openPlaylistManager()
		}

	case "t":
		m.openThemePicker()

	case "y":
		m.lyrics.visible = !m.lyrics.visible
		if m.lyrics.visible {
			return m.retryLyrics()
		}

	case "o":
		m.openFileBrowser()

	case "u":
		m.urlInput = urlInputState{active: true}

	case "N":
		if cmd, ok := m.openSelectedTrackArtistBrowser(); ok {
			return cmd
		}
		if providerSupportsBrowse(m.provider) {
			m.openNavBrowserWith(m.provider)
		}

	case "ctrl+h":
		m.toggleAlbumHeadersManual()
		m.adjustScroll()

	case "ctrl+g":
		m.toggleHelpBar()
		m.adjustScroll()

	case "ctrl+b":
		m.toggleSettingsPane()
		m.adjustScroll()

	case "v":
		if m.simplified {
			break
		}
		_ = m.cycleVisualizer()

	case "ctrl+v":
		if m.simplified {
			break
		}
		m.openVisPicker()

	case "V":
		if m.simplified {
			break
		}
		m.fullVis = !m.fullVis
		m.recomputeLayout()

	case "ctrl+x":
		if !m.simplified && m.focus == focusPlaylist {
			m.toggleExpandedView()
		}

	case "x":
		if m.focus == focusPlaylist {
			var cmd tea.Cmd
			m.keepPlCursorRow(func() { cmd, _ = m.removeTrack(m.plCursor, true) })
			return cmd
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

// handleInfoKey processes key presses while the track info overlay is open.
func (m *Model) handleInfoKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "i", "q":
		m.info.visible = false
	case "ctrl+i":
		m.info.visible = false
		m.toggleMetadata()
	case "up", "k":
		if m.info.scroll > 0 {
			m.info.scroll--
		}
	case "down", "j":
		m.info.scroll++
		m.infoMaybeAdjustScroll()
	}
	return nil
}

// handleLyricsKey processes key presses while the lyrics overlay is open.
func (m *Model) handleLyricsKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "y", "q":
		nextRequest(&m.requests.lyrics)
		m.lyrics.loading = false
		m.lyrics.query = ""
		m.lyrics.visible = false
	case "r":
		return m.retryLyrics()
	case "[":
		if m.lyricsSyncable() && m.lyricsHaveTimestamps() {
			return m.nudgeLyricsOffset(-250 * time.Millisecond)
		}
	case "]":
		if m.lyricsSyncable() && m.lyricsHaveTimestamps() {
			return m.nudgeLyricsOffset(250 * time.Millisecond)
		}
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

func (m *Model) exitFullVisualizer() {
	m.fullVis = false
	m.recomputeLayout()
}

func (m *Model) handleFullVisualizerKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "backspace", "b", "V", "q":
		m.exitFullVisualizer()
	case "space":
		return m.togglePlayPause()
	case ">", ".":
		return m.skipNext()
	case "<", ",":
		return m.skipPrev()
	case "left":
		return m.doSeek(-5 * time.Second)
	case "shift+left":
		return m.doSeek(-m.seekStepLarge)
	case "right":
		return m.doSeek(5 * time.Second)
	case "shift+right":
		return m.doSeek(m.seekStepLarge)
	case "+", "=":
		m.adjustVolume(1)
	case "-":
		m.adjustVolume(-1)
	case "v":
		_ = m.cycleVisualizer()
	case "t":
		// Hide the episode name so the full-screen visualizer can be put on a
		// shared screen without naming what is playing.
		m.hideTrackInfo = !m.hideTrackInfo
	case "?":
		m.exitFullVisualizer()
		m.openKeymap()

	default:
		// Plugin bindings are global: a pomodoro or sleep-timer key is about
		// the session, not about which screen happens to be open. Without
		// this, every plugin key is dead in the full-screen visualizer —
		// which is exactly where a plugin visualizer is being watched.
		if m.luaMgr != nil {
			m.luaMgr.EmitKey(msg.String())
		}
	}
	return nil
}

// saveTrack saves the current track in the configured downloads directory.
// tracksave.SaveTo runs in a tea.Cmd, so neither a yt-dlp download nor a file
// copy blocks the Update goroutine. IPC save uses the same routine.
func (m *Model) saveTrack() tea.Cmd {
	track, idx := m.currentPlaybackTrack()
	if idx < 0 {
		m.status.Warning("Nothing to save", statusTTLShort)
		return nil
	}
	download := tracksave.NeedsDownload(track)
	if download {
		m.status.Clear()
		m.save.startDownload()
	}
	directory := m.downloadsDirectory
	return func() tea.Msg {
		path, err := tracksave.SaveTo(context.Background(), track, directory)
		return trackSavedMsg{path: path, err: err, download: download}
	}
}

func (m *Model) openJumpMode() {
	m.jump = jumpState{active: true}
}

func (m *Model) closeJumpMode() {
	m.jump = jumpState{}
}

// handleJumpKey processes key presses while in jump-time mode.
func (m *Model) handleJumpKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.closeJumpMode()
		return nil
	case tea.KeyEnter:
		target, err := parseJumpTarget(m.jump.input)
		if err != nil {
			m.jump.err = "Invalid jump: " + err.Error()
			m.status.Warning(m.jump.err, statusTTLDefault)
			return nil
		}
		if !m.player.Seekable() {
			m.jump.err = "This track cannot be seeked."
			m.status.Warning(m.jump.err, statusTTLDefault)
			return nil
		}
		if dur := m.player.Duration(); dur > 0 && target > dur {
			m.jump.err = fmt.Sprintf("Jump exceeds track duration (%s).", formatJumpClock(dur))
			m.status.Warning(m.jump.err, statusTTLDefault)
			return nil
		}
		cmd, err := m.trySeekAbsolute(target)
		if err != nil {
			m.jump.err = "Seek failed: " + err.Error()
			m.status.Warning(m.jump.err, statusTTLDefault)
			return nil
		}
		m.closeJumpMode()
		return cmd
	}

	if m.editText("jump", &m.jump.input, msg) {
		m.jump.err = ""
	}
	return nil
}

// toggleExpandedView toggles the UI between default and expanded height.
func (m *Model) toggleExpandedView() {
	m.heightExpanded = !m.heightExpanded
	m.applyHeightMode()
	m.adjustScroll()
}

// handlePaste sends pasted text to the text field of the top overlay, as
// handleKey sends keys. With no overlay open, the provider filter takes it
// while the provider pane has the focus.
func (m *Model) handlePaste(content string) tea.Cmd {
	if content == "" {
		return nil
	}
	if spec, ok := m.topOverlay(); ok {
		if spec.paste != nil {
			spec.paste(m, content)
		}
		return nil
	}

	if m.provSearch.active && m.focus == focusProvider {
		m.insertText("provider-search", &m.provSearch.query, content)
		if _, ok := m.provider.(provider.CatalogSearcher); !ok {
			m.updateProvSearch()
		}
		return nil
	}

	return nil
}

// handleURLInputKey processes key presses while in URL input mode.
func (m *Model) handleURLInputKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.urlInput.active = false
	case tea.KeyEnter:
		m.urlInput.active = false
		input := strings.TrimSpace(m.urlInput.input)
		if input != "" {
			m.feedLoading = true
			m.status.Activity("Loading URL...", statusTTLLong)
			return resolveURLCmd(input, true)
		}
		m.urlInput.active = true
		m.urlInput.err = "Enter a stream, track, or playlist URL."
	default:
		if m.editText("url", &m.urlInput.input, msg) {
			m.urlInput.err = ""
		}
	}
	return nil
}
