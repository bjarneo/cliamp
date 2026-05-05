package model

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"cliamp/ui"
)

// keymapEntry is a row in the Ctrl+K overlay. Rows with `divider = true` are
// unselectable section headers (e.g. "— plugins —").
type keymapEntry struct {
	key, action string
	divider     bool
}

// keymapEntries is the full list of keybindings shown in the keymap overlay.
var keymapEntries = []keymapEntry{
	{key: "Space", action: "Play / Pause"},
	{key: "s", action: "Stop"},
	{key: "> .", action: "Next track"},
	{key: "< ,", action: "Previous track"},
	{key: "← →", action: "Seek ±5s"},
	{key: "Shift+← →", action: "Seek ±large step"},
	{key: "Nj", action: "Seek to N×10% of track (e.g. 7j = 70%)"},
	{key: "+ -", action: "Volume up/down"},
	{key: "] [", action: "Speed up/down (±0.25x)"},
	{key: "z", action: "Toggle shuffle"},
	{key: "r", action: "Cycle repeat"},
	{key: "m", action: "Toggle mono"},
	{key: "e", action: "Cycle EQ preset"},
	{key: "t", action: "Choose theme"},
	{key: "v", action: "Cycle visualizer"},
	{key: "V", action: "Full-screen visualizer"},
	{key: "↑ ↓", action: "Playlist scroll / EQ adjust (wraps around)"},
	{key: "PgUp PgDn / Ctrl+U D", action: "Scroll playlist/browser by page"},
	{key: "Home End / g G", action: "Go to top/end of playlist/browser"},
	{key: "Shift+↑ ↓", action: "Move track up/down"},
	{key: "h l", action: "EQ cursor left/right"},
	{key: "Enter", action: "Play selected track"},
	{key: "a", action: "Toggle queue (play next)"},
	{key: "A", action: "Queue manager"},
	{key: "o", action: "Open file browser"},
	{key: "N", action: "Navidrome browser"},
	{key: "L", action: "Browse local playlists"},
	{key: "R", action: "Open radio provider"},
	{key: "S", action: "Open Spotify provider"},
	{key: "P", action: "Open Plex provider"},
	{key: "Y", action: "Open YouTube provider"},
	{key: "C", action: "Open SoundCloud provider"},
	{key: "J", action: "Open Jellyfin provider"},
	{key: "E", action: "Open Emby provider"},
	{key: "Ctrl+J", action: "Jump to time"},
	{key: "p", action: "Playlist manager"},
	{key: "i", action: "Track info / metadata"},
	{key: "Ctrl+S", action: "Save/download track to ~/Music"},
	{key: "Ctrl+X", action: "Expand/collapse view"},
	{key: "/", action: "Filter/search list"},
	{key: "f", action: "Toggle bookmark ★ (or favorite station in radio)"},
	{key: "Ctrl+F", action: "Search (active provider or YouTube)"},
	{key: "u", action: "Load URL (stream/playlist)"},
	{key: "d", action: "Audio device picker"},
	{key: "y", action: "Show lyrics"},
	{key: "Tab", action: "Toggle focus"},
	{key: "Esc", action: "Back to provider"},
	{key: "Ctrl+K", action: "This keymap"},
	{key: "q", action: "Quit"},
}

// coreReservedKeys is the set of keys owned by cliamp's global UI handler.
// Plugins are refused registration for any key in this set. Kept as a plain
// slice so it's obvious at a glance which strings belong here; every entry
// is in Bubbletea's `msg.String()` form (lowercase, ctrl+ prefix, etc.).
//
// This must be kept in sync with handleKey() in keys.go. If you add or remove
// a case there, update this list — the TestReservedKeys test in keymap_test.go
// guards against drift.
var coreReservedKeys = []string{
	// Global quit / escape.
	"q", "ctrl+c", "esc", "backspace", "b",

	// Playback.
	"space", "s", ">", ".", "<", ",",
	"left", "right", "shift+left", "shift+right",
	"+", "=", "-", "]", "[",
	"f",

	// Percentage seek primes on digits 0-9 and consumes the following `j`.
	"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "j",

	// Navigation and focus.
	"up", "k", "down",
	"shift+up", "shift+down",
	"pgup", "pgdown", "ctrl+u", "ctrl+d",
	"g", "G", "home", "end",
	"enter", "tab", "h", "l",

	// Features.
	"r", "z", "m", "e", "a", "A",
	"ctrl+s", "S", "/", "ctrl+f",
	"ctrl+j", "J", "E", "p", "t", "i", "y", "o", "u",
	"N", "L", "R", "P", "Y", "C",
	"v", "V", "ctrl+x", "d", "ctrl+k",
	"ctrl+r",
}

// ReservedKeys returns a fresh copy of the core-reserved key set. Handed to
// the Lua plugin manager at startup so it can reject conflicting plugin binds.
func ReservedKeys() map[string]bool {
	out := make(map[string]bool, len(coreReservedKeys))
	for _, k := range coreReservedKeys {
		out[k] = true
	}
	return out
}

