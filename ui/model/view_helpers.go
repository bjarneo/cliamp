package model

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"cliamp/ui"
)

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

// helpKey renders a key as a pill (background-highlighted) followed by a dim label.
func helpKey(key, label string) string {
	return helpKeyStyle.Render(" "+key+" ") + helpStyle.Render(" "+label)
}

// albumSeparator builds a full-width album divider line.
func (m Model) albumSeparator(album string, year int) string {
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
	maxVisible := m.plVisible
	if maxVisible < 5 {
		maxVisible = 5
	}

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
func (m Model) navCountLine(noun string, total int) string {
	if len(m.navBrowser.searchIdx) > 0 || m.navBrowser.search != "" {
		return dimStyle.Render(fmt.Sprintf("  %d/%d %s (filtered)", len(m.navBrowser.searchIdx), total, noun))
	}
	return dimStyle.Render(fmt.Sprintf("  %d/%d %s", m.navBrowser.cursor+1, total, noun))
}

// navSearchBar renders the search input or a help-key hint as footer lines.
func (m Model) navSearchBar(defaultHelp string) []string {
	if m.navBrowser.searching {
		return []string{"", playlistSelectedStyle.Render("  / " + m.navBrowser.search + "_")}
	}
	if m.navBrowser.search != "" {
		return []string{"", dimStyle.Render("  / "+m.navBrowser.search) + " " + helpKey("/", "Clear")}
	}
	return []string{"", defaultHelp}
}
