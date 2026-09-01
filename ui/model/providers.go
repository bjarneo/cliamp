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

// refreshPaneAfterLocalWrite re-pulls Playlists() into the provider pane after
// a local playlist mutation, but only when Local is the active provider: a
// remote provider's pane must not receive an unrelated fetch (or surface an
// unrelated fetch error) because of a local write.
func (m *Model) refreshPaneAfterLocalWrite() tea.Cmd {
	if !m.isActiveProvider("Local") {
		return nil
	}
	return m.fetchProviderPlaylists()
}

func (m *Model) fetchProviderTracks(playlistID string) tea.Cmd {
	if m.provider == nil {
		return nil
	}
	return fetchTracksCmd(m.provider, playlistID, nextRequest(&m.requests.tracks))
}

// applyTracksResume positions the cursor on the in-progress track and arms the
// seek when the provider reported a stored listening position.
func (m *Model) applyTracksResume(msg tracksLoadedMsg) {
	if msg.resumeOffset <= 0 || msg.resumeIdx < 0 || msg.resumeIdx >= len(msg.tracks) {
		if m.resume.path != "" && !tracksContainPath(msg.tracks, m.resume.path) {
			m.resume.path = ""
			m.resume.secs = 0
		}
		return
	}
	m.plCursor = msg.resumeIdx
	m.resume.path = msg.tracks[msg.resumeIdx].Path
	m.resume.secs = int(msg.resumeOffset.Seconds())
}

// tracksContainPath reports whether any track in tracks has the given path.
func tracksContainPath(tracks []playlist.Track, path string) bool {
	for _, t := range tracks {
		if t.Path == path {
			return true
		}
	}
	return false
}