// buildKeymapEntries returns the core keybindings plus any plugin-registered
// binds that supplied a description. Plugins appear under a divider row.
// Only called when the overlay is opened; the result is cached on keymap.entries
// so navigation (which calls keymapCount many times per frame) is allocation-free.
func (m Model) buildKeymapEntries() []keymapEntry {
	out := make([]keymapEntry, 0, len(keymapEntries)+4)
	out = append(out, keymapEntries...)
	if m.luaMgr == nil {
		return out
	}
	binds := m.luaMgr.KeyBindings()
	if len(binds) == 0 {
		return out
	}
	out = append(out, keymapEntry{action: "— plugins —", divider: true})
	for _, b := range binds {
		label := b.Description
		if b.Plugin != "" {
			label += "  (" + b.Plugin + ")"
		}
		out = append(out, keymapEntry{key: b.Key, action: label})
	}
	return out
}

func (m *Model) keymapCount() int {
	if m.keymap.searching || m.keymap.search != "" {
		return len(m.keymap.filtered)
	}
	return len(m.keymap.entries)
}

func (m *Model) keymapHelpLine() string {
	if m.keymap.searching {
		return helpKey("Enter", "Confirm ") + helpKey("Esc", "Cancel ") + helpKey("Type", "Filter")
	}
	return helpKey("↓↑", "Scroll ") + helpKey("PgUp/Dn", "Page ") +
		helpKey("Home/End", "Jump ") + helpKey("/", "Filter ") + helpKey("Esc", "Close")
}

func (m *Model) keymapHeaderLines() []string {
	header := []string{
		titleStyle.Render("K E Y M A P"),
		"",
	}
	if m.keymap.searching {
		cursor := "_"
		if (time.Now().UnixNano()/500000000)%2 != 0 {
			cursor = " "
		}
		header = append(header, playlistSelectedStyle.Render("  / "+m.keymap.search+cursor), "")
	} else if m.keymap.search != "" {
		header = append(header, dimStyle.Render("  / "+m.keymap.search), "")
	}
	return header
}

func (m *Model) keymapVisible() int {
	probeSections := append([]string{}, m.keymapHeaderLines()...)

	// 1-line list placeholder.
	probeSections = append(probeSections, "x", "")

	// Footer area must mirror renderKeymapOverlay().
	probeSections = append(probeSections,
		dimStyle.Render("  0/0 keys"),
		"",
		m.keymapHelpLine(),
	)

	probeFrame := ui.FrameStyle.Render(strings.Join(probeSections, "\n"))
	fixedHeight := lipgloss.Height(probeFrame) - 1

	limit := maxPlVisible
	if m.heightExpanded {
		limit = m.height
	}
	return max(3, min(limit, m.height-fixedHeight))
}

// keymapMaybeAdjustScroll keeps the cursor visible in the current keymap window.
func (m *Model) keymapMaybeAdjustScroll(visible int) {
	if visible <= 0 {
		return
	}
	count := m.keymapCount()
	if m.keymap.cursor < 0 {
		m.keymap.cursor = 0
	}
	if m.keymap.cursor >= count && count > 0 {
		m.keymap.cursor = count - 1
	}

	if m.keymap.cursor < m.keymap.scroll {
		m.keymap.scroll = m.keymap.cursor
	} else if m.keymap.cursor >= m.keymap.scroll+visible {
		m.keymap.scroll = m.keymap.cursor - visible + 1
	}

	if m.keymap.scroll+visible > count && count > 0 {
		m.keymap.scroll = max(0, count-visible)
	}
}

// openKeymap resets the keymap state and shows it. Snapshots plugin bindings
// once so the render/navigation code doesn't re-query the plugin manager.
func (m *Model) openKeymap() {
	m.keymap.searching = false
	m.keymap.search = ""
	m.keymap.filtered = nil
	m.keymap.cursor = 0
	m.keymap.scroll = 0
	m.keymap.entries = m.buildKeymapEntries()
	m.keymap.visible = true
}

func (m *Model) handleKeymapSearchKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.keymap.visible = false
		return m.quit()
	case "esc":
		m.keymap.searching = false
		m.keymap.search = ""
		m.keymap.filtered = nil
		m.keymap.cursor = m.keymap.savedCursor
		m.keymap.scroll = m.keymap.savedScroll
		return nil
	case "enter":
		m.keymap.searching = false
		if m.keymap.search == "" {
			m.keymap.cursor = m.keymap.savedCursor
			m.keymap.scroll = m.keymap.savedScroll
		}
		return nil
	case "down":
		m.keymap.searching = false
		if m.keymapCount() > 0 {
			m.keymap.cursor = 0
			m.keymapMaybeAdjustScroll(m.keymapVisible())
		}
		return nil
	case "backspace":
		if m.keymap.search != "" {
			m.keymap.search = removeLastRune(m.keymap.search)
			m.updateKeymapFilter()
		} else {
			m.keymap.searching = false
			m.keymap.cursor = m.keymap.savedCursor
			m.keymap.scroll = m.keymap.savedScroll
		}
		return nil
	case "space":
		m.keymap.search += " "
		m.updateKeymapFilter()
		return nil
	}

	if len(msg.Text) > 0 {
		m.keymap.search += msg.Text
		m.updateKeymapFilter()
	}
	return nil
}

