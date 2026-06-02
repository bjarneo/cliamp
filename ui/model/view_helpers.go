package model

import (
	"fmt"
	"iter"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"cliamp/playlist"
	"cliamp/ui"
)

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
// with the duration right-aligned at ui.PanelWidth - 4 (to leave space for
// the cursor prefix the caller adds). The title column is truncated as
// needed; the duration is hidden when secs is 0.
func formatTrackRow(num int, name string, secs int) string {
	const prefixOverhead = 4 // leaves room for "  " / "> " caller prefix
	dur := formatTrackTime(secs)
	numStr := fmt.Sprintf("%d. ", num)
	numLen := utf8.RuneCountInString(numStr)
	durLen := utf8.RuneCountInString(dur)

	titleBudget := ui.PanelWidth - prefixOverhead - numLen
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

	pad := ui.PanelWidth - prefixOverhead - durLen - numLen - utf8.RuneCountInString(title)
	if pad < 1 {
		pad = 1
	}
	return numStr + title + strings.Repeat(" ", pad) + dur
}

// truncate shortens s to maxW runes, appending "…" if truncated.
// Uses RuneCountInString first to avoid rune slice allocation in the common
// case where the string is already short enough.
func truncate(s string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= maxW {
		return s
	}
	if maxW == 1 {
		return "…"
	}
	r := []rune(s)
	return string(r[:maxW-1]) + "…"
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

// spinnerFrame returns the current animation frame, time-driven so the caller
// doesn't need to track an animation index.
func spinnerFrame() string {
	idx := (time.Now().UnixMilli() / 100) % int64(len(spinnerFrames))
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

// fitHelpLine keeps a help line to a single panel-wide row. Overlay help lines
// are fixed strings that can exceed the panel width and wrap to two rows, which
// would shift the layout height; this clips them (ANSI-aware) to one row.
func fitHelpLine(s string) string {
	if ui.PanelWidth <= 0 || lipgloss.Width(s) <= ui.PanelWidth {
		return s
	}
	return ansi.Truncate(s, ui.PanelWidth, "")
}

// toggleAlbumHeadersManual flips header visibility and pins the choice so
// later Adds don't re-run the cohesion heuristic over the user.
func (m *Model) toggleAlbumHeadersManual() {
	m.showAlbumHeaders = !m.showAlbumHeaders
	m.headerManual = true
}

// minTracksPerAlbum is the threshold at which a list is considered cohesive
// enough to default to showing album headers; below this average tracks/album,
// the list looks like a fragmented mixtape and headers add noise.
const minTracksPerAlbum = 3.0

// setHeaderStateFromTracks resets the running counters and re-runs the
// cohesion heuristic. A fresh load also clears any manual override.
func (m *Model) setHeaderStateFromTracks(tracks []playlist.Track) {
	m.headerManual = false
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

// separatorLine pads or truncates an unstyled separator to exactly fill the playlist pane width.
func separatorLine(line string) string {
	if ui.PanelWidth <= 0 {
		return ""
	}
	if w := lipgloss.Width(line); w < ui.PanelWidth {
		return line + strings.Repeat("─", ui.PanelWidth-w)
	}
	if lipgloss.Width(line) > ui.PanelWidth {
		return ansi.Truncate(line, ui.PanelWidth, "")
	}
	return line
}

// labeledSeparator builds a labeled separator line.
func labeledSeparator(indent, label string) string {
	return separatorLine(indent + "── " + label + " ")
}

// albumSeparator builds an album separator line.
func (m Model) albumSeparator(album string, year int) string {
	if album == "" {
		return dimStyle.Render(strings.Repeat("─", ui.PanelWidth))
	}
	label := album
	if year != 0 {
		label += fmt.Sprintf(" (%d)", year)
	}
	return dimStyle.Render(labeledSeparator("", label))
}

// navScrollItems renders a filtered or unfiltered scrolled list for nav browsers.
func (m Model) navScrollItems(total int, labelFn func(int) string) []string {
	maxVisible := m.navVisible()

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
