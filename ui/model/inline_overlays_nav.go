package model

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// — provider browser (nav) —

type navViewKind int

const (
	navViewMenu navViewKind = iota
	navViewArtists
	navViewAlbums
	navViewTracks
	navViewGenres
	navViewGenreSorts
)

// navView collapses the nav browser's mode + screen into the list actually
// shown, so the header, help, and body all agree.
func (m Model) navView() navViewKind {
	switch m.navBrowser.mode {
	case navBrowseModeByAlbum:
		if m.navBrowser.screen == navBrowseScreenTracks {
			return navViewTracks
		}
		return navViewAlbums
	case navBrowseModeByArtist:
		if m.navBrowser.screen == navBrowseScreenTracks {
			return navViewTracks
		}
		return navViewArtists
	case navBrowseModeByArtistAlbum:
		switch m.navBrowser.screen {
		case navBrowseScreenAlbums:
			return navViewAlbums
		case navBrowseScreenTracks:
			return navViewTracks
		default:
			return navViewArtists
		}
	case navBrowseModeByGenre:
		switch m.navBrowser.screen {
		case navBrowseScreenAlbums:
			return navViewGenreSorts
		case navBrowseScreenTracks:
			return navViewTracks
		default:
			return navViewGenres
		}
	default:
		return navViewMenu
	}
}

func (m Model) navHeaderLine() string {
	if m.navBrowser.confirmReplace {
		return sepHeader("Replace current queue?", m.layout.panelWidth)
	}
	if m.navBrowser.searching {
		return m.filterHeader("Filter: "+m.navBreadcrumb(), "nav-search", m.navBrowser.search, "")
	}
	switch m.navView() {
	case navViewArtists:
		return sepHeaderN(m.navBreadcrumb(), m.navBrowser.cursor+1, len(m.navBrowser.artists), m.layout.panelWidth)
	case navViewAlbums:
		return sepHeaderN(m.navBreadcrumb(), m.navBrowser.cursor+1, len(m.navBrowser.albums), m.layout.panelWidth)
	case navViewTracks:
		label := m.navBreadcrumb()
		if m.navTrackListFileStyle() {
			if n := len(m.navBrowser.selected); n > 0 {
				label += fmt.Sprintf("  [%d selected]", n)
			}
			return sepHeaderN(label, m.navBrowser.cursor+1, len(m.navBrowser.tracks), m.layout.panelWidth)
		}
		return sepHeaderN(label, m.navBrowser.cursor+1, len(m.navBrowser.tracks), m.layout.panelWidth)
	case navViewGenres:
		return sepHeaderN(m.navBreadcrumb(), m.navBrowser.cursor+1, len(m.navBrowser.genres), m.layout.panelWidth)
	case navViewGenreSorts:
		return sepHeaderN(m.navBreadcrumb(), m.navBrowser.cursor+1, len(m.navBrowser.genreSorts), m.layout.panelWidth)
	default:
		return sepHeader(m.navBreadcrumb(), m.layout.panelWidth)
	}
}

