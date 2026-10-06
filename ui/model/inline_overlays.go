package model

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/lyrics"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/theme"
)

// Inline overlays render in the playlist region while the now-playing,
// visualizer, and controls chrome stays live above them. Each overlay supplies
// three pieces, all the same vertical size as the normal playlist chrome so
// opening an overlay never shifts the layout height:
//
//   - a header line   (used by renderPlaylistHeader)
//   - a body          (fills effectivePlaylistVisible rows)
//   - a help line     (the hints of its command mode, used by renderHelp)
//
// overlayStack in overlays_table.go holds these pieces for each overlay, so the
// header, body, and help always describe the same overlay.

// — shared header/body helpers —

// sepHeader renders a labeled separator width cells wide. The label is
// embedded before the "─" fill, so separatorLine truncates it: it never wraps.
func sepHeader(label string, width int) string {
	return dimStyle.Render(labeledSeparator("", label, width))
}

// sepHeaderN appends an "n/total" position counter to a separator label.
func sepHeaderN(label string, pos, total, width int) string {
	if total <= 0 {
		return sepHeader(label, width)
	}
	return sepHeader(fmt.Sprintf("%s  %d/%d", label, pos, total), width)
}

// promptHeader renders an editable input with the shared editor cursor at its
// actual insertion point, then clips it to the panel width.
func (m Model) promptHeader(field, label, value string) string {
	return playlistSelectedStyle.Render(truncate("  "+label+": "+m.textWithCursor(field, value), m.layout.panelWidth))
}

// filterHeader renders the line of an open search or filter input. Every
// input uses it, so each one shows the same mode badge, such as
// "[Search: Spotify]", then the query with the editor cursor, an optional
// count, and the key that exits the mode. The line stays one panel wide. On a
// narrow panel it drops the count first and then the exit hint. The help line
// still shows Esc.
func (m Model) filterHeader(label, field, query, count string) string {
	const minInput, minLabel = 8, 12
	input := m.textWithCursor(field, query)
	exit := "  " + helpKey("Esc", "Exit")
	tail := exit
	if count != "" {
		tail = dimStyle.Render("  "+count) + exit
	}
	if m.layout.panelWidth <= 0 {
		return activeToggle.Render("  ["+label+"]") + " " + playlistSelectedStyle.Render(input) + tail
	}
	// The badge adds five columns: two spaces, two brackets, and one space.
	labelRoom := func() int {
		return m.layout.panelWidth - lipgloss.Width(tail) - 5 - min(lipgloss.Width(input), minInput)
	}
	for _, shorter := range []string{exit, ""} {
		if labelRoom() >= min(lipgloss.Width(label), minLabel) {
			break
		}
		tail = shorter
	}
	label = truncate(label, max(1, labelRoom()))
	inputRoom := m.layout.panelWidth - lipgloss.Width(tail) - 5 - lipgloss.Width(label)
	return activeToggle.Render("  ["+label+"]") + " " + playlistSelectedStyle.Render(truncate(input, max(1, inputRoom))) + tail
}

// windowList renders items[scroll:] into at most budget rows, applying the
// cursor highlight via cursorLine.
func windowList(items []string, cursor, scroll, budget int) string {
	if budget <= 0 {
		return ""
	}
	lines := make([]string, 0, budget)
	for i := scroll; i < len(items) && len(lines) < budget; i++ {
		lines = append(lines, cursorLine(items[i], i == cursor))
	}
	return strings.Join(padLines(lines, budget, len(lines)), "\n")
}

// bodyLines fits pre-built lines into the budget (truncate + pad to budget).
func bodyLines(lines []string, budget int) string {
	if budget <= 0 {
		return ""
	}
	return strings.Join(fitLines(lines, budget), "\n")
}

// bodyMessage renders a single dim message line into the budget.
func bodyMessage(msg string, budget int) string {
	return bodyLines([]string{dimStyle.Render("  " + msg)}, budget)
}

