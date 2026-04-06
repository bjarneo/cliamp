package model

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"cliamp/ui"
)

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
	{"] [", "Speed up/down (±0.25x)"},
	{"z", "Toggle shuffle"},
	{"r", "Cycle repeat"},
	{"m", "Toggle mono"},
	{"e", "Cycle EQ preset"},
	{"t", "Choose theme"},
	{"v", "Cycle visualizer"},
	{"V", "Full-screen visualizer"},
	{"↑ ↓", "Playlist scroll / EQ adjust (wraps around)"},
	{"PgUp PgDn / Ctrl+U D", "Scroll playlist/browser by page"},
	{"Home End / g G", "Go to top/end of playlist/browser"},
	{"Shift+↑ ↓", "Move track up/down"},
	{"h l", "EQ cursor left/right"},
	{"Enter", "Play selected track"},
	{"a", "Toggle queue (play next)"},
	{"A", "Queue manager"},
	{"o", "Open file browser"},
	{"N", "Navidrome browser"},
	{"L", "Browse local playlists"},
	{"R", "Open radio provider"},
	{"S", "Open Spotify provider"},
	{"P", "Open Plex provider"},
	{"Y", "Open YouTube provider"},
	{"J", "Open Jellyfin provider"},
	{"Ctrl+J", "Jump to time"},
	{"*", "Toggle favorite ★"},
	{"p", "Playlist manager"},
	{"i", "Track info / metadata"},
	{"Ctrl+S", "Save/download track to ~/Music"},
	{"Ctrl+X", "Expand/collapse playlist"},
	{"/", "Search playlist"},
	{"f", "Find on YouTube (queue play next)"},
	{"Ctrl+F", "Find on SoundCloud (queue play next)"},
	{"F", "Spotify search + add to playlist"},
	{"u", "Load URL (stream/playlist)"},
	{"d", "Audio device picker"},
	{"y", "Show lyrics"},
	{"Tab", "Toggle focus"},
	{"Esc", "Back to provider"},
	{"Ctrl+K", "This keymap"},
	{"q", "Quit"},
}

func (m *Model) keymapCount() int {
	if m.keymap.search != "" {
		return len(m.keymap.filtered)
	}
	return len(keymapEntries)
}

func (m *Model) keymapHelpLine() string {
	return helpKey("↑↓", "Navigate ") + helpKey("PgUp/Dn", "Page ") +
		helpKey("Home/End", "Jump ") + helpKey("Type", "Filter ") + helpKey("Esc", "Close")
}

func (m *Model) keymapHeaderLines() []string {
	header := []string{
		titleStyle.Render("K E Y M A P"),
		"",
	}
	if m.keymap.search != "" {
		header = append(header, playlistSelectedStyle.Render("  / "+m.keymap.search+"_"), "")
	} else {
		header = append(header, dimStyle.Render("  Type to filter…"), "")
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

	if m.keymap.scroll+visible > count {
		m.keymap.scroll = max(0, count-visible)
	}
}

// openKeymap resets the keymap state and shows it.
func (m *Model) openKeymap() {
	m.keymap.search = ""
	m.keymap.filtered = nil
	m.keymap.cursor = 0
	m.keymap.scroll = 0
	m.keymap.visible = true
}

// handleKeymapKey processes key presses while the keymap overlay is open.
func (m *Model) handleKeymapKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.keymap.visible = false
		return m.quit()

	case "esc", "ctrl+k":
		m.keymap.visible = false

	case "up":
		count := m.keymapCount()
		if m.keymap.cursor > 0 {
			m.keymap.cursor--
		} else if count > 0 {
			m.keymap.cursor = count - 1
		}
		m.keymapMaybeAdjustScroll(m.keymapVisible())

	case "down":
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

	case "home":
		m.keymap.cursor = 0
		m.keymapMaybeAdjustScroll(m.keymapVisible())

	case "end":
		count := m.keymapCount()
		if count > 0 {
			m.keymap.cursor = count - 1
		}
		m.keymapMaybeAdjustScroll(m.keymapVisible())

	case "backspace":
		if m.keymap.search != "" {
			m.keymap.search = removeLastRune(m.keymap.search)
			m.updateKeymapFilter()
		}

	case "space":
		m.keymap.search += " "
		m.updateKeymapFilter()

	default:
		if len(msg.Text) > 0 {
			m.keymap.search += msg.Text
			m.updateKeymapFilter()
		}
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
	for i, e := range keymapEntries {
		if strings.Contains(strings.ToLower(e.key), query) ||
			strings.Contains(strings.ToLower(e.action), query) {
			m.keymap.filtered = append(m.keymap.filtered, i)
		}
	}
}

// renderKeymapOverlay renders the keymap overlay.
func (m Model) renderKeymapOverlay() string {
	lines := append(make([]string, 0, 16), m.keymapHeaderLines()...)

	entries := keymapEntries
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
		lines = append(lines, dimStyle.Render("  No matches"))
		rendered = 1
	} else {
		scroll := m.keymap.scroll
		for i := scroll; i < len(visible) && i < scroll+maxVisible; i++ {
			line := fmt.Sprintf("%-10s %s", visible[i].key, visible[i].action)
			lines = append(lines, cursorLine(line, i == m.keymap.cursor))
			rendered++
		}
	}

	lines = padLines(lines, maxVisible, rendered)
	lines = append(lines, "", dimStyle.Render(fmt.Sprintf("  %d/%d keys", len(visible), len(entries))))
	lines = append(lines, "", m.keymapHelpLine())

	return m.centerOverlay(strings.Join(lines, "\n"))
}