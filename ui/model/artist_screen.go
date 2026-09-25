package model

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// artist_screen.go implements the inline artist profile overlay opened from
// the search results artist tab and the nav browser artist list (and later the
// Home view). It renders Popular / Liked Songs / Discography sections and
// drills into Discography rows through its own album stack, which mirrors the
// search drill machinery (spotDrillLevel crumbs, Esc pops one level).

// artistRequestTimeout bounds one ArtistDetail or album drill fetch.
const artistRequestTimeout = 30 * time.Second

// openArtistScreen opens the artist profile overlay for one artist and starts
// the async ArtistDetail fetch. A second open supersedes the first: cancelling
// any in-flight request first (the state reset below would otherwise orphan
// its cancel), then the generation bump makes the earlier completion stale.
// The surface it is opened from stays mounted underneath, so popping the
// screen returns to it with its cursor intact.
func (m *Model) openArtistScreen(providerName string, artist provider.ArtistInfo) tea.Cmd {
	prov := m.providerNamed(providerName)
	l, ok := prov.(provider.ArtistDetailLoader)
	if !ok {
		m.status.Show("Artist profiles are not supported", statusTTLDefault)
		return nil
	}
	m.cancelArtistRequest()
	m.artist = artistScreenState{
		prov:    prov,
		visible: true,
		info:    artist,
		loading: true,
	}
	m.applyHeightMode()
	gen := nextRequest(&m.requests.artist)
	// The stored cancel bounds ctx users only: the frozen ArtistDetail and
	// AlbumTracks contracts take no context, so the provider's own timeout —
	// not this cancel — aborts its work.
	return fetchArtistDetailCmd(m.newArtistRequestContext(artistRequestTimeout), l, providerName, artist.ID, gen)
}

// closeArtistScreen pops the artist overlay (and any Discography drill),
// cancelling in-flight requests and dropping the loading state.
func (m *Model) closeArtistScreen() {
	m.cancelArtistRequest()
	nextRequest(&m.requests.artist)
	m.artist = artistScreenState{}
	m.applyHeightMode()
	m.adjustScroll()
}

func (m *Model) newArtistRequestContext(timeout time.Duration) context.Context {
	m.cancelArtistRequest()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	m.artist.cancel = cancel
	return ctx
}

func (m *Model) cancelArtistRequest() {
	if m.artist.cancel != nil {
		m.artist.cancel()
		m.artist.cancel = nil
	}
}

func (m Model) isCurrentArtistRequest(gen uint64, providerName string) bool {
	return m.artist.visible &&
		m.artist.prov != nil &&
		m.artist.prov.Name() == providerName &&
		gen == m.requests.artist
}

// providerNamed resolves the configured provider with the given display name,
// preferring the active provider.
func (m Model) providerNamed(name string) playlist.Provider {
	if m.provider != nil && m.provider.Name() == name {
		return m.provider
	}
	for _, pe := range m.providers {
		if pe.Provider != nil && pe.Provider.Name() == name {
			return pe.Provider
		}
	}
	return nil
}

// — sectioned rows —

// artistRowKind identifies which artist-screen section a row belongs to.
type artistRowKind int

const (
	artistRowPopular artistRowKind = iota
	artistRowLiked
	artistRowDiscography
)

// artistRow is one selectable row of the sectioned artist list.
type artistRow struct {
	kind  artistRowKind      // section the row belongs to
	pos   int                // index of the row within its section
	track playlist.Track     // Popular / Liked rows
	album provider.AlbumInfo // Discography rows
}

// trackPopularity parses the popularity meta of a popular-pool row; missing or
// malformed values count as zero popularity.
func trackPopularity(t playlist.Track) int {
	n, _ := strconv.Atoi(strings.TrimSpace(t.Meta(provider.MetaSpotifyPopularity)))
	return n
}

// trackLikedByProvider reports the provider's liked mark on a track.
func trackLikedByProvider(t playlist.Track) bool {
	return t.Meta(provider.MetaSpotifyLiked) == "true"
}

