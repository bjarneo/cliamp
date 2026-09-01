package model

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

// commandMode identifies the UI contexts in which a command is available.
// A command may belong to more than one context.
type commandMode uint64

const (
	commandModeMain commandMode = 1 << iota
	commandModeProvider
	commandModeEQ
	commandModeSpeed
	commandModeProviderPill
	commandModeKeymap
	commandModeKeymapSearch
	commandModeFileBrowser
	commandModeFileBrowserSearch
	commandModeNavBrowser
	commandModeNavSearch
	commandModePlaylistManager
	commandModePlaylistManagerInput
	commandModePlaylistManagerDirs
	commandModePlaylistPicker
	commandModePlaylistPickerInput
	commandModeQueue
	commandModeSearch
	commandModeNetSearch
	commandModeSpotSearch
	commandModeJump
	commandModeURL
	commandModeLyrics
	commandModeThemePicker
	commandModeVisPicker
	commandModeDevicePicker
	commandModeInfo
	commandModeThemePickerFilter
	commandModeVisPickerFilter
	commandModeProviderSearch
	commandModeRadioStats
)

const commandModeAny = ^commandMode(0)

// commandSpec is the single source of metadata for in-app commands. Dispatch
// remains in the focused handlers, while keymap, plugin reservations, and help
// all consume this description.
type commandSpec struct {
	Mode        commandMode
	Keys        []string // Bubbletea KeyPressMsg.String values.
	KeyLabel    string   // Human-readable key label.
	Label       string
	LabelFor    func(Model) string
	Enabled     func(Model) bool
	Destructive bool
	Keymap      bool
	ContextHelp bool
	Prominent   bool
	Primary     bool
	Cancel      bool
	Help        bool
}

func (c commandSpec) enabled(m Model) bool {
	return c.Enabled == nil || c.Enabled(m)
}

func (c commandSpec) label(m Model) string {
	if c.LabelFor != nil {
		return c.LabelFor(m)
	}
	return c.Label
}

