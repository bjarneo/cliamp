package model

import (
	"fmt"
	"strings"

	"cliamp/provider"
	"cliamp/ui"
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
	title := "B R O W S E"
	if m.navBrowser.prov != nil {
		title = spacedTitle(m.navBrowser.prov.Name())
	}
	lines := []string{
		titleStyle.Render(title),
		"",
	}

	items := []string{"By Album", "By Artist", "By Artist / Album"}
	for i, item := range items {
		lines = append(lines, cursorLine(item, i == m.navBrowser.cursor))
	}

	lines = append(lines, "",
		helpKey("↓↑", "Scroll ")+helpKey("Enter", "Select ")+helpKey("Esc", "Close"))

	return lines
}

func (m Model) renderNavArtistList() []string {
	lines := []string{titleStyle.Render("A R T I S T S"), ""}
	lines = append(lines, filterHeader(m.navBrowser.searching, m.navBrowser.search, helpKey("/", "Clear"))...)

	if m.navBrowser.loading && len(m.navBrowser.artists) == 0 {
		lines = append(lines, loadingLine("Loading artists…"), "", helpKey("Esc", "Back"))
		return lines
	}

	if len(m.navBrowser.artists) == 0 {
		lines = append(lines, dimStyle.Render("  No artists found."), "", helpKey("Esc", "Back"))
		return lines
	}

	items := m.navScrollItems(len(m.navBrowser.artists), func(i int) string {
		a := m.navBrowser.artists[i]
		return truncate(fmt.Sprintf("%s (%d albums)", a.Name, a.AlbumCount), ui.PanelWidth-6)
	})
	lines = append(lines, items...)

	rendered := min(len(m.navBrowser.artists)-m.navBrowser.scroll, max(m.plVisible, 5))
	if rendered < 0 {
		rendered = 0
	}
	footerCount := fmt.Sprintf("%d/%d", rendered, len(m.navBrowser.artists))
	if m.navBrowser.searching && m.navBrowser.search != "" {
		footerCount = fmt.Sprintf("%d/%d", len(m.navBrowser.artists), len(m.navBrowser.artists))
	}
	lines = append(lines, "", dimStyle.Render(fmt.Sprintf("  %s artists", footerCount)),
		"", helpKey("←↓↑→", "Navigate ")+helpKey("Enter", "Open ")+helpKey("/", "Search"))

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
	lines = append(lines, filterHeader(m.navBrowser.searching, m.navBrowser.search, helpKey("/", "Clear"))...)

	if !artistAlbums {
		sortLabel := m.navSortLabel(m.navBrowser.sortType)
		lines = append(lines, dimStyle.Render("  Sort: ")+activeToggle.Render(sortLabel), "")
	}

	if m.navBrowser.loading && len(m.navBrowser.albums) == 0 {
		lines = append(lines, loadingLine("Loading albums…"))
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
		return truncate(label, ui.PanelWidth-6)
	})
	lines = append(lines, items...)

	if m.navBrowser.albumLoading {
		lines = append(lines, loadingLine("Loading more…"))
	} else {
		rendered := min(len(m.navBrowser.albums)-m.navBrowser.scroll, max(m.plVisible, 5))
		if rendered < 0 {
			rendered = 0
		}
		footerCount := fmt.Sprintf("%d/%d", rendered, len(m.navBrowser.albums))
		if m.navBrowser.searching && m.navBrowser.search != "" {
			footerCount = fmt.Sprintf("%d/%d", len(m.navBrowser.albums), len(m.navBrowser.albums))
		}
		lines = append(lines, dimStyle.Render(fmt.Sprintf("  %s albums", footerCount)))
	}

	help := helpKey("←↓↑→", "Navigate ") + helpKey("Enter", "Open ")
	if !artistAlbums {
		help += helpKey("s", "Sort ")
	}
	help += helpKey("/", "Search")
	lines = append(lines, "", help)

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
	lines = append(lines, filterHeader(m.navBrowser.searching, m.navBrowser.search, helpKey("/", "Clear"))...)

	if m.navBrowser.loading && len(m.navBrowser.tracks) == 0 {
		lines = append(lines, loadingLine("Loading tracks…"), "", helpKey("Esc", "Back"))
		return lines
	}

	if len(m.navBrowser.tracks) == 0 {
		lines = append(lines, dimStyle.Render("  No tracks found."), "", helpKey("Esc", "Back"))
		return lines
	}

	if subtitle := tracksSubtitle(m.navBrowser.tracks); subtitle != "" {
		lines = append(lines, dimStyle.Render("  "+subtitle), "")
	}

	maxVisible := max(m.plVisible, 5)

	useFilter := len(m.navBrowser.searchIdx) > 0 || m.navBrowser.search != ""

	rendered := 0
	if useFilter {
		items := m.navScrollItems(len(m.navBrowser.tracks), func(i int) string {
			t := m.navBrowser.tracks[i]
			return formatTrackRow(i+1, t.DisplayName()+trackAlbumSuffix(t, m.showAlbumHeaders), t.DurationSecs)
		})
		lines = append(lines, items...)
		rendered = min(len(m.navBrowser.tracks)-m.navBrowser.scroll, max(m.plVisible, 5))
		if rendered < 0 {
			rendered = 0
		}
	} else {
		scroll := m.navBrowser.scroll

		for row := range m.playlistRows(m.navBrowser.tracks, scroll, m.showAlbumHeaders) {
			if row.Index < 0 {
				if rendered+1 >= maxVisible {
					break
				}
				lines = append(lines, m.albumSeparator(row.Album, row.Year))
				rendered++
				continue
			}

			if rendered >= maxVisible {
				break
			}

			i, t := row.Index, row.Track
			label := formatTrackRow(i+1, t.DisplayName()+trackAlbumSuffix(t, m.showAlbumHeaders), t.DurationSecs)
			lines = append(lines, cursorLine(label, i == m.navBrowser.cursor))
			rendered++
		}

		lines = padLines(lines, maxVisible, rendered)
	}

	footerCount := fmt.Sprintf("%d/%d", rendered, len(m.navBrowser.tracks))
	if m.navBrowser.searching && m.navBrowser.search != "" {
		footerCount = fmt.Sprintf("%d/%d", len(m.navBrowser.tracks), len(m.navBrowser.tracks))
	}
	lines = append(lines, "", dimStyle.Render(fmt.Sprintf("  %s tracks", footerCount)),
		"", helpKey("←↓↑→", "Navigate ")+
			helpKey("Enter", "Play from here ")+
			helpKey("q", "Queue this ")+
			helpKey("R", "Replace queue ")+
			helpKey("a", "Append all ")+
			helpKey("/", "Search"))
	return lines
}

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

func spacedTitle(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "B R O W S E"
	}
	runes := []rune(strings.ToUpper(s))
	parts := make([]string, 0, len(runes))
	for _, r := range runes {
		parts = append(parts, string(r))
	}
	return strings.Join(parts, " ")
}
