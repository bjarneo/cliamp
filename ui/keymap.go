package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
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
	{"R", "Radio catalog (search online stations)"},
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
		m.keymap.visible = false
		m.keymap.search = ""
		m.keymap.filtered = nil
		m.keymap.cursor = 0
	case tea.KeyUp:
		if m.keymap.cursor > 0 {
			m.keymap.cursor--
		} else {
			count := len(keymapEntries)
			if m.keymap.search != "" {
				count = len(m.keymap.filtered)
			}
			if count > 0 {
				m.keymap.cursor = count-1
			}
		}
	case tea.KeyDown:
		count := len(keymapEntries)
		if m.keymap.search != "" {
			count = len(m.keymap.filtered)
		}
		if m.keymap.cursor < count-1 {
			m.keymap.cursor++
		} else if count > 0 {
			m.keymap.cursor = 0
		}
	case tea.KeyBackspace:
		if m.keymap.search != "" {
			m.keymap.search = removeLastRune(m.keymap.search)
			m.updateKeymapFilter()
		}
	case tea.KeySpace:
		m.keymap.search += " "
		m.updateKeymapFilter()
	default:
		switch msg.String() {
		case "ctrl+c":
			m.keymap.visible = false
			return m.quit()
		default:
			if msg.Type == tea.KeyRunes {
				m.keymap.search += string(msg.Runes)
				m.updateKeymapFilter()
			}
		}
	}
	return nil
}

// updateKeymapFilter rebuilds the filtered indices and clamps the cursor.
func (m *Model) updateKeymapFilter() {
	m.keymap.filtered = nil
	m.keymap.cursor = 0
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