// commandRegistry deliberately lists every core-reserved key, including text
// editor keys that are not shown in the global keymap. Keep key labels in this
// table so the keymap cannot drift from plugin key reservations.
var commandRegistry = []commandSpec{
	{Mode: commandModeMain | commandModeEQ | commandModeSpeed, Keys: []string{"space"}, KeyLabel: "Space", Label: "Play / Pause", Keymap: true, ContextHelp: true, Primary: true},
	{Mode: commandModeMain, Keys: []string{"s"}, KeyLabel: "s", Label: "Stop", Keymap: true},
	{Mode: commandModeMain, Keys: []string{">", "."}, KeyLabel: "> .", Label: "Next track", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"<", ","}, KeyLabel: "< ,", Label: "Previous track", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"left", "right"}, KeyLabel: "Left Right", Label: "Seek +/-5s", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"shift+left", "shift+right"}, KeyLabel: "Shift+Left Right", Label: "Seek +/-large step", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "j"}, KeyLabel: "Nj", Label: "Seek to N x 10% of track (e.g. 7j = 70%)", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"+", "=", "-"}, KeyLabel: "+ -", Label: "Volume up/down", Keymap: true},
	{Mode: commandModeMain | commandModeSpeed, Keys: []string{"]", "["}, KeyLabel: "] [", Label: "Speed up/down (+/-0.25x)", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"z"}, KeyLabel: "z", Label: "Toggle shuffle", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"r"}, KeyLabel: "r", Label: "Cycle repeat", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"m"}, KeyLabel: "m", Label: "Toggle mono", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"e"}, KeyLabel: "e", Label: "Cycle EQ preset", Enabled: func(m Model) bool { return !m.simplified }, Keymap: true},
	{Mode: commandModeMain, Keys: []string{"t"}, KeyLabel: "t", Label: "Choose theme", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"v"}, KeyLabel: "v", Label: "Cycle visualizer", Enabled: func(m Model) bool { return !m.simplified }, Keymap: true},
	{Mode: commandModeMain, Keys: []string{"ctrl+v"}, KeyLabel: "Ctrl+V", Label: "Choose visualizer", Enabled: func(m Model) bool { return !m.simplified }, Keymap: true},
	{Mode: commandModeMain, Keys: []string{"V"}, KeyLabel: "V", Label: "Full-screen visualizer", Enabled: func(m Model) bool { return !m.simplified }, Keymap: true},
	{Mode: commandModeMain, Keys: []string{"up", "down", "k", "j"}, KeyLabel: "Up Down", Label: "Playlist scroll / EQ adjust (wraps around)", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"pgup", "pgdown", "ctrl+u", "ctrl+d"}, KeyLabel: "PgUp PgDn / Ctrl+U D", Label: "Scroll playlist/browser by page", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"home", "end", "g", "G"}, KeyLabel: "Home End / g G", Label: "Go to top/end of playlist/browser", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"shift+up", "shift+down"}, KeyLabel: "Shift+Up Down", Label: "Move track up/down", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"h", "l"}, KeyLabel: "h l", Label: "EQ cursor left/right", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"enter"}, KeyLabel: "Enter", Label: "Play selected track", Keymap: true, ContextHelp: true, Primary: true},
	{Mode: commandModeMain, Keys: []string{"f", "n"}, KeyLabel: "f/n", Label: "★/" + favHeart, Keymap: true, ContextHelp: true},
	{Mode: commandModeMain, Keys: []string{"a"}, KeyLabel: "a", Label: "Toggle queue (play next)", Keymap: true, ContextHelp: true},
	{Mode: commandModeMain, Keys: []string{"A"}, KeyLabel: "A", Label: "Queue manager", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"x"}, KeyLabel: "x", Label: "Remove selected track from playlist", Destructive: true, Keymap: true},
	{Mode: commandModeMain, Keys: []string{"w"}, KeyLabel: "w", Label: "Write selected track/selection to playlist", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"o"}, KeyLabel: "o", Label: "Open file browser", Keymap: true},
	{Mode: commandModeMain | commandModeProvider, Keys: []string{"N"}, KeyLabel: "N", Label: "Browse provider", LabelFor: func(m Model) string {
		if _, ok := m.selectedTrackArtistBrowserTarget(); ok {
			return "Browse creator"
		}
		return "Browse provider"
	}, Enabled: func(m Model) bool { return m.canOpenProviderBrowser() }, Keymap: true, ContextHelp: true, Prominent: true},
	{Mode: commandModeMain, Keys: []string{"L"}, KeyLabel: "L", Label: "Browse local playlists", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"R"}, KeyLabel: "R", Label: "Open radio provider", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"S"}, KeyLabel: "S", Label: "Open Spotify provider", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"P"}, KeyLabel: "P", Label: "Open Plex provider", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"Y"}, KeyLabel: "Y", Label: "Open YouTube provider", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"C"}, KeyLabel: "C", Label: "Open SoundCloud provider", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"X"}, KeyLabel: "X", Label: "Open Mixcloud provider", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"M"}, KeyLabel: "M", Label: "Open NetEase provider", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"J"}, KeyLabel: "J", Label: "Open Jellyfin provider", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"E"}, KeyLabel: "E", Label: "Open Emby provider", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"B"}, KeyLabel: "B", Label: "Open Audiobookshelf provider", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"Q"}, KeyLabel: "Q", Label: "Open Qobuz provider", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"T"}, KeyLabel: "T", Label: "Open Tidal provider", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"ctrl+j"}, KeyLabel: "Ctrl+J", Label: "Jump to time", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"p"}, KeyLabel: "p", Label: "Playlist manager", Keymap: true},
	{Mode: commandModeProvider, Keys: []string{"p"}, KeyLabel: "p", Label: "Playlist manager", Keymap: true, ContextHelp: true, Enabled: func(m Model) bool {
		return m.isActiveProvider("Local") && m.localProvider != nil
	}},
	{Mode: commandModeMain, Keys: []string{"ctrl+h"}, KeyLabel: "Ctrl+H", Label: "Toggle album headers", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"ctrl+g"}, KeyLabel: "Ctrl+G", Label: "Toggle key-binding hint bar", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"i"}, KeyLabel: "i", Label: "Track info / metadata", Keymap: true, ContextHelp: true},
	{Mode: commandModeMain, Keys: []string{"ctrl+s"}, KeyLabel: "Ctrl+S", Label: "Save/download track to ~/Music/cliamp", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"ctrl+x"}, KeyLabel: "Ctrl+X", Label: "Expand/collapse view", Enabled: func(m Model) bool { return !m.simplified }, Keymap: true},
	{Mode: commandModeMain, Keys: []string{"ctrl+x"}, KeyLabel: "Ctrl+X", Label: "Expand", Enabled: func(m Model) bool {
		return !m.simplified && !m.heightExpanded && m.layout.bodyRows > m.plVisible
	}},
	{Mode: commandModeMain, Keys: []string{"/"}, KeyLabel: "/", Label: "Filter/search list", Keymap: true, ContextHelp: true},
	{Mode: commandModeMain, Keys: []string{"ctrl+f"}, KeyLabel: "Ctrl+F", Label: "Search active provider or YouTube", Keymap: true, ContextHelp: true},
	{Mode: commandModeMain, Keys: []string{"u"}, KeyLabel: "u", Label: "Load URL (stream/playlist)", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"d"}, KeyLabel: "d", Label: "Audio device picker", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"y"}, KeyLabel: "y", Label: "Show lyrics", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"tab"}, KeyLabel: "Tab", Label: "Toggle focus", Keymap: true},
	{Mode: commandModeMain, Keys: []string{"esc", "backspace", "b"}, KeyLabel: "Esc", Label: "Back to provider", Keymap: true, ContextHelp: true, Cancel: true},
	{Mode: commandModeAny, Keys: []string{"ctrl+k"}, KeyLabel: "Ctrl+K", Label: "Help", Keymap: true, ContextHelp: true, Help: true},
	{Mode: commandModeMain, Keys: []string{"?"}, KeyLabel: "?", Label: "Help", Keymap: true},
	{Mode: commandModeAny, Keys: []string{"ctrl+c", "q"}, KeyLabel: "q", Label: "Quit", Keymap: true},
	{Mode: commandModeAny, Keys: []string{"ctrl+z"}, KeyLabel: "Ctrl+Z", Label: "Undo latest playlist or queue mutation"},
	{Mode: commandModeProvider, Keys: []string{"ctrl+r"}, KeyLabel: "Ctrl+R", Label: "Refresh provider"},

	// Shared text editing is reserved even though these are intentionally absent
	// from the global keymap, where they would be misleading outside a field.
	{Mode: commandModeKeymapSearch | commandModeFileBrowserSearch | commandModeNavSearch | commandModePlaylistManagerInput | commandModePlaylistPickerInput | commandModeSearch | commandModeNetSearch | commandModeSpotSearch | commandModeJump | commandModeURL | commandModeThemePickerFilter | commandModeVisPickerFilter | commandModeProviderSearch, Keys: []string{"left", "right", "home", "end", "ctrl+a", "ctrl+e", "backspace", "delete", "ctrl+w", "ctrl+u"}, KeyLabel: "Text editor", Label: "Move cursor and delete text"},

	{Mode: commandModeProvider, Keys: []string{"enter"}, KeyLabel: "Enter", Label: "Load", LabelFor: func(m Model) string {
		if m.selectedProviderListIsBrowseEntry() {
			return "Open"
		}
		return "Load"
	}, ContextHelp: true, Primary: true},
	{Mode: commandModeProvider, Keys: []string{"esc", "backspace", "b"}, KeyLabel: "Esc", Label: "Back", ContextHelp: true, Cancel: true},
	{Mode: commandModeEQ, Keys: []string{"up", "down"}, KeyLabel: "Up Down", Label: "Gain", ContextHelp: true},
	{Mode: commandModeSpeed, Keys: []string{"left", "right"}, KeyLabel: "Left Right", Label: "Speed", ContextHelp: true},
	{Mode: commandModeProviderPill, Keys: []string{"enter"}, KeyLabel: "Enter", Label: "Open", ContextHelp: true, Primary: true},
	{Mode: commandModeProviderPill, Keys: []string{"esc", "backspace"}, KeyLabel: "Esc", Label: "Back", ContextHelp: true, Cancel: true},
	{Mode: commandModeKeymap | commandModeFileBrowser | commandModeNavBrowser | commandModePlaylistManager | commandModePlaylistPicker | commandModeQueue | commandModeDevicePicker, Keys: []string{"esc"}, KeyLabel: "Esc", Label: "Back", ContextHelp: true, Cancel: true},
	{Mode: commandModeKeymapSearch | commandModeFileBrowserSearch | commandModeNavSearch | commandModePlaylistManagerInput | commandModePlaylistPickerInput | commandModeSearch | commandModeNetSearch | commandModeSpotSearch | commandModeJump | commandModeURL | commandModeProviderSearch, Keys: []string{"esc"}, KeyLabel: "Esc", Label: "Cancel", ContextHelp: true, Cancel: true},
	{Mode: commandModeKeymapSearch | commandModeFileBrowserSearch | commandModeNavSearch | commandModePlaylistManagerInput | commandModePlaylistPickerInput | commandModeSearch | commandModeNetSearch | commandModeSpotSearch | commandModeJump | commandModeURL | commandModeProviderSearch, Keys: []string{"enter"}, KeyLabel: "Enter", Label: "Confirm", ContextHelp: true, Primary: true},
	{Mode: commandModeNavBrowser | commandModePlaylistManager | commandModePlaylistPicker | commandModeQueue | commandModeDevicePicker | commandModeProviderSearch, Keys: []string{"up", "down", "k", "j"}, KeyLabel: "Up Down", Label: "Navigate", ContextHelp: true},
	{Mode: commandModeFileBrowser | commandModeNavBrowser | commandModePlaylistManager | commandModePlaylistPicker | commandModeDevicePicker, Keys: []string{"enter"}, KeyLabel: "Enter", Label: "Select", ContextHelp: true, Primary: true},
	{Mode: commandModeNavBrowser, Keys: []string{"/"}, KeyLabel: "/", Label: "Filter", ContextHelp: true, Enabled: func(m Model) bool { return m.navBrowser.mode != navBrowseModeMenu }},
	{Mode: commandModeNavBrowser, Keys: []string{"f"}, KeyLabel: "f", Label: "Favorite genre", LabelFor: func(m Model) string {
		if genre, ok := m.selectedNavGenre(); ok && genre.Favorite {
			return "Unfavorite genre"
		}
		return "Favorite genre"
	}, ContextHelp: true, Enabled: func(m Model) bool {
		_, canFavorite := m.navBrowser.prov.(provider.GenreFavoriteToggler)
		return canFavorite && m.navBrowser.mode == navBrowseModeByGenre && m.navBrowser.screen == navBrowseScreenList && !m.navBrowser.loading
	}},
	{Mode: commandModeKeymap, Keys: []string{"/"}, KeyLabel: "/", Label: "Filter", ContextHelp: true, Primary: true},
	{Mode: commandModeThemePicker, Keys: []string{"esc"}, KeyLabel: "Esc", Label: "Cancel preview", ContextHelp: true, Cancel: true},
	{Mode: commandModeThemePicker, Keys: []string{"enter"}, KeyLabel: "Enter", Label: "Apply theme", ContextHelp: true, Primary: true},
	{Mode: commandModeThemePicker, Keys: []string{"up", "down", "k", "j"}, KeyLabel: "Up Down", Label: "Preview", ContextHelp: true},
	{Mode: commandModeThemePicker, Keys: []string{"/"}, KeyLabel: "/", Label: "Filter", ContextHelp: true},
	{Mode: commandModeVisPicker, Keys: []string{"esc"}, KeyLabel: "Esc", Label: "Cancel preview", ContextHelp: true, Cancel: true},
	{Mode: commandModeVisPicker, Keys: []string{"enter"}, KeyLabel: "Enter", Label: "Save", ContextHelp: true, Primary: true},
	{Mode: commandModeVisPicker, Keys: []string{"up", "down", "k", "j"}, KeyLabel: "Up Down", Label: "Preview", ContextHelp: true},
	{Mode: commandModeVisPicker, Keys: []string{"/"}, KeyLabel: "/", Label: "Filter", ContextHelp: true},
	{Mode: commandModeThemePickerFilter | commandModeVisPickerFilter, Keys: []string{"esc"}, KeyLabel: "Esc", Label: "Cancel filter", ContextHelp: true, Cancel: true},
	{Mode: commandModeThemePickerFilter | commandModeVisPickerFilter, Keys: []string{"enter"}, KeyLabel: "Enter", Label: "Finish filter", ContextHelp: true, Primary: true},
	{Mode: commandModeQueue, Keys: []string{"d"}, KeyLabel: "d", Label: "Remove", Destructive: true, ContextHelp: true, Primary: true, Enabled: func(m Model) bool { return m.playlist != nil && m.playlist.QueueLen() > 0 }},
	{Mode: commandModeQueue, Keys: []string{"c"}, KeyLabel: "c", Label: "Clear", Destructive: true, ContextHelp: true},
	{Mode: commandModeFileBrowser, Keys: []string{"R"}, KeyLabel: "R", Label: "Replace queue", Destructive: true, ContextHelp: true},
	{Mode: commandModeNavBrowser, Keys: []string{"R"}, KeyLabel: "R", Label: "Replace queue", Destructive: true, ContextHelp: true, Enabled: func(m Model) bool { return m.navView() == navViewTracks }},
	{Mode: commandModeLyrics, Keys: []string{"r"}, KeyLabel: "r", Label: "Retry", ContextHelp: true, Primary: true, Enabled: func(m Model) bool { return !m.lyrics.loading && (m.lyrics.err != nil || len(m.lyrics.lines) == 0) }},
	{Mode: commandModeLyrics, Keys: []string{"esc"}, KeyLabel: "Esc", Label: "Close", ContextHelp: true, Cancel: true},
	{Mode: commandModeInfo, Keys: []string{"esc"}, KeyLabel: "Esc", Label: "Close", ContextHelp: true, Cancel: true},
	{Mode: commandModePlaylistManager, Keys: []string{"a"}, KeyLabel: "a", Label: "New playlist", ContextHelp: true, Primary: true, Enabled: func(m Model) bool {
		return m.plManager.visible && m.plManager.screen == plMgrScreenList
	}},
	{Mode: commandModePlaylistManager, Keys: []string{"D"}, KeyLabel: "D", Label: "Add dir sources", ContextHelp: true, Enabled: func(m Model) bool {
		if !m.plManager.visible {
			return false
		}
		switch m.plManager.screen {
		case plMgrScreenTracks:
			return plMgrVirtualPlaylistName(m.plManager.selPlaylist) == ""
		case plMgrScreenList:
			idx := m.plMgrPlaylistRealIndex(m.plManager.cursor)
			return idx >= 0 && plMgrVirtualPlaylistName(m.plManager.playlists[idx].Name) == ""
		default:
			return false
		}
	}},
	{Mode: commandModePlaylistManager, Keys: []string{"f", "n"}, KeyLabel: "f/n", Label: "★/" + favHeart, ContextHelp: true, Enabled: func(m Model) bool {
		return m.plManager.visible && m.plManager.screen == plMgrScreenTracks
	}},
	{Mode: commandModePlaylistManager, Keys: []string{"[", "]"}, KeyLabel: "[ ]", Label: "Reorder", ContextHelp: true, Enabled: func(m Model) bool {
		return m.plManager.visible && m.plManager.screen == plMgrScreenTracks
	}},
	{Mode: commandModePlaylistManagerDirs, Keys: []string{"esc", "backspace", "h", "left"}, KeyLabel: "Esc", Label: "Back to tracks", ContextHelp: true, Cancel: true},
	{Mode: commandModePlaylistManagerDirs, Keys: []string{"a"}, KeyLabel: "a", Label: "Add dir", ContextHelp: true, Primary: true},
	{Mode: commandModePlaylistManagerDirs, Keys: []string{"d"}, KeyLabel: "d", Label: "Remove", Destructive: true, ContextHelp: true},
	{Mode: commandModePlaylistManagerDirs, Keys: []string{"r"}, KeyLabel: "r", Label: "Toggle recursive", ContextHelp: true},
	{Mode: commandModePlaylistManagerDirs, Keys: []string{"up", "down", "k", "j"}, KeyLabel: "Up Down", Label: "Navigate", ContextHelp: true},
	{Mode: commandModeFileBrowser, Keys: []string{"D"}, KeyLabel: "D", Label: "Add as dir source", ContextHelp: true, Enabled: func(m Model) bool {
		return m.fileBrowser.visible && m.fileBrowser.targetPlaylist != ""
	}},
	{Mode: commandModeRadioStats, Keys: []string{"esc"}, KeyLabel: "Esc", Label: "Close", ContextHelp: true, Cancel: true},
	{Mode: commandModeRadioStats, Keys: []string{"r"}, KeyLabel: "r", Label: "Refresh", ContextHelp: true, Primary: true},
	{Mode: commandModeRadioStats, Keys: []string{"up", "down", "k", "j"}, KeyLabel: "Up Down", Label: "Scroll", ContextHelp: true},
}

