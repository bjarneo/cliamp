package model

import (
	"fmt"
	"strings"

	"github.com/bjarneo/cliamp/ui"
)

// — artist screen (profile overlay) —

func (m Model) artistFollowed() bool {
	if m.artist.prov == nil {
		return false
	}
	return m.followState[followKey("artist", m.artist.prov.Name(), m.artist.info.ID)]
}

// artistInfoLine renders the one-line profile summary under the header:
// follower count, genres, and the session follow state. Empty when the
// provider supplied none of them.
func (m Model) artistInfoLine() string {
	var parts []string
	if m.artist.detail.Followers > 0 {
		parts = append(parts, fmt.Sprintf("%d followers", m.artist.detail.Followers))
	}
	if len(m.artist.detail.Genres) > 0 {
		parts = append(parts, strings.Join(m.artist.detail.Genres, ", "))
	}
	if m.artistFollowed() {
		parts = append(parts, "Following")
	}
	if len(parts) == 0 {
		return ""
	}
	return dimStyle.Render("  " + truncate(strings.Join(parts, " · "), max(1, ui.PanelWidth-2)))
}

func (m Model) artistHeaderLine() string {
	if len(m.artist.drill) > 0 {
		lvl := m.artist.drill[len(m.artist.drill)-1]
		return sepHeaderN("Artist — "+m.artist.info.Name, lvl.cursor+1, m.spotDrillCount(lvl))
	}
	return sepHeaderN("Artist — "+m.artist.info.Name, m.artist.cursor+1, len(m.artistRows()))
}

func (m Model) artistHelpLine() string {
	return m.commandHelp(commandModeArtist)
}

// artistSectionLabel names one section header; the Popular header carries the
// active sort so the `s` cycler state is always visible.
func (m Model) artistSectionLabel(kind artistRowKind) string {
	switch kind {
	case artistRowLiked:
		return "Liked Songs"
	case artistRowDiscography:
		return "Discography"
	default:
		return fmt.Sprintf("Popular (by %s)", artistSortLabels[m.artist.sort])
	}
}

// artistRowLabel renders one selectable row like the corresponding search
// drill rows.
func artistRowLabel(row artistRow) string {
	if row.kind == artistRowDiscography {
		if row.album.Year > 0 {
			return fmt.Sprintf("%s — %s (%d)", row.album.Name, row.album.Artist, row.album.Year)
		}
		return fmt.Sprintf("%s — %s", row.album.Name, row.album.Artist)
	}
	return fmt.Sprintf("%s - %s", row.track.Artist, row.track.Title)
}

// artistListVisible is the sectioned list's row budget: the playlist region
// minus the profile info line when one is rendered.
func (m Model) artistListVisible() int {
	budget := m.effectivePlaylistVisible()
	if m.artistInfoLine() != "" {
		budget--
	}
	return max(1, budget)
}

func (m Model) renderArtistBody() string {
	budget := m.effectivePlaylistVisible()
	if budget <= 0 {
		return ""
	}
	if len(m.artist.drill) > 0 {
		return m.renderArtistDrillBody(budget)
	}
	if m.artist.loading {
		return bodyLines([]string{loadingLine("Loading artist…")}, budget)
	}

	rows := m.artistRows()
	if len(rows) == 0 {
		return bodyMessage("No popular tracks or albums", budget)
	}

	info := m.artistInfoLine()
	listBudget := budget
	if info != "" {
		listBudget = budget - 1
	}

	var lines []string
	prev := artistRowKind(-1)
	if m.artist.scroll > 0 && m.artist.scroll <= len(rows) {
		prev = rows[m.artist.scroll-1].kind
	}
	for i := m.artist.scroll; i < len(rows) && len(lines) < listBudget; i++ {
		if rows[i].kind != prev {
			lines = append(lines, dimStyle.Render(labeledSeparator("  ", m.artistSectionLabel(rows[i].kind))))
			if len(lines) >= listBudget {
				break
			}
		}
		lines = append(lines, cursorLine(truncate(artistRowLabel(rows[i]), max(1, ui.PanelWidth-6)), i == m.artist.cursor))
		prev = rows[i].kind
	}
	list := strings.Join(padLines(lines, listBudget, len(lines)), "\n")
	if info != "" {
		return info + "\n" + list
	}
	return list
}

// renderArtistDrillBody renders the drill crumb plus the album's tracks,
// mirroring the search drill body.
func (m Model) renderArtistDrillBody(budget int) string {
	lvl := m.artist.drill[len(m.artist.drill)-1]
	crumb := dimStyle.Render("  " + truncate(m.artistDrillCrumb(), max(1, ui.PanelWidth-2)))
	if budget <= 1 {
		return crumb
	}
	var list string
	if lvl.loading && m.spotDrillCount(lvl) == 0 {
		list = bodyLines([]string{loadingLine("Loading…")}, budget-1)
	} else {
		items := make([]string, len(lvl.tracks))
		for i, t := range lvl.tracks {
			items[i] = fmt.Sprintf("%s - %s", t.Artist, t.Title)
		}
		list = windowList(items, lvl.cursor, lvl.scroll, budget-1)
	}
	return crumb + "\n" + list
}

// artistDrillCrumb renders the artist name plus the drill path, mirroring
// spotDrillCrumb.
func (m Model) artistDrillCrumb() string {
	parts := make([]string, 0, len(m.artist.drill)+1)
	parts = append(parts, "Artist — "+m.artist.info.Name)
	for _, lvl := range m.artist.drill {
		parts = append(parts, lvl.crumb)
	}
	return strings.Join(parts, " / ")
}
