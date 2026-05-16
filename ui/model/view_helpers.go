package model

import (
	"fmt"
	"iter"
	"strings"
	"time"
	"unicode/utf8"

	"cliamp/playlist"
	"cliamp/ui"
)

// formatListRangeCount returns a human-readable viewport counter.
// It emits "start-end of total" when partially visible, and collapses to
// "total" when the full list is visible.
func (m Model) formatListRangeCount(startIndex, visibleCount, total int) string {
	if total <= 0 || visibleCount <= 0 {
		return "0"
	}
	if startIndex >= total {
		startIndex = max(0, total-1)
	}
	start := startIndex + 1
	if start < 1 {
		start = 1
	}
	end := startIndex + visibleCount
	if end > total {
		end = total
	}
	if end < start {
		end = start
	}
	if start == 1 && end == total {
		return fmt.Sprintf("%d", total)
	}
	return fmt.Sprintf("%d-%d of %d", start, end, total)
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

// tracksSubtitle renders a "N tracks · Hh Mm" headline shown under track-list
// titles. Returns "" when the slice is empty so callers can suppress the line.
func tracksSubtitle(tracks []playlist.Track) string {
	if len(tracks) == 0 {
		return ""
	}
	out := fmt.Sprintf("%d tracks", len(tracks))
	if d := formatPlaylistDuration(playlist.TotalDurationSecs(tracks)); d != "" {
		out += " · " + d
	}
	return out
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

// minTracksPerAlbum is the threshold at which a list is considered cohesive
// enough to default to showing album headers; below this average tracks/album,
// the list looks like a fragmented mixtape and headers add noise.
const minTracksPerAlbum = 3.0

// isListCohesive returns true if the track list appears to be organized into
// distinct albums (e.g. an artist's discography or a full album) rather than
// a fragmented mixtape.
func isListCohesive(tracks []playlist.Track) bool {
	headers := 0
	prev := ""
	first := true
	for _, t := range tracks {
		if first || t.Album != prev {
			headers++
			prev = t.Album
			first = false
		}
	}

	if headers == 0 {
		return false
	}
	return float64(len(tracks))/float64(headers) >= minTracksPerAlbum
}

// setInitialHeaderState picks a sensible header default for a freshly loaded
// list. Only call this when the playlist is replaced or first populated —
// calling it after every Add re-runs the heuristic over the whole list (O(N²)
// during incremental loads) and silently overrides the user's Ctrl+H toggle.
func (m *Model) setInitialHeaderState(tracks []playlist.Track) {
	m.showAlbumHeaders = isListCohesive(tracks)
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

// albumSeparator builds a full-width album divider line.
func (m Model) albumSeparator(album string, year int) string {
	if album == "" {
		return dimStyle.Render(strings.Repeat("─", ui.PanelWidth))
	}
	prefix := "── "
	suffix := " "
	label := prefix + album
	if year != 0 {
		label += fmt.Sprintf(" (%d)", year)
	}
	label += suffix
	if labelLen := utf8.RuneCountInString(label); labelLen < ui.PanelWidth {
		label += strings.Repeat("─", ui.PanelWidth-labelLen)
	}
	return dimStyle.Render(label)
}

// navScrollItems renders a filtered or unfiltered scrolled list for nav browsers.
func (m Model) navScrollItems(total int, labelFn func(int) string) []string {
	maxVisible := max(m.plVisible, 5)

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

// navCountLine renders an "X/Y noun (filtered)" footer.

// filterHeader renders the `/` filter input line under a list title. While
// the user is typing it shows an editable bar with a trailing cursor; once
// the input bar is closed but a query is still active, it renders a dim recap
// with an optional "Clear" hint. Returns nil when there's nothing to show.
func filterHeader(searching bool, query, clearHint string) []string {
	if searching {
		return []string{playlistSelectedStyle.Render("  / " + query + "_"), ""}
	}
	if query != "" {
		line := dimStyle.Render("  / " + query)
		if clearHint != "" {
			line += " " + clearHint
		}
		return []string{line, ""}
	}
	return nil
}
