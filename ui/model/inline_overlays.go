package model

import (
	"errors"
	"fmt"
	"strings"

	"cliamp/lyrics"
	"cliamp/playlist"
	"cliamp/theme"
	"cliamp/ui"
)

// Inline overlays render in the playlist region while the now-playing,
// visualizer, and controls chrome stays live above them. Each overlay supplies
// three pieces, all the same vertical size as the normal playlist chrome so
// opening an overlay never shifts the layout height:
//
//   - a header line   (via overlayHeaderLine, used by renderPlaylistHeader)
//   - a body          (via overlayBody, fills effectivePlaylistVisible rows)
//   - a help line      (via overlayHelpLine, used by renderHelp)
//
// The switches below are ordered to match activeScreen so the header, body, and
// help always describe the same overlay.

// — shared header/body helpers —

// sepHeader renders a labeled separator. The label is embedded before the "─"
// fill, so separatorLine truncates it to the panel width: it never wraps.
func sepHeader(label string) string {
	return dimStyle.Render(labeledSeparator("", label))
}

// sepHeaderN appends an "n/total" position counter to a separator label.
func sepHeaderN(label string, pos, total int) string {
	if total <= 0 {
		return sepHeader(label)
	}
	return sepHeader(fmt.Sprintf("%s  %d/%d", label, pos, total))
}

// promptHeader renders an editable "label: value_" input as the header line,
// truncated to the panel width.
func promptHeader(label, value string) string {
	return playlistSelectedStyle.Render(truncate("  "+label+": "+value+"_", ui.PanelWidth))
}

// filterPromptHeader renders a `/` filter input as the header line.
func filterPromptHeader(query string) string {
	return playlistSelectedStyle.Render(truncate("  / "+query+"_", ui.PanelWidth))
}

// filterCountHeader renders a `/` filter prompt with a trailing match count,
// kept to one panel-wide row by clipping the query to leave room for the count.
func filterCountHeader(query, count string) string {
	maxPrompt := max(1, ui.PanelWidth-len(count)-2)
	return playlistSelectedStyle.Render(truncate("  / "+query+"_", maxPrompt)) + dimStyle.Render("  "+count)
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
		label := formatTrackRow(i+1, t.DisplayName()+trackAlbumSuffix(t, m.showAlbumHeaders), t.DurationSecs)
		lines = append(lines, cursorLine(label, i == cursor))
	}
	return bodyLines(lines, budget)
}

// — dispatch —

// overlayView bundles the three render pieces of an inline overlay: the header
// line (shown where the playlist header is), the help line, and the body that
// fills the playlist region. The pieces are method expressions (func(*Model)),
// not bound method values, so building an overlayView does not copy the Model
// onto the heap on the render hot path.
type overlayView struct {
	header func(*Model) string
	help   func(*Model) string
	body   func(*Model) string
}

// activeOverlay returns the render pieces for the active inline overlay, or
// ok=false when no overlay is open (the normal playlist is shown). Describing
// each overlay once here keeps its header, help, and body in sync, and the
// cases are ordered to match activeScreen. renderPlaylistHeader, renderHelp,
// and renderMainBody each call this and invoke the piece they need with &m.
func (m Model) activeOverlay() (overlayView, bool) {
	switch {
	case m.keymap.visible:
		return overlayView{(*Model).keymapHeaderLine, (*Model).keymapHelpLine, (*Model).renderKeymapList}, true
	case m.themePicker.visible:
		return overlayView{
			func(m *Model) string { return sepHeaderN("Themes", m.themePicker.cursor+1, m.themeCount()) },
			(*Model).themePickerHelpLine, (*Model).renderThemeBody}, true
	case m.visPicker.visible:
		return overlayView{
			func(m *Model) string { return sepHeaderN("Visualizers", m.visPicker.cursor+1, len(m.visPicker.modes)) },
			(*Model).visPickerHelpLine, (*Model).renderVisPickerList}, true
	case m.devicePicker.visible:
		return overlayView{(*Model).deviceHeaderLine, (*Model).devicePickerHelpLine, (*Model).renderDeviceBody}, true
	case m.fileBrowser.visible:
		return overlayView{(*Model).fbHeaderLine, (*Model).fbHelpLine, (*Model).renderFileBrowserBody}, true
	case m.navBrowser.visible:
		return overlayView{(*Model).navHeaderLine, (*Model).navHelpLine, (*Model).renderNavBody}, true
	case m.plManager.visible:
		return overlayView{(*Model).plMgrHeaderLine, (*Model).plMgrHelpLine, (*Model).renderPlMgrBody}, true
	case m.spotSearch.visible:
		return overlayView{(*Model).spotSearchHeaderLine, (*Model).spotSearchHelpLine, (*Model).renderSpotSearchBody}, true
	case m.queue.visible:
		return overlayView{
			func(m *Model) string { return sepHeaderN("Queue", m.queue.cursor+1, m.playlist.QueueLen()) },
			(*Model).queueHelpLine, (*Model).renderQueueBody}, true
	case m.showInfo:
		return overlayView{
			func(*Model) string { return sepHeader("Track Info") },
			func(*Model) string { return helpKey("Esc", "Close") },
			(*Model).renderInfoBody}, true
	case m.search.active:
		return overlayView{(*Model).searchHeaderLine, (*Model).searchHelpLine, (*Model).renderSearchList}, true
	case m.netSearch.active:
		return overlayView{(*Model).netSearchHeaderLine, (*Model).netSearchHelpLine, (*Model).renderNetSearchBody}, true
	case m.urlInputting:
		return overlayView{
			func(m *Model) string { return promptHeader("Load URL", m.urlInput) },
			func(*Model) string { return helpKey("Enter", "Load ") + helpKey("Esc", "Cancel") },
			(*Model).renderURLBody}, true
	case m.lyrics.visible:
		return overlayView{
			func(*Model) string { return sepHeader("Lyrics") },
			(*Model).lyricsHelpLine, (*Model).renderLyricsBody}, true
	case m.jumping:
		return overlayView{
			func(*Model) string { return sepHeader("Jump to Time") },
			func(*Model) string { return helpKey("Enter", "Jump ") + helpKey("Esc", "Cancel") },
			(*Model).renderJumpBody}, true
	}
	return overlayView{}, false
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
	return filterCountHeader(m.search.query, m.formatListMatchCount(len(m.search.results), m.playlist.Len()))
}

