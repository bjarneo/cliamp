package ui

import (
	"fmt"
	"strings"
)

// truncate shortens s to maxW runes, appending "…" if truncated.
func truncate(s string, maxW int) string {
	r := []rune(s)
	if len(r) > maxW {
		return string(r[:maxW-1]) + "…"
	}
	return s
}

// cursorLine renders a list item with "> " prefix when active, "  " otherwise.
func cursorLine(label string, active bool) string {
	if active {
		return playlistSelectedStyle.Render("> " + label)
	}
	return dimStyle.Render("  " + label)
}

// scrollStart returns the scroll offset so that cursor remains visible
// within a window of maxVisible items.
func scrollStart(cursor, maxVisible int) int {
	if cursor >= maxVisible {
		return cursor - maxVisible + 1
	}
	return 0
}

// padLines appends empty strings so that rendered items fill maxVisible rows.
func padLines(lines []string, maxVisible, rendered int) []string {
	for range maxVisible - rendered {
		lines = append(lines, "")
	}
	return lines
}

// helpKey renders a key in accent color inside dim brackets, followed by a dim label.
func helpKey(key, label string) string {
	return dimStyle.Render("[") + activeToggle.Render(key) + dimStyle.Render("]") + helpStyle.Render(label)
}

// albumSeparator builds a full-width album divider line.
func albumSeparator(album string, year int) string {
	label := "── " + album
	if year != 0 {
		label += fmt.Sprintf(" (%d)", year)
	}
	label += " "
	if labelLen := len([]rune(label)); labelLen < panelWidth {
		label += strings.Repeat("─", panelWidth-labelLen)
	}
	return dimStyle.Render(label)
}

// navScrollItems renders a filtered or unfiltered scrolled list for nav browsers.
func (m Model) navScrollItems(total int, labelFn func(int) string) []string {
	maxVisible := m.plVisible
	if maxVisible < 5 {
		maxVisible = 5
	}

	useFilter := len(m.navSearchIdx) > 0 || m.navSearch != ""
	scroll := m.navScroll

	var lines []string
	rendered := 0

	if useFilter {
		for j := scroll; j < len(m.navSearchIdx) && rendered < maxVisible; j++ {
			label := labelFn(m.navSearchIdx[j])
			lines = append(lines, cursorLine(label, j == m.navCursor))
			rendered++
		}
	} else {
		for i := scroll; i < total && rendered < maxVisible; i++ {
			label := labelFn(i)
			lines = append(lines, cursorLine(label, i == m.navCursor))
			rendered++
		}
	}

	return padLines(lines, maxVisible, rendered)
}

// navCountLine renders an "X/Y noun (filtered)" footer.
func (m Model) navCountLine(noun string, total int) string {
	if len(m.navSearchIdx) > 0 || m.navSearch != "" {
		return dimStyle.Render(fmt.Sprintf("  %d/%d %s (filtered)", len(m.navSearchIdx), total, noun))
	}
	return dimStyle.Render(fmt.Sprintf("  %d/%d %s", m.navCursor+1, total, noun))
}

// navSearchBar renders the search input or a help-key hint as footer lines.
func (m Model) navSearchBar(defaultHelp string) []string {
	if m.navSearching {
		return []string{"", playlistSelectedStyle.Render("  / " + m.navSearch + "_")}
	}
	if m.navSearch != "" {
		return []string{"", dimStyle.Render("  / "+m.navSearch) + " " + helpKey("/", "Clear")}
	}
	return []string{"", defaultHelp}
}
