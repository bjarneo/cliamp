package model

import (
	"errors"
	"fmt"
	"strings"

	"cliamp/lyrics"
	"cliamp/theme"
	"cliamp/ui"
)

func (m Model) renderDeviceOverlay() string {
	lines := []string{
		titleStyle.Render("A U D I O  D E V I C E S"),
		"",
	}

	if m.devicePicker.loading {
		lines = append(lines, dimStyle.Render("  Loading devices..."))
		lines = append(lines, "", helpKey("Esc", "Cancel"))
		return m.centerOverlay(strings.Join(lines, "\n"))
	}

	if len(m.devicePicker.devices) == 0 {
		lines = append(lines, dimStyle.Render("  No audio output devices found."))
		lines = append(lines, "", helpKey("Esc", "Close"))
		return m.centerOverlay(strings.Join(lines, "\n"))
	}

	maxVisible := 12
	scroll := scrollStart(m.devicePicker.cursor, maxVisible)
	rendered := 0

	for i := scroll; i < len(m.devicePicker.devices) && i < scroll+maxVisible; i++ {
		d := m.devicePicker.devices[i]
		label := d.Description
		if label == "" {
			label = d.Name
		}
		suffix := ""
		if d.Active {
			suffix = " " + activeToggle.Render("●")
		}
		if i == m.devicePicker.cursor {
			lines = append(lines, playlistSelectedStyle.Render("> "+label)+suffix)
		} else {
			lines = append(lines, dimStyle.Render("  "+label)+suffix)
		}
		rendered++
	}

	lines = padLines(lines, maxVisible, rendered)

	if len(m.devicePicker.devices) > maxVisible {
		lines = append(lines, "", dimStyle.Render(fmt.Sprintf("  %d/%d devices", m.devicePicker.cursor+1, len(m.devicePicker.devices))))
	}

	lines = append(lines, "", helpKey("↑↓", "Navigate ")+helpKey("Enter", "Select ")+helpKey("Esc", "Cancel"))
	return m.centerOverlay(strings.Join(lines, "\n"))
}



func (m Model) renderThemePicker() string {
	lines := []string{
		titleStyle.Render("T H E M E S"),
		"",
	}

	count := len(m.themes) + 1
	maxVisible := 15
	scroll := scrollStart(m.themePicker.cursor, maxVisible)

	for i := scroll; i < count && i < scroll+maxVisible; i++ {
		var name string
		if i == 0 {
			name = theme.DefaultName
		} else {
			name = m.themes[i-1].Name
		}
		lines = append(lines, cursorLine(name, i == m.themePicker.cursor))
	}

	if count > maxVisible {
		lines = append(lines, "", dimStyle.Render(fmt.Sprintf("  %d/%d themes", m.themePicker.cursor+1, count)))
	}

	lines = append(lines, "", helpKey("↑↓", "Navigate ")+helpKey("Enter", "Select ")+helpKey("Esc", "Cancel"))

	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) renderPlaylistManager() string {
	var lines []string
	switch m.plManager.screen {
	case plMgrScreenList:
		lines = m.renderPlMgrList()
	case plMgrScreenTracks:
		lines = m.renderPlMgrTracks()
	case plMgrScreenNewName:
		lines = m.renderPlMgrNewName()
	}
	return m.centerOverlay(strings.Join(m.appendFooterMessages(lines), "\n"))
}

func (m Model) renderPlMgrList() []string {
	lines := []string{
		titleStyle.Render("P L A Y L I S T S"),
		"",
	}

	count := len(m.plManager.playlists) + 1 // +1 for "+ New Playlist..."
	maxVisible := 12
	scroll := scrollStart(m.plManager.cursor, maxVisible)

	for i := scroll; i < count && i < scroll+maxVisible; i++ {
		var label string
		if i < len(m.plManager.playlists) {
			pl := m.plManager.playlists[i]
			label = playlistLabel("", pl)
		} else {
			label = "+ New Playlist..."
		}

		if i == m.plManager.cursor {
			if m.plManager.confirmDel && i < len(m.plManager.playlists) {
				lines = append(lines, playlistSelectedStyle.Render("> Delete \""+m.plManager.playlists[i].Name+"\"? [y/n]"))
			} else {
				lines = append(lines, playlistSelectedStyle.Render("> "+label))
			}
		} else {
			lines = append(lines, dimStyle.Render("  "+label))
		}
	}

	if count > maxVisible {
		lines = append(lines, "", dimStyle.Render(fmt.Sprintf("  %d/%d playlists", m.plManager.cursor+1, count)))
	}

	lines = append(lines, "", helpKey("↑↓", "Navigate ")+helpKey("Enter/→", "Open ")+helpKey("a", "Add track ")+helpKey("d", "Delete ")+helpKey("Esc", "Close"))

	return lines
}

