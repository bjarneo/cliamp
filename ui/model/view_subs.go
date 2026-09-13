package model

import (
	"fmt"

	"github.com/bjarneo/cliamp/ui"
)

// subsHeaderLine renders the overlay header: the filter prompt while filtering,
// otherwise the show count.
func (m *Model) subsHeaderLine() string {
	if m.subs.filtering {
		return m.filterCountHeader("subs-filter", m.subs.filter, fmt.Sprintf("%d shows", len(m.subsVisibleShows())))
	}
	return sepHeaderN("Subscriptions", m.subs.cursor+1, len(m.subsVisibleShows()))
}

func (m *Model) subsHelpLine() string {
	if m.subs.filtering {
		return m.commandHelp(commandModeSubsFilter)
	}
	return m.commandHelp(commandModeSubs)
}

// renderSubsBody lists the subscribed shows, with any loading or error line
// pinned to the top so a slow feed fetch is visible while the list stays put.
func (m Model) renderSubsBody() string {
	budget := m.effectivePlaylistVisible()
	if budget <= 0 {
		return ""
	}

	var notice string
	switch {
	case m.subs.loading && m.subs.status != "":
		notice = activeToggle.Render("  "+spinnerFrame()) + dimStyle.Render(" "+m.subs.status)
	case m.subs.err != "":
		notice = playlistUnavailableStyle.Render("  " + truncate(m.subs.err, ui.PanelWidth-2))
	}
	if notice != "" {
		budget--
	}

	visible := m.subsVisibleShows()
	var body string
	if len(visible) == 0 {
		body = bodyMessage("(no shows match)", budget)
	} else {
		items := make([]string, len(visible))
		for i, idx := range visible {
			show := m.subs.shows[idx]
			label := show.Name
			if show.Author != "" {
				label += " · " + show.Author
			}
			// cursorLine styles the whole row, so keep this plain: truncate
			// counts characters and would cut an escape sequence in half.
			items[i] = truncate(label, ui.PanelWidth-4)
		}
		body = windowList(items, m.subs.cursor, m.subs.scroll, budget)
	}
	if notice == "" {
		return body
	}
	return notice + "\n" + body
}
