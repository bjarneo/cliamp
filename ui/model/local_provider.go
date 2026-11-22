package model

import (
	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/external/local"
	"github.com/bjarneo/cliamp/playlist"
)

// localPaneAction is a UI-only Local provider-pane row handler.
type localPaneAction int

const (
	localPaneNone localPaneAction = iota
	localPaneBrowseFiles
	localPanePlaylistManager
)

func localPaneActionForID(id string) localPaneAction {
	switch id {
	case local.BrowseFilesID:
		return localPaneBrowseFiles
	case local.PlaylistManagerID:
		return localPanePlaylistManager
	default:
		return localPaneNone
	}
}

// showingLocalProvider reports that the provider on screen is the Local
// source. The pill key alone is not enough: a caller can replace Provider
// without moving the pill. Names are compared because a provider value is
// not always comparable.
func (m Model) showingLocalProvider() bool {
	if m.activeProviderKey() != providerKeyLocal || m.provider == nil || m.provPillIdx < 0 || m.provPillIdx >= len(m.providers) {
		return false
	}
	entry := m.providers[m.provPillIdx].Provider
	return entry != nil && entry.Name() == m.provider.Name()
}

func (m Model) selectedLocalPaneAction() localPaneAction {
	if m.provPane.cursor < 0 || m.provPane.cursor >= len(m.provPane.lists) {
		return localPaneNone
	}
	return localPaneActionForID(m.provPane.lists[m.provPane.cursor].ID)
}

func (m *Model) runLocalPaneAction(action localPaneAction) tea.Cmd {
	switch action {
	case localPaneBrowseFiles:
		m.openFileBrowser()
	case localPanePlaylistManager:
		if m.localProvider != nil {
			m.openPlaylistManager()
		}
	}
	return nil
}

// tryOpenLocalFileBrowser opens the file browser when Local is on screen.
// It reports whether the key was handled.
func (m *Model) tryOpenLocalFileBrowser() bool {
	if !m.showingLocalProvider() {
		return false
	}
	m.openFileBrowser()
	return true
}

// tryRestoreLocalFileBrowserFromQueue reopens the file browser after Esc from
// the queue when playback had left a resume point. It reports whether Esc was
// handled.
func (m *Model) tryRestoreLocalFileBrowserFromQueue() bool {
	if !m.showingLocalProvider() || !m.fileBrowser.resumeLocal {
		return false
	}
	m.restoreLocalFileBrowser()
	return true
}

// localBrowseKeyEnabled reports whether N should browse the active provider.
func (m Model) localBrowseKeyEnabled() bool {
	return m.showingLocalProvider() || m.canOpenProviderBrowser()
}

// localBrowseKeyLabel names the N action for the keymap and help bar.
func (m Model) localBrowseKeyLabel() string {
	if m.showingLocalProvider() {
		return "Open local files"
	}
	if _, ok := m.selectedTrackArtistBrowserTarget(); ok {
		return "Browse creator"
	}
	return "Browse provider"
}

// providerEnterLabel names Enter on the provider pane list for the keymap.
func (m Model) providerEnterLabel() string {
	switch m.selectedLocalPaneAction() {
	case localPanePlaylistManager, localPaneBrowseFiles:
		return "Open"
	}
	if m.selectedProviderListIsBrowseEntry() {
		return "Open"
	}
	if _, _, ok := m.selectedProviderShow(); ok {
		return "Open episodes"
	}
	return "Load"
}

// localProviderLists is the fixed Local provider pane: file browser and playlist
// manager shortcuts. Saved playlists live in the manager (p), not in this pane.
func localProviderLists() []playlist.PlaylistInfo {
	return []playlist.PlaylistInfo{
		{ID: local.BrowseFilesID, Name: "Open local files"},
		{ID: local.PlaylistManagerID, Name: "Playlists"},
	}
}

// localProviderBlankLinesBefore counts empty rows the Local pane draws before
// row index. Scroll is the first visible list index.
func localProviderBlankLinesBefore(lists []playlist.PlaylistInfo, index, scroll int) int {
	if index < 0 || index >= len(lists) {
		return 0
	}
	n := 0
	if index == 0 && scroll == 0 {
		n++
	}
	if index > 0 {
		n++
	}
	return n
}
