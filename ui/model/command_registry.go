package model

import (
	"slices"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/provider"
)

// commandMode identifies the UI contexts in which a command is available.
// A command may belong to more than one context.
type commandMode uint64

const (
	commandModeMain commandMode = 1 << iota
	commandModeEQ
	commandModeVolume
	commandModeShuffle
	commandModeRepeat
	commandModeSpeed
	commandModeProviderPill
	commandModeQueue
	commandModeSearch
	commandModeNetSearch
	commandModeSearchOverlay
	commandModeProvider
	commandModeProviderSearch
	commandModeSubs
	commandModeSubsFilter
	commandModeNavBrowser
	commandModeNavSearch
	commandModeFileBrowser
	commandModeFileBrowserSearch
	commandModePlaylistManager
	commandModePlaylistManagerInput
	commandModePlaylistManagerDirs
	commandModePlaylistPicker
	commandModePlaylistPickerInput
	commandModeKeymap
	commandModeKeymapSearch
	commandModeJump
	commandModeURL
	commandModeAuthCode
	commandModeLyrics
	commandModeThemePicker
	commandModeThemePickerFilter
	commandModeDevicePicker
	commandModeInfo
	commandModeVisPicker
	commandModeVisPickerFilter
	commandModeFullVis
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
	// RunKey is the key that the keymap sends to run a command that has more
	// than one key. Set it only when all keys do the same thing. Leave it
	// empty for key pairs and count prefixes: the keymap cannot run those.
	RunKey string
}

func (c commandSpec) enabled(m Model) bool {
	return c.Enabled == nil || c.Enabled(m)
}