// handleKeymapKey processes key presses while the keymap overlay is open.
func (m *Model) handleKeymapKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.keymap.searching {
		return m.handleKeymapSearchKey(msg)
	}

	switch msg.String() {
	case "ctrl+c":
		m.keymap.visible = false
		return m.quit()

	case "esc", "ctrl+k", "q":
		m.keymap.visible = false
		m.keymap.searching = false
		m.keymap.search = ""
		m.keymap.filtered = nil

	case "/":
		m.keymap.savedCursor = m.keymap.cursor
		m.keymap.savedScroll = m.keymap.scroll
		m.keymap.searching = true
		m.keymap.search = ""
		m.updateKeymapFilter()
		return nil

	case "up", "k":
		if m.keymap.search != "" && m.keymap.cursor == 0 {
			m.keymap.searching = true
			return nil
		}
		count := m.keymapCount()
		if m.keymap.cursor > 0 {
			m.keymap.cursor--
		} else if count > 0 {
			m.keymap.cursor = count - 1
		}
		m.keymapMaybeAdjustScroll(m.keymapVisible())

	case "down", "j":
		count := m.keymapCount()
		if m.keymap.cursor < count-1 {
			m.keymap.cursor++
		} else if count > 0 {
			m.keymap.cursor = 0
		}
		m.keymapMaybeAdjustScroll(m.keymapVisible())

	case "ctrl+x":
		m.toggleExpandPlaylist()
		m.keymapMaybeAdjustScroll(m.keymapVisible())

	case "pgup", "ctrl+u":
		if m.keymap.cursor > 0 {
			visible := m.keymapVisible()
			m.keymap.cursor -= min(m.keymap.cursor, visible)
			m.keymapMaybeAdjustScroll(visible)
		}

	case "pgdown", "ctrl+d":
		count := m.keymapCount()
		if m.keymap.cursor < count-1 {
			visible := m.keymapVisible()
			m.keymap.cursor = min(count-1, m.keymap.cursor+visible)
			m.keymapMaybeAdjustScroll(visible)
		}

	case "home", "g":
		m.keymap.cursor = 0
		m.keymapMaybeAdjustScroll(m.keymapVisible())

	case "end", "G":
		count := m.keymapCount()
		if count > 0 {
			m.keymap.cursor = count - 1
		}
		m.keymapMaybeAdjustScroll(m.keymapVisible())

	case "backspace", "h":
		if m.keymap.search != "" {
			m.keymap.search = ""
			m.updateKeymapFilter()
		} else {
			m.keymap.visible = false
		}

	case "enter", "l":
		m.keymap.visible = false
	}

	return nil
}

// updateKeymapFilter rebuilds the filtered indices and clamps the cursor.
func (m *Model) updateKeymapFilter() {
	m.keymap.filtered = nil
	m.keymap.cursor = 0
	m.keymap.scroll = 0
	if m.keymap.search == "" {
		return
	}
	query := strings.ToLower(m.keymap.search)
	for i, e := range m.keymap.entries {
		if e.divider {
			continue
		}
		if strings.Contains(strings.ToLower(e.key), query) ||
			strings.Contains(strings.ToLower(e.action), query) {
			m.keymap.filtered = append(m.keymap.filtered, i)
		}
	}
}

// renderKeymapOverlay renders the keymap overlay.
func (m Model) renderKeymapOverlay() string {
	lines := append(make([]string, 0, 16), m.keymapHeaderLines()...)

	entries := m.keymap.entries
	var visible []keymapEntry
	if m.keymap.search != "" {
		for _, i := range m.keymap.filtered {
			visible = append(visible, entries[i])
		}
	} else {
		visible = entries
	}

	maxVisible := m.keymapVisible()
	rendered := 0

	if len(visible) == 0 {
		if m.keymap.search != "" {
			lines = append(lines, dimStyle.Render("  No matches"))
		} else {
			lines = append(lines, dimStyle.Render("  (empty)"))
		}
		rendered = 1
	} else {
		scroll := m.keymap.scroll
		for i := scroll; i < len(visible) && i < scroll+maxVisible; i++ {
			entry := visible[i]
			if entry.divider {
				lines = append(lines, dimStyle.Render("  "+entry.action))
				rendered++
				continue
			}
			line := fmt.Sprintf("%-10s %s", entry.key, entry.action)
			if m.keymap.searching {
				lines = append(lines, dimStyle.Render("  "+line))
			} else {
				lines = append(lines, cursorLine(line, i == m.keymap.cursor))
			}
			rendered++
		}
	}

	lines = padLines(lines, maxVisible, rendered)
	lines = append(lines, "", dimStyle.Render(fmt.Sprintf("  %d/%d keys", len(visible), len(entries))))
	lines = append(lines, "", m.keymapHelpLine())

	return m.centerOverlay(strings.Join(lines, "\n"))
}
