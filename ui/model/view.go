package model

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/theme"
	"github.com/bjarneo/cliamp/ui"
)

const trackInfoMarqueeWidth = 48

// Pre-built styles for elements created per-render to avoid repeated allocation.
var (
	seekFillStyle = lipgloss.NewStyle().Foreground(ui.ColorSeekBar)
	seekDimStyle  = lipgloss.NewStyle().Foreground(ui.ColorDim)
	volBarStyle   = lipgloss.NewStyle().Foreground(ui.ColorVolume)
	activeToggle  = lipgloss.NewStyle().Foreground(ui.ColorAccent).Bold(true)
	// favMarkerStyle paints the favorite heart in the theme's red so it
	// reads as a deliberate accent instead of inheriting the dim/unavailable
	// look. The glyph carries U+FE0E (text presentation) so terminals render
	// it as a compact font glyph rather than a large color emoji.
	favMarkerStyle = lipgloss.NewStyle().Foreground(ui.ColorError)
	// favRemovedStyle mutes the same filled heart for unfavorite feedback:
	// identical attractive glyph, faded to signal the removed state instead
	// of switching to a thin outline glyph.
	favRemovedStyle = lipgloss.NewStyle().Foreground(ui.ColorDim)
)

// favHeart is the small, text-presentation favorite heart used everywhere the
// UI shows favorite state (track rows, header badge, status messages).
const favHeart = "♥\uFE0E"

// Pre-rendered toggle feedback marks for the status bar.
var (
	favAddedMark   = favMarkerStyle.Render(favHeart)
	favRemovedMark = favRemovedStyle.Render(favHeart)
)

// providerEmptyStateHint, keyed by lowercase provider Name(), returns the
// remediation hint shown under the generic "No playlists in X" message.
var providerEmptyStateHint = map[string]string{
	"local playlists":     "Add .toml playlists to ~/.config/cliamp/playlists/.",
	"local":               "Add .toml playlists to ~/.config/cliamp/playlists/.",
	"spotify":             "Sign in via Spotify, or check SPOTIFY_REFRESH_TOKEN.",
	"navidrome":           "Verify [navidrome] url/username/password in config.toml.",
	"jellyfin":            "Verify [jellyfin] url and token in config.toml.",
	"emby":                "Verify [emby] url and token or username/password in config.toml.",
	"audiobookshelf":      "Verify [audiobookshelf] url and token or username/password in config.toml.",
	"plex":                "Verify [plex] server URL and token or library filter in config.toml.",
	"youtube music":       "Run `cliamp ytmusic-login` to authorize, then refresh.",
	"ytmusic":             "Run `cliamp ytmusic-login` to authorize, then refresh.",
	"soundcloud":          "Set [soundcloud] user in config.toml to browse a profile.",
	"netease cloud music": "Run `cliamp setup` and configure NetEase browser cookies.",
}

// renderProviderEmptyState explains why the playlists pane is empty for the
// current provider and offers a remediation hint. Always pads to budget so the
// pane height stays stable.
func (m Model) renderProviderEmptyState(budget int) string {
	name := "this provider"
	if m.provider != nil {
		name = m.provider.Name()
	}
	lines := []string{
		dimStyle.Render(fmt.Sprintf("  No playlists in %s.", name)),
		"",
	}
	if _, searchable := m.provider.(provider.Searcher); searchable {
		lines = append(lines,
			dimStyle.Render("  Press ")+helpKeyStyle.Render(" Ctrl+F ")+dimStyle.Render(" to search."))
	}
	if m.provider != nil {
		if hint, ok := providerEmptyStateHint[strings.ToLower(m.provider.Name())]; ok {
			lines = append(lines, dimStyle.Render("  "+hint))
		}
	}
	return strings.Join(fitLines(lines, budget), "\n")
}

