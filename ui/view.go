package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"cliamp/external/navidrome"
	"cliamp/theme"
)

// Pre-built styles for elements created per-render to avoid repeated allocation.
var (
	seekFillStyle = lipgloss.NewStyle().Foreground(colorSeekBar)
	seekDimStyle  = lipgloss.NewStyle().Foreground(colorDim)
	volBarStyle   = lipgloss.NewStyle().Foreground(colorVolume)
	activeToggle  = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
)

// View renders the full TUI frame.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	if m.showKeymap {
		return m.renderKeymapOverlay()
	}

	if m.showThemes {
		return m.renderThemePicker()
	}

	if m.showFileBrowser {
		return m.renderFileBrowser()
	}

	if m.showNavBrowser {
		return m.renderNavBrowser()
	}

	if m.showPlManager {
		return m.renderPlaylistManager()
	}

	if m.showQueue {
		return m.renderQueueOverlay()
	}

	if m.showInfo {
		return m.renderInfoOverlay()
	}

	if m.searching {
		return m.renderSearchOverlay()
	}

	if m.netSearching {
		return m.renderNetSearchOverlay()
	}

	if m.fullVis {
		return m.renderFullVisualizer()
	}

	sections := []string{
		// Now playing
		m.renderTitle(),
		m.renderTrackInfo(),
		m.renderTimeStatus(),
		"",
		// Visualizer
		m.renderSpectrum(),
		m.renderSeekBar(),
		"",
		// Controls
		m.renderControls(),
		"",
		// Playlist
		m.renderPlaylistHeader(),
		m.renderPlaylist(),
		"",
		// Help
		m.renderHelp(),
		m.renderStreamStatus(),
	}

	if m.err != nil {
		sections = append(sections, errorStyle.Render(fmt.Sprintf("ERR: %s", m.err)))
	}
	if m.saveMsg != "" {
		sections = append(sections, statusStyle.Render(m.saveMsg))
	}

	content := strings.Join(sections, "\n")
	frame := frameStyle.Render(content)

	return m.centerFrame(frame)
}

