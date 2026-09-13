package model

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// home_overlay.go implements the Home view: a full-screen overlay with an
// internal two-pane renderer (sectioned library sidebar + content pane). It
// opens on top of the main view and the artist screen can open on top of it.
// All state lives in homeState; rendering lives in inline_overlays_home.go.

// homeScreenType identifies which screen of the Home overlay is active.
type homeScreenType int

const (
	homeScreenLibrary homeScreenType = iota // two-pane library browser
	homeScreenNewName                       // "+ New playlist" name input
)

// homePane identifies which Home pane holds the keyboard focus.
type homePane int

const (
	homePaneSidebar homePane = iota
	homePaneContent
)

// homeSortMode orders every sidebar section at once; `s` cycles through them.
// The modes are client-side only — no extra server calls are made.
type homeSortMode int

const (
	homeSortRecents homeSortMode = iota // provider order as fetched
	homeSortAdded                       // reverse fetch order for albums; provider order elsewhere
	homeSortAlpha                       // case-insensitive by name
	homeSortCount
)

var homeSortLabels = [homeSortCount]string{"recents", "recently added", "alphabetical"}

// homeContentKind identifies what the content pane is showing.
type homeContentKind int

const (
	homeContentNone homeContentKind = iota
	homeContentPlaylist
	homeContentAlbum
)

// homePagingState tracks incremental loading of a playlist through the Home
// content pane (mirrors trackPagingState, which does the same for the queue).
type homePagingState struct {
	active  bool
	offset  int  // tracks loaded so far
	total   int  // total reported by the provider
	loading bool // next page fetch in flight
}

// homeContentState is the right pane: the tracks of one playlist or album.
type homeContentState struct {
	kind    homeContentKind
	id      string
	name    string
	tracks  []playlist.Track
	loading bool
	cursor  int
	scroll  int
	paging  homePagingState
}

// homeState holds the Home overlay. Section lists stay in raw fetch order;
// ordering and filtering are applied per render (homeRows) so cycling `s` or
// editing the `/` filter never mutates fetched data.
type homeState struct {
	prov    playlist.Provider // provider whose library is shown
	visible bool
	screen  homeScreenType
	focus   homePane

	lists          []playlist.PlaylistInfo
	albums         []provider.AlbumInfo
	artists        []provider.ArtistInfo
	loadingLists   bool
	loadingAlbums  bool
	loadingArtists bool
	albumsDone     bool   // no more AlbumList pages to fetch
	sortType       string // provider-side album sort (persisted via AlbumSortSaver)
	order          homeSortMode

	cursor int // index over selectable sidebar rows (section headers skipped)
	scroll int // first visible selectable row

	filtering bool
	filter    string

	content homeContentState

	newName  string
	inputErr string
	creating bool   // create request in flight; guards against a duplicate Enter
	fixupID  string // playlist to select on the next lists refresh
}

// — messages —

// homeListsMsg carries the provider playlist list for the sidebar.
type homeListsMsg struct {
	playlists    []playlist.PlaylistInfo
	providerName string
	gen          uint64
	err          error
}

// homeAlbumsMsg carries one page of the album list and the offset it was
// requested at (mirrors navAlbumsLoadedMsg).
type homeAlbumsMsg struct {
	albums       []provider.AlbumInfo
	offset       int
	isLast       bool
	providerName string
	gen          uint64
	err          error
}

// homeArtistsMsg carries the artist list for the sidebar.
type homeArtistsMsg struct {
	artists      []provider.ArtistInfo
	providerName string
	gen          uint64
	err          error
}

// homeContentMsg carries the first (or only) track list for the content pane.
type homeContentMsg struct {
	kind         homeContentKind
	id           string
	tracks       []playlist.Track
	total        int // pager total (playlists only; 0 otherwise)
	providerName string
	gen          uint64
	err          error
}

// homePageMsg carries one appended page of a playlist in the content pane.
type homePageMsg struct {
	tracks       []playlist.Track
	total        int
	offset       int
	playlistID   string
	providerName string
	gen          uint64
	err          error
}

// homeCreatedMsg carries the outcome of the "+ New playlist" flow.
type homeCreatedMsg struct {
	playlistID   string
	name         string
	providerName string
	gen          uint64
	err          error
}

// — command constructors —

func fetchHomeListsCmd(prov playlist.Provider, providerName string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		playlists, err := prov.Playlists()
		return homeListsMsg{playlists: playlists, providerName: providerName, gen: gen, err: err}
	}
}

func fetchHomeAlbumsCmd(b provider.AlbumBrowser, providerName, sortType string, offset int, gen uint64) tea.Cmd {
	return func() tea.Msg {
		albums, err := b.AlbumList(sortType, offset, navAlbumPageSize)
		return homeAlbumsMsg{albums: albums, offset: offset, isLast: len(albums) < navAlbumPageSize, providerName: providerName, gen: gen, err: err}
	}
}

func fetchHomeArtistsCmd(ab provider.ArtistBrowser, providerName string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		artists, err := ab.Artists()
		return homeArtistsMsg{artists: artists, providerName: providerName, gen: gen, err: err}
	}
}

