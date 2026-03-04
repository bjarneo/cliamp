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

	"cliamp/config"
	"cliamp/external/navidrome"
	"cliamp/playlist"
)

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
			m.showInfo = false
			m.player.Close()
			m.quitting = true
			return tea.Quit
		case "esc", "i":
			m.showInfo = false
		}
		return nil
	}

	if m.searching {
		return m.handleSearchKey(msg)
	}

	if m.netSearching {
		return m.handleNetSearchKey(msg)
	}

	if m.focus == focusProvider {
		switch msg.String() {
		case "q", "ctrl+c":
			m.player.Close()
			m.quitting = true
			return tea.Quit
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
			if len(m.providerLists) > 0 && !m.provLoading {
				m.provLoading = true
				return fetchTracksCmd(m.provider, m.providerLists[m.provCursor].ID)
			}
		case "tab":
			if m.playlist.Len() > 0 {
				m.focus = focusPlaylist
			}
		case "o":
			m.openFileBrowser()
		case "N":
			if m.navClient != nil {
				m.openNavBrowser()
			}
		}
		return nil
	}

	switch msg.String() {
	case "q", "ctrl+c":
		m.player.Close()
		m.quitting = true
		return tea.Quit
	case "esc", "backspace", "b":
		if m.fullVis {
			m.fullVis = false
			m.vis.Rows = defaultVisRows
		} else if m.focus == focusPlaylist && m.provider != nil {
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
		// Scrobble the current track if ≥50% has been played.
		if track, _ := m.playlist.Current(); track.NavidromeID != "" {
			m.maybeScrobble(track, m.player.Position(), m.player.Duration())
		}
		cmd := m.nextTrack()
		m.notifyMPRIS()
		return cmd

	case "<", ",":
		// Scrobble the current track if ≥50% has been played.
		if track, _ := m.playlist.Current(); track.NavidromeID != "" {
			m.maybeScrobble(track, m.player.Position(), m.player.Duration())
		}
		cmd := m.prevTrack()
		m.notifyMPRIS()
		return cmd

	case "left":
		if m.focus == focusEQ {
			if m.eqCursor > 0 {
				m.eqCursor--
			}
		} else {
			m.player.Seek(-5 * time.Second)
			if m.mpris != nil {
				m.mpris.EmitSeeked(m.player.Position().Microseconds())
			}
		}

	case "right":
		if m.focus == focusEQ {
			if m.eqCursor < numBands-1 {
				m.eqCursor++
			}
		} else {
			m.player.Seek(5 * time.Second)
			if m.mpris != nil {
				m.mpris.EmitSeeked(m.player.Position().Microseconds())
			}
		}

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
			// Scrobble the track being replaced if ≥50% was heard.
			if track, _ := m.playlist.Current(); track.NavidromeID != "" {
				m.maybeScrobble(track, m.player.Position(), m.player.Duration())
			}
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
		m.player.ClearPreload()
		return m.preloadNext()

	case "z":
		m.playlist.ToggleShuffle()
		m.player.ClearPreload()
		return m.preloadNext()

	case "tab":
		if m.focus == focusPlaylist {
			m.focus = focusEQ
		} else {
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

	case "f":
		m.netSearching = true
		m.netSearchQuery = ""
		m.prevFocus = m.focus
		m.focus = focusNetSearch

	case "p":
		if m.localProvider != nil {
			m.openPlaylistManager()
		}

	case "t":
		m.openThemePicker()

	case "i":
		m.showInfo = true

	case "o":
		m.openFileBrowser()

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
			if m.plVisible == 5 {
				m.plVisible = 20
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

	// yt-dlp tracks: async download directly to ~/Music/cliamp/.
	if playlist.IsYTDL(track.Path) {
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

// handleSearchKey processes key presses while in search mode.
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
		if len(m.searchQuery) > 0 {
			_, size := utf8.DecodeLastRuneInString(m.searchQuery)
			m.searchQuery = m.searchQuery[:len(m.searchQuery)-size]
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
			m.saveMsg = "Queuing search..."
			m.saveMsgTTL = 40
			cmd = fetchNetSearchCmd("ytsearch1:" + strings.TrimSpace(m.netSearchQuery))
		}
		return cmd

	case tea.KeyBackspace:
		if len(m.netSearchQuery) > 0 {
			_, size := utf8.DecodeLastRuneInString(m.netSearchQuery)
			m.netSearchQuery = m.netSearchQuery[:len(m.netSearchQuery)-size]
		}

	case tea.KeySpace:
		m.netSearchQuery += " "

	default:
		if msg.Type == tea.KeyRunes {
			m.netSearchQuery += string(msg.Runes)
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
		m.player.Close()
		m.quitting = true
		return tea.Quit
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
		m.player.Close()
		m.quitting = true
		return tea.Quit
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
		if len(m.plMgrNewName) > 0 {
			_, size := utf8.DecodeLastRuneInString(m.plMgrNewName)
			m.plMgrNewName = m.plMgrNewName[:len(m.plMgrNewName)-size]
		}
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
		m.player.Close()
		m.quitting = true
		return tea.Quit
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
		m.player.Close()
		m.quitting = true
		return tea.Quit
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
	{"+ -", "Volume up/down"},
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
	{"p", "Playlist manager"},
	{"i", "Track info / metadata"},
	{"S", "Save/download track to ~/Music"},
	{"r", "Cycle repeat"},
	{"z", "Toggle shuffle"},
	{"x", "Expand/collapse playlist"},
	{"/", "Search playlist"},
	{"f", "Find online (queue play next)"},
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
		if len(m.keymapSearch) > 0 {
			_, size := utf8.DecodeLastRuneInString(m.keymapSearch)
			m.keymapSearch = m.keymapSearch[:len(m.keymapSearch)-size]
			m.updateKeymapFilter()
		}
	case tea.KeySpace:
		m.keymapSearch += " "
		m.updateKeymapFilter()
	default:
		switch msg.String() {
		case "ctrl+c":
			m.showKeymap = false
			m.player.Close()
			m.quitting = true
			return tea.Quit
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

// handleNavBrowserKey processes key presses while the Navidrome browser is open.
func (m *Model) handleNavBrowserKey(msg tea.KeyMsg) tea.Cmd {
	navClient := m.navClient
	if navClient == nil {
		m.showNavBrowser = false
		return nil
	}

	// Search bar: active on any list/track screen (not the mode menu).
	if m.navMode != navBrowseModeMenu {
		if m.navSearching {
			return m.handleNavSearchKey(msg)
		}
		if msg.String() == "/" {
			// Toggle: if already filtered, clear; otherwise open.
			if m.navSearch != "" {
				m.navClearSearch()
			} else {
				m.navSearching = true
			}
			return nil
		}
	}

	switch m.navMode {
	case navBrowseModeMenu:
		return m.handleNavMenuKey(msg, navClient)
	case navBrowseModeByAlbum:
		return m.handleNavByAlbumKey(msg, navClient)
	case navBrowseModeByArtist:
		return m.handleNavByArtistKey(msg, navClient)
	case navBrowseModeByArtistAlbum:
		return m.handleNavByArtistAlbumKey(msg, navClient)
	}
	return nil
}

func (m *Model) handleNavMenuKey(msg tea.KeyMsg, navClient *navidrome.NavidromeClient) tea.Cmd {
	const menuItems = 3
	switch msg.String() {
	case "ctrl+c":
		m.showNavBrowser = false
		m.player.Close()
		m.quitting = true
		return tea.Quit
	case "up", "k":
		if m.navCursor > 0 {
			m.navCursor--
		}
	case "down", "j":
		if m.navCursor < menuItems-1 {
			m.navCursor++
		}
	case "enter", "l", "right":
		switch m.navCursor {
		case 0: // By Album
			m.navMode = navBrowseModeByAlbum
			m.navScreen = navBrowseScreenList
			m.navCursor = 0
			m.navScroll = 0
			m.navAlbums = nil
			m.navAlbumLoading = true
			m.navAlbumDone = false
			m.navLoading = false
			return fetchNavAlbumListCmd(navClient, m.navSortType, 0)
		case 1: // By Artist
			m.navMode = navBrowseModeByArtist
			m.navScreen = navBrowseScreenList
			m.navCursor = 0
			m.navScroll = 0
			m.navArtists = nil
			m.navLoading = true
			return fetchNavArtistsCmd(navClient)
		case 2: // By Artist / Album
			m.navMode = navBrowseModeByArtistAlbum
			m.navScreen = navBrowseScreenList
			m.navCursor = 0
			m.navScroll = 0
			m.navArtists = nil
			m.navLoading = true
			return fetchNavArtistsCmd(navClient)
		}
	case "esc", "N", "backspace", "b":
		m.showNavBrowser = false
	}
	return nil
}

func (m *Model) handleNavByAlbumKey(msg tea.KeyMsg, navClient *navidrome.NavidromeClient) tea.Cmd {
	switch m.navScreen {
	case navBrowseScreenList:
		return m.handleNavAlbumListKey(msg, navClient, false)
	case navBrowseScreenTracks:
		return m.handleNavTrackListKey(msg)
	}
	return nil
}

func (m *Model) handleNavByArtistKey(msg tea.KeyMsg, navClient *navidrome.NavidromeClient) tea.Cmd {
	switch m.navScreen {
	case navBrowseScreenList:
		return m.handleNavArtistListKey(msg, navClient)
	case navBrowseScreenTracks:
		return m.handleNavTrackListKey(msg)
	}
	return nil
}

func (m *Model) handleNavByArtistAlbumKey(msg tea.KeyMsg, navClient *navidrome.NavidromeClient) tea.Cmd {
	switch m.navScreen {
	case navBrowseScreenList:
		return m.handleNavArtistListKey(msg, navClient)
	case navBrowseScreenAlbums:
		return m.handleNavAlbumListKey(msg, navClient, true)
	case navBrowseScreenTracks:
		return m.handleNavTrackListKey(msg)
	}
	return nil
}

// handleNavArtistListKey handles the artist list screen (used by both By Artist and By Artist/Album modes).
func (m *Model) handleNavArtistListKey(msg tea.KeyMsg, navClient *navidrome.NavidromeClient) tea.Cmd {
	// Determine effective list length (filtered or full).
	listLen := len(m.navArtists)
	if len(m.navSearchIdx) > 0 {
		listLen = len(m.navSearchIdx)
	}

	switch msg.String() {
	case "ctrl+c":
		m.showNavBrowser = false
		m.player.Close()
		m.quitting = true
		return tea.Quit
	case "up", "k":
		if m.navCursor > 0 {
			m.navCursor--
			m.navMaybeAdjustScroll()
		}
	case "down", "j":
		if m.navCursor < listLen-1 {
			m.navCursor++
			m.navMaybeAdjustScroll()
		}
	case "enter", "l", "right":
		if m.navLoading || len(m.navArtists) == 0 {
			return nil
		}
		// Resolve raw index (filtered or direct).
		rawIdx := m.navCursor
		if len(m.navSearchIdx) > 0 && m.navCursor < len(m.navSearchIdx) {
			rawIdx = m.navSearchIdx[m.navCursor]
		}
		artist := m.navArtists[rawIdx]
		m.navSelArtist = artist
		m.navLoading = true
		if m.navMode == navBrowseModeByArtistAlbum {
			// Drill into album list for this artist.
			m.navAlbums = nil
			m.navAlbumLoading = false
			m.navScreen = navBrowseScreenAlbums
			m.navCursor = 0
			m.navScroll = 0
			m.navClearSearch()
			return fetchNavArtistAlbumsCmd(navClient, artist.ID)
		}
		// By Artist: fetch all albums first, then all tracks via a two-step command.
		// We use a dedicated command that fetches albums then tracks in one shot.
		return m.fetchNavArtistAllTracksCmd(navClient, artist.ID)
	case "esc", "h", "left", "backspace":
		// Back to menu.
		m.navClearSearch()
		m.navMode = navBrowseModeMenu
		m.navScreen = navBrowseScreenList
	}
	return nil
}

// handleNavAlbumListKey handles the album list screen.
// artistAlbums=true means this is the artist's album sub-screen (ArtistAlbum mode), not the global list.
func (m *Model) handleNavAlbumListKey(msg tea.KeyMsg, navClient *navidrome.NavidromeClient, artistAlbums bool) tea.Cmd {
	// Determine effective list length (filtered or full).
	listLen := len(m.navAlbums)
	if len(m.navSearchIdx) > 0 {
		listLen = len(m.navSearchIdx)
	}

	switch msg.String() {
	case "ctrl+c":
		m.showNavBrowser = false
		m.player.Close()
		m.quitting = true
		return tea.Quit
	case "up", "k":
		if m.navCursor > 0 {
			m.navCursor--
			m.navMaybeAdjustScroll()
		}
	case "down", "j":
		if m.navCursor < listLen-1 {
			m.navCursor++
			m.navMaybeAdjustScroll()
			// Lazy-load next page: only trigger on the raw (unfiltered) list.
			if !artistAlbums && len(m.navSearchIdx) == 0 && !m.navAlbumLoading && !m.navAlbumDone && m.navCursor >= len(m.navAlbums)-10 {
				m.navAlbumLoading = true
				return fetchNavAlbumListCmd(navClient, m.navSortType, len(m.navAlbums))
			}
		}
	case "enter", "l", "right":
		if (m.navLoading && !artistAlbums) || len(m.navAlbums) == 0 {
			return nil
		}
		// Resolve raw index (filtered or direct).
		rawIdx := m.navCursor
		if len(m.navSearchIdx) > 0 && m.navCursor < len(m.navSearchIdx) {
			rawIdx = m.navSearchIdx[m.navCursor]
		}
		album := m.navAlbums[rawIdx]
		m.navSelAlbum = album
		m.navLoading = true
		m.navClearSearch()
		return fetchNavAlbumTracksCmd(navClient, album.ID)
	case "s":
		if artistAlbums {
			return nil // Sort only applies to global album list.
		}
		// Cycle to the next sort type.
		m.navSortType = navNextSort(m.navSortType)
		m.navAlbums = nil
		m.navCursor = 0
		m.navScroll = 0
		m.navAlbumLoading = true
		m.navAlbumDone = false
		m.navClearSearch()
		// Persist the new sort preference.
		if err := config.SaveNavidromeSort(m.navSortType); err != nil {
			m.saveMsg = fmt.Sprintf("Sort save failed: %s", err)
			m.saveMsgTTL = 60
		}
		return fetchNavAlbumListCmd(navClient, m.navSortType, 0)
	case "esc", "h", "left", "backspace":
		m.navClearSearch()
		if artistAlbums {
			// Back to artist list.
			m.navScreen = navBrowseScreenList
		} else {
			// Back to menu.
			m.navMode = navBrowseModeMenu
			m.navScreen = navBrowseScreenList
		}
	}
	return nil
}

// handleNavTrackListKey handles the final track-list screen (used by all modes).
func (m *Model) handleNavTrackListKey(msg tea.KeyMsg) tea.Cmd {
	// Determine effective list length (filtered or full).
	listLen := len(m.navTracks)
	if len(m.navSearchIdx) > 0 {
		listLen = len(m.navSearchIdx)
	}

	switch msg.String() {
	case "ctrl+c":
		m.showNavBrowser = false
		m.player.Close()
		m.quitting = true
		return tea.Quit
	case "up", "k":
		if m.navCursor > 0 {
			m.navCursor--
			m.navMaybeAdjustScroll()
		}
	case "down", "j":
		if m.navCursor < listLen-1 {
			m.navCursor++
			m.navMaybeAdjustScroll()
		}
	case "enter":
		// Play the selected track immediately, then enqueue everything from that
		// position to the end of the list (capped at 500 total tracks added).
		if len(m.navTracks) == 0 {
			return nil
		}
		rawIdx := m.navCursor
		if len(m.navSearchIdx) > 0 && m.navCursor < len(m.navSearchIdx) {
			rawIdx = m.navSearchIdx[m.navCursor]
		}
		if rawIdx < len(m.navTracks) {
			const maxAdd = 500
			m.player.Stop()
			m.player.ClearPreload()

			// Build the slice of tracks to add: from rawIdx to end (or 500 max).
			var toAdd []playlist.Track
			if len(m.navSearchIdx) > 0 {
				// Filtered: use positions from navCursor onward in the filtered list.
				for j := m.navCursor; j < len(m.navSearchIdx) && len(toAdd) < maxAdd; j++ {
					toAdd = append(toAdd, m.navTracks[m.navSearchIdx[j]])
				}
			} else {
				for i := rawIdx; i < len(m.navTracks) && len(toAdd) < maxAdd; i++ {
					toAdd = append(toAdd, m.navTracks[i])
				}
			}

			m.playlist.Add(toAdd...)
			newIdx := m.playlist.Len() - len(toAdd)
			m.playlist.SetIndex(newIdx)
			m.plCursor = newIdx
			m.adjustScroll()
			if len(toAdd) > 1 {
				m.saveMsg = fmt.Sprintf("Playing: %s (+%d queued)", toAdd[0].DisplayName(), len(toAdd)-1)
			} else {
				m.saveMsg = fmt.Sprintf("Playing: %s", toAdd[0].DisplayName())
			}
			m.saveMsgTTL = 80
			cmd := m.playCurrentTrack()
			m.notifyMPRIS()
			return cmd
		}
	case "R":
		// Replace playlist with all displayed tracks and close browser.
		tracks := m.navTracks
		if len(m.navSearchIdx) > 0 {
			// Replace with only the filtered subset.
			filtered := make([]playlist.Track, 0, len(m.navSearchIdx))
			for _, i := range m.navSearchIdx {
				filtered = append(filtered, m.navTracks[i])
			}
			tracks = filtered
		}
		if len(tracks) > 0 {
			m.player.Stop()
			m.player.ClearPreload()
			m.playlist.Replace(tracks)
			m.plCursor = 0
			m.plScroll = 0
			m.playlist.SetIndex(0)
			m.focus = focusPlaylist
			m.showNavBrowser = false
			cmd := m.playCurrentTrack()
			m.notifyMPRIS()
			return cmd
		}
	case "a":
		// Append all displayed tracks to the playlist (keep current playback).
		tracks := m.navTracks
		if len(m.navSearchIdx) > 0 {
			filtered := make([]playlist.Track, 0, len(m.navSearchIdx))
			for _, i := range m.navSearchIdx {
				filtered = append(filtered, m.navTracks[i])
			}
			tracks = filtered
		}
		if len(tracks) > 0 {
			wasEmpty := m.playlist.Len() == 0
			m.playlist.Add(tracks...)
			m.saveMsg = fmt.Sprintf("Added %d tracks", len(tracks))
			m.saveMsgTTL = 80
			if wasEmpty || !m.player.IsPlaying() {
				m.playlist.SetIndex(0)
				cmd := m.playCurrentTrack()
				m.notifyMPRIS()
				return cmd
			}
		}
	case "q":
		// Add selected track to playlist and queue it to play next.
		if len(m.navTracks) == 0 {
			return nil
		}
		rawIdx := m.navCursor
		if len(m.navSearchIdx) > 0 && m.navCursor < len(m.navSearchIdx) {
			rawIdx = m.navSearchIdx[m.navCursor]
		}
		if rawIdx < len(m.navTracks) {
			t := m.navTracks[rawIdx]
			m.playlist.Add(t)
			newIdx := m.playlist.Len() - 1
			m.playlist.Queue(newIdx)
			m.saveMsg = fmt.Sprintf("Queued: %s", t.DisplayName())
			m.saveMsgTTL = 80
		}
	case "esc", "h", "left", "backspace":
		// Navigate back one level depending on the mode and how we got here.
		m.navClearSearch()
		m.navCursor = 0
		m.navScroll = 0
		switch m.navMode {
		case navBrowseModeByAlbum:
			m.navScreen = navBrowseScreenList
		case navBrowseModeByArtist:
			m.navScreen = navBrowseScreenList
		case navBrowseModeByArtistAlbum:
			m.navScreen = navBrowseScreenAlbums
		}
	}
	return nil
}

// handleNavSearchKey handles key input while the nav search bar is open.
func (m *Model) handleNavSearchKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEscape:
		// Close the search bar; keep the filter active so the user can act on results.
		m.navSearching = false
		return nil
	case tea.KeyEnter:
		m.navSearching = false
		return nil
	case tea.KeyBackspace, tea.KeyDelete:
		if len(m.navSearch) > 0 {
			_, size := utf8.DecodeLastRuneInString(m.navSearch)
			m.navSearch = m.navSearch[:len(m.navSearch)-size]
			m.navCursor = 0
			m.navScroll = 0
			m.navUpdateSearch()
		}
		return nil
	}
	// Printable character — append to query.
	if msg.Type == tea.KeyRunes {
		m.navSearch += string(msg.Runes)
		m.navCursor = 0
		m.navScroll = 0
		m.navUpdateSearch()
	}
	return nil
}

// navNextSort returns the sort type that follows s in SortTypes, wrapping around.
func navNextSort(s string) string {
	for i, t := range navidrome.SortTypes {
		if t == s {
			return navidrome.SortTypes[(i+1)%len(navidrome.SortTypes)]
		}
	}
	return navidrome.SortTypes[0]
}

// navMaybeAdjustScroll keeps navCursor visible within the rendered list window.
func (m *Model) navMaybeAdjustScroll() {
	visible := m.plVisible
	if visible < 5 {
		visible = 5
	}
	if m.navCursor < m.navScroll {
		m.navScroll = m.navCursor
	}
	if m.navCursor >= m.navScroll+visible {
		m.navScroll = m.navCursor - visible + 1
	}
}
