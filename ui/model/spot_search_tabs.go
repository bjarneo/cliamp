package model

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

// spot_search_tabs.go implements the tabbed result screen and drill-down
// navigation shown when the searched provider implements provider.MultiSearcher.

// resultsAllCount returns the total number of rows across all result tabs.
func (s spotSearchState) resultsAllCount() int {
	return len(s.resultsAll.Tracks) + len(s.resultsAll.Albums) +
		len(s.resultsAll.Artists) + len(s.resultsAll.Playlists)
}

// spotTabCount returns the row count of one result tab.
func (m Model) spotTabCount(tab spotSearchTab) int {
	switch tab {
	case spotTabAlbums:
		return len(m.spotSearch.resultsAll.Albums)
	case spotTabArtists:
		return len(m.spotSearch.resultsAll.Artists)
	case spotTabPlaylists:
		return len(m.spotSearch.resultsAll.Playlists)
	default:
		return len(m.spotSearch.resultsAll.Tracks)
	}
}

// spotResultsListLen returns the number of rows in whichever list the results
// screen is currently showing (active tab, or the drilled-in list).
func (m Model) spotResultsListLen() int {
	if !m.spotSearch.multi {
		return len(m.spotSearch.results)
	}
	if n := len(m.spotSearch.drill); n > 0 {
		return m.spotDrillCount(m.spotSearch.drill[n-1])
	}
	return m.spotTabCount(m.spotSearch.tab)
}

// spotDrillCount returns the row count of one drill level.
func (m Model) spotDrillCount(lvl spotDrillLevel) int {
	if lvl.albums != nil {
		return len(lvl.albums)
	}
	return len(lvl.tracks)
}

// spotSwitchTab moves the tab selection by delta, wrapping around, and resets
// the list cursor.
func (m *Model) spotSwitchTab(delta int) {
	m.spotSearch.tab = (m.spotSearch.tab + spotSearchTab(delta%int(spotTabCount)) + spotTabCount) % spotTabCount
	m.spotSearch.cursor = 0
	m.spotSearch.scroll = 0
	m.spotSearch.err = ""
}

// handleSpotTabsKey handles keys on the tabbed multi-type result screen.
func (m *Model) handleSpotTabsKey(msg tea.KeyPressMsg) tea.Cmd {
	if len(m.spotSearch.drill) > 0 {
		return m.handleSpotDrillKey(msg)
	}
	count := m.spotTabCount(m.spotSearch.tab)

	switch msg.String() {
	case "ctrl+x":
		m.toggleExpandedView()
		m.spotSearchResultsMaybeAdjustScroll(m.spotSearchResultsVisible())
	case "up", "k", "ctrl+p":
		if m.spotSearch.cursor > 0 {
			m.spotSearch.cursor--
		} else if count > 0 {
			m.spotSearch.cursor = count - 1
		}
		m.spotSearchResultsMaybeAdjustScroll(m.spotSearchResultsVisible())
	case "down", "j", "ctrl+n":
		if m.spotSearch.cursor < count-1 {
			m.spotSearch.cursor++
		} else if count > 0 {
			m.spotSearch.cursor = 0
		}
		m.spotSearchResultsMaybeAdjustScroll(m.spotSearchResultsVisible())
	case "left":
		m.spotSwitchTab(-1)
	case "right", "tab":
		m.spotSwitchTab(1)
	case "shift+tab":
		m.spotSwitchTab(-1)
	case "enter", "l":
		if count > 0 && !m.spotSearch.loading {
			return m.spotDrillFromTab()
		}
	case "a":
		if m.spotSearch.tab == spotTabTracks && count > 0 && !m.spotSearch.loading {
			track := m.spotSearch.resultsAll.Tracks[m.spotSearch.cursor]
			m.closeSpotSearch()
			return m.appendTrack(track)
		}
	case "q":
		if m.spotSearch.tab == spotTabTracks && count > 0 && !m.spotSearch.loading {
			track := m.spotSearch.resultsAll.Tracks[m.spotSearch.cursor]
			m.closeSpotSearch()
			return m.queueTrackNext(track)
		}
	case "p":
		if m.spotSearch.tab == spotTabTracks && count > 0 && !m.spotSearch.loading {
			m.spotSearch.selTrack = m.spotSearch.resultsAll.Tracks[m.spotSearch.cursor]
			m.spotSearch.loading = true
			m.spotSearch.err = ""
			return fetchSpotPlaylistsCmd(m.spotSearch.prov, nextRequest(&m.requests.spotLists))
		}
	case "S":
		if m.spotSearch.tab == spotTabTracks && count > 0 && !m.spotSearch.loading {
			return m.likeTrack(m.spotSearch.resultsAll.Tracks[m.spotSearch.cursor])
		}
	case "f":
		if count > 0 && !m.spotSearch.loading {
			return m.spotFollowFromTab()
		}
	case "esc", "backspace":
		m.spotSearch.screen = spotSearchInput
		m.spotSearch.err = ""
	case "ctrl+u":
		step := m.spotSearchResultsVisible()
		if step < 1 {
			step = 1
		}
		if m.spotSearch.cursor >= step {
			m.spotSearch.cursor -= step
		} else {
			m.spotSearch.cursor = 0
		}
		m.spotSearchResultsMaybeAdjustScroll(m.spotSearchResultsVisible())
	case "ctrl+d":
		step := m.spotSearchResultsVisible()
		if step < 1 {
			step = 1
		}
		m.spotSearch.cursor += step
		if m.spotSearch.cursor >= count {
			m.spotSearch.cursor = max(0, count-1)
		}
		m.spotSearchResultsMaybeAdjustScroll(m.spotSearchResultsVisible())
	}
	return nil
}