// runKey returns the key that the keymap sends to run the command. It
// returns "" when one key press cannot run the command.
func (c commandSpec) runKey() string {
	if c.RunKey != "" {
		return c.RunKey
	}
	if len(c.Keys) == 1 {
		return c.Keys[0]
	}
	return ""
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
	{
		Mode:     commandModeAny,
		Keys:     []string{"ctrl+k"},
		KeyLabel: "Ctrl+K",
		Label:    "Help",
		Keymap:   true,
	},
	{
		Mode:     commandModeAny,
		Keys:     []string{"?"},
		KeyLabel: "?",
		Label:    "Help",
		Keymap:   true,
	},
	{
		Mode:     commandModeAny,
		Keys:     []string{"ctrl+c"},
		KeyLabel: "Ctrl+C",
		Label:    "Quit",
		Keymap:   true,
	},
	{
		Mode:     commandModeAny,
		Keys:     []string{"ctrl+z"},
		KeyLabel: "Ctrl+Z",
		Label:    "Undo latest playlist or queue mutation",
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"esc", "backspace", "b"},
		KeyLabel: "Esc",
		RunKey:   "esc",
		Label:    "Back to provider",
		Keymap:   true,
	},

	// q quits only from the player and its focused controls. Overlays use it
	// to close or to queue, so the keymap must not run it as quit there.
	{
		Mode:     commandModeMain | commandModeProvider | commandModeProviderPill | commandModeEQ | commandModeVolume | commandModeShuffle | commandModeRepeat | commandModeSpeed,
		Keys:     []string{"q"},
		KeyLabel: "q",
		Label:    "Quit",
		Keymap:   true,
		Enabled:  func(m Model) bool { return !m.subs.visible },
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"enter"},
		KeyLabel: "Enter",
		Label:    "Play selected track",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain | commandModeEQ | commandModeSpeed,
		Keys:     []string{"space"},
		KeyLabel: "Space",
		Label:    "Play / Pause",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"s"},
		KeyLabel: "s",
		Label:    "Stop",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"<", ","},
		KeyLabel: "< ,",
		RunKey:   "<",
		Label:    "Previous track",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{">", "."},
		KeyLabel: "> .",
		RunKey:   ">",
		Label:    "Next track",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"left", "right"},
		KeyLabel: "Left Right",
		Label:    "Seek +/-5s",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"shift+left", "shift+right"},
		KeyLabel: "Shift+Left Right",
		Label:    "Seek +/-large step",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "j"},
		KeyLabel: "Nj",
		Label:    "Seek to N x 10% of track (e.g. 7j = 70%)",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"ctrl+j"},
		KeyLabel: "Ctrl+J",
		Label:    "Jump to time",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"pgdown", "ctrl+d"},
		KeyLabel: "PgDn Ctrl+d",
		Label:    "Scroll to next page",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"pgup", "ctrl+u"},
		KeyLabel: "PgUp Ctrl+u",
		Label:    "Scroll to previous page",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"end", "G"},
		KeyLabel: "End G",
		Label:    "Go to bottom of playlist",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"home", "g"},
		KeyLabel: "Home g",
		Label:    "Go to top of playlist",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"up", "down", "k", "j"},
		KeyLabel: "Up Down",
		Label:    "Playlist scroll / EQ adjust (wraps around)",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"shift+up", "shift+down"},
		KeyLabel: "Shift+Up Down",
		Label:    "Move track up/down",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"ctrl+o"},
		KeyLabel: "Ctrl+O",
		Label:    "Reverse playlist order",
		Keymap:   true,
	},
	{
		Mode:        commandModeMain,
		Keys:        []string{"x"},
		KeyLabel:    "x",
		Label:       "Remove selected track from playlist",
		Destructive: true,
		Keymap:      true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"f"},
		KeyLabel: "f",
		Label:    "Favorite track / station",
		LabelFor: func(m Model) string {
			if m.selectedPlaylistStarAction() == starRadioFavorite {
				return "Favorite station"
			}
			return "Favorite track"
		},
		Enabled: func(m Model) bool { return m.selectedPlaylistStarAction() != starUnavailable },
		Keymap:  true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"*", "0", "1", "2", "3", "4", "5"},
		KeyLabel: "* then 0-5",
		Label:    "Rate selected track",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"w"},
		KeyLabel: "w",
		Label:    "Write selected track/selection to playlist",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"ctrl+s"},
		KeyLabel: "Ctrl+S",
		Label:    "Save/download current track",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"/"},
		KeyLabel: "/",
		Label:    "Filter/search list",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain | commandModeProvider | commandModeProviderPill | commandModeVolume | commandModeEQ | commandModeShuffle | commandModeRepeat | commandModeSpeed,
		Keys:     []string{"tab", "shift+tab"},
		KeyLabel: "Tab/Shift+Tab",
		Label:    "Focus",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain | commandModeSpeed,
		Keys:     []string{"]", "["},
		KeyLabel: "] [",
		Label:    "Speed up/down (+/-0.25x)",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"e"},
		KeyLabel: "e",
		Label:    "Cycle EQ preset",
		Enabled:  func(m Model) bool { return !m.simplified },
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"alt+e"},
		KeyLabel: "Alt+E",
		Label:    "Previous EQ preset",
		Enabled:  func(m Model) bool { return !m.simplified && m.layout.tier != layoutMinimal },
		Keymap:   true,
	},
	{
		Mode:     commandModeMain | commandModeVolume | commandModeShuffle | commandModeRepeat,
		Keys:     []string{"r"},
		KeyLabel: "r",
		Label:    "Cycle repeat",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"v"},
		KeyLabel: "v",
		Label:    "Cycle visualizer",
		Enabled:  func(m Model) bool { return !m.simplified },
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"h", "l"},
		KeyLabel: "h l",
		Label:    "EQ cursor left/right",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain | commandModeVolume | commandModeShuffle | commandModeRepeat,
		Keys:     []string{"+", "=", "-"},
		KeyLabel: "+ -",
		Label:    "Volume up/down",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain | commandModeVolume | commandModeShuffle | commandModeRepeat,
		Keys:     []string{"z"},
		KeyLabel: "z",
		Label:    "Toggle shuffle",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"m"},
		KeyLabel: "m",
		Label:    "Toggle mono",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"d"},
		KeyLabel: "d",
		Label:    "Audio device picker",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"t"},
		KeyLabel: "t",
		Label:    "Choose theme",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"ctrl+v"},
		KeyLabel: "Ctrl+V",
		Label:    "Choose visualizer",
		Enabled:  func(m Model) bool { return !m.simplified },
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"V"},
		KeyLabel: "V",
		Label:    "Full-screen visualizer",
		Enabled:  func(m Model) bool { return !m.simplified },
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"ctrl+h"},
		KeyLabel: "Ctrl+H",
		Label:    "Toggle album headers",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"ctrl+x"},
		KeyLabel: "Ctrl+X",
		Label:    "Expand/collapse view",
		Enabled:  func(m Model) bool { return !m.simplified },
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"y"},
		KeyLabel: "y",
		Label:    "Show lyrics",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"ctrl+g"},
		KeyLabel: "Ctrl+G",
		Label:    "Toggle key-binding hint bar",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"ctrl+b"},
		KeyLabel: "Ctrl+B",
		Label:    "Open/close the settings pane",
		Enabled: func(m Model) bool {
			return !m.simplified && m.layout.tier == layoutFull
		},
		Keymap: true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"ctrl+i"},
		KeyLabel: "Ctrl+I",
		Label:    "Toggle Track info pane",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"i"},
		KeyLabel: "i",
		Label:    "Track info / metadata",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"o"},
		KeyLabel: "o",
		Label:    "Open file browser",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"p"},
		KeyLabel: "p",
		Label:    "Playlist",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain | commandModeProvider,
		Keys:     []string{"F"},
		KeyLabel: "F",
		Label:    "Subscribed shows",
		Enabled:  func(m Model) bool { return m.hasSubscriptions() },
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"a"},
		KeyLabel: "a",
		Label:    "Toggle queue (play next)",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"A"},
		KeyLabel: "A",
		Label:    "Queue manager",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"ctrl+f"},
		KeyLabel: "Ctrl+F",
		Label:    "Search active provider or YouTube",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain | commandModeProvider,
		Keys:     []string{"N"},
		KeyLabel: "N",
		Label:    "Browse provider",
		LabelFor: func(m Model) string {
			if _, ok := m.selectedTrackArtistBrowserTarget(); ok {
				return "Browse creator"
			}
			return "Browse provider"
		},
		Enabled: func(m Model) bool { return m.canOpenProviderBrowser() },
		Keymap:  true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"u"},
		KeyLabel: "u",
		Label:    "Load URL (stream/playlist)",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"B"},
		KeyLabel: "B",
		Label:    "Open Audiobookshelf provider",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"K"},
		KeyLabel: "K",
		Label:    "Open Bandcamp provider",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"E"},
		KeyLabel: "E",
		Label:    "Open Emby provider",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"J"},
		KeyLabel: "J",
		Label:    "Open Jellyfin provider",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"L"},
		KeyLabel: "L",
		Label:    "Open Local provider",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"X"},
		KeyLabel: "X",
		Label:    "Open Mixcloud provider",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"M"},
		KeyLabel: "M",
		Label:    "Open NetEase provider",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"P"},
		KeyLabel: "P",
		Label:    "Open Plex provider",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"O"},
		KeyLabel: "O",
		Label:    "Open Podcasts provider",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"Q"},
		KeyLabel: "Q",
		Label:    "Open Qobuz provider",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"C"},
		KeyLabel: "C",
		Label:    "Open SoundCloud provider",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"S"},
		KeyLabel: "S",
		Label:    "Open Spotify provider",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"U"},
		KeyLabel: "U",
		Label:    "Open Telegram provider",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"T"},
		KeyLabel: "T",
		Label:    "Open Tidal provider",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"Y"},
		KeyLabel: "Y",
		Label:    "Open YouTube provider, again for YouTube and YouTube Music",
		Keymap:   true,
	},
	{
		Mode:     commandModeMain,
		Keys:     []string{"R"},
		KeyLabel: "R",
		Label:    "Open radio provider",
		Keymap:   true,
	},
	{
		Mode:     commandModeProviderPill | commandModeEQ | commandModeSpeed | commandModeShuffle | commandModeRepeat | commandModeVolume,
		Keys:     []string{"esc", "backspace"},
		KeyLabel: "Esc",
		RunKey:   "esc",
		Label:    "Back",
		Keymap: true,
	},
	{
		Mode:     commandModeEQ,
		Keys:     []string{"left", "right", "h", "l"},
		KeyLabel: "Left Right",
		Label:    "Select band",
	},
	{
		Mode:     commandModeEQ,
		Keys:     []string{"up", "down"},
		KeyLabel: "Up Down",
		Label:    "Gain",
		Keymap: true,
	},
	{
		Mode:     commandModeVolume,
		Keys:     []string{"left", "right", "up", "down", "h", "l", "k", "j"},
		KeyLabel: "Arrows",
		Label:    "Volume +/-1dB",
		Keymap: true,
	},
	{
		Mode:     commandModeShuffle,
		Keys:     []string{"enter", "left", "right", "up", "down", "h", "l", "k", "j"},
		KeyLabel: "Enter / Arrows",
		RunKey:   "enter",
		Label:    "Toggle shuffle",
		Keymap: true,
	},
	{
		Mode:     commandModeRepeat,
		Keys:     []string{"enter", "left", "right", "up", "down", "h", "l", "k", "j"},
		KeyLabel: "Enter / Arrows",
		Label:    "Cycle repeat",
		Keymap: true,
	},
	{
		Mode:     commandModeSpeed,
		Keys:     []string{"left", "right"},
		KeyLabel: "Left Right",
		Label:    "Speed",
		Keymap: true,
	},
	{
		Mode:     commandModeProviderPill,
		Keys:     []string{"enter"},
		KeyLabel: "Enter",
		Label:    "Open",
		Keymap: true,
	},
	{
		Mode:     commandModePlaylistManager | commandModeQueue,
		Keys:     []string{"esc", "q"},
		KeyLabel: "Esc",
		Label:    "Back",
		Keymap: true,
	},
	{
		Mode:        commandModeQueue,
		Keys:        []string{"d"},
		KeyLabel:    "d",
		Label:       "Remove",
		Destructive: true,
		Enabled:     func(m Model) bool { return m.playlist != nil && m.playlist.QueueLen() > 0 },
		Keymap: true,
	},
	{
		Mode:        commandModeQueue,
		Keys:        []string{"c"},
		KeyLabel:    "c",
		Label:       "Clear",
		Destructive: true,
		Keymap: true,
	},
	{
		Mode:     commandModeQueue,
		Keys:     []string{"shift+up", "shift+down"},
		KeyLabel: "Shift+Up Down",
		Label:    "Reorder",
		Keymap:   true,
	},
	{
		Mode:     commandModeKeymapSearch | commandModeFileBrowserSearch | commandModeNavSearch | commandModePlaylistManagerInput | commandModePlaylistPickerInput | commandModeSearch | commandModeNetSearch | commandModeSearchOverlay | commandModeJump | commandModeURL | commandModeAuthCode | commandModeProviderSearch,
		Keys:     []string{"esc"},
		KeyLabel: "Esc",
		Label:    "Cancel",
		Keymap: true,
	},
	{
		Mode:     commandModeKeymapSearch | commandModeFileBrowserSearch | commandModeNavSearch | commandModePlaylistManagerInput | commandModePlaylistPickerInput | commandModeSearch | commandModeNetSearch | commandModeSearchOverlay | commandModeJump | commandModeURL | commandModeAuthCode | commandModeProviderSearch,
		Keys:     []string{"enter"},
		KeyLabel: "Enter",
		Label:    "Confirm",
		Keymap: true,
	},
	{
		Mode:     commandModeSearch,
		Keys:     []string{"tab"},
		KeyLabel: "Tab",
		Label:    "Toggle queue for result",
		Keymap:   true,
	},

	// Shared text editing is reserved even though these are intentionally absent
	// from the global keymap, where they would be misleading outside a field.
	{
		Mode:     commandModeKeymapSearch | commandModeFileBrowserSearch | commandModeNavSearch | commandModePlaylistManagerInput | commandModePlaylistPickerInput | commandModeSearch | commandModeNetSearch | commandModeSearchOverlay | commandModeJump | commandModeURL | commandModeAuthCode | commandModeThemePickerFilter | commandModeVisPickerFilter | commandModeProviderSearch,
		Keys:     []string{"left", "right", "home", "end", "ctrl+a", "ctrl+e", "backspace", "delete", "ctrl+w", "ctrl+u"},
		KeyLabel: "Text editor",
		Label:    "Move cursor and delete text",
	},
	{
		Mode:     commandModeNetSearch,
		Keys:     []string{"f"},
		KeyLabel: "f",
		Label:    "Favorite track",
		Enabled: func(m Model) bool {
			return m.favStore != nil && m.netSearch.screen == netSearchResults && !m.netSearch.loading && len(m.netSearch.results) > 0
		},
		Keymap: true,
	},
	{
		Mode:     commandModeSearchOverlay,
		Keys:     []string{"f"},
		KeyLabel: "f",
		Label:    "Favorite album",
		Enabled: func(m Model) bool {
			_, ok := m.searchOverlay.prov.(provider.FavoriteToggler)
			return ok && m.searchOverlay.screen == searchOverlayResults && !m.searchOverlayBusy() &&
				m.searchOverlay.cursor >= 0 && m.searchOverlay.cursor < len(m.searchOverlay.results) &&
				m.searchOverlay.results[m.searchOverlay.cursor].IsAlbum() && m.searchOverlay.results[m.searchOverlay.cursor].AlbumID() != ""
		},
		Keymap: true,
	},
	{
		Mode:     commandModeSearchOverlay,
		Keys:     []string{"f"},
		KeyLabel: "f",
		Label:    "Favorite track",
		Enabled: func(m Model) bool {
			return m.favStore != nil && m.searchOverlay.screen == searchOverlayResults && !m.searchOverlayBusy() &&
				m.searchOverlay.cursor >= 0 && m.searchOverlay.cursor < len(m.searchOverlay.results) &&
				!m.searchOverlay.results[m.searchOverlay.cursor].IsAlbum()
		},
		Keymap: true,
	},
	{
		Mode:     commandModeSearchOverlay,
		Keys:     []string{"p"},
		KeyLabel: "p",
		Label:    "Save to a playlist",
		Keymap:   true,
		Enabled: func(m Model) bool {
			return m.searchOverlay.screen == searchOverlayResults && !m.searchOverlayBusy() &&
				m.searchOverlay.cursor >= 0 && m.searchOverlay.cursor < len(m.searchOverlay.results)
		},
	},
	{
		Mode:     commandModeProvider,
		Keys:     []string{"esc", "backspace", "b"},
		KeyLabel: "Esc",
		RunKey:   "esc",
		Label:    "Back",
		LabelFor: func(m Model) string {
			if m.providerCatalogSearching() {
				return "Clear search"
			}
			return "Back"
		},
		Keymap: true,
	},
	{
		Mode:     commandModeProvider,
		Keys:     []string{"enter"},
		KeyLabel: "Enter",
		Label:    "Load",
		LabelFor: func(m Model) string {
			if m.selectedProviderListIsBrowseEntry() {
				return "Open"
			}
			return "Load"
		},
		Keymap: true,
	},
	{
		Mode:     commandModeProvider,
		Keys:     []string{"l"},
		KeyLabel: "l",
		Label:    "Latest episode, added to the queue",
		Keymap:   true,
		Enabled: func(m Model) bool {
			_, _, ok := m.selectedProviderShow()
			return ok
		},
	},
	{
		Mode:     commandModeProvider,
		Keys:     []string{"a"},
		KeyLabel: "a",
		Label:    "Append every episode",
		Keymap:   true,
		Enabled: func(m Model) bool {
			_, _, ok := m.selectedProviderShow()
			return ok
		},
	},
	{
		Mode:     commandModeProvider,
		Keys:     []string{"f"},
		KeyLabel: "f",
		Label:    "Favorite",
		Enabled: func(m Model) bool {
			_, ok := m.provider.(provider.FavoriteToggler)
			if !ok || m.provPane.loading || m.provPane.cursor < 0 || m.provPane.cursor >= len(m.provPane.lists) || m.selectedProviderListIsBrowseEntry() {
				return false
			}
			if sl, ok := m.provider.(provider.SectionedList); ok {
				return sl.IsFavoritableID(m.provPane.lists[m.provPane.cursor].ID)
			}
			return true
		},
		Keymap: true,
	},
	{
		Mode:     commandModeProvider,
		Keys:     []string{"ctrl+r"},
		KeyLabel: "Ctrl+R",
		Label:    "Refresh provider",
		Keymap:   true,
	},
	{
		Mode:     commandModeProvider,
		Keys:     []string{"/"},
		KeyLabel: "/",
		Label:    "Filter",
		Keymap:   true,
	},
	{
		Mode:     commandModeProvider,
		Keys:     []string{"ctrl+p"},
		KeyLabel: "Ctrl+P",
		Label:    "Pick chats",
		Keymap:   true,
		Enabled: func(m Model) bool {
			_, ok := m.provider.(provider.ChatFilter)
			return ok
		},
	},
	{
		Mode:     commandModeProvider,
		Keys:     []string{"p"},
		KeyLabel: "p",
		Label:    "Playlist",
		Keymap:   true,
		Enabled: func(m Model) bool {
			return m.localProvider != nil
		},
	},

	// k and j type into the provider filter.
	{
		Mode:     commandModeProviderSearch,
		Keys:     []string{"up", "down"},
		KeyLabel: "Up Down",
		Label:    "Navigate",
		Keymap: true,
	},
	{
		Mode:     commandModeSubs,
		Keys:     []string{"up", "down", "j", "k"},
		KeyLabel: "Up Down",
		Label:    "Navigate",
		Keymap:   true,
	},
	{
		Mode:     commandModeSubs,
		Keys:     []string{"esc", "F"},
		KeyLabel: "Esc",
		RunKey:   "esc",
		Label:    "Close",
		Keymap:   true,
	},
	{
		Mode:     commandModeSubs,
		Keys:     []string{"enter"},
		KeyLabel: "Enter",
		Label:    "Append episodes and play first",
		Keymap:   true,
	},
	{
		Mode:     commandModeSubs,
		Keys:     []string{"a"},
		KeyLabel: "a",
		Label:    "Append episodes",
		Keymap:   true,
	},
	{
		Mode:     commandModeSubs,
		Keys:     []string{"q"},
		KeyLabel: "q",
		Label:    "Append and queue episodes",
		Keymap:   true,
	},
	{
		Mode:     commandModeSubs,
		Keys:     []string{"l"},
		KeyLabel: "l",
		Label:    "Latest episode, added to the queue",
		Keymap:   true,
	},
	{
		Mode:     commandModeSubs,
		Keys:     []string{"L"},
		KeyLabel: "L",
		Label:    "Latest from every show",
		Keymap:   true,
	},
	{
		Mode:     commandModeSubs,
		Keys:     []string{"/"},
		KeyLabel: "/",
		Label:    "Filter",
		Keymap:   true,
	},
	{
		Mode:     commandModeSubsFilter,
		Keys:     []string{"esc"},
		KeyLabel: "Esc",
		Label:    "Clear filter",
		Keymap:   true,
	},
	{
		Mode:     commandModeSubsFilter,
		Keys:     []string{"enter"},
		KeyLabel: "Enter",
		Label:    "Apply filter",
		Keymap:   true,
	},
	{
		Mode:     commandModeNavBrowser | commandModePlaylistManager | commandModePlaylistPicker | commandModeQueue | commandModeDevicePicker,
		Keys:     []string{"up", "down", "k", "j"},
		KeyLabel: "Up Down",
		Label:    "Navigate",
		Keymap: true,
	},
	{
		Mode:     commandModeNavBrowser,
		Keys:     []string{"esc"},
		KeyLabel: "Esc",
		Label:    "Back",
		Keymap: true,
	},
	{
		Mode:     commandModeNavBrowser | commandModePlaylistPicker | commandModeDevicePicker,
		Keys:     []string{"enter"},
		KeyLabel: "Enter",
		Label:    "Select",
		Keymap: true,
	},
	{
		Mode:     commandModeNavBrowser,
		Keys:     []string{"f"},
		KeyLabel: "f",
		Label:    "Favorite album",
		Enabled: func(m Model) bool {
			_, ok := m.navBrowser.prov.(provider.FavoriteToggler)
			idx := m.selectedNavRawIndex(len(m.navBrowser.albums))
			return ok && m.navView() == navViewAlbums && !m.navBrowser.loading && !m.navBrowser.albumLoading &&
				idx >= 0 && m.navBrowser.albums[idx].ID != ""
		},
		Keymap: true,
	},
	{
		Mode:     commandModeNavBrowser,
		Keys:     []string{"f"},
		KeyLabel: "f",
		Label:    "Favorite track",
		Enabled: func(m Model) bool {
			return m.favStore != nil && m.navView() == navViewTracks && !m.navBrowser.confirmReplace &&
				m.selectedNavRawIndex(len(m.navBrowser.tracks)) >= 0
		},
		Keymap: true,
	},
	{
		Mode:     commandModeNavBrowser,
		Keys:     []string{"f"},
		KeyLabel: "f",
		Label:    "Favorite genre",
		LabelFor: func(m Model) string {
			if genre, ok := m.selectedNavGenre(); ok && genre.Favorite {
				return "Unfavorite genre"
			}
			return "Favorite genre"
		},
		Enabled: func(m Model) bool {
			_, canFavorite := m.navGenreBrowser().(provider.GenreFavoriteToggler)
			return canFavorite && m.navBrowser.mode == navBrowseModeByGenre && m.navBrowser.screen == navBrowseScreenList && !m.navBrowser.loading
		},
		Keymap: true,
	},
	{
		Mode:     commandModeNavBrowser,
		Keys:     []string{"s"},
		KeyLabel: "s",
		Label:    "Sort albums",
		Keymap:   true,
		Enabled: func(m Model) bool {
			if m.navBrowser.mode != navBrowseModeByAlbum || m.navBrowser.screen != navBrowseScreenList {
				return false
			}
			_, ok := m.navBrowser.prov.(provider.AlbumBrowser)
			return ok && !m.navBrowser.albumLoading
		},
	},
	{
		Mode:        commandModeNavBrowser,
		Keys:        []string{"R"},
		KeyLabel:    "R",
		Label:       "Replace queue",
		Destructive: true,
		Enabled:     func(m Model) bool { return m.navView() == navViewTracks },
		Keymap: true,
	},
	{
		Mode:     commandModeNavBrowser,
		Keys:     []string{"/"},
		KeyLabel: "/",
		Label:    "Filter",
		Enabled:  func(m Model) bool { return m.navBrowser.mode != navBrowseModeMenu },
		Keymap: true,
	},
	{
		Mode:     commandModeKeymap | commandModeFileBrowser | commandModePlaylistPicker,
		Keys:     []string{"esc", "q"},
		KeyLabel: "Esc",
		Label:    "Back",
		Keymap: true,
	},
	{
		Mode:     commandModeFileBrowser,
		Keys:     []string{"enter"},
		KeyLabel: "Enter",
		Label:    "Add to queue",
		Keymap: true,
	},
	{
		Mode:     commandModeFileBrowser,
		Keys:     []string{"space"},
		KeyLabel: "Space",
		Label:    "Select",
		Keymap:   true,
	},
	{
		Mode:        commandModeFileBrowser,
		Keys:        []string{"R"},
		KeyLabel:    "R",
		Label:       "Replace queue",
		Destructive: true,
		Enabled: func(m Model) bool {
			return m.fileBrowser.targetPlaylist == "" && len(m.fileBrowser.selected) > 0
		},
		Keymap: true,
	},
	{
		Mode:     commandModeFileBrowser,
		Keys:     []string{"w"},
		KeyLabel: "w",
		Label:    "Save to playlist",
		Keymap:   true,
		Enabled: func(m Model) bool {
			return m.fileBrowser.targetPlaylist == "" && len(m.fileBrowser.selected) > 0
		},
	},
	{
		Mode:     commandModeFileBrowser,
		Keys:     []string{"/"},
		KeyLabel: "/",
		Label:    "Filter",
		Keymap:   true,
	},
	{
		Mode:     commandModeFileBrowser,
		Keys:     []string{"D"},
		KeyLabel: "D",
		Label:    "Add as dir source",
		Enabled: func(m Model) bool {
			return m.fileBrowser.visible && m.fileBrowser.targetPlaylist != ""
		},
		Keymap: true,
	},
	{
		Mode:     commandModeFileBrowser,
		Keys:     []string{"a"},
		KeyLabel: "a",
		Label:    "Select/unselect all audio",
		Keymap:   true,
	},
	{
		Mode:     commandModePlaylistManager,
		Keys:     []string{"enter"},
		KeyLabel: "Enter",
		Label:    "Play",
		Enabled: func(m Model) bool {
			return m.plManager.visible && m.plManager.screen == plMgrScreenTracks
		},
		Keymap: true,
	},
	{
		Mode:     commandModePlaylistManager,
		Keys:     []string{"enter"},
		KeyLabel: "Enter",
		Label:    "Select",
		Enabled: func(m Model) bool {
			return m.plManager.visible && m.plManager.screen == plMgrScreenList
		},
		Keymap: true,
	},
	{
		Mode:     commandModePlaylistManager,
		Keys:     []string{"space"},
		KeyLabel: "Space",
		Label:    "Mark",
		Enabled: func(m Model) bool {
			return m.plManager.visible && m.plManager.screen == plMgrScreenTracks
		},
		Keymap: true,
	},
	{
		Mode:     commandModePlaylistManager,
		Keys:     []string{"A"},
		KeyLabel: "A",
		Label:    "Add to the current playlist",
		Keymap:   true,
	},
	{
		Mode:     commandModePlaylistManager,
		Keys:     []string{"[", "]"},
		KeyLabel: "[ ]",
		Label:    "Reorder",
		Enabled: func(m Model) bool {
			return m.plManager.visible && m.plManager.screen == plMgrScreenTracks
		},
		Keymap: true,
	},
	{
		Mode:        commandModePlaylistManager,
		Keys:        []string{"d"},
		KeyLabel:    "d",
		Label:       "Remove",
		Destructive: true,
		Keymap:      true,
		Enabled: func(m Model) bool {
			return m.plManager.visible && m.plManager.screen == plMgrScreenTracks
		},
	},
	{
		Mode:     commandModePlaylistManager,
		Keys:     []string{"o"},
		KeyLabel: "o",
		Label:    "Add files",
		Keymap:   true,
		Enabled: func(m Model) bool {
			return m.plManager.visible && m.plManager.screen == plMgrScreenTracks
		},
	},
	{
		Mode:     commandModePlaylistManager,
		Keys:     []string{"/"},
		KeyLabel: "/",
		Label:    "Filter",
		Enabled: func(m Model) bool {
			return m.plManager.visible && !m.plManager.filtering &&
				(m.plManager.screen == plMgrScreenList || m.plManager.screen == plMgrScreenTracks)
		},
	},
	{
		Mode:     commandModePlaylistManager,
		Keys:     []string{"D"},
		KeyLabel: "D",
		Label:    "Add dir sources",
		Enabled: func(m Model) bool {
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
		},
		Keymap: true,
	},
	{
		Mode:     commandModePlaylistManager,
		Keys:     []string{"f"},
		KeyLabel: "f",
		Label:    "Favorite track",
		Enabled: func(m Model) bool {
			return m.plManager.visible && m.plManager.screen == plMgrScreenTracks && m.favStore != nil
		},
		Keymap: true,
	},
	{
		Mode:     commandModePlaylistManager,
		Keys:     []string{"a"},
		KeyLabel: "a",
		Label:    "Mark/unmark all",
		Keymap:   true,
		Enabled: func(m Model) bool {
			return m.plManager.visible && m.plManager.screen == plMgrScreenTracks
		},
	},
	{
		Mode:     commandModePlaylistManager,
		Keys:     []string{"a"},
		KeyLabel: "a",
		Label:    "New playlist",
		Enabled: func(m Model) bool {
			return m.plManager.visible && m.plManager.screen == plMgrScreenList
		},
		Keymap: true,
	},
	{
		Mode:     commandModePlaylistManager,
		Keys:     []string{"p"},
		KeyLabel: "p",
		Label:    "Play all from top",
		Keymap:   true,
		Enabled: func(m Model) bool {
			return m.plManager.visible && m.plManager.screen == plMgrScreenTracks
		},
	},
	{
		Mode:     commandModePlaylistManager,
		Keys:     []string{"w"},
		KeyLabel: "w",
		Label:    "Save to playlist",
		Keymap:   true,
		Enabled: func(m Model) bool {
			return m.plManager.visible && m.plManager.screen == plMgrScreenTracks
		},
	},
	{
		Mode:     commandModePlaylistManager,
		Keys:     []string{"s"},
		KeyLabel: "s",
		Label:    "Sort",
		Keymap:   true,
		Enabled: func(m Model) bool {
			return m.plManager.visible && m.plManager.screen == plMgrScreenTracks
		},
	},
	{
		Mode:     commandModePlaylistManager,
		Keys:     []string{"u"},
		KeyLabel: "u",
		Label:    "Undo",
		Keymap:   true,
		Enabled: func(m Model) bool {
			return m.plManager.visible && m.plManager.screen == plMgrScreenTracks
		},
	},
	{
		Mode:     commandModePlaylistManagerDirs,
		Keys:     []string{"esc", "backspace", "h", "left", "q"},
		KeyLabel: "Esc",
		Label:    "Back to tracks",
		Keymap: true,
	},
	{
		Mode:     commandModePlaylistManagerDirs,
		Keys:     []string{"a"},
		KeyLabel: "a",
		Label:    "Add dir",
		Keymap: true,
	},
	{
		Mode:        commandModePlaylistManagerDirs,
		Keys:        []string{"d"},
		KeyLabel:    "d",
		Label:       "Remove",
		Destructive: true,
		Keymap: true,
	},
	{
		Mode:     commandModePlaylistManagerDirs,
		Keys:     []string{"r"},
		KeyLabel: "r",
		Label:    "Toggle recursive",
		Keymap: true,
	},
	{
		Mode:     commandModePlaylistManagerDirs,
		Keys:     []string{"up", "down", "k", "j"},
		KeyLabel: "Up Down",
		Label:    "Navigate",
		Keymap: true,
	},
	{
		Mode:     commandModePlaylistPicker,
		Keys:     []string{"p"},
		KeyLabel: "p",
		Label:    "Add to the start instead",
		Keymap:   true,
	},
	{
		Mode:     commandModeKeymap,
		Keys:     []string{"enter"},
		KeyLabel: "Enter",
		Label:    "Run",
		Keymap: true,
	},
	{
		Mode:     commandModeKeymap,
		Keys:     []string{"/"},
		KeyLabel: "/",
		Label:    "Filter",
		Keymap: true,
	},
	{
		Mode:     commandModeLyrics,
		Keys:     []string{"esc", "q"},
		KeyLabel: "Esc",
		Label:    "Close",
		Keymap: true,
	},
	{
		Mode:     commandModeLyrics,
		Keys:     []string{"r"},
		KeyLabel: "r",
		Label:    "Retry",
		Enabled:  func(m Model) bool { return !m.lyrics.loading && (m.lyrics.err != nil || len(m.lyrics.lines) == 0) },
		Keymap: true,
	},
	{
		Mode:     commandModeLyrics,
		Keys:     []string{"[", "]"},
		KeyLabel: "[ ]",
		Label:    "Sync offset (−/+250 ms)",
		Keymap:   true,
		Enabled:  func(m Model) bool { return m.lyricsSyncable() && m.lyricsHaveTimestamps() },
	},
	{
		Mode:     commandModeThemePicker,
		Keys:     []string{"esc", "q"},
		KeyLabel: "Esc",
		Label:    "Cancel preview",
		Keymap: true,
	},
	{
		Mode:     commandModeThemePicker,
		Keys:     []string{"enter"},
		KeyLabel: "Enter",
		Label:    "Apply theme",
		Keymap: true,
	},
	{
		Mode:     commandModeThemePicker,
		Keys:     []string{"/"},
		KeyLabel: "/",
		Label:    "Filter",
		Keymap: true,
	},
	{
		Mode:     commandModeThemePicker,
		Keys:     []string{"up", "down", "k", "j"},
		KeyLabel: "Up Down",
		Label:    "Preview",
		Keymap: true,
	},
	{
		Mode:     commandModeThemePickerFilter | commandModeVisPickerFilter,
		Keys:     []string{"esc"},
		KeyLabel: "Esc",
		Label:    "Cancel filter",
		Keymap: true,
	},
	{
		Mode:     commandModeThemePickerFilter | commandModeVisPickerFilter,
		Keys:     []string{"enter"},
		KeyLabel: "Enter",
		Label:    "Finish filter",
		Keymap: true,
	},
	{
		Mode:     commandModeDevicePicker,
		Keys:     []string{"esc", "q"},
		KeyLabel: "Esc",
		Label:    "Back",
		Keymap: true,
	},
	{
		Mode:     commandModeInfo,
		Keys:     []string{"esc", "q"},
		KeyLabel: "Esc",
		Label:    "Close",
		Keymap: true,
	},
	{
		Mode:     commandModeInfo,
		Keys:     []string{"ctrl+i"},
		KeyLabel: "Ctrl+I",
		Label:    "Track info",
		Keymap:   true,
	},
	{
		Mode:     commandModeVisPicker,
		Keys:     []string{"esc", "q"},
		KeyLabel: "Esc",
		Label:    "Cancel preview",
		Keymap: true,
	},
	{
		Mode:     commandModeVisPicker,
		Keys:     []string{"enter"},
		KeyLabel: "Enter",
		Label:    "Save",
		Keymap: true,
	},
	{
		Mode:     commandModeVisPicker,
		Keys:     []string{"/"},
		KeyLabel: "/",
		Label:    "Filter",
		Keymap: true,
	},
	{
		Mode:     commandModeVisPicker,
		Keys:     []string{"up", "down", "k", "j"},
		KeyLabel: "Up Down",
		Label:    "Preview",
		Keymap: true,
	},
	{
		Mode:     commandModeFullVis,
		Keys:     []string{"esc", "backspace", "b", "V", "q"},
		KeyLabel: "Esc",
		RunKey:   "esc",
		Label:    "Exit visualizer",
		Keymap:   true,
	},
	{
		Mode:     commandModeFullVis,
		Keys:     []string{"space"},
		KeyLabel: "Space",
		Label:    "Play / Pause",
		Keymap:   true,
	},
	{
		Mode:     commandModeFullVis,
		Keys:     []string{"t"},
		KeyLabel: "t",
		Label:    "Hide track info",
		Keymap:   true,
	},
	{
		Mode:     commandModeFullVis,
		Keys:     []string{"v"},
		KeyLabel: "v",
		Label:    "Cycle visualizer",
		Keymap:   true,
	},
	{
		Mode:     commandModeFullVis,
		Keys:     []string{">", "."},
		KeyLabel: "> .",
		RunKey:   ">",
		Label:    "Next track",
		Keymap:   true,
	},
	{
		Mode:     commandModeFullVis,
		Keys:     []string{"<", ","},
		KeyLabel: "< ,",
		RunKey:   "<",
		Label:    "Previous track",
		Keymap:   true,
	},
	{
		Mode:     commandModeFullVis,
		Keys:     []string{"left", "right"},
		KeyLabel: "Left Right",
		Label:    "Seek +/-5s",
		Keymap:   true,
	},
	{
		Mode:     commandModeFullVis,
		Keys:     []string{"shift+left", "shift+right"},
		KeyLabel: "Shift+Left Right",
		Label:    "Seek +/-large step",
		Keymap:   true,
	},
	{
		Mode:     commandModeFullVis,
		Keys:     []string{"+", "=", "-"},
		KeyLabel: "+ -",
		Label:    "Volume up/down",
		Keymap:   true,
	},
}

