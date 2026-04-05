package model

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"cliamp/playlist"
	"cliamp/provider"
	"cliamp/theme"
	"cliamp/ui"
)

// titleScrollSep is the separator runes for cyclic title scrolling,
// pre-allocated to avoid per-frame conversion.
var titleScrollSep = []rune("   ♫   ")

// Pre-built styles for elements created per-render to avoid repeated allocation.
var (
	seekFillStyle = lipgloss.NewStyle().Foreground(ui.ColorSeekBar)
	seekDimStyle  = lipgloss.NewStyle().Foreground(ui.ColorDim)
	volBarStyle   = lipgloss.NewStyle().Foreground(ui.ColorVolume)
	activeToggle  = lipgloss.NewStyle().Foreground(ui.ColorAccent).Bold(true)
)

// playlistLabel formats a playlist entry, omitting the track count when it is
// unknown (zero). This avoids showing "(0 tracks)" for providers such as Plex
// that do not return a track count in their album list responses.
func playlistLabel(prefix string, p playlist.PlaylistInfo) string {
	if p.TrackCount > 0 {
		return fmt.Sprintf("%s%s (%d tracks)", prefix, p.Name, p.TrackCount)
	}
	return prefix + p.Name
}

// View renders the full TUI frame.
func (m Model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}

	screen := m.activeScreen()
	if !screen.hidesVisualizer() {
		m.refreshVisualizerIfPending()
	}

	var content string
	switch screen {
	case screenKeymap:
		content = m.renderKeymapOverlay()
	case screenThemePicker:
		content = m.renderThemePicker()
	case screenDevicePicker:
		content = m.renderDeviceOverlay()
	case screenFileBrowser:
		content = m.renderFileBrowser()
	case screenNavBrowser:
		content = m.renderNavBrowser()
	case screenPlaylistManager:
		content = m.renderPlaylistManager()
	case screenSpotSearch:
		content = m.renderSpotSearch()
	case screenQueue:
		content = m.renderQueueOverlay()
	case screenInfo:
		content = m.renderInfoOverlay()
	case screenSearch:
		content = m.renderSearchOverlay()
	case screenNetSearch:
		content = m.renderNetSearchOverlay()
	case screenURLInput:
		content = m.renderURLInputOverlay()
	case screenLyrics:
		content = m.renderLyricsOverlay()
	case screenJump:
		content = m.renderJumpOverlay()
	case screenFullVisualizer:
		content = m.renderFullVisualizer()
	default:
		content = strings.Join(m.mainSections(m.renderPlaylist(), true), "\n")
	}

	rendered := content
	if screen == screenMain || screen == screenFullVisualizer {
		rendered = m.centerFrame(ui.FrameStyle.Render(content))
	}

	view := tea.NewView(rendered)
	view.AltScreen = true
	view.WindowTitle = currentTerminalTitle(m.termTitle, m.width, m.terminalTitleValues())
	return view
}

func trimTrailingEmpty(sections []string) []string {
	for len(sections) > 0 && sections[len(sections)-1] == "" {
		sections = sections[:len(sections)-1]
	}
	return sections
}

func appendFooter(lines, footer []string) []string {
	if len(footer) == 0 {
		return lines
	}
	lines = append(lines, "")
	lines = append(lines, footer...)
	return lines
}

func (m Model) mainSections(playlist string, includeTransient bool) []string {
	sections := []string{
		// Now playing
		m.renderTitle(),
		m.renderTrackInfo(),
		m.renderTimeStatus(),
		"",
		// ui.Visualizer
		m.renderSpectrum(),
		m.renderSeekBar(),
		"",
		// Controls
		m.renderControls(),
		m.renderProviderPill(),
		"",
		// Playlist
		m.renderPlaylistHeader(),
	}
	if playlist != "" {
		sections = append(sections, playlist)
	}
	sections = append(sections,
		"",
		// Help
		m.renderHelp(),
		m.renderBottomStatus(),
	)

	if includeTransient {
		if m.err != nil {
			sections = append(sections, errorStyle.Render(fmt.Sprintf("ERR: %s", m.err)))
		}
		sections = append(sections, m.footerMessages()...)
	}

	return trimTrailingEmpty(sections)
}

func (m Model) footerMessages() []string {
	var lines []string
	if text := m.save.activityText(); text != "" {
		lines = append(lines, statusStyle.Render(text))
	}
	if m.status.text != "" {
		lines = append(lines, statusStyle.Render(m.status.text))
	}
	for _, l := range m.logLines {
		lines = append(lines, dimStyle.Render(l.text))
	}
	return lines
}

func (m Model) appendFooterMessages(lines []string) []string {
	return appendFooter(lines, m.footerMessages())
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
	return m.centerFrame(ui.FrameStyle.Render(content))
}

func (m Model) renderTitle() string {
	title := titleStyle.Render("C L I A M P")
	label := m.focus.label()
	if label == "" {
		return title
	}
	indicator := dimStyle.Render("[" + label + "]")
	gap := ui.PanelWidth - lipgloss.Width(title) - lipgloss.Width(indicator)
	if gap < 1 {
		gap = 1
	}
	return title + strings.Repeat(" ", gap) + indicator
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

	// Append album to the title line to save vertical space.
	// The album is truncated (never scrolled) so artist/song stays readable.
	album := track.Album
	if m.streamTitle != "" && track.Stream {
		album = ""
	}

	maxW := ui.PanelWidth - 4
	nameRunes := []rune(name)

	if album != "" {
		sep := " · "
		sepLen := len([]rune(sep))
		remaining := maxW - len(nameRunes) - sepLen
		if remaining >= 4 { // enough room for at least a few album chars
			name += sep + truncate(album, remaining)
		} else if remaining >= 0 { // very tight — skip album entirely
			// name stays as-is
		} else {
			// name itself is longer than maxW, album won't help
		}
	}

	runes := []rune(name)

	if len(runes) <= maxW {
		return trackStyle.Render("♫ " + name)
	}
	// Cyclic scrolling for long titles (only artist/song, album already handled)
	padded := append(runes, titleScrollSep...)
	total := len(padded)
	off := m.titleOff % total

	display := make([]rune, maxW)
	for i := range maxW {
		display[i] = padded[(off+i)%total]
	}
	return trackStyle.Render("♫ " + string(display))
}

func (m Model) renderTimeStatus() string {
	// Use per-tick cached values to avoid repeated speaker.Lock() calls.
	pos := m.cachedPos
	dur := m.cachedDur

	posMin := int(pos.Minutes())
	posSec := int(pos.Seconds()) % 60
	durMin := int(dur.Minutes())
	durSec := int(dur.Seconds()) % 60

	timeStr := fmt.Sprintf("%02d:%02d / %02d:%02d", posMin, posSec, durMin, durSec)

	track, _ := m.playlist.Current()

	var status string
	switch {
	case m.seek.active:
		status = statusStyle.Render("⟳ Seeking...")
	case m.buffering:
		if elapsed := int(time.Since(m.bufferingAt).Seconds()); elapsed > 0 {
			status = statusStyle.Render(fmt.Sprintf("◌ Buffering... (%ds)", elapsed))
		} else {
			status = statusStyle.Render("◌ Buffering...")
		}
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
	gap := ui.PanelWidth - lipgloss.Width(left) - lipgloss.Width(status)
	if gap < 1 {
		gap = 1
	}

	return left + strings.Repeat(" ", gap) + status
}

func (m Model) renderSpectrum() string {
	if m.vis.Mode == ui.VisNone {
		return ""
	}
	return m.vis.Render()
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
		helpKey("V", "Exit ") + helpKey("v", "Mode:"+m.vis.ModeName()+" ") + helpKey("Spc", "▶❚❚ ") + helpKey("<>", "Trk ") + helpKey("+-", "Vol"),
	}

	return strings.Join(sections, "\n")
}

