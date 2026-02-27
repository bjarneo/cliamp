package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"cliamp/playlist"
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
		m.renderVolume(),
		m.renderEQ(),
		"",
		// Playlist
		m.renderPlaylistHeader(),
		m.renderPlaylist(),
		"",
		// Help
		m.renderHelp(),
	}

	if m.err != nil {
		sections = append(sections, errorStyle.Render(fmt.Sprintf("ERR: %s", m.err)))
	}
	if m.saveMsg != "" {
		sections = append(sections, statusStyle.Render(m.saveMsg))
	}

	content := strings.Join(sections, "\n")
	frame := frameStyle.Render(content)

	// Center horizontally and vertically within the terminal
	frameW := lipgloss.Width(frame)
	frameH := lipgloss.Height(frame)

	padLeft := max(0, (m.width-frameW)/2)
	padTop := max(0, (m.height-frameH)/2)

	return strings.Repeat("\n", padTop) +
		lipgloss.NewStyle().MarginLeft(padLeft).Render(frame)
}

func (m Model) renderKeymapOverlay() string {
	keys := []struct{ key, action string }{
		{"Space", "Play / Pause"},
		{"s", "Stop"},
		{"> .", "Next track"},
		{"< ,", "Previous track"},
		{"← →", "Seek ±5s"},
		{"+ -", "Volume up/down"},
		{"m", "Toggle mono"},
		{"e", "Cycle EQ preset"},
		{"t", "Choose theme"},
		{"v", "Cycle visualizer"},
		{"↑ ↓", "Playlist scroll / EQ adjust"},
		{"h l", "EQ cursor left/right"},
		{"Enter", "Play selected track"},
		{"a", "Toggle queue (play next)"},
		{"S", "Save track to ~/Music"},
		{"r", "Cycle repeat"},
		{"z", "Toggle shuffle"},
		{"/", "Search playlist"},
		{"Tab", "Toggle focus"},
		{"Esc", "Back to provider"},
		{"Ctrl+K", "This keymap"},
		{"q", "Quit"},
	}

	lines := []string{
		titleStyle.Render("K E Y M A P"),
		"",
	}
	for _, k := range keys {
		line := fmt.Sprintf("  %-10s %s", k.key, k.action)
		lines = append(lines, dimStyle.Render(line))
	}
	lines = append(lines, "", helpStyle.Render("Press any key to close"))

	content := strings.Join(lines, "\n")
	frame := frameStyle.Render(content)

	frameW := lipgloss.Width(frame)
	frameH := lipgloss.Height(frame)
	padLeft := max(0, (m.width-frameW)/2)
	padTop := max(0, (m.height-frameH)/2)

	return strings.Repeat("\n", padTop) +
		lipgloss.NewStyle().MarginLeft(padLeft).Render(frame)
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

	lines = append(lines, "", helpStyle.Render("[↑↓]Navigate [Enter]Select [Esc]Cancel"))

	content := strings.Join(lines, "\n")
	frame := frameStyle.Render(content)

	frameW := lipgloss.Width(frame)
	frameH := lipgloss.Height(frame)
	padLeft := max(0, (m.width-frameW)/2)
	padTop := max(0, (m.height-frameH)/2)

	return strings.Repeat("\n", padTop) +
		lipgloss.NewStyle().MarginLeft(padLeft).Render(frame)
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

	if len(runes) <= maxW {
		return trackStyle.Render("♫ " + name)
	}

	// Cyclic scrolling for long titles
	sep := []rune("   ♫   ")
	padded := append(runes, sep...)
	total := len(padded)
	off := m.titleOff % total

	display := make([]rune, maxW)
	for i := range maxW {
		display[i] = padded[(off+i)%total]
	}
	return trackStyle.Render("♫ " + string(display))
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
	bands := m.vis.Analyze(m.player.Samples())
	return m.vis.Render(bands)
}