// spotDrillFromTab enters the row under the cursor on the active tab: albums
// load their tracks, artists load their album list, playlists load their
// tracks.
func (m *Model) spotDrillFromTab() tea.Cmd {
	prov := m.spotSearch.prov
	gen := nextRequest(&m.requests.spotSearch)
	switch m.spotSearch.tab {
	case spotTabAlbums:
		l, ok := prov.(provider.AlbumTrackLoader)
		if !ok {
			m.status.Show("Album drill-down not supported", statusTTLDefault)
			return nil
		}
		album := m.spotSearch.resultsAll.Albums[m.spotSearch.cursor]
		crumb := "Album — " + album.Name
		m.spotPushDrill(crumb)
		return fetchSpotDrillAlbumTracksCmd(m.newSpotRequestContext(30*time.Second), l, prov.Name(), album.ID, crumb, gen)
	case spotTabArtists:
		if _, ok := prov.(provider.ArtistDetailLoader); ok {
			// The rich artist profile screen replaces the flat album
			// drill-down for providers that support it.
			artist := m.spotSearch.resultsAll.Artists[m.spotSearch.cursor]
			return m.openArtistScreen(prov.Name(), artist)
		}
		if _, ok := prov.(provider.ArtistBrowser); !ok {
			m.status.Show("Artist drill-down not supported", statusTTLDefault)
			return nil
		}
		artist := m.spotSearch.resultsAll.Artists[m.spotSearch.cursor]
		crumb := "Artist — " + artist.Name
		m.spotPushDrill(crumb)
		return fetchSpotArtistCmd(m.newSpotRequestContext(30*time.Second), prov, prov.Name(), artist, crumb, gen)
	case spotTabPlaylists:
		pl := m.spotSearch.resultsAll.Playlists[m.spotSearch.cursor]
		crumb := "Playlist — " + pl.Name
		m.spotPushDrill(crumb)
		return fetchSpotPlaylistTracksCmd(m.newSpotRequestContext(30*time.Second), prov, prov.Name(), pl.ID, crumb, gen)
	default:
		track := m.spotSearch.resultsAll.Tracks[m.spotSearch.cursor]
		m.closeSpotSearch()
		return m.playTrackImmediate(track)
	}
}

// spotPushDrill appends a loading drill level under the given crumb.
func (m *Model) spotPushDrill(crumb string) {
	m.spotSearch.drill = append(m.spotSearch.drill, spotDrillLevel{crumb: crumb, loading: true})
	m.spotSearch.err = ""
}

