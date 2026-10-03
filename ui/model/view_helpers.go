package model

import (
	"fmt"
	"iter"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

const restrictedViewSuffix = " [E]"

// trackViewName decorates provider-specific presentation metadata without
// mutating the title used by playlist export, IPC, or media-session metadata.
func trackViewName(track playlist.Track) string {
	name := track.DisplayName()
	if track.Meta(provider.MetaPodcastFeed) != "" && track.Title != "" {
		name = podcastEpisodeViewName(track)
	}
	if track.Restricted {
		return strings.TrimSpace(name) + restrictedViewSuffix
	}
	return name
}

// podcastEpisodeViewName avoids repeating the show name in episode rows. Some
// publishers also include it in the RSS title, so trim that prefix as well.
func podcastEpisodeViewName(track playlist.Track) string {
	for _, show := range []string{track.Album, track.Artist} {
		if show == "" {
			continue
		}
		for _, separator := range []string{" - ", " — ", " – ", ": ", " | "} {
			if suffix, ok := strings.CutPrefix(track.Title, show+separator); ok && strings.TrimSpace(suffix) != "" {
				return strings.TrimSpace(suffix)
			}
		}
	}
	return track.Title
}

func albumViewName(album provider.AlbumInfo) string {
	if album.Restricted {
		return strings.TrimSpace(album.Name) + restrictedViewSuffix
	}
	return album.Name
}

// formatListMatchCount returns a human-readable "matches of total" summary
// for filtered lists.
func (m Model) formatListMatchCount(matches, total int) string {
	if matches < 0 {
		matches = 0
	}
	if total < 0 {
		total = 0
	}
	return fmt.Sprintf("%d matches of %d total", matches, total)
}

// formatTrackTime formats a duration in seconds as M:SS or H:MM:SS for tracks.
// Returns "" when secs is non-positive so callers can skip rendering entirely.
func formatTrackTime(secs int) string {
	if secs <= 0 {
		return ""
	}
	h := secs / 3600
	m := (secs % 3600) / 60
	s := secs % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// formatPlaylistDuration formats a total runtime for a playlist as "1h 23m"
// or "12m" or "45s". Returns "" when secs is non-positive.
func formatPlaylistDuration(secs int) string {
	if secs <= 0 {
		return ""
	}
	h := secs / 3600
	m := (secs % 3600) / 60
	if h > 0 {
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%ds", secs)
}

// formatTrackRow renders a track list row of the form
//
//	"01. Title · Album         3:42"
//
// with the duration right-aligned at width - 4 (to leave space for
// the cursor prefix the caller adds). The title column is truncated as
// needed; the duration is hidden when secs is 0.
func formatTrackRow(num int, name string, secs, width int) string {
	const prefixOverhead = 4 // leaves room for "  " / "> " caller prefix
	dur := formatTrackTime(secs)
	numStr := fmt.Sprintf("%d. ", num)
	numLen := lipgloss.Width(numStr)
	durLen := lipgloss.Width(dur)

	titleBudget := width - prefixOverhead - numLen
	if dur != "" {
		titleBudget -= durLen + 1 // +1 for spacing gap
	}
	if titleBudget < 4 {
		titleBudget = 4
	}
	title := truncate(name, titleBudget)
	if dur == "" {
		return numStr + title
	}

	pad := width - prefixOverhead - durLen - numLen - lipgloss.Width(title)
	if pad < 1 {
		pad = 1
	}
	return numStr + title + strings.Repeat(" ", pad) + dur
}

// truncate shortens s to maxW terminal cells, preserving ANSI escapes and
// avoiding splits inside wide characters.
func truncate(s string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxW {
		return s
	}
	return ansi.Truncate(s, maxW, "…")
}

// wrapText breaks s into lines no wider than maxW, splitting on spaces.
// Widths are measured with lipgloss so wide glyphs and accents count once.
// A single word longer than maxW is truncated rather than left to overflow
// the panel.
func wrapText(s string, maxW int) []string {
	if maxW <= 0 {
		return nil
	}
	var lines []string
	line := ""
	for _, word := range strings.Fields(s) {
		switch {
		case line == "":
			line = word
		case lipgloss.Width(line)+1+lipgloss.Width(word) <= maxW:
			line += " " + word
		default:
			lines = append(lines, truncate(line, maxW))
			line = word
		}
	}
	if line != "" {
		lines = append(lines, truncate(line, maxW))
	}
	return lines
}

// markerColumns says which optional state columns the playlist rows reserve.
// The cursor and playing/unavailable cells are always drawn. Queue and played
// cost a column of title width each, so they are reserved only once the
// playlist has something to put in them. The favorite column is always
// reserved: toggling the first/last favorite would otherwise shift every title
// by one cell.
type markerColumns struct {
	queue    bool
	favorite bool
	played   bool
}

// markerColumns decides the reserved marker columns for one render pass. It is
// a per-pass decision, not a per-row one: a row-by-row choice would shift the
// title column as you scrolled. With no queue or playback state the titles
// start two columns further left; the favorite cell stays put so favoriting
// never moves the titles.
func (m Model) markerColumns() markerColumns {
	return markerColumns{
		queue:    m.playlist.QueueLen() > 0,
		favorite: true,
		played:   m.hasPlaybackState(),
	}
}

// cursorLine renders a list item with "> " prefix when active, "  " otherwise.
func cursorLine(label string, active bool) string {
	if active {
		return playlistSelectedStyle.Render("> " + label)
	}
	return dimStyle.Render("  " + label)
}

// spinnerFrames is the braille-dot animation used to indicate loading. The
// view re-renders on the model tick so the spinner advances on its own.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinnerInterval is how long each spinner frame stays on the screen. While
// a spinner shows, the view redraws at least this often.
const spinnerInterval = 100 * time.Millisecond

// spinnerFrame returns the current animation frame, time-driven so the caller
// doesn't need to track an animation index.
func spinnerFrame() string {
	idx := (time.Now().UnixMilli() / spinnerInterval.Milliseconds()) % int64(len(spinnerFrames))
	return spinnerFrames[idx]
}

// loadingLine renders a single styled "<spinner> <label>" line for use as a
// loading indicator inside a list pane.
func loadingLine(label string) string {
	return activeToggle.Render("  "+spinnerFrame()) + dimStyle.Render(" "+label)
}

// padLines appends empty strings so that rendered items fill maxVisible rows.
func padLines(lines []string, maxVisible, rendered int) []string {
	for range maxVisible - rendered {
		lines = append(lines, "")
	}
	return lines
}

// fitLines truncates lines to budget then pads with empty strings to exactly budget rows.
func fitLines(lines []string, budget int) []string {
	if len(lines) > budget {
		lines = lines[:budget]
	}
	return padLines(lines, budget, len(lines))
}

// helpKey renders a key as a pill (background-highlighted) followed by a dim label.
func helpKey(key, label string) string {
	return helpKeyStyle.Render(" "+key+" ") + helpStyle.Render(" "+label)
}

// fitHelpLine keeps a hint line to a single row width cells wide. A longer
// line would wrap to two rows and shift the layout height, so this clips it
// (ANSI-aware) to one row.
func fitHelpLine(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "")
}

// toggleAlbumHeadersManual flips header visibility and pins the choice so
// later Adds don't re-run the cohesion heuristic over the user.
func (m *Model) toggleAlbumHeadersManual() {
	m.showAlbumHeaders = !m.showAlbumHeaders
	m.headerManual = true
}

// toggleHelpBar shows or hides the key-binding hint bar and persists the
// choice to the hide_help_bar config key, so the bar comes back the way the
// listener left it on the next launch.
func (m *Model) toggleHelpBar() {
	m.SetHideHelpBar(!m.hideHelpBar)
	_ = m.saveConfigBool("hide_help_bar", m.hideHelpBar)
}

// toggleSettingsPane opens or closes the settings pane beside the playlist and
// persists the choice to the hide_settings_pane config key, so the pane comes
// back the way it was left. Closing it hands the playlist the full frame width
// and keeps a source and volume row above it.
func (m *Model) toggleSettingsPane() {
	m.SetHideSettingsPane(!m.hideSettings)
	_ = m.saveConfigBool("hide_settings_pane", m.hideSettings)
}

// minTracksPerAlbum is the threshold at which a list is considered cohesive
// enough to default to showing album headers; below this average tracks/album,
// the list looks like a fragmented mixtape and headers add noise.
const minTracksPerAlbum = 3.0

// setHeaderStateFromTracks resets the running counters and re-runs the
// cohesion heuristic. A fresh load also clears any manual override.
func (m *Model) setHeaderStateFromTracks(tracks []playlist.Track) {
	m.headerManual = false
	m.recountHeaderState(tracks)
}

// recountHeaderState resets the running counters and counts tracks again,
// after a queue edit that moved or removed tracks. A manual override stays.
func (m *Model) recountHeaderState(tracks []playlist.Track) {
	m.headerLastAlbum = ""
	m.headerSegments = 0
	m.headerTracks = 0
	m.addToHeaderState(tracks)
}

// addToHeaderState advances the cohesion counters by the newly added tracks
// (O(k)) and refreshes header visibility, unless the user has pinned it.
func (m *Model) addToHeaderState(tracks []playlist.Track) {
	for _, t := range tracks {
		if m.headerTracks == 0 || t.Album != m.headerLastAlbum {
			m.headerSegments++
		}
		m.headerLastAlbum = t.Album
		m.headerTracks++
	}

	if m.headerManual {
		return
	}
	if m.headerSegments == 0 {
		m.showAlbumHeaders = false
		return
	}
	m.showAlbumHeaders = float64(m.headerTracks)/float64(m.headerSegments) >= minTracksPerAlbum
}

// trackAlbumSuffix returns the " · Album" suffix shown after track names when
// album headers are hidden. Empty when headers are on or the track has no album.
func trackAlbumSuffix(t playlist.Track, showHeaders bool) string {
	if showHeaders || t.Album == "" {
		return ""
	}
	return " · " + t.Album
}

// playlistRow represents a single line in a track list, which can be either
// an album separator header or an actual track.
type playlistRow struct {
	Index int            // index into the original track list; -1 for headers
	Track playlist.Track // only populated if Index >= 0
	Album string         // only populated for headers (Index == -1)
	Year  int            // only populated for headers (Index == -1)
}

// playlistRows returns an iterator over tracks and their injected album headers,
// starting from the given scroll position. It accounts for "sticky" headers
// (showing the header for an album even if we scrolled into the middle of it).
func (m Model) playlistRows(tracks []playlist.Track, scroll int, showHeaders bool) iter.Seq[playlistRow] {
	return func(yield func(playlistRow) bool) {
		if len(tracks) == 0 || scroll < 0 || scroll >= len(tracks) {
			return
		}

		prevAlbum := ""
		if scroll > 0 {
			prevAlbum = tracks[scroll-1].Album
		}

		for i := scroll; i < len(tracks); i++ {
			t := tracks[i]

			if showHeaders {
				// Sticky header when the viewport opens mid-album.
				if i == scroll && t.Album != "" && t.Album == prevAlbum {
					if !yield(playlistRow{Index: -1, Album: t.Album, Year: t.Year}) {
						return
					}
				}

				// Suppress a blank closing separator at the very top of the view.
				if t.Album != prevAlbum && (t.Album != "" || i > scroll) {
					if !yield(playlistRow{Index: -1, Album: t.Album, Year: t.Year}) {
						return
					}
				}
			}

			if !yield(playlistRow{Index: i, Track: t}) {
				return
			}
			prevAlbum = t.Album
		}
	}
}

// searchOverlayRow is one rendered row of the provider search results: a section
// separator when Index is negative, otherwise the result at Index.
type searchOverlayRow struct {
	Index   int
	Track   playlist.Track
	Section string
}

// searchOverlaySection names the section a search result belongs to. Albums are
// placeholders that expand into a record; everything else plays as-is.
func searchOverlaySection(t playlist.Track) string {
	if t.IsAlbum() {
		return "Albums"
	}
	return "Tracks"
}

// searchOverlayRows walks the search results from scroll, emitting a separator
// whenever the section changes. The provider returns albums first, so this
// yields at most two headers, plus a sticky one at the top of the viewport so
// the section stays named while scrolling through a long run of results.
func searchOverlayRows(results []playlist.Track, scroll int) iter.Seq[searchOverlayRow] {
	return func(yield func(searchOverlayRow) bool) {
		if len(results) == 0 || scroll < 0 || scroll >= len(results) {
			return
		}

		prev := ""
		for i := scroll; i < len(results); i++ {
			section := searchOverlaySection(results[i])
			if section != prev {
				if !yield(searchOverlayRow{Index: -1, Section: section}) {
					return
				}
			}
			if !yield(searchOverlayRow{Index: i, Track: results[i]}) {
				return
			}
			prev = section
		}
	}
}

// searchOverlayRowsToCursor counts rendered rows from scroll to cursor inclusive,
// separators included, so scrolling can account for the space they take.
func searchOverlayRowsToCursor(results []playlist.Track, scroll, cursor int) int {
	if len(results) == 0 || scroll < 0 || cursor < scroll || cursor >= len(results) {
		return 0
	}
	rows := 0
	for row := range searchOverlayRows(results, scroll) {
		rows++
		if row.Index == cursor {
			break
		}
	}
	return rows
}

// albumSeparatorRows counts rendered rows between scroll and cursor (inclusive)
// in a playlist view that emits an album-separator row whenever the album
// changes. Streaming tracks are treated as not contributing a separator,
// matching the renderer.
func (m Model) albumSeparatorRows(tracks []playlist.Track, scroll, cursor int, showHeaders bool) int {
	if len(tracks) == 0 || scroll < 0 || cursor < scroll || cursor >= len(tracks) {
		return 0
	}
	if !showHeaders {
		return cursor - scroll + 1
	}

	rows := 0
	for row := range m.playlistRows(tracks, scroll, showHeaders) {
		rows++
		if row.Index == cursor {
			break
		}
	}
	return rows
}

// separatorLine pads or truncates an unstyled separator to exactly width
// cells. The caller styles the result, so the "─" fill is added bare; use
// fillSeparator for a line that is already rendered.
func separatorLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	switch w := lipgloss.Width(line); {
	case w < width:
		return line + strings.Repeat("─", width-w)
	case w > width:
		return ansi.Truncate(line, width, "")
	default:
		return line
	}
}

// fillSeparator extends an already-styled separator with dim "─" fill to
// exactly width cells, clipping it instead when it is longer. separatorLine
// cannot do this job: it appends bare runes, which on a pre-rendered line show
// up in the terminal's default foreground rather than continuing the dim rule
// they are extending.
func fillSeparator(line string, width int) string {
	if width <= 0 {
		return ""
	}
	if w := lipgloss.Width(line); w < width {
		return line + dimStyle.Render(strings.Repeat("─", width-w))
	}
	return ansi.Truncate(line, width, "")
}

// labeledSeparator builds a labeled separator line width cells wide.
func labeledSeparator(indent, label string, width int) string {
	return separatorLine(indent+"── "+label+" ", width)
}

// albumSeparator builds an album separator line.
func (m Model) albumSeparator(album string, year int) string {
	if album == "" {
		return dimStyle.Render(strings.Repeat("─", m.layout.panelWidth))
	}
	label := album
	if year != 0 {
		label += fmt.Sprintf(" (%d)", year)
	}
	return dimStyle.Render(labeledSeparator("", label, m.layout.panelWidth))
}

// navScrollItems renders a filtered or unfiltered scrolled list for nav browsers.
func (m Model) navScrollItems(total int, labelFn func(int) string) []string {
	maxVisible := m.effectivePlaylistVisible()

	useFilter := len(m.navBrowser.searchIdx) > 0 || m.navBrowser.search != ""
	scroll := m.navBrowser.scroll

	var lines []string
	rendered := 0

	if useFilter {
		for j := scroll; j < len(m.navBrowser.searchIdx) && rendered < maxVisible; j++ {
			label := labelFn(m.navBrowser.searchIdx[j])
			lines = append(lines, cursorLine(label, j == m.navBrowser.cursor))
			rendered++
		}
	} else {
		for i := scroll; i < total && rendered < maxVisible; i++ {
			label := labelFn(i)
			lines = append(lines, cursorLine(label, i == m.navBrowser.cursor))
			rendered++
		}
	}

	return padLines(lines, maxVisible, rendered)
}
