package model

import (
	"errors"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// handlePlaylistsLoaded shows the lists of the active provider, or asks for
// sign-in when the provider needs it.
func (m *Model) handlePlaylistsLoaded(msg playlistsLoadedMsg) tea.Cmd {
	if msg.gen != m.requests.provider || !m.isActiveProvider(msg.providerName) {
		return nil
	}
	m.provPane.loading = m.provSearch.loading
	if msg.err != nil {
		if errors.Is(msg.err, playlist.ErrNeedsAuth) {
			m.provPane.signIn = true
			m.err = nil
			return nil
		}
		if len(msg.playlists) == 0 {
			m.err = msg.err
			return nil
		}
		m.err = nil
		m.status.Warningf(statusTTLLong, "%s", msg.err)
	}
	m.replaceProviderLists(msg.playlists)
	return m.startCatalogLoading()
}

// handleTracksLoaded puts a loaded provider list, or one page of it, in the
// queue. It asks for the next page while more pages exist.
func (m *Model) handleTracksLoaded(msg tracksLoadedMsg) tea.Cmd {
	if msg.gen != m.requests.tracks || !m.isActiveProvider(msg.providerName) {
		return nil
	}
	m.provPane.loading = false
	m.tracksPaging = msg.err == nil && msg.next > 0
	if msg.err != nil {
		if errors.Is(msg.err, playlist.ErrNeedsAuth) {
			m.provPane.signIn = true
			m.err = nil
			return nil
		}
		if errors.Is(msg.err, playlist.ErrListChanged) {
			// The list moved under a paged read, so what is on screen is a
			// partial view of a list that no longer exists. Say so and let it
			// expire: reopening starts a clean load, and a persistent error
			// would sit in front of every later status message.
			m.status.Warningf(statusTTLDefault, "Playlist changed while loading — reopen current playlist to reload")
			return nil
		}
		m.err = msg.err
		return nil
	}
	if msg.offset > 0 {
		m.playlist.Add(msg.tracks...)
		m.normalizeQueueOverlay()
		m.addToHeaderState(msg.tracks)
		// Add mixes the page into the upcoming shuffle order, so an armed
		// preload may no longer be the next track. The gapless swap runs on
		// the audio thread and the model then names the new track from
		// playlist.Next(), so a stale preload would play one track while the
		// UI, scrobble and now-playing announced another. Drop it and let the
		// tick loop re-arm against the order this page produced.
		if m.player.HasPreload() || m.preloading {
			m.player.ClearPreload()
			m.preloading = false
		}
	} else {
		m.replacePlayerPlaylist(msg.tracks)
		if msg.playlistExact {
			m.setLoadedLocalPlaylist(msg.providerName, msg.playlistID)
		}
	}
	if msg.next > 0 {
		m.adjustScroll()
		if pager, ok := m.provider.(provider.TrackPager); ok {
			return fetchTracksPageCmd(pager, msg.providerName, msg.playlistID, msg.next, msg.gen)
		}
	}
	if msg.offset > 0 {
		msg.tracks = m.playlist.Tracks()
	}
	m.applyTracksResume(msg)
	m.adjustScroll()
	return nil
}

// handleCatalogBatch adds a batch of catalog entries to the provider pane.
func (m *Model) handleCatalogBatch(msg catalogBatchMsg) {
	if msg.gen != m.requests.catalog || !m.isActiveProvider(msg.providerName) {
		return
	}
	m.catalogBatch.loading = false
	if msg.err != nil {
		m.catalogBatch.done = true
		m.status.Errorf(statusTTLDefault, "Catalog load failed: %s", msg.err)
		return
	}
	if msg.added == 0 {
		m.catalogBatch.done = true
		return
	}
	if err := m.refreshProviderListsNow(); err != nil {
		m.err = err
	}
	m.catalogBatch.offset += msg.added
	if msg.added < catalogBatchSize {
		m.catalogBatch.done = true
	}
}

// handleCatalogSearch shows the result of a provider catalog search.
func (m *Model) handleCatalogSearch(msg catalogSearchMsg) {
	if msg.gen != m.requests.catalog || !m.isActiveProvider(msg.providerName) {
		return
	}
	m.provPane.loading = false
	m.provSearch.loading = false
	if msg.err != nil {
		m.status.Errorf(statusTTLDefault, "Search failed: %s", msg.err)
	} else {
		if err := m.refreshProviderListsNow(); err != nil {
			m.err = err
		}
		m.provPane.cursor = 0
		m.provPane.scroll = 0
		if msg.count == 0 {
			m.status.Warning("No results found", statusTTLDefault)
		}
	}
}

// handleProvAuthDone loads the provider lists after a sign-in, or keeps the
// sign-in prompt after a failure.
func (m *Model) handleProvAuthDone(msg provAuthDoneMsg) tea.Cmd {
	if msg.gen != m.requests.auth || !m.isActiveProvider(msg.providerName) {
		return nil
	}
	m.provPane.authURL = ""
	if msg.err != nil {
		// Keep the sign-in prompt, so Enter retries without a restart.
		m.err = msg.err
		m.provPane.loading = false
		m.provPane.signIn = true
		return nil
	}
	m.err = nil
	m.provPane.signIn = false
	m.provPane.loading = true
	return m.fetchProviderPlaylists()
}
