package model

// immersive.go implements the prototype "immersive" Spotify-style mode: a
// three-pane presentation layer (library rail / content pane / now-playing or
// queue rail) plus a bottom player bar, laid over the same provider data and
// player the normal TUI already drives. No new data plumbing — the pane reads
// the provider's existing playlist/artist/album/track/search surfaces.
//
// Normal mode is untouched while the mode is off: `I` toggles it, every key
// and renderer lives behind m.immersive.active, and exiting restores the
// regular screens exactly where they were.

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

// immersivePane identifies which pane of the frame holds keyboard focus.
type immersivePane int

const (
	immPaneRail immersivePane = iota
	immPaneCenter
	immPaneRight
	immPaneCount
)

// immersiveRailSection is the active library-rail filter pill.
type immersiveRailSection int

const (
	immSectionPlaylists immersiveRailSection = iota
	immSectionAlbums
	immSectionArtists
	immSectionCount
)

var immSectionLabels = [immSectionCount]string{"Playlists", "Albums", "Artists"}

// immersiveView identifies what the center pane is showing.
type immersiveView int

const (
	immViewHome     immersiveView = iota // rolodex + card grid
	immViewPlaylist                      // hero + track table
	immViewAlbum                         // hero + track table
	immViewArtist                        // artist hero + popular tracks
	immViewSearch                        // search results table
)

// immersiveTrackSort is the client-side track-table ordering.
type immersiveTrackSort int

const (
	immSortTrackOrder immersiveTrackSort = iota // provider order
	immSortTrackTitle
	immSortTrackAlbum
	immSortTrackDuration
	immSortTrackCount
)

var immSortTrackLabels = [immSortTrackCount]string{"#", "title", "album", "time"}

// immersiveRightTab is the active right-rail tab.
type immersiveRightTab int

const (
	immTabNowPlaying immersiveRightTab = iota
	immTabQueue
	immTabCount
)

// immRailSort cycles the rail's client-side ordering.
type immRailSort int

const (
	immRailSortRecents immRailSort = iota // provider order
	immRailSortAlpha                      // case-insensitive by name
	immRailSortCount
)

var immRailSortLabels = [immRailSortCount]string{"recents", "a-z"}

// roloItem is one card in the rolodex strip. kind mirrors the rail section the
// item came from (or immKindTrack for the song picker).
type roloItemKind int

const (
	roloKindPlaylist roloItemKind = iota // square card
	roloKindAlbum                        // square card
	roloKindArtist                       // circle card
	roloKindTrack                        // square card (song picker)
)

type roloItem struct {
	kind  roloItemKind
	id    string
	title string
	sub   string
}

// centerZone tracks which sub-region of the center pane is focused.
type centerZone int

const (
	zoneRolo  centerZone = iota // rolodex strip (home) / full rolodex
	zoneGrid                    // card grid (home below rolodex)
	zoneTable                   // track table (playlist/album/artist/search)
)

// immNavSnap is one entry on the center-pane back stack.
type immNavSnap struct {
	view        immersiveView
	ctxID       string
	ctxName     string
	ctxSub      string
	ctxKind     roloItemKind
	tracks      []playlist.Track
	trackCursor int
	trackScroll int
	gridCursor  int
	roloCursor  int
}

// immersiveState holds every piece of the immersive mode. It is the one state
// block the mode touches, so leaving immersive never disturbs normal state.
type immersiveState struct {
	active bool
	prov   playlist.Provider // provider the pane is browsing
	focus  immersivePane
	zone   centerZone // focus within the center pane

	// — library rail —
	railCollapsed bool
	railSection   immersiveRailSection
	railCursor    int
	railScroll    int
	railFiltering bool
	railFilter    string
	railSort      immRailSort

	lists          []playlist.PlaylistInfo
	albums         []provider.AlbumInfo
	artists        []provider.ArtistInfo
	loadingLists   bool
	loadingAlbums  bool
	loadingArtists bool

	// — center pane —
	view    immersiveView
	ctxID   string // playlist/album/artist id the open view belongs to
	ctxName string
	ctxSub  string // hero subtitle line: "Public Playlist", artist name, …
	ctxKind roloItemKind

	tracks        []playlist.Track
	tracksLoading bool
	trackCursor   int
	trackScroll   int
	trackSort     immersiveTrackSort
	gridCursor    int
	back          []immNavSnap

	// — rolodex —
	roloMode    bool          // true = the rolodex owns the whole center pane
	roloCursor  int           // index into roloItems()
	roloSpin    int           // pending animated steps (magnitude)
	roloSpinDir int           // +1 right, -1 left
	roloSpinFor time.Duration // elapsed since the last consumed step

	// — right rail —
	rightTab      immersiveRightTab
	rightCursor   int
	rightScroll   int
	artistMeta    provider.ArtistDetail
	artistMetaFor string // artist name the cached detail belongs to
	artistLoading bool

	// — top-bar search —
	searching     bool
	searchQuery   string
	searchResults []playlist.Track
	searchLoading bool
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

// immersiveContentMsg carries the track list for an opened playlist or album.
type immersiveContentMsg struct {
	kind         roloItemKind // playlist or album
	id           string
	name         string
	sub          string
	tracks       []playlist.Track
	providerName string
	gen          uint64
	err          error
}

// immersiveArtistMsg carries an artist profile for the artist view / panel.
type immersiveArtistMsg struct {
	detail       provider.ArtistDetail
	forPanel     bool // true = fetched for the now-playing rail, not the view
	providerName string
	gen          uint64
	err          error
}

// immersiveSearchMsg carries global-search results into the results table.
type immersiveSearchMsg struct {
	tracks       []playlist.Track
	providerName string
	gen          uint64
	err          error
}

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

func fetchImmersivePlaylistCmd(prov playlist.Provider, id, name, sub string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		tracks, err := prov.Tracks(id)
		return immersiveContentMsg{kind: roloKindPlaylist, id: id, name: name, sub: sub, tracks: tracks, providerName: prov.Name(), gen: gen, err: err}
	}
}