// helpBarKeyOrder lists the keys shown in the help bar for each UI mode, in
// display order. Each key resolves to the first matching command in
// commandRegistry for that mode.
var helpBarKeyOrder = map[commandMode][]string{
	commandModeMain:                 {"esc", "space", "shift+up", "x", "f", "w", "tab"},
	commandModeProvider:             {"esc", "enter", "l", "f", "ctrl+r", "/"},
	commandModeEQ:                   {"esc", "left", "up", "tab"},
	commandModeVolume:               {"esc", "left", "tab"},
	commandModeShuffle:              {"esc", "left", "tab"},
	commandModeRepeat:               {"esc", "left", "tab"},
	commandModeSpeed:                {"esc", "left", "tab"},
	commandModeProviderPill:         {"esc", "left", "enter", "tab"},
	commandModeKeymap:               {"esc", "enter", "/"},
	commandModeKeymapSearch:         {"esc", "enter"},
	commandModeFileBrowser:          {"esc", "space", "enter", "R", "w", "/"},
	commandModeFileBrowserSearch:    {"esc", "enter"},
	commandModeNavBrowser:           {"esc", "enter", "f", "s", "R", "/"},
	commandModeNavSearch:            {"esc", "enter"},
	commandModePlaylistManager:      {"esc", "enter", "space", "A", "[", "d", "o", "/"},
	commandModePlaylistManagerInput: {"esc", "enter"},
	commandModePlaylistManagerDirs:  {"esc", "a", "d", "r"},
	commandModePlaylistPicker:       {"esc", "enter", "p"},
	commandModePlaylistPickerInput:  {"esc", "enter"},
	commandModeQueue:                {"esc", "d", "c", "shift+up"},
	commandModeSearch:               {"esc", "enter", "tab"},
	commandModeNetSearch:            {"esc", "enter", "f"},
	commandModeSearchOverlay:        {"esc", "enter", "f", "p"},
	commandModeJump:                 {"esc", "enter"},
	commandModeURL:                  {"esc", "enter"},
	commandModeAuthCode:             {"esc", "enter"},
	commandModeLyrics:               {"esc", "r", "["},
	commandModeThemePicker:          {"esc", "enter", "/"},
	commandModeVisPicker:            {"esc", "enter", "/"},
	commandModeDevicePicker:         {"esc", "enter"},
	commandModeInfo:                 {"esc", "ctrl+i"},
	commandModeThemePickerFilter:    {"esc", "enter"},
	commandModeVisPickerFilter:      {"esc", "enter"},
	commandModeProviderSearch:       {"esc", "enter"},
	commandModeSubs:                 {"a", "l", "L", "/"},
	commandModeFullVis:              {"esc", "space", "t"},
}