// fetchHomePlaylistTracksCmd is the one-shot fallback used when the queue is
// (or is about to be) paging the same playlist: TracksPage rewrites the
// provider's page cursor for the playlist ID and would corrupt that load.
func fetchHomePlaylistTracksCmd(prov playlist.Provider, providerName, playlistID string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		tracks, err := prov.Tracks(playlistID)
		return homeContentMsg{kind: homeContentPlaylist, id: playlistID, tracks: tracks, providerName: providerName, gen: gen, err: err}
	}
}

// fetchHomeTracksPageCmd loads one page of a playlist into the content pane.
// The offset=0 page reports through homeContentMsg; later pages append via
// homePageMsg.
func fetchHomeTracksPageCmd(p provider.TrackPager, providerName, playlistID string, offset, limit int, gen uint64) tea.Cmd {
	return func() tea.Msg {
		tracks, total, err := p.TracksPage(playlistID, offset, limit)
		if offset > 0 {
			return homePageMsg{tracks: tracks, total: total, offset: offset, playlistID: playlistID, providerName: providerName, gen: gen, err: err}
		}
		return homeContentMsg{kind: homeContentPlaylist, id: playlistID, tracks: tracks, total: total, providerName: providerName, gen: gen, err: err}
	}
}

func fetchHomeAlbumTracksCmd(l provider.AlbumTrackLoader, providerName, albumID string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		tracks, err := l.AlbumTracks(albumID)
		return homeContentMsg{kind: homeContentAlbum, id: albumID, tracks: tracks, providerName: providerName, gen: gen, err: err}
	}
}

func createHomePlaylistCmd(ctx context.Context, c provider.PlaylistCreator, providerName, name string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		id, err := c.CreatePlaylist(ctx, name)
		return homeCreatedMsg{playlistID: id, name: name, providerName: providerName, gen: gen, err: err}
	}
}

// — open / close —

// openHomeView opens the Home overlay for the active provider. The provider
// pane's cached playlist list is reused when it belongs to the same provider
// and is settled; albums and artists are always fetched gen-guarded.
func (m *Model) openHomeView() tea.Cmd {
	if m.provider == nil {
		m.status.Show("No active provider", statusTTLDefault)
		return nil
	}
	m.home = homeState{
		prov:    m.provider,
		visible: true,
		screen:  homeScreenLibrary,
		focus:   homePaneSidebar,
		order:   homeSortRecents,
	}
	if ab, ok := m.provider.(provider.AlbumBrowser); ok {
		m.home.sortType = ab.DefaultAlbumSort()
		m.home.loadingAlbums = true
	}
	if _, ok := m.provider.(provider.ArtistBrowser); ok {
		m.home.loadingArtists = true
	}
	if m.isActiveProvider(m.provider.Name()) && !m.provLoading && len(m.providerLists) > 0 {
		m.home.lists = filterHomePlaylists(m.providerLists)
	} else {
		m.home.loadingLists = true
	}
	m.applyHeightMode()
	m.homeMaybeAdjustScroll()
	return m.fetchHomeSidebarLists()
}

// closeHomeView drops the overlay and supersedes every in-flight Home request.
func (m *Model) closeHomeView() {
	nextRequest(&m.requests.homeLists)
	nextRequest(&m.requests.homeAlbums)
	nextRequest(&m.requests.homeArtists)
	nextRequest(&m.requests.homeCreate)
	nextRequest(&m.requests.homeContent)
	m.home = homeState{}
	m.applyHeightMode()
	m.adjustScroll()
}

