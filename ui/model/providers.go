package model

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"cliamp/playlist"
	"cliamp/provider"
)

// resetProviderNav resets provider navigation and search state to the top.
func (m *Model) resetProviderNav() {
	m.provCursor = 0
	m.provScroll = 0
	m.provLoading = true
	m.provSearch.active = false
	m.provSearch.query = ""
	m.provSearch.results = nil
	m.provSearch.cursor = 0
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
	m.resetProviderNav()
	m.focus = focusProvider
	return fetchPlaylistsCmd(m.provider)
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
	m.navBrowser.selArtist = provider.ArtistInfo{}
	m.navBrowser.selAlbum = provider.AlbumInfo{}
	if ab, ok := prov.(provider.AlbumBrowser); ok {
		m.navBrowser.sortType = ab.DefaultAlbumSort()
	} else {
		m.navBrowser.sortType = ""
	}
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
	loader, _ := m.navBrowser.prov.(provider.AlbumTrackLoader)
	return func() tea.Msg {
		albums, err := ab.ArtistAlbums(artistID)
		if err != nil {
			return err
		}
		if loader == nil {
			return navTracksLoadedMsg(nil)
		}
		var all []playlist.Track
		for _, album := range albums {
			tracks, err := loader.AlbumTracks(album.ID)
			if err != nil {
				return err
			}
			all = append(all, tracks...)
		}
		return navTracksLoadedMsg(all)
	}
}
