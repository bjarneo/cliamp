package model

import (
	"errors"
	"fmt"
	"strings"

	"cliamp/history"
	"cliamp/lyrics"
	"cliamp/theme"
	"cliamp/ui"
)

func (m Model) renderSimpleList(
	before, after []string,
	items []string,
	cursor, scroll, maxVisible int,
	counterValue, emptyLabel string,
) []string {
	lines := append([]string{}, before...)
	rendered := 0

	if len(items) == 0 && emptyLabel != "" {
		lines = append(lines, dimStyle.Render("  "+emptyLabel))
		rendered = 1
	} else {
		for i := scroll; i < len(items) && rendered < maxVisible; i++ {
			lines = append(lines, cursorLine(items[i], i == cursor))
			rendered++
		}
	}

	lines = padLines(lines, maxVisible, rendered)
	if len(after) > 1 {
		footer := append([]string(nil), after...)
		footer[1] = dimStyle.Render(fmt.Sprintf("  %s", counterValue))
		lines = append(lines, footer...)
	} else {
		lines = append(lines, after...)
	}

	return lines
}

func (m Model) devicePickerChrome() (before, after []string) {
	before = []string{
		titleStyle.Render("A U D I O  D E V I C E S"),
		"",
	}
	after = []string{
		"",
		dimStyle.Render("  0/0 devices"),
		"",
		m.devicePickerHelpLine(),
	}
	return before, after
}

func (m Model) renderDeviceOverlay() string {
	before, after := m.devicePickerChrome()

	if m.devicePicker.loading {
		lines := append([]string{}, before...)
		lines = append(lines, loadingLine("Loading devices…"), "", helpKey("Esc", "Cancel"))
		return m.centerOverlay(strings.Join(lines, "\n"))
	}

	items := make([]string, len(m.devicePicker.devices))
	for i, d := range m.devicePicker.devices {
		label := d.Description
		if label == "" {
			label = d.Name
		}
		if d.Active {
			label += " " + activeToggle.Render("●")
		}
		items[i] = label
	}

	visible := m.devicePickerVisible()
	scroll := scrollStart(m.devicePicker.cursor, visible)
	visibleCount := max(0, min(visible, len(items)-scroll))
	lines := m.renderSimpleList(
		before, after,
		items,
		m.devicePicker.cursor,
		scroll,
		visible,
		fmt.Sprintf("%s devices", m.formatListRangeCount(scroll, visibleCount, len(items))),
		"No audio output devices found.",
	)

	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) themePickerChrome() (before, after []string) {
	before = []string{
		titleStyle.Render("T H E M E S"),
		"",
	}
	after = []string{
		"",
		dimStyle.Render("  0/0 themes"),
		"",
		m.themePickerHelpLine(),
	}
	return before, after
}

