package model

import tea "charm.land/bubbletea/v2"

// overlaySpec describes one overlay: the screen it shows, the handlers that
// own the keys and pasted text while it is on top, its command mode, and the
// pieces it renders.
type overlaySpec struct {
	screen topLevelScreen
	key    func(*Model, tea.KeyPressMsg) tea.Cmd
	// paste takes pasted text. It is nil when the overlay has no text field.
	// The paste is then dropped.
	paste func(*Model, string)
	// context returns the command mode and the screen name for the help line
	// and the keymap. It is nil for the full-screen visualizer, which draws
	// its own help.
	context func(*Model) (commandMode, string)
	// view is zero for the full-screen visualizer, which replaces the whole
	// frame instead of the playlist region.
	view overlayView
}

// fixedContext returns the context of an overlay that has one command mode.
func fixedContext(mode commandMode, name string) func(*Model) (commandMode, string) {
	return func(*Model) (commandMode, string) { return mode, name }
}

// overlayStack lists the overlays from the top down. The first open overlay
// renders, gets the keys and gets pasted text, so these three always agree.
//
// An overlay that opens from inside another overlay sits above it. The
// playlist picker and the file browser open over the playlist manager, and
// the playlist picker also opens over the file browser. Provider search opens
// over the nav browser, and so does the YouTube search when the provider has
// no search. The other overlays open from the main keys, so a key press does
// not stack them. A message, such as the default provider browser at startup,
// can still open one over another, so the order must be the same on every
// route. The queue sits above the subscriptions overlay because the render
// order already put it there.
var overlayStack []overlaySpec