func (m Model) renderSeekBar() string {
	if ui.PanelWidth <= 0 {
		return ""
	}
	// During buffering, show a dim bar — avoids speaker.Lock() contention.
	if m.buffering {
		return seekDimStyle.Render(strings.Repeat("━", ui.PanelWidth))
	}
	// Show a static streaming bar for non-seekable streams with no known duration.
	if !m.player.Seekable() && m.player.IsPlaying() && m.cachedDur == 0 {
		label := " STREAMING "
		pad := ui.PanelWidth - lipgloss.Width(label)
		if pad < 0 {
			return seekFillStyle.Render(label[:ui.PanelWidth])
		}
		left := pad / 2
		right := pad - left
		return seekFillStyle.Render(strings.Repeat("━", left) + label + strings.Repeat("━", right))
	}

	pos := m.cachedPos
	dur := m.cachedDur

	var progress float64
	if dur > 0 {
		progress = float64(pos) / float64(dur)
	}
	progress = max(0, min(1, progress))

	filled := int(progress * float64(max(1, ui.PanelWidth-1)))

	return seekFillStyle.Render(strings.Repeat("━", filled)) +
		seekFillStyle.Render("●") +
		seekDimStyle.Render(strings.Repeat("━", max(0, ui.PanelWidth-filled-1)))
}

func (m Model) renderControls() string {
	// ── EQ [Preset] (left)  ·····  VOL bar dB [Mono] (right) ──

	bands := m.player.EQBands()
	presetName := m.EQPresetName()

	eqParts := make([]string, 10)
	eqLabels := [10]string{"70", "180", "320", "600", "1k", "3k", "6k", "12k", "14k", "16k"}
	for i, label := range eqLabels {
		style := eqInactiveStyle
		if bands[i] != 0 {
			label = fmt.Sprintf("%+.0f", bands[i])
		}
		if m.focus == focusEQ && i == m.eqCursor {
			style = eqActiveStyle
		}
		eqParts[i] = style.Render(label)
	}

	eqLabel := labelStyle.Render("EQ ")
	if m.focus == focusEQ {
		eqLabel = activeToggle.Render("EQ ▸ ")
	}
	left := eqLabel + dimStyle.Render("[") + activeToggle.Render(presetName) + dimStyle.Render("] ") + strings.Join(eqParts, " ")

	vol := m.player.Volume()
	frac := max(0, min(1, (vol+30)/36))
	dbStr := fmt.Sprintf(" %+.0fdB", vol)
	monoStr := ""
	if m.player.Mono() {
		monoStr = " " + activeToggle.Render("[M]")
	}

	leftW := lipgloss.Width(left)
	volLabel := labelStyle.Render("VOL ")
	volSuffix := dimStyle.Render(dbStr) + monoStr
	volLabelW := lipgloss.Width(volLabel)
	volSuffixW := lipgloss.Width(volSuffix)
	barW := max(6, (ui.PanelWidth-leftW-2-volLabelW-volSuffixW)*3/4)
	filled := int(frac * float64(barW))

	bar := volBarStyle.Render(strings.Repeat("█", filled)) +
		dimStyle.Render(strings.Repeat("░", barW-filled))

	right := volLabel + bar + volSuffix
	rightW := lipgloss.Width(right)
	gap := max(1, ui.PanelWidth-leftW-rightW)

	return left + strings.Repeat(" ", gap) + right
}

func (m Model) renderProviderPill() string {
	if len(m.providers) <= 1 {
		return ""
	}

	var pills []string
	for i, pe := range m.providers {
		name := pe.Name
		if m.focus == focusProvPill && i == m.provPillIdx {
			pills = append(pills, activeToggle.Render("["+name+"]"))
		} else if i == m.provPillIdx {
			pills = append(pills, dimStyle.Render("[")+trackStyle.Render(name)+dimStyle.Render("]"))
		} else {
			pills = append(pills, dimStyle.Render("["+name+"]"))
		}
	}

	srcLabel := labelStyle.Render("SRC ")
	if m.focus == focusProvPill {
		srcLabel = activeToggle.Render("SRC ▸ ")
	}
	return srcLabel + strings.Join(pills, " ")
}