// fetchHomeSidebarLists dispatches the sidebar fetches that are still needed,
// one generation per section.
func (m *Model) fetchHomeSidebarLists() tea.Cmd {
	prov := m.home.prov
	if prov == nil {
		return nil
	}
	providerName := prov.Name()
	var cmds []tea.Cmd
	if m.home.loadingLists {
		cmds = append(cmds, fetchHomeListsCmd(prov, providerName, nextRequest(&m.requests.homeLists)))
	}
	if ab, ok := prov.(provider.AlbumBrowser); ok {
		if m.home.loadingAlbums {
			cmds = append(cmds, fetchHomeAlbumsCmd(ab, providerName, m.home.sortType, 0, nextRequest(&m.requests.homeAlbums)))
		}
	}
	if ab, ok := prov.(provider.ArtistBrowser); ok {
		if m.home.loadingArtists {
			cmds = append(cmds, fetchHomeArtistsCmd(ab, providerName, nextRequest(&m.requests.homeArtists)))
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	if len(cmds) == 1 {
		return cmds[0]
	}
	return tea.Batch(cmds...)
}

// isCurrentHomeSectionRequest reports whether a sidebar-section completion is
// still current: Home open, same provider, and no newer request for that
// section has superseded it.
func (m Model) isCurrentHomeSectionRequest(gen uint64, providerName string, cur *uint64) bool {
	return m.home.visible &&
		m.home.prov != nil &&
		m.home.prov.Name() == providerName &&
		gen == *cur
}

func (m Model) isCurrentHomeContentRequest(gen uint64, providerName string, kind homeContentKind, id string) bool {
	return m.home.visible &&
		m.home.prov != nil &&
		m.home.prov.Name() == providerName &&
		m.home.content.kind == kind &&
		m.home.content.id == id &&
		gen == m.requests.homeContent
}

// filterHomePlaylists drops synthetic provider rows ("Liked Songs"-style
// pseudo entries whose IDs contain spaces) so the sidebar lists real
// playlists only.
func filterHomePlaylists(lists []playlist.PlaylistInfo) []playlist.PlaylistInfo {
	filtered := make([]playlist.PlaylistInfo, 0, len(lists))
	for _, pl := range lists {
		if !isSyntheticProviderRow(pl.ID) {
			filtered = append(filtered, pl)
		}
	}
	return filtered
}

// homeApplyFixup moves the sidebar cursor onto the playlist named by
// fixupID (set after the "+ New playlist" flow) and clears it.
func (m *Model) homeApplyFixup() {
	if m.home.fixupID == "" {
		return
	}
	id := m.home.fixupID
	m.home.fixupID = ""
	for i, row := range m.homeRows() {
		if row.kind == homeRowPlaylist && row.playlist.ID == id {
			m.home.cursor = i
			m.homeSidebarMaybeAdjustScroll()
			return
		}
	}
}

// — sidebar row model —

// homeRowKind identifies which sidebar section a row belongs to.
type homeRowKind int

const (
	homeRowNew homeRowKind = iota // "+ New playlist" action row
	homeRowPlaylist
	homeRowAlbum
	homeRowArtist
)

// homeSectionKinds lists the sectioned kinds in display order (after the
// optional "+ New playlist" row).
var homeSectionKinds = []homeRowKind{homeRowPlaylist, homeRowAlbum, homeRowArtist}

// homeRow is one selectable sidebar row.
type homeRow struct {
	kind     homeRowKind
	playlist playlist.PlaylistInfo
	album    provider.AlbumInfo
	artist   provider.ArtistInfo
}

func homeNameMatches(name, filter string) bool {
	return filter == "" || strings.Contains(strings.ToLower(name), strings.ToLower(filter))
}

// homePlaylistsView returns the playlist rows in the active order and filter.
// Recents and recently-added both keep provider order: playlist APIs expose
// no added-at, and their order is already most-recent-first on Spotify.
func (m Model) homePlaylistsView() []playlist.PlaylistInfo {
	out := make([]playlist.PlaylistInfo, 0, len(m.home.lists))
	for _, p := range m.home.lists {
		if homeNameMatches(p.Name, m.home.filter) {
			out = append(out, p)
		}
	}
	if m.home.order == homeSortAlpha {
		sort.SliceStable(out, func(i, j int) bool {
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		})
	}
	return out
}

// homeAlbumsView returns the album rows in the active order and filter.
// Recently-added reverses the fetch order — the honest approximation the
// provider's recents-style sort gives us without extra server calls.
func (m Model) homeAlbumsView() []provider.AlbumInfo {
	out := make([]provider.AlbumInfo, 0, len(m.home.albums))
	for _, a := range m.home.albums {
		if homeNameMatches(a.Name, m.home.filter) {
			out = append(out, a)
		}
	}
	switch m.home.order {
	case homeSortAdded:
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	case homeSortAlpha:
		sort.SliceStable(out, func(i, j int) bool {
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		})
	}
	return out
}

// homeArtistsView returns the artist rows in the active order and filter.
// Like playlists, artists expose no added-at; only alphabetical differs from
// provider order.
func (m Model) homeArtistsView() []provider.ArtistInfo {
	out := make([]provider.ArtistInfo, 0, len(m.home.artists))
	for _, a := range m.home.artists {
		if homeNameMatches(a.Name, m.home.filter) {
			out = append(out, a)
		}
	}
	if m.home.order == homeSortAlpha {
		sort.SliceStable(out, func(i, j int) bool {
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		})
	}
	return out
}

// homeRows flattens the sidebar into selectable rows: the optional
// "+ New playlist" action row first, then each section's rows.
func (m Model) homeRows() []homeRow {
	if !m.home.visible {
		return nil
	}
	var rows []homeRow
	if _, ok := m.home.prov.(provider.PlaylistCreator); ok {
		rows = append(rows, homeRow{kind: homeRowNew})
	}
	for _, p := range m.homePlaylistsView() {
		rows = append(rows, homeRow{kind: homeRowPlaylist, playlist: p})
	}
	for _, a := range m.homeAlbumsView() {
		rows = append(rows, homeRow{kind: homeRowAlbum, album: a})
	}
	for _, a := range m.homeArtistsView() {
		rows = append(rows, homeRow{kind: homeRowArtist, artist: a})
	}
	return rows
}

// homeSectionOmitted reports whether a section's interface is missing on the
// provider (degrade honestly: the section does not render at all).
func (m Model) homeSectionOmitted(kind homeRowKind) bool {
	switch kind {
	case homeRowAlbum:
		_, ok := m.home.prov.(provider.AlbumBrowser)
		return !ok
	case homeRowArtist:
		_, ok := m.home.prov.(provider.ArtistBrowser)
		return !ok
	}
	return false
}

// homeRowAt returns the selectable row under the sidebar cursor.
func (m Model) homeRowAt() (homeRow, bool) {
	rows := m.homeRows()
	if m.home.cursor < 0 || m.home.cursor >= len(rows) {
		return homeRow{}, false
	}
	return rows[m.home.cursor], true
}

// homeRowLabel renders one sidebar row's text (unstyled).
func homeRowLabel(row homeRow) string {
	switch row.kind {
	case homeRowNew:
		return "+ New playlist"
	case homeRowPlaylist:
		return playlistLabel("", row.playlist)
	case homeRowAlbum:
		if row.album.Year > 0 {
			return fmt.Sprintf("%s — %s (%d)", row.album.Name, row.album.Artist, row.album.Year)
		}
		return fmt.Sprintf("%s — %s", row.album.Name, row.album.Artist)
	default:
		return row.artist.Name
	}
}

// — scrolling —

// homeSidebarLinesFrom renders the sidebar list lines starting at row index
// scroll. Section headers (with counts) are emitted when the walk crosses a
// section, including empty or still-loading sections passed over between two
// rows. When stopRow >= 0, rendering stops after emitting that row — the
// scroll adjuster uses this to count the rendered lines up to the cursor.
func (m Model) homeSidebarLinesFrom(scroll, stopRow, width int) []string {
	rows := m.homeRows()
	if scroll < 0 {
		scroll = 0
	}
	if scroll >= len(rows) {
		// No rows below the scroll point: sections still render (loading,
		// empty, or fully filtered out) so filtering keeps the layout stable.
		if stopRow >= 0 || scroll > 0 {
			return nil
		}
		var lines []string
		for _, k := range homeSectionKinds {
			if !m.homeSectionOmitted(k) {
				lines = append(lines, dimStyle.Render(m.homeSectionSeparator(k, width)))
			}
		}
		return lines
	}
	var lines []string
	prev := homeRowKind(-1)
	if scroll > 0 {
		prev = rows[scroll-1].kind
	}
	for i := scroll; i < len(rows); i++ {
		if rows[i].kind != prev && rows[i].kind != homeRowNew {
			// Sections passed over between two consecutive rows are
			// necessarily empty and render as their header line only.
			for _, k := range m.homeSectionsBetween(prev, rows[i].kind) {
				lines = append(lines, dimStyle.Render(m.homeSectionSeparator(k, width)))
			}
			lines = append(lines, dimStyle.Render(m.homeSectionSeparator(rows[i].kind, width)))
		}
		lines = append(lines, cursorLine(truncate(homeRowLabel(rows[i]), max(1, width-2)), i == m.home.cursor))
		prev = rows[i].kind
		if stopRow >= 0 && i == stopRow {
			break
		}
	}
	// Sections after the last row (empty or filtered out) keep rendering;
	// the scroll adjuster's counting variant skips them because the cursor
	// can never sit inside them.
	if stopRow < 0 {
		for _, k := range m.homeSectionsAfter(prev) {
			lines = append(lines, dimStyle.Render(m.homeSectionSeparator(k, width)))
		}
	}
	return lines
}

// homeSectionsAfter returns the non-omitted sections that follow from in
// display order (all of them when from is the sentinel or the "+ New
// playlist" row).
func (m Model) homeSectionsAfter(from homeRowKind) []homeRowKind {
	pos := func(k homeRowKind) int {
		for i, sk := range homeSectionKinds {
			if sk == k {
				return i
			}
		}
		return -1
	}
	fromPos := pos(from)
	var after []homeRowKind
	for i, k := range homeSectionKinds {
		if (fromPos < 0 || i > fromPos) && !m.homeSectionOmitted(k) {
			after = append(after, k)
		}
	}
	return after
}

// homeSectionsBetween returns the non-omitted sections whose display position
// lies strictly between from and to. Sections between two consecutive rows
// are necessarily empty, so they render as a single header line each.
func (m Model) homeSectionsBetween(from, to homeRowKind) []homeRowKind {
	pos := func(k homeRowKind) int {
		for i, sk := range homeSectionKinds {
			if sk == k {
				return i
			}
		}
		return -1 // sentinel or the "+ New playlist" row: nothing precedes them
	}
	fromPos, toPos := pos(from), pos(to)
	var between []homeRowKind
	for i, k := range homeSectionKinds {
		if (fromPos < 0 || i > fromPos) && (toPos < 0 || i < toPos) && !m.homeSectionOmitted(k) {
			between = append(between, k)
		}
	}
	return between
}

// homeSectionSeparator renders one section header line with its row count,
// padded to the sidebar width.
func (m Model) homeSectionSeparator(kind homeRowKind, width int) string {
	count := func(visible, total int, loading bool, loaded bool) string {
		switch {
		case loading && total == 0:
			return "loading…"
		case m.home.filter != "":
			return fmt.Sprintf("%d/%d", visible, total)
		case !loaded || visible == 0:
			return "none"
		default:
			return fmt.Sprintf("%d", visible)
		}
	}
	var label string
	switch kind {
	case homeRowPlaylist:
		label = "Playlists (" + count(len(m.homePlaylistsView()), len(m.home.lists), m.home.loadingLists, m.home.lists != nil) + ")"
	case homeRowAlbum:
		label = "Albums (" + count(len(m.homeAlbumsView()), len(m.home.albums), m.home.loadingAlbums, m.home.albums != nil) + ")"
	default:
		label = "Artists (" + count(len(m.homeArtistsView()), len(m.home.artists), m.home.loadingArtists, m.home.artists != nil) + ")"
	}
	line := "  ── " + label + " "
	if w := lipgloss.Width(line); w < width {
		line += strings.Repeat("─", width-w)
	}
	return truncate(line, max(1, width))
}

// homeSidebarListVisible is the sidebar list's row budget: the body budget
// minus the pane header line.
func (m Model) homeSidebarListVisible() int {
	return max(1, m.effectivePlaylistVisible()-1)
}

// homeSidebarMaybeAdjustScroll keeps the sidebar cursor visible, accounting
// for section header lines that share its row budget.
func (m *Model) homeSidebarMaybeAdjustScroll() {
	rows := m.homeRows()
	if len(rows) == 0 {
		m.home.cursor = 0
		m.home.scroll = 0
		return
	}
	m.home.cursor = min(max(m.home.cursor, 0), len(rows)-1)
	visible := m.homeSidebarListVisible()
	scroll := min(max(m.home.scroll, 0), len(rows)-1)
	if m.home.cursor < scroll {
		scroll = m.home.cursor
	}
	for scroll < m.home.cursor && len(m.homeSidebarLinesFrom(scroll, m.home.cursor, 1)) > visible {
		scroll++
	}
	m.home.scroll = scroll
}

// homeContentListVisible is the content list's row budget: the body budget
// minus the breadcrumb line.
func (m Model) homeContentListVisible() int {
	return max(1, m.effectivePlaylistVisible()-1)
}

func (m *Model) homeContentMaybeAdjustScroll() {
	c := &m.home.content
	clampScroll(&c.cursor, &c.scroll, len(c.tracks), m.homeContentListVisible())
}

func (m *Model) homeMaybeAdjustScroll() {
	m.homeSidebarMaybeAdjustScroll()
	m.homeContentMaybeAdjustScroll()
}

// — content pane —

// homeQueueConflict reports whether the queue is loading or incrementally
// paging the given playlist on the Home provider. Opening the same playlist
// in the content pane must then avoid TracksPage entirely (see the guard on
// fetchSpotPlaylistTracksCmd).
func (m Model) homeQueueConflict(playlistID string) bool {
	if m.home.prov == nil || m.provider == nil || m.home.prov.Name() != m.provider.Name() {
		return false
	}
	if m.trackPaging.active && m.trackPaging.playlistID == playlistID {
		return true
	}
	return m.provLoading && m.activeProviderPlaylistID == playlistID
}

// homeContentPaging reports whether the Home content pane is incrementally
// paging the given playlist on the same provider as the queue (the queue-side
// mirror of homeQueueConflict, checked before the queue pages a playlist).
func (m Model) homeContentPaging(playlistID string) bool {
	return m.home.visible &&
		m.home.prov != nil &&
		m.provider != nil &&
		m.home.prov.Name() == m.provider.Name() &&
		m.home.content.kind == homeContentPlaylist &&
		m.home.content.id == playlistID &&
		m.home.content.paging.active
}

// homeOpenPlaylist starts the content pane on one playlist, loaded
// incrementally via TracksPage unless the queue conflicts.
func (m *Model) homeOpenPlaylist(p playlist.PlaylistInfo) tea.Cmd {
	if m.home.prov == nil {
		return nil
	}
	m.home.content = homeContentState{kind: homeContentPlaylist, id: p.ID, name: p.Name, loading: true}
	m.home.focus = homePaneContent
	gen := nextRequest(&m.requests.homeContent)
	if m.homeQueueConflict(p.ID) {
		return fetchHomePlaylistTracksCmd(m.home.prov, m.home.prov.Name(), p.ID, gen)
	}
	pager, ok := m.home.prov.(provider.TrackPager)
	if !ok {
		return fetchHomePlaylistTracksCmd(m.home.prov, m.home.prov.Name(), p.ID, gen)
	}
	m.home.content.paging = homePagingState{active: true, loading: true}
	return fetchHomeTracksPageCmd(pager, m.home.prov.Name(), p.ID, 0, providerTrackPageSize, gen)
}

// homeOpenAlbum starts the content pane on one album's tracks.
func (m *Model) homeOpenAlbum(a provider.AlbumInfo) tea.Cmd {
	l, ok := m.home.prov.(provider.AlbumTrackLoader)
	if !ok {
		m.status.Show("Album tracks are not supported", statusTTLDefault)
		return nil
	}
	m.home.content = homeContentState{kind: homeContentAlbum, id: a.ID, name: a.Name, loading: true}
	m.home.focus = homePaneContent
	gen := nextRequest(&m.requests.homeContent)
	return fetchHomeAlbumTracksCmd(l, m.home.prov.Name(), a.ID, gen)
}

// homeCloseContent pops the content pane back to the sidebar and supersedes
// any in-flight content request (including an incremental page chain).
func (m *Model) homeCloseContent() {
	nextRequest(&m.requests.homeContent)
	m.home.content = homeContentState{}
	m.home.focus = homePaneSidebar
}

// — lazy album pages —

// maybeLoadHomeAlbums fetches the next AlbumList page when the cursor nears
// the end of the loaded album section (mirrors the nav browser's lazy load).
func (m *Model) maybeLoadHomeAlbums() tea.Cmd {
	if m.home.filter != "" || m.home.loadingAlbums || m.home.albumsDone || len(m.home.albums) == 0 {
		return nil
	}
	ab, ok := m.home.prov.(provider.AlbumBrowser)
	if !ok {
		return nil
	}
	rows := m.homeRows()
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].kind == homeRowAlbum {
			if m.home.cursor < i-10 {
				return nil
			}
			break
		}
	}
	m.home.loadingAlbums = true
	return fetchHomeAlbumsCmd(ab, m.home.prov.Name(), m.home.sortType, len(m.home.albums), nextRequest(&m.requests.homeAlbums))
}

