package model

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// keymapEntry is a row in the Ctrl+K overlay. Rows with `divider = true` are
// unselectable section headers (e.g. "— plugins —").
type keymapEntry struct {
	key, action string
	divider     bool
	// run is the key that Enter sends to run the entry. When run is empty,
	// Enter shows hint instead.
	run, hint string
}

// keyCodes holds the named keys that commands and plugins bind.
var keyCodes = map[string]rune{
	"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab, "space": tea.KeySpace,
	"backspace": tea.KeyBackspace, "delete": tea.KeyDelete, "insert": tea.KeyInsert,
	"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
	"home": tea.KeyHome, "end": tea.KeyEnd, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
	"f1": tea.KeyF1, "f2": tea.KeyF2, "f3": tea.KeyF3, "f4": tea.KeyF4, "f5": tea.KeyF5, "f6": tea.KeyF6,
	"f7": tea.KeyF7, "f8": tea.KeyF8, "f9": tea.KeyF9, "f10": tea.KeyF10, "f11": tea.KeyF11, "f12": tea.KeyF12,
}

// keyPressFor builds a key press whose String value is key, such as "a",
// "ctrl+x" or "shift+left". It returns false when it cannot build one.
func keyPressFor(key string) (tea.KeyPressMsg, bool) {
	var msg tea.KeyPressMsg
	name := key
	for {
		mod, rest, found := strings.Cut(name, "+")
		if !found || mod == "" || rest == "" {
			break
		}
		switch mod {
		case "ctrl":
			msg.Mod |= tea.ModCtrl
		case "alt":
			msg.Mod |= tea.ModAlt
		case "shift":
			msg.Mod |= tea.ModShift
		default:
			return tea.KeyPressMsg{}, false
		}
		name = rest
	}
	if code, ok := keyCodes[name]; ok {
		msg.Code = code
		if code == tea.KeySpace && msg.Mod == 0 {
			msg.Text = " "
		}
	} else if r, size := utf8.DecodeRuneInString(name); r != utf8.RuneError && size == len(name) {
		msg.Code = r
		if msg.Mod == 0 {
			msg.Text = name
		}
	} else {
		return tea.KeyPressMsg{}, false
	}
	return msg, msg.String() == key
}

// ReservedKeys returns a fresh copy of every key described by commandRegistry.
// It is handed to the Lua plugin manager at startup so plugins cannot shadow
// a core action or an active text field.
func ReservedKeys() map[string]bool {
	out := make(map[string]bool)
	for _, command := range commandRegistry {
		for _, key := range command.Keys {
			out[key] = true
		}
	}
	return out
}

// buildKeymapEntries starts with commands for the screen that opened Ctrl+K,
// then lists global player and library commands. The result is cached on open
// so navigation (which calls keymapCount many times per frame) is allocation-free.
func (m Model) buildKeymapEntries() []keymapEntry {
	out := make([]keymapEntry, 0, len(commandRegistry)+6)
	seen := make(map[string]bool)
	mode, screen := m.keymapContext()
	add := func(command commandSpec) {
		label := command.label(m)
		id := command.KeyLabel + "\x00" + label
		if seen[id] {
			return
		}
		seen[id] = true
		entry := keymapEntry{key: command.KeyLabel, action: label}
		switch run := command.runKey(); {
		case run == "":
			entry.hint = "Close the keymap. Then press " + command.KeyLabel + "."
		case command.Mode&mode == 0:
			entry.hint = label + " is not available in " + screen + "."
		default:
			entry.run = run
		}
		out = append(out, entry)
	}

	if mode != commandModeMain {
		out = append(out, keymapEntry{action: "— current: " + screen + " —", divider: true})
		for _, command := range commandRegistry {
			if command.Mode != commandModeAny && (command.Keymap || command.ContextHelp) && command.enabled(m) && command.Mode&mode != 0 {
				add(command)
			}
		}
		out = append(out, keymapEntry{action: "— player & library —", divider: true})
	}
	for _, command := range commandRegistry {
		if command.Keymap && command.enabled(m) {
			add(command)
		}
	}
	if mode != commandModeMain || m.luaMgr == nil {
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
		out = append(out, keymapEntry{key: b.Key, action: label, run: b.Key})
	}
	return out
}

