package model

import tea "charm.land/bubbletea/v2"

// handleNetSearchResults shows the results of a net search.
func (m *Model) handleNetSearchResults(msg netSearchResultsMsg) {
	if msg.gen != m.requests.netSearch || !m.netSearch.active || msg.query != m.netSearch.request {
		return
	}
	m.netSearch.loading = false
	m.netSearch.cursor = 0
	m.netSearch.scroll = 0
	if msg.err != nil {
		m.netSearch.err = msg.err.Error()
		return
	}
	m.netSearch.results = msg.tracks
	m.netSearch.cursor = 0
	m.netSearch.screen = netSearchResults
	if len(msg.tracks) == 0 {
		m.netSearch.err = "No results found"
	}
	m.applyHeightMode()
	m.clampActiveScrollState()
}

// handleSearchOverlayResults shows the track results of a provider search.
func (m *Model) handleSearchOverlayResults(msg searchOverlayResultsMsg) {
	if !m.isCurrentSearchOverlayRequest(msg.gen, msg.providerName) || m.searchOverlay.query != msg.query {
		return
	}
	m.cancelSearchOverlayRequest()
	m.searchOverlay.loading = false
	m.searchOverlay.cursor = 0
	m.searchOverlay.scroll = 0
	if msg.err != nil {
		m.setSearchOverlayError(msg.err.Error())
		return
	}
	m.searchOverlay.results = msg.tracks
	m.searchOverlay.cursor = 0
	m.searchOverlay.screen = searchOverlayResults
	m.applyHeightMode()
	m.clampActiveScrollState()
}

// handleSearchOverlayAlbumTracks plays, appends or queues the tracks of an album
// that the provider search expanded.
func (m *Model) handleSearchOverlayAlbumTracks(msg searchOverlayAlbumTracksMsg) tea.Cmd {
	if msg.gen != m.requests.searchOverlayAlbum {
		return nil
	}
	m.cancelSearchOverlayRequest()
	m.searchOverlay.albumLoading = false
	if msg.err != nil {
		m.setSearchOverlayError(msg.err.Error())
		return nil
	}
	if len(msg.tracks) == 0 {
		m.setSearchOverlayError("That album has no tracks available here.")
		return nil
	}
	album := msg.album
	tracks := msg.tracks
	m.closeSearchOverlay()
	switch msg.action {
	case searchOverlayAlbumAppend:
		return m.appendAlbum(album, tracks)
	case searchOverlayAlbumQueueNext:
		return m.queueAlbumNext(album, tracks)
	default:
		return m.playAlbumImmediate(album, tracks)
	}
}

// handleSearchOverlayPlaylists shows the playlists that can take a provider search
// result.
func (m *Model) handleSearchOverlayPlaylists(msg searchOverlayPlaylistsMsg) {
	if !m.isCurrentSearchOverlayListRequest(msg.gen, msg.providerName) {
		return
	}
	m.searchOverlay.loading = false
	m.searchOverlay.cursor = 0
	m.searchOverlay.scroll = 0
	if msg.err != nil {
		m.setSearchOverlayError(msg.err.Error())
		return
	}
	m.searchOverlay.playlists = msg.playlists
	m.searchOverlay.cursor = 0
	m.searchOverlay.screen = searchOverlayPlaylist
	m.applyHeightMode()
	m.clampActiveScrollState()
}

// handleSearchOverlayAdded reports the add of a provider search result to a
// playlist.
func (m *Model) handleSearchOverlayAdded(msg searchOverlayAddedMsg) {
	if !m.isCurrentSearchOverlayMutation(msg.gen, msg.providerName) {
		return
	}
	m.cancelSearchOverlayRequest()
	m.searchOverlay.loading = false
	if msg.err != nil {
		m.setSearchOverlayError("Add failed: " + msg.err.Error())
		return
	}
	m.status.Showf(statusTTLDefault, "Added to %q", msg.name)
	m.closeSearchOverlay()
}

// handleSearchOverlayCreated reports a playlist that the provider search created for
// a track.
func (m *Model) handleSearchOverlayCreated(msg searchOverlayCreatedMsg) {
	if !m.isCurrentSearchOverlayMutation(msg.gen, msg.providerName) {
		return
	}
	m.cancelSearchOverlayRequest()
	m.searchOverlay.loading = false
	if msg.err != nil {
		m.setSearchOverlayError("Create failed: " + msg.err.Error())
		return
	}
	m.status.Showf(statusTTLDefault, "Created %q & added track", msg.name)
	m.closeSearchOverlay()
}