func (m Model) renderPlMgrTracks() []string {
	title := fmt.Sprintf("P L A Y L I S T : %s", m.plManager.selPlaylist)
	lines := []string{
		titleStyle.Render(title),
		"",
	}

	if len(m.plManager.tracks) == 0 {
		lines = append(lines, dimStyle.Render("  (empty)"))
		lines = append(lines, "", helpKey("a", "Add track ")+helpKey("Esc", "Back"))
		return lines
	}

	maxVisible := 12
	scroll := scrollStart(m.plManager.cursor, maxVisible)

	for i := scroll; i < len(m.plManager.tracks) && i < scroll+maxVisible; i++ {
		name := truncate(m.plManager.tracks[i].DisplayName(), ui.PanelWidth-8)
		label := fmt.Sprintf("%d. %s", i+1, name)
		lines = append(lines, cursorLine(label, i == m.plManager.cursor))
	}

	if len(m.plManager.tracks) > maxVisible {
		lines = append(lines, "", dimStyle.Render(fmt.Sprintf("  %d/%d tracks", m.plManager.cursor+1, len(m.plManager.tracks))))
	}

	lines = append(lines, "", helpKey("↑↓", "Navigate ")+helpKey("Enter", "Play all ")+helpKey("a", "Add track ")+helpKey("d", "Remove ")+helpKey("Esc", "Back"))

	return lines
}

