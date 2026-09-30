package model

// immersive.go implements the v1 immersive frame: a full-width visualizer
// band, a nav-pill row, a two-column body (Now Playing + Queue on the left,
// the section canvas on the right), a centered controls row, and an
// eighth-block progress bar — over the same provider data and player the
// normal TUI already drives. No new data plumbing: the canvas reads the
// provider's existing playlist/artist/album/show/search surfaces.
//
// The mode stays opt-in: `I` toggles it (or `immersive = true` in config),
// every key and renderer lives behind m.immersiveShown(), and the classic
// layout takes over whenever the terminal is too small for the wireframe.
// Cover art is still the shade-glyph placeholder (immArtBlock); every box
// art occupies is a fixed cell-aligned rect exposed via ImmersiveArtRects()
// so a later image layer can blit into the same rectangles.

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// immersivePane identifies which region of the frame holds keyboard focus.
type immersivePane int

const (
	immPaneNav    immersivePane = iota // nav pill row
	immPaneCanvas                      // section canvas (right column)
	immPaneQueue                       // queue panel (left column)
	immPaneCount
)

// immSection is a nav-pill destination — what the canvas browses.
type immSection int

const (
	immSecPlaylists immSection = iota
	immSecArtists
	immSecSearch
	immSecAlbums
	immSecPodcasts
	immSecCount
)

// The history buttons share the pill row but are not sections.
const (
	immNavBack immSection = -1 - iota
	immNavForward
)

// immSectionOrder is the visual pill order: two pills left of the wide
// center search pill, two pills right of it.
var immSectionOrder = [immSecCount]immSection{
	immSecPlaylists, immSecArtists, immSecSearch, immSecAlbums, immSecPodcasts,
}

var immSectionLabels = [immSecCount]string{
	"Playlists", "Artists", "Search", "Albums", "Podcasts",
}

// immersiveView identifies what the canvas is showing.
type immersiveView int

const (
	immViewBrowse   immersiveView = iota // item list of the active section
	immViewPlaylist                      // opened playlist's tracks
	immViewAlbum                         // opened album's tracks
	immViewArtist                        // artist detail (popular tracks)
	immViewShow                          // podcast show episodes
	immViewSearch                        // search results
	immViewSettings                      // settings/EQ tab (key-only, no pill)
)

// isTrackView reports whether the canvas shows a track list (detail or
// search results) rather than a collection browse or the settings tab.
func (v immersiveView) isTrackView() bool {
	switch v {
	case immViewPlaylist, immViewAlbum, immViewArtist, immViewShow, immViewSearch:
		return true
	}
	return false
}

// immCanvasMode is the canvas presentation style cycled by `v`.
type immCanvasMode int

const (
	immCanvasList immCanvasMode = iota // one line per item
	immCanvasRows                      // 3-row blocks with an art box
	immCanvasGrid                      // art tiles with titles below
	immCanvasModeCount
)

var immCanvasModeNames = [immCanvasModeCount]string{"list", "rows", "grid"}

// immersiveTrackSort is the client-side track-list ordering.
type immersiveTrackSort int

const (
	immSortTrackOrder immersiveTrackSort = iota // provider order
	immSortTrackTitle
	immSortTrackAlbum
	immSortTrackDuration
	immSortTrackCount
)

var immSortTrackLabels = [immSortTrackCount]string{"#", "title", "album", "time"}

// immBrowseSort cycles the browse canvas's client-side ordering.
type immBrowseSort int

const (
	immBrowseSortRecents immBrowseSort = iota // provider order
	immBrowseSortAlpha                        // case-insensitive by name
	immBrowseSortCount
)

var immBrowseSortLabels = [immBrowseSortCount]string{"recents", "a-z"}

// immItemKind distinguishes what a canvas item opens into.
type immItemKind int

const (
	immKindPlaylist immItemKind = iota
	immKindAlbum
	immKindArtist
	immKindShow // podcast show
	immKindTrack
)

// immItem is one selectable canvas row: a collection in a browse view or a
// track in a detail/search view (id then holds its index in sortedTracks()).
type immItem struct {
	kind  immItemKind
	id    string
	title string
	sub   string // artist / playlist summary / show author
	sub2  string // album (tracks only)
	dur   int    // seconds; tracks only
	path  string // tracks only: used to mark the playing row
	art   string // cover image URL, "" when none
}