// renderNavBody renders the list for the active provider-browser route.
func (m Model) renderNavBody() string {
	labels := m.navLabels()
	budget := m.effectivePlaylistVisible()
	if m.navBrowser.confirmReplace {
		msg := "Displayed tracks will replace the current queue. Enter confirms; Esc cancels."
		if m.navTrackListFileStyle() {
			msg = "Selected episodes will replace the current queue. Enter confirms; Esc cancels."
		}
		return bodyMessage(msg, budget)
	}
	switch m.navView() {
	case navViewArtists:
		if m.navBrowser.loading && len(m.navBrowser.artists) == 0 {
			return bodyLines([]string{loadingLine("Loading " + labels.artistsLower() + "…")}, budget)
		}
		if m.navBrowser.search != "" && len(m.navBrowser.searchIdx) == 0 {
			return bodyMessage("No matches.", budget)
		}
		if len(m.navBrowser.artists) == 0 {
			return bodyMessage("No "+labels.artistsLower()+" found.", budget)
		}
		items := m.navScrollItems(len(m.navBrowser.artists), func(i int) string {
			a := m.navBrowser.artists[i]
			name := a.Name
			if a.AlbumCount > 0 {
				name = fmt.Sprintf("%s (%d %s)", name, a.AlbumCount, labels.albumsLower())
			}
			return truncate(name, m.layout.panelWidth-6)
		})
		return strings.Join(items, "\n")
	case navViewAlbums:
		if (m.navBrowser.loading || m.navBrowser.albumLoading) && len(m.navBrowser.albums) == 0 {
			return bodyLines([]string{loadingLine("Loading " + labels.albumsLower() + "…")}, budget)
		}
		if m.navBrowser.search != "" && len(m.navBrowser.searchIdx) == 0 {
			return bodyMessage("No matches.", budget)
		}
		if len(m.navBrowser.albums) == 0 {
			return bodyMessage("No "+labels.albumsLower()+" found.", budget)
		}
		items := m.navScrollItems(len(m.navBrowser.albums), func(i int) string {
			a := m.navBrowser.albums[i]
			name := albumViewName(a)
			if a.Year > 0 {
				return truncate(fmt.Sprintf("%s — %s (%d)", name, a.Artist, a.Year), m.layout.panelWidth-6)
			}
			return truncate(fmt.Sprintf("%s — %s", name, a.Artist), m.layout.panelWidth-6)
		})
		return strings.Join(items, "\n")
	case navViewTracks:
		return m.renderNavTrackBody(budget)
	case navViewGenres:
		genres := m.navLabels().genresLower()
		if m.navBrowser.loading && len(m.navBrowser.genres) == 0 {
			return bodyLines([]string{loadingLine("Loading " + genres + "…")}, budget)
		}
		if m.navBrowser.search != "" && len(m.navBrowser.searchIdx) == 0 {
			return bodyMessage("No matches.", budget)
		}
		if len(m.navBrowser.genres) == 0 {
			return bodyMessage("No "+genres+" found.", budget)
		}
		_, canFavorite := m.navGenreBrowser().(provider.GenreFavoriteToggler)
		items := m.navScrollItems(len(m.navBrowser.genres), func(i int) string {
			genre := m.navBrowser.genres[i]
			mark := ""
			if canFavorite {
				mark = "☆ "
				if genre.Favorite {
					mark = "★ "
				}
			}
			label := mark + genre.Name
			if genre.Group != "" && !strings.EqualFold(genre.Group, "music") {
				label += " — " + genre.Group
			}
			return truncate(label, m.layout.panelWidth-6)
		})
		return strings.Join(items, "\n")
	case navViewGenreSorts:
		if len(m.navBrowser.genreSorts) == 0 {
			return bodyMessage("No views found.", budget)
		}
		items := m.navScrollItems(len(m.navBrowser.genreSorts), func(i int) string {
			return truncate(m.navBrowser.genreSorts[i].Label, m.layout.panelWidth-6)
		})
		return strings.Join(items, "\n")
	default:
		menu := m.navMenuItems()
		items := make([]string, len(menu))
		for i := range menu {
			items[i] = menu[i].label
		}
		return windowList(items, m.navBrowser.cursor, 0, budget)
	}
}

func (m Model) renderNavTrackBody(budget int) string {
	if m.navBrowser.loading && len(m.navBrowser.tracks) == 0 {
		return bodyLines([]string{loadingLine("Loading tracks…")}, budget)
	}
	if len(m.navBrowser.tracks) == 0 {
		return bodyMessage("No tracks found.", budget)
	}
	if m.navBrowser.search != "" && len(m.navBrowser.searchIdx) == 0 {
		return bodyMessage("No matches.", budget)
	}

	if m.navTrackListFileStyle() {
		return m.renderNavTrackRowsWithSelection(budget)
	}

	if m.navBrowser.search != "" {
		items := m.navScrollItems(len(m.navBrowser.tracks), func(i int) string {
			t := m.navBrowser.tracks[i]
			return formatTrackRow(i+1, trackViewName(t)+trackAlbumSuffix(t, m.showAlbumHeaders), t.DurationSecs, m.layout.panelWidth)
		})
		return strings.Join(items, "\n")
	}

	return m.renderTrackRowsBody(m.navBrowser.tracks, m.navBrowser.cursor, m.navBrowser.scroll, budget)
}