// — album sort cycling —

// homeSortLabel resolves the provider album sort ID to its label.
func (m Model) homeSortLabel() string {
	if ab, ok := m.home.prov.(provider.AlbumBrowser); ok {
		for _, st := range ab.AlbumSortTypes() {
			if st.ID == m.home.sortType {
				return st.Label
			}
		}
	}
	return m.home.sortType
}

// homeCycleAlbumSort advances the provider-side album sort, persists it via
// AlbumSortSaver, and refetches the album list (mirrors the nav browser).
func (m *Model) homeCycleAlbumSort() tea.Cmd {
	ab, ok := m.home.prov.(provider.AlbumBrowser)
	if !ok {
		return nil
	}
	m.home.sortType = navNextSort(m.home.sortType, ab.AlbumSortTypes())
	m.home.albums = nil
	m.home.albumsDone = false
	m.home.loadingAlbums = true
	if saver, ok := m.home.prov.(provider.AlbumSortSaver); ok {
		if err := saver.SaveAlbumSort(m.home.sortType); err != nil {
			m.status.Showf(statusTTLDefault, "Sort save failed: %s", err)
		}
	}
	m.status.Showf(statusTTLDefault, "Albums sorted by %s", m.homeSortLabel())
	m.homeSidebarMaybeAdjustScroll()
	return fetchHomeAlbumsCmd(ab, m.home.prov.Name(), m.home.sortType, 0, nextRequest(&m.requests.homeAlbums))
}