// init fills overlayStack. The key handlers reach overlayStack again through
// handleKey, so a package-level initializer would form a cycle.
func init() {
	overlayStack = []overlaySpec{
		{
			screen: screenFullVisualizer,
			key:    (*Model).handleFullVisualizerKey,
		},
		{
			screen: screenKeymap,
			key:    (*Model).handleKeymapKey,
			paste: func(m *Model, s string) {
				m.insertText("keymap", &m.keymap.filter, s)
				m.updateKeymapFilter()
			},
			context: func(m *Model) (commandMode, string) {
				if m.keymap.filtering {
					return commandModeKeymapSearch, "Keymap Filter"
				}
				return commandModeKeymap, "Keymap"
			},
			view: overlayView{(*Model).keymapHeaderLine, (*Model).renderKeymapList},
		},
		{
			screen:  screenDevicePicker,
			key:     (*Model).handleDeviceKey,
			context: fixedContext(commandModeDevicePicker, "Audio Device"),
			view:    overlayView{(*Model).deviceHeaderLine, (*Model).renderDeviceBody},
		},
		{
			screen: screenPlaylistPicker,
			key:    (*Model).handlePlaylistPickerKey,
			paste: func(m *Model, s string) {
				if m.plPicker.screen == plPickerNewName {
					m.insertText("playlist-picker-name", &m.plPicker.newName, s)
					m.plPicker.inputErr = ""
				}
			},
			context: func(m *Model) (commandMode, string) {
				if m.plPicker.screen == plPickerNewName {
					return commandModePlaylistPickerInput, "Playlist Name"
				}
				return commandModePlaylistPicker, "Save to Playlist"
			},
			view: overlayView{(*Model).plPickerHeaderLine, (*Model).renderPlaylistPickerBody},
		},
		{
			screen: screenFileBrowser,
			key:    (*Model).handleFileBrowserKey,
			paste: func(m *Model, s string) {
				if m.fileBrowser.filtering {
					m.insertText("file-browser-search", &m.fileBrowser.filter, s)
					m.fbUpdateFilter()
				}
			},
			context: func(m *Model) (commandMode, string) {
				if m.fileBrowser.filtering {
					return commandModeFileBrowserSearch, "File Filter"
				}
				return commandModeFileBrowser, "Files"
			},
			view: overlayView{(*Model).fbHeaderLine, (*Model).renderFileBrowserBody},
		},
		{
			screen: screenSearchOverlay,
			key:    (*Model).handleSearchOverlayKey,
			paste: func(m *Model, s string) {
				switch m.searchOverlay.screen {
				case searchOverlayInput:
					m.insertText("search-overlay", &m.searchOverlay.query, s)
				case searchOverlayNewName:
					m.insertText("search-overlay-playlist-name", &m.searchOverlay.newName, s)
				}
			},
			context: fixedContext(commandModeSearchOverlay, "Provider Search"),
			view:    overlayView{(*Model).searchOverlayHeaderLine, (*Model).renderSearchOverlayBody},
		},
		{
			screen: screenNetSearch,
			key:    (*Model).handleNetSearchKey,
			paste: func(m *Model, s string) {
				if m.netSearch.screen == netSearchInput {
					m.insertText("net-search", &m.netSearch.query, s)
				}
			},
			context: fixedContext(commandModeNetSearch, "Online Search"),
			view:    overlayView{(*Model).netSearchHeaderLine, (*Model).renderNetSearchBody},
		},
		{
			screen: screenNavBrowser,
			key:    (*Model).handleNavBrowserKey,
			paste: func(m *Model, s string) {
				if m.navBrowser.mode != navBrowseModeMenu && m.navBrowser.searching {
					m.insertText("nav-search", &m.navBrowser.search, s)
					m.navBrowser.cursor = 0
					m.navBrowser.scroll = 0
					m.navUpdateSearch()
				}
			},
			context: func(m *Model) (commandMode, string) {
				if m.navBrowser.searching {
					return commandModeNavSearch, "Browser Filter"
				}
				return commandModeNavBrowser, "Browse"
			},
			view: overlayView{(*Model).navHeaderLine, (*Model).renderNavBody},
		},
		{
			screen: screenThemePicker,
			key:    (*Model).handleThemeKey,
			paste: func(m *Model, s string) {
				if m.themePicker.filtering {
					m.insertText("theme-picker-filter", &m.themePicker.filter, s)
					m.themePickerRecomputeFilter()
				}
			},
			context: func(m *Model) (commandMode, string) {
				if m.themePicker.filtering {
					return commandModeThemePickerFilter, "Theme Filter"
				}
				return commandModeThemePicker, "Themes"
			},
			view: overlayView{(*Model).themePickerHeaderLine, (*Model).renderThemeBody},
		},
		{
			screen: screenVisPicker,
			key:    (*Model).handleVisPickerKey,
			paste: func(m *Model, s string) {
				if m.visPicker.filtering {
					m.insertText("visualizer-picker-filter", &m.visPicker.filter, s)
					m.visPickerRecomputeFilter()
				}
			},
			context: func(m *Model) (commandMode, string) {
				if m.visPicker.filtering {
					return commandModeVisPickerFilter, "Visualizer Filter"
				}
				return commandModeVisPicker, "Visualizers"
			},
			view: overlayView{(*Model).visPickerHeaderLine, (*Model).renderVisPickerList},
		},
		{
			screen: screenPlaylistManager,
			key:    (*Model).handlePlaylistManagerKey,
			paste: func(m *Model, s string) {
				switch {
				case m.plManager.screen == plMgrScreenNewName:
					m.insertText("playlist-manager-new-name", &m.plManager.newName, s)
					m.plManager.inputErr = ""
				case m.plManager.screen == plMgrScreenRename:
					m.insertText("playlist-manager-rename", &m.plManager.renameName, s)
					m.plManager.inputErr = ""
				case m.plManager.filtering:
					m.insertText("playlist-manager-filter", &m.plManager.filter, s)
					m.plManager.cursor = 0
					m.plMgrRecomputeFilter()
				}
			},
			context: func(m *Model) (commandMode, string) {
				switch m.plManager.screen {
				case plMgrScreenNewName, plMgrScreenRename:
					return commandModePlaylistManagerInput, "Playlist Name"
				case plMgrScreenDirs:
					return commandModePlaylistManagerDirs, "Directory Sources"
				}
				return commandModePlaylistManager, "Playlist"
			},
			view: overlayView{(*Model).plMgrHeaderLine, (*Model).renderPlMgrBody},
		},
		{
			screen:  screenQueue,
			key:     (*Model).handleQueueKey,
			context: fixedContext(commandModeQueue, "Queue"),
			view: overlayView{
				func(m *Model) string {
					return sepHeaderN("Queue", m.queue.cursor+1, m.playlist.QueueLen(), m.layout.panelWidth)
				},
				(*Model).renderQueueBody},
		},
		{
			screen: screenSubs,
			key:    (*Model).handleSubsKey,
			paste: func(m *Model, s string) {
				if m.subs.filtering {
					m.insertText("subs-filter", &m.subs.filter, s)
					m.updateSubsFilter()
				}
			},
			context: func(m *Model) (commandMode, string) {
				if m.subs.filtering {
					return commandModeSubsFilter, "Subscription Filter"
				}
				return commandModeSubs, "Subscriptions"
			},
			view: overlayView{(*Model).subsHeaderLine, (*Model).renderSubsBody},
		},
		{
			screen:  screenInfo,
			key:     (*Model).handleInfoKey,
			context: fixedContext(commandModeInfo, "Track Info"),
			view:    overlayView{func(m *Model) string { return sepHeader("Track Info", m.layout.panelWidth) }, (*Model).renderInfoBody},
		},
		{
			screen:  screenLyrics,
			key:     (*Model).handleLyricsKey,
			context: fixedContext(commandModeLyrics, "Lyrics"),
			view:    overlayView{func(m *Model) string { return sepHeader("Lyrics", m.layout.panelWidth) }, (*Model).renderLyricsBody},
		},
		{
			screen: screenJump,
			key:    (*Model).handleJumpKey,
			paste: func(m *Model, s string) {
				m.insertText("jump", &m.jump.input, s)
				m.jump.err = ""
			},
			context: fixedContext(commandModeJump, "Jump to Time"),
			view:    overlayView{func(m *Model) string { return sepHeader("Jump to Time", m.layout.panelWidth) }, (*Model).renderJumpBody},
		},
		{
			screen: screenURLInput,
			key:    (*Model).handleURLInputKey,
			paste: func(m *Model, s string) {
				m.insertText("url", &m.urlInput.input, s)
				m.urlInput.err = ""
			},
			context: fixedContext(commandModeURL, "Load URL"),
			view: overlayView{
				func(m *Model) string { return m.promptHeader("url", "Load URL", m.urlInput.input) },
				(*Model).renderURLBody},
		},
		{
			screen: screenSearch,
			key:    (*Model).handleSearchKey,
			paste: func(m *Model, s string) {
				m.insertText("playlist-search", &m.search.query, s)
				m.updateSearch()
			},
			context: fixedContext(commandModeSearch, "Playlist Filter"),
			view:    overlayView{(*Model).searchHeaderLine, (*Model).renderSearchList},
		},
	}
}