// Minimum terminal size for the immersive frame; below it the classic
// layout takes over automatically.
const (
	immMinWidth  = 80
	immMinHeight = 24
)

// immNavSnap is one entry on the canvas back stack.
type immNavSnap struct {
	section   immSection
	view      immersiveView
	ctxID     string
	ctxName   string
	ctxSub    string
	ctxKind   immItemKind
	tracks    []playlist.Track
	trackSort immersiveTrackSort
	cursor    int
	scroll    int
}

// immersiveState holds every piece of the immersive mode. It is the one
// state block the mode touches, so leaving immersive never disturbs normal
// state.
type immersiveState struct {
	active bool
	prov   playlist.Provider // provider the canvas is browsing
	focus  immersivePane

	// — nav pills —
	section immSection

	// — section data —
	lists          []playlist.PlaylistInfo
	albums         []provider.AlbumInfo
	artists        []provider.ArtistInfo
	loadingLists   bool
	loadingAlbums  bool
	loadingArtists bool

	// — canvas —
	view      immersiveView
	mode      immCanvasMode
	ctxID     string // playlist/album/artist/show id the open view belongs to
	ctxName   string
	ctxSub    string
	ctxKind   immItemKind
	cursor    int // item index into canvasItems()/sortedTracks()
	scroll    int // first visible item (list/rows) or tile row (grid)
	filtering bool
	filter    string
	sort      immBrowseSort

	tracks        []playlist.Track
	tracksLoading bool
	trackSort     immersiveTrackSort
	needsAuth     bool         // the provider wants a sign-in before it lists anything
	back          []immNavSnap // history behind the current canvas (Back)
	fwd           []immNavSnap // history undone by Back (Forward)

	settingsReturn immersiveView // view `e` returns to
	settingsCursor int           // row inside the settings tab

	// — queue panel —
	queueCursor int
	queueScroll int

	// — search pill input —
	searching   bool
	searchQuery string

	searchLoading bool

	// spin advances on every tick while any immersive fetch is in flight,
	// driving the loading indicators.
	spin int
}

// immSpinFrames is the braille spinner cycled by loading indicators.
var immSpinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// immSpin returns the current spinner frame for a loading indicator.
func (m Model) immSpin() string {
	return immSpinFrames[m.immersive.spin%len(immSpinFrames)]
}

// immLoadingActive reports whether any immersive fetch is in flight.
func (m Model) immLoadingActive() bool {
	im := m.immersive
	return im.loadingLists || im.loadingAlbums || im.loadingArtists ||
		im.tracksLoading || im.searchLoading
}

// — messages —

type immersiveListsMsg struct {
	lists        []playlist.PlaylistInfo
	providerName string
	gen          uint64
	err          error
}

type immersiveAlbumsMsg struct {
	albums       []provider.AlbumInfo
	providerName string
	gen          uint64
	err          error
}

type immersiveArtistsMsg struct {
	artists      []provider.ArtistInfo
	providerName string
	gen          uint64
	err          error
}

// immersiveContentMsg carries the track list for an opened playlist, album,
// or podcast show.
type immersiveContentMsg struct {
	kind         immItemKind
	id           string
	name         string
	sub          string
	tracks       []playlist.Track
	providerName string
	gen          uint64
	err          error
}

// immersiveArtistMsg carries an artist profile for the artist view.
type immersiveArtistMsg struct {
	detail       provider.ArtistDetail
	providerName string
	gen          uint64
	err          error
}

// immersiveSearchMsg carries global-search results into the canvas.
type immersiveSearchMsg struct {
	tracks       []playlist.Track
	providerName string
	gen          uint64
	err          error
}

// openImmersiveMsg asks Update to enter immersive mode once the program is
// running (Init has a value receiver and cannot mutate the model).
type openImmersiveMsg struct{}

// — command constructors —

func fetchImmersiveListsCmd(prov playlist.Provider, gen uint64) tea.Cmd {
	return func() tea.Msg {
		lists, err := prov.Playlists()
		return immersiveListsMsg{lists: lists, providerName: prov.Name(), gen: gen, err: err}
	}
}