// — key handling —

// handleHomeKey routes keys within the Home overlay. Text inputs (filter,
// new-playlist name) claim keys first; then the focused pane. Tab and
// ctrl+arrows cycle pane focus and never leak to the main view while Home is
// open.
func (m *Model) handleHomeKey(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "ctrl+c" {
		m.closeHomeView()
		return m.quit()
	}
	if m.home.screen == homeScreenNewName {
		return m.handleHomeNameKey(msg)
	}
	if m.home.filtering {
		return m.handleHomeFilterKey(msg)
	}
	if m.home.focus == homePaneContent && m.home.content.kind != homeContentNone {
		return m.handleHomeContentKey(msg)
	}
	return m.handleHomeSidebarKey(msg)
}

// homeJumpToSection moves the sidebar cursor to the first row of the given
// section (the 1/2/3 quick jump). No-op when the section is absent.
func (m *Model) homeJumpToSection(kind homeRowKind) {
	rows := m.homeRows()
	for i, row := range rows {
		if row.kind == kind {
			m.home.cursor = i
			m.homeSidebarMaybeAdjustScroll()
			return
		}
	}
}

func (m *Model) handleHomeSidebarKey(msg tea.KeyPressMsg) tea.Cmd {
	count := len(m.homeRows())
	move := func(delta int) {
		if delta < 0 && m.home.cursor > 0 {
			m.home.cursor--
		} else if delta < 0 && count > 0 {
			m.home.cursor = count - 1
		} else if delta > 0 && m.home.cursor < count-1 {
			m.home.cursor++
		} else if delta > 0 && count > 0 {
			m.home.cursor = 0
		}
		m.homeSidebarMaybeAdjustScroll()
	}

	switch msg.String() {
	case "ctrl+x":
		m.toggleExpandedView()
		m.homeMaybeAdjustScroll()
	case "up", "k", "ctrl+p":
		move(-1)
	case "down", "j", "ctrl+n":
		move(1)
		// Lazy-load the next album page when scrolling near the section end.
		return m.maybeLoadHomeAlbums()
	case "ctrl+u":
		step := m.homeSidebarListVisible()
		if m.home.cursor >= step {
			m.home.cursor -= step
		} else {
			m.home.cursor = 0
		}
		m.homeSidebarMaybeAdjustScroll()
	case "ctrl+d":
		step := m.homeSidebarListVisible()
		m.home.cursor += step
		if m.home.cursor >= count {
			m.home.cursor = max(0, count-1)
		}
		m.homeSidebarMaybeAdjustScroll()
		return m.maybeLoadHomeAlbums()
	case "g", "home":
		m.home.cursor = 0
		m.homeSidebarMaybeAdjustScroll()
	case "1", "2", "3":
		m.homeJumpToSection([]homeRowKind{homeRowPlaylist, homeRowAlbum, homeRowArtist}[msg.String()[0]-'1'])
	case "G", "end":
		if count > 0 {
			m.home.cursor = count - 1
		}
		m.homeSidebarMaybeAdjustScroll()
		return m.maybeLoadHomeAlbums()
	case "tab", "shift+tab", "ctrl+left", "ctrl+right", "ctrl+up", "ctrl+down":
		if m.home.content.kind != homeContentNone {
			m.home.focus = homePaneContent
		}
	case "enter", "l", "right":
		row, ok := m.homeRowAt()
		if !ok {
			return nil
		}
		switch row.kind {
		case homeRowNew:
			m.home.screen = homeScreenNewName
			m.home.newName = ""
			m.home.inputErr = ""
		case homeRowPlaylist:
			return m.homeOpenPlaylist(row.playlist)
		case homeRowAlbum:
			return m.homeOpenAlbum(row.album)
		default:
			return m.openArtistScreen(m.home.prov.Name(), row.artist)
		}
	case "/":
		if !m.home.filtering && m.home.filter != "" {
			m.home.filter = ""
			m.home.cursor = 0
			m.home.scroll = 0
			m.homeSidebarMaybeAdjustScroll()
		} else {
			m.home.filtering = true
		}
	case "s":
		m.home.order = (m.home.order + 1) % homeSortCount
		m.home.cursor = 0
		m.home.scroll = 0
		m.homeSidebarMaybeAdjustScroll()
		m.status.Showf(statusTTLDefault, "Library order: %s", homeSortLabels[m.home.order])
	case "S":
		return m.homeCycleAlbumSort()
	case "H":
		m.closeHomeView()
	case "esc", "backspace", "h", "left":
		// Pop the content drill level first, then close Home.
		if m.home.content.kind != homeContentNone {
			m.homeCloseContent()
		} else {
			m.closeHomeView()
		}
	}
	return nil
}

