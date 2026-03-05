package ui

import (
	"fmt"
	"strings"

	"cliamp/external/navidrome"
)

// — Navidrome browser renderers —

func (m Model) renderNavBrowser() string {
	switch m.navMode {
	case navBrowseModeMenu:
		return m.renderNavMenu()
	case navBrowseModeByAlbum:
		switch m.navScreen {
		case navBrowseScreenTracks:
			return m.renderNavTrackList()
		default:
			return m.renderNavAlbumList(false)
		}
	case navBrowseModeByArtist:
		switch m.navScreen {
		case navBrowseScreenTracks:
			return m.renderNavTrackList()
		default:
			return m.renderNavArtistList()
		}
	case navBrowseModeByArtistAlbum:
		switch m.navScreen {
		case navBrowseScreenAlbums:
			return m.renderNavAlbumList(true)
		case navBrowseScreenTracks:
			return m.renderNavTrackList()
		default:
			return m.renderNavArtistList()
		}
	}
	return m.renderNavMenu()
}

func (m Model) renderNavMenu() string {
	lines := []string{
		titleStyle.Render("N A V I D R O M E"),
		"",
	}

	items := []string{"By Album", "By Artist", "By Artist / Album"}
	for i, item := range items {
		lines = append(lines, cursorLine(item, i == m.navCursor))
	}

	lines = append(lines, "",
		helpKey("↑↓", "Navigate ")+helpKey("Enter", "Select ")+helpKey("Esc", "Close"))

	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) renderNavArtistList() string {
	lines := []string{titleStyle.Render("A R T I S T S"), ""}

	if m.navLoading && len(m.navArtists) == 0 {
		lines = append(lines, dimStyle.Render("  Loading artists..."), "", helpKey("Esc", "Back"))
		return m.centerOverlay(strings.Join(lines, "\n"))
	}

	if len(m.navArtists) == 0 {
		lines = append(lines, dimStyle.Render("  No artists found."), "", helpKey("Esc", "Back"))
		return m.centerOverlay(strings.Join(lines, "\n"))
	}

	items := m.navScrollItems(len(m.navArtists), func(i int) string {
		a := m.navArtists[i]
		return truncate(fmt.Sprintf("%s (%d albums)", a.Name, a.AlbumCount), panelWidth-6)
	})
	lines = append(lines, items...)

	lines = append(lines, "", m.navCountLine("artists", len(m.navArtists)))
	lines = append(lines, m.navSearchBar(
		helpKey("←↑↓→", "Navigate ")+helpKey("Enter", "Open ")+helpKey("/", "Search"))...)

	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) renderNavAlbumList(artistAlbums bool) string {
	var titleStr string
	if artistAlbums {
		titleStr = titleStyle.Render("A L B U M S : " + m.navSelArtist.Name)
	} else {
		titleStr = titleStyle.Render("A L B U M S")
	}

	lines := []string{titleStr, ""}

	if !artistAlbums {
		sortLabel := navidrome.SortTypeLabel(m.navSortType)
		lines = append(lines, dimStyle.Render("  Sort: ")+activeToggle.Render(sortLabel), "")
	}

	if m.navLoading && len(m.navAlbums) == 0 {
		lines = append(lines, dimStyle.Render("  Loading albums..."))
		help := helpKey("Esc", "Back")
		if !artistAlbums {
			help = helpKey("s", "Sort ") + help
		}
		lines = append(lines, "", help)
		return m.centerOverlay(strings.Join(lines, "\n"))
	}

	if len(m.navAlbums) == 0 {
		lines = append(lines, dimStyle.Render("  No albums found."))
		help := helpKey("Esc", "Back")
		if !artistAlbums {
			help = helpKey("s", "Sort ") + help
		}
		lines = append(lines, "", help)
		return m.centerOverlay(strings.Join(lines, "\n"))
	}

	items := m.navScrollItems(len(m.navAlbums), func(i int) string {
		a := m.navAlbums[i]
		var label string
		if a.Year > 0 {
			label = fmt.Sprintf("%s — %s (%d)", a.Name, a.Artist, a.Year)
		} else {
			label = fmt.Sprintf("%s — %s", a.Name, a.Artist)
		}
		return truncate(label, panelWidth-6)
	})
	lines = append(lines, items...)

	if m.navAlbumLoading {
		lines = append(lines, dimStyle.Render("  Loading more..."))
	} else {
		lines = append(lines, m.navCountLine("albums", len(m.navAlbums)))
	}

	defaultHelp := helpKey("←↑↓→", "Navigate ") + helpKey("Enter", "Open ")
	if !artistAlbums {
		defaultHelp += helpKey("s", "Sort ")
	}
	defaultHelp += helpKey("/", "Search")
	lines = append(lines, m.navSearchBar(defaultHelp)...)

	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) renderNavTrackList() string {
	var breadcrumb string
	switch m.navMode {
	case navBrowseModeByArtist:
		breadcrumb = "A R T I S T : " + m.navSelArtist.Name
	case navBrowseModeByAlbum:
		breadcrumb = "A L B U M : " + m.navSelAlbum.Name
	case navBrowseModeByArtistAlbum:
		breadcrumb = m.navSelArtist.Name + " / " + m.navSelAlbum.Name
	}

	lines := []string{titleStyle.Render(breadcrumb), ""}

	if m.navLoading && len(m.navTracks) == 0 {
		lines = append(lines, dimStyle.Render("  Loading tracks..."), "", helpKey("Esc", "Back"))
		return m.centerOverlay(strings.Join(lines, "\n"))
	}

	if len(m.navTracks) == 0 {
		lines = append(lines, dimStyle.Render("  No tracks found."), "", helpKey("Esc", "Back"))
		return m.centerOverlay(strings.Join(lines, "\n"))
	}

	maxVisible := m.plVisible
	if maxVisible < 5 {
		maxVisible = 5
	}

	useFilter := len(m.navSearchIdx) > 0 || m.navSearch != ""

	if useFilter {
		items := m.navScrollItems(len(m.navTracks), func(i int) string {
			return fmt.Sprintf("%d. %s", i+1, truncate(m.navTracks[i].DisplayName(), panelWidth-8))
		})
		lines = append(lines, items...)
	} else {
		scroll := m.navScroll
		rendered := 0
		prevAlbum := ""
		if scroll > 0 {
			prevAlbum = m.navTracks[scroll-1].Album
		}

		for i := scroll; i < len(m.navTracks) && rendered < maxVisible; i++ {
			t := m.navTracks[i]

			if album := t.Album; album != "" && album != prevAlbum {
				lines = append(lines, albumSeparator(album, t.Year))
				if rendered >= maxVisible {
					break
				}
			}
			prevAlbum = t.Album

			label := fmt.Sprintf("%d. %s", i+1, truncate(t.DisplayName(), panelWidth-8))
			lines = append(lines, cursorLine(label, i == m.navCursor))
			rendered++
		}

		lines = padLines(lines, maxVisible, rendered)
	}

	lines = append(lines, "", m.navCountLine("tracks", len(m.navTracks)))
	lines = append(lines, m.navSearchBar(
		helpKey("←↑↓→", "Navigate ")+
			helpKey("Enter", "Play ")+
			helpKey("q", "Queue ")+
			helpKey("R", "Replace ")+
			helpKey("a", "Append ")+
			helpKey("/", "Search"))...)

	if m.saveMsg != "" {
		lines = append(lines, "", statusStyle.Render(m.saveMsg))
	}

	return m.centerOverlay(strings.Join(lines, "\n"))
}
