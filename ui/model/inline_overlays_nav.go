package model

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/ui"
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

func (m Model) navHeaderLine() string {
	if m.navBrowser.confirmReplace {
		return sepHeader("Replace current queue?")
	}
	if m.navBrowser.searching {
		input := m.textWithCursor("nav-search", m.navBrowser.search)
		prefix := "  / "
		input = truncate(input, max(1, ui.PanelWidth-lipgloss.Width(prefix)-4))
		suffix := " / " + input
		pathWidth := max(1, ui.PanelWidth-lipgloss.Width(prefix)-lipgloss.Width(suffix))
		return playlistSelectedStyle.Render(prefix + truncate(m.navBreadcrumb(), pathWidth) + suffix)
	}
	switch m.navView() {
	case navViewArtists:
		return sepHeaderN(m.navBreadcrumb(), m.navBrowser.cursor+1, len(m.navBrowser.artists))
	case navViewAlbums:
		return sepHeaderN(m.navBreadcrumb(), m.navBrowser.cursor+1, len(m.navBrowser.albums))
	case navViewTracks:
		return sepHeaderN(m.navBreadcrumb(), m.navBrowser.cursor+1, len(m.navBrowser.tracks))
	default:
		return sepHeader(m.navBreadcrumb())
	}
}

func (m Model) navHelpLine() string {
	if m.navBrowser.searching {
		return m.commandHelp(commandModeNavSearch)
	}
	return m.commandHelp(commandModeNavBrowser)
}

func (m Model) renderNavBody() string {
	labels := m.navLabels()
	budget := m.effectivePlaylistVisible()
	if m.navBrowser.confirmReplace {
		return bodyMessage("Displayed tracks will replace the current queue. Enter confirms; Esc cancels.", budget)
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
			return truncate(fmt.Sprintf("%s (%d %s)", a.Name, a.AlbumCount, labels.albumsLower()), ui.PanelWidth-6)
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
			if a.Year > 0 {
				return truncate(fmt.Sprintf("%s — %s (%d)", a.Name, a.Artist, a.Year), ui.PanelWidth-6)
			}
			return truncate(fmt.Sprintf("%s — %s", a.Name, a.Artist), ui.PanelWidth-6)
		})
		return strings.Join(items, "\n")
	case navViewTracks:
		return m.renderNavTrackBody(budget)
	default:
		items := []string{"By " + labels.album, "By " + labels.artist, "By " + labels.artist + " / " + labels.album}
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

	if m.navBrowser.search != "" {
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
	if m.fileBrowser.confirmReplace {
		return sepHeader("Replace current queue?")
	}
	if m.fileBrowser.searching {
		return m.filterPromptHeader("file-browser-search", m.fileBrowser.search)
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
	if m.fileBrowser.confirmReplace {
		return bodyMessage("Selected files will replace the current queue. Enter confirms; Esc cancels.", budget)
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
