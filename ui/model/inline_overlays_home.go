package model

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

// inline_overlays_home.go renders the Home overlay: the global header/help
// lines plus the internal two-pane body. The panes are joined horizontally
// inside the overlay body only — the global single-column layout and
// ui.PanelWidth stay untouched (this is the codebase's first two-list
// overlay).

// homeHeaderLine is the overlay header shown where the playlist header is:
// the `/` filter prompt, the new-playlist name prompt, or the sectioned
// position counter.
func (m Model) homeHeaderLine() string {
	if m.home.filtering || m.home.filter != "" {
		rows := m.homeRows()
		return m.filterCountHeader("home-filter", m.home.filter, fmt.Sprintf("%d/%d", len(rows), m.homeRowsTotal()))
	}
	if m.home.screen == homeScreenNewName {
		return m.promptHeader("home-new-name", "New Playlist", m.home.newName)
	}
	name := "Provider"
	if m.home.prov != nil {
		name = m.home.prov.Name()
	}
	rows := m.homeRows()
	return sepHeaderN("Home — "+name, min(m.home.cursor+1, max(1, len(rows))), len(rows))
}

// homeRowsTotal counts all selectable sidebar rows ignoring the filter (the
// denominator of the filter match count).
func (m Model) homeRowsTotal() int {
	n := len(m.home.lists) + len(m.home.albums) + len(m.home.artists)
	if _, ok := m.home.prov.(provider.PlaylistCreator); ok {
		n++
	}
	return n
}

func (m Model) homeHelpLine() string {
	if m.home.filtering {
		return m.commandHelp(commandModeHomeFilter)
	}
	if m.home.screen == homeScreenNewName {
		return m.commandHelp(commandModeHomeInput)
	}
	return m.commandHelp(commandModeHome)
}

// homeSidebarWidth picks the left pane's column budget: a third of the panel,
// clamped to a readable range, always leaving the content pane room.
func homeSidebarWidth(panelW int) int {
	w := min(max(panelW/3, 16), 44)
	if w > panelW-12 {
		w = max(8, panelW-12)
	}
	return w
}

// renderHomeBody renders the two-pane body into the playlist-region budget.
func (m Model) renderHomeBody() string {
	budget := m.effectivePlaylistVisible()
	if budget <= 0 {
		return ""
	}
	if m.home.screen == homeScreenNewName {
		return m.renderHomeNameBody(budget)
	}
	panelW := max(1, ui.PanelWidth)
	sideW := homeSidebarWidth(panelW)
	contW := max(4, panelW-sideW-1)
	sidebar := m.renderHomeSidebar(sideW, budget-1)
	content := m.renderHomeContent(contW, budget-1)
	return lipgloss.JoinHorizontal(lipgloss.Top, sidebar, " ", content)
}

// renderHomeSidebar renders the left pane: a focusable header line carrying
// the active ordering, then the sectioned list.
func (m Model) renderHomeSidebar(width, listBudget int) string {
	label := "Library · " + homeSortLabels[m.home.order]
	var head string
	if m.home.focus == homePaneSidebar || m.home.content.kind == homeContentNone {
		head = activeToggle.Render(truncate("▸ "+label, max(1, width)))
	} else {
		head = dimStyle.Render(truncate("  "+label, max(1, width)))
	}
	lines := m.homeSidebarLinesFrom(m.home.scroll, -1, width)
	return head + "\n" + strings.Join(fitLines(lines, listBudget), "\n")
}

// homeContentCrumb renders the content pane's breadcrumb, e.g.
// "Home / Playlists / Road Trips".
func (m Model) homeContentCrumb() string {
	section := "Albums"
	if m.home.content.kind == homeContentPlaylist {
		section = "Playlists"
	}
	return "Home / " + section + " / " + m.home.content.name
}

// renderHomeContent renders the right pane: a focusable breadcrumb line,
// then the track list (or its loading/empty states).
func (m Model) renderHomeContent(width, listBudget int) string {
	c := m.home.content
	var head string
	switch {
	case c.kind == homeContentNone:
		head = dimStyle.Render(truncate("  Home", max(1, width)))
	case m.home.focus == homePaneContent:
		head = activeToggle.Render(truncate("▸ "+m.homeContentCrumb(), max(1, width)))
	default:
		head = dimStyle.Render(truncate("  "+m.homeContentCrumb(), max(1, width)))
	}

	var lines []string
	switch {
	case c.kind == homeContentNone:
		lines = []string{
			dimStyle.Render("  Select a playlist, album, or artist"),
			dimStyle.Render("  from the library."),
		}
	case c.loading && len(c.tracks) == 0:
		lines = []string{loadingLine("Loading…")}
	case len(c.tracks) == 0:
		lines = []string{dimStyle.Render("  No tracks")}
	default:
		// windowList with a local width bound: track rows truncated to the
		// pane width instead of the global panel width.
		for i := c.scroll; i < len(c.tracks) && len(lines) < listBudget; i++ {
			t := c.tracks[i]
			lines = append(lines, cursorLine(truncate(fmt.Sprintf("%s - %s", t.Artist, t.Title), max(1, width-2)), i == c.cursor))
		}
		// Subtle indicator while more pages of a large playlist arrive.
		if c.paging.loading && len(lines) < listBudget {
			lines = append(lines, loadingLine("Loading more…"))
		}
		lines = padLines(lines, listBudget, len(lines))
	}
	return head + "\n" + strings.Join(fitLines(lines, listBudget), "\n")
}

// renderHomeNameBody renders the "+ New playlist" input screen body.
func (m Model) renderHomeNameBody(budget int) string {
	name := "the provider"
	if m.home.prov != nil {
		name = m.home.prov.Name()
	}
	lines := []string{dimStyle.Render("  Create a new playlist on " + name + ".")}
	if m.home.inputErr != "" {
		lines = append(lines, errorStyle.Render("  "+m.home.inputErr))
	}
	return bodyLines(lines, budget)
}