func (m Model) renderSeekBar() string {
	// Show a static streaming bar for non-seekable streams
	if !m.player.Seekable() && m.player.IsPlaying() {
		label := " STREAMING "
		pad := panelWidth - len(label)
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

func (m Model) renderVolume() string {
	vol := m.player.Volume()
	frac := max(0, min(1, (vol+30)/36))

	barW := 30
	filled := int(frac * float64(barW))

	bar := volBarStyle.Render(strings.Repeat("█", filled)) +
		dimStyle.Render(strings.Repeat("░", barW-filled))

	line := labelStyle.Render("VOL ") + bar + dimStyle.Render(fmt.Sprintf(" %+.1fdB", vol))
	if m.player.Mono() {
		line += " " + activeToggle.Render("[Mono]")
	}
	return line
}

func (m Model) renderEQ() string {
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
	presetLabel := dimStyle.Render(" [" + presetName + "]")
	return labelStyle.Render("EQ  ") + strings.Join(parts, " ") + presetLabel
}

func (m Model) renderPlaylistHeader() string {
	if m.focus == focusProvider {
		return dimStyle.Render(fmt.Sprintf("── %s Playlists ── ", m.provider.Name()))
	}

	var shuffle string
	if m.playlist.Shuffled() {
		shuffle = activeToggle.Render("[Shuffle]")
	} else {
		shuffle = dimStyle.Render("[Shuffle]")
	}

	repeatStr := fmt.Sprintf("[Repeat: %s]", m.playlist.Repeat())
	if m.playlist.Repeat() != 0 {
		repeatStr = activeToggle.Render(repeatStr)
	} else {
		repeatStr = dimStyle.Render(repeatStr)
	}

	var queueStr string
	if qLen := m.playlist.QueueLen(); qLen > 0 {
		queueStr = " " + activeToggle.Render(fmt.Sprintf("[Queue: %d]", qLen))
	}

	var themeStr string
	if name := m.ThemeName(); name != theme.DefaultName {
		themeStr = " " + activeToggle.Render("[Theme: "+name+"]")
	}

	return dimStyle.Render("── Playlist ── ") + shuffle + " " + repeatStr + queueStr + themeStr + " " + dimStyle.Render("──")
}

func (m Model) renderPlaylist() string {
	if m.focus == focusProvider {
		if m.provLoading {
			return dimStyle.Render(fmt.Sprintf("  Loading %s...", m.provider.Name()))
		}
		if len(m.providerLists) == 0 {
			return dimStyle.Render("  No playlists found.")
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

	if m.searching {
		return m.renderSearchResults(tracks)
	}

	currentIdx := m.playlist.Index()
	visible := min(m.plVisible, len(tracks))

	scroll := m.plScroll
	if scroll+visible > len(tracks) {
		scroll = len(tracks) - visible
	}
	scroll = max(0, scroll)

	lines := make([]string, 0, visible)
	for i := scroll; i < scroll+visible && i < len(tracks); i++ {
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

func (m Model) renderSearchResults(tracks []playlist.Track) string {
	if len(m.searchResults) == 0 {
		if m.searchQuery != "" {
			return dimStyle.Render("  No matches")
		}
		return dimStyle.Render("  Type to search…")
	}

	currentIdx := m.playlist.Index()
	visible := min(m.plVisible, len(m.searchResults))

	// Scroll the search results so the cursor is always visible
	scroll := 0
	if m.searchCursor >= visible {
		scroll = m.searchCursor - visible + 1
	}

	lines := make([]string, 0, visible)
	for j := scroll; j < scroll+visible && j < len(m.searchResults); j++ {
		i := m.searchResults[j]
		prefix := "  "
		style := playlistItemStyle

		if i == currentIdx && m.player.IsPlaying() {
			prefix = "▶ "
			style = playlistActiveStyle
		}

		if j == m.searchCursor {
			style = playlistSelectedStyle
		}

		name := tracks[i].DisplayName()
		maxW := panelWidth - 6
		nameRunes := []rune(name)
		if len(nameRunes) > maxW {
			name = string(nameRunes[:maxW-1]) + "…"
		}

		lines = append(lines, style.Render(fmt.Sprintf("%s%d. %s", prefix, i+1, name)))
	}

	return strings.Join(lines, "\n")
}

func (m Model) renderHelp() string {
	if m.searching {
		query := m.searchQuery
		count := len(m.searchResults)
		return helpStyle.Render(fmt.Sprintf("/ %s  (%d found)  [↑↓]Navigate [Enter]Play [Esc]Cancel", query, count))
	}
	if m.focus == focusProvider {
		return helpStyle.Render("[↑↓]Navigate  [Enter]Load Playlist  [Tab]Focus  [Q]Quit")
	}

	help := "[Spc]⏯  [<>]Trk "

	// Hide seek hint for non-seekable streams
	track, _ := m.playlist.Current()
	if !track.Stream || m.player.Seekable() {
		help += "[←→]Seek "
	}

	if !track.Stream && strings.HasPrefix(track.Path, os.TempDir()) {
		help += "[S]Save "
	}
	help += "[+-]Vol [m]Mono [e]EQ [t]Theme [v]Vis [a]Queue [/]Search "

	// Conditionally show the back button if a provider is configured
	if m.provider != nil {
		help += "[Esc]Back "
	}

	help += "[Tab]Focus [Q]Quit"

	return helpStyle.Render(help)
}
