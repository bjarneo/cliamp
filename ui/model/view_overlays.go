package model

import (
	"fmt"
	"strings"

	"cliamp/history"
	"cliamp/ui"
)

// renderVisPickerList renders the visualizer mode list for the playlist region
// while the picker is open. The header and help line are supplied by the main
// layout (renderPlaylistHeader / renderHelp).
func (m Model) renderVisPickerList() string {
	budget := m.effectivePlaylistVisible()
	if budget <= 0 {
		return ""
	}
	items := m.visPicker.modes
	scroll := m.visPicker.scroll

	lines := make([]string, 0, budget)
	for i := scroll; i < len(items) && len(lines) < budget; i++ {
		lines = append(lines, cursorLine(items[i], i == m.visPicker.cursor))
	}
	return strings.Join(lines, "\n")
}

// — playlist manager (inline) —

func (m Model) plMgrHeaderLine() string {
	if m.plManager.filtering {
		return filterPromptHeader(m.plManager.filter)
	}
	switch m.plManager.screen {
	case plMgrScreenTracks:
		return sepHeaderN("Playlist: "+m.plManager.selPlaylist, m.plManager.cursor+1, len(m.plManager.tracks))
	case plMgrScreenNewName:
		return promptHeader("New Playlist", m.plManager.newName)
	case plMgrScreenRename:
		return promptHeader("Rename "+m.plManager.renameOldName, m.plManager.renameName)
	default:
		total := len(m.plManager.playlists)
		if m.plManager.filter != "" {
			total = len(m.plManager.filtered)
		}
		return sepHeaderN("Playlists", m.plManager.cursor+1, total)
	}
}

func (m Model) plMgrHelpLine() string {
	switch m.plManager.screen {
	case plMgrScreenTracks:
		return m.plMgrTracksHelpLine()
	case plMgrScreenNewName:
		return helpKey("Enter", "Create & add ") + helpKey("Esc", "Cancel")
	case plMgrScreenRename:
		return helpKey("Enter", "Rename ") + helpKey("Esc", "Cancel")
	default:
		return m.plMgrListHelpLine()
	}
}

func (m Model) renderPlMgrBody() string {
	switch m.plManager.screen {
	case plMgrScreenTracks:
		return m.renderPlMgrTracksBody()
	case plMgrScreenNewName, plMgrScreenRename:
		return m.renderPlMgrFormBody()
	default:
		return m.renderPlMgrListBody()
	}
}

func (m Model) renderPlMgrFormBody() string {
	budget := m.effectivePlaylistVisible()
	if m.plManager.screen == plMgrScreenRename {
		return bodyMessage("Enter a new name for the playlist above.", budget)
	}
	label := "Create the playlist (nothing playing to add)."
	if track, idx := m.playlist.Current(); idx >= 0 && track.Path != "" {
		label = "Create & add: " + truncate(track.DisplayName(), max(1, ui.PanelWidth-16))
	}
	return bodyMessage(label, budget)
}

func (m Model) renderPlMgrListBody() string {
	budget := m.effectivePlaylistVisible()

	visibleN := len(m.plManager.playlists)
	if m.plManager.filter != "" {
		visibleN = len(m.plManager.filtered)
	}

	// Empty state: no playlists at all.
	if len(m.plManager.playlists) == 0 {
		return bodyLines([]string{
			dimStyle.Render("  No playlists yet."),
			dimStyle.Render("  Press Enter on \"+ New Playlist…\" below,"),
			dimStyle.Render("  or `a` to save the now-playing track."),
			"",
			playlistSelectedStyle.Render("> + New Playlist..."),
		}, budget)
	}

	// Filtered with no matches: still allow "+ New Playlist..."
	if m.plManager.filter != "" && visibleN == 0 {
		newLabel := "+ New Playlist \"" + m.plManager.filter + "\"..."
		return bodyLines([]string{
			dimStyle.Render(fmt.Sprintf("  No playlists match %q", m.plManager.filter)),
			cursorLine(newLabel, m.plManager.cursor == 0),
		}, budget)
	}

	type plRow struct {
		label   string
		realIdx int // -1 for "New" or spacer
		viewIdx int // logical index for cursor comparison
		spacer  bool
	}

	var rows []plRow
	foundUser := false
	for i := 0; i < visibleN; i++ {
		idx := m.plMgrPlaylistRealIndex(i)
		p := m.plManager.playlists[idx]

		if p.Name != history.PlaylistName {
			if !foundUser && i > 0 {
				rows = append(rows, plRow{spacer: true, viewIdx: -1})
			}
			foundUser = true
		}

		rows = append(rows, plRow{
			label:   playlistLabel("", p),
			realIdx: idx,
			viewIdx: i,
		})
	}

	if visibleN > 0 {
		rows = append(rows, plRow{spacer: true, viewIdx: -1})
	}

	newLabel := "+ New Playlist..."
	if m.plManager.filter != "" {
		newLabel = "+ New Playlist \"" + m.plManager.filter + "\"..."
	}
	rows = append(rows, plRow{label: newLabel, realIdx: -1, viewIdx: visibleN})

	// Map the logical scroll position to our row index.
	startIndex := 0
	for i, r := range rows {
		if r.viewIdx == m.plManager.scroll {
			startIndex = i
			break
		}
	}

	lines := make([]string, 0, budget)
	for i := startIndex; i < len(rows) && len(lines) < budget; i++ {
		r := rows[i]
		if r.spacer {
			lines = append(lines, "")
			continue
		}
		if r.viewIdx == m.plManager.cursor {
			if m.plManager.confirmDel && r.realIdx >= 0 {
				lines = append(lines, playlistSelectedStyle.Render("> Delete \""+m.plManager.playlists[r.realIdx].Name+"\"? [y/n]"))
			} else {
				lines = append(lines, playlistSelectedStyle.Render("> "+r.label))
			}
		} else {
			lines = append(lines, dimStyle.Render("  "+r.label))
		}
	}
	return bodyLines(lines, budget)
}