// spotFollowFromTab follows/unfollows the artist or playlist row under the
// cursor on the artist and playlist tabs.
func (m *Model) spotFollowFromTab() tea.Cmd {
	prov := m.spotSearch.prov
	switch m.spotSearch.tab {
	case spotTabArtists:
		f, ok := prov.(provider.ArtistFollower)
		if !ok {
			return nil
		}
		artist := m.spotSearch.resultsAll.Artists[m.spotSearch.cursor]
		follow := !m.followState[followKey("artist", prov.Name(), artist.ID)]
		return followArtistCmd(m.newLikeContext(), f, prov.Name(), artist.ID, artist.Name, follow, nextRequest(&m.requests.follow))
	case spotTabPlaylists:
		f, ok := prov.(provider.PlaylistFollower)
		if !ok {
			return nil
		}
		pl := m.spotSearch.resultsAll.Playlists[m.spotSearch.cursor]
		if pl.Owned {
			// Unfollowing an owned playlist deletes it server-side — route
			// through the provider pane's D flow, which asks for confirmation.
			m.status.Show("You own this playlist — delete it with D in the provider pane", statusTTLDefault)
			return nil
		}
		follow := !m.followState[followKey("playlist", prov.Name(), pl.ID)]
		return followPlaylistCmd(m.newLikeContext(), f, prov.Name(), pl.ID, pl.Name, follow, nextRequest(&m.requests.follow))
	}
	return nil
}

// handleSpotDrillKey handles keys inside a drilled-in list. Track lists carry
// the same row actions as the search track tab; album lists drill one level
// deeper.
func (m *Model) handleSpotDrillKey(msg tea.KeyPressMsg) tea.Cmd {
	lvl := &m.spotSearch.drill[len(m.spotSearch.drill)-1]
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
		m.spotDrillMaybeAdjustScroll(lvl)
	}

	switch msg.String() {
	case "ctrl+x":
		m.toggleExpandedView()
		m.spotDrillMaybeAdjustScroll(lvl)
	case "up", "k", "ctrl+p":
		move(-1)
	case "down", "j", "ctrl+n":
		move(1)
	case "enter", "l":
		if count == 0 || lvl.loading {
			return nil
		}
		if lvl.albums != nil {
			l, ok := m.spotSearch.prov.(provider.AlbumTrackLoader)
			if !ok {
				m.status.Show("Album drill-down not supported", statusTTLDefault)
				return nil
			}
			album := lvl.albums[lvl.cursor]
			crumb := "Album — " + album.Name
			m.spotPushDrill(crumb)
			return fetchSpotDrillAlbumTracksCmd(m.newSpotRequestContext(30*time.Second), l, m.spotSearch.prov.Name(), album.ID, crumb, nextRequest(&m.requests.spotSearch))
		}
		track := lvl.tracks[lvl.cursor]
		m.closeSpotSearch()
		return m.playTrackImmediate(track)
	case "a":
		if lvl.tracks != nil && count > 0 && !lvl.loading {
			track := lvl.tracks[lvl.cursor]
			m.closeSpotSearch()
			return m.appendTrack(track)
		}
	case "q":
		if lvl.tracks != nil && count > 0 && !lvl.loading {
			track := lvl.tracks[lvl.cursor]
			m.closeSpotSearch()
			return m.queueTrackNext(track)
		}
	case "p":
		if lvl.tracks != nil && count > 0 && !lvl.loading {
			m.spotSearch.selTrack = lvl.tracks[lvl.cursor]
			m.spotSearch.loading = true
			m.spotSearch.err = ""
			return fetchSpotPlaylistsCmd(m.spotSearch.prov, nextRequest(&m.requests.spotLists))
		}
	case "S":
		if lvl.tracks != nil && count > 0 && !lvl.loading {
			return m.likeTrack(lvl.tracks[lvl.cursor])
		}
	case "esc", "backspace", "h", "left":
		// Pop one level; when the stack empties, the tab bar cursor was
		// preserved all along, so the tabs reappear where they were left.
		m.spotSearch.drill = m.spotSearch.drill[:len(m.spotSearch.drill)-1]
		m.spotSearch.err = ""
	case "ctrl+u":
		step := max(1, m.spotSearchResultsVisible())
		if lvl.cursor >= step {
			lvl.cursor -= step
		} else {
			lvl.cursor = 0
		}
		m.spotDrillMaybeAdjustScroll(lvl)
	case "ctrl+d":
		step := max(1, m.spotSearchResultsVisible())
		lvl.cursor += step
		if lvl.cursor >= count {
			lvl.cursor = max(0, count-1)
		}
		m.spotDrillMaybeAdjustScroll(lvl)
	}
	return nil
}

func (m *Model) spotDrillMaybeAdjustScroll(lvl *spotDrillLevel) {
	clampScroll(&lvl.cursor, &lvl.scroll, m.spotDrillCount(*lvl), max(1, m.effectivePlaylistVisible()-1))
}