func fetchImmersiveAlbumCmd(l provider.AlbumTrackLoader, providerName, id, name, sub string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		tracks, err := l.AlbumTracks(id)
		return immersiveContentMsg{kind: roloKindAlbum, id: id, name: name, sub: sub, tracks: tracks, providerName: providerName, gen: gen, err: err}
	}
}

func fetchImmersiveArtistCmd(l provider.ArtistDetailLoader, providerName, id string, forPanel bool, gen uint64) tea.Cmd {
	return func() tea.Msg {
		detail, err := l.ArtistDetail(id)
		return immersiveArtistMsg{detail: detail, forPanel: forPanel, providerName: providerName, gen: gen, err: err}
	}
}

func fetchImmersiveSearchCmd(ctx context.Context, s provider.Searcher, providerName, query string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		tracks, err := s.SearchTracks(ctx, query, 50)
		return immersiveSearchMsg{tracks: tracks, providerName: providerName, gen: gen, err: err}
	}
}

// — open / close —

// enterImmersive switches to the immersive frame and kicks the library-rail
// fetches. The provider's existing playlist cache seeds the rail when it is
// already settled for this provider.
func (m *Model) enterImmersive() tea.Cmd {
	m.immersive = immersiveState{
		active:      true,
		prov:        m.provider,
		focus:       immPaneCenter,
		zone:        zoneRolo,
		view:        immViewHome,
		railSort:    immRailSortRecents,
		railSection: immSectionPlaylists,
	}
	if m.provider == nil {
		return nil
	}
	if _, ok := m.provider.(provider.AlbumBrowser); ok {
		m.immersive.loadingAlbums = true
	}
	if _, ok := m.provider.(provider.ArtistBrowser); ok {
		m.immersive.loadingArtists = true
	}
	if len(m.providerLists) > 0 {
		// Reuse the already-fetched list; browse-route pseudo rows are
		// filtered out of the rail.
		m.immersive.lists = m.filterImmersivePlaylists(m.providerLists)
	} else {
		m.immersive.loadingLists = true
	}
	return m.fetchImmersiveSidebar()
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
}

// fetchImmersiveSidebar dispatches the outstanding rail fetches, one
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

// isCurrentImmersiveRequest reports whether a completion still belongs to the
// mode's current provider session.
func (m Model) isCurrentImmersiveRequest(gen uint64, providerName string, cur uint64) bool {
	return m.immersive.active &&
		m.immersive.prov != nil &&
		m.immersive.prov.Name() == providerName &&
		gen == cur
}

// filterImmersivePlaylists drops UI-only browse-route pseudo entries; real
// provider rows — including synthetic library rows like "Liked Songs" — stay.
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

// — rail rows —

// immRailRow is one selectable row in the library rail.
type immRailRow struct {
	kind roloItemKind
	id   string
	name string
	sub  string
}

