package ui

import (
	"fmt"
	"strings"

	"cliamp/theme"
)

func (m Model) renderKeymapOverlay() string {
	lines := []string{
		titleStyle.Render("K E Y M A P"),
		"",
	}

	if m.keymapSearch != "" {
		lines = append(lines, playlistSelectedStyle.Render("  / "+m.keymapSearch+"_"), "")
	} else {
		lines = append(lines, dimStyle.Render("  Type to filter…"), "")
	}

	entries := keymapEntries
	var visible []keymapEntry
	if m.keymapSearch != "" {
		for _, i := range m.keymapFiltered {
			visible = append(visible, entries[i])
		}
	} else {
		visible = entries
	}

	maxVisible := 12
	rendered := 0

	if len(visible) == 0 {
		lines = append(lines, dimStyle.Render("  No matches"))
		rendered = 1
	} else {
		scroll := scrollStart(m.keymapCursor, maxVisible)
		for i := scroll; i < len(visible) && i < scroll+maxVisible; i++ {
			line := fmt.Sprintf("%-10s %s", visible[i].key, visible[i].action)
			lines = append(lines, cursorLine(line, i == m.keymapCursor))
			rendered++
		}
	}

	lines = padLines(lines, maxVisible, rendered)
	lines = append(lines, "", dimStyle.Render(fmt.Sprintf("  %d/%d keys", len(visible), len(entries))))
	lines = append(lines, "", helpKey("↑↓", "Navigate ")+helpKey("Type", "Filter ")+helpKey("Esc", "Close"))

	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) renderThemePicker() string {
	lines := []string{
		titleStyle.Render("T H E M E S"),
		"",
	}

	count := len(m.themes) + 1
	maxVisible := 15
	scroll := scrollStart(m.themeCursor, maxVisible)

	for i := scroll; i < count && i < scroll+maxVisible; i++ {
		var name string
		if i == 0 {
			name = theme.DefaultName
		} else {
			name = m.themes[i-1].Name
		}
		lines = append(lines, cursorLine(name, i == m.themeCursor))
	}

	if count > maxVisible {
		lines = append(lines, "", dimStyle.Render(fmt.Sprintf("  %d/%d themes", m.themeCursor+1, count)))
	}

	lines = append(lines, "", helpKey("↑↓", "Navigate ")+helpKey("Enter", "Select ")+helpKey("Esc", "Cancel"))

	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) renderPlaylistManager() string {
	var lines []string
	switch m.plMgrScreen {
	case plMgrScreenList:
		lines = m.renderPlMgrList()
	case plMgrScreenTracks:
		lines = m.renderPlMgrTracks()
	case plMgrScreenNewName:
		lines = m.renderPlMgrNewName()
	}

	if m.saveMsg != "" {
		lines = append(lines, "", statusStyle.Render(m.saveMsg))
	}

	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) renderPlMgrList() []string {
	lines := []string{
		titleStyle.Render("P L A Y L I S T S"),
		"",
	}

	count := len(m.plMgrPlaylists) + 1 // +1 for "+ New Playlist..."
	maxVisible := 12
	scroll := scrollStart(m.plMgrCursor, maxVisible)

	for i := scroll; i < count && i < scroll+maxVisible; i++ {
		var label string
		if i < len(m.plMgrPlaylists) {
			pl := m.plMgrPlaylists[i]
			label = fmt.Sprintf("%s (%d tracks)", pl.Name, pl.TrackCount)
		} else {
			label = "+ New Playlist..."
		}

		if i == m.plMgrCursor {
			if m.plMgrConfirmDel && i < len(m.plMgrPlaylists) {
				lines = append(lines, playlistSelectedStyle.Render("> Delete \""+m.plMgrPlaylists[i].Name+"\"? [y/n]"))
			} else {
				lines = append(lines, playlistSelectedStyle.Render("> "+label))
			}
		} else {
			lines = append(lines, dimStyle.Render("  "+label))
		}
	}

	if count > maxVisible {
		lines = append(lines, "", dimStyle.Render(fmt.Sprintf("  %d/%d playlists", m.plMgrCursor+1, count)))
	}

	lines = append(lines, "", helpKey("↑↓", "Navigate ")+helpKey("Enter/→", "Open ")+helpKey("a", "Add track ")+helpKey("d", "Delete ")+helpKey("Esc", "Close"))

	return lines
}