// homeContentTrackReady reports whether the content pane has a selectable
// track row under its cursor (used by key handlers and the help registry).
func (m Model) homeContentTrackReady() bool {
	c := m.home.content
	return m.home.visible &&
		c.kind != homeContentNone &&
		len(c.tracks) > 0 &&
		!c.loading &&
		c.cursor >= 0 && c.cursor < len(c.tracks)
}

// handleHomeContentKey handles keys while the content pane holds focus. Track
// rows carry the same actions as the artist screen's drill lists.
func (m *Model) handleHomeContentKey(msg tea.KeyPressMsg) tea.Cmd {
	c := &m.home.content
	count := len(c.tracks)
	move := func(delta int) {
		if delta < 0 && c.cursor > 0 {
			c.cursor--
		} else if delta < 0 && count > 0 {
			c.cursor = count - 1
		} else if delta > 0 && c.cursor < count-1 {
			c.cursor++
		} else if delta > 0 && count > 0 {
			c.cursor = 0
		}
		m.homeContentMaybeAdjustScroll()
	}
	switch msg.String() {
	case "ctrl+x":
		m.toggleExpandedView()
		m.homeContentMaybeAdjustScroll()
	case "up", "k", "ctrl+p":
		move(-1)
	case "down", "j", "ctrl+n":
		move(1)
	case "ctrl+u":
		step := m.homeContentListVisible()
		if c.cursor >= step {
			c.cursor -= step
		} else {
			c.cursor = 0
		}
		m.homeContentMaybeAdjustScroll()
	case "ctrl+d":
		step := m.homeContentListVisible()
		c.cursor += step
		if c.cursor >= count {
			c.cursor = max(0, count-1)
		}
		m.homeContentMaybeAdjustScroll()
	case "g", "home":
		c.cursor = 0
		m.homeContentMaybeAdjustScroll()
	case "G", "end":
		if count > 0 {
			c.cursor = count - 1
		}
		m.homeContentMaybeAdjustScroll()
	case "enter", "l":
		if !m.homeContentTrackReady() {
			return nil
		}
		return m.artistPlayFrom(c.tracks, c.cursor)
	case "a":
		if m.homeContentTrackReady() {
			return m.appendTrack(c.tracks[c.cursor])
		}
	case "q":
		if m.homeContentTrackReady() {
			return m.queueTrackNext(c.tracks[c.cursor])
		}
	case "*":
		if m.homeContentTrackReady() {
			return m.likeTrack(c.tracks[c.cursor])
		}
	case "p":
		if m.homeContentTrackReady() {
			track := c.tracks[c.cursor]
			return m.openPlaylistPicker([]playlist.Track{track}, "Track: "+track.DisplayName())
		}
	case "tab", "shift+tab", "ctrl+left", "ctrl+right", "ctrl+up", "ctrl+down":
		m.home.focus = homePaneSidebar
	case "H":
		m.closeHomeView()
	case "esc", "backspace", "h", "left":
		m.homeCloseContent()
	}
	return nil
}