func (m Model) renderPlaylistHeader() string {
	if m.focus == focusProvider {
		return dimStyle.Render(fmt.Sprintf("── %s Playlists ──", m.provider.Name()))
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

	var favStr string
	if favCount := m.playlist.FavoriteCount(); favCount > 0 {
		favStr = " " + activeToggle.Render(fmt.Sprintf("[★ %d]", favCount))
	}

	var themeStr string
	if name := m.ThemeName(); name != theme.DefaultName {
		themeStr = " " + activeToggle.Render("[Theme: "+name+"]")
	}

	var posStr string
	if total := m.playlist.Len(); total > 0 {
		posStr = " " + dimStyle.Render(fmt.Sprintf("[%d/%d]", m.playlist.Index()+1, total))
	}

	headerStyle := dimStyle
	headerLabel := "── Playlist ── "
	if m.focus == focusPlaylist {
		headerStyle = activeToggle
		headerLabel = "▸─ Playlist ── "
	}
	return headerStyle.Render(headerLabel) + shuffle + queueStr + favStr + posStr + themeStr + " " + dimStyle.Render("──")
}

func (m Model) renderProviderList() string {
	visibleBudget := m.effectivePlaylistVisible()
	if visibleBudget <= 0 {
		return ""
	}
	if m.provSignIn {
		return dimStyle.Render(fmt.Sprintf("  Sign in to %s. Press Enter to continue.", m.provider.Name()))
	}
	if m.provLoading {
		return dimStyle.Render(fmt.Sprintf("  Loading %s...", m.provider.Name()))
	}
	if len(m.providerLists) == 0 {
		return dimStyle.Render("  No playlists found.\n  Add playlists to ~/.config/cliamp/playlists/")
	}

	sl, isRadio := m.provider.(provider.SectionedList)
	var lines []string

	if m.provSearch.active {
		lines = append(lines, playlistSelectedStyle.Render("  / "+m.provSearch.query+"_"))

		if isRadio {
			if m.provSearch.query == "" {
				lines = append(lines, dimStyle.Render("  Type a station name, Enter to search…"))
			} else {
				lines = append(lines, dimStyle.Render("  Press Enter to search"))
			}
		} else {
			if m.provSearch.query == "" {
				lines = append(lines, dimStyle.Render("  Type to filter…"))
			} else if len(m.provSearch.results) == 0 {
				lines = append(lines, dimStyle.Render("  No matches"))
			} else {
				visible := max(0, min(visibleBudget-1, len(m.provSearch.results)))
				scroll := max(0, m.provSearch.cursor-visible+1)
				for j := scroll; j < scroll+visible && j < len(m.provSearch.results); j++ {
					idx := m.provSearch.results[j]
					p := m.providerLists[idx]
					prefix, style := "  ", playlistItemStyle
					if j == m.provSearch.cursor {
						style = playlistSelectedStyle
						prefix = "> "
					}
					lines = append(lines, style.Render(playlistLabel(prefix, p)))
				}
				lines = append(lines, dimStyle.Render(fmt.Sprintf("  %d/%d playlists", len(m.provSearch.results), len(m.providerLists))))
			}
		}
	} else {
		scroll := max(0, m.provScroll)
		if scroll >= len(m.providerLists) {
			scroll = max(0, len(m.providerLists)-1)
		}
		if m.provCursor < scroll {
			scroll = m.provCursor
		}

		if isRadio {
			for scroll < len(m.providerLists)-1 && m.providerRowsFromScroll(sl, scroll, m.provCursor) > visibleBudget {
				scroll++
			}
		} else if m.provCursor >= scroll+visibleBudget {
			scroll = m.provCursor - visibleBudget + 1
		}

		prevPrefix := ""
		if isRadio && scroll > 0 {
			prevPrefix = sl.IDPrefix(m.providerLists[scroll-1].ID)
		}

		for j := scroll; j < len(m.providerLists) && len(lines) < visibleBudget; j++ {
			p := m.providerLists[j]

			if isRadio {
				pfx := sl.IDPrefix(p.ID)
				if pfx != prevPrefix {
					var header string
					switch pfx {
					case "f":
						header = "  ── favorites ──"
					case "c":
						header = "  ── catalog ──"
					case "s":
						header = "  ── search results ──"
					}
					if header != "" && len(lines) < visibleBudget {
						lines = append(lines, dimStyle.Render(header))
					}
					prevPrefix = pfx
				}
			}

			if len(lines) >= visibleBudget {
				break
			}

			prefix, style := "  ", playlistItemStyle
			if j == m.provCursor {
				style = playlistSelectedStyle
				prefix = "> "
			}
			lines = append(lines, style.Render(playlistLabel(prefix, p)))
		}
	}

	// Loading indicator for catalog batch (never displace selected row if full).
	if isRadio && m.catalogBatch.loading && len(lines) < visibleBudget {
		lines = append(lines, dimStyle.Render("  Loading more stations..."))
	}

	// Clamp exactly to visible budget so footer/help remain visible.
	if len(lines) > visibleBudget {
		lines = lines[:visibleBudget]
	}
	for len(lines) < visibleBudget {
		lines = append(lines, "")
	}

	return strings.Join(lines, "\n")
}

func (m Model) renderPlaylist() string {
	budget := m.effectivePlaylistVisible()
	if budget <= 0 {
		return ""
	}

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
	scroll := m.playlistScroll(budget)

	// budget is the number of rendered lines available for tracks.
	// The loop below counts every appended line against this budget
	// so the playlist never overflows its area.
	lines := make([]string, 0, budget) // tracks
	numWidth := len(fmt.Sprintf("%d", len(tracks)))
	for i := scroll; i < len(tracks) && len(lines) < budget; i++ {
		prefix := "  "
		style := playlistItemStyle

		if i == currentIdx && m.player.IsPlaying() {
			prefix = "▶ "
			style = playlistActiveStyle
		} else if strings.HasPrefix(tracks[i].Path, "ssh://") {
			prefix = "↗ "
		}

		if m.focus == focusPlaylist && i == m.plCursor {
			style = playlistSelectedStyle
		}

		name := tracks[i].DisplayName()
		isFav := tracks[i].Favorite
		favBudget := 0
		if isFav {
			favBudget = 2 // "★ "
		}
		queueSuffix := ""
		if qp := m.playlist.QueuePosition(i); qp > 0 {
			queueSuffix = fmt.Sprintf(" [Q%d]", qp)
		}
		queueLen := utf8.RuneCountInString(queueSuffix)

		linePrefixWidth := utf8.RuneCountInString(prefix) + numWidth + 2 // 2 for ". "

		// Truncate the track name only against queue/fav overhead, never album.
		name = truncate(name, ui.PanelWidth-linePrefixWidth-queueLen-favBudget)
		// Truncate the album to fit whatever space remains after the track name.
		albumSuffix := ""
		if album := tracks[i].Album; album != "" {
			nameLen := utf8.RuneCountInString(name)
			remaining := ui.PanelWidth - linePrefixWidth - favBudget - nameLen - queueLen - 3 // 3 = " · "
			if remaining >= 4 {
				albumSuffix = " · " + truncate(album, remaining)
			}
		}

		numStr := fmt.Sprintf("%s%*d. ", prefix, numWidth, i+1)
		line := style.Render(numStr)
		if isFav {
			line += activeToggle.Render("★ ")
		}
		line += style.Render(name)
		if albumSuffix != "" {
			line += dimStyle.Render(albumSuffix)
		}
		if queueSuffix != "" {
			line += activeToggle.Render(queueSuffix)
		}
		lines = append(lines, line)
	}

	for len(lines) < budget {
		lines = append(lines, "")
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
		help := helpKey("↑↓", "Navigate ") + helpKey("Enter", "Load ") + helpKey("/", "Search ")
		if _, ok := m.provider.(provider.FavoriteToggler); ok {
			help += helpKey("f", "Fav ")
		}
		return help + helpKey("Tab", "Focus ") + helpKey("Ctrl+K", "Keys")
	}
	if m.focus == focusProvPill {
		return helpKey("←→", "Select ") + helpKey("Enter", "Open ") + helpKey("Esc", "Back ") + helpKey("Tab", "Focus ") + helpKey("Ctrl+K", "Keys")
	}

	// Show only the 4-5 most relevant keys per mode; Ctrl+K always anchored for full list.
	var hints []helpHint

	if m.focus == focusSpeed {
		hints = append(hints,
			helpHint{helpKey("←→", "Speed "), 100},
			helpHint{helpKey("[]", "Speed "), 90},
			helpHint{helpKey("Spc", "▶❚❚ "), 80},
			helpHint{helpKey("Tab", "Focus "), 70},
			helpHint{helpKey("Ctrl+K", "Keys"), 100},
		)
	} else if m.focus == focusEQ {
		hints = append(hints,
			helpHint{helpKey("←→", "Band "), 100},
			helpHint{helpKey("↑↓", "Gain "), 100},
			helpHint{helpKey("e", "Preset "), 90},
			helpHint{helpKey("Spc", "▶❚❚ "), 80},
			helpHint{helpKey("Tab", "Focus "), 70},
			helpHint{helpKey("Ctrl+K", "Keys"), 100},
		)
	} else {
		// focusPlaylist (default)
		hints = append(hints,
			helpHint{helpKey("↑↓", "Scroll "), 100},
			helpHint{helpKey("Enter", "Play "), 100},
			helpHint{helpKey("Spc", "▶❚❚ "), 90},
		)
		track, _ := m.playlist.Current()
		if !track.Stream || m.player.Seekable() {
			hints = append(hints, helpHint{helpKey("←→", "Seek "), 80})
		}
		hints = append(hints,
			helpHint{helpKey("*", "Fav "), 75},
			helpHint{helpKey("Tab", "Focus "), 70},
			helpHint{helpKey("Ctrl+K", "Keys"), 100},
		)
	}

	return fitHints(hints, ui.PanelWidth)
}

// helpHint is a rendered help key with an associated display priority.
type helpHint struct {
	text     string
	priority int
}

// fitHints drops lowest-priority hints until they fit within maxWidth.
// Widths are pre-computed once to avoid repeated lipgloss.Width calls.
func fitHints(hints []helpHint, maxWidth int) string {
	active := make([]bool, len(hints))
	widths := make([]int, len(hints))
	var total int
	for i, h := range hints {
		active[i] = true
		widths[i] = lipgloss.Width(h.text)
		total += widths[i]
	}

	for total > maxWidth {
		// Find lowest-priority active hint and drop it.
		minPri := math.MaxInt
		minIdx := -1
		for i, h := range hints {
			if active[i] && h.priority < minPri {
				minPri = h.priority
				minIdx = i
			}
		}
		if minIdx < 0 {
			break
		}
		active[minIdx] = false
		total -= widths[minIdx]
	}

	var sb strings.Builder
	for i, h := range hints {
		if active[i] {
			sb.WriteString(h.text)
		}
	}
	return sb.String()
}

// renderBottomStatus renders the bottom status line: speed (left) and
// network stats (right) on the same row.
func (m Model) renderBottomStatus() string {
	// Left: speed indicator.
	speed := m.player.Speed()
	if speed == 0 {
		speed = 1.0
	}
	speedVal := fmt.Sprintf("%.2gx", speed)

	var left string
	speedLabel := labelStyle.Render("SPD ")
	if m.focus == focusSpeed {
		speedLabel = activeToggle.Render("SPD ▸ ")
		left = speedLabel + activeToggle.Render("["+speedVal+"]")
	} else if speed != 1.0 {
		left = speedLabel + activeToggle.Render("["+speedVal+"]")
	} else {
		left = speedLabel + dimStyle.Render("[") + trackStyle.Render(speedVal) + dimStyle.Render("]")
	}

	// Right: network stream stats (empty for local files).
	var right string
	downloaded, total := m.player.StreamBytes()
	if downloaded > 0 || total > 0 {
		mb := float64(downloaded) / (1024 * 1024)
		if total > 0 {
			totalMB := float64(total) / (1024 * 1024)
			pct := float64(downloaded) / float64(total) * 100
			right = fmt.Sprintf("↓ %.1f / %.1f MB (%.0f%%)", mb, totalMB, pct)
		} else {
			right = fmt.Sprintf("↓ %.1f MB", mb)
		}
		if m.network.speed > 0 {
			kbs := m.network.speed / 1024
			if kbs >= 1024 {
				right += fmt.Sprintf("  %.1f MB/s", kbs/1024)
			} else {
				right += fmt.Sprintf("  %.0f KB/s", kbs)
			}
		}
		right = dimStyle.Render(right)
	}

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	gap := max(1, ui.PanelWidth-leftW-rightW)

	if right == "" {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}