func (m Model) commandHelp(mode commandMode) string {
	var cancel, primary, help, prominent, optional []commandSpec
	for _, command := range commandRegistry {
		if !command.ContextHelp || command.Mode&mode == 0 || !command.enabled(m) {
			continue
		}
		switch {
		case command.Cancel:
			cancel = append(cancel, command)
		case command.Primary:
			primary = append(primary, command)
		case command.Help:
			help = append(help, command)
		case command.Prominent:
			prominent = append(prominent, command)
		default:
			optional = append(optional, command)
		}
	}

	// Back/cancel, the primary action, and help are the only mandatory hints.
	// Keep them first so narrow terminals drop optional navigation rather than
	// trapping the user in an unfamiliar overlay.
	var ordered []commandSpec
	if len(cancel) > 0 {
		ordered = append(ordered, cancel[0])
	}
	if len(primary) > 0 {
		ordered = append(ordered, primary[0])
	}
	if len(help) > 0 {
		ordered = append(ordered, help[0])
	}
	ordered = append(ordered, prominent...)
	ordered = append(ordered, optional...)
	return renderCommandHelp(ordered, m.helpWidth(), m)
}

func (m Model) helpWidth() int {
	if ui.PanelWidth > 0 {
		return ui.PanelWidth
	}
	if m.width > 0 {
		return m.width
	}
	return 80
}

func renderCommandHelp(commands []commandSpec, width int, m Model) string {
	if width <= 0 {
		return ""
	}
	narrow := width < 48
	var b strings.Builder
	for _, command := range commands {
		label := command.label(m)
		if narrow {
			switch {
			case command.Cancel:
				label = "Back"
			case command.Primary:
				label = "Go"
			case command.Help:
				label = "Help"
			}
		}
		hint := helpKey(command.KeyLabel, label)
		if b.Len() > 0 {
			hint = " " + hint
		}
		if lipgloss.Width(b.String()+hint) > width {
			break
		}
		b.WriteString(hint)
	}
	return b.String()
}