// providerRowStyle picks the prefix and style for a provider-list row.
// Cursor takes precedence; "currently loaded" gets the active-track style and
// the ▶ prefix so users can see at a glance which playlist is in the queue.
func (m Model) providerRowStyle(p playlist.PlaylistInfo, isCursor bool) (string, lipgloss.Style) {
	if isCursor {
		return "> ", playlistSelectedStyle
	}
	if m.isProviderRowActive(p) {
		return "▶ ", playlistActiveStyle
	}
	return "  ", playlistItemStyle
}

// isProviderRowActive reports whether the given playlist is the one whose
// tracks are currently loaded into the player.
func (m Model) isProviderRowActive(p playlist.PlaylistInfo) bool {
	if m.activeProviderPlaylistID != "" && m.activeProviderPlaylistID == p.ID {
		return true
	}
	if m.loadedPlaylist != "" && m.loadedPlaylist == p.Name {
		return true
	}
	return false
}

// playlistLabel formats a playlist entry, omitting fields the provider didn't
// supply. Track count and total duration are appended when available. The
// Favorites virtual playlist always shows its count — it stays listed even
// when empty, and a bare name would look broken. Directory-backed playlists
// hide the duration: their track counts are cheap estimates, so a runtime
// total would be misleading.
func playlistLabel(prefix string, p playlist.PlaylistInfo) string {
	out := prefix + p.Name
	var parts []string
	if p.TrackCount > 0 || p.Name == favorites.PlaylistName {
		parts = append(parts, fmt.Sprintf("%d tracks", p.TrackCount))
	}
	if p.DirSourceCount == 0 {
		if d := formatPlaylistDuration(p.DurationSecs); d != "" {
			parts = append(parts, d)
		}
	}
	if len(parts) > 0 {
		out += " · " + strings.Join(parts, " · ")
	}
	return out
}

// View renders the full TUI frame.
func (m Model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}
	m.recomputeLayout()
	if m.layout.tooSmall() {
		content := fmt.Sprintf("Terminal too small. Resize to at least 40x10 (current: %dx%d).", m.width, m.height)
		view := tea.NewView(ui.FitRect(content, max(1, m.width), max(1, m.height)))
		view.BackgroundColor = ui.ColorBackground
		if ui.ColorBackground != nil {
			view.ForegroundColor = ui.ColorText
		}
		view.AltScreen = true
		return view
	}

	screen := m.activeScreen()
	contentFirst := m.usesContentFirstLayout()
	if m.visualizerVisible() {
		m.refreshVisualizerIfPending()
	}

	var content string
	switch screen {
	case screenFullVisualizer:
		content = m.renderFullVisualizer()
	default:
		// Overlays render in the playlist region (renderMainBody), with their
		// header/help supplied by renderPlaylistHeader / renderHelp. List-heavy
		// tasks collapse to a compact now-playing summary so they can use the
		// reclaimed rows for browsing.
		body := ui.FitRect(m.renderMainBody(), m.layout.panelWidth, m.layout.bodyRows)
		content = strings.Join(m.mainSections(body, true, contentFirst), "\n")
	}

	// Every screen now renders within the main frame, so frame and center
	// uniformly.
	rendered := m.centerFrame(ui.FrameStyle.Render(content))
	rendered = ui.FitRect(rendered, m.layout.frameWidth, max(1, m.height))

	view := tea.NewView(rendered)
	view.BackgroundColor = ui.ColorBackground
	if ui.ColorBackground != nil {
		view.ForegroundColor = ui.ColorText
	}
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