func (m Model) renderPlMgrTracksBody() string {
	budget := m.effectivePlaylistVisible()

	if len(m.plManager.tracks) == 0 {
		return bodyLines([]string{
			dimStyle.Render("  This playlist is empty."),
			dimStyle.Render("  Press `a` to add the now-playing track."),
		}, budget)
	}

	visibleN := len(m.plManager.tracks)
	if m.plManager.filter != "" {
		visibleN = len(m.plManager.filtered)
		if visibleN == 0 {
			return bodyMessage(fmt.Sprintf("No tracks match %q", m.plManager.filter), budget)
		}
	}

	scroll := m.plManager.scroll

	if m.plManager.filter != "" {
		lines := make([]string, 0, budget)
		for i := scroll; i < visibleN && len(lines) < budget; i++ {
			realIdx := m.plMgrTrackRealIndex(i)
			t := m.plManager.tracks[realIdx]
			label := formatTrackRow(realIdx+1, t.DisplayName()+trackAlbumSuffix(t, m.showAlbumHeaders), t.DurationSecs)
			lines = append(lines, cursorLine(label, i == m.plManager.cursor))
		}
		return bodyLines(lines, budget)
	}

	return m.renderTrackRowsBody(m.plManager.tracks, m.plManager.cursor, scroll, budget)
}

// renderSearchList renders the playlist-search results for the playlist region
// while search is active. The query prompt and help line are supplied by the
// main layout (renderPlaylistHeader / renderHelp), mirroring renderVisPickerList.
func (m Model) renderSearchList() string {
	budget := m.effectivePlaylistVisible()
	if budget <= 0 {
		return ""
	}

	if len(m.search.results) == 0 {
		msg := "Type to search…"
		if m.search.query != "" {
			msg = "No matches"
		}
		return strings.Join(fitLines([]string{dimStyle.Render("  " + msg)}, budget), "\n")
	}

	tracks := m.playlist.Tracks()
	currentIdx := m.playlist.Index()
	isPlaying := m.player.IsPlaying()
	scroll := m.search.scroll

	lines := make([]string, 0, budget)
	for j := scroll; j < len(m.search.results) && len(lines) < budget; j++ {
		i := m.search.results[j]
		prefix := "  "
		style := dimStyle
		if i == currentIdx && isPlaying {
			prefix = "▶ "
			style = playlistActiveStyle
		}

		name := tracks[i].DisplayName()
		queueSuffix := ""
		if qp := m.playlist.QueuePosition(i); qp > 0 {
			queueSuffix = fmt.Sprintf(" [Q%d]", qp)
		}
		name = truncate(name, ui.PanelWidth-8-len([]rune(queueSuffix)))

		line := fmt.Sprintf("%s%d. %s", prefix, i+1, name)
		item := style.Render(line)
		if queueSuffix != "" {
			item += activeToggle.Render(queueSuffix)
		}
		// cursorLine adds the "> "/"  " prefix and selected styling.
		lines = append(lines, cursorLine(item, j == m.search.cursor))
	}
	return strings.Join(lines, "\n")
}