// — theme picker —

func (m Model) themeCount() int { return len(m.themes) + 1 }

func (m Model) renderThemeBody() string {
	budget := m.effectivePlaylistVisible()
	items := make([]string, 0, m.themeCount())
	items = append(items, theme.DefaultName)
	for _, t := range m.themes {
		items = append(items, t.Name)
	}
	return windowList(items, m.themePicker.cursor, m.themePicker.scroll, budget)
}

// — device picker —

func (m Model) deviceHeaderLine() string {
	if m.devicePicker.loading {
		return sepHeader("Audio Devices")
	}
	return sepHeaderN("Audio Devices", m.devicePicker.cursor+1, len(m.devicePicker.devices))
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

func (m Model) renderQueueBody() string {
	budget := m.effectivePlaylistVisible()
	tracks := m.playlist.QueueTracks()
	if len(tracks) == 0 {
		return bodyMessage("(empty)", budget)
	}
	items := make([]string, len(tracks))
	for i, t := range tracks {
		items[i] = fmt.Sprintf("%d. %s", i+1, truncate(t.DisplayName(), ui.PanelWidth-8))
	}
	return windowList(items, m.queue.cursor, m.queue.scroll, budget)
}

// — track info —

func (m Model) renderInfoBody() string {
	budget := m.effectivePlaylistVisible()
	track, _ := m.currentPlaybackTrack()

	var lines []string
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
	if len(lines) == 0 {
		lines = append(lines, dimStyle.Render("  No track metadata available."))
	}
	return bodyLines(lines, budget)
}

// — URL input —

func (m Model) renderURLBody() string {
	budget := m.effectivePlaylistVisible()
	return bodyMessage("Paste a stream, track, or playlist URL above.", budget)
}

// — jump to time —

func (m Model) renderJumpBody() string {
	budget := m.effectivePlaylistVisible()
	pos := m.player.Position()
	dur := m.player.Duration()
	inputLine := dimStyle.Faint(true).Render("  " + formatJumpPlaceholder(dur))
	if m.jumpInput != "" {
		inputLine = playlistSelectedStyle.Render("  " + m.jumpInput + "_")
	}
	return bodyLines([]string{
		dimStyle.Render(fmt.Sprintf("  %s / %s", formatJumpClock(pos), formatJumpClock(dur))),
		"",
		inputLine,
	}, budget)
}

// — lyrics —

func (m Model) lyricsHelpLine() string {
	if m.lyricsSyncable() && m.lyricsHaveTimestamps() {
		return helpKey("Esc", "Close")
	}
	return helpKey("↓↑", "Scroll ") + helpKey("Esc", "Close")
}

func (m Model) renderLyricsBody() string {
	visible := m.effectivePlaylistVisible()
	if visible <= 0 {
		return ""
	}

	var lines []string
	switch {
	case m.lyrics.loading:
		lines = append(lines, dimStyle.Render("  Searching for lyrics..."))
	case m.lyrics.err != nil:
		if errors.Is(m.lyrics.err, lyrics.ErrNotFound) {
			lines = append(lines, dimStyle.Render("  No lyrics found for this track."))
		} else {
			lines = append(lines, helpStyle.Render("  Lyrics fetch failed: "+m.lyrics.err.Error()))
		}
	case len(m.lyrics.lines) == 0:
		artist, title := m.lyricsArtistTitle()
		if artist == "" && title == "" {
			lines = append(lines, dimStyle.Render("  No artist/title metadata available."))
			if track, idx := m.currentPlaybackTrack(); idx >= 0 && track.Stream {
				lines = append(lines, dimStyle.Render("  Waiting for stream metadata..."))
			}
		} else {
			lines = append(lines, dimStyle.Render("  No lyrics loaded. Press y to retry."))
		}
	case m.lyricsSyncable() && m.lyricsHaveTimestamps():
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

func (m Model) netSearchSource() string {
	if m.netSearch.soundcloud {
		return "SoundCloud"
	}
	return "YouTube"
}

func (m Model) netSearchHeaderLine() string {
	if m.netSearch.screen == netSearchResults {
		return sepHeaderN("Online Results", m.netSearch.cursor+1, len(m.netSearch.results))
	}
	return promptHeader(m.netSearchSource()+" search", m.netSearch.query)
}

func (m Model) netSearchHelpLine() string {
	if m.netSearch.screen == netSearchResults {
		return m.netSearchResultsHelpLine()
	}
	return helpKey("Enter", "Search ") + helpKey("Esc", "Cancel")
}

func (m Model) renderNetSearchBody() string {
	budget := m.effectivePlaylistVisible()
	if m.netSearch.screen == netSearchInput {
		var lines []string
		if m.netSearch.loading {
			lines = append(lines, dimStyle.Render("  Searching "+m.netSearchSource()+"..."))
		} else {
			lines = append(lines, dimStyle.Render("  Type a query and press Enter to search "+m.netSearchSource()+"."))
		}
		if m.netSearch.err != "" {
			lines = append(lines, "", helpStyle.Render("  "+m.netSearch.err))
		}
		return bodyLines(lines, budget)
	}

	if len(m.netSearch.results) == 0 {
		return bodyMessage("No results", budget)
	}
	items := make([]string, len(m.netSearch.results))
	for i, t := range m.netSearch.results {
		items[i] = truncate(t.DisplayName(), ui.PanelWidth-8)
	}
	return windowList(items, m.netSearch.cursor, m.netSearch.scroll, budget)
}

// — provider (Spotify) search —

func (m Model) spotSearchHeaderLine() string {
	switch m.spotSearch.screen {
	case spotSearchResults:
		return sepHeaderN("Results", m.spotSearch.cursor+1, len(m.spotSearch.results))
	case spotSearchPlaylist:
		return sepHeaderN("Add to Playlist", m.spotSearch.cursor+1, len(m.spotSearch.playlists)+1)
	case spotSearchNewName:
		return promptHeader("New Playlist", m.spotSearch.newName)
	default:
		return promptHeader("Search", m.spotSearch.query)
	}
}

func (m Model) spotSearchHelpLine() string {
	switch m.spotSearch.screen {
	case spotSearchResults:
		return m.spotSearchResultsHelpLine()
	case spotSearchPlaylist:
		return m.spotSearchPlaylistHelpLine()
	case spotSearchNewName:
		return helpKey("Enter", "Create & add ") + helpKey("Esc", "Cancel")
	default:
		return helpKey("Enter", "Search ") + helpKey("Esc", "Cancel")
	}
}

func (m Model) renderSpotSearchBody() string {
	budget := m.effectivePlaylistVisible()
	var body string
	switch m.spotSearch.screen {
	case spotSearchResults:
		if len(m.spotSearch.results) == 0 {
			body = bodyMessage("No results", budget)
		} else {
			items := make([]string, len(m.spotSearch.results))
			for i, t := range m.spotSearch.results {
				items[i] = truncate(fmt.Sprintf("%s - %s", t.Artist, t.Title), ui.PanelWidth-8)
			}
			body = windowList(items, m.spotSearch.cursor, m.spotSearch.scroll, budget)
		}
	case spotSearchPlaylist:
		if m.spotSearch.loading {
			body = bodyLines([]string{loadingLine("Loading playlists…")}, budget)
			break
		}
		track := m.spotSearch.selTrack
		head := dimStyle.Render("  " + truncate(fmt.Sprintf("%s - %s", track.Artist, track.Title), ui.PanelWidth-2))
		count := len(m.spotSearch.playlists) + 1
		items := make([]string, count)
		for i := range count {
			if i < len(m.spotSearch.playlists) {
				items[i] = m.spotSearch.playlists[i].Name
			} else {
				items[i] = "+ New Playlist..."
			}
		}
		list := windowList(items, m.spotSearch.cursor, m.spotSearch.scroll, max(0, budget-1))
		body = strings.Join([]string{head, list}, "\n")
	case spotSearchNewName:
		body = bodyMessage("Enter a name for the new playlist above.", budget)
	default:
		var lines []string
		if m.spotSearch.loading {
			lines = append(lines, dimStyle.Render("  Searching..."))
		} else {
			lines = append(lines, dimStyle.Render("  Type a query and press Enter to search."))
		}
		body = bodyLines(lines, budget)
	}
	if m.spotSearch.err != "" && m.spotSearch.screen != spotSearchPlaylist {
		return strings.Join([]string{body, helpStyle.Render("  " + m.spotSearch.err)}, "\n")
	}
	return body
}