func helpBarCommandForKey(m Model, mode commandMode, key string) (commandSpec, bool) {
	for _, command := range commandRegistry {
		if command.Mode&mode == 0 || !slices.Contains(command.Keys, key) {
			continue
		}
		if !command.enabled(m) {
			continue
		}
		return command, true
	}
	return commandSpec{}, false
}

func (m Model) commandHelp(mode commandMode) string {
	keys := helpBarKeyOrder[mode]
	var commands []commandSpec
	for _, key := range keys {
		if command, ok := helpBarCommandForKey(m, mode, key); ok {
			commands = append(commands, command)
		}
	}
	var helpSuffix string
	if command, ok := m.findCommand(mode, "?"); ok {
		helpSuffix = helpKey(command.KeyLabel, command.label(m))
	}
	return fitHelpBar(commands, m.helpWidth(), m, helpSuffix)
}

// findCommand returns the enabled command that key runs in mode.
func (m Model) findCommand(mode commandMode, key string) (commandSpec, bool) {
	for _, command := range commandRegistry {
		if command.Mode&mode != 0 && slices.Contains(command.Keys, key) && command.enabled(m) {
			return command, true
		}
	}
	return commandSpec{}, false
}

// commandHint renders the command that key runs in mode the way the hint bar
// does. It returns "" when no enabled command matches.
func (m Model) commandHint(mode commandMode, key string) string {
	command, ok := m.findCommand(mode, key)
	if !ok {
		return ""
	}
	return fitHelpLine("  "+helpKey(command.KeyLabel, command.label(m)), m.layout.panelWidth)
}

