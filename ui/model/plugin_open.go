package model

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// handlePluginOpenProvider loads a playlist that a plugin resolved, such as a
// bot chat created from the chat list. The provider pane may not contain the
// row yet, so the row is added before the tracks are fetched.
func (m *Model) handlePluginOpenProvider(msg PluginOpenProviderMsg) tea.Cmd {
	if m.provider == nil || msg.Playlist.ID == "" {
		return nil
	}
	if msg.ProviderName != "" && !strings.EqualFold(m.provider.Name(), msg.ProviderName) {
		m.status.Warningf(statusTTLDefault, "Switch to %s first.", msg.ProviderName)
		return nil
	}
	found := false
	for i, row := range m.provPane.lists {
		if row.ID == msg.Playlist.ID {
			m.provPane.lists[i] = msg.Playlist
			m.provPane.cursor = i
			found = true
			break
		}
	}
	if !found {
		m.provPane.lists = append(m.provPane.lists, msg.Playlist)
		m.provPane.cursor = len(m.provPane.lists) - 1
	}
	m.provPane.loading = true
	m.providerOpenPrevID = m.activeProviderPlaylistID
	m.providerOpenPrevSet = true
	m.activeProviderPlaylistID = msg.Playlist.ID
	m.focus = focusPlaylist
	m.applyHeightMode()
	m.adjustScroll()
	m.recomputeLayout()
	m.status.Activityf(statusTTLShort, "Loading %s…", msg.Playlist.Name)
	return m.fetchProviderTracks(msg.Playlist.ID)
}