func (m Model) keymapContext() (commandMode, string) {
	switch m.activeScreen() {
	case screenImmersive:
		return commandModeImmersive, "Immersive"
	case screenTrackMenu:
		return commandModeTrackMenu, "Track Menu"
	case screenCredits:
		return commandModeCredits, "Credits"
	case screenDevicePicker:
		return commandModeDevicePicker, "Audio Device"
	case screenPlaylistPicker:
		if m.plPicker.screen == plPickerNewName {
			return commandModePlaylistPickerInput, "Playlist Name"
		}
		return commandModePlaylistPicker, "Save to Playlist"
	case screenFileBrowser:
		if m.fileBrowser.searching {
			return commandModeFileBrowserSearch, "File Filter"
		}
		return commandModeFileBrowser, "Files"
	case screenSpotSearch:
		return commandModeSpotSearch, "Provider Search"
	case screenArtist:
		return commandModeArtist, "Artist"
	case screenHome:
		if m.home.filtering {
			return commandModeHomeFilter, "Home Filter"
		}
		if m.home.screen == homeScreenNewName {
			return commandModeHomeInput, "New Playlist"
		}
		return commandModeHome, "Home"
	case screenNavBrowser:
		if m.navBrowser.searching {
			return commandModeNavSearch, "Browser Filter"
		}
		return commandModeNavBrowser, "Browse"
	case screenThemePicker:
		if m.themePicker.filtering {
			return commandModeThemePickerFilter, "Theme Filter"
		}
		return commandModeThemePicker, "Themes"
	case screenVisPicker:
		if m.visPicker.filtering {
			return commandModeVisPickerFilter, "Visualizer Filter"
		}
		return commandModeVisPicker, "Visualizers"
	case screenPlaylistManager:
		if m.plManager.screen == plMgrScreenNewName || m.plManager.screen == plMgrScreenRename {
			return commandModePlaylistManagerInput, "Playlist Name"
		}
		return commandModePlaylistManager, "Playlists"
	case screenQueue:
		return commandModeQueue, "Queue"
	case screenSubs:
		if m.subs.filtering {
			return commandModeSubsFilter, "Subscription Filter"
		}
		return commandModeSubs, "Subscriptions"
	case screenInfo:
		return commandModeInfo, "Track Info"
	case screenSearch:
		return commandModeSearch, "Playlist Filter"
	case screenNetSearch:
		return commandModeNetSearch, "Online Search"
	case screenURLInput:
		return commandModeURL, "Load URL"
	case screenLyrics:
		return commandModeLyrics, "Lyrics"
	case screenJump:
		return commandModeJump, "Jump to Time"
	}

	switch m.focus {
	case focusProvider:
		if m.provSearch.active {
			return commandModeProviderSearch, "Provider Filter"
		}
		return commandModeProvider, "Provider"
	case focusEQ:
		return commandModeEQ, "Equalizer"
	case focusVolume:
		return commandModeVolume, "Volume"
	case focusShuffle:
		return commandModeShuffle, "Shuffle"
	case focusRepeat:
		return commandModeRepeat, "Repeat"
	case focusSpeed:
		return commandModeSpeed, "Speed"
	case focusProvPill:
		return commandModeProviderPill, "Source"
	default:
		return commandModeMain, "Playlist"
	}
}

func (m *Model) keymapCount() int {
	if m.keymap.searching || m.keymap.search != "" {
		return len(m.keymap.filtered)
	}
	return len(m.keymap.entries)
}

func (m *Model) keymapHelpLine() string {
	if m.keymap.searching {
		return m.commandHelp(commandModeKeymapSearch)
	}
	return m.commandHelp(commandModeKeymap)
}

// keymapHeaderLine renders the keymap's single-line header for the playlist
// region: the filter prompt while searching/filtered, otherwise a labeled
// separator with the match count.
func (m Model) keymapHeaderLine() string {
	if m.keymap.searching || m.keymap.search != "" {
		return m.filterHeader("Filter: Keymap", "keymap", m.keymap.search, fmt.Sprintf("%d/%d", m.keymapCount(), len(m.keymap.entries)))
	}
	return sepHeaderN("Keymap", m.keymap.cursor+1, len(m.keymap.entries))
}