// artistPopular returns the popular pool in the active sort order. The pool is
// copied so cycling the sort never mutates the loaded detail.
func (m Model) artistPopular() []playlist.Track {
	sorted := append([]playlist.Track(nil), m.artist.detail.Popular...)
	switch m.artist.sort {
	case artistSortRecency:
		sort.SliceStable(sorted, func(i, j int) bool {
			if sorted[i].Year != sorted[j].Year {
				return sorted[i].Year > sorted[j].Year
			}
			return trackPopularity(sorted[i]) > trackPopularity(sorted[j])
		})
	case artistSortLikedFirst:
		sort.SliceStable(sorted, func(i, j int) bool {
			li, lj := trackLikedByProvider(sorted[i]), trackLikedByProvider(sorted[j])
			if li != lj {
				return li
			}
			return trackPopularity(sorted[i]) > trackPopularity(sorted[j])
		})
	default: // artistSortPopularity
		sort.SliceStable(sorted, func(i, j int) bool {
			return trackPopularity(sorted[i]) > trackPopularity(sorted[j])
		})
	}
	return sorted
}

// artistLiked returns the liked popular-pool rows, in the active sort order.
func (m Model) artistLiked() []playlist.Track {
	popular := m.artistPopular()
	liked := make([]playlist.Track, 0, len(popular))
	for _, t := range popular {
		if trackLikedByProvider(t) {
			liked = append(liked, t)
		}
	}
	return liked
}

// artistRows flattens the sectioned list into selectable rows. Empty sections
// are omitted (their header would render alone).
func (m Model) artistRows() []artistRow {
	if !m.artist.visible || m.artist.loading {
		return nil
	}
	var rows []artistRow
	for i, t := range m.artistPopular() {
		rows = append(rows, artistRow{kind: artistRowPopular, pos: i, track: t})
	}
	for i, t := range m.artistLiked() {
		rows = append(rows, artistRow{kind: artistRowLiked, pos: i, track: t})
	}
	for i, a := range m.artist.detail.Discography {
		rows = append(rows, artistRow{kind: artistRowDiscography, pos: i, album: a})
	}
	return rows
}

// artistRowAt returns the selectable row under the artist screen cursor.
func (m Model) artistRowAt() (artistRow, bool) {
	rows := m.artistRows()
	if m.artist.loading || m.artist.cursor < 0 || m.artist.cursor >= len(rows) {
		return artistRow{}, false
	}
	return rows[m.artist.cursor], true
}

// artistTrackLikerAvailable reports whether the track under the artist screen
// cursor has a provider that supports liking it.
func (m Model) artistTrackLikerAvailable() bool {
	row, ok := m.artistRowAt()
	return ok && row.kind != artistRowDiscography && m.likerForTrack(row.track) != nil
}

// artistPlayFrom plays the track at pos and enqueues the rest of the given
// list after it, mirroring the nav browser's play-row-and-queue-remainder
// pattern (capped at 500 tracks).
func (m *Model) artistPlayFrom(tracks []playlist.Track, pos int) tea.Cmd {
	if pos < 0 || pos >= len(tracks) {
		return nil
	}
	const maxAdd = 500
	m.player.Stop()
	m.player.ClearPreload()
	toAdd := tracks[pos:]
	if len(toAdd) > maxAdd {
		toAdd = toAdd[:maxAdd]
	}
	m.playlist.Add(toAdd...)
	m.loadedPlaylist = ""
	m.addToHeaderState(toAdd)
	newIdx := m.playlist.Len() - len(toAdd)
	m.playlist.SetIndex(newIdx)
	m.plCursor = newIdx
	m.adjustScroll()
	if len(toAdd) > 1 {
		m.status.Showf(statusTTLMedium, "Playing: %s (+%d queued)", toAdd[0].DisplayName(), len(toAdd)-1)
	} else {
		m.status.Showf(statusTTLMedium, "Playing: %s", toAdd[0].DisplayName())
	}
	cmd := m.playCurrentTrack()
	m.notifyPlayback()
	return cmd
}

// artistToggleFollow follows/unfollows the open artist. The provider
// interfaces expose no follow-state query, so the intended state comes from
// the session-local followState map, exactly like the search and nav `f` keys:
// the first toggle of a session assumes the artist is unfollowed.
func (m *Model) artistToggleFollow() tea.Cmd {
	f, ok := m.artist.prov.(provider.ArtistFollower)
	if !ok {
		return nil
	}
	provName := m.artist.prov.Name()
	follow := !m.followState[followKey("artist", provName, m.artist.info.ID)]
	return followArtistCmd(m.newLikeContext(), f, provName, m.artist.info.ID, m.artist.info.Name, follow, nextRequest(&m.requests.follow))
}