func (m Model) mainSections(playlist string, includeTransient, contentFirst bool) []string {
	if m.usesSimplifiedLayout() {
		sections := []string{
			m.renderSimplifiedTrackInfo(),
			m.renderTimeStatus(),
			m.renderSeekBar(),
		}
		if includeTransient {
			if line := m.renderTransient(); line != "" {
				sections = append(sections, line)
			}
		}
		return sections
	}

	var sections []string
	if contentFirst {
		if m.layout.tier == layoutMinimal {
			sections = []string{
				m.renderTrackInfo(),
				m.renderTimeStatus(),
				m.renderSeekBar(),
				m.renderPlaylistHeader(),
			}
		} else {
			sections = []string{
				m.renderTitle(),
				m.renderTrackInfo(),
				m.renderTimeStatus(),
				m.renderSeekBar(),
				m.renderPlaylistHeader(),
			}
		}
	} else {
		switch m.layout.tier {
		case layoutCompact:
			sections = []string{
				m.renderTitle(),
				m.renderTrackInfo(),
				m.renderTimeStatus(),
				m.renderSpectrum(),
				m.renderSeekBar(),
				m.renderCompactControls(),
				m.renderCompactSource(),
				m.renderPlaylistHeader(),
			}
		case layoutMinimal:
			sections = []string{
				m.renderTrackInfo(),
				m.renderTimeStatus(),
				m.renderSeekBar(),
				m.renderPlaylistHeader(),
			}
		default:
			sections = []string{
				m.renderTitle(),
				m.renderTrackInfo(),
				m.renderTimeStatus(),
				"",
				m.renderSpectrum(),
				m.renderSeekBar(),
				m.renderControls(),
			}
			if source := m.renderProviderPill(); source != "" {
				sections = append(sections, source)
			}
			sections = append(sections, m.renderPlaylistHeader())
		}
	}
	if playlist != "" {
		sections = append(sections, playlist)
	}
	sections = append(sections, "", m.renderTierHelp(), m.renderBottomStatus())

	if includeTransient {
		if line := m.renderTransient(); line != "" {
			sections = append(sections, line)
		}
	}

	return trimTrailingEmpty(sections)
}

func (m Model) renderSimplifiedTrackInfo() string {
	track, _ := m.currentPlaybackTrack()
	name := trackInfoName(track, m.streamTitle)

	duration := formatTrackTime(int(m.cachedDur.Seconds()))
	if duration == "" {
		duration = formatTrackTime(track.DurationSecs)
	}
	if duration == "" {
		duration = "LIVE"
		if !track.Stream {
			duration = "--:--"
		}
	}

	maxW := min(max(1, ui.PanelWidth-lipgloss.Width(duration)-1), trackInfoMarqueeWidth)
	name = scrollTrackNameOnce(name, maxW, m.titleOff)
	gap := max(1, ui.PanelWidth-lipgloss.Width(name)-lipgloss.Width(duration))
	return trackStyle.Render(name) + strings.Repeat(" ", gap) + dimStyle.Render(duration)
}

func trackInfoName(track playlist.Track, streamTitle string) string {
	if streamTitle != "" && track.Stream {
		return streamTitle
	}

	name := trackViewName(track)
	if name == "" {
		name = "No track loaded"
	}
	if track.Album != "" {
		name += " · " + track.Album
	}
	return name
}

func scrollTrackNameOnce(name string, maxW, offset int) string {
	runes := []rune(name)
	if len(runes) <= maxW {
		return name
	}
	offset = min(max(0, offset), len(runes)-maxW)
	return string(runes[offset : offset+maxW])
}

func (m *Model) resetTitleScroll() {
	m.titleOff = 0
	m.titleLastScroll = time.Time{}
	m.titleScrolled = false
}

func (m Model) titleScrollLimit() int {
	track, _ := m.currentPlaybackTrack()
	maxW := min(ui.PanelWidth-4, trackInfoMarqueeWidth)
	return max(0, len([]rune(trackInfoName(track, m.streamTitle)))-maxW)
}

func (m *Model) advanceTitleScroll(now time.Time) {
	if m.player == nil || !m.player.IsPlaying() || m.player.IsPaused() || m.titleScrolled || now.Sub(m.titleLastScroll) < 200*time.Millisecond {
		return
	}
	m.titleLastScroll = now
	if m.titleOff >= m.titleScrollLimit() {
		m.titleOff = 0
		m.titleScrolled = true
		return
	}
	m.titleOff++
}

func (m Model) renderTierHelp() string {
	if m.layout.tier != layoutMinimal {
		return m.renderHelp()
	}
	if ov, ok := m.activeOverlay(); ok {
		return fitHelpLine(ov.help(&m))
	}
	return m.commandHelp(commandModeMain)
}