func (m Model) renderPlMgrNewName() []string {
	lines := []string{
		titleStyle.Render("N E W  P L A Y L I S T"),
		"",
		dimStyle.Render("  Playlist name:"),
		playlistSelectedStyle.Render("  " + m.plManager.newName + "_"),
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
		scroll := scrollStart(m.queue.cursor, maxVisible)
		for i := scroll; i < len(tracks) && i < scroll+maxVisible; i++ {
			name := truncate(tracks[i].DisplayName(), ui.PanelWidth-8)
			label := fmt.Sprintf("%d. %s", i+1, name)
			lines = append(lines, cursorLine(label, i == m.queue.cursor))
			rendered++
		}
	}

	lines = padLines(lines, maxVisible, rendered)
	lines = append(lines, "", dimStyle.Render(fmt.Sprintf("  %d queued", len(tracks))))
	lines = append(lines, "", helpKey("↑↓", "Navigate ")+helpKey("Shift+↑↓", "Reorder ")+helpKey("d", "Remove ")+helpKey("c", "Clear ")+helpKey("Esc", "Close"))

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
		playlistSelectedStyle.Render("  / " + m.search.query + "_"),
		"",
	}

	tracks := m.playlist.Tracks()
	maxVisible := 12
	rendered := 0

	if len(m.search.results) == 0 {
		if m.search.query != "" {
			lines = append(lines, dimStyle.Render("  No matches"))
		} else {
			lines = append(lines, dimStyle.Render("  Type to search…"))
		}
		rendered = 1
	} else {
		currentIdx := m.playlist.Index()
		scroll := scrollStart(m.search.cursor, maxVisible)

		for j := scroll; j < scroll+maxVisible && j < len(m.search.results); j++ {
			i := m.search.results[j]
			prefix := "  "
			style := dimStyle

			if i == currentIdx && m.player.IsPlaying() {
				prefix = "▶ "
				style = playlistActiveStyle
			}

			if j == m.search.cursor {
				style = playlistSelectedStyle
			}

			name := tracks[i].DisplayName()
			queueSuffix := ""
			if qp := m.playlist.QueuePosition(i); qp > 0 {
				queueSuffix = fmt.Sprintf(" [Q%d]", qp)
			}
			name = truncate(name, ui.PanelWidth-8-len([]rune(queueSuffix)))

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
	lines = append(lines, "", dimStyle.Render(fmt.Sprintf("  %d found", len(m.search.results))))
	lines = append(lines, "", helpKey("↑↓", "Navigate ")+helpKey("Enter", "Play ")+helpKey("Tab", "Queue ")+helpKey("Ctrl+K", "Keymap ")+helpKey("Esc", "Close"))

	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) renderNetSearchOverlay() string {
	lines := []string{
		titleStyle.Render("F I N D   O N L I N E"),
		"",
		playlistSelectedStyle.Render("  Search: " + m.netSearch.query + "_"),
		"",
		helpKey("Enter", "Search & Queue ") + helpKey("Ctrl+K", "Keys ") + helpKey("Esc", "Cancel"),
	}
	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) renderURLInputOverlay() string {
	lines := []string{
		titleStyle.Render("L O A D   U R L"),
		"",
		playlistSelectedStyle.Render("  URL: " + m.urlInput + "_"),
		"",
		helpKey("Enter", "Load") + " " + helpKey("Esc", "Cancel"),
	}
	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) renderLyricsOverlay() string {
	lines := []string{
		titleStyle.Render("L Y R I C S"),
		"",
	}

	if m.lyrics.loading {
		lines = append(lines, dimStyle.Render("  Searching for lyrics..."))
	} else if m.lyrics.err != nil {
		if errors.Is(m.lyrics.err, lyrics.ErrNotFound) {
			lines = append(lines, dimStyle.Render("  No lyrics found for this track."))
		} else {
			lines = append(lines, helpStyle.Render("  Lyrics fetch failed: "+m.lyrics.err.Error()))
		}
	} else if len(m.lyrics.lines) == 0 {
		artist, title := m.lyricsArtistTitle()
		if artist == "" && title == "" {
			lines = append(lines, dimStyle.Render("  No artist/title metadata available."))
			track, idx := m.playlist.Current()
			if idx >= 0 && track.Stream {
				lines = append(lines, dimStyle.Render("  Waiting for stream metadata..."))
			}
		} else {
			lines = append(lines, dimStyle.Render("  No lyrics loaded. Press y to retry."))
		}
	} else if m.lyricsSyncable() && m.lyricsHaveTimestamps() {
		// Synced mode: auto-scroll to follow playback position.
		pos := m.player.Position()
		activeIdx := -1
		for i, line := range m.lyrics.lines {
			if line.Start <= pos {
				activeIdx = i
			} else {
				break
			}
		}

		visible := m.height - 8
		if visible < 5 {
			visible = 5
		}
		half := visible / 2
		startIdx := activeIdx - half
		if startIdx < 0 {
			startIdx = 0
		}
		endIdx := startIdx + visible
		if endIdx > len(m.lyrics.lines) {
			endIdx = len(m.lyrics.lines)
			startIdx = endIdx - visible
			if startIdx < 0 {
				startIdx = 0
			}
		}

		for i := startIdx; i < endIdx; i++ {
			text := m.lyrics.lines[i].Text
			if text == "" {
				text = "♪"
			}
			if i == activeIdx {
				lines = append(lines, playlistSelectedStyle.Render("  "+text))
			} else {
				lines = append(lines, dimStyle.Render("  "+text))
			}
		}
	} else {
		// Scroll mode: manual navigation with j/k or arrow keys.
		visible := m.height - 8
		if visible < 5 {
			visible = 5
		}
		endIdx := m.lyrics.scroll + visible
		if endIdx > len(m.lyrics.lines) {
			endIdx = len(m.lyrics.lines)
		}

		for i := m.lyrics.scroll; i < endIdx; i++ {
			text := m.lyrics.lines[i].Text
			if text == "" {
				text = "♪"
			}
			lines = append(lines, dimStyle.Render("  "+text))
		}
	}

	for len(lines) < 14 {
		lines = append(lines, "")
	}

	if m.lyricsSyncable() && m.lyricsHaveTimestamps() {
		lines = append(lines, "", helpKey("y/Esc", "Close"))
	} else {
		lines = append(lines, "", helpKey("↑↓/jk", "Scroll")+" "+helpKey("y/Esc", "Close"))
	}
	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) renderSpotSearch() string {
	var lines []string
	switch m.spotSearch.screen {
	case spotSearchInput:
		lines = m.renderSpotSearchInput()
	case spotSearchResults:
		lines = m.renderSpotSearchResults()
	case spotSearchPlaylist:
		lines = m.renderSpotSearchPlaylist()
	case spotSearchNewName:
		lines = m.renderSpotSearchNewName()
	}

	if m.spotSearch.err != "" {
		lines = append(lines, "", helpStyle.Render("  "+m.spotSearch.err))
	}

	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) renderSpotSearchInput() []string {
	lines := []string{
		titleStyle.Render("S P O T I F Y  S E A R C H"),
		"",
	}

	if m.spotSearch.loading {
		lines = append(lines, dimStyle.Render("  Searching..."))
	} else {
		lines = append(lines, playlistSelectedStyle.Render("  Search: "+m.spotSearch.query+"_"))
	}

	lines = append(lines, "", helpKey("Enter", "Search ")+helpKey("Esc", "Cancel"))
	return lines
}

func (m Model) renderSpotSearchResults() []string {
	lines := []string{
		titleStyle.Render("S E A R C H  R E S U L T S"),
		"",
	}

	maxVisible := 12
	rendered := 0

	if len(m.spotSearch.results) == 0 {
		lines = append(lines, dimStyle.Render("  No results"))
		rendered = 1
	} else {
		scroll := scrollStart(m.spotSearch.cursor, maxVisible)
		for i := scroll; i < len(m.spotSearch.results) && i < scroll+maxVisible; i++ {
			t := m.spotSearch.results[i]
			label := truncate(fmt.Sprintf("%s - %s", t.Artist, t.Title), ui.PanelWidth-8)
			lines = append(lines, cursorLine(label, i == m.spotSearch.cursor))
			rendered++
		}
	}

	lines = padLines(lines, maxVisible, rendered)
	lines = append(lines, "", dimStyle.Render(fmt.Sprintf("  %d results", len(m.spotSearch.results))))
	lines = append(lines, "", helpKey("↑↓", "Navigate ")+helpKey("Enter", "Add to playlist ")+helpKey("Esc", "Back"))
	return lines
}

func (m Model) renderSpotSearchPlaylist() []string {
	lines := []string{
		titleStyle.Render("A D D  T O  P L A Y L I S T"),
		"",
	}

	if m.spotSearch.loading {
		lines = append(lines, dimStyle.Render("  Loading playlists..."))
		return lines
	}

	track := m.spotSearch.selTrack
	lines = append(lines, dimStyle.Render("  "+truncate(fmt.Sprintf("%s - %s", track.Artist, track.Title), ui.PanelWidth-8)), "")

	count := len(m.spotSearch.playlists) + 1 // +1 for "+ New Playlist..."
	maxVisible := 12
	scroll := scrollStart(m.spotSearch.cursor, maxVisible)

	for i := scroll; i < count && i < scroll+maxVisible; i++ {
		var label string
		if i < len(m.spotSearch.playlists) {
			pl := m.spotSearch.playlists[i]
			label = pl.Name
		} else {
			label = "+ New Playlist..."
		}

		lines = append(lines, cursorLine(label, i == m.spotSearch.cursor))
	}

	if count > maxVisible {
		lines = append(lines, "", dimStyle.Render(fmt.Sprintf("  %d/%d playlists", m.spotSearch.cursor+1, count)))
	}

	lines = append(lines, "", helpKey("↑↓", "Navigate ")+helpKey("Enter", "Add ")+helpKey("Esc", "Back"))
	return lines
}

func (m Model) renderSpotSearchNewName() []string {
	lines := []string{
		titleStyle.Render("N E W  P L A Y L I S T"),
		"",
		dimStyle.Render("  Playlist name:"),
		playlistSelectedStyle.Render("  " + m.spotSearch.newName + "_"),
		"",
		helpKey("Enter", "Create & add ") + helpKey("Esc", "Cancel"),
	}
	return lines
}