func fetchImmersiveAlbumsCmd(b provider.AlbumBrowser, providerName, sortType string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		albums, err := b.AlbumList(sortType, 0, navAlbumPageSize)
		return immersiveAlbumsMsg{albums: albums, providerName: providerName, gen: gen, err: err}
	}
}

func fetchImmersiveArtistsCmd(ab provider.ArtistBrowser, providerName string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		artists, err := ab.Artists()
		return immersiveArtistsMsg{artists: artists, providerName: providerName, gen: gen, err: err}
	}
}

func fetchImmersiveContentCmd(prov playlist.Provider, kind immItemKind, id, name, sub string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		tracks, err := prov.Tracks(id)
		return immersiveContentMsg{kind: kind, id: id, name: name, sub: sub, tracks: tracks, providerName: prov.Name(), gen: gen, err: err}
	}
}

func fetchImmersiveAlbumCmd(l provider.AlbumTrackLoader, providerName string, kind immItemKind, id, name, sub string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		tracks, err := l.AlbumTracks(id)
		return immersiveContentMsg{kind: kind, id: id, name: name, sub: sub, tracks: tracks, providerName: providerName, gen: gen, err: err}
	}
}

func fetchImmersiveArtistCmd(l provider.ArtistDetailLoader, providerName, id string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		detail, err := l.ArtistDetail(id)
		return immersiveArtistMsg{detail: detail, providerName: providerName, gen: gen, err: err}
	}
}

func fetchImmersiveSearchCmd(ctx context.Context, s provider.Searcher, providerName, query string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		tracks, err := s.SearchTracks(ctx, query, 50)
		return immersiveSearchMsg{tracks: tracks, providerName: providerName, gen: gen, err: err}
	}
}

// — open / close —

// immersiveFits reports whether the terminal can hold the immersive frame.
// The classic layout takes over below this size.
func (m Model) immersiveFits() bool {
	return m.width >= immMinWidth && m.height >= immMinHeight
}

// immersiveShown reports whether the immersive frame is the one on screen:
// the mode is active and the terminal is currently big enough.
func (m Model) immersiveShown() bool {
	return m.immersive.active && m.immersiveFits()
}

// toggleImmersive enters immersive when off, exits when on.
func (m *Model) toggleImmersive() tea.Cmd {
	if m.immersive.active {
		m.exitImmersive()
		return nil
	}
	return m.enterImmersive()
}

// enterImmersive switches to the immersive frame and kicks the section
// fetches. The provider's existing playlist cache seeds Playlists when it
// is already settled for this provider.
func (m *Model) enterImmersive() tea.Cmd {
	if !m.immersiveFits() {
		m.status.Showf(statusTTLDefault, "Immersive needs a %dx%d terminal", immMinWidth, immMinHeight)
		return nil
	}
	return m.startImmersive()
}

// startImmersive activates the mode without the size check; the frame
// stays hidden behind the classic layout while the terminal is too small.
func (m *Model) startImmersive() tea.Cmd {
	m.immersive = immersiveState{
		active:         true,
		prov:           m.provider,
		focus:          immPaneCanvas,
		section:        immSecPlaylists,
		view:           immViewBrowse,
		mode:           m.immCanvasPref,
		sort:           immBrowseSortRecents,
		settingsReturn: immViewBrowse,
	}
	m.recomputeLayout() // the visualizer takes the band's height
	artCmd := m.startArtPolling()
	if m.provider == nil {
		return artCmd
	}
	if _, ok := m.provider.(provider.AlbumBrowser); ok {
		m.immersive.loadingAlbums = true
	}
	if _, ok := m.provider.(provider.ArtistBrowser); ok {
		m.immersive.loadingArtists = true
	}
	if len(m.providerLists) > 0 {
		// Reuse the already-fetched list; browse-route pseudo rows are
		// filtered out of the canvas.
		m.immersive.lists = m.filterImmersivePlaylists(m.providerLists)
	} else {
		m.immersive.loadingLists = true
	}
	return tea.Batch(artCmd, m.fetchImmersiveSidebar())
}

// exitImmersive leaves the mode and supersedes every in-flight fetch.
func (m *Model) exitImmersive() {
	nextRequest(&m.requests.immersiveLists)
	nextRequest(&m.requests.immersiveAlbums)
	nextRequest(&m.requests.immersiveArtists)
	nextRequest(&m.requests.immersiveContent)
	nextRequest(&m.requests.immersiveArtist)
	nextRequest(&m.requests.immersiveSearch)
	m.immersive = immersiveState{}
	if m.immMouse != nil {
		m.immMouse.valid = false
	}
	m.recomputeLayout() // hand the visualizer back its classic height
}