func (m Model) renderTransient() string {
	if m.err != nil {
		return ui.FitRect(errorStyle.Render(fmt.Sprintf("ERR: %s", m.err)), m.layout.panelWidth, 1)
	}
	if text := m.save.activityText(); text != "" {
		return ui.FitRect(feedbackActivityStyle.Render(text), m.layout.panelWidth, 1)
	}
	if m.status.text != "" {
		text := m.status.text
		style := feedbackSuccessStyle
		switch m.status.kind {
		case feedbackActivity:
			style = feedbackActivityStyle
		case feedbackWarning:
			style = feedbackWarningStyle
			text = "WARN: " + text
		case feedbackError:
			style = errorStyle
			text = "ERR: " + text
		}
		return ui.FitRect(style.Render(text), m.layout.panelWidth, 1)
	}
	if n := len(m.logLines); n > 0 {
		return ui.FitRect(dimStyle.Render(m.logLines[n-1].text), m.layout.panelWidth, 1)
	}
	return ""
}

func (m Model) renderCompactControls() string {
	mono := ""
	if m.player.Mono() {
		mono = " [M]"
	}
	eqLabel := labelStyle.Render("EQ ")
	eqValue := activeToggle.Render("[" + m.EQPresetName() + "]")
	if m.focus == focusEQ {
		eqLabel = activeToggle.Render("EQ ▸ ")
		bands := m.player.EQBands()
		labels := [10]string{"70", "180", "320", "600", "1k", "3k", "6k", "12k", "14k", "16k"}
		eqValue += " " + eqActiveStyle.Render(fmt.Sprintf("%s %+.0fdB", labels[m.eqCursor], bands[m.eqCursor]))
	}
	return eqLabel + eqValue +
		" " + labelStyle.Render("VOL ") + fmt.Sprintf("%+.0fdB", m.player.Volume()) + mono
}

func (m Model) renderCompactSource() string {
	if len(m.providers) <= 1 {
		return ""
	}
	name := "Unknown"
	if m.provider != nil {
		name = m.provider.Name()
	}
	return labelStyle.Render("SRC ") + trackStyle.Render("["+name+"]") + dimStyle.Render(fmt.Sprintf(" %d/%d", m.provPillIdx+1, len(m.providers)))
}

// centerFrame centers a pre-rendered frame in the terminal.
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

func (m Model) renderTitle() string {
	title := titleStyle.Render("C L I A M P")
	label := m.focus.label()
	if screen := m.activeScreen(); screen != screenMain {
		label = screen.label()
	}
	if label == "" {
		return title
	}
	indicator := dimStyle.Render("[" + label + "]")
	gap := max(ui.PanelWidth-lipgloss.Width(title)-lipgloss.Width(indicator), 1)
	return title + strings.Repeat(" ", gap) + indicator
}