// spotDrillCrumb renders the full one-line crumb of the current drill path.
func (m Model) spotDrillCrumb() string {
	parts := make([]string, 0, len(m.spotSearch.drill))
	for _, lvl := range m.spotSearch.drill {
		parts = append(parts, lvl.crumb)
	}
	return strings.Join(parts, " / ")
}

// — rendering helpers used by inline_overlays.go —

// renderSpotTabBar renders the Tracks/Albums/Artists/Playlists bar with
// counts, highlighting the active tab.
func (m Model) renderSpotTabBar() string {
	cells := make([]string, 0, spotTabCount)
	for tab := spotSearchTab(0); tab < spotTabCount; tab++ {
		label := fmt.Sprintf("%s (%d)", spotTabLabels[tab], m.spotTabCount(tab))
		if tab == m.spotSearch.tab {
			cells = append(cells, playlistSelectedStyle.Render(label))
		} else {
			cells = append(cells, dimStyle.Render(label))
		}
	}
	return "  " + truncate(strings.Join(cells, "  "), max(1, ui.PanelWidth-2))
}

// renderSpotTabsBody renders the tab bar plus the active tab's rows.
func (m Model) renderSpotTabsBody(budget int) string {
	if budget <= 1 {
		return m.renderSpotTabBar()
	}
	count := m.spotTabCount(m.spotSearch.tab)
	list := ""
	switch {
	case count == 0:
		list = bodyMessage("No results", budget-1)
	case m.spotSearch.tab == spotTabAlbums:
		items := make([]string, count)
		for i, a := range m.spotSearch.resultsAll.Albums {
			if a.Year > 0 {
				items[i] = fmt.Sprintf("%s — %s (%d)", a.Name, a.Artist, a.Year)
			} else {
				items[i] = fmt.Sprintf("%s — %s", a.Name, a.Artist)
			}
		}
		list = windowList(items, m.spotSearch.cursor, m.spotSearch.scroll, budget-1)
	case m.spotSearch.tab == spotTabArtists:
		items := make([]string, count)
		for i, a := range m.spotSearch.resultsAll.Artists {
			items[i] = navArtistLabel(a)
		}
		list = windowList(items, m.spotSearch.cursor, m.spotSearch.scroll, budget-1)
	case m.spotSearch.tab == spotTabPlaylists:
		items := make([]string, count)
		for i, pl := range m.spotSearch.resultsAll.Playlists {
			items[i] = playlistLabel("", pl)
		}
		list = windowList(items, m.spotSearch.cursor, m.spotSearch.scroll, budget-1)
	default:
		items := make([]string, count)
		for i, t := range m.spotSearch.resultsAll.Tracks {
			items[i] = fmt.Sprintf("%s - %s", t.Artist, t.Title)
		}
		list = windowList(items, m.spotSearch.cursor, m.spotSearch.scroll, budget-1)
	}
	return m.renderSpotTabBar() + "\n" + list
}

// renderSpotDrillBody renders the drill crumb plus the drilled-in rows.
func (m Model) renderSpotDrillBody(budget int) string {
	lvl := m.spotSearch.drill[len(m.spotSearch.drill)-1]
	if budget <= 1 {
		return dimStyle.Render("  " + m.spotDrillCrumb())
	}
	crumb := dimStyle.Render("  " + truncate(m.spotDrillCrumb(), max(1, ui.PanelWidth-2)))
	var list string
	if lvl.loading && m.spotDrillCount(lvl) == 0 {
		list = bodyLines([]string{loadingLine("Loading…")}, budget-1)
	} else if lvl.albums != nil {
		items := make([]string, len(lvl.albums))
		for i, a := range lvl.albums {
			if a.Year > 0 {
				items[i] = fmt.Sprintf("%s — %s (%d)", a.Name, a.Artist, a.Year)
			} else {
				items[i] = fmt.Sprintf("%s — %s", a.Name, a.Artist)
			}
		}
		list = windowList(items, lvl.cursor, lvl.scroll, budget-1)
	} else {
		items := make([]string, len(lvl.tracks))
		for i, t := range lvl.tracks {
			items[i] = fmt.Sprintf("%s - %s", t.Artist, t.Title)
		}
		list = windowList(items, lvl.cursor, lvl.scroll, budget-1)
	}
	return crumb + "\n" + list
}