func (m Model) renderThemePicker() string {
	count := len(m.themes) + 1
	items := make([]string, count)
	items[0] = theme.DefaultName
	for i, t := range m.themes {
		items[i+1] = t.Name
	}

	before, after := m.themePickerChrome()
	visible := m.themePickerVisible()
	scroll := m.themePicker.scroll
	visibleCount := max(0, min(visible, len(items)-scroll))
	lines := m.renderSimpleList(
		before, after,
		items,
		m.themePicker.cursor,
		scroll,
		visible,
		fmt.Sprintf("%s themes", m.formatListRangeCount(scroll, visibleCount, len(items))),
		"",
	)

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

func (m Model) plMgrListChrome() (before, after []string) {
	before = []string{
		titleStyle.Render("P L A Y L I S T S"),
		"",
	}
	before = append(before, filterHeader(m.plManager.filtering, m.plManager.filter, "")...)

	after = []string{
		"",
		dimStyle.Render("  0/0 playlists"),
		"",
		m.plMgrListHelpLine(),
	}
	return before, after
}

func (m Model) renderPlMgrList() []string {
	before, after := m.plMgrListChrome()
	lines := append([]string{}, before...)

	visibleN := len(m.plManager.playlists)
	if m.plManager.filter != "" {
		visibleN = len(m.plManager.filtered)
	}

	// Empty state: no playlists at all.
	if len(m.plManager.playlists) == 0 {
		lines = append(lines,
			dimStyle.Render("  No playlists yet."),
			dimStyle.Render("  Press Enter on \"+ New Playlist…\" below,"),
			dimStyle.Render("  or `a` to save the now-playing track."),
			"",
			playlistSelectedStyle.Render("> + New Playlist..."),
		)
		lines = append(lines, after...)
		return lines
	}

	// Filtered with no matches: still allow "+ New Playlist..."
	if m.plManager.filter != "" && visibleN == 0 {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("  No playlists match %q", m.plManager.filter)))
		newLabel := "+ New Playlist \"" + m.plManager.filter + "\"..."
		lines = append(lines, cursorLine(newLabel, m.plManager.cursor == 0))
		lines = append(lines, after...)
		return lines
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
			// Spacer above the first user item?
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

	// Spacer before "+ New Playlist"?
	if visibleN > 0 {
		rows = append(rows, plRow{spacer: true, viewIdx: -1})
	}

	// New Playlist pseudo-item.
	newLabel := "+ New Playlist..."
	if m.plManager.filter != "" {
		newLabel = "+ New Playlist \"" + m.plManager.filter + "\"..."
	}
	rows = append(rows, plRow{label: newLabel, realIdx: -1, viewIdx: visibleN})

	// 2. Map logical scroll position to our row index.
	startIndex := 0
	for i, r := range rows {
		if r.viewIdx == m.plManager.scroll {
			startIndex = i
			break
		}
	}

	maxVisible := m.plMgrListVisible()
	rendered := 0
	renderedUser := 0

	// 3. Render the rows within the viewport budget.
	for i := startIndex; i < len(rows) && rendered < maxVisible; i++ {
		r := rows[i]
		if r.spacer {
			lines = append(lines, "")
			rendered++
			continue
		}

		if r.viewIdx < visibleN && m.plManager.playlists[r.realIdx].Name != history.PlaylistName {
			renderedUser++
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
		rendered++
	}

	lines = padLines(lines, maxVisible, rendered)

	totalUser := 0
	for _, p := range m.plManager.playlists {
		if p.Name != history.PlaylistName {
			totalUser++
		}
	}

	visibleUser := 0
	if m.plManager.filter != "" {
		for _, idx := range m.plManager.filtered {
			if m.plManager.playlists[idx].Name != history.PlaylistName {
				visibleUser++
			}
		}
	} else {
		visibleUser = totalUser
	}

	var footerCount string
	if m.plManager.filter == "" {
		scroll := m.plManager.scroll
		visibleCount := renderedUser
		footerCount = m.formatListRangeCount(scroll, visibleCount, totalUser)
	} else {
		visibleCount := visibleUser
		footerCount = m.formatListMatchCount(visibleCount, totalUser)
	}
	// Use the chrome's footer but update the counter line.
	footer := append([]string(nil), after...)
	footer[1] = dimStyle.Render(fmt.Sprintf("  %s playlists", footerCount))
	lines = append(lines, footer...)

	return lines
}

func (m Model) plMgrTracksChrome() (before, after []string) {
	title := fmt.Sprintf("P L A Y L I S T : %s", m.plManager.selPlaylist)
	before = []string{
		titleStyle.Render(title),
		"",
	}
	if subtitle := tracksSubtitle(m.plManager.tracks); subtitle != "" {
		before = append(before, dimStyle.Render("  "+subtitle), "")
	}
	before = append(before, filterHeader(m.plManager.filtering, m.plManager.filter, "")...)

	after = []string{
		"",
		dimStyle.Render("  0/0 tracks"),
		"",
		m.plMgrTracksHelpLine(),
	}
	return before, after
}

func (m Model) renderPlMgrTracks() []string {
	before, after := m.plMgrTracksChrome()
	lines := append([]string{}, before...)

	if len(m.plManager.tracks) == 0 {
		lines = append(lines,
			dimStyle.Render("  This playlist is empty."),
			dimStyle.Render("  Press `a` to add the now-playing track."),
		)
		lines = append(lines, after...)
		return lines
	}

	visibleN := len(m.plManager.tracks)
	if m.plManager.filter != "" {
		visibleN = len(m.plManager.filtered)
		if visibleN == 0 {
			lines = append(lines, dimStyle.Render(fmt.Sprintf("  No tracks match %q", m.plManager.filter)))
			lines = append(lines, after...)
			return lines
		}
	}

	maxVisible := m.plMgrTracksVisible()
	useAlbumSep := m.plManager.filter == "" && m.showAlbumHeaders

	scroll := m.plManager.scroll
	rendered := 0
	renderedTrackCount := 0

	if m.plManager.filter != "" {
		for i := scroll; i < visibleN && rendered < maxVisible; i++ {
			realIdx := m.plMgrTrackRealIndex(i)
			t := m.plManager.tracks[realIdx]
			label := formatTrackRow(realIdx+1, t.DisplayName()+trackAlbumSuffix(t, m.showAlbumHeaders), t.DurationSecs)
			lines = append(lines, cursorLine(label, i == m.plManager.cursor))
			rendered++
			renderedTrackCount++
		}
	} else {
		for row := range m.playlistRows(m.plManager.tracks, scroll, useAlbumSep) {
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
			lines = append(lines, cursorLine(label, i == m.plManager.cursor))
			rendered++
			renderedTrackCount++
		}
	}

	lines = padLines(lines, maxVisible, rendered)

	footerCount := m.formatListRangeCount(m.plManager.scroll, renderedTrackCount, len(m.plManager.tracks))
	if m.plManager.filter != "" {
		footerCount = m.formatListMatchCount(visibleN, len(m.plManager.tracks))
	}
	// Use the chrome's footer but update the counter line.
	footer := append([]string(nil), after...)
	footer[1] = dimStyle.Render(fmt.Sprintf("  %s tracks", footerCount))
	lines = append(lines, footer...)

	return lines
}

func (m Model) renderPlMgrNewName() []string {
	createAndAddLabel := "Create & add (nothing playing)"
	if track, idx := m.playlist.Current(); idx >= 0 && track.Path != "" {
		createAndAddLabel = "Create & add: " + truncate(track.DisplayName(), 32)
	}
	return []string{
		titleStyle.Render("N E W  P L A Y L I S T"),
		"",
		dimStyle.Render("  Playlist name:"),
		playlistSelectedStyle.Render("  " + m.plManager.newName + "_"),
		"",
		helpKey("Enter", createAndAddLabel+" ") +
			helpKey("Esc", "Cancel"),
	}
}

func (m Model) queueChrome() (before, after []string) {
	before = []string{
		titleStyle.Render("Q U E U E"),
		"",
	}
	after = []string{
		"",
		dimStyle.Render("  0 queued"),
		"",
		m.queueHelpLine(),
	}
	return before, after
}

func (m Model) renderQueueOverlay() string {
	tracks := m.playlist.QueueTracks()
	items := make([]string, len(tracks))
	for i, t := range tracks {
		name := truncate(t.DisplayName(), ui.PanelWidth-8)
		items[i] = fmt.Sprintf("%d. %s", i+1, name)
	}

	before, after := m.queueChrome()
	visible := m.queueVisible()
	scroll := scrollStart(m.queue.cursor, visible)
	visibleCount := max(0, min(visible, len(items)-scroll))
	lines := m.renderSimpleList(
		before, after,
		items,
		m.queue.cursor,
		scroll,
		visible,
		fmt.Sprintf("%s queued", m.formatListRangeCount(scroll, visibleCount, len(items))),
		"(empty)",
	)

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

	lines = append(lines, "", helpKey("Esc", "Close"))

	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) searchChrome() (before, after []string) {
	before = []string{
		titleStyle.Render("S E A R C H"),
		"",
		playlistSelectedStyle.Render("  / " + m.search.query + "_"),
		"",
	}
	after = []string{
		"",
		dimStyle.Render("  0 found"),
		"",
		m.searchHelpLine(),
	}
	return before, after
}

func (m Model) renderSearchOverlay() string {
	emptyMsg := "Type to search…"
	if m.search.query != "" {
		emptyMsg = "No matches"
	}

	tracks := m.playlist.Tracks()
	currentIdx := m.playlist.Index()
	isPlaying := m.player.IsPlaying()

	items := make([]string, len(m.search.results))
	for j, i := range m.search.results {
		prefix := "  "
		style := dimStyle

		if i == currentIdx && isPlaying {
			prefix = "▶ "
			style = playlistActiveStyle
		}

		// renderSimpleList will wrap this in cursorLine, which handles
		// the selected style for the cursor position.
		name := tracks[i].DisplayName()
		queueSuffix := ""
		if qp := m.playlist.QueuePosition(i); qp > 0 {
			queueSuffix = fmt.Sprintf(" [Q%d]", qp)
		}
		name = truncate(name, ui.PanelWidth-8-len([]rune(queueSuffix)))

		line := fmt.Sprintf("%s%d. %s", prefix, i+1, name)
		if queueSuffix != "" {
			items[j] = style.Render(line) + activeToggle.Render(queueSuffix)
		} else {
			items[j] = style.Render(line)
		}
	}

	before, after := m.searchChrome()
	visible := m.searchVisible()
	scroll := scrollStart(m.search.cursor, visible)
	lines := m.renderSimpleList(
		before, after,
		items,
		m.search.cursor,
		scroll,
		visible,
		m.formatListMatchCount(len(m.search.results), len(tracks)),
		emptyMsg,
	)

	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) renderNetSearchOverlay() string {
	var lines []string
	switch m.netSearch.screen {
	case netSearchInput:
		lines = m.renderNetSearchInput()
	case netSearchResults:
		lines = m.renderNetSearchResults()
	}
	if m.netSearch.err != "" {
		lines = append(lines, "", helpStyle.Render("  "+m.netSearch.err))
	}
	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) renderNetSearchInput() []string {
	source := "YouTube"
	if m.netSearch.soundcloud {
		source = "SoundCloud"
	}
	lines := []string{
		titleStyle.Render("F I N D   O N L I N E"),
		"",
		dimStyle.Render("  Source: " + source),
		"",
	}
	if m.netSearch.loading {
		lines = append(lines, dimStyle.Render("  Searching..."))
	} else {
		lines = append(lines, playlistSelectedStyle.Render("  Search: "+m.netSearch.query+"_"))
	}
	lines = append(lines, "", helpKey("Enter", "Search ")+helpKey("Ctrl+K", "Keys ")+helpKey("Esc", "Cancel"))
	return lines
}

func (m Model) netSearchResultsChrome() (before, after []string) {
	before = []string{
		titleStyle.Render("S E A R C H  R E S U L T S"),
		"",
	}
	after = []string{
		"",
		dimStyle.Render("  0 results"),
		"",
		m.netSearchResultsHelpLine(),
	}
	return before, after
}

func (m Model) renderNetSearchResults() []string {
	items := make([]string, len(m.netSearch.results))
	for i, t := range m.netSearch.results {
		label := t.DisplayName()
		items[i] = truncate(label, ui.PanelWidth-8)
	}

	before, after := m.netSearchResultsChrome()
	visible := m.netSearchResultsVisible()
	scroll := scrollStart(m.netSearch.cursor, visible)
	visibleCount := max(0, min(visible, len(items)-scroll))
	return m.renderSimpleList(
		before, after,
		items,
		m.netSearch.cursor,
		scroll,
		visible,
		fmt.Sprintf("%s results", m.formatListRangeCount(scroll, visibleCount, len(items))),
		"No results",
	)
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
	visible := m.lyricsVisibleHeight()
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

		half := visible / 2
		startIdx := max(activeIdx-half, 0)
		endIdx := startIdx + visible
		if endIdx > len(m.lyrics.lines) {
			endIdx = len(m.lyrics.lines)
			startIdx = max(endIdx-visible, 0)
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
		endIdx := min(m.lyrics.scroll+visible, len(m.lyrics.lines))

		for i := m.lyrics.scroll; i < endIdx; i++ {
			text := m.lyrics.lines[i].Text
			if text == "" {
				text = "♪"
			}
			lines = append(lines, dimStyle.Render("  "+text))
		}
	}

	rendered := len(lines) - 2 // -2 for header and spacing
	lines = padLines(lines, visible, rendered)

	if m.lyricsSyncable() && m.lyricsHaveTimestamps() {
		lines = append(lines, "", helpKey("Esc", "Close"))
	} else {
		lines = append(lines, "", helpKey("↓↑", "Scroll")+" "+helpKey("Esc", "Close"))
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
		titleStyle.Render("S E A R C H"),
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

func (m Model) spotSearchResultsChrome() (before, after []string) {
	before = []string{
		titleStyle.Render("S E A R C H  R E S U L T S"),
		"",
	}
	after = []string{
		"",
		dimStyle.Render("  0 results"),
		"",
		m.spotSearchResultsHelpLine(),
	}
	return before, after
}

func (m Model) renderSpotSearchResults() []string {
	items := make([]string, len(m.spotSearch.results))
	for i, t := range m.spotSearch.results {
		items[i] = truncate(fmt.Sprintf("%s - %s", t.Artist, t.Title), ui.PanelWidth-8)
	}

	before, after := m.spotSearchResultsChrome()
	visible := m.spotSearchResultsVisible()
	scroll := scrollStart(m.spotSearch.cursor, visible)
	visibleCount := max(0, min(visible, len(items)-scroll))
	return m.renderSimpleList(
		before, after,
		items,
		m.spotSearch.cursor,
		scroll,
		visible,
		fmt.Sprintf("%s results", m.formatListRangeCount(scroll, visibleCount, len(items))),
		"No results",
	)
}

func (m Model) spotSearchPlaylistChrome() (before, after []string) {
	before = []string{
		titleStyle.Render("A D D  T O  P L A Y L I S T"),
		"",
	}
	after = []string{
		"",
		dimStyle.Render("  0 playlists"),
		"",
		m.spotSearchPlaylistHelpLine(),
	}
	return before, after
}

func (m Model) renderSpotSearchPlaylist() []string {
	before, after := m.spotSearchPlaylistChrome()

	if m.spotSearch.loading {
		lines := append([]string{}, before...)
		lines = append(lines, loadingLine("Loading playlists…"))
		return lines
	}

	track := m.spotSearch.selTrack
	// Add the track header to the 'before' lines.
	before = append(before, dimStyle.Render("  "+truncate(fmt.Sprintf("%s - %s", track.Artist, track.Title), ui.PanelWidth-8)), "")

	playlistCount := len(m.spotSearch.playlists)
	count := playlistCount + 1 // +1 for "+ New Playlist..."
	items := make([]string, count)
	for i := 0; i < count; i++ {
		if i < len(m.spotSearch.playlists) {
			items[i] = m.spotSearch.playlists[i].Name
		} else {
			items[i] = "+ New Playlist..."
		}
	}

	visible := m.spotSearchPlaylistVisible()
	scroll := scrollStart(m.spotSearch.cursor, visible)
	visibleCount := max(0, min(visible, playlistCount-scroll))
	return m.renderSimpleList(
		before, after,
		items,
		m.spotSearch.cursor,
		scroll,
		visible,
		fmt.Sprintf("%s playlists", m.formatListRangeCount(scroll, visibleCount, playlistCount)),
		"",
	)
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