func (m *Model) keymapVisible() int {
	return m.effectivePlaylistVisible()
}

// keymapMaybeAdjustScroll keeps the cursor visible in the current keymap window.
func (m *Model) keymapMaybeAdjustScroll(visible int) {
	clampScroll(&m.keymap.cursor, &m.keymap.scroll, m.keymapCount(), visible)
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
	// The keymap now renders in the playlist region; recompute chrome so its
	// header/help are reflected in the visible-row budget, then fit the cursor.
	m.refreshChrome()
	m.applyHeightMode()
	m.keymapMaybeAdjustScroll(m.keymapVisible())
}

// closeKeymap hides the keymap, clears its filter state, and restores playlist
// sizing after the inline header and help line are dismissed.
func (m *Model) closeKeymap() {
	m.keymap.visible = false
	m.keymap.searching = false
	m.keymap.search = ""
	m.keymap.filtered = nil
	m.refreshChrome()
	m.applyHeightMode()
	m.adjustScroll()
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
		if m.keymap.search == "" {
			m.keymap.searching = false
			m.keymap.cursor = m.keymap.savedCursor
			m.keymap.scroll = m.keymap.savedScroll
			return nil
		}
	}

	if m.editText("keymap", &m.keymap.search, msg) {
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

	case "esc", "ctrl+k", "?", "q":
		m.closeKeymap()

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
		m.toggleExpandedView()
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
			m.closeKeymap()
		}

	case "enter", "l":
		return m.runKeymapEntry()
	}

	return nil
}

// selectedKeymapEntry returns the entry under the keymap cursor.
func (m *Model) selectedKeymapEntry() (keymapEntry, bool) {
	idx := m.keymap.cursor
	if m.keymap.search != "" {
		if idx < 0 || idx >= len(m.keymap.filtered) {
			return keymapEntry{}, false
		}
		idx = m.keymap.filtered[idx]
	}
	if idx < 0 || idx >= len(m.keymap.entries) {
		return keymapEntry{}, false
	}
	return m.keymap.entries[idx], true
}

// runKeymapEntry runs the selected command. It closes the keymap and sends
// the command key to handleKey, so the command acts as if the user pressed
// the key on the screen that opened the keymap. An entry that one key press
// cannot run keeps the keymap open and shows its hint.
func (m *Model) runKeymapEntry() tea.Cmd {
	entry, ok := m.selectedKeymapEntry()
	if !ok || entry.divider {
		return nil
	}
	msg, ok := keyPressFor(entry.run)
	if !ok {
		hint := entry.hint
		if hint == "" {
			hint = "Close the keymap. Then press " + entry.key + "."
		}
		m.status.Warning(hint, statusTTLMedium)
		return nil
	}
	m.closeKeymap()
	return m.handleKey(msg)
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

// renderKeymapList renders the keymap entries for the playlist region while the
// keymap is open. The header and help line are supplied by the main layout
// (renderPlaylistHeader / renderHelp), mirroring renderVisPickerList.
func (m Model) renderKeymapList() string {
	budget := m.effectivePlaylistVisible()
	if budget <= 0 {
		return ""
	}

	entries := m.keymap.entries
	var visible []keymapEntry
	if m.keymap.search != "" {
		for _, i := range m.keymap.filtered {
			visible = append(visible, entries[i])
		}
	} else {
		visible = entries
	}

	if len(visible) == 0 {
		msg := "(empty)"
		if m.keymap.search != "" {
			msg = "No matches"
		}
		return strings.Join(fitLines([]string{dimStyle.Render("  " + msg)}, budget), "\n")
	}

	lines := make([]string, 0, budget)
	for i := m.keymap.scroll; i < len(visible) && len(lines) < budget; i++ {
		entry := visible[i]
		if entry.divider {
			lines = append(lines, dimStyle.Render("  "+entry.action))
			continue
		}
		line := fmt.Sprintf("%-10s %s", entry.key, entry.action)
		if m.keymap.searching {
			lines = append(lines, dimStyle.Render("  "+line))
		} else {
			lines = append(lines, cursorLine(line, i == m.keymap.cursor))
		}
	}
	return strings.Join(padLines(lines, budget, len(lines)), "\n")
}
