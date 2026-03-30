package ui

import (
	"fmt"
	"strings"

	"cliamp/provider"
)

// — Navidrome browser renderers —

func (m Model) renderNavBrowser() string {
	var lines []string
	switch m.navBrowser.mode {
	case navBrowseModeMenu:
		lines = m.renderNavMenu()
	case navBrowseModeByAlbum:
		switch m.navBrowser.screen {
		case navBrowseScreenTracks:
			lines = m.renderNavTrackList()
		default:
			lines = m.renderNavAlbumList(false)
		}
	case navBrowseModeByArtist:
		switch m.navBrowser.screen {
		case navBrowseScreenTracks:
			lines = m.renderNavTrackList()
		default:
			lines = m.renderNavArtistList()
		}
	case navBrowseModeByArtistAlbum:
		switch m.navBrowser.screen {
		case navBrowseScreenAlbums:
			lines = m.renderNavAlbumList(true)
		case navBrowseScreenTracks:
			lines = m.renderNavTrackList()
		default:
			lines = m.renderNavArtistList()
		}
	default:
		lines = m.renderNavMenu()
	}
	return m.centerOverlay(strings.Join(m.appendFooterMessages(lines), "\n"))
}

func (m Model) renderNavMenu() []string {
	lines := []string{
		titleStyle.Render("N A V I D R O M E"),
		"",
	}

	items := []string{"By Album", "By Artist", "By Artist / Album"}
	for i, item := range items {
		lines = append(lines, cursorLine(item, i == m.navBrowser.cursor))
	}

	lines = append(lines, "",
		helpKey("↑↓", "Navigate ")+helpKey("Enter", "Select ")+helpKey("Esc", "Close"))

	return lines
}

func (m Model) renderNavArtistList() []string {
	lines := []string{titleStyle.Render("A R T I S T S"), ""}

	if m.navBrowser.loading && len(m.navBrowser.artists) == 0 {
		lines = append(lines, dimStyle.Render("  Loading artists..."), "", helpKey("Esc", "Back"))
		return lines
	}

	if len(m.navBrowser.artists) == 0 {
		lines = append(lines, dimStyle.Render("  No artists found."), "", helpKey("Esc", "Back"))
		return lines
	}

	items := m.navScrollItems(len(m.navBrowser.artists), func(i int) string {
		a := m.navBrowser.artists[i]
		return truncate(fmt.Sprintf("%s (%d albums)", a.Name, a.AlbumCount), panelWidth-6)
	})
	lines = append(lines, items...)

	lines = append(lines, "", m.navCountLine("artists", len(m.navBrowser.artists)))
	lines = append(lines, m.navSearchBar(
		helpKey("←↑↓→", "Navigate ")+helpKey("Enter", "Open ")+helpKey("/", "Search"))...)

	return lines
}

func (m Model) renderNavAlbumList(artistAlbums bool) []string {
	var titleStr string
	if artistAlbums {
		titleStr = titleStyle.Render("A L B U M S : " + m.navBrowser.selArtist.Name)
	} else {
		titleStr = titleStyle.Render("A L B U M S")
	}

	lines := []string{titleStr, ""}

	if !artistAlbums {
		sortLabel := m.navSortLabel(m.navBrowser.sortType)
		lines = append(lines, dimStyle.Render("  Sort: ")+activeToggle.Render(sortLabel), "")
	}

	if m.navBrowser.loading && len(m.navBrowser.albums) == 0 {
		lines = append(lines, dimStyle.Render("  Loading albums..."))
		help := helpKey("Esc", "Back")
		if !artistAlbums {
			help = helpKey("s", "Sort ") + help
		}
		lines = append(lines, "", help)
		return lines
	}

	if len(m.navBrowser.albums) == 0 {
		lines = append(lines, dimStyle.Render("  No albums found."))
		help := helpKey("Esc", "Back")
		if !artistAlbums {
			help = helpKey("s", "Sort ") + help
		}
		lines = append(lines, "", help)
		return lines
	}

	items := m.navScrollItems(len(m.navBrowser.albums), func(i int) string {
		a := m.navBrowser.albums[i]
		var label string
		if a.Year > 0 {
			label = fmt.Sprintf("%s — %s (%d)", a.Name, a.Artist, a.Year)
		} else {
			label = fmt.Sprintf("%s — %s", a.Name, a.Artist)
		}
		return truncate(label, panelWidth-6)
	})
	lines = append(lines, items...)

	if m.navBrowser.albumLoading {
		lines = append(lines, dimStyle.Render("  Loading more..."))
	} else {
		lines = append(lines, m.navCountLine("albums", len(m.navBrowser.albums)))
	}

	defaultHelp := helpKey("←↑↓→", "Navigate ") + helpKey("Enter", "Open ")
	if !artistAlbums {
		defaultHelp += helpKey("s", "Sort ")
	}
	defaultHelp += helpKey("/", "Search")
	lines = append(lines, m.navSearchBar(defaultHelp)...)

	return lines
}