func (m Model) renderPlMgrTracks() []string {
	title := fmt.Sprintf("P L A Y L I S T : %s", m.plMgrSelPlaylist)
	lines := []string{
		titleStyle.Render(title),
		"",
	}

	if len(m.plMgrTracks) == 0 {
		lines = append(lines, dimStyle.Render("  (empty)"))
		lines = append(lines, "", helpKey("a", "Add track ")+helpKey("Esc", "Back"))
		return lines
	}

	maxVisible := 12
	scroll := scrollStart(m.plMgrCursor, maxVisible)

	for i := scroll; i < len(m.plMgrTracks) && i < scroll+maxVisible; i++ {
		name := truncate(m.plMgrTracks[i].DisplayName(), panelWidth-8)
		label := fmt.Sprintf("%d. %s", i+1, name)
		lines = append(lines, cursorLine(label, i == m.plMgrCursor))
	}

	if len(m.plMgrTracks) > maxVisible {
		lines = append(lines, "", dimStyle.Render(fmt.Sprintf("  %d/%d tracks", m.plMgrCursor+1, len(m.plMgrTracks))))
	}

	lines = append(lines, "", helpKey("↑↓", "Navigate ")+helpKey("Enter", "Play all ")+helpKey("a", "Add track ")+helpKey("d", "Remove ")+helpKey("Esc", "Back"))

	return lines
}

func (m Model) renderPlMgrNewName() []string {
	lines := []string{
		titleStyle.Render("N E W  P L A Y L I S T"),
		"",
		dimStyle.Render("  Playlist name:"),
		playlistSelectedStyle.Render("  " + m.plMgrNewName + "_"),
		"",
		helpKey("Enter", "Create & add track ") + helpKey("Esc", "Cancel"),
	}
	return lines
}