// — scrolling —

// artistRenderedRows counts rendered lines (rows plus section headers)
// between scroll and cursor inclusive, mirroring albumSeparatorRows.
func artistRenderedRows(rows []artistRow, scroll, cursor int) int {
	if scroll < 0 || cursor < scroll || cursor >= len(rows) {
		return 0
	}
	prev := artistRowKind(-1)
	if scroll > 0 {
		prev = rows[scroll-1].kind
	}
	count := 0
	for i := scroll; i <= cursor; i++ {
		if rows[i].kind != prev {
			count++ // section header line
		}
		count++
		prev = rows[i].kind
	}
	return count
}

// artistMaybeAdjustScroll keeps the cursor visible in the sectioned list,
// accounting for the section headers that share its row budget.
func (m *Model) artistMaybeAdjustScroll() {
	rows := m.artistRows()
	if len(rows) == 0 {
		m.artist.cursor = 0
		m.artist.scroll = 0
		return
	}
	m.artist.cursor = min(max(m.artist.cursor, 0), len(rows)-1)
	visible := m.artistListVisible()
	scroll := min(max(m.artist.scroll, 0), len(rows)-1)
	if m.artist.cursor < scroll {
		scroll = m.artist.cursor
	}
	for scroll < m.artist.cursor && artistRenderedRows(rows, scroll, m.artist.cursor) > visible {
		scroll++
	}
	m.artist.scroll = scroll
}

func (m *Model) artistDrillMaybeAdjustScroll(lvl *spotDrillLevel) {
	clampScroll(&lvl.cursor, &lvl.scroll, m.spotDrillCount(*lvl), max(1, m.effectivePlaylistVisible()-1))
}

// — key handling —

// handleArtistKey handles keys on the artist profile screen. Track rows carry
// the same row actions as the search drill lists; Discography rows drill into
// album tracks; Esc pops the screen back to whatever opened it.
func (m *Model) handleArtistKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.closeArtistScreen()
		return m.quit()
	}
	if len(m.artist.drill) > 0 {
		return m.handleArtistDrillKey(msg)
	}

	count := len(m.artistRows())
	move := func(delta int) {
		if delta < 0 && m.artist.cursor > 0 {
			m.artist.cursor--
		} else if delta < 0 && count > 0 {
			m.artist.cursor = count - 1
		} else if delta > 0 && m.artist.cursor < count-1 {
			m.artist.cursor++
		} else if delta > 0 && count > 0 {
			m.artist.cursor = 0
		}
		m.artistMaybeAdjustScroll()
	}

	switch msg.String() {
	case "ctrl+x":
		m.toggleExpandedView()
		m.artistMaybeAdjustScroll()
	case "up", "k", "ctrl+p":
		move(-1)
	case "down", "j", "ctrl+n":
		move(1)
	case "ctrl+u":
		step := m.artistListVisible()
		if m.artist.cursor >= step {
			m.artist.cursor -= step
		} else {
			m.artist.cursor = 0
		}
		m.artistMaybeAdjustScroll()
	case "ctrl+d":
		step := m.artistListVisible()
		m.artist.cursor += step
		if m.artist.cursor >= count {
			m.artist.cursor = max(0, count-1)
		}
		m.artistMaybeAdjustScroll()
	case "g", "home":
		m.artist.cursor = 0
		m.artistMaybeAdjustScroll()
	case "G", "end":
		if count > 0 {
			m.artist.cursor = count - 1
		}
		m.artistMaybeAdjustScroll()
	case "enter", "l":
		row, ok := m.artistRowAt()
		if !ok {
			return nil
		}
		if row.kind == artistRowDiscography {
			l, ok := m.artist.prov.(provider.AlbumTrackLoader)
			if !ok {
				m.status.Show("Album drill-down not supported", statusTTLDefault)
				return nil
			}
			crumb := "Album — " + row.album.Name
			m.artistPushDrill(crumb)
			gen := nextRequest(&m.requests.artist)
			return fetchArtistAlbumTracksCmd(m.newArtistRequestContext(artistRequestTimeout), l, m.artist.prov.Name(), m.artist.info.ID, row.album.ID, crumb, gen)
		}
		return m.artistPlayFrom(m.artistSectionTracks(row.kind), row.pos)
	case "s":
		if m.artist.loading || len(m.artist.detail.Popular) == 0 {
			return nil
		}
		m.artist.sort = (m.artist.sort + 1) % artistSortCount
		m.artist.cursor = 0
		m.artist.scroll = 0
		m.artistMaybeAdjustScroll()
		m.status.Showf(statusTTLDefault, "Popular sorted by %s", artistSortLabels[m.artist.sort])
	case "f":
		return m.artistToggleFollow()
	case "a":
		if row, ok := m.artistRowAt(); ok && row.kind != artistRowDiscography {
			return m.appendTrack(row.track)
		}
	case "q":
		if row, ok := m.artistRowAt(); ok && row.kind != artistRowDiscography {
			return m.queueTrackNext(row.track)
		}
	case "*", "S":
		if row, ok := m.artistRowAt(); ok && row.kind != artistRowDiscography {
			return m.likeTrack(row.track)
		}
	case "p":
		if row, ok := m.artistRowAt(); ok && row.kind != artistRowDiscography {
			return m.openPlaylistPicker([]playlist.Track{row.track}, "Track: "+row.track.DisplayName())
		}
	case "esc", "backspace", "h", "left":
		m.closeArtistScreen()
	}
	return nil
}