// centerFrame centers a pre-rendered frame in the terminal using plain string
// padding instead of allocating a new lipgloss.Style every render.
func (m Model) centerFrame(frame string) string {
	frameW := lipgloss.Width(frame)
	frameH := lipgloss.Height(frame)
	padLeft := max(0, (m.width-frameW)/2)
	padTop := max(0, (m.height-frameH)/2)

	if padLeft == 0 {
		return strings.Repeat("\n", padTop) + frame
	}
	// Indent every line by padLeft spaces.
	prefix := strings.Repeat(" ", padLeft)
	lines := strings.Split(frame, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Repeat("\n", padTop) + strings.Join(lines, "\n")
}

// centerOverlay wraps content in a frame and centers it in the terminal.
func (m Model) centerOverlay(content string) string {
	return m.centerFrame(frameStyle.Render(content))
}

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

	// Build visible entries (filtered or all).
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
		scroll := 0
		if m.keymapCursor >= maxVisible {
			scroll = m.keymapCursor - maxVisible + 1
		}

		for i := scroll; i < len(visible) && i < scroll+maxVisible; i++ {
			line := fmt.Sprintf("%-10s %s", visible[i].key, visible[i].action)
			if i == m.keymapCursor {
				lines = append(lines, playlistSelectedStyle.Render("> "+line))
			} else {
				lines = append(lines, dimStyle.Render("  "+line))
			}
			rendered++
		}
	}

	for range maxVisible - rendered {
		lines = append(lines, "")
	}

	lines = append(lines, "", dimStyle.Render(fmt.Sprintf("  %d/%d keys", len(visible), len(entries))))
	lines = append(lines, "", helpKey("↑↓", "Navigate ")+helpKey("Type", "Filter ")+helpKey("Esc", "Close"))

	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) renderThemePicker() string {
	lines := []string{
		titleStyle.Render("T H E M E S"),
		"",
	}

	// Theme list: Default at index 0, then all loaded themes.
	count := len(m.themes) + 1
	maxVisible := 15
	scroll := 0
	if m.themeCursor >= maxVisible {
		scroll = m.themeCursor - maxVisible + 1
	}

	for i := scroll; i < count && i < scroll+maxVisible; i++ {
		var name string
		if i == 0 {
			name = theme.DefaultName
		} else {
			name = m.themes[i-1].Name
		}

		if i == m.themeCursor {
			lines = append(lines, playlistSelectedStyle.Render("> "+name))
		} else {
			lines = append(lines, dimStyle.Render("  "+name))
		}
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
		scroll := 0
		if m.queueCursor >= maxVisible {
			scroll = m.queueCursor - maxVisible + 1
		}

		for i := scroll; i < len(tracks) && i < scroll+maxVisible; i++ {
			name := tracks[i].DisplayName()
			maxW := panelWidth - 8
			nameRunes := []rune(name)
			if len(nameRunes) > maxW {
				name = string(nameRunes[:maxW-1]) + "…"
			}
			label := fmt.Sprintf("%d. %s", i+1, name)

			if i == m.queueCursor {
				lines = append(lines, playlistSelectedStyle.Render("> "+label))
			} else {
				lines = append(lines, dimStyle.Render("  "+label))
			}
			rendered++
		}
	}

	// Pad to fixed height so the overlay doesn't shift.
	for range maxVisible - rendered {
		lines = append(lines, "")
	}

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

func (m Model) renderPlMgrList() []string {
	lines := []string{
		titleStyle.Render("P L A Y L I S T S"),
		"",
	}

	count := len(m.plMgrPlaylists) + 1 // +1 for "+ New Playlist..."
	maxVisible := 12
	scroll := 0
	if m.plMgrCursor >= maxVisible {
		scroll = m.plMgrCursor - maxVisible + 1
	}

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
	scroll := 0
	if m.plMgrCursor >= maxVisible {
		scroll = m.plMgrCursor - maxVisible + 1
	}

	for i := scroll; i < len(m.plMgrTracks) && i < scroll+maxVisible; i++ {
		name := m.plMgrTracks[i].DisplayName()
		maxW := panelWidth - 8
		nameRunes := []rune(name)
		if len(nameRunes) > maxW {
			name = string(nameRunes[:maxW-1]) + "…"
		}
		label := fmt.Sprintf("%d. %s", i+1, name)

		if i == m.plMgrCursor {
			lines = append(lines, playlistSelectedStyle.Render("> "+label))
		} else {
			lines = append(lines, dimStyle.Render("  "+label))
		}
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

func (m Model) renderTitle() string {
	return titleStyle.Render("C L I A M P")
}

func (m Model) renderTrackInfo() string {
	track, _ := m.playlist.Current()
	name := track.DisplayName()
	if name == "" {
		name = "No track loaded"
	}
	// Show live ICY stream title instead of static track name for radio streams.
	if m.streamTitle != "" && track.Stream {
		name = m.streamTitle
	}

	maxW := panelWidth - 4
	runes := []rune(name)

	var titleLine string
	if len(runes) <= maxW {
		titleLine = trackStyle.Render("♫ " + name)
	} else {
		// Cyclic scrolling for long titles
		sep := []rune("   ♫   ")
		padded := append(runes, sep...)
		total := len(padded)
		off := m.titleOff % total

		display := make([]rune, maxW)
		for i := range maxW {
			display[i] = padded[(off+i)%total]
		}
		titleLine = trackStyle.Render("♫ " + string(display))
	}

	// Show album subtitle when available.
	album := track.Album
	if m.streamTitle != "" && track.Stream {
		album = "" // no album for live streams
	}
	if album != "" {
		albumRunes := []rune(album)
		if len(albumRunes) > maxW {
			album = string(albumRunes[:maxW-1]) + "…"
		}
		return titleLine + "\n" + dimStyle.Render("  "+album)
	}
	return titleLine
}

func (m Model) renderTimeStatus() string {
	pos := m.player.Position()
	dur := m.player.Duration()

	posMin := int(pos.Minutes())
	posSec := int(pos.Seconds()) % 60
	durMin := int(dur.Minutes())
	durSec := int(dur.Seconds()) % 60

	timeStr := fmt.Sprintf("%02d:%02d / %02d:%02d", posMin, posSec, durMin, durSec)

	track, _ := m.playlist.Current()

	var status string
	switch {
	case m.buffering:
		status = statusStyle.Render("◌ Buffering...")
	case m.player.IsPlaying() && m.player.IsPaused():
		status = statusStyle.Render("⏸ Paused")
	case m.player.IsPlaying() && track.Stream:
		status = statusStyle.Render("● Streaming")
	case m.player.IsPlaying():
		status = statusStyle.Render("▶ Playing")
	default:
		status = dimStyle.Render("■ Stopped")
	}

	left := timeStyle.Render(timeStr)
	gap := panelWidth - lipgloss.Width(left) - lipgloss.Width(status)
	if gap < 1 {
		gap = 1
	}

	return left + strings.Repeat(" ", gap) + status
}

func (m Model) renderSpectrum() string {
	if m.vis.Mode == VisNone {
		return ""
	}
	bands := m.vis.Analyze(m.player.Samples())
	return m.vis.Render(bands)
}

// renderFullVisualizer renders a full-screen view showing only the visualizer
// with minimal track info and a seek bar.
func (m Model) renderFullVisualizer() string {
	sections := []string{
		m.renderTrackInfo(),
		m.renderTimeStatus(),
		"",
		m.renderSpectrum(),
		m.renderSeekBar(),
		"",
		helpKey("V", "Exit ") + helpKey("v", "Mode:"+m.vis.ModeName()+" ") + helpKey("Spc", "⏯ ") + helpKey("<>", "Trk ") + helpKey("+-", "Vol"),
	}

	content := strings.Join(sections, "\n")
	frame := frameStyle.Render(content)

	frameW := lipgloss.Width(frame)
	frameH := lipgloss.Height(frame)

	padLeft := max(0, (m.width-frameW)/2)
	padTop := max(0, (m.height-frameH)/2)

	return strings.Repeat("\n", padTop) +
		lipgloss.NewStyle().MarginLeft(padLeft).Render(frame)
}

func (m Model) renderSeekBar() string {
	// Show a static streaming bar for non-seekable streams with no known duration.
	if !m.player.Seekable() && m.player.IsPlaying() && m.player.Duration() == 0 {
		label := " STREAMING "
		pad := panelWidth - lipgloss.Width(label)
		left := pad / 2
		right := pad - left
		return seekFillStyle.Render(strings.Repeat("━", left) + label + strings.Repeat("━", right))
	}

	pos := m.player.Position()
	dur := m.player.Duration()

	var progress float64
	if dur > 0 {
		progress = float64(pos) / float64(dur)
	}
	progress = max(0, min(1, progress))

	filled := int(progress * float64(panelWidth-1))

	return seekFillStyle.Render(strings.Repeat("━", filled)) +
		seekFillStyle.Render("●") +
		seekDimStyle.Render(strings.Repeat("━", max(0, panelWidth-filled-1)))
}

func (m Model) renderControls() string {
	// ── Line 1: VOL bar + dB [Mono]  ·····  OUT 48kHz · Resample 3/4 ──

	vol := m.player.Volume()
	frac := max(0, min(1, (vol+30)/36))

	barW := 20
	filled := int(frac * float64(barW))

	bar := volBarStyle.Render(strings.Repeat("█", filled)) +
		dimStyle.Render(strings.Repeat("░", barW-filled))

	left := labelStyle.Render("VOL ") + bar + dimStyle.Render(fmt.Sprintf(" %+.1fdB", vol))
	if m.player.Mono() {
		left += " " + activeToggle.Render("[Mono]")
	}

	sr := m.player.SampleRate()
	rq := m.player.ResampleQuality()
	var srStr string
	if sr >= 1000 {
		srStr = fmt.Sprintf("%gkHz", float64(sr)/1000)
	} else {
		srStr = fmt.Sprintf("%dHz", sr)
	}
	right := labelStyle.Render("OUT ") +
		activeToggle.Render(srStr) + dimStyle.Render(" · ") +
		dimStyle.Render("Resample ") + activeToggle.Render(fmt.Sprintf("%d/4", rq))

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	gap := max(2, panelWidth-leftW-rightW)
	line1 := left + strings.Repeat(" ", gap) + right

	// ── Line 2: EQ bands  ·····  [Preset] ──

	bands := m.player.EQBands()
	labels := [10]string{"70", "180", "320", "600", "1k", "3k", "6k", "12k", "14k", "16k"}

	parts := make([]string, len(labels))
	for i, label := range labels {
		style := eqInactiveStyle
		if bands[i] != 0 {
			label = fmt.Sprintf("%+.0f", bands[i])
		}
		if m.focus == focusEQ && i == m.eqCursor {
			style = eqActiveStyle
		}
		parts[i] = style.Render(label)
	}

	presetName := m.EQPresetName()
	eqLeft := labelStyle.Render("EQ  ") + strings.Join(parts, " ")
	eqRight := dimStyle.Render("[") + activeToggle.Render(presetName) + dimStyle.Render("]")

	eqLeftW := lipgloss.Width(eqLeft)
	eqRightW := lipgloss.Width(eqRight)
	eqGap := max(2, panelWidth-eqLeftW-eqRightW)
	line2 := eqLeft + strings.Repeat(" ", eqGap) + eqRight

	return line1 + "\n" + line2
}

func (m Model) renderPlaylistHeader() string {
	if m.focus == focusProvider {
		return dimStyle.Render(fmt.Sprintf("── %s Playlists ── ", m.provider.Name()))
	}

	var shuffle string
	if m.playlist.Shuffled() {
		shuffle = activeToggle.Render("[Shuffle]")
	} else {
		shuffle = dimStyle.Render("[") + trackStyle.Render("Shuffle") + dimStyle.Render("]")
	}

	repeatVal := m.playlist.Repeat().String()
	if m.playlist.Repeat() != 0 {
		repeatStr := fmt.Sprintf("[Repeat: %s]", repeatVal)
		repeatStr = activeToggle.Render(repeatStr)
		shuffle += " " + repeatStr
	} else {
		repeatStr := dimStyle.Render("[") + trackStyle.Render("Repeat") + dimStyle.Render(": ") + dimStyle.Render(repeatVal) + dimStyle.Render("]")
		shuffle += " " + repeatStr
	}

	var queueStr string
	if qLen := m.playlist.QueueLen(); qLen > 0 {
		queueStr = " " + activeToggle.Render(fmt.Sprintf("[Queue: %d]", qLen))
	}

	var themeStr string
	if name := m.ThemeName(); name != theme.DefaultName {
		themeStr = " " + activeToggle.Render("[Theme: "+name+"]")
	}

	return dimStyle.Render("── Playlist ── ") + shuffle + queueStr + themeStr + " " + dimStyle.Render("──")
}

func (m Model) renderPlaylist() string {
	if m.focus == focusProvider {
		if m.provLoading {
			return dimStyle.Render(fmt.Sprintf("  Loading %s...", m.provider.Name()))
		}
		if len(m.providerLists) == 0 {
			return dimStyle.Render("  No playlists found.\n  Add playlists to ~/.config/cliamp/playlists/")
		}

		visible := min(m.plVisible, len(m.providerLists))
		scroll := max(0, m.provCursor-visible+1)

		var lines []string
		for j := scroll; j < scroll+visible && j < len(m.providerLists); j++ {
			p := m.providerLists[j]
			prefix, style := "  ", playlistItemStyle
			if j == m.provCursor {
				style = playlistSelectedStyle
				prefix = "> "
			}
			lines = append(lines, style.Render(fmt.Sprintf("%s%s (%d tracks)", prefix, p.Name, p.TrackCount)))
		}
		return strings.Join(lines, "\n")
	}

	tracks := m.playlist.Tracks()
	if len(tracks) == 0 {
		if m.feedLoading {
			return dimStyle.Render("  Loading feed...")
		}
		return dimStyle.Render("  No tracks loaded")
	}

	currentIdx := m.playlist.Index()
	visible := min(m.plVisible, len(tracks))

	scroll := m.plScroll
	if scroll+visible > len(tracks) {
		scroll = len(tracks) - visible
	}
	scroll = max(0, scroll)

	lines := make([]string, 0, visible)
	prevAlbum := ""
	if scroll > 0 {
		prevAlbum = tracks[scroll-1].Album
	}
	for i := scroll; i < len(tracks) && len(lines) < visible; i++ {
		// Insert album separator when album changes
		if album := tracks[i].Album; album != "" && album != prevAlbum {
			label := "── " + album
			if tracks[i].Year != 0 {
				label += fmt.Sprintf(" (%d)", tracks[i].Year)
			}
			label += " "
			pw := panelWidth
			labelLen := len([]rune(label))
			if labelLen < pw {
				label += strings.Repeat("─", pw-labelLen)
			}
			lines = append(lines, dimStyle.Render(label))
			if len(lines) >= visible {
				break
			}
		}
		prevAlbum = tracks[i].Album

		prefix := "  "
		style := playlistItemStyle

		if i == currentIdx && m.player.IsPlaying() {
			prefix = "▶ "
			style = playlistActiveStyle
		}

		if m.focus == focusPlaylist && i == m.plCursor {
			style = playlistSelectedStyle
		}

		name := tracks[i].DisplayName()
		queueSuffix := ""
		if qp := m.playlist.QueuePosition(i); qp > 0 {
			queueSuffix = fmt.Sprintf(" [Q%d]", qp)
		}
		maxW := panelWidth - 6 - len([]rune(queueSuffix))
		nameRunes := []rune(name)
		if len(nameRunes) > maxW {
			name = string(nameRunes[:maxW-1]) + "…"
		}

		line := fmt.Sprintf("%s%d. %s", prefix, i+1, name)
		if queueSuffix != "" {
			line = style.Render(line) + activeToggle.Render(queueSuffix)
		} else {
			line = style.Render(line)
		}
		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
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
		scroll := 0
		if m.searchCursor >= maxVisible {
			scroll = m.searchCursor - maxVisible + 1
		}

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
			maxW := panelWidth - 8 - len([]rune(queueSuffix))
			nameRunes := []rune(name)
			if len(nameRunes) > maxW {
				name = string(nameRunes[:maxW-1]) + "…"
			}

			line := fmt.Sprintf("%s%d. %s", prefix, i+1, name)
			if queueSuffix != "" {
				lines = append(lines, style.Render(line)+activeToggle.Render(queueSuffix))
			} else {
				lines = append(lines, style.Render(line))
			}
			rendered++
		}
	}

	// Pad to fixed height so the overlay doesn't shift.
	for range maxVisible - rendered {
		lines = append(lines, "")
	}

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

// helpKey renders a key in accent color inside dim brackets, followed by a dim label.
func helpKey(key, label string) string {
	return dimStyle.Render("[") + activeToggle.Render(key) + dimStyle.Render("]") + helpStyle.Render(label)
}

func (m Model) renderHelp() string {
	if m.focus == focusProvider {
		return helpKey("↑↓", "Navigate ") + helpKey("Enter", "Load ") + helpKey("Tab", "Focus ") + helpKey("Q", "Quit")
	}

	parts := helpKey("Spc", "⏯ ") + helpKey("<>", "Trk ")

	track, _ := m.playlist.Current()
	if !track.Stream || m.player.Seekable() {
		parts += helpKey("←→", "Seek ")
	}

	parts += helpKey("+-", "Vol ") + helpKey("/", "Search ") + helpKey("f", "Find ") + helpKey("a", "Queue ") + helpKey("Tab", "Focus ") + helpKey("Ctrl+K", "Keys ") + helpKey("Q", "Quit")

	return parts
}

// renderStreamStatus shows a network stats line for HTTP streams:
// bytes downloaded, total size (if known), and throughput.
func (m Model) renderStreamStatus() string {
	downloaded, total := m.player.StreamBytes()
	if downloaded == 0 && total <= 0 {
		return ""
	}

	mb := float64(downloaded) / (1024 * 1024)

	var status string
	if total > 0 {
		totalMB := float64(total) / (1024 * 1024)
		pct := float64(downloaded) / float64(total) * 100
		status = fmt.Sprintf("↓ %.1f / %.1f MB (%.0f%%)", mb, totalMB, pct)
	} else {
		status = fmt.Sprintf("↓ %.1f MB", mb)
	}

	if m.networkSpeed > 0 {
		kbs := m.networkSpeed / 1024
		if kbs >= 1024 {
			status += fmt.Sprintf("  %.1f MB/s", kbs/1024)
		} else {
			status += fmt.Sprintf("  %.0f KB/s", kbs)
		}
	}

	w := lipgloss.Width(status)
	pad := panelWidth - w
	if pad > 0 {
		status = strings.Repeat(" ", pad) + status
	}
	return dimStyle.Render(status)
}

// — Navidrome browser renderers —

func (m Model) renderNavBrowser() string {
	switch m.navMode {
	case navBrowseModeMenu:
		return m.renderNavMenu()
	case navBrowseModeByAlbum:
		switch m.navScreen {
		case navBrowseScreenTracks:
			return m.renderNavTrackList()
		default:
			return m.renderNavAlbumList(false)
		}
	case navBrowseModeByArtist:
		switch m.navScreen {
		case navBrowseScreenTracks:
			return m.renderNavTrackList()
		default:
			return m.renderNavArtistList()
		}
	case navBrowseModeByArtistAlbum:
		switch m.navScreen {
		case navBrowseScreenAlbums:
			return m.renderNavAlbumList(true)
		case navBrowseScreenTracks:
			return m.renderNavTrackList()
		default:
			return m.renderNavArtistList()
		}
	}
	return m.renderNavMenu()
}

func (m Model) renderNavMenu() string {
	lines := []string{
		titleStyle.Render("N A V I D R O M E"),
		"",
	}

	items := []string{"By Album", "By Artist", "By Artist / Album"}
	for i, item := range items {
		if i == m.navCursor {
			lines = append(lines, playlistSelectedStyle.Render("> "+item))
		} else {
			lines = append(lines, dimStyle.Render("  "+item))
		}
	}

	lines = append(lines, "",
		helpKey("↑↓", "Navigate ")+helpKey("Enter", "Select ")+helpKey("Esc", "Close"))

	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) renderNavArtistList() string {
	lines := []string{
		titleStyle.Render("A R T I S T S"),
		"",
	}

	if m.navLoading && len(m.navArtists) == 0 {
		lines = append(lines, dimStyle.Render("  Loading artists..."))
		lines = append(lines, "", helpKey("Esc", "Back"))
		return m.centerOverlay(strings.Join(lines, "\n"))
	}

	if len(m.navArtists) == 0 {
		lines = append(lines, dimStyle.Render("  No artists found."))
		lines = append(lines, "", helpKey("Esc", "Back"))
		return m.centerOverlay(strings.Join(lines, "\n"))
	}

	maxVisible := m.plVisible
	if maxVisible < 5 {
		maxVisible = 5
	}

	// Use filtered index when a search is active.
	list := m.navArtists
	useFilter := len(m.navSearchIdx) > 0 || m.navSearch != ""

	scroll := m.navScroll
	rendered := 0
	if useFilter {
		for j := scroll; j < len(m.navSearchIdx) && rendered < maxVisible; j++ {
			i := m.navSearchIdx[j]
			a := list[i]
			label := fmt.Sprintf("%s (%d albums)", a.Name, a.AlbumCount)
			maxW := panelWidth - 6
			lr := []rune(label)
			if len(lr) > maxW {
				label = string(lr[:maxW-1]) + "…"
			}
			if j == m.navCursor {
				lines = append(lines, playlistSelectedStyle.Render("> "+label))
			} else {
				lines = append(lines, dimStyle.Render("  "+label))
			}
			rendered++
		}
	} else {
		for i := scroll; i < len(list) && rendered < maxVisible; i++ {
			a := list[i]
			label := fmt.Sprintf("%s (%d albums)", a.Name, a.AlbumCount)
			maxW := panelWidth - 6
			lr := []rune(label)
			if len(lr) > maxW {
				label = string(lr[:maxW-1]) + "…"
			}
			if i == m.navCursor {
				lines = append(lines, playlistSelectedStyle.Render("> "+label))
			} else {
				lines = append(lines, dimStyle.Render("  "+label))
			}
			rendered++
		}
	}

	// Pad to fixed height.
	for range maxVisible - rendered {
		lines = append(lines, "")
	}

	if useFilter {
		lines = append(lines, "",
			dimStyle.Render(fmt.Sprintf("  %d/%d artists (filtered)", len(m.navSearchIdx), len(list))))
	} else {
		lines = append(lines, "",
			dimStyle.Render(fmt.Sprintf("  %d/%d artists", m.navCursor+1, len(list))))
	}

	// Search bar or hint.
	if m.navSearching {
		lines = append(lines, "", playlistSelectedStyle.Render("  / "+m.navSearch+"_"))
	} else if m.navSearch != "" {
		lines = append(lines, "", dimStyle.Render("  / "+m.navSearch)+" "+helpKey("/", "Clear"))
	} else {
		lines = append(lines, "", helpKey("←↑↓→", "Navigate ")+helpKey("Enter", "Open ")+helpKey("/", "Search"))
	}

	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) renderNavAlbumList(artistAlbums bool) string {
	var titleStr string
	if artistAlbums {
		titleStr = titleStyle.Render("A L B U M S : " + m.navSelArtist.Name)
	} else {
		titleStr = titleStyle.Render("A L B U M S")
	}

	lines := []string{titleStr, ""}

	if !artistAlbums {
		sortLabel := navidrome.SortTypeLabel(m.navSortType)
		lines = append(lines, dimStyle.Render("  Sort: ")+activeToggle.Render(sortLabel), "")
	}

	if m.navLoading && len(m.navAlbums) == 0 {
		lines = append(lines, dimStyle.Render("  Loading albums..."))
		if artistAlbums {
			lines = append(lines, "", helpKey("Esc", "Back"))
		} else {
			lines = append(lines, "", helpKey("s", "Sort ")+helpKey("Esc", "Back"))
		}
		return m.centerOverlay(strings.Join(lines, "\n"))
	}

	if len(m.navAlbums) == 0 {
		lines = append(lines, dimStyle.Render("  No albums found."))
		if artistAlbums {
			lines = append(lines, "", helpKey("Esc", "Back"))
		} else {
			lines = append(lines, "", helpKey("s", "Sort ")+helpKey("Esc", "Back"))
		}
		return m.centerOverlay(strings.Join(lines, "\n"))
	}

	maxVisible := m.plVisible
	if maxVisible < 5 {
		maxVisible = 5
	}

	list := m.navAlbums
	useFilter := len(m.navSearchIdx) > 0 || m.navSearch != ""

	scroll := m.navScroll
	rendered := 0
	if useFilter {
		for j := scroll; j < len(m.navSearchIdx) && rendered < maxVisible; j++ {
			i := m.navSearchIdx[j]
			a := list[i]
			var label string
			if a.Year > 0 {
				label = fmt.Sprintf("%s — %s (%d)", a.Name, a.Artist, a.Year)
			} else {
				label = fmt.Sprintf("%s — %s", a.Name, a.Artist)
			}
			maxW := panelWidth - 6
			lr := []rune(label)
			if len(lr) > maxW {
				label = string(lr[:maxW-1]) + "…"
			}
			if j == m.navCursor {
				lines = append(lines, playlistSelectedStyle.Render("> "+label))
			} else {
				lines = append(lines, dimStyle.Render("  "+label))
			}
			rendered++
		}
	} else {
		for i := scroll; i < len(list) && rendered < maxVisible; i++ {
			a := list[i]
			var label string
			if a.Year > 0 {
				label = fmt.Sprintf("%s — %s (%d)", a.Name, a.Artist, a.Year)
			} else {
				label = fmt.Sprintf("%s — %s", a.Name, a.Artist)
			}
			maxW := panelWidth - 6
			lr := []rune(label)
			if len(lr) > maxW {
				label = string(lr[:maxW-1]) + "…"
			}
			if i == m.navCursor {
				lines = append(lines, playlistSelectedStyle.Render("> "+label))
			} else {
				lines = append(lines, dimStyle.Render("  "+label))
			}
			rendered++
		}
	}

	// Pad to fixed height.
	for range maxVisible - rendered {
		lines = append(lines, "")
	}

	// Loading indicator when fetching next page.
	if m.navAlbumLoading {
		lines = append(lines, dimStyle.Render("  Loading more..."))
	} else if useFilter {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("  %d/%d albums (filtered)", len(m.navSearchIdx), len(list))))
	} else {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("  %d/%d albums", m.navCursor+1, len(list))))
	}
	lines = append(lines, "")

	// Search bar or help line.
	if m.navSearching {
		lines = append(lines, playlistSelectedStyle.Render("  / "+m.navSearch+"_"))
	} else if m.navSearch != "" {
		lines = append(lines, dimStyle.Render("  / "+m.navSearch)+" "+helpKey("/", "Clear"))
	} else if artistAlbums {
		lines = append(lines,
			helpKey("←↑↓→", "Navigate ")+helpKey("Enter", "Open ")+helpKey("/", "Search"))
	} else {
		lines = append(lines,
			helpKey("←↑↓→", "Navigate ")+helpKey("Enter", "Open ")+helpKey("s", "Sort ")+helpKey("/", "Search"))
	}

	return m.centerOverlay(strings.Join(lines, "\n"))
}

