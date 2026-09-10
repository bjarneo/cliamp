package model

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// resetProviderNav resets provider navigation and search state to the top.
func (m *Model) resetProviderNav() {
	nextRequest(&m.requests.provider)
	nextRequest(&m.requests.tracks)
	nextRequest(&m.requests.auth)
	nextRequest(&m.requests.catalog)
	m.provCursor = 0
	m.provScroll = 0
	m.provLoading = true
	m.provSearch.active = false
	m.provSearch.query = ""
	m.provSearch.results = nil
	m.provSearch.cursor = 0
	m.provSearch.scroll = 0
}

// StartInProvider configures the model to begin in the provider browse view.
// Call this from main when no CLI tracks or pending URLs were given.
func (m *Model) StartInProvider() {
	if m.provider != nil {
		m.focus = focusProvider
		m.resetProviderNav()
	}
}

// switchProvider sets the active provider by pill index and fetches its playlists.
func (m *Model) switchProvider(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.providers) {
		return nil
	}
	m.provPillIdx = idx
	m.provider = m.providers[idx].Provider
	m.providerLists = nil
	m.provSignIn = false
	m.catalogBatch = catalogBatchState{}
	m.activeProviderPlaylistID = ""
	m.providerQueueLen = 0
	m.providerQueueLastPath = ""
	m.trackPaging = trackPagingState{}
	m.provConfirm = provConfirmState{}
	m.provRename = provRenameState{}
	m.provListFixup = provListFixupState{}
	m.resetProviderNav()
	m.focus = focusProvider
	return m.fetchProviderPlaylists()
}

func (m *Model) fetchProviderPlaylists() tea.Cmd {
	if m.provider == nil {
		return nil
	}
	return fetchPlaylistsCmd(m.provider, nextRequest(&m.requests.provider))
}

// applyProvListFixup applies and clears the pending post-write list
// adjustment (cursor clamping or reselection) after a playlists refresh.
func (m *Model) applyProvListFixup() {
	fix := m.provListFixup
	m.provListFixup = provListFixupState{}
	if fix.selectID != "" {
		for i, pl := range m.providerLists {
			if pl.ID == fix.selectID {
				m.provCursor = i
				break
			}
		}
		m.providerMaybeAdjustScroll()
		return
	}
	if fix.clampCursor && m.provCursor >= len(m.providerLists) {
		m.provCursor = max(0, len(m.providerLists)-1)
		m.providerMaybeAdjustScroll()
	}
}

func (m *Model) fetchProviderTracks(playlistID string) tea.Cmd {
	if m.provider == nil {
		return nil
	}
	m.trackPaging = trackPagingState{}
	m.provConfirm = provConfirmState{}
	m.provRename = provRenameState{}
	if pager, ok := m.provider.(provider.TrackPager); ok {
		return fetchTracksPageCmd(pager, m.provider.Name(), playlistID, 0, providerTrackPageSize, nextRequest(&m.requests.tracks))
	}
	return fetchTracksCmd(m.provider, playlistID, nextRequest(&m.requests.tracks))
}

func (m Model) isActiveProvider(name string) bool {
	return m.provider != nil && m.provider.Name() == name
}

func (m Model) isCurrentNavRequest(gen uint64) bool {
	return m.navBrowser.visible && gen == m.requests.nav
}

func (m Model) isCurrentSpotProvider(providerName string) bool {
	return m.spotSearch.visible &&
		m.spotSearch.prov != nil &&
		m.spotSearch.prov.Name() == providerName
}

func (m Model) isCurrentSpotRequest(gen uint64, providerName string) bool {
	return m.isCurrentSpotProvider(providerName) && gen == m.requests.spotSearch
}

func (m Model) isCurrentSpotListRequest(gen uint64, providerName string) bool {
	return m.isCurrentSpotProvider(providerName) && gen == m.requests.spotLists
}

func (m Model) isCurrentSpotMutation(gen uint64, providerName string) bool {
	return m.isCurrentSpotProvider(providerName) && gen == m.requests.spotMutation
}

func (m *Model) fetchCatalogBatch(loader provider.CatalogLoader) tea.Cmd {
	if m.provider == nil {
		return nil
	}
	return fetchCatalogBatchCmd(loader, m.catalogBatch.offset, catalogBatchSize, m.provider.Name(), nextRequest(&m.requests.catalog))
}

// quickSwitchProvider closes any browser overlays and jumps to the provider
// matched by key. Use the same Shift+letter shortcuts that switch providers
// from the main pane (S, N, P, J, E, Y, C, M, Q, R, L). Returns nil when the key doesn't
// match a known provider.
func (m *Model) quickSwitchProvider(key string) tea.Cmd {
	provKey := providerKeyForShortcut(key)
	if provKey == "" {
		return nil
	}
	// Close any open overlays so the user lands on the provider pane.
	m.cancelNavRequests()
	m.navBrowser.visible = false
	m.plManager.visible = false
	m.fileBrowser.visible = false
	return m.switchToProvider(provKey)
}

// providerKeyForShortcut maps the Shift+letter provider shortcuts to the
// config key used by switchToProvider, or "" when the key is unrelated.
func providerKeyForShortcut(key string) string {
	switch key {
	case "S":
		return "spotify"
	case "N":
		return "navidrome"
	case "P":
		return "plex"
	case "J":
		return "jellyfin"
	case "E":
		return "emby"
	case "Y":
		return "yt"
	case "C":
		return "soundcloud"
	case "M":
		return "netease"
	case "Q":
		return "qobuz"
	case "L":
		return "local"
	case "R":
		return "radio"
	}
	return ""
}

