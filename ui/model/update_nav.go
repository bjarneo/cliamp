package model

import "github.com/bjarneo/cliamp/playlist"

// handleNavArtistsLoaded shows the artist list in the nav browser.
func (m *Model) handleNavArtistsLoaded(msg navArtistsLoadedMsg) {
	if !m.isCurrentNavRequest(msg.gen) {
		return
	}
	m.navBrowser.loading = false
	if msg.err != nil {
		m.status.Errorf(statusTTLDefault, "Artist load failed: %s", msg.err)
		return
	}
	m.navBrowser.artists = msg.artists
	m.navBrowser.cursor = 0
	m.navBrowser.scroll = 0
}

// handleNavAlbumsLoaded shows a fresh album list in the nav browser, or
// appends the next page of it.
func (m *Model) handleNavAlbumsLoaded(msg navAlbumsLoadedMsg) {
	if !m.isCurrentNavRequest(msg.gen) {
		return
	}
	m.navBrowser.albumLoading = false
	m.navBrowser.loading = false
	if msg.err != nil {
		m.status.Errorf(statusTTLDefault, "Album load failed: %s", msg.err)
		return
	}
	if msg.offset == 0 {
		// Fresh load (new sort or drill-in): replace the list.
		m.navBrowser.albums = msg.albums
		m.navBrowser.albumDone = false
	} else {
		// Lazy-load page: append.
		m.navBrowser.albums = append(m.navBrowser.albums, msg.albums...)
	}
	if msg.isLast {
		m.navBrowser.albumDone = true
	}
	if msg.offset == 0 {
		m.navBrowser.cursor = 0
		m.navBrowser.scroll = 0
	}
	if m.navBrowser.search != "" {
		m.navUpdateSearch()
	}
}

// handleNavGenresLoaded shows the genre list in the nav browser.
func (m *Model) handleNavGenresLoaded(msg navGenresLoadedMsg) {
	if !m.isCurrentNavRequest(msg.gen) {
		return
	}
	m.navBrowser.loading = false
	if msg.err != nil {
		m.status.Errorf(statusTTLDefault, "Genre load failed: %s", msg.err)
		return
	}
	m.navBrowser.genres = msg.genres
	m.navBrowser.cursor = 0
	m.navBrowser.scroll = 0
}

// handleNavTracksLoaded shows the tracks in the nav browser. When the
// browser opened for the playlist, the tracks replace the queue.
func (m *Model) handleNavTracksLoaded(msg navTracksLoadedMsg) {
	if !m.isCurrentNavRequest(msg.gen) {
		return
	}
	m.navBrowser.loading = false
	if msg.err != nil {
		m.status.Errorf(statusTTLDefault, "Track load failed: %s", msg.err)
		return
	}
	if m.navBrowser.openInPlaylist {
		if len(msg.tracks) == 0 {
			m.status.Warning("No tracks found", statusTTLDefault)
			return
		}
		m.retireTracksPaging()
		m.replacePlayerPlaylist(msg.tracks)
		if pr, ok := m.navBrowser.prov.(playlist.RefreshablePlaylist); ok &&
			m.isActiveProvider(m.navBrowser.prov.Name()) && pr.CanRefreshPlaylist(m.navBrowser.selAlbum.ID) {
			m.activeProviderPlaylistID = m.navBrowser.selAlbum.ID
		}
		m.navBrowser.visible = false
		m.status.Successf(statusTTLDefault, "Replaced queue with %d tracks", len(msg.tracks))
		return
	}
	m.navBrowser.tracks = msg.tracks
	m.setHeaderStateFromTracks(m.navBrowser.tracks)
	m.navBrowser.cursor = 0
	m.navBrowser.scroll = 0
	m.navBrowser.screen = navBrowseScreenTracks
}