// pressKeyHint renders "Press <key> <text>" for the command that key runs in
// mode. It returns "" when no enabled command matches.
func (m Model) pressKeyHint(mode commandMode, key, text string) string {
	command, ok := m.findCommand(mode, key)
	if !ok {
		return ""
	}
	return fitHelpLine(dimStyle.Render("  Press ")+helpKeyStyle.Render(" "+command.KeyLabel+" ")+dimStyle.Render(" "+text), m.layout.panelWidth)
}

func (m Model) helpWidth() int {
	if m.layout.panelWidth > 0 {
		return m.layout.panelWidth
	}
	if m.width > 0 {
		return m.width
	}
	return 80
}

func fitHelpBar(commands []commandSpec, width int, m Model, helpSuffix string) string {
	if width <= 0 {
		return ""
	}
	if helpSuffix != "" {
		if w := lipgloss.Width(helpSuffix); w > 0 && width > w {
			width -= w + 1
		}
	}
	var b strings.Builder
	for _, command := range commands {
		entry := helpKey(command.KeyLabel, command.label(m))
		if b.Len() > 0 {
			entry = " " + entry
		}
		if lipgloss.Width(b.String()+entry) > width {
			continue
		}
		b.WriteString(entry)
	}
	out := b.String()
	if helpSuffix != "" {
		if out != "" {
			helpSuffix = " " + helpSuffix
		}
		out += helpSuffix
	}
	return out
}