// artistSectionTracks returns the full section a row kind belongs to, used to
// enqueue the remainder after the played row.
func (m Model) artistSectionTracks(kind artistRowKind) []playlist.Track {
	if kind == artistRowLiked {
		return m.artistLiked()
	}
	return m.artistPopular()
}

// artistPushDrill appends a loading album level to the artist screen's drill
// stack, mirroring spotPushDrill.
func (m *Model) artistPushDrill(crumb string) {
	m.artist.drill = append(m.artist.drill, spotDrillLevel{crumb: crumb, loading: true})
}

// handleArtistDrillKey handles keys inside an album drilled into from the
// Discography section. Track rows carry the same actions as the artist
// screen's Popular rows; Esc pops one drill level, returning to the artist
// sections when the stack empties.
func (m *Model) handleArtistDrillKey(msg tea.KeyPressMsg) tea.Cmd {
	lvl := &m.artist.drill[len(m.artist.drill)-1]
	count := m.spotDrillCount(*lvl)

	move := func(delta int) {
		if delta < 0 && lvl.cursor > 0 {
			lvl.cursor--
		} else if delta < 0 && count > 0 {
			lvl.cursor = count - 1
		} else if delta > 0 && lvl.cursor < count-1 {
			lvl.cursor++
		} else if delta > 0 && count > 0 {
			lvl.cursor = 0
		}
		m.artistDrillMaybeAdjustScroll(lvl)
	}

	switch msg.String() {
	case "ctrl+x":
		m.toggleExpandedView()
		m.artistDrillMaybeAdjustScroll(lvl)
	case "up", "k", "ctrl+p":
		move(-1)
	case "down", "j", "ctrl+n":
		move(1)
	case "enter", "l":
		if count == 0 || lvl.loading {
			return nil
		}
		return m.artistPlayFrom(lvl.tracks, lvl.cursor)
	case "a":
		if count > 0 && !lvl.loading {
			return m.appendTrack(lvl.tracks[lvl.cursor])
		}
	case "q":
		if count > 0 && !lvl.loading {
			return m.queueTrackNext(lvl.tracks[lvl.cursor])
		}
	case "*", "S":
		if count > 0 && !lvl.loading {
			return m.likeTrack(lvl.tracks[lvl.cursor])
		}
	case "p":
		if count > 0 && !lvl.loading {
			track := lvl.tracks[lvl.cursor]
			return m.openPlaylistPicker([]playlist.Track{track}, "Track: "+track.DisplayName())
		}
	case "esc", "backspace", "h", "left":
		// Pop one level; when the stack empties the artist sections reappear
		// with the cursor preserved.
		m.artist.drill = m.artist.drill[:len(m.artist.drill)-1]
	case "ctrl+u":
		step := max(1, m.effectivePlaylistVisible()-1)
		if lvl.cursor >= step {
			lvl.cursor -= step
		} else {
			lvl.cursor = 0
		}
		m.artistDrillMaybeAdjustScroll(lvl)
	case "ctrl+d":
		step := max(1, m.effectivePlaylistVisible()-1)
		lvl.cursor += step
		if lvl.cursor >= count {
			lvl.cursor = max(0, count-1)
		}
		m.artistDrillMaybeAdjustScroll(lvl)
	}
	return nil
}
