package model

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

type playlistPreviewLoadedMsg struct {
	gen    uint64
	tracks []playlist.Track
	err    error
}

func (m Model) canPreviewProviderPlaylist() bool {
	if m.provider == nil || m.provPane.signIn || m.provPane.loading ||
		m.provPane.cursor < 0 || m.provPane.cursor >= len(m.provPane.lists) {
		return false
	}
	_, browseEntry := providerBrowseEntryForID(m.provider, m.provPane.lists[m.provPane.cursor].ID)
	return !browseEntry
}

func (m *Model) previewProviderPlaylist() tea.Cmd {
	if !m.canPreviewProviderPlaylist() {
		return nil
	}
	item := m.provPane.lists[m.provPane.cursor]

	m.cancelNavRequests()
	source := m.provider
	gen := m.nextNavRequest()
	var resolver provider.URLResolver
	var ctx context.Context
	var cancel context.CancelFunc
	if candidate, ok := source.(provider.URLResolver); ok && candidate.CanResolveURL(item.ID) {
		resolver = candidate
		ctx, cancel = context.WithTimeout(context.Background(), 2*time.Minute)
	}
	m.navBrowser = navBrowserState{
		prov: source, visible: true, mode: navBrowseModeByAlbum, screen: navBrowseScreenTracks,
		loading: true, previewName: "Preview: " + item.Name, previewID: item.ID, previewCancel: cancel,
	}
	return func() tea.Msg {
		msg := playlistPreviewLoadedMsg{gen: gen}
		if resolver != nil {
			defer cancel()
			msg.tracks, msg.err = resolver.ResolveURL(ctx, item.ID)
		} else {
			// Provider.Tracks has no context; cancelNavRequests still drops its stale result.
			msg.tracks, msg.err = source.Tracks(item.ID)
		}
		return msg
	}
}

func (m *Model) handlePlaylistPreviewLoaded(msg playlistPreviewLoadedMsg) {
	if m.navBrowser.previewName == "" || !m.isCurrentNavRequest(msg.gen) {
		return
	}
	if m.navBrowser.previewCancel != nil {
		m.navBrowser.previewCancel()
		m.navBrowser.previewCancel = nil
	}
	m.navBrowser.loading = false
	m.navBrowser.tracks = msg.tracks
	if msg.err != nil {
		m.navBrowser.previewErr = msg.err.Error()
		return
	}
	m.navBrowser.cursor, m.navBrowser.scroll = 0, 0
}

func (m *Model) handlePlaylistPreviewKey(msg tea.KeyPressMsg) tea.Cmd {
	if cmd, ok := m.quickSwitchProvider(msg.String()); ok {
		return cmd
	}
	if stepListCursor(msg.String(), &m.navBrowser.cursor, len(m.navBrowser.tracks), m.effectivePlaylistVisible()) {
		return nil
	}
	switch msg.String() {
	case "enter":
		if m.navBrowser.loading || m.navBrowser.previewErr != "" || len(m.navBrowser.tracks) == 0 {
			return nil
		}
		return m.playPlaylistPreview()
	case "esc", "backspace", "q":
		m.cancelNavRequests()
		m.navBrowser.previewName = ""
		m.navBrowser.previewID = ""
		m.navBrowser.visible = false
	}
	return nil
}

func (m *Model) playPlaylistPreview() tea.Cmd {
	index := m.navBrowser.cursor
	tracks := m.navBrowser.tracks
	if index < 0 || index >= len(tracks) {
		return nil
	}
	m.retireTracksPaging()
	m.replacePlayerPlaylist(tracks)
	m.activeProviderPlaylistID = m.navBrowser.previewID
	m.playlist.SetIndex(index)
	m.plCursor = index
	m.adjustScroll()
	m.navBrowser.visible = false
	m.navBrowser.previewName = ""
	m.navBrowser.previewID = ""
	m.status.Showf(statusTTLMedium, "Playing: %s", tracks[index].DisplayName())
	return m.playCurrentTrack()
}