// handleHomeFilterKey handles the sidebar-wide `/` filter input.
func (m *Model) handleHomeFilterKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.home.filtering = false
		m.home.filter = ""
		m.home.cursor = 0
		m.home.scroll = 0
		m.homeSidebarMaybeAdjustScroll()
		return nil
	case tea.KeyEnter:
		m.home.filtering = false
		return nil
	}
	if msg.Code == tea.KeySpace && msg.Text == "" {
		m.insertText("home-filter", &m.home.filter, " ")
	} else if !m.editText("home-filter", &m.home.filter, msg) {
		return nil
	}
	m.home.cursor = 0
	m.home.scroll = 0
	m.homeSidebarMaybeAdjustScroll()
	return nil
}

// handleHomeNameKey handles the "+ New playlist" name input.
func (m *Model) handleHomeNameKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.home.screen = homeScreenLibrary
		m.home.newName = ""
		m.home.inputErr = ""
		return nil
	case tea.KeyEnter:
		name := strings.TrimSpace(m.home.newName)
		if name == "" {
			m.home.inputErr = "Enter a name for the playlist."
			return nil
		}
		c, ok := m.home.prov.(provider.PlaylistCreator)
		if !ok {
			m.home.screen = homeScreenLibrary
			return nil
		}
		if m.home.creating {
			return nil
		}
		m.home.creating = true
		return createHomePlaylistCmd(m.newLikeContext(), c, m.home.prov.Name(), name, nextRequest(&m.requests.homeCreate))
	}
	if msg.Code == tea.KeySpace && msg.Text == "" {
		m.insertText("home-new-name", &m.home.newName, " ")
	} else if !m.editText("home-new-name", &m.home.newName, msg) {
		return nil
	}
	m.home.inputErr = ""
	return nil
}