// fetchImmersiveSidebar dispatches the outstanding section fetches, one
// generation per section like the Home overlay.
func (m *Model) fetchImmersiveSidebar() tea.Cmd {
	prov := m.immersive.prov
	if prov == nil {
		return nil
	}
	providerName := prov.Name()
	var cmds []tea.Cmd
	if m.immersive.loadingLists {
		cmds = append(cmds, fetchImmersiveListsCmd(prov, nextRequest(&m.requests.immersiveLists)))
	}
	if ab, ok := prov.(provider.AlbumBrowser); ok && m.immersive.loadingAlbums {
		sortType := ab.DefaultAlbumSort()
		cmds = append(cmds, fetchImmersiveAlbumsCmd(ab, providerName, sortType, nextRequest(&m.requests.immersiveAlbums)))
	}
	if ab, ok := prov.(provider.ArtistBrowser); ok && m.immersive.loadingArtists {
		cmds = append(cmds, fetchImmersiveArtistsCmd(ab, providerName, nextRequest(&m.requests.immersiveArtists)))
	}
	if len(cmds) == 0 {
		return nil
	}
	if len(cmds) == 1 {
		return cmds[0]
	}
	return tea.Batch(cmds...)
}

// isCurrentImmersiveRequest reports whether a completion still belongs to
// the mode's current provider session.
func (m Model) isCurrentImmersiveRequest(gen uint64, providerName string, cur uint64) bool {
	return m.immersive.active &&
		m.immersive.prov != nil &&
		m.immersive.prov.Name() == providerName &&
		gen == cur
}

// filterImmersivePlaylists drops UI-only browse-route pseudo entries; real
// provider rows — including synthetic library rows like "Liked Songs" —
// stay.
func (m Model) filterImmersivePlaylists(lists []playlist.PlaylistInfo) []playlist.PlaylistInfo {
	out := make([]playlist.PlaylistInfo, 0, len(lists))
	for _, l := range lists {
		if _, browse := providerBrowseEntryForID(m.immersive.prov, l.ID); browse {
			continue
		}
		out = append(out, l)
	}
	return out
}

// — canvas items —

// browseItems returns the item rows for the active browse section with
// filter and sort applied. Fetched data is never mutated.
func (m Model) browseItems() []immItem {
	im := m.immersive
	var items []immItem
	switch im.section {
	case immSecPlaylists:
		for _, l := range im.lists {
			items = append(items, immItem{
				kind: immKindPlaylist, id: l.ID, title: l.Name,
				sub: playlistRowSub(l), art: l.ImageURL,
			})
		}
	case immSecArtists:
		for _, a := range im.artists {
			items = append(items, immItem{
				kind: immKindArtist, id: a.ID, title: a.Name,
				sub: "Artist", art: a.ImageURL,
			})
		}
	case immSecAlbums:
		for _, a := range im.albums {
			items = append(items, immItem{
				kind: immKindAlbum, id: a.ID, title: a.Name,
				sub: firstNonEmpty(a.Artist, "Album"), art: a.ImageURL,
			})
		}
	case immSecPodcasts:
		if l, ok := im.prov.(provider.SubscriptionLister); ok {
			for _, s := range l.Subscriptions() {
				items = append(items, immItem{
					kind: immKindShow, id: s.ID, title: s.Name,
					sub: firstNonEmpty(s.Author, "Podcast"),
				})
			}
		}
	}
	if im.filter != "" {
		q := strings.ToLower(im.filter)
		kept := items[:0]
		for _, it := range items {
			if strings.Contains(strings.ToLower(it.title), q) ||
				strings.Contains(strings.ToLower(it.sub), q) {
				kept = append(kept, it)
			}
		}
		items = kept
	}
	if im.sort == immBrowseSortAlpha {
		sort.SliceStable(items, func(i, j int) bool {
			return strings.ToLower(items[i].title) < strings.ToLower(items[j].title)
		})
	}
	return items
}