func (m Model) renderQueueOverlay() string {
	lines := []string{
		titleStyle.Render("Q U E U E"),
		"",
	}

	tracks := m.playlist.QueueTracks()
	maxVisible := 12
	rendered := 0

	if len(tracks) == 0 {
		lines = append(lines, dimStyle.Render("  (empty)"))
		rendered = 1
	} else {
		scroll := scrollStart(m.queueCursor, maxVisible)
		for i := scroll; i < len(tracks) && i < scroll+maxVisible; i++ {
			name := truncate(tracks[i].DisplayName(), panelWidth-8)
			label := fmt.Sprintf("%d. %s", i+1, name)
			lines = append(lines, cursorLine(label, i == m.queueCursor))
			rendered++
		}
	}

	lines = padLines(lines, maxVisible, rendered)
	lines = append(lines, "", dimStyle.Render(fmt.Sprintf("  %d queued", len(tracks))))
	lines = append(lines, "", helpKey("↑↓", "Navigate ")+helpKey("d", "Remove ")+helpKey("c", "Clear ")+helpKey("Esc", "Close"))

	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) renderInfoOverlay() string {
	track, _ := m.playlist.Current()

	lines := []string{
		titleStyle.Render("T R A C K  I N F O"),
		"",
	}

	field := func(label, value string) {
		if value != "" {
			lines = append(lines, dimStyle.Render("  "+label+": ")+trackStyle.Render(value))
		}
	}

	field("Title", track.Title)
	field("Artist", track.Artist)
	field("Album", track.Album)
	field("Genre", track.Genre)
	if track.Year != 0 {
		field("Year", fmt.Sprintf("%d", track.Year))
	}
	if track.TrackNumber != 0 {
		field("Track", fmt.Sprintf("%d", track.TrackNumber))
	}
	field("Path", track.Path)

	lines = append(lines, "", helpKey("Esc/i", "Close"))

	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) renderSearchOverlay() string {
	lines := []string{
		titleStyle.Render("S E A R C H"),
		"",
		playlistSelectedStyle.Render("  / " + m.searchQuery + "_"),
		"",
	}

	tracks := m.playlist.Tracks()
	maxVisible := 12
	rendered := 0

	if len(m.searchResults) == 0 {
		if m.searchQuery != "" {
			lines = append(lines, dimStyle.Render("  No matches"))
		} else {
			lines = append(lines, dimStyle.Render("  Type to search…"))
		}
		rendered = 1
	} else {
		currentIdx := m.playlist.Index()
		scroll := scrollStart(m.searchCursor, maxVisible)

		for j := scroll; j < scroll+maxVisible && j < len(m.searchResults); j++ {
			i := m.searchResults[j]
			prefix := "  "
			style := dimStyle

			if i == currentIdx && m.player.IsPlaying() {
				prefix = "▶ "
				style = playlistActiveStyle
			}

			if j == m.searchCursor {
				style = playlistSelectedStyle
			}

			name := tracks[i].DisplayName()
			queueSuffix := ""
			if qp := m.playlist.QueuePosition(i); qp > 0 {
				queueSuffix = fmt.Sprintf(" [Q%d]", qp)
			}
			name = truncate(name, panelWidth-8-len([]rune(queueSuffix)))

			line := fmt.Sprintf("%s%d. %s", prefix, i+1, name)
			if queueSuffix != "" {
				lines = append(lines, style.Render(line)+activeToggle.Render(queueSuffix))
			} else {
				lines = append(lines, style.Render(line))
			}
			rendered++
		}
	}

	lines = padLines(lines, maxVisible, rendered)
	lines = append(lines, "", dimStyle.Render(fmt.Sprintf("  %d found", len(m.searchResults))))
	lines = append(lines, "", helpKey("↑↓", "Navigate ")+helpKey("Enter", "Play ")+helpKey("Tab", "Queue ")+helpKey("Ctrl+K", "Keymap ")+helpKey("Esc", "Close"))

	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) renderNetSearchOverlay() string {
	lines := []string{
		titleStyle.Render("F I N D   O N L I N E"),
		"",
		playlistSelectedStyle.Render("  Search: " + m.netSearchQuery + "_"),
		"",
		helpKey("Enter", "Search & Queue ") + helpKey("Esc", "Cancel"),
	}
	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) renderLyricsOverlay() string {
	lines := []string{
		titleStyle.Render("S Y N C E D   L Y R I C S"),
		"",
	}

	if m.lyricsLoading {
		lines = append(lines, dimStyle.Render("  Fetching lyrics from lrclib.net..."))
	} else if m.lyricsErr != nil {
		lines = append(lines, helpStyle.Render("  Error: "+m.lyricsErr.Error()))
	} else if len(m.lyricsLines) == 0 {
		lines = append(lines, dimStyle.Render("  No lyrics available for this track."))
	} else {
		pos := m.player.Position()
		activeIdx := -1
		for i, line := range m.lyricsLines {
			if line.Start <= pos {
				activeIdx = i
			} else {
				break
			}
		}

		startIdx := activeIdx - 4
		if startIdx < 0 {
			startIdx = 0
		}
		endIdx := activeIdx + 7
		if endIdx > len(m.lyricsLines) {
			endIdx = len(m.lyricsLines)
		}

		for i := startIdx; i < endIdx; i++ {
			text := m.lyricsLines[i].Text
			if text == "" {
				text = "♪"
			}
			if i == activeIdx {
				lines = append(lines, playlistSelectedStyle.Render("  "+text))
			} else {
				lines = append(lines, dimStyle.Render("  "+text))
			}
		}
	}

	for len(lines) < 14 {
		lines = append(lines, "")
	}

	lines = append(lines, "", helpKey("y", "Close Lyric View"))
	return m.centerOverlay(strings.Join(lines, "\n"))
}
