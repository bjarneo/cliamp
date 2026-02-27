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
)

// handleKey processes a single key press and returns an optional command.
func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	// Dismiss keymap overlay on any key
	if m.showKeymap {
		m.showKeymap = false
		return nil
	}

	// Theme picker overlay — interactive navigation
	if m.showThemes {
		return m.handleThemeKey(msg)
	}

	if m.searching {
		return m.handleSearchKey(msg)
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
		}
		return nil
	}

	switch msg.String() {
	case "q", "ctrl+c":
		m.player.Close()
		m.quitting = true
		return tea.Quit
	case "esc", "backspace", "b":
		if m.focus == focusPlaylist && m.provider != nil {
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
		cmd := m.nextTrack()
		m.notifyMPRIS()
		return cmd

	case "<", ",":
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
		}

	case "right":
		if m.focus == focusEQ {
			if m.eqCursor < 9 {
				m.eqCursor++
			}
		} else {
			m.player.Seek(5 * time.Second)
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
		if m.focus == focusEQ && m.eqCursor < 9 {
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

	case "S":
		m.saveTrack()

	case "m":
		m.player.ToggleMono()

	case "/":
		m.searching = true
		m.searchQuery = ""
		m.searchResults = nil
		m.searchCursor = 0
		m.prevFocus = m.focus
		m.focus = focusSearch

	case "t":
		m.openThemePicker()

	case "v":
		m.vis.CycleMode()

	case "ctrl+k":
		m.showKeymap = true
	}

	return nil
}

// saveTrack copies the current track to ~/Music/cliamp/ with a clean filename.
// Only works for downloaded yt-dlp tracks (temp files).
func (m *Model) saveTrack() {
	track, idx := m.playlist.Current()
	if idx < 0 {
		m.saveMsg = "Nothing to save"
		m.saveMsgTTL = 40 // ~2s at 50ms ticks
		return
	}

	// Only save local temp files (yt-dlp downloads), not streams or user's own files.
	if track.Stream || !strings.HasPrefix(track.Path, os.TempDir()) {
		m.saveMsg = "Only downloaded tracks can be saved"
		m.saveMsgTTL = 40
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		m.saveMsg = fmt.Sprintf("Save failed: %s", err)
		m.saveMsgTTL = 40
		return
	}

	saveDir := filepath.Join(home, "Music", "cliamp")
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		m.saveMsg = fmt.Sprintf("Save failed: %s", err)
		m.saveMsgTTL = 40
		return
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
		return
	}

	m.saveMsg = fmt.Sprintf("Saved to ~/Music/cliamp/%s", name+ext)
	m.saveMsgTTL = 60 // ~3s
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
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// handleSearchKey processes key presses while in search mode.
func (m *Model) handleSearchKey(msg tea.KeyMsg) tea.Cmd {
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

	default:
		if msg.Type == tea.KeyRunes {
			m.searchQuery += string(msg.Runes)
			m.updateSearch()
		}
	}

	return nil
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