func (m Model) renderNavTrackRowsWithSelection(budget int) string {
	indices := m.navTrackViewIndices()
	if len(indices) == 0 {
		return bodyMessage("No matches.", budget)
	}
	tracks := m.navBrowser.tracks
	numWidth := len(fmt.Sprintf("%d", len(indices)))
	var reporters []provider.PlaybackStateReporter
	if m.hasPlaybackState() {
		reporters = m.playbackStateReporters()
	}
	lines := make([]string, 0, budget)
	scroll := max(m.navBrowser.scroll, 0)
	if scroll > len(indices)-1 {
		scroll = max(0, len(indices)-1)
	}
	for viewPos := scroll; viewPos < len(indices) && len(lines) < budget; viewPos++ {
		rawIdx := indices[viewPos]
		if rawIdx < 0 || rawIdx >= len(tracks) {
			continue
		}
		t := tracks[rawIdx]
		selected := m.navBrowser.selected[navTrackSelectKey(t)]
		lines = append(lines, m.navTrackRow(t, viewPos+1, numWidth, selected, viewPos == m.navBrowser.cursor, reporters))
	}
	return bodyLines(lines, budget)
}

// navTrackRow renders one nav-browser episode row with cursor, selection, and
// listened-state markers—the same glyphs as the main playlist.
func (m Model) navTrackRow(t playlist.Track, num, numWidth int, marked, cursor bool, reporters []provider.PlaybackStateReporter) string {
	style := playlistItemStyle
	if cursor {
		style = playlistSelectedStyle
	}
	cursorMarker := " "
	if cursor {
		cursorMarker = ">"
	}
	selectMarker, selectStyle := " ", dimStyle
	if marked {
		selectMarker, selectStyle = "✓", activeToggle
	}
	stateMarker, stateStyle := " ", dimStyle
	if state, ok := playbackStateFrom(reporters, t); ok {
		switch {
		case state.Played:
			stateMarker, stateStyle = playedMarker, activeToggle
		case state.Position > 0:
			stateMarker, stateStyle = partialMarker, dimStyle
		}
	}
	prefix := cursorMarker + selectMarker + stateMarker + " "
	styledPrefix := dimStyle.Render(cursorMarker) + selectStyle.Render(selectMarker) + stateStyle.Render(stateMarker) + " "

	name := trackViewName(t) + trackAlbumSuffix(t, m.showAlbumHeaders)
	tail := podcastEpisodeRowTail(t, time.Now())
	tailLen := lipgloss.Width(tail)
	prefixWidth := lipgloss.Width(prefix) + numWidth + 2
	titleBudget := m.layout.panelWidth - prefixWidth
	if tail != "" {
		titleBudget -= tailLen + 1
	}
	if titleBudget < 4 {
		titleBudget = 4
	}
	name = truncate(name, titleBudget)

	line := styledPrefix + style.Render(fmt.Sprintf("%*d. ", numWidth, num)) + style.Render(name)
	if tail != "" {
		padding := max(1, m.layout.panelWidth-lipgloss.Width(line)-tailLen)
		line += strings.Repeat(" ", padding) + dimStyle.Render(tail)
	}
	return line
}

func (m Model) navTrackViewIndices() []int {
	if m.navBrowser.search != "" {
		return m.navBrowser.searchIdx
	}
	out := make([]int, len(m.navBrowser.tracks))
	for i := range out {
		out[i] = i
	}
	return out
}

