package model

import (
	"fmt"
)

// subsHeaderLine renders the overlay header: the filter prompt while filtering,
// otherwise the show count.
func (m *Model) subsHeaderLine() string {
	if m.subs.filtering {
		return m.filterHeader("Filter: Shows", "subs-filter", m.subs.filter, fmt.Sprintf("%d shows", len(m.subsVisibleShows())))
	}
	return sepHeaderN("Subscriptions", m.subs.cursor+1, len(m.subsVisibleShows()), m.layout.panelWidth)
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
		notice = activeToggle.Render("  "+spinnerFrame()) + dimStyle.Render(" "+terminalText(m.subs.status))
	case m.subs.err != "":
		notice = playlistUnavailableStyle.Render("  " + truncate(terminalText(m.subs.err), m.layout.panelWidth-2))
	}
	if notice != "" {
		budget--
		if budget <= 0 {
			// The notice is the only row there is room for.
			return notice
		}
	}

	visible := m.subsVisibleShows()
	var body string
	if len(visible) == 0 {
		body = bodyMessage("(no shows match)", budget)
	} else {
		body = windowListFunc(len(visible), m.subs.cursor, m.subs.scroll, budget, func(i int) string {
			show := m.subs.shows[visible[i]]
			label := show.Name
			if show.Author != "" {
				label += " · " + show.Author
			}
			label = terminalText(label)
			// cursorLine styles the whole row, so keep this plain.
			return truncate(label, m.layout.panelWidth-4)
		})
	}
	if notice == "" {
		return body
	}
	return notice + "\n" + body
}
