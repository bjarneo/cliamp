package model

import (
	"fmt"
	"strings"

	"cliamp/ui"
)

// — provider browser (nav) —

type navViewKind int

const (
	navViewMenu navViewKind = iota
	navViewArtists
	navViewAlbums
	navViewTracks
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
	default:
		return navViewMenu
	}
}

func (m Model) navTrackBreadcrumb() string {
	switch m.navBrowser.mode {
	case navBrowseModeByArtist:
		return "Artist: " + m.navBrowser.selArtist.Name
	case navBrowseModeByAlbum:
		return "Album: " + m.navBrowser.selAlbum.Name
	case navBrowseModeByArtistAlbum:
		return m.navBrowser.selArtist.Name + " / " + m.navBrowser.selAlbum.Name
	}
	return "Tracks"
}

func (m Model) navHeaderLine() string {
	if m.navBrowser.searching {
		return filterPromptHeader(m.navBrowser.search)
	}
	switch m.navView() {
	case navViewArtists:
		return sepHeaderN("Artists", m.navBrowser.cursor+1, len(m.navBrowser.artists))
	case navViewAlbums:
		label := "Albums"
		if m.navBrowser.mode == navBrowseModeByArtistAlbum {
			label = "Albums: " + m.navBrowser.selArtist.Name
		} else if s := m.navSortLabel(m.navBrowser.sortType); s != "" {
			label += "  Sort: " + s
		}
		return sepHeaderN(label, m.navBrowser.cursor+1, len(m.navBrowser.albums))
	case navViewTracks:
		return sepHeaderN(m.navTrackBreadcrumb(), m.navBrowser.cursor+1, len(m.navBrowser.tracks))
	default:
		name := "Browse"
		if m.navBrowser.prov != nil {
			name = m.navBrowser.prov.Name()
		}
		return sepHeader(name)
	}
}

func (m Model) navHelpLine() string {
	if m.navBrowser.searching {
		return helpKey("Enter", "Confirm ") + helpKey("Esc", "Cancel ") + helpKey("Type", "Filter")
	}
	switch m.navView() {
	case navViewArtists:
		return helpKey("←↓↑→", "Navigate ") + helpKey("Enter", "Open ") + helpKey("/", "Search")
	case navViewAlbums:
		h := helpKey("←↓↑→", "Navigate ") + helpKey("Enter", "Open ")
		if m.navBrowser.mode == navBrowseModeByAlbum {
			h += helpKey("s", "Sort ")
		}
		return h + helpKey("/", "Search")
	case navViewTracks:
		return helpKey("←↓↑→", "Navigate ") + helpKey("Enter", "Play from here ") +
			helpKey("q", "Queue ") + helpKey("R", "Replace ") + helpKey("a", "Append ") + helpKey("/", "Search")
	default:
		return helpKey("↓↑", "Scroll ") + helpKey("Enter", "Select ") + helpKey("Esc", "Close")
	}
}

func (m Model) renderNavBody() string {
	budget := m.effectivePlaylistVisible()
	switch m.navView() {
	case navViewArtists:
		if m.navBrowser.loading && len(m.navBrowser.artists) == 0 {
			return bodyLines([]string{loadingLine("Loading artists…")}, budget)
		}
		if len(m.navBrowser.artists) == 0 {
			return bodyMessage("No artists found.", budget)
		}
		items := m.navScrollItems(len(m.navBrowser.artists), func(i int) string {
			a := m.navBrowser.artists[i]
			return truncate(fmt.Sprintf("%s (%d albums)", a.Name, a.AlbumCount), ui.PanelWidth-6)
		})
		return strings.Join(items, "\n")
	case navViewAlbums:
		if m.navBrowser.loading && len(m.navBrowser.albums) == 0 {
			return bodyLines([]string{loadingLine("Loading albums…")}, budget)
		}
		if len(m.navBrowser.albums) == 0 {
			return bodyMessage("No albums found.", budget)
		}
		items := m.navScrollItems(len(m.navBrowser.albums), func(i int) string {
			a := m.navBrowser.albums[i]
			if a.Year > 0 {
				return truncate(fmt.Sprintf("%s — %s (%d)", a.Name, a.Artist, a.Year), ui.PanelWidth-6)
			}
			return truncate(fmt.Sprintf("%s — %s", a.Name, a.Artist), ui.PanelWidth-6)
		})
		return strings.Join(items, "\n")
	case navViewTracks:
		return m.renderNavTrackBody(budget)
	default:
		items := []string{"By Album", "By Artist", "By Artist / Album"}
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

	if len(m.navBrowser.searchIdx) > 0 || m.navBrowser.search != "" {
		items := m.navScrollItems(len(m.navBrowser.tracks), func(i int) string {
			t := m.navBrowser.tracks[i]
			return formatTrackRow(i+1, t.DisplayName()+trackAlbumSuffix(t, m.showAlbumHeaders), t.DurationSecs)
		})
		return strings.Join(items, "\n")
	}

	return m.renderTrackRowsBody(m.navBrowser.tracks, m.navBrowser.cursor, m.navBrowser.scroll, budget)
}

// — file browser —

func (m Model) fbHeaderLine() string {
	if m.fileBrowser.searching {
		return filterPromptHeader(m.fileBrowser.search)
	}
	label := "Files: " + m.fileBrowser.dir
	if n := len(m.fileBrowser.selected); n > 0 {
		label += fmt.Sprintf("  [%d selected]", n)
	}
	return sepHeader(label)
}

func (m Model) renderFileBrowserBody() string {
	budget := m.effectivePlaylistVisible()
	if budget <= 0 {
		return ""
	}

	var lines []string
	if m.fileBrowser.err != "" {
		lines = append(lines, errorStyle.Render("  "+m.fileBrowser.err))
	}

	count := m.fbCount()
	if count == 0 {
		if m.fileBrowser.search != "" {
			lines = append(lines, dimStyle.Render("  No matches"))
		} else {
			lines = append(lines, dimStyle.Render("  (empty)"))
		}
		return bodyLines(lines, budget)
	}

	scroll := max(m.fileBrowser.scroll, 0)
	if scroll > count-1 {
		scroll = max(0, count-1)
	}
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
		label := truncate(check+e.name+suffix, max(1, ui.PanelWidth-2))

		switch {
		case m.fileBrowser.searching:
			lines = append(lines, dimStyle.Render("  "+label))
		case i == m.fileBrowser.cursor:
			lines = append(lines, playlistSelectedStyle.Render("> "+label))
		case e.isDir:
			lines = append(lines, trackStyle.Render("  "+label))
		case e.isAudio:
			lines = append(lines, playlistItemStyle.Render("  "+label))
		default:
			lines = append(lines, dimStyle.Render("  "+label))
		}
	}
	return bodyLines(lines, budget)
}