// — file browser —

func (m Model) fbHeaderLine() string {
	if m.fileBrowser.confirmReplace {
		return sepHeader("Replace current queue?", m.layout.panelWidth)
	}
	if m.fileBrowser.filtering {
		count := ""
		if m.fileBrowser.usesIndex() {
			count = fmt.Sprintf("%d found", m.fbCount())
		}
		return m.filterHeader("Search: Files", "file-browser-search", m.fileBrowser.filter, count)
	}
	label := "Files: " + m.fileBrowser.dir
	dir := m.fileBrowser.dir
	if name := m.fileBrowser.targetPlaylist; name != "" {
		switch m.fileBrowser.targetPurpose {
		case fbTargetAddFiles:
			label = fmt.Sprintf(`Add files to "%s": %s`, name, dir)
		case fbTargetAddDirSource:
			label = fmt.Sprintf(`Add dir source to "%s": %s`, name, dir)
		}
	}
	if m.fileBrowser.filter != "" && m.fileBrowser.usesIndex() {
		if m.fileBrowser.targetPlaylist != "" && m.fileBrowser.targetPurpose != fbTargetNone {
			label = fmt.Sprintf("%s  [%d matches, subfolders included]", label, m.fbCount())
		} else {
			label = fmt.Sprintf("Files: %s  [%d matches, subfolders included]", m.fileBrowser.dir, m.fbCount())
		}
	}
	if n := len(m.fileBrowser.selected); n > 0 {
		label += fmt.Sprintf("  [%d selected]", n)
	}
	if sort := m.fileBrowser.sortMode.label(); sort != "" {
		head := dimStyle.Render("── "+terminalText(label)+" · ") +
			trackStyle.Render(terminalText("Sort: "+sort)) + " "
		return fillSeparator(head, m.layout.panelWidth)
	}
	return sepHeader(label, m.layout.panelWidth)
}

func (m Model) renderFileBrowserBody() string {
	budget := m.effectivePlaylistVisible()
	if budget <= 0 {
		return ""
	}
	if m.fileBrowser.confirmReplace {
		return bodyMessage("Selected files will replace the current queue. Enter confirms; Esc cancels.", budget)
	}

	var lines []string
	if m.fileBrowser.err != "" {
		lines = append(lines, errorStyle.Render("  "+m.fileBrowser.err))
	}

	count := m.fbCount()
	if count == 0 {
		switch {
		case m.fileBrowser.indexLoading && m.fileBrowser.filter != "":
			lines = append(lines, loadingLine("Searching subfolders…"))
		case m.fileBrowser.filter != "":
			lines = append(lines, dimStyle.Render("  No matches"))
		default:
			lines = append(lines, dimStyle.Render("  (empty)"))
		}
		return bodyLines(lines, budget)
	}

	scroll := max(m.fileBrowser.scroll, 0)
	if scroll > count-1 {
		scroll = max(0, count-1)
	}
	// An index row is labelled by its path from the browsed directory, so two
	// files with the same name stay distinguishable.
	for i := scroll; i < count && len(lines) < budget; i++ {
		e := m.fbEntry(i)
		check := "  "
		if m.fileBrowser.selected[e.path] {
			check = "✓ "
		}
		suffix := ""
		if e.isAudio {
			suffix = " ♫"
		}
		label := truncate(check+e.name+suffix, max(1, m.layout.panelWidth-2))

		switch {
		case m.fileBrowser.filtering:
			lines = append(lines, dimStyle.Render("  "+label))
		case i == m.fileBrowser.cursor:
			lines = append(lines, playlistSelectedStyle.Render("> "+label))
		case e.isDir:
			lines = append(lines, playlistItemStyle.Render("  "+label))
		case e.isAudio:
			lines = append(lines, playlistItemStyle.Render("  "+label))
		default:
			lines = append(lines, dimStyle.Render("  "+label))
		}
	}
	return bodyLines(lines, budget)
}