// replacePlayerPlaylist installs a provider result in the main playlist while
// preserving detached playback semantics used by provider-list selection.
func (m *Model) replacePlayerPlaylist(tracks []playlist.Track) {
	if m.player.IsPlaying() || m.buffering {
		m.detachPlaybackTrack()
		m.player.ClearPreload()
		m.preloading = false
	} else {
		m.player.Stop()
		m.player.ClearPreload()
		m.clearPlaybackTrack()
	}
	m.resetYTDLBatch()
	m.replacePlaylist(tracks)
	m.setHeaderStateFromTracks(tracks)
	m.loadedPlaylist = ""
	m.plCursor = 0
	m.plScroll = 0
	m.focus = focusPlaylist
	m.applyHeightMode()
	m.adjustScroll()
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
	return m.spotSearch.screen == spotSearchResults &&
		m.isCurrentSpotProvider(providerName) &&
		gen == m.requests.spotLists
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
// from the main pane (S, N, P, J, E, B, Y, C, X, M, Q, R, L). Returns nil when the key doesn't
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
	case "B":
		return "audiobookshelf"
	case "Y":
		return "yt"
	case "C":
		return "soundcloud"
	case "X":
		return "mixcloud"
	case "M":
		return "netease"
	case "Q":
		return "qobuz"
	case "T":
		return "tidal"
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

type browseEntryGroup struct {
	afterID      string
	afterSection string
	section      string
	lists        []playlist.PlaylistInfo
}

// providerListsWithBrowse adds UI-only hierarchical browse routes to a
// provider's playable playlist list. Keeping these routes out of Playlists()
// means IPC and other playlist consumers never mistake them for audio lists.
func providerListsWithBrowse(prov playlist.Provider, lists []playlist.PlaylistInfo) []playlist.PlaylistInfo {
	entries, ok := prov.(provider.BrowseEntryProvider)
	if !ok {
		return lists
	}
	groups := groupBrowseEntries(entries.BrowseEntries(), lists)
	if len(groups) == 0 {
		return lists
	}
	return spliceBrowseGroups(lists, groups)
}

// groupBrowseEntries removes invalid and duplicate shortcuts, then combines
// adjacent entries that share placement and section metadata.
func groupBrowseEntries(entries []provider.BrowseEntry, lists []playlist.PlaylistInfo) []browseEntryGroup {
	existing := make(map[string]struct{}, len(lists))
	for _, item := range lists {
		existing[item.ID] = struct{}{}
	}
	groups := make([]browseEntryGroup, 0, len(entries))
	for _, entry := range entries {
		if entry.ID == "" || entry.Name == "" {
			continue
		}
		if _, duplicate := existing[entry.ID]; duplicate {
			continue
		}
		existing[entry.ID] = struct{}{}
		item := playlist.PlaylistInfo{ID: entry.ID, Name: entry.Name, Section: entry.Section}
		last := len(groups) - 1
		if last >= 0 &&
			groups[last].afterID == entry.AfterID &&
			groups[last].afterSection == entry.AfterSection &&
			groups[last].section == entry.Section {
			groups[last].lists = append(groups[last].lists, item)
		} else {
			groups = append(groups, browseEntryGroup{
				afterID:      entry.AfterID,
				afterSection: entry.AfterSection,
				section:      entry.Section,
				lists:        []playlist.PlaylistInfo{item},
			})
		}
	}
	return groups
}

// spliceBrowseGroups resolves exact-item and section fallback anchors, omits
// extensions of missing sections, and prepends otherwise unanchored groups.
func spliceBrowseGroups(lists []playlist.PlaylistInfo, groups []browseEntryGroup) []playlist.PlaylistInfo {
	itemIndex := make(map[string]int, len(lists))
	lastSectionIndex := make(map[string]int, len(lists))
	for i, item := range lists {
		itemIndex[item.ID] = i
		lastSectionIndex[item.Section] = i
	}
	entryCount := 0
	for _, group := range groups {
		entryCount += len(group.lists)
	}
	prepend := make([]playlist.PlaylistInfo, 0, entryCount)
	groupsAfter := make(map[int][]browseEntryGroup, len(groups))
	for _, group := range groups {
		anchor := -1
		if group.afterID != "" {
			if index, found := itemIndex[group.afterID]; found {
				anchor = index
			}
		}
		if anchor < 0 && group.afterSection != "" {
			if index, found := lastSectionIndex[group.afterSection]; found {
				anchor = index
			}
		}
		if anchor >= 0 {
			groupsAfter[anchor] = append(groupsAfter[anchor], group)
			continue
		}
		if group.afterSection != "" && group.section == group.afterSection {
			continue
		}
		prepend = append(prepend, group.lists...)
	}
	out := make([]playlist.PlaylistInfo, 0, len(lists)+entryCount)
	out = append(out, prepend...)
	for i, item := range lists {
		out = append(out, item)
		for _, group := range groupsAfter[i] {
			out = append(out, group.lists...)
		}
	}
	return out
}

func providerBrowseEntryForID(prov playlist.Provider, id string) (provider.BrowseEntry, bool) {
	entries, ok := prov.(provider.BrowseEntryProvider)
	if !ok {
		return provider.BrowseEntry{}, false
	}
	for _, entry := range entries.BrowseEntries() {
		if entry.ID == id {
			return entry, true
		}
	}
	return provider.BrowseEntry{}, false
}

func providerBrowseEntryForMode(prov playlist.Provider, mode provider.BrowseMode) (provider.BrowseEntry, bool) {
	entries, ok := prov.(provider.BrowseEntryProvider)
	if !ok {
		return provider.BrowseEntry{}, false
	}
	for _, entry := range entries.BrowseEntries() {
		if entry.Mode == mode {
			return entry, true
		}
	}
	return provider.BrowseEntry{}, false
}

func (m Model) selectedProviderListIsBrowseEntry() bool {
	if m.provCursor < 0 || m.provCursor >= len(m.providerLists) {
		return false
	}
	_, ok := providerBrowseEntryForID(m.provider, m.providerLists[m.provCursor].ID)
	return ok
}

// openProviderList activates either a playable provider list or a UI-only
// hierarchical browse entry contributed by the provider.
func (m *Model) openProviderList(index int) tea.Cmd {
	if index < 0 || index >= len(m.providerLists) || m.provider == nil {
		return nil
	}
	item := m.providerLists[index]
	// The location offer is a question, not a list. Selecting it raises the
	// question; nothing about the listener's location is worked out until they
	// answer it.
	if consenter, ok := m.provider.(provider.LocationConsenter); ok {
		if id := consenter.LocationConsentID(); id != "" && id == item.ID {
			m.provAskLoc = true
			return nil
		}
	}
	if entry, ok := providerBrowseEntryForID(m.provider, item.ID); ok {
		m.activeProviderPlaylistID = ""
		cmd := m.openNavBrowserEntry(m.provider, entry)
		if m.navBrowser.visible {
			m.navBrowser.fromProvList = true
		}
		return cmd
	}
	m.provLoading = true
	m.activeProviderPlaylistID = item.ID
	return m.fetchProviderTracks(item.ID)
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

// findBrowseProvider returns the first provider that supports artist, album,
// or genre browsing, preferring the active provider.
func (m *Model) findBrowseProvider() playlist.Provider {
	return m.findProviderWith(providerSupportsBrowse)
}

func providerSupportsBrowse(prov playlist.Provider) bool {
	if prov == nil {
		return false
	}
	if _, ok := prov.(provider.ArtistBrowser); ok {
		return true
	}
	if _, ok := prov.(provider.AlbumBrowser); ok {
		return true
	}
	_, ok := prov.(provider.GenreBrowser)
	return ok
}

// canOpenProviderBrowser keeps help scoped to the active provider or a
// highlighted track with a provider-specific creator jump. Merely registering
// another browsable provider must not advertise its browser in this context.
func (m Model) canOpenProviderBrowser() bool {
	if _, ok := m.selectedTrackArtistBrowserTarget(); ok {
		return true
	}
	return providerSupportsBrowse(m.provider)
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
	m.navBrowser.genres = nil
	m.navBrowser.genreSorts = nil
	m.navBrowser.loading = false
	m.navBrowser.albumLoading = false
	m.navBrowser.albumDone = false
	m.navBrowser.searching = false
	m.navBrowser.search = ""
	m.navBrowser.searchIdx = nil
	m.navBrowser.confirmReplace = false
	m.navBrowser.directTrackJump = false
	m.navBrowser.fromProvList = false
	m.navBrowser.openInPlaylist = false
	m.navBrowser.selArtist = provider.ArtistInfo{}
	m.navBrowser.selAlbum = provider.AlbumInfo{}
	m.navBrowser.selGenre = provider.GenreInfo{}
	m.navBrowser.selGenreSort = provider.SortType{}
	m.navBrowser.genreQuery = ""
	if ab, ok := prov.(provider.AlbumBrowser); ok {
		m.navBrowser.sortType = ab.DefaultAlbumSort()
	} else {
		m.navBrowser.sortType = ""
	}
}

func (m *Model) navBackFromRoot() {
	m.cancelNavRequests()
	m.navClearSearch()
	if m.navBrowser.fromProvList {
		m.navBrowser.fromProvList = false
		m.navBrowser.visible = false
		return
	}
	m.navBrowser.mode = navBrowseModeMenu
	m.navBrowser.screen = navBrowseScreenList
	m.navBrowser.cursor = 0
	m.navBrowser.scroll = 0
}

// openNavBrowserAt opens a provider's hierarchy at a concrete route instead
// of making the user pass through the generic browse-mode menu first.
func (m *Model) openNavBrowserAt(prov playlist.Provider, mode provider.BrowseMode) tea.Cmd {
	openInPlaylist := false
	if entry, ok := providerBrowseEntryForMode(prov, mode); ok {
		openInPlaylist = entry.OpenInPlaylist
	}
	return m.openNavBrowserRoute(prov, mode, openInPlaylist)
}

// openNavBrowserEntry opens the exact provider-pane route selected by ID. It
// must not re-resolve by mode because providers may expose multiple entries
// with the same hierarchy and different leaf behavior.
func (m *Model) openNavBrowserEntry(prov playlist.Provider, entry provider.BrowseEntry) tea.Cmd {
	return m.openNavBrowserRoute(prov, entry.Mode, entry.OpenInPlaylist)
}

func (m *Model) openNavBrowserRoute(prov playlist.Provider, mode provider.BrowseMode, openInPlaylist bool) tea.Cmd {
	m.openNavBrowserWith(prov)
	m.navBrowser.openInPlaylist = openInPlaylist
	switch mode {
	case provider.BrowseAlbums:
		browser, ok := prov.(provider.AlbumBrowser)
		if !ok {
			m.navBrowser.visible = false
			return nil
		}
		m.navBrowser.mode = navBrowseModeByAlbum
		m.navBrowser.albumLoading = true
		return fetchNavAlbumListCmd(browser, m.navBrowser.sortType, 0, m.nextNavRequest())
	case provider.BrowseArtists, provider.BrowseArtistAlbums:
		browser, ok := prov.(provider.ArtistBrowser)
		if !ok {
			m.navBrowser.visible = false
			return nil
		}
		if mode == provider.BrowseArtistAlbums {
			m.navBrowser.mode = navBrowseModeByArtistAlbum
		} else {
			m.navBrowser.mode = navBrowseModeByArtist
		}
		m.navBrowser.loading = true
		return fetchNavArtistsCmd(browser, m.nextNavRequest())
	case provider.BrowseGenres:
		browser, ok := prov.(provider.GenreBrowser)
		if !ok {
			m.navBrowser.visible = false
			return nil
		}
		m.navBrowser.mode = navBrowseModeByGenre
		m.navBrowser.loading = true
		return fetchNavGenresCmd(browser, m.nextNavRequest())
	default:
		m.navBrowser.visible = false
		return nil
	}
}

type selectedTrackArtistTarget struct {
	prov    playlist.Provider
	browser provider.ArtistBrowser
	artist  provider.ArtistInfo
}

func (m Model) selectedTrackArtistBrowserTarget() (selectedTrackArtistTarget, bool) {
	if m.focus != focusPlaylist || m.playlist == nil {
		return selectedTrackArtistTarget{}, false
	}
	track, ok := m.playlist.Track(m.plCursor)
	if !ok {
		return selectedTrackArtistTarget{}, false
	}
	candidates := make([]playlist.Provider, 0, len(m.providers)+1)
	if m.provider != nil {
		candidates = append(candidates, m.provider)
	}
	for _, entry := range m.providers {
		if entry.Provider != nil {
			candidates = append(candidates, entry.Provider)
		}
	}
	for _, candidate := range candidates {
		resolver, ok := candidate.(provider.TrackArtistResolver)
		if !ok {
			continue
		}
		artist, ok := resolver.ArtistForTrack(track)
		if !ok {
			continue
		}
		browser, ok := candidate.(provider.ArtistBrowser)
		if ok {
			return selectedTrackArtistTarget{prov: candidate, browser: browser, artist: artist}, true
		}
	}
	return selectedTrackArtistTarget{}, false
}

// openSelectedTrackArtistBrowser jumps from the highlighted track to its
// artist's album/show categories when one registered provider recognizes it.
func (m *Model) openSelectedTrackArtistBrowser() (tea.Cmd, bool) {
	target, ok := m.selectedTrackArtistBrowserTarget()
	if !ok {
		return nil, false
	}
	m.openNavBrowserWith(target.prov)
	m.navBrowser.mode = navBrowseModeByArtistAlbum
	m.navBrowser.screen = navBrowseScreenAlbums
	m.navBrowser.selArtist = target.artist
	m.navBrowser.directTrackJump = true
	if entry, found := providerBrowseEntryForMode(target.prov, provider.BrowseArtistAlbums); found {
		m.navBrowser.openInPlaylist = entry.OpenInPlaylist
	}
	m.navBrowser.loading = true
	return fetchNavArtistAlbumsCmd(target.browser, target.artist.ID, m.nextNavRequest()), true
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
	case m.navBrowser.mode == navBrowseModeByGenre && m.navBrowser.screen == navBrowseScreenList:
		for i, genre := range m.navBrowser.genres {
			if strings.Contains(strings.ToLower(genre.Name), q) ||
				strings.Contains(strings.ToLower(genre.Group), q) {
				m.navBrowser.searchIdx = append(m.navBrowser.searchIdx, i)
			}
		}
	case m.navBrowser.mode == navBrowseModeByGenre && m.navBrowser.screen == navBrowseScreenAlbums:
		for i, sortType := range m.navBrowser.genreSorts {
			if strings.Contains(strings.ToLower(sortType.Label), q) {
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

// navClearSearchKeepingCursor removes a filter while keeping the selected raw
// item highlighted during an in-flight leaf request.
func (m *Model) navClearSearchKeepingCursor(cursor int) {
	m.navClearSearch()
	m.navBrowser.cursor = cursor
	m.navMaybeAdjustScroll()
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
