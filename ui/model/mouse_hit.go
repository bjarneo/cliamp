package model

// Row-to-track resolution for pointer input. Each resolver replays the
// exact row math of its own renderer — same iterator, same header
// suppression, same budgets — so a click maps to the row the user sees.
// The returned menuRemove says which surface's removal path applies to the
// hit track, and index is that surface's cursor-domain position.

import (
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// menuRemoveKind selects how the context menu's "Remove" item applies.
type menuRemoveKind int

const (
	menuRemoveNone     menuRemoveKind = iota // hide the item
	menuRemovePlaylist                       // playlist index via plCursor
	menuRemoveQueue                          // queue position via RemoveQueueAt
	menuRemovePlMgr                          // playlist-manager view index
)

// trackHit describes the track under a body-region row.
type trackHit struct {
	track     playlist.Track
	cursor    int // position in the surface's own cursor domain; -1 = no track on this row
	remove    menuRemoveKind
	removeIdx int // playlist index / queue position / manager view index for remove
}

// hitTrack resolves the track under body-region row n for the active
// screen. col is the column within the track-list region, needed only by
// the two-pane home overlay to tell sidebar rows from content rows.
func (m Model) hitTrack(row, col int) trackHit {
	if m.playlist == nil {
		return trackHit{cursor: -1}
	}
	switch m.activeScreen() {
	case screenMain:
		return m.hitPlaylistRow(row)
	case screenQueue:
		return m.hitQueueRow(row)
	case screenSearch:
		return m.hitSearchRow(row)
	case screenNetSearch:
		return m.hitNetSearchRow(row)
	case screenSpotSearch:
		return m.hitSpotRow(row)
	case screenNavBrowser:
		return m.hitNavRow(row)
	case screenArtist:
		return m.hitArtistRow(row)
	case screenHome:
		return m.hitHomeRow(row, col)
	case screenPlaylistManager:
		return m.hitPlMgrRow(row)
	}
	return trackHit{cursor: -1}
}

// selectTrackRow moves the active surface's cursor to the row the user
// clicked, so a right-click's menu acts on that row and a left click
// selects it the way arrows would.
func (m *Model) selectTrackRow(row, col int) {
	hit := m.hitTrack(row, col)
	if hit.cursor < 0 {
		return
	}
	switch m.activeScreen() {
	case screenMain:
		m.plCursor = hit.cursor
		m.focus = focusPlaylist
	case screenQueue:
		m.queue.cursor = hit.cursor
	case screenSearch:
		m.search.cursor = hit.cursor
	case screenNetSearch:
		m.netSearch.cursor = hit.cursor
	case screenSpotSearch:
		if len(m.spotSearch.drill) > 0 {
			m.spotSearch.drill[len(m.spotSearch.drill)-1].cursor = hit.cursor
		} else {
			m.spotSearch.cursor = hit.cursor
		}
	case screenNavBrowser:
		m.navBrowser.cursor = hit.cursor
	case screenArtist:
		if len(m.artist.drill) > 0 {
			m.artist.drill[len(m.artist.drill)-1].cursor = hit.cursor
		} else {
			m.artist.cursor = hit.cursor
		}
	case screenHome:
		m.home.content.cursor = hit.cursor
	case screenPlaylistManager:
		m.plManager.cursor = hit.cursor
	}
}

// hitPlaylistRow replays renderPlaylist: TrackWindow + playlistRows with
// the same scroll/header math, returning the global playlist index.
func (m Model) hitPlaylistRow(row int) trackHit {
	if m.focus == focusProvider || m.playlist.Len() == 0 {
		return trackHit{cursor: -1}
	}
	budget := m.effectivePlaylistVisible()
	if budget <= 0 {
		return trackHit{cursor: -1}
	}
	scroll := m.playlistScroll(budget)
	windowStart := max(0, scroll-1)
	tracks := m.playlist.TrackWindow(windowStart, budget+1)
	localScroll := scroll - windowStart
	i := 0
	for r := range m.playlistRows(tracks, localScroll, m.showAlbumHeaders) {
		if r.Index < 0 {
			if i+1 >= budget {
				break
			}
			if i == row {
				return trackHit{cursor: -1} // album separator
			}
			i++
			continue
		}
		if i >= budget {
			break
		}
		if i == row {
			idx := windowStart + r.Index
			return trackHit{track: r.Track, cursor: idx, remove: menuRemovePlaylist, removeIdx: idx}
		}
		i++
	}
	return trackHit{cursor: -1}
}

// hitQueueRow replays renderQueueBody, returning the queue position.
func (m Model) hitQueueRow(row int) trackHit {
	total := m.playlist.QueueLen()
	budget := m.effectivePlaylistVisible()
	if total == 0 || budget <= 0 {
		return trackHit{cursor: -1}
	}
	scroll := clampedScroll(m.queue.scroll, m.queue.cursor, total, budget)
	windowStart := max(0, scroll-1)
	tracks := m.playlist.QueueWindow(windowStart, 2*budget+2)
	localScroll, localCursor := scroll-windowStart, m.queue.cursor-windowStart
	for localScroll < localCursor && m.albumSeparatorRows(tracks, localScroll, localCursor, m.showAlbumHeaders) > budget {
		localScroll++
	}
	i := 0
	for r := range m.playlistRows(tracks, localScroll, m.showAlbumHeaders) {
		if i >= budget {
			break
		}
		if r.Index < 0 {
			if i+1 < budget {
				if i == row {
					return trackHit{cursor: -1}
				}
				i++
			}
			continue
		}
		if i == row {
			pos := windowStart + r.Index
			return trackHit{track: r.Track, cursor: pos, remove: menuRemoveQueue, removeIdx: pos}
		}
		i++
	}
	return trackHit{cursor: -1}
}

// hitSearchRow replays renderSearchList: a flat filtered list starting at
// search.scroll. Row n maps to playlist index results[scroll+n].
func (m Model) hitSearchRow(row int) trackHit {
	i := m.search.scroll + row
	if i >= len(m.search.results) {
		return trackHit{cursor: -1}
	}
	idx := m.search.results[i]
	t, ok := m.playlist.Track(idx)
	if !ok {
		return trackHit{cursor: -1}
	}
	return trackHit{track: t, cursor: i, remove: menuRemovePlaylist, removeIdx: idx}
}

// hitNetSearchRow replays windowList over netSearch.results.
func (m Model) hitNetSearchRow(row int) trackHit {
	if m.netSearch.screen != netSearchResults {
		return trackHit{cursor: -1}
	}
	i := m.netSearch.scroll + row
	if i >= len(m.netSearch.results) {
		return trackHit{cursor: -1}
	}
	return trackHit{track: m.netSearch.results[i], cursor: i}
}

// hitSpotRow replays the provider-search result renderers: the sectioned
// flat list, the tab bar + tab list, and the drill crumb + drill list. Only
// track rows resolve — album/playlist/artist rows are not tracks.
func (m Model) hitSpotRow(row int) trackHit {
	s := &m.spotSearch
	if s.screen != spotSearchResults {
		return trackHit{cursor: -1}
	}
	if s.multi {
		if len(s.drill) > 0 {
			lvl := s.drill[len(s.drill)-1]
			if row == 0 || lvl.albums != nil || lvl.loading {
				return trackHit{cursor: -1} // crumb, or an album drill
			}
			i := lvl.scroll + row - 1
			if i >= len(lvl.tracks) {
				return trackHit{cursor: -1}
			}
			return trackHit{track: lvl.tracks[i], cursor: i}
		}
		if row == 0 || s.tab != spotTabTracks || s.loading {
			return trackHit{cursor: -1} // tab bar, or a non-track tab
		}
		i := s.scroll + row - 1
		if i >= len(s.resultsAll.Tracks) {
			return trackHit{cursor: -1}
		}
		return trackHit{track: s.resultsAll.Tracks[i], cursor: i}
	}
	budget := m.spotSearchResultsVisible()
	i := 0
	for r := range spotSearchRows(s.results, s.scroll) {
		if i >= budget {
			break
		}
		if r.Index < 0 {
			if budget == 1 {
				continue
			}
			if i == row {
				return trackHit{cursor: -1} // section separator
			}
			i++
			continue
		}
		if i == row {
			if r.Track.IsAlbum() {
				return trackHit{cursor: -1}
			}
			return trackHit{track: r.Track, cursor: r.Index}
		}
		i++
	}
	return trackHit{cursor: -1}
}

// hitNavRow replays renderNavTrackBody for the provider browser. Other
// routes list artists/albums/playlists — not tracks.
func (m Model) hitNavRow(row int) trackHit {
	if m.navView() != navViewTracks || m.navBrowser.confirmReplace {
		return trackHit{cursor: -1}
	}
	if m.navBrowser.search != "" || len(m.navBrowser.searchIdx) > 0 {
		j := m.navBrowser.scroll + row
		if j >= len(m.navBrowser.searchIdx) {
			return trackHit{cursor: -1}
		}
		idx := m.navBrowser.searchIdx[j]
		if idx < 0 || idx >= len(m.navBrowser.tracks) {
			return trackHit{cursor: -1}
		}
		return trackHit{track: m.navBrowser.tracks[idx], cursor: j}
	}
	budget := m.navVisible()
	i := 0
	for r := range m.playlistRows(m.navBrowser.tracks, m.navBrowser.scroll, m.showAlbumHeaders) {
		if i >= budget {
			break
		}
		if r.Index < 0 {
			if i == row {
				return trackHit{cursor: -1}
			}
			i++
			continue
		}
		if i == row {
			return trackHit{track: r.Track, cursor: r.Index}
		}
		i++
	}
	return trackHit{cursor: -1}
}

// hitArtistRow replays renderArtistBody / renderArtistDrillBody. Section
// headers and discography (album) rows resolve to no track.
func (m Model) hitArtistRow(row int) trackHit {
	if len(m.artist.drill) > 0 {
		lvl := m.artist.drill[len(m.artist.drill)-1]
		if row == 0 || (lvl.loading && len(lvl.tracks) == 0) {
			return trackHit{cursor: -1} // crumb or loading row
		}
		i := lvl.scroll + row - 1
		if i >= len(lvl.tracks) {
			return trackHit{cursor: -1}
		}
		return trackHit{track: lvl.tracks[i], cursor: i}
	}
	if m.artist.loading {
		return trackHit{cursor: -1}
	}
	rows := m.artistRows()
	if len(rows) == 0 {
		return trackHit{cursor: -1}
	}
	if m.artistInfoLine() != "" {
		if row == 0 {
			return trackHit{cursor: -1} // profile info line
		}
		row--
	}
	listBudget := m.artistListVisible()
	i := 0
	prev := artistRowKind(-1)
	if m.artist.scroll > 0 && m.artist.scroll <= len(rows) {
		prev = rows[m.artist.scroll-1].kind
	}
	for j := m.artist.scroll; j < len(rows) && i < listBudget; j++ {
		if rows[j].kind != prev {
			if i == row {
				return trackHit{cursor: -1} // section header
			}
			i++
			if i >= listBudget {
				break
			}
		}
		if i == row {
			if rows[j].kind == artistRowDiscography {
				return trackHit{cursor: -1} // album row, not a track
			}
			return trackHit{track: rows[j].track, cursor: j}
		}
		i++
		prev = rows[j].kind
	}
	return trackHit{cursor: -1}
}

// hitHomeRow replays renderHomeBody's two-pane layout: the sidebar holds
// playlist/album/artist entries, the content pane's row 0 is its
// breadcrumb and rows 1+ are tracks.
func (m Model) hitHomeRow(row, col int) trackHit {
	if m.home.screen != homeScreenLibrary {
		return trackHit{cursor: -1}
	}
	sideW := homeSidebarWidth(max(1, ui.PanelWidth))
	if col <= sideW || row == 0 {
		return trackHit{cursor: -1}
	}
	c := &m.home.content
	i := c.scroll + row - 1
	if c.loading && len(c.tracks) == 0 || i >= len(c.tracks) {
		return trackHit{cursor: -1}
	}
	return trackHit{track: c.tracks[i], cursor: i}
}

// hitPlMgrRow replays renderPlMgrTracksBody: a flat filtered list or the
// album-header sectioned list when unfiltered. Only the tracks screen
// resolves rows.
func (m Model) hitPlMgrRow(row int) trackHit {
	if m.plManager.screen != plMgrScreenTracks || len(m.plManager.tracks) == 0 {
		return trackHit{cursor: -1}
	}
	budget := m.effectivePlaylistVisible()
	if m.plManager.filter != "" {
		i := m.plManager.scroll + row
		if i >= len(m.plManager.filtered) {
			return trackHit{cursor: -1}
		}
		j := m.plMgrTrackRealIndex(i)
		if j < 0 || j >= len(m.plManager.tracks) {
			return trackHit{cursor: -1}
		}
		return trackHit{track: m.plManager.tracks[j], cursor: i, remove: menuRemovePlMgr, removeIdx: i}
	}
	i := 0
	for r := range m.playlistRows(m.plManager.tracks, m.plManager.scroll, m.showAlbumHeaders) {
		if i >= budget {
			break
		}
		if r.Index < 0 {
			if i == row {
				return trackHit{cursor: -1}
			}
			i++
			continue
		}
		if i == row {
			return trackHit{track: r.Track, cursor: r.Index, remove: menuRemovePlMgr, removeIdx: r.Index}
		}
		i++
	}
	return trackHit{cursor: -1}
}

// highlightedTrack is the keyboard mirror of hitTrack: the track under the
// active surface's cursor. The context menu's hotkey equivalents and the
// "open menu" key act on it.
func (m Model) highlightedTrack() (playlist.Track, menuRemoveKind, int) {
	switch m.activeScreen() {
	case screenMain:
		if m.focus != focusPlaylist {
			return playlist.Track{}, menuRemoveNone, 0
		}
		if t, ok := m.playlist.Track(m.plCursor); ok {
			return t, menuRemovePlaylist, m.plCursor
		}
	case screenQueue:
		if m.queue.cursor < m.playlist.QueueLen() {
			tracks := m.playlist.QueueWindow(m.queue.cursor, 1)
			if len(tracks) > 0 {
				return tracks[0], menuRemoveQueue, m.queue.cursor
			}
		}
	case screenSearch:
		if m.search.cursor < len(m.search.results) {
			idx := m.search.results[m.search.cursor]
			if t, ok := m.playlist.Track(idx); ok {
				return t, menuRemovePlaylist, idx
			}
		}
	case screenNetSearch:
		if m.netSearch.screen == netSearchResults && m.netSearch.cursor < len(m.netSearch.results) {
			return m.netSearch.results[m.netSearch.cursor], menuRemoveNone, 0
		}
	case screenSpotSearch:
		return m.spotHighlightedTrack()
	case screenNavBrowser:
		return m.navHighlightedTrack()
	case screenArtist:
		return m.artistHighlightedTrack()
	case screenHome:
		if m.home.screen != homeScreenLibrary {
			return playlist.Track{}, menuRemoveNone, 0
		}
		c := m.home.content
		if c.cursor < len(c.tracks) {
			return c.tracks[c.cursor], menuRemoveNone, 0
		}
	case screenPlaylistManager:
		return m.plMgrHighlightedTrack()
	}
	return playlist.Track{}, menuRemoveNone, 0
}

func (m Model) spotHighlightedTrack() (playlist.Track, menuRemoveKind, int) {
	s := &m.spotSearch
	if s.screen != spotSearchResults {
		return playlist.Track{}, menuRemoveNone, 0
	}
	if s.multi {
		if n := len(s.drill); n > 0 {
			lvl := s.drill[n-1]
			if lvl.albums == nil && lvl.cursor < len(lvl.tracks) {
				return lvl.tracks[lvl.cursor], menuRemoveNone, 0
			}
			return playlist.Track{}, menuRemoveNone, 0
		}
		if s.tab == spotTabTracks && s.cursor < len(s.resultsAll.Tracks) {
			return s.resultsAll.Tracks[s.cursor], menuRemoveNone, 0
		}
		return playlist.Track{}, menuRemoveNone, 0
	}
	if s.cursor < len(s.results) && !s.results[s.cursor].IsAlbum() {
		return s.results[s.cursor], menuRemoveNone, 0
	}
	return playlist.Track{}, menuRemoveNone, 0
}

func (m Model) navHighlightedTrack() (playlist.Track, menuRemoveKind, int) {
	if m.navView() != navViewTracks {
		return playlist.Track{}, menuRemoveNone, 0
	}
	if len(m.navBrowser.searchIdx) > 0 || m.navBrowser.search != "" {
		if m.navBrowser.cursor < len(m.navBrowser.searchIdx) {
			j := m.navBrowser.searchIdx[m.navBrowser.cursor]
			if j >= 0 && j < len(m.navBrowser.tracks) {
				return m.navBrowser.tracks[j], menuRemoveNone, 0
			}
		}
		return playlist.Track{}, menuRemoveNone, 0
	}
	if m.navBrowser.cursor < len(m.navBrowser.tracks) {
		return m.navBrowser.tracks[m.navBrowser.cursor], menuRemoveNone, 0
	}
	return playlist.Track{}, menuRemoveNone, 0
}

func (m Model) artistHighlightedTrack() (playlist.Track, menuRemoveKind, int) {
	if n := len(m.artist.drill); n > 0 {
		lvl := m.artist.drill[n-1]
		if lvl.cursor < len(lvl.tracks) {
			return lvl.tracks[lvl.cursor], menuRemoveNone, 0
		}
		return playlist.Track{}, menuRemoveNone, 0
	}
	if m.artist.cursor < len(m.artistRows()) {
		r := m.artistRows()[m.artist.cursor]
		if r.kind != artistRowDiscography {
			return r.track, menuRemoveNone, 0
		}
	}
	return playlist.Track{}, menuRemoveNone, 0
}

func (m Model) plMgrHighlightedTrack() (playlist.Track, menuRemoveKind, int) {
	if m.plManager.screen != plMgrScreenTracks || len(m.plManager.tracks) == 0 {
		return playlist.Track{}, menuRemoveNone, 0
	}
	if m.plManager.filter != "" {
		if m.plManager.cursor < len(m.plManager.filtered) {
			j := m.plMgrTrackRealIndex(m.plManager.cursor)
			if j >= 0 && j < len(m.plManager.tracks) {
				return m.plManager.tracks[j], menuRemovePlMgr, m.plManager.cursor
			}
		}
		return playlist.Track{}, menuRemoveNone, 0
	}
	if m.plManager.cursor < len(m.plManager.tracks) {
		return m.plManager.tracks[m.plManager.cursor], menuRemovePlMgr, m.plManager.cursor
	}
	return playlist.Track{}, menuRemoveNone, 0
}