// renderSearchOverlayResults renders the search results grouped into labeled
// sections, so albums are visibly a different kind of result than the tracks
// below them rather than one long undifferentiated list.
func (m Model) renderSearchOverlayResults(budget int) string {
	lines := make([]string, 0, budget)
	for row := range searchOverlayRows(m.searchOverlay.results, m.searchOverlay.scroll) {
		if len(lines) >= budget {
			break
		}
		if row.Index < 0 {
			if budget == 1 {
				continue
			}
			lines = append(lines, dimStyle.Render(labeledSeparator("", row.Section, m.layout.panelWidth)))
			continue
		}
		label := truncate(trackViewName(row.Track), m.layout.panelWidth-8)
		lines = append(lines, cursorLine(label, row.Index == m.searchOverlay.cursor))
	}
	return strings.Join(padLines(lines, budget, len(lines)), "\n")
}

// renderTrackRowsBody renders a track list with album-header separators into
// the playlist-region budget, highlighting the row at cursor. Shared by the
// nav browser and playlist manager unfiltered track views.
func (m Model) renderTrackRowsBody(tracks []playlist.Track, cursor, scroll, budget int) string {
	lines := make([]string, 0, budget)
	for row := range m.playlistRows(tracks, scroll, m.showAlbumHeaders) {
		if len(lines) >= budget {
			break
		}
		if row.Index < 0 {
			lines = append(lines, m.albumSeparator(row.Album, row.Year))
			continue
		}
		i, t := row.Index, row.Track
		label := formatTrackRow(i+1, trackViewName(t)+trackAlbumSuffix(t, m.showAlbumHeaders), t.DurationSecs, m.layout.panelWidth)
		lines = append(lines, cursorLine(label, i == cursor))
	}
	return bodyLines(lines, budget)
}

// — dispatch —

// overlayView bundles the render pieces of an inline overlay: the header line
// (shown where the playlist header is) and the body that fills the playlist
// region. The help line comes from the command mode of the overlay. The pieces
// are method expressions (func(*Model)), not bound method values, so building
// an overlayView does not copy the Model onto the heap on the render hot path.
type overlayView struct {
	header func(*Model) string
	body   func(*Model) string
}

// activeOverlay returns the render pieces of the top overlay. It returns
// ok=false when no overlay is open, and for the full-screen visualizer, which
// replaces the whole frame. renderPlaylistHeader and renderMainBody each call
// this and invoke the piece they need with &m.
func (m Model) activeOverlay() (overlayView, bool) {
	spec, ok := m.topOverlay()
	if !ok || spec.view.body == nil {
		return overlayView{}, false
	}
	return spec.view, true
}

// renderMainBody returns the active overlay's body, or the playlist when no
// overlay is open.
func (m Model) renderMainBody() string {
	if ov, ok := m.activeOverlay(); ok {
		return ov.body(&m)
	}
	return m.renderPlaylist()
}

// — search —

func (m Model) searchHeaderLine() string {
	return m.filterHeader("Filter: Playlist", "playlist-search", m.search.query, m.formatListMatchCount(len(m.search.results), m.playlist.Len()))
}

// — theme picker —

func (m Model) themeCount() int { return len(m.themes) + 1 }

func (m Model) themePickerHeaderLine() string {
	if m.themePicker.isFiltered() {
		return m.filterHeader("Filter: Themes", "theme-picker-filter", m.themePicker.filter, fmt.Sprintf("%d/%d", m.themePickerViewCount(), m.themeCount()))
	}
	return sepHeaderN("Themes", m.themePicker.cursor+1, m.themePickerViewCount(), m.layout.panelWidth)
}

func (m Model) renderThemeBody() string {
	budget := m.effectivePlaylistVisible()
	names := make([]string, 0, m.themeCount())
	names = append(names, theme.DefaultName)
	for _, t := range m.themes {
		names = append(names, t.Name)
	}
	items := shownRows(&m.themePicker.filterList, names)
	if len(items) == 0 {
		return bodyMessage("No matches.", budget)
	}
	return windowList(items, m.themePicker.cursor, m.themePicker.scroll, budget)
}

// — device picker —

func (m Model) deviceHeaderLine() string {
	if m.devicePicker.loading {
		return sepHeader("Audio Devices", m.layout.panelWidth)
	}
	return sepHeaderN("Audio Devices", m.devicePicker.cursor+1, len(m.devicePicker.devices), m.layout.panelWidth)
}