// — completion handlers (called from Update) —

func (m *Model) handleHomeLists(msg homeListsMsg) {
	m.home.loadingLists = false
	if msg.err != nil {
		m.status.Showf(statusTTLDefault, "Playlists load failed: %s", msg.err)
		return
	}
	m.home.lists = filterHomePlaylists(msg.playlists)
	m.homeApplyFixup()
	m.homeSidebarMaybeAdjustScroll()
}

func (m *Model) handleHomeAlbums(msg homeAlbumsMsg) {
	m.home.loadingAlbums = false
	if msg.err != nil {
		m.status.Showf(statusTTLDefault, "Albums load failed: %s", msg.err)
		return
	}
	if msg.offset == 0 {
		m.home.albums = msg.albums
	} else {
		m.home.albums = append(m.home.albums, msg.albums...)
	}
	if msg.isLast {
		m.home.albumsDone = true
	}
	m.homeSidebarMaybeAdjustScroll()
}

func (m *Model) handleHomeArtists(msg homeArtistsMsg) {
	m.home.loadingArtists = false
	if msg.err != nil {
		m.status.Showf(statusTTLDefault, "Artists load failed: %s", msg.err)
		return
	}
	m.home.artists = msg.artists
	m.homeSidebarMaybeAdjustScroll()
}

// handleHomeContent applies a first/one-shot content load and arms the
// incremental page chain for playlists when more pages remain.
func (m *Model) handleHomeContent(msg homeContentMsg) tea.Cmd {
	c := &m.home.content
	c.loading = false
	if msg.err != nil {
		m.status.Showf(statusTTLDefault, "Load failed: %s", msg.err)
		m.homeCloseContent()
		return nil
	}
	c.tracks = msg.tracks
	c.cursor = 0
	c.scroll = 0
	c.paging = homePagingState{}
	if len(c.tracks) == 0 {
		m.status.Show("No tracks found", statusTTLDefault)
	}
	m.homeContentMaybeAdjustScroll()
	if c.kind == homeContentPlaylist && len(c.tracks) < msg.total {
		pager, ok := m.home.prov.(provider.TrackPager)
		if !ok || m.homeQueueConflict(c.id) {
			return nil
		}
		c.paging = homePagingState{active: true, offset: len(c.tracks), total: msg.total, loading: true}
		return fetchHomeTracksPageCmd(pager, msg.providerName, c.id, len(c.tracks), providerTrackPageSize, msg.gen)
	}
	return nil
}

// handleHomePage appends one incrementally loaded playlist page and chains
// the next under the same generation.
func (m *Model) handleHomePage(msg homePageMsg) tea.Cmd {
	c := &m.home.content
	c.paging.loading = false
	if msg.err != nil {
		c.paging.active = false
		m.status.Showf(statusTTLDefault, "Loading more tracks failed: %s", msg.err)
		return nil
	}
	if !c.paging.active || msg.offset != c.paging.offset {
		c.paging.active = false
		return nil
	}
	c.tracks = append(c.tracks, msg.tracks...)
	c.paging.offset += len(msg.tracks)
	c.paging.total = msg.total
	m.homeContentMaybeAdjustScroll()
	if len(msg.tracks) == 0 || c.paging.offset >= c.paging.total {
		c.paging.active = false
		return nil
	}
	pager, ok := m.home.prov.(provider.TrackPager)
	if !ok {
		c.paging.active = false
		return nil
	}
	c.paging.loading = true
	return fetchHomeTracksPageCmd(pager, msg.providerName, c.id, c.paging.offset, providerTrackPageSize, msg.gen)
}
