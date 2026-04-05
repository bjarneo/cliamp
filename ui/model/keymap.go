package model

import (
	"strings"

	tea "charm.land/bubbletea/v2"
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

func (m Model) keymapCount() int {
	if m.keymap.search != "" {
		return len(m.keymap.filtered)
	}
	return len(keymapEntries)
}

// handleKeymapKey processes key presses while the keymap overlay is open.
func (m *Model) handleKeymapKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()

	switch {
	case key == "ctrl+c":
		m.keymap.visible = false
		return m.quit()

	case msg.Code == tea.KeyEscape:
		m.keymap.visible = false
		m.keymap.search = ""
		m.keymap.filtered = nil
		m.keymap.cursor = 0

	case msg.Code == tea.KeyUp:
		count := m.keymapCount()
		if m.keymap.cursor > 0 {
			m.keymap.cursor--
		} else if count > 0 {
			m.keymap.cursor = count - 1
		}

	case msg.Code == tea.KeyDown:
		count := m.keymapCount()
		if m.keymap.cursor < count-1 {
			m.keymap.cursor++
		} else if count > 0 {
			m.keymap.cursor = 0
		}

	case key == "ctrl+x":
		m.toggleExpandPlaylist()

	case key == "pgup" || key == "ctrl+u":
		if m.keymap.cursor > 0 {
			step := max(1, m.keymapVisibleRows())
			m.keymap.cursor -= min(m.keymap.cursor, step)
		}

	case key == "pgdown" || key == "ctrl+d":
		count := m.keymapCount()
		if m.keymap.cursor < count-1 {
			step := max(1, m.keymapVisibleRows())
			m.keymap.cursor = min(count-1, m.keymap.cursor+step)
		}

	case msg.Code == tea.KeyHome:
		m.keymap.cursor = 0

	case msg.Code == tea.KeyEnd:
		count := m.keymapCount()
		if count > 0 {
			m.keymap.cursor = count - 1
		}

	case msg.Code == tea.KeyBackspace:
		if m.keymap.search != "" {
			m.keymap.search = removeLastRune(m.keymap.search)
			m.updateKeymapFilter()
		}

	case msg.Code == tea.KeySpace:
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