func (m Model) renderTrackInfo() string {
	track, _ := m.currentPlaybackTrack()
	name := trackInfoName(track, m.streamTitle)
	maxW := min(ui.PanelWidth-4, trackInfoMarqueeWidth)
	if maxW < 1 {
		return trackStyle.Render("♫ " + name)
	}
	runes := []rune(name)

	if len(runes) <= maxW {
		return trackStyle.Render("♫ " + name)
	}
	return trackStyle.Render("♫ " + scrollTrackNameOnce(name, maxW, m.titleOff))
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

	track, _ := m.currentPlaybackTrack()
	if track.Stream && !m.player.Seekable() {
		timeStr = fmt.Sprintf("%02d:%02d / LIVE", posMin, posSec)
	}

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
	gap := max(ui.PanelWidth-lipgloss.Width(left)-lipgloss.Width(status), 1)

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
		helpKey("V", "Exit ") + helpKey("v", "Mode:"+m.vis.ModeName()+" ") + helpKey("Spc", "▶❚❚ ") + helpKey("<>", "Trk ") + helpKey("+-", "Vol ") + helpKey("?", "Keys"),
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

	// Half-cell resolution: each cell is two sub-units, so a 4-minute track on
	// an 80-wide bar advances the tip every ~1.5s instead of ~3s. The
	// transition cell uses ╸ (heavy left-half) as a half-step stub.
	w := ui.PanelWidth
	subPos := int(progress * 2 * float64(w))
	fullCells := subPos / 2
	hasHalf := subPos%2 == 1 && fullCells < w

	var b strings.Builder
	b.WriteString(seekFillStyle.Render(strings.Repeat("━", fullCells)))
	if hasHalf {
		b.WriteString(seekFillStyle.Render("╸"))
		b.WriteString(seekDimStyle.Render(strings.Repeat("━", w-fullCells-1)))
	} else {
		b.WriteString(seekDimStyle.Render(strings.Repeat("━", w-fullCells)))
	}
	return b.String()
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
	volMin := m.player.VolumeMin()
	frac := max(0, min(1, (vol-volMin)/(6-volMin)))
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

	srcLabel := labelStyle.Render("SRC ")
	if m.focus == focusProvPill {
		srcLabel = activeToggle.Render("SRC ▸ ")
	}
	current := m.providers[m.provPillIdx].Name
	indicator := dimStyle.Render("[") + trackStyle.Render(current) + dimStyle.Render("]") +
		dimStyle.Render(fmt.Sprintf(" %d/%d", m.provPillIdx+1, len(m.providers)))
	if m.focus == focusProvPill {
		indicator = activeToggle.Render("["+current+"]") + dimStyle.Render(fmt.Sprintf(" %d/%d", m.provPillIdx+1, len(m.providers)))
	}
	if ui.PanelWidth < 110 {
		return srcLabel + indicator
	}

	var neighbors []string
	if m.provPillIdx > 0 {
		neighbors = append(neighbors, dimStyle.Render("["+m.providers[m.provPillIdx-1].Name+"]"))
	}
	if m.provPillIdx+1 < len(m.providers) {
		neighbors = append(neighbors, dimStyle.Render("["+m.providers[m.provPillIdx+1].Name+"]"))
	}
	return srcLabel + strings.Join(append([]string{indicator}, neighbors...), " ")
}