// trackItems wraps a track list as canvas items; id is the track's index in
// the list so activation maps back to the same sort order.
func trackItems(tracks []playlist.Track) []immItem {
	items := make([]immItem, 0, len(tracks))
	for i, t := range tracks {
		if t.IsAlbum() {
			// Search results lead with album hits; they open the album
			// rather than play (they are placeholders, not tracks).
			items = append(items, immItem{
				kind: immKindAlbum, id: t.AlbumID(), title: t.Title,
				sub: "Album · " + firstNonEmpty(t.Artist, "Various"), art: t.AlbumArtURL,
			})
			continue
		}
		title := t.Title
		if title == "" {
			title = trackViewName(t)
		}
		items = append(items, immItem{
			kind:  immKindTrack,
			id:    strconv.Itoa(i),
			title: title,
			sub:   t.Artist,
			sub2:  t.Album,
			dur:   t.DurationSecs,
			path:  t.Path,
			art:   t.AlbumArtURL,
		})
	}
	return items
}

// canvasItems returns the items the canvas lists in its current view.
// Track views wrap sortedTracks(); the settings tab has no items.
func (m Model) canvasItems() []immItem {
	im := m.immersive
	if im.view == immViewSettings {
		return nil
	}
	if im.view.isTrackView() {
		return trackItems(m.sortedTracks())
	}
	return m.browseItems()
}