func (m Model) renderNavTrackList() string {
	// Build a breadcrumb title based on the active mode.
	var breadcrumb string
	switch m.navMode {
	case navBrowseModeByArtist:
		breadcrumb = "A R T I S T : " + m.navSelArtist.Name
	case navBrowseModeByAlbum:
		breadcrumb = "A L B U M : " + m.navSelAlbum.Name
	case navBrowseModeByArtistAlbum:
		breadcrumb = m.navSelArtist.Name + " / " + m.navSelAlbum.Name
	}

	lines := []string{titleStyle.Render(breadcrumb), ""}

	if m.navLoading && len(m.navTracks) == 0 {
		lines = append(lines, dimStyle.Render("  Loading tracks..."))
		lines = append(lines, "", helpKey("Esc", "Back"))
		return m.centerOverlay(strings.Join(lines, "\n"))
	}

	if len(m.navTracks) == 0 {
		lines = append(lines, dimStyle.Render("  No tracks found."))
		lines = append(lines, "", helpKey("Esc", "Back"))
		return m.centerOverlay(strings.Join(lines, "\n"))
	}

	maxVisible := m.plVisible
	if maxVisible < 5 {
		maxVisible = 5
	}

	list := m.navTracks
	useFilter := len(m.navSearchIdx) > 0 || m.navSearch != ""

	scroll := m.navScroll
	rendered := 0

	if useFilter {
		for j := scroll; j < len(m.navSearchIdx) && rendered < maxVisible; j++ {
			i := m.navSearchIdx[j]
			t := list[i]
			name := t.DisplayName()
			maxW := panelWidth - 8
			nr := []rune(name)
			if len(nr) > maxW {
				name = string(nr[:maxW-1]) + "…"
			}
			label := fmt.Sprintf("%d. %s", i+1, name)
			if j == m.navCursor {
				lines = append(lines, playlistSelectedStyle.Render("> "+label))
			} else {
				lines = append(lines, dimStyle.Render("  "+label))
			}
			rendered++
		}
	} else {
		prevAlbum := ""
		if scroll > 0 {
			prevAlbum = list[scroll-1].Album
		}

		for i := scroll; i < len(list) && rendered < maxVisible; i++ {
			t := list[i]

			// Album separator header when album changes.
			if album := t.Album; album != "" && album != prevAlbum {
				label := "── " + album
				if t.Year != 0 {
					label += fmt.Sprintf(" (%d)", t.Year)
				}
				label += " "
				labelLen := len([]rune(label))
				if labelLen < panelWidth {
					label += strings.Repeat("─", panelWidth-labelLen)
				}
				lines = append(lines, dimStyle.Render(label))
				if rendered >= maxVisible {
					break
				}
			}
			prevAlbum = t.Album

			name := t.DisplayName()
			maxW := panelWidth - 8
			nr := []rune(name)
			if len(nr) > maxW {
				name = string(nr[:maxW-1]) + "…"
			}

			label := fmt.Sprintf("%d. %s", i+1, name)
			if i == m.navCursor {
				lines = append(lines, playlistSelectedStyle.Render("> "+label))
			} else {
				lines = append(lines, dimStyle.Render("  "+label))
			}
			rendered++
		}
	}

	// Pad to fixed height.
	for range maxVisible - rendered {
		lines = append(lines, "")
	}

	if useFilter {
		lines = append(lines, "",
			dimStyle.Render(fmt.Sprintf("  %d/%d tracks (filtered)", len(m.navSearchIdx), len(list))))
	} else {
		lines = append(lines, "",
			dimStyle.Render(fmt.Sprintf("  %d/%d tracks", m.navCursor+1, len(list))))
	}

	// Search bar or help line.
	if m.navSearching {
		lines = append(lines, "", playlistSelectedStyle.Render("  / "+m.navSearch+"_"))
	} else if m.navSearch != "" {
		lines = append(lines, "", dimStyle.Render("  / "+m.navSearch)+" "+helpKey("/", "Clear"))
	} else {
		lines = append(lines, "",
			helpKey("←↑↓→", "Navigate ")+
				helpKey("Enter", "Play ")+
				helpKey("q", "Queue ")+
				helpKey("R", "Replace ")+
				helpKey("a", "Append ")+
				helpKey("/", "Search"))
	}

	if m.saveMsg != "" {
		lines = append(lines, "", statusStyle.Render(m.saveMsg))
	}

	return m.centerOverlay(strings.Join(lines, "\n"))
}
