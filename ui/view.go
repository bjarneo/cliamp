package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

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

	if m.jumping {
		return m.renderJumpOverlay()
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
		album = truncate(album, maxW)
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

	return m.centerOverlay(strings.Join(sections, "\n"))
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

func (m Model) renderProviderList() string {
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

func (m Model) renderPlaylist() string {
	if m.focus == focusProvider {
		return m.renderProviderList()
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
			lines = append(lines, albumSeparator(album, tracks[i].Year))
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
		name = truncate(name, panelWidth-6-len([]rune(queueSuffix)))

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

func (m Model) renderJumpOverlay() string {
	pos := m.player.Position()
	dur := m.player.Duration()
	timeLine := fmt.Sprintf("%s / %s", formatJumpClock(pos), formatJumpClock(dur))
	inputLine := dimStyle.Faint(true).Render("  " + formatJumpPlaceholder(dur))
	if m.jumpInput != "" {
		inputLine = playlistSelectedStyle.Render("  " + m.jumpInput + "_")
	}

	lines := []string{
		titleStyle.Render("J U M P  T O  T I M E"),
		"",
		dimStyle.Render("  " + timeLine),
		"",
		inputLine,
	}

	lines = append(lines, "", helpKey("Enter", "Jump ")+helpKey("Esc", "Cancel"))
	return m.centerOverlay(strings.Join(lines, "\n"))
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