// overlayOpen reports whether the overlay that shows screen is open. It is a
// method and not a func field of overlaySpec, so activeScreen can check the
// overlays on each frame without moving the Model to the heap.
func (m *Model) overlayOpen(screen topLevelScreen) bool {
	switch screen {
	case screenFullVisualizer:
		return m.fullVis
	case screenKeymap:
		return m.keymap.visible
	case screenDevicePicker:
		return m.devicePicker.visible
	case screenPlaylistPicker:
		return m.plPicker.visible
	case screenFileBrowser:
		return m.fileBrowser.visible
	case screenSearchOverlay:
		return m.searchOverlay.visible
	case screenNavBrowser:
		return m.navBrowser.visible
	case screenThemePicker:
		return m.themePicker.visible
	case screenVisPicker:
		return m.visPicker.visible
	case screenPlaylistManager:
		return m.plManager.visible
	case screenQueue:
		return m.queue.visible
	case screenSubs:
		return m.subs.visible
	case screenInfo:
		return m.info.visible
	case screenLyrics:
		return m.lyrics.visible
	case screenJump:
		return m.jump.active
	case screenURLInput:
		return m.urlInput.active
	case screenSearch:
		return m.search.active
	case screenNetSearch:
		return m.netSearch.active
	}
	return false
}

// overlayContentFirst reports whether the overlay that shows screen gives its
// list the frame. Like overlayOpen, it is a method so the layout can check it
// on each frame. The visualizer picker keeps the playback chrome for live
// previews. The queue keeps it too: it holds the same tracks as the playlist
// and reads as a view of it.
func (m *Model) overlayContentFirst(screen topLevelScreen) bool {
	switch screen {
	case screenKeymap, screenDevicePicker, screenFileBrowser, screenNavBrowser,
		screenThemePicker, screenSubs, screenSearch:
		return true
	case screenPlaylistPicker:
		return m.plPicker.screen == plPickerChoose
	case screenSearchOverlay:
		return m.searchOverlay.screen == searchOverlayResults || m.searchOverlay.screen == searchOverlayPlaylist
	case screenPlaylistManager:
		return m.plManager.screen == plMgrScreenList || m.plManager.screen == plMgrScreenTracks
	case screenNetSearch:
		return m.netSearch.screen == netSearchResults
	}
	return false
}

// topOverlay returns the open overlay nearest the top of overlayStack.
func (m *Model) topOverlay() (overlaySpec, bool) {
	for _, spec := range overlayStack {
		if m.overlayOpen(spec.screen) {
			return spec, true
		}
	}
	return overlaySpec{}, false
}