func (m Model) renderPlaylistHeader() string {
	if ov, ok := m.activeOverlay(); ok {
		return ov.header(&m)
	}
	if m.focus == focusProvider {
		label := m.provider.Name() + " / Playlists"
		if m.provSearch.active {
			label += " / Filter"
		}
		return dimStyle.Render(labeledSeparator("", label))
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

	var bookmarkStr string
	if bookmarkCount := m.playlist.BookmarkCount(); bookmarkCount > 0 {
		bookmarkStr = " " + activeToggle.Render(fmt.Sprintf("[★ %d]", bookmarkCount))
	}

	var favStr string
	// Render from the cached favSet: the render path must not hit disk.
	if count := len(m.favSet); count > 0 {
		favStr = " " + activeToggle.Render(fmt.Sprintf("[%s %d]", favHeart, count))
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
	return headerStyle.Render(headerLabel) + shuffle + queueStr + bookmarkStr + favStr + posStr + themeStr + " " + dimStyle.Render("──")
}

func (m Model) renderProviderList() string {
	visibleBudget := m.effectivePlaylistVisible()
	if visibleBudget <= 0 {
		return ""
	}
	if m.provSignIn {
		return dimStyle.Render(fmt.Sprintf("  Sign in to %s. Press Enter to continue.", m.provider.Name()))
	}
	if m.provLoading && len(m.providerLists) == 0 {
		lines := []string{loadingLine(fmt.Sprintf("Loading %s…", m.provider.Name()))}
		if m.provAuthURL != "" {
			lines = append(lines,
				"",
				dimStyle.Render("  If your browser didn't open, visit this URL to sign in:"),
				"  "+m.provAuthURL,
			)
		}
		for len(lines) < visibleBudget {
			lines = append(lines, "")
		}
		return strings.Join(lines, "\n")
	}
	if len(m.providerLists) == 0 {
		return m.renderProviderEmptyState(visibleBudget)
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
				scroll := m.provSearch.scroll
				for j := scroll; j < scroll+visible && j < len(m.provSearch.results); j++ {
					idx := m.provSearch.results[j]
					p := m.providerLists[idx]
					prefix, style := m.providerRowStyle(p, j == m.provSearch.cursor)
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

		hasSections := !isRadio && slices.ContainsFunc(m.providerLists, func(p playlist.PlaylistInfo) bool {
			return p.Section != ""
		})

		if isRadio {
			for scroll < len(m.providerLists)-1 && m.providerRowsFromScroll(scroll, m.provCursor) > visibleBudget {
				scroll++
			}
		} else if m.provCursor >= scroll+visibleBudget {
			scroll = m.provCursor - visibleBudget + 1
		}

		prevPrefix := ""
		if isRadio && scroll > 0 {
			prevPrefix = sl.IDPrefix(m.providerLists[scroll-1].ID)
		}
		prevSection := ""
		if hasSections && scroll > 0 {
			prevSection = m.providerLists[scroll-1].Section
		}

		for j := scroll; j < len(m.providerLists) && len(lines) < visibleBudget; j++ {
			p := m.providerLists[j]

			if isRadio {
				pfx := sl.IDPrefix(p.ID)
				if pfx != prevPrefix {
					var header string
					switch pfx {
					case "f":
						header = labeledSeparator("  ", "Favorites")
					case "c":
						header = labeledSeparator("  ", "Catalog")
					case "s":
						header = labeledSeparator("  ", "Search Results")
					}
					if header != "" && len(lines) < visibleBudget {
						lines = append(lines, dimStyle.Render(header))
					}
					prevPrefix = pfx
				}
			} else if hasSections && p.Section != prevSection {
				header := labeledSeparator("  ", p.Section)
				if len(lines) < visibleBudget {
					lines = append(lines, dimStyle.Render(header))
				}
				prevSection = p.Section
			}

			if len(lines) >= visibleBudget {
				break
			}

			prefix, style := m.providerRowStyle(p, j == m.provCursor)
			lines = append(lines, style.Render(playlistLabel(prefix, p)))
		}
	}

	// Loading indicator for catalog batch (never displace selected row if full).
	if isRadio && m.catalogBatch.loading && len(lines) < visibleBudget {
		lines = append(lines, loadingLine("Loading more stations…"))
	}

	return strings.Join(fitLines(lines, visibleBudget), "\n")
}

func (m Model) renderPlaylist() string {
	budget := m.effectivePlaylistVisible()
	if budget <= 0 {
		return ""
	}

	if m.focus == focusProvider {
		return m.renderProviderList()
	}

	trackCount := m.playlist.Len()
	if trackCount == 0 {
		var lines []string
		if m.feedLoading {
			lines = append(lines, loadingLine("Loading feed…"))
		} else {
			lines = append(lines, dimStyle.Render("  No tracks loaded"))
		}
		return strings.Join(fitLines(lines, budget), "\n")
	}

	currentIdx := m.playlist.Index()
	scroll := m.playlistScroll(budget)
	windowStart := max(0, scroll-1)
	tracks := m.playlist.TrackWindow(windowStart, budget+1)
	localScroll := scroll - windowStart

	lines := make([]string, 0, budget)
	numWidth := len(fmt.Sprintf("%d", trackCount))

	for row := range m.playlistRows(tracks, localScroll, m.showAlbumHeaders) {
		if row.Index < 0 {
			if len(lines)+1 >= budget {
				break
			}
			lines = append(lines, m.albumSeparator(row.Album, row.Year))
			continue
		}

		if len(lines) >= budget {
			break
		}

		i, t := windowStart+row.Index, row.Track
		style := playlistItemStyle
		selected := m.focus == focusPlaylist && i == m.plCursor
		playing := !m.playbackDetached && i == currentIdx && m.player.IsPlaying()
		if playing {
			style = playlistActiveStyle
		}
		if selected {
			style = playlistSelectedStyle
		}

		if t.Unplayable {
			if selected {
				style = dimStyle
			} else {
				style = playlistUnavailableStyle
			}
		}
		cursorMarker := " "
		if selected {
			cursorMarker = ">"
		}
		playingMarker := " "
		if playing {
			playingMarker = "▶"
		}
		queuePosition := m.playlist.QueuePosition(i)
		queueMarker := " "
		if queuePosition > 0 {
			queueMarker = "Q"
		}
		bookmarkMarker := " "
		if t.Bookmark {
			bookmarkMarker = "★"
		}
		favMarker := " "
		if m.favSet != nil {
			if _, ok := m.favSet[t.Path]; ok {
				favMarker = favHeart
			}
		}
		unavailableMarker := " "
		if t.Unplayable {
			unavailableMarker = "!"
		}
		markers := cursorMarker + playingMarker + queueMarker + bookmarkMarker + favMarker + unavailableMarker + " "

		name := trackViewName(t)
		queueSuffix := ""
		if queuePosition > 0 && ui.PanelWidth >= 64 {
			queueSuffix = fmt.Sprintf(" [Q%d]", queuePosition)
		}
		queueLen := lipgloss.Width(queueSuffix)
		duration := formatTrackTime(t.DurationSecs)
		durationLen := lipgloss.Width(duration)
		durationGap := 0
		if duration != "" {
			durationGap = durationLen + 1
		}

		linePrefixWidth := lipgloss.Width(markers) + numWidth + 2 // 2 for ". "

		// State markers always occupy the same cells; low-priority queue position,
		// album, and unavailable labels appear only when the terminal has room.
		name = truncate(name, ui.PanelWidth-linePrefixWidth-queueLen-durationGap)
		// Truncate the album to fit whatever space remains after the track name.
		albumSuffix := ""
		nameLen := lipgloss.Width(name)
		if t.Unplayable && ui.PanelWidth >= 68 {
			remaining := ui.PanelWidth - linePrefixWidth - nameLen - queueLen - durationGap
			if remaining >= len(" (unavailable)") {
				albumSuffix = truncate(" (unavailable)", remaining)
			}
		} else if album := t.Album; album != "" && !m.showAlbumHeaders && ui.PanelWidth >= 56 {
			remaining := ui.PanelWidth - linePrefixWidth - nameLen - queueLen - durationGap - 3 // 3 = " · "
			if remaining >= 4 {
				albumSuffix = " · " + truncate(album, remaining)
			}
		}

		numStr := fmt.Sprintf("%*d. ", numWidth, i+1)
		line := dimStyle.Render(cursorMarker) + playlistActiveStyle.Render(playingMarker) +
			activeToggle.Render(queueMarker+bookmarkMarker) + favMarkerStyle.Render(favMarker) +
			playlistUnavailableStyle.Render(unavailableMarker) +
			" " + style.Render(numStr)
		line += style.Render(name)
		if albumSuffix != "" {
			line += dimStyle.Render(albumSuffix)
		}
		if queueSuffix != "" {
			line += activeToggle.Render(queueSuffix)
		}
		if duration != "" {
			padding := max(1, ui.PanelWidth-lipgloss.Width(line)-durationLen)
			line += strings.Repeat(" ", padding) + dimStyle.Render(duration)
		}
		lines = append(lines, line)
	}

	return strings.Join(padLines(lines, budget, len(lines)), "\n")
}

func (m Model) renderHelp() string {
	if ov, ok := m.activeOverlay(); ok {
		return fitHelpLine(ov.help(&m))
	}
	switch m.focus {
	case focusProvider:
		return m.commandHelp(commandModeProvider)
	case focusProvPill:
		return m.commandHelp(commandModeProviderPill)
	case focusSpeed:
		return m.commandHelp(commandModeSpeed)
	case focusEQ:
		return m.commandHelp(commandModeEQ)
	default:
		return m.commandHelp(commandModeMain)
	}
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