func (m Model) renderDeviceBody() string {
	budget := m.effectivePlaylistVisible()
	if m.devicePicker.loading {
		return bodyLines([]string{loadingLine("Loading devices…")}, budget)
	}
	if len(m.devicePicker.devices) == 0 {
		return bodyMessage("No audio output devices found.", budget)
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
	return windowList(items, m.devicePicker.cursor, m.devicePicker.scroll, budget)
}

// — queue —

// renderQueueBody lists the queued tracks the way the playlist pane lists its
// own: grouped under a show or album header, with played markers and durations.
// The queue holds the same tracks, so reading it should not feel like reading a
// different kind of list.
func (m Model) renderQueueBody() string {
	budget := m.effectivePlaylistVisible()
	if budget <= 0 {
		return ""
	}
	total := m.playlist.QueueLen()
	if total == 0 {
		return bodyLines([]string{
			dimStyle.Render("  The queue is empty."),
			m.pressKeyHint(commandModeMain, "a", "on a playlist track to play it next."),
		}, budget)
	}
	if m.queue.confirmClear {
		return bodyMessage(fmt.Sprintf("%d tracks will be removed from the queue. c confirms; Esc cancels.", total), budget)
	}

	var stateReporters []provider.PlaybackStateReporter
	if m.hasPlaybackState() {
		stateReporters = m.playbackStateReporters()
	}
	numWidth := len(fmt.Sprintf("%d", total))
	scroll := clampedScroll(m.queue.scroll, m.queue.cursor, total, budget)
	// The window only needs the tracks around the cursor, so a long queue is
	// not cloned on every frame.
	windowStart := max(0, scroll-1)
	tracks := m.playlist.QueueWindow(windowStart, 2*budget+2)
	// clampedScroll counts tracks, but album headers take rows too.
	localScroll := m.fitHeaderScroll(tracks, scroll-windowStart, m.queue.cursor-windowStart, budget, m.showAlbumHeaders)

	lines := make([]string, 0, budget)
	for row := range m.playlistRows(tracks, localScroll, m.showAlbumHeaders) {
		if len(lines) >= budget {
			break
		}
		if row.Index < 0 {
			// A header on the last row would hide the track under it, and the
			// track is what the row is for.
			if len(lines)+1 < budget {
				lines = append(lines, m.albumSeparator(row.Album, row.Year))
			}
			continue
		}
		lines = append(lines, m.queueRow(row.Track, windowStart+row.Index, numWidth, stateReporters))
	}
	return strings.Join(padLines(lines, budget, len(lines)), "\n")
}

// queueRow renders one queued track: cursor, played marker, position, title,
// and a right-aligned duration.
func (m Model) queueRow(t playlist.Track, idx, numWidth int, reporters []provider.PlaybackStateReporter) string {
	style := playlistItemStyle
	selected := idx == m.queue.cursor
	if selected {
		style = playlistSelectedStyle
	}
	if t.Unplayable {
		style = playlistUnavailableStyle
		if selected {
			style = dimStyle
		}
	}

	cursorMarker := " "
	if selected {
		cursorMarker = ">"
	}
	stateMarker, stateStyle := " ", playlistActiveStyle
	if t.Unplayable {
		stateMarker, stateStyle = "!", playlistUnavailableStyle
	} else if state, ok := playbackStateFrom(reporters, t); ok {
		switch {
		case state.Played:
			stateMarker, stateStyle = playedMarker, activeToggle
		case state.Position > 0:
			stateMarker, stateStyle = partialMarker, dimStyle
		}
	}
	markers := cursorMarker + stateMarker + " "
	styled := dimStyle.Render(cursorMarker) + stateStyle.Render(stateMarker) + " "

	duration := formatTrackTime(t.DurationSecs)
	durationGap := 0
	if duration != "" {
		durationGap = lipgloss.Width(duration) + 1
	}
	prefixWidth := lipgloss.Width(markers) + numWidth + 2 // 2 for ". "
	name := truncate(trackViewName(t), m.layout.panelWidth-prefixWidth-durationGap)

	line := styled + style.Render(fmt.Sprintf("%*d. ", numWidth, idx+1)) + style.Render(name)
	if duration != "" {
		padding := max(1, m.layout.panelWidth-lipgloss.Width(line)-lipgloss.Width(duration))
		line += strings.Repeat(" ", padding) + dimStyle.Render(duration)
	}
	return line
}

// clampedScroll keeps the cursor inside the visible window without mutating
// the overlay's stored scroll, which the key handler owns.
func clampedScroll(scroll, cursor, count, budget int) int {
	if count <= budget {
		return 0
	}
	scroll = min(max(0, scroll), max(0, count-budget))
	if cursor < scroll {
		return cursor
	}
	if cursor >= scroll+budget {
		return min(cursor-budget+1, count-budget)
	}
	return scroll
}

// — track info —

func (m Model) renderInfoBody() string {
	budget := m.effectivePlaylistVisible()
	lines := m.infoLines()
	start := min(m.info.scroll, max(0, len(lines)-budget))
	end := min(start+budget, len(lines))
	return bodyLines(lines[start:end], budget)
}

func (m Model) infoLines() []string {
	var lines []string
	for _, field := range m.metadataFields() {
		lines = append(lines, dimStyle.Render("  "+field.label+": ")+trackStyle.Render(field.value))
	}
	if path := metadataText(m.selectedMetadataTrack().Path); path != "" {
		lines = append(lines, dimStyle.Render("  Path: ")+trackStyle.Render(path))
	}
	if len(lines) == 0 {
		lines = append(lines, dimStyle.Render("  No track metadata available."))
	}
	return lines
}

func (m *Model) infoMaybeAdjustScroll() {
	m.info.scroll = min(m.info.scroll, max(0, len(m.infoLines())-m.effectivePlaylistVisible()))
}

// — URL input —

func (m Model) renderURLBody() string {
	budget := m.effectivePlaylistVisible()
	lines := []string{dimStyle.Render("  Paste a stream, track, or playlist URL above.")}
	if m.urlInput.err != "" {
		lines = append(lines, errorStyle.Render("  "+m.urlInput.err))
	}
	return bodyLines(lines, budget)
}

// — jump to time —

func (m Model) renderJumpBody() string {
	budget := m.effectivePlaylistVisible()
	pos := m.player.Position()
	dur := m.player.Duration()
	inputLine := dimStyle.Render("  " + formatJumpPlaceholder(dur))
	if m.jump.input != "" {
		inputLine = playlistSelectedStyle.Render("  " + m.textWithCursor("jump", m.jump.input))
	}
	lines := []string{
		dimStyle.Render(fmt.Sprintf("  %s / %s", formatJumpClock(pos), formatJumpClock(dur))),
		"",
		inputLine,
	}
	if m.jump.err != "" {
		lines = append(lines, errorStyle.Render("  "+m.jump.err))
	}
	return bodyLines(lines, budget)
}

// — lyrics —

func (m Model) renderLyricsBody() string {
	visible := m.effectivePlaylistVisible()
	if visible <= 0 {
		return ""
	}

	var lines []string
	switch {
	case m.lyrics.loading:
		lines = append(lines, loadingLine("Searching for lyrics..."))
	case m.lyrics.err != nil:
		if errors.Is(m.lyrics.err, lyrics.ErrNotFound) {
			lines = append(lines, dimStyle.Render("  No lyrics found for this track."))
		} else {
			lines = append(lines, errorStyle.Render("  Lyrics fetch failed: "+m.lyrics.err.Error()))
		}
	case len(m.lyrics.lines) == 0:
		artist, title := m.lyricsArtistTitle()
		if artist == "" && title == "" {
			lines = append(lines, dimStyle.Render("  No artist/title metadata available."))
			if track, idx := m.currentPlaybackTrack(); idx >= 0 && track.Stream {
				lines = append(lines, dimStyle.Render("  Waiting for stream metadata..."))
			}
		} else {
			lines = append(lines, dimStyle.Render("  No lyrics loaded. Press r to retry."))
		}
	case m.lyricsSyncable() && m.lyricsHaveTimestamps():
		pos := m.lyricsPlaybackPosition()
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
	default:
		endIdx := min(m.lyrics.scroll+visible, len(m.lyrics.lines))
		for i := m.lyrics.scroll; i < endIdx; i++ {
			text := m.lyrics.lines[i].Text
			if text == "" {
				text = "♪"
			}
			lines = append(lines, dimStyle.Render("  "+text))
		}
	}
	return bodyLines(lines, visible)
}

// — online (net) search —

// providerName returns the name of prov, or "" when prov is nil.
func providerName(prov playlist.Provider) string {
	if prov == nil {
		return ""
	}
	return prov.Name()
}

func (m Model) netSearchSource() string {
	if m.netSearch.soundcloud {
		return "SoundCloud"
	}
	return "YouTube"
}

func (m Model) netSearchHeaderLine() string {
	if m.netSearch.screen == netSearchResults {
		return sepHeaderN(m.netSearchSource()+" Results", m.netSearch.cursor+1, len(m.netSearch.results), m.layout.panelWidth)
	}
	return m.filterHeader("Search: "+m.netSearchSource(), "net-search", m.netSearch.query, "")
}

func (m Model) renderNetSearchBody() string {
	budget := m.effectivePlaylistVisible()
	if m.netSearch.screen == netSearchInput {
		var lines []string
		if m.netSearch.from != "" {
			lines = append(lines, dimStyle.Render(fmt.Sprintf("  %s has no Ctrl+F search. This searches %s.", m.netSearch.from, m.netSearchSource())))
		}
		if m.netSearch.loading {
			lines = append(lines, loadingLine("Searching "+m.netSearchSource()+"..."))
		} else {
			lines = append(lines, dimStyle.Render("  Type a query and press Enter to search "+m.netSearchSource()+"."))
		}
		if m.netSearch.err != "" {
			lines = append(lines, "", errorStyle.Render("  "+m.netSearch.err))
		}
		return bodyLines(lines, budget)
	}

	if len(m.netSearch.results) == 0 {
		return bodyMessage("No results", budget)
	}
	items := make([]string, len(m.netSearch.results))
	for i, t := range m.netSearch.results {
		items[i] = truncate(trackViewName(t), m.layout.panelWidth-8)
	}
	return windowList(items, m.netSearch.cursor, m.netSearch.scroll, budget)
}

// — provider (Spotify) search —

func (m Model) searchOverlayHeaderLine() string {
	switch m.searchOverlay.screen {
	case searchOverlayResults:
		return sepHeaderN("Results", m.searchOverlay.cursor+1, len(m.searchOverlay.results), m.layout.panelWidth)
	case searchOverlayPlaylist:
		return sepHeaderN("Add to Playlist", m.searchOverlay.cursor+1, len(m.searchOverlay.playlists)+1, m.layout.panelWidth)
	case searchOverlayNewName:
		return m.promptHeader("search-overlay-playlist-name", "New Playlist", m.searchOverlay.newName)
	default:
		return m.filterHeader("Search: "+providerName(m.searchOverlay.prov), "search-overlay", m.searchOverlay.query, "")
	}
}

func (m Model) renderSearchOverlayBody() string {
	budget := m.effectivePlaylistVisible()
	showError := m.searchOverlay.err != ""
	bodyBudget := budget
	if showError {
		bodyBudget = max(0, bodyBudget-1)
	}
	var body string
	switch m.searchOverlay.screen {
	case searchOverlayResults:
		switch {
		case m.searchOverlay.albumLoading:
			body = bodyLines([]string{loadingLine("Loading album…")}, bodyBudget)
		case len(m.searchOverlay.results) == 0:
			body = bodyMessage("No results", bodyBudget)
		default:
			body = m.renderSearchOverlayResults(bodyBudget)
		}
	case searchOverlayPlaylist:
		if m.searchOverlay.loading {
			body = bodyLines([]string{loadingLine("Loading playlists…")}, bodyBudget)
			break
		}
		track := m.searchOverlay.selTrack
		head := dimStyle.Render("  " + truncate(fmt.Sprintf("%s - %s", track.Artist, track.Title), m.layout.panelWidth-2))
		count := len(m.searchOverlay.playlists) + 1
		items := make([]string, count)
		for i := range count {
			if i < len(m.searchOverlay.playlists) {
				items[i] = m.searchOverlay.playlists[i].Name
			} else {
				items[i] = "+ New Playlist..."
			}
		}
		list := windowList(items, m.searchOverlay.cursor, m.searchOverlay.scroll, max(0, bodyBudget-1))
		body = strings.Join([]string{head, list}, "\n")
	case searchOverlayNewName:
		body = bodyMessage("Enter a name for the new playlist above.", bodyBudget)
	default:
		var lines []string
		if m.searchOverlay.loading {
			lines = append(lines, loadingLine("Searching "+providerName(m.searchOverlay.prov)+"..."))
		} else {
			lines = append(lines, dimStyle.Render("  Type a query and press Enter to search."))
		}
		body = bodyLines(lines, bodyBudget)
	}
	if showError {
		errLine := errorStyle.Render("  " + m.searchOverlay.err)
		if body == "" {
			return errLine
		}
		return strings.Join([]string{body, errLine}, "\n")
	}
	return body
}