// switchToProvider finds a provider by config key and switches to it.
// Returns nil if the provider is not configured.
func (m *Model) switchToProvider(key string) tea.Cmd {
	for i, pe := range m.providers {
		if pe.Key == key {
			return m.switchProvider(i)
		}
	}
	return nil
}

// SetPendingURLs stores remote URLs (feeds, M3U) for async resolution after Init.
func (m *Model) SetPendingURLs(urls []string) {
	m.pendingURLs = urls
	m.feedLoading = len(urls) > 0
}

// SetLoadedPlaylist records that the live queue exactly mirrors a local saved
// playlist, allowing path-based write-backs such as bookmarks and removals.
func (m *Model) SetLoadedPlaylist(name string) {
	m.loadedPlaylist = name
}

// findBrowseProvider returns the first provider that supports browsing
// (ArtistBrowser or AlbumBrowser), preferring the active provider.
func (m *Model) findBrowseProvider() playlist.Provider {
	return m.findProviderWith(func(p playlist.Provider) bool {
		if _, ok := p.(provider.ArtistBrowser); ok {
			return true
		}
		_, ok := p.(provider.AlbumBrowser)
		return ok
	})
}

func (m *Model) openNavBrowserWith(prov playlist.Provider) {
	nextRequest(&m.requests.nav)
	m.navBrowser.prov = prov
	m.navBrowser.visible = true
	m.navBrowser.mode = navBrowseModeMenu
	m.navBrowser.screen = navBrowseScreenList
	m.navBrowser.cursor = 0
	m.navBrowser.scroll = 0
	m.navBrowser.artists = nil
	m.navBrowser.albums = nil
	m.navBrowser.tracks = nil
	m.navBrowser.loading = false
	m.navBrowser.albumLoading = false
	m.navBrowser.albumDone = false
	m.navBrowser.searching = false
	m.navBrowser.search = ""
	m.navBrowser.searchIdx = nil
	m.navBrowser.confirmReplace = false
	m.navBrowser.selArtist = provider.ArtistInfo{}
	m.navBrowser.selAlbum = provider.AlbumInfo{}
	if ab, ok := prov.(provider.AlbumBrowser); ok {
		m.navBrowser.sortType = ab.DefaultAlbumSort()
	} else {
		m.navBrowser.sortType = ""
	}
}

func (m *Model) nextNavRequest() uint64 {
	return nextRequest(&m.requests.nav)
}

func (m *Model) cancelNavRequests() {
	nextRequest(&m.requests.nav)
	m.navBrowser.loading = false
	m.navBrowser.albumLoading = false
}

// navUpdateSearch rebuilds navSearchIdx from the current navSearch query
// against whichever list is active on the current nav screen.
func (m *Model) navUpdateSearch() {
	q := strings.ToLower(m.navBrowser.search)
	if q == "" {
		m.navBrowser.searchIdx = nil
		return
	}
	m.navBrowser.searchIdx = nil
	switch {
	case m.navBrowser.mode == navBrowseModeByArtist && m.navBrowser.screen == navBrowseScreenList,
		m.navBrowser.mode == navBrowseModeByArtistAlbum && m.navBrowser.screen == navBrowseScreenList:
		for i, a := range m.navBrowser.artists {
			if strings.Contains(strings.ToLower(a.Name), q) {
				m.navBrowser.searchIdx = append(m.navBrowser.searchIdx, i)
			}
		}
	case m.navBrowser.mode == navBrowseModeByAlbum && m.navBrowser.screen == navBrowseScreenList,
		m.navBrowser.mode == navBrowseModeByArtistAlbum && m.navBrowser.screen == navBrowseScreenAlbums:
		for i, a := range m.navBrowser.albums {
			if strings.Contains(strings.ToLower(a.Name), q) ||
				strings.Contains(strings.ToLower(a.Artist), q) {
				m.navBrowser.searchIdx = append(m.navBrowser.searchIdx, i)
			}
		}
	case m.navBrowser.screen == navBrowseScreenTracks:
		for i, t := range m.navBrowser.tracks {
			if strings.Contains(strings.ToLower(t.Title), q) ||
				strings.Contains(strings.ToLower(t.Artist), q) ||
				strings.Contains(strings.ToLower(t.Album), q) {
				m.navBrowser.searchIdx = append(m.navBrowser.searchIdx, i)
			}
		}
	}
}

// navClearSearch resets the nav search state.
func (m *Model) navClearSearch() {
	m.navBrowser.searching = false
	m.navBrowser.search = ""
	m.navBrowser.searchIdx = nil
	m.navBrowser.cursor = 0
	m.navBrowser.scroll = 0
}

// fetchNavArtistAllTracksCmd first fetches the artist's album list, then fetches
// all tracks across every album. This is used by the "By Artist" browse mode.
// The provider must implement both ArtistBrowser and AlbumTrackLoader.
func (m *Model) fetchNavArtistAllTracksCmd(ab provider.ArtistBrowser, artistID string) tea.Cmd {
	gen := m.nextNavRequest()
	loader, _ := m.navBrowser.prov.(provider.AlbumTrackLoader)
	return func() tea.Msg {
		albums, err := ab.ArtistAlbums(artistID)
		if err != nil {
			return navTracksLoadedMsg{gen: gen, err: err}
		}
		if loader == nil {
			return navTracksLoadedMsg{gen: gen}
		}
		var all []playlist.Track
		for _, album := range albums {
			tracks, err := loader.AlbumTracks(album.ID)
			if err != nil {
				return navTracksLoadedMsg{gen: gen, err: err}
			}
			all = append(all, tracks...)
		}
		return navTracksLoadedMsg{tracks: all, gen: gen}
	}
}