// railRows returns the rows for the active section with filter and sort
// applied. Fetched data is never mutated.
func (m Model) railRows() []immRailRow {
	var rows []immRailRow
	switch m.immersive.railSection {
	case immSectionPlaylists:
		for _, l := range m.immersive.lists {
			rows = append(rows, immRailRow{
				kind: roloKindPlaylist, id: l.ID, name: l.Name,
				sub: playlistRowSub(l),
			})
		}
	case immSectionAlbums:
		for _, a := range m.immersive.albums {
			rows = append(rows, immRailRow{
				kind: roloKindAlbum, id: a.ID, name: a.Name,
				sub: firstNonEmpty(a.Artist, "Album"),
			})
		}
	case immSectionArtists:
		for _, a := range m.immersive.artists {
			rows = append(rows, immRailRow{
				kind: roloKindArtist, id: a.ID, name: a.Name,
				sub: "Artist",
			})
		}
	}
	if m.immersive.railFilter != "" {
		q := strings.ToLower(m.immersive.railFilter)
		kept := rows[:0]
		for _, r := range rows {
			if strings.Contains(strings.ToLower(r.name), q) ||
				strings.Contains(strings.ToLower(r.sub), q) {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	if m.immersive.railSort == immRailSortAlpha {
		sort.SliceStable(rows, func(i, j int) bool {
			return strings.ToLower(rows[i].name) < strings.ToLower(rows[j].name)
		})
	}
	return rows
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

// — rolodex —

// roloItems builds the rolodex deck from the current context. On the home view
// it is the rail's section items; inside a track view with roloMode it is the
// tracks themselves (the song picker); in the plain track view it is hidden.
func (m Model) roloItems() []roloItem {
	switch {
	case m.immersive.roloMode && (m.immersive.view == immViewPlaylist ||
		m.immersive.view == immViewAlbum || m.immersive.view == immViewSearch ||
		m.immersive.view == immViewArtist):
		// The deck follows the table's active sort: item ids are indexes into
		// sortedTracks(), so activation and the table cursor stay aligned.
		tracks := m.sortedTracks()
		items := make([]roloItem, 0, len(tracks))
		for i, t := range tracks {
			title := t.Title
			if title == "" {
				title = trackViewName(t)
			}
			items = append(items, roloItem{
				kind:  roloKindTrack,
				id:    strconv.Itoa(i),
				title: title,
				sub:   firstNonEmpty(t.Artist, t.Album),
			})
		}
		return items
	default:
		var items []roloItem
		for _, r := range m.railRows() {
			items = append(items, roloItem{kind: r.kind, id: r.id, title: r.name, sub: r.sub})
		}
		return items
	}
}

// roloSpinStep consumes one pending spin step, moving the cursor by dir.
// Returns false when the deck is empty or no steps remain.
func (m *Model) roloSpinStep() bool {
	if m.immersive.roloSpin == 0 {
		return false
	}
	n := len(m.roloItems())
	if n == 0 {
		m.immersive.roloSpin = 0
		return false
	}
	m.immersive.roloCursor = wrapIndex(m.immersive.roloCursor+m.immersive.roloSpinDir, n)
	m.immersive.roloSpin--
	if m.immersive.roloSpin == 0 {
		m.immersive.roloSpinFor = 0
	}
	return true
}

// roloSpinKick adds momentum steps to the wheel; repeated presses make the
// deck roll faster, a reverse press cancels then flips the spin.
func (m *Model) roloSpinKick(dir int) {
	if len(m.roloItems()) == 0 {
		return
	}
	if m.immersive.roloSpin > 0 && m.immersive.roloSpinDir != dir {
		m.immersive.roloSpin = 0
		m.immersive.roloSpinFor = 0
	}
	m.immersive.roloSpinDir = dir
	m.immersive.roloSpin += 2
	const cap = 12
	if m.immersive.roloSpin > cap {
		m.immersive.roloSpin = cap
	}
	m.roloSpinStep() // first step lands immediately so a single tap never lags
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

// tickImmersive advances the rolodex spin at the fast tick quantum.
func (m *Model) tickImmersive(dt time.Duration) {
	if !m.immersive.active || m.immersive.roloSpin == 0 {
		return
	}
	m.immersive.roloSpinFor += dt
	for m.immersive.roloSpinFor >= ui.TickFast && m.immersive.roloSpin > 0 {
		m.immersive.roloSpinFor -= ui.TickFast
		m.roloSpinStep()
	}
}

// — center navigation —

// openImmersiveRow opens a rail/rolodex row into the matching center view.
func (m *Model) openImmersiveRow(row immRailRow) tea.Cmd {
	prov := m.immersive.prov
	if prov == nil {
		return nil
	}
	m.pushImmersiveBack()
	providerName := prov.Name()
	gen := nextRequest(&m.requests.immersiveContent)
	m.immersive.tracks = nil
	m.immersive.tracksLoading = true
	m.immersive.trackCursor = 0
	m.immersive.trackScroll = 0
	m.immersive.trackSort = immSortTrackOrder
	m.immersive.zone = zoneTable
	m.immersive.roloMode = false

	switch row.kind {
	case roloKindPlaylist:
		m.immersive.view = immViewPlaylist
		m.immersive.ctxID, m.immersive.ctxName, m.immersive.ctxSub = row.id, row.name, row.sub
		m.immersive.ctxKind = roloKindPlaylist
		return fetchImmersivePlaylistCmd(prov, row.id, row.name, row.sub, gen)
	case roloKindAlbum:
		m.immersive.view = immViewAlbum
		m.immersive.ctxID, m.immersive.ctxName, m.immersive.ctxSub = row.id, row.name, row.sub
		m.immersive.ctxKind = roloKindAlbum
		if l, ok := prov.(provider.AlbumTrackLoader); ok {
			return fetchImmersiveAlbumCmd(l, providerName, row.id, row.name, row.sub, gen)
		}
	case roloKindArtist:
		m.immersive.view = immViewArtist
		m.immersive.ctxID, m.immersive.ctxName, m.immersive.ctxSub = row.id, row.name, "Artist"
		m.immersive.ctxKind = roloKindArtist
		if l, ok := prov.(provider.ArtistDetailLoader); ok {
			m.immersive.artistLoading = true
			return fetchImmersiveArtistCmd(l, providerName, row.id, false, nextRequest(&m.requests.immersiveArtist))
		}
	}
	m.immersive.tracksLoading = false
	return nil
}

// pushImmersiveBack snapshots the current center context onto the back stack.
func (m *Model) pushImmersiveBack() {
	// Home and the transient search view are the root: nothing to return to.
	if m.immersive.view == immViewHome {
		return
	}
	m.immersive.back = append(m.immersive.back, immNavSnap{
		view:        m.immersive.view,
		ctxID:       m.immersive.ctxID,
		ctxName:     m.immersive.ctxName,
		ctxSub:      m.immersive.ctxSub,
		ctxKind:     m.immersive.ctxKind,
		tracks:      m.immersive.tracks,
		trackCursor: m.immersive.trackCursor,
		trackScroll: m.immersive.trackScroll,
		gridCursor:  m.immersive.gridCursor,
		roloCursor:  m.immersive.roloCursor,
	})
}

// immersiveGoBack restores the last center context, or lands on home.
func (m *Model) immersiveGoBack() {
	n := len(m.immersive.back)
	if n == 0 {
		m.immersive.view = immViewHome
		m.immersive.zone = zoneRolo
		m.immersive.roloMode = false
		m.immersive.ctxID, m.immersive.ctxName, m.immersive.ctxSub = "", "", ""
		m.immersive.tracks = nil
		m.immersive.tracksLoading = false
		return
	}
	snap := m.immersive.back[n-1]
	m.immersive.back = m.immersive.back[:n-1]
	m.immersive.view = snap.view
	m.immersive.ctxID, m.immersive.ctxName, m.immersive.ctxSub = snap.ctxID, snap.ctxName, snap.ctxSub
	m.immersive.ctxKind = snap.ctxKind
	m.immersive.tracks = snap.tracks
	m.immersive.tracksLoading = false
	m.immersive.trackCursor = snap.trackCursor
	m.immersive.trackScroll = snap.trackScroll
	m.immersive.gridCursor = snap.gridCursor
	m.immersive.roloCursor = snap.roloCursor
	m.immersive.zone = zoneTable
	m.immersive.roloMode = false
}

// sortedTracks returns the open context's tracks in the active table order.
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
// queue rail can name what is playing next.
func (m *Model) playImmersiveContext(startIdx int) tea.Cmd {
	tracks := m.sortedTracks()
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

// playImmersiveRow plays a rolo/rail row directly: for tracks it plays the
// song in the open context; for collections it is handled by openImmersiveRow
// instead (Enter drills, `p` plays the context).
func (m *Model) playImmersiveTrack(index int) tea.Cmd {
	tracks := m.sortedTracks()
	if index < 0 || index >= len(tracks) {
		return nil
	}
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

// maybeFetchNowPlayingArtist lazily loads the artist profile shown in the
// right rail's About-the-artist block.
func (m *Model) maybeFetchNowPlayingArtist() tea.Cmd {
	track, _ := m.currentPlaybackTrack()
	if track.Artist == "" {
		return nil
	}
	if m.immersive.artistMetaFor == track.Artist || m.immersive.artistLoading {
		return nil
	}
	resolver, ok := m.immersive.prov.(provider.TrackArtistResolver)
	if !ok {
		return nil
	}
	info, ok := resolver.ArtistForTrack(track)
	if !ok || info.ID == "" {
		return nil
	}
	loader, ok := m.immersive.prov.(provider.ArtistDetailLoader)
	if !ok {
		return nil
	}
	m.immersive.artistLoading = true
	m.immersive.artistMetaFor = track.Artist
	return fetchImmersiveArtistCmd(loader, m.immersive.prov.Name(), info.ID, true, nextRequest(&m.requests.immersiveArtist))
}