func (m Model) renderNavTrackList() []string {
	var breadcrumb string
	switch m.navBrowser.mode {
	case navBrowseModeByArtist:
		breadcrumb = "A R T I S T : " + m.navBrowser.selArtist.Name
	case navBrowseModeByAlbum:
		breadcrumb = "A L B U M : " + m.navBrowser.selAlbum.Name
	case navBrowseModeByArtistAlbum:
		breadcrumb = m.navBrowser.selArtist.Name + " / " + m.navBrowser.selAlbum.Name
	}

	lines := []string{titleStyle.Render(breadcrumb), ""}

	if m.navBrowser.loading && len(m.navBrowser.tracks) == 0 {
		lines = append(lines, dimStyle.Render("  Loading tracks..."), "", helpKey("Esc", "Back"))
		return lines
	}

	if len(m.navBrowser.tracks) == 0 {
		lines = append(lines, dimStyle.Render("  No tracks found."), "", helpKey("Esc", "Back"))
		return lines
	}

	maxVisible := m.plVisible
	if maxVisible < 5 {
		maxVisible = 5
	}

	useFilter := len(m.navBrowser.searchIdx) > 0 || m.navBrowser.search != ""

	if useFilter {
		items := m.navScrollItems(len(m.navBrowser.tracks), func(i int) string {
			return fmt.Sprintf("%d. %s", i+1, truncate(m.navBrowser.tracks[i].DisplayName(), panelWidth-8))
		})
		lines = append(lines, items...)
	} else {
		scroll := m.navBrowser.scroll
		rendered := 0
		prevAlbum := ""
		if scroll > 0 {
			prevAlbum = m.navBrowser.tracks[scroll-1].Album
		}

		for i := scroll; i < len(m.navBrowser.tracks) && rendered < maxVisible; i++ {
			t := m.navBrowser.tracks[i]

			if album := t.Album; album != "" && album != prevAlbum {
				lines = append(lines, albumSeparator(album, t.Year))
				if rendered >= maxVisible {
					break
				}
			}
			prevAlbum = t.Album

			label := fmt.Sprintf("%d. %s", i+1, truncate(t.DisplayName(), panelWidth-8))
			lines = append(lines, cursorLine(label, i == m.navBrowser.cursor))
			rendered++
		}

		lines = padLines(lines, maxVisible, rendered)
	}

	lines = append(lines, "", m.navCountLine("tracks", len(m.navBrowser.tracks)))
	lines = append(lines, m.navSearchBar(
		helpKey("←↑↓→", "Navigate ")+
			helpKey("Enter", "Play ")+
			helpKey("q", "Queue ")+
			helpKey("R", "Replace ")+
			helpKey("a", "Append ")+
			helpKey("/", "Search"))...)
	return lines
}

// navSortLabel returns the human-readable label for the current sort type
// by querying the provider's AlbumSortTypes. Falls back to the raw ID.
func (m Model) navSortLabel(sortID string) string {
	if ab, ok := m.navBrowser.prov.(provider.AlbumBrowser); ok {
		for _, st := range ab.AlbumSortTypes() {
			if st.ID == sortID {
				return st.Label
			}
		}
	}
	return sortID
}