func playlistRowSub(l playlist.PlaylistInfo) string {
	sub := "Playlist"
	if l.TrackCount > 0 {
		sub += " · " + plural(l.TrackCount, "song")
	}
	return sub
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// — section / view switching —

// immRootView is the view a section lands on: the search pill opens the
// results view, everything else browses its collection list.
func immRootView(s immSection) immersiveView {
	if s == immSecSearch {
		return immViewSearch
	}
	return immViewBrowse
}

// immersiveSetSection switches the canvas to a nav-pill section as one
// history step, so Back returns to where the pill was clicked.
func (m *Model) immersiveSetSection(s immSection) {
	im := &m.immersive
	if s == im.section && im.view == immRootView(s) && im.ctxName == "" {
		im.cursor, im.scroll = 0, 0
		return
	}
	m.pushImmersiveBack()
	im.section = s
	im.filter = ""
	m.immersiveResetRoot()
}

// immersiveHome returns the canvas to the section's root view as one
// history step.
func (m *Model) immersiveHome() {
	m.immersiveSetSection(m.immersive.section)
}

// immersiveResetRoot lands on the section's root view, dropping the opened
// context so no earlier list shows under the new view. History is untouched.
func (m *Model) immersiveResetRoot() {
	m.dropImmersiveFetches()
	im := &m.immersive
	im.cursor, im.scroll = 0, 0
	im.view = immRootView(im.section)
	im.ctxID, im.ctxName, im.ctxSub = "", "", ""
	im.tracks = nil
}

// noteImmersiveLoadErr records a failed section fetch: a sign-in request
// turns the canvas into the sign-in prompt, anything else is reported.
func (m *Model) noteImmersiveLoadErr(what string, err error) {
	switch {
	case err == nil:
	case errors.Is(err, playlist.ErrNeedsAuth):
		m.immersive.needsAuth = true
	default:
		m.status.Errorf(statusTTLDefault, "%s failed to load: %s", what, err)
	}
}

// immersiveSignIn starts the provider's interactive sign-in (it opens the
// browser); the result arrives as provAuthDoneMsg.
func (m *Model) immersiveSignIn() tea.Cmd {
	auth, ok := m.immersive.prov.(playlist.Authenticator)
	if !ok || m.provider == nil {
		return nil
	}
	m.provLoading = true // lets ProvAuthURLMsg through for the prompt
	return authenticateProviderCmd(auth, m.provider.Name(), nextRequest(&m.requests.auth))
}

// dropImmersiveFetches invalidates in-flight track fetches (collection,
// artist, search) so a late result cannot land in the view that replaced
// the one that asked for it.
func (m *Model) dropImmersiveFetches() {
	nextRequest(&m.requests.immersiveContent)
	nextRequest(&m.requests.immersiveArtist)
	nextRequest(&m.requests.immersiveSearch)
	m.immersive.tracksLoading = false
	m.immersive.searchLoading = false
}

// — navigation —

// openImmersiveItem opens a canvas item into its track view; tracks play
// directly instead of opening.
func (m *Model) openImmersiveItem(item immItem) tea.Cmd {
	prov := m.immersive.prov
	if prov == nil {
		return nil
	}
	m.pushImmersiveBack()
	return m.loadImmersiveItem(item)
}

// loadImmersiveItem shows item's track view and fetches its tracks, without
// touching history (openImmersiveItem records it; a history step whose list
// never finished loading reloads through here).
func (m *Model) loadImmersiveItem(item immItem) tea.Cmd {
	prov := m.immersive.prov
	if prov == nil {
		return nil
	}
	m.dropImmersiveFetches()
	providerName := prov.Name()
	gen := nextRequest(&m.requests.immersiveContent)
	m.immersive.tracks = nil
	m.immersive.tracksLoading = true
	m.immersive.cursor, m.immersive.scroll = 0, 0
	m.immersive.trackSort = immSortTrackOrder
	m.immersive.ctxID, m.immersive.ctxName, m.immersive.ctxSub = item.id, item.title, item.sub
	m.immersive.ctxKind = item.kind

	switch item.kind {
	case immKindPlaylist:
		m.immersive.view = immViewPlaylist
		return fetchImmersiveContentCmd(prov, item.kind, item.id, item.title, item.sub, gen)
	case immKindAlbum:
		m.immersive.view = immViewAlbum
		if l, ok := prov.(provider.AlbumTrackLoader); ok {
			return fetchImmersiveAlbumCmd(l, providerName, item.kind, item.id, item.title, item.sub, gen)
		}
	case immKindArtist:
		m.immersive.view = immViewArtist
		if l, ok := prov.(provider.ArtistDetailLoader); ok {
			return fetchImmersiveArtistCmd(l, providerName, item.id, nextRequest(&m.requests.immersiveArtist))
		}
	case immKindShow:
		m.immersive.view = immViewShow
		if l, ok := prov.(provider.AlbumTrackLoader); ok {
			return fetchImmersiveAlbumCmd(l, providerName, item.kind, item.id, item.title, item.sub, gen)
		}
		return fetchImmersiveContentCmd(prov, item.kind, item.id, item.title, item.sub, gen)
	}
	m.immersive.tracksLoading = false
	return nil
}

// immersiveSnap captures the current canvas context for the history stacks.
func (m Model) immersiveSnap() immNavSnap {
	im := m.immersive
	return immNavSnap{
		section:   im.section,
		view:      im.view,
		ctxID:     im.ctxID,
		ctxName:   im.ctxName,
		ctxSub:    im.ctxSub,
		ctxKind:   im.ctxKind,
		tracks:    im.tracks,
		trackSort: im.trackSort,
		cursor:    im.cursor,
		scroll:    im.scroll,
	}
}

// pushImmersiveBack records the current canvas before a new navigation,
// which also discards the forward history. Settings is transient and never
// recorded.
func (m *Model) pushImmersiveBack() {
	m.immersive.fwd = nil
	if m.immersive.view == immViewSettings {
		return
	}
	m.immersive.back = append(m.immersive.back, m.immersiveSnap())
}

// immersiveGoBack steps back through the canvas history. With no history it
// leaves a drill-down for the section root.
func (m *Model) immersiveGoBack() tea.Cmd {
	im := &m.immersive
	if im.view == immViewSettings {
		im.view = im.settingsReturn // Settings is a tab, not a history step
		return nil
	}
	n := len(im.back)
	if n == 0 {
		if im.view != immRootView(im.section) || im.ctxName != "" {
			im.fwd = append(im.fwd, m.immersiveSnap())
			m.immersiveResetRoot()
		}
		return nil
	}
	snap := im.back[n-1]
	im.back = im.back[:n-1]
	im.fwd = append(im.fwd, m.immersiveSnap())
	return m.immersiveRestore(snap)
}

// immersiveGoForward re-applies the history Back undid.
func (m *Model) immersiveGoForward() tea.Cmd {
	im := &m.immersive
	n := len(im.fwd)
	if n == 0 {
		return nil
	}
	snap := im.fwd[n-1]
	im.fwd = im.fwd[:n-1]
	if im.view != immViewSettings {
		im.back = append(im.back, m.immersiveSnap())
	}
	return m.immersiveRestore(snap)
}

// immersiveRestore puts a recorded canvas context back on screen; a
// collection whose tracks never arrived (it was left while loading) loads
// again.
func (m *Model) immersiveRestore(snap immNavSnap) tea.Cmd {
	m.dropImmersiveFetches()
	im := &m.immersive
	if snap.section != im.section {
		im.filter = ""
	}
	im.section = snap.section
	im.view = snap.view
	im.ctxID, im.ctxName, im.ctxSub = snap.ctxID, snap.ctxName, snap.ctxSub
	im.ctxKind = snap.ctxKind
	im.tracks = snap.tracks
	im.trackSort = snap.trackSort
	im.cursor = snap.cursor
	im.scroll = snap.scroll
	if snap.tracks == nil && snap.view.isTrackView() && snap.view != immViewSearch && snap.ctxID != "" {
		cmd := m.loadImmersiveItem(immItem{kind: snap.ctxKind, id: snap.ctxID, title: snap.ctxName, sub: snap.ctxSub})
		im.cursor, im.scroll, im.trackSort = snap.cursor, snap.scroll, snap.trackSort
		return cmd
	}
	return nil
}

// sortedTracks returns the open context's tracks in the active list order.
func (m Model) sortedTracks() []playlist.Track {
	tracks := m.immersive.tracks
	if m.immersive.trackSort == immSortTrackOrder {
		return tracks
	}
	out := make([]playlist.Track, len(tracks))
	copy(out, tracks)
	switch m.immersive.trackSort {
	case immSortTrackTitle:
		sort.SliceStable(out, func(i, j int) bool {
			return strings.ToLower(trackViewName(out[i])) < strings.ToLower(trackViewName(out[j]))
		})
	case immSortTrackAlbum:
		sort.SliceStable(out, func(i, j int) bool {
			return strings.ToLower(out[i].Album) < strings.ToLower(out[j].Album)
		})
	case immSortTrackDuration:
		sort.SliceStable(out, func(i, j int) bool {
			return out[i].DurationSecs < out[j].DurationSecs
		})
	}
	return out
}

// playImmersiveContext loads the open context's tracks into the queue and
// plays from index startIdx (in sorted order). Context is remembered so the
// queue panel can name what is playing next.
func (m *Model) playImmersiveContext(startIdx int) tea.Cmd {
	tracks, startIdx := playableFrom(m.sortedTracks(), startIdx)
	if len(tracks) == 0 {
		return nil
	}
	m.replacePlayerPlaylist(tracks)
	if startIdx < 0 || startIdx >= m.playlist.Len() {
		startIdx = 0
	}
	m.plCursor = startIdx
	m.playlist.SetIndex(startIdx)
	if m.immersive.view == immViewPlaylist {
		m.activeProviderPlaylistID = m.immersive.ctxID
		m.loadedPlaylist = ""
	}
	cmd := m.playCurrentTrack()
	m.notifyPlayback()
	return cmd
}

// playableFrom drops album placeholders (search results lead with them) so
// they never enter the play queue, remapping index into the kept tracks.
func playableFrom(tracks []playlist.Track, index int) ([]playlist.Track, int) {
	kept := make([]playlist.Track, 0, len(tracks))
	at := 0
	for i, t := range tracks {
		if t.IsAlbum() {
			continue
		}
		if i <= index {
			at = len(kept)
		}
		kept = append(kept, t)
	}
	return kept, at
}

// playImmersiveTrack plays the track at index in the open context.
func (m *Model) playImmersiveTrack(index int) tea.Cmd {
	all := m.sortedTracks()
	if index < 0 || index >= len(all) || all[index].IsAlbum() {
		return nil
	}
	tracks, index := playableFrom(all, index)
	m.replacePlayerPlaylist(tracks)
	m.plCursor = index
	m.playlist.SetIndex(index)
	if m.immersive.view == immViewPlaylist {
		m.activeProviderPlaylistID = m.immersive.ctxID
	}
	cmd := m.playCurrentTrack()
	m.notifyPlayback()
	return cmd
}

func wrapIndex(i, n int) int {
	if n <= 0 {
		return 0
	}
	i %= n
	if i < 0 {
		i += n
	}
	return i
}

// tickImmersive advances the loading spinner while fetches are in flight.
func (m *Model) tickImmersive() {
	if m.immersive.active && m.immLoadingActive() {
		m.immersive.spin++
	}
}
