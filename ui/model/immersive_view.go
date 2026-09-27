package model

// immersive_view.go renders the immersive frame: top bar, three panes, and a
// bottom player bar. It draws into the panel rectangle the normal layout
// already computes, so frame padding and FitRect clipping come for free.

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// Frame geometry. The player bar is two content rows (transport + seek) plus
// one hint/status row; the top bar is one row with a spacer under it.
const (
	immTopBarRows    = 2 // bar + spacer
	immPlayerBarRows = 4 // spacer + transport + seek + status/hint
	immRailMinW      = 18
	immRailMaxW      = 28
	immRailIconW     = 5 // collapsed rail width
	immRightMinW     = 24
	immRightMaxW     = 32
	immPaneSep       = 1 // separator column between panes
	immCardGap       = 1 // blank column between grid cards
	immCardArtRows   = 4 // art block height in grid cards
	immHeroArtRows   = 6 // hero art block height
	immRoloArtRows   = 5 // rolodex card art height
	immRoloStripRows = 8 // art + title + sub + position row
)

// immArtChars are the shade glyphs a text-art cover picks from. The pick is a
// hash of the item name, so covers stay stable for a session.
var immArtChars = []rune{'█', '▓', '▒', '░'}

// fitCell pads or truncates s to exactly w display cells.
func fitCell(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) > w {
		s = ansi.Truncate(s, w, "")
	}
	if d := w - lipgloss.Width(s); d > 0 {
		s += strings.Repeat(" ", d)
	}
	return s
}

// padPane normalizes a pane's lines to exactly rows tall and w cells wide.
func padPane(lines []string, w, rows int) []string {
	if len(lines) > rows {
		lines = lines[:rows]
	}
	out := make([]string, 0, rows)
	for _, l := range lines {
		out = append(out, fitCell(l, w))
	}
	for len(out) < rows {
		out = append(out, strings.Repeat(" ", w))
	}
	return out
}

// renderImmersive lays out the full frame.
func (m Model) renderImmersive() string {
	w := m.layout.panelWidth
	if w <= 0 {
		w = 74
	}
	h := m.height - 2*m.layout.paddingV
	if h <= 0 {
		h = 22
	}
	bodyRows := h - immTopBarRows - immPlayerBarRows
	if bodyRows < 3 {
		bodyRows = 3
	}

	// Pane widths: rail collapses to an icon strip on `c`; the right rail
	// drops first on narrow terminals, then the rail hides entirely.
	railW, rightW := m.immersiveRailWidth(w), m.immersiveRightWidth(w)
	centerW := w - railW - rightW
	if railW > 0 {
		centerW -= immPaneSep
	}
	if rightW > 0 {
		centerW -= immPaneSep
	}
	if centerW < 20 {
		rightW = 0
		centerW = w - railW
		if railW > 0 {
			centerW -= immPaneSep
		}
	}

	sep := padPane([]string{""}, immPaneSep, bodyRows)
	var columns []string
	if railW > 0 {
		columns = append(columns, strings.Join(padPane(m.renderImmRail(railW, bodyRows), railW, bodyRows), "\n"))
		columns = append(columns, strings.Join(sep, "\n"))
	}
	columns = append(columns, strings.Join(padPane(m.renderImmCenter(centerW, bodyRows), centerW, bodyRows), "\n"))
	if rightW > 0 {
		columns = append(columns, strings.Join(sep, "\n"))
		columns = append(columns, strings.Join(padPane(m.renderImmRight(rightW, bodyRows), rightW, bodyRows), "\n"))
	}

	return strings.Join([]string{
		m.renderImmTopBar(w),
		"",
		lipgloss.JoinHorizontal(lipgloss.Top, columns...),
		"",
		m.renderImmPlayerBar(w),
		m.renderImmSeekRow(w),
		m.renderImmStatusLine(w),
	}, "\n")
}

func (m Model) immersiveRailWidth(w int) int {
	if m.immersive.railCollapsed {
		return immRailIconW
	}
	if w < 64 {
		return 0
	}
	return clampInt(w/4, immRailMinW, immRailMaxW)
}

func (m Model) immersiveRightWidth(w int) int {
	switch {
	case w >= 110:
		return immRightMaxW
	case w >= 84:
		return immRightMinW
	default:
		return 0
	}
}

// Budget helpers shared with the key handler so cursor math and drawing agree.

func (m Model) immersiveRailBudget() int {
	h := m.height - 2*m.layout.paddingV
	// Header (title + pills + filter row + blank) occupies the first rows.
	return max(1, h-immTopBarRows-immPlayerBarRows-5)
}

func (m Model) immersiveRightBudget() int {
	h := m.height - 2*m.layout.paddingV
	// Tabs + "Now playing" row + section header.
	return max(1, h-immTopBarRows-immPlayerBarRows-6)
}

func (m Model) immersiveTableBudget() int {
	h := m.height - 2*m.layout.paddingV
	// Hero block (immHeroArtRows + sub rows) + action row + column header.
	return max(1, h-immTopBarRows-immPlayerBarRows-immHeroArtRows-5)
}

func (m Model) immersivePageStep() int {
	return max(3, m.immersiveTableBudget()-1)
}

func (m Model) immersiveGridCols() int {
	w := m.layout.panelWidth
	center := w - m.immersiveRailWidth(w) - m.immersiveRightWidth(w) - 2*immPaneSep
	cardW := 16
	cols := center / (cardW + immCardGap)
	return clampInt(cols, 1, 8)
}

// — top bar —

func (m Model) renderImmTopBar(w int) string {
	left := dimStyle.Render("‹ ›") + "  " + titleStyle.Render("⌂") + "  "
	var mid string
	switch {
	case m.immersive.searching:
		mid = helpKeyStyle.Render(" / ") + " " + m.immersive.searchQuery + "▌"
	case m.immersive.railFiltering:
		mid = helpKeyStyle.Render(" f ") + " filter library: " + m.immersive.railFilter + "▌"
	default:
		mid = dimStyle.Render(" / search · what do you want to play?")
	}
	prov := ""
	if m.immersive.prov != nil {
		prov = m.immersive.prov.Name()
	}
	right := dimStyle.Render(prov+"  ") + helpKeyStyle.Render(" I ") + dimStyle.Render(" exit")
	line := left + mid
	if pad := w - lipgloss.Width(line) - lipgloss.Width(right); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return fitCell(line+right, w)
}

// — left library rail —

func (m Model) renderImmRail(w, rows int) []string {
	if m.immersive.railCollapsed {
		return m.renderImmRailCollapsed(w, rows)
	}
	lines := []string{
		labelStyle.Render("≡ Your Library"),
		"",
	}
	// Pills: full names when the rail can hold them, initials when it cannot.
	var pills []string
	pillLabels := immSectionLabels
	if w < 28 {
		pillLabels = [immSectionCount]string{"P", "A", "R"}
	}
	for s := immersiveRailSection(0); s < immSectionCount; s++ {
		label := pillLabels[s]
		if s == m.immersive.railSection {
			pills = append(pills, helpKeyStyle.Render("["+label+"]"))
		} else {
			pills = append(pills, dimStyle.Render(" "+label+" "))
		}
	}
	lines = append(lines, strings.Join(pills, ""), "")
	// Filter/sort row.
	filterRow := dimStyle.Render("f ⌕") + "  " + dimStyle.Render(immRailSortLabels[m.immersive.railSort]+" ≣")
	if m.immersive.railFilter != "" {
		filterRow = dimStyle.Render("⌕ ") + trackStyle.Render(m.immersive.railFilter)
	}
	lines = append(lines, filterRow, "")

	railRows := m.railRows()
	switch m.immersive.railSection {
	case immSectionPlaylists:
		if m.immersive.loadingLists {
			return append(lines, dimStyle.Render("  loading…"))
		}
	case immSectionAlbums:
		if m.immersive.loadingAlbums {
			return append(lines, dimStyle.Render("  loading…"))
		}
	case immSectionArtists:
		if m.immersive.loadingArtists {
			return append(lines, dimStyle.Render("  loading…"))
		}
	}
	if len(railRows) == 0 {
		return append(lines, dimStyle.Render("  (empty)"))
	}

	budget := rows - len(lines)
	scroll := clampedScroll(m.immersive.railScroll, m.immersive.railCursor, len(railRows), max(1, budget/2))
	for i := scroll; i < len(railRows) && len(lines) < rows; i++ {
		r := railRows[i]
		icon, name, sub := railRowGlyph(r.kind), r.name, r.sub
		selected := i == m.immersive.railCursor
		nameStyle := playlistItemStyle
		iconStyle := dimStyle
		if selected && m.immersive.focus == immPaneRail {
			nameStyle = playlistSelectedStyle
			iconStyle = playlistSelectedStyle
		}
		if selected && m.immersive.focus != immPaneRail {
			iconStyle = playlistActiveStyle
		}
		lines = append(lines,
			iconStyle.Render(icon)+" "+nameStyle.Render(ansi.Truncate(name, max(1, w-4), "…")),
			"   "+dimStyle.Render(ansi.Truncate(sub, max(1, w-4), "…")))
	}
	return lines
}

func (m Model) renderImmRailCollapsed(w, rows int) []string {
	lines := []string{labelStyle.Render("≡"), ""}
	railRows := m.railRows()
	for i, r := range railRows {
		if len(lines) >= rows {
			break
		}
		icon := railRowGlyph(r.kind)
		style := dimStyle
		if i == m.immersive.railCursor {
			style = playlistActiveStyle
			if m.immersive.focus == immPaneRail {
				style = playlistSelectedStyle
			}
		}
		lines = append(lines, style.Render(" "+icon))
	}
	return lines
}

func railRowGlyph(kind roloItemKind) string {
	switch kind {
	case roloKindArtist:
		return "◯"
	case roloKindAlbum:
		return "▧"
	default:
		return "▤"
	}
}

// — center pane —

func (m Model) renderImmCenter(w, rows int) []string {
	if m.immersive.roloMode {
		return m.renderImmRolodexBig(w, rows)
	}
	switch m.immersive.view {
	case immViewPlaylist, immViewAlbum:
		return m.renderImmTrackView(w, rows, false)
	case immViewArtist:
		return m.renderImmTrackView(w, rows, true)
	case immViewSearch:
		return m.renderImmTrackView(w, rows, false)
	default:
		return m.renderImmHome(w, rows)
	}
}

// renderImmHome draws the rolodex strip over a "Jump back in" card grid.
func (m Model) renderImmHome(w, rows int) []string {
	lines := m.renderImmRolodexStrip(w)
	lines = append(lines, "", labelStyle.Render("Jump back in"), "")
	grid := m.renderImmCardGrid(w, rows-len(lines))
	return append(lines, grid...)
}

// — rolodex —

// renderImmRolodexStrip draws the spinning deck: the focused card large and
// bright, neighbors stepping down in width and brightness to the sides.
func (m Model) renderImmRolodexStrip(w int) []string {
	items := m.roloItems()
	if len(items) == 0 {
		if m.immersive.loadingLists || m.immersive.loadingAlbums || m.immersive.loadingArtists {
			return []string{dimStyle.Render("  loading library…")}
		}
		return []string{dimStyle.Render("  nothing to browse yet — pick a provider with the library")}
	}
	cur := wrapIndex(m.immersive.roloCursor, len(items))

	focusW := clampInt(w/4, 14, 22)
	neighW := clampInt(w/9, 8, 14)

	// Build columns for offsets -2..+2, fading out.
	type col struct {
		lines []string
		w     int
	}
	cols := make([]col, 0, 5)
	for off := -2; off <= 2; off++ {
		idx := wrapIndex(cur+off, len(items))
		cw := neighW
		depth := off
		if depth < 0 {
			depth = -depth
		}
		style := dimStyle
		if off == 0 {
			cw = focusW
			style = playlistSelectedStyle
		}
		card := roloCard(items[idx], cw, immRoloArtRows, style, off == 0, depth)
		cols = append(cols, col{lines: card, w: cw})
	}
	totalW := 0
	for _, c := range cols {
		totalW += c.w + 1
	}
	pad := max(0, (w-totalW)/2)

	merged := make([]string, 0, immRoloStripRows-1)
	maxRows := 0
	for _, c := range cols {
		if len(c.lines) > maxRows {
			maxRows = len(c.lines)
		}
	}
	for r := 0; r < maxRows; r++ {
		line := strings.Repeat(" ", pad)
		for _, c := range cols {
			if r < len(c.lines) {
				line += fitCell(c.lines[r], c.w)
			} else {
				line += strings.Repeat(" ", c.w)
			}
			line += " "
		}
		merged = append(merged, line)
	}
	// Position indicator centered under the focused card.
	pos := fmt.Sprintf("◂ %d/%d ▸", cur+1, len(items))
	if m.immersive.roloSpin > 0 {
		pos += " " + dimStyle.Render("≫")
	}
	posPad := pad + 2*(neighW+1) + max(0, (focusW-lipgloss.Width(pos))/2)
	merged = append(merged, strings.Repeat(" ", posPad)+dimStyle.Render(pos))
	return merged
}

// roloCard renders one deck card: rounded corners for artists, square for
// everything else, with the art fill hashed from the item name.
func roloCard(item roloItem, w, artRows int, style lipgloss.Style, focused bool, depth int) []string {
	art := immArtBlock(item.title, w-2, artRows-2, focused)
	top, bottom := "┌", "└"
	tr, br := "┐", "┘"
	if item.kind == roloKindArtist {
		top, bottom, tr, br = "╭", "╰", "╮", "╯"
	}
	inner := strings.Repeat("─", w-2)
	lines := []string{style.Render(top + inner + tr)}
	for _, row := range art {
		lines = append(lines, style.Render("│")+row+style.Render("│"))
	}
	lines = append(lines, style.Render(bottom+inner+br))
	if focused {
		lines = append(lines,
			playlistSelectedStyle.Render(fitCell(ansi.Truncate(item.title, w, "…"), w)),
			dimStyle.Render(fitCell(ansi.Truncate(item.sub, w, "…"), w)))
	} else if depth == 1 {
		lines = append(lines, dimStyle.Render(fitCell(ansi.Truncate(item.title, w, "…"), w)))
	}
	return lines
}

// immArtBlock draws a deterministic shade-glyph mosaic stand-in for cover art.
func immArtBlock(name string, w, h int, bright bool) []string {
	if w < 1 || h < 1 {
		return nil
	}
	var seed uint32 = 5381
	for _, r := range name {
		seed = seed*33 + uint32(r)
	}
	style := dimStyle
	if bright {
		style = lipgloss.NewStyle().Foreground(ui.ColorAccent)
	}
	lines := make([]string, 0, h)
	for y := 0; y < h; y++ {
		var row strings.Builder
		for x := 0; x < w; x++ {
			v := seed + uint32(x*31+y*17)
			v ^= v >> 9
			row.WriteRune(immArtChars[v%uint32(len(immArtChars))])
		}
		lines = append(lines, style.Render(row.String()))
	}
	// Center a note glyph on the focused card so it reads as a cover, not noise.
	if bright && h >= 3 && w >= 4 {
		mid := h / 2
		padL := (w - 3) / 2
		fill := string(immArtChars[int(seed)%4])
		lines[mid] = style.Render(strings.Repeat(fill, padL) + " ♪ " + strings.Repeat(fill, max(0, w-padL-3)))
	}
	return lines
}

// renderImmRolodexBig fills the center pane with the wheel (`o` mode).
func (m Model) renderImmRolodexBig(w, rows int) []string {
	items := m.roloItems()
	if len(items) == 0 {
		return []string{dimStyle.Render("  empty deck")}
	}
	focusW := clampInt(w/3, 16, 30)
	artRows := clampInt(rows/3, immRoloArtRows, 9)
	cur := wrapIndex(m.immersive.roloCursor, len(items))

	type col struct {
		lines []string
		w     int
	}
	cols := make([]col, 0, 7)
	for off := -3; off <= 3; off++ {
		idx := wrapIndex(cur+off, len(items))
		depth := off
		if depth < 0 {
			depth = -depth
		}
		cw := focusW - 4*depth
		if cw < 6 {
			continue
		}
		style := dimStyle
		if off == 0 {
			style = playlistSelectedStyle
		}
		cols = append(cols, col{lines: roloCard(items[idx], cw, artRows, style, off == 0, depth), w: cw})
	}
	totalW := 0
	for _, c := range cols {
		totalW += c.w + 1
	}
	pad := max(0, (w-totalW)/2)
	topPad := max(0, (rows-artRows-4)/2)
	var merged []string
	for i := 0; i < topPad; i++ {
		merged = append(merged, "")
	}
	maxRows := 0
	for _, c := range cols {
		if len(c.lines) > maxRows {
			maxRows = len(c.lines)
		}
	}
	for r := 0; r < maxRows; r++ {
		line := strings.Repeat(" ", pad)
		for _, c := range cols {
			if r < len(c.lines) {
				line += fitCell(c.lines[r], c.w)
			} else {
				line += strings.Repeat(" ", c.w)
			}
			line += " "
		}
		merged = append(merged, line)
	}
	pos := fmt.Sprintf("◂ %d/%d ▸   h/l spin · enter %s · o close", cur+1, len(items), roloEnterVerb(items[cur].kind))
	merged = append(merged, "", strings.Repeat(" ", max(0, (w-lipgloss.Width(pos))/2))+dimStyle.Render(pos))
	return merged
}

func roloEnterVerb(kind roloItemKind) string {
	if kind == roloKindTrack {
		return "plays"
	}
	return "opens"
}

// — card grid —

func (m Model) renderImmCardGrid(w, rows int) []string {
	items := m.roloItems()
	if len(items) == 0 {
		return nil
	}
	cols := m.immersiveGridCols()
	cardW := max(10, (w-(cols-1)*immCardGap)/cols)
	gridCursor := clampInt(m.immersive.gridCursor, 0, len(items)-1)

	// A card row is art+labels+blank lines tall; scroll rows so the focused
	// card stays on screen.
	rowH := immCardArtRows + 3
	visibleRows := max(1, rows/rowH)
	curRow := gridCursor / cols
	firstRow := 0
	if curRow >= visibleRows {
		firstRow = curRow - visibleRows + 1
	}

	var lines []string
	for start := firstRow * cols; start < len(items) && len(lines) < rows; start += cols {
		var rowLines []string
		for r := 0; r < immCardArtRows+2; r++ {
			rowLines = append(rowLines, "")
		}
		for c := 0; c < cols && start+c < len(items); c++ {
			idx := start + c
			item := items[idx]
			focused := idx == gridCursor && m.immersive.focus == immPaneCenter && m.immersive.zone == zoneGrid
			style := dimStyle
			if focused {
				style = playlistSelectedStyle
			}
			card := gridCard(item, cardW, style, focused)
			for r := 0; r < len(card) && r < len(rowLines); r++ {
				rowLines[r] += fitCell(card[r], cardW)
				if c < cols-1 {
					rowLines[r] += strings.Repeat(" ", immCardGap)
				}
			}
		}
		lines = append(lines, rowLines...)
		lines = append(lines, "")
	}
	return lines
}

// gridCard is a small collection card: square for playlists/albums, round for
// artists, art mosaic inside, name and subtitle below.
func gridCard(item roloItem, w int, style lipgloss.Style, focused bool) []string {
	art := immArtBlock(item.title, w-2, immCardArtRows-2, focused)
	top, bottom, tr, br := "┌", "└", "┐", "┘"
	if item.kind == roloKindArtist {
		top, bottom, tr, br = "╭", "╰", "╮", "╯"
	}
	inner := strings.Repeat("─", max(0, w-2))
	lines := []string{style.Render(top + inner + tr)}
	for _, row := range art {
		lines = append(lines, style.Render("│")+row+style.Render("│"))
	}
	lines = append(lines, style.Render(bottom+inner+br))
	lines = append(lines,
		style.Render(fitCell(ansi.Truncate(item.title, w, "…"), w)),
		dimStyle.Render(fitCell(ansi.Truncate(item.sub, w, "…"), w)))
	return lines
}

// — track-table views (playlist / album / artist / search) —

func (m Model) renderImmTrackView(w, rows int, circleArt bool) []string {
	var lines []string
	// Hero: art block + kicker/title/sub.
	artW := min(immHeroArtRows*3, max(12, w/4))
	art := immArtBlock(m.immersive.ctxName, artW, immHeroArtRows-1, true)
	kicker := "Playlist"
	switch m.immersive.view {
	case immViewAlbum:
		kicker = "Album"
	case immViewArtist:
		kicker = "Artist"
	case immViewSearch:
		kicker = "Search results"
	}
	heroText := []string{
		dimStyle.Render(kicker),
		"",
		titleStyle.Render(ansi.Truncate(m.immersive.ctxName, max(1, w-artW-4), "…")),
		"",
		dimStyle.Render(ansi.Truncate(m.immersive.ctxSub, max(1, w-artW-4), "…")),
		"",
	}
	for i := 0; i < immHeroArtRows; i++ {
		var artRow string
		if i < len(art) {
			artRow = fitCell(art[i], artW)
		} else {
			artRow = strings.Repeat(" ", artW)
		}
		text := ""
		if i < len(heroText) {
			text = heroText[i]
		}
		lines = append(lines, artRow+"  "+text)
	}
	// Action row.
	lines = append(lines,
		helpKeyStyle.Render(" ▶ ")+dimStyle.Render(" play  ")+
			helpKeyStyle.Render(" z ")+dimStyle.Render(" shuffle  ")+
			helpKeyStyle.Render(" t ")+dimStyle.Render(" sort: "+immSortTrackLabels[m.immersive.trackSort]+"  ")+
			helpKeyStyle.Render(" o ")+dimStyle.Render(" rolodex"),
		"")

	if m.immersive.tracksLoading || m.immersive.artistLoading {
		return append(lines, dimStyle.Render("  loading…"))
	}
	tracks := m.sortedTracks()
	if len(tracks) == 0 {
		return append(lines, dimStyle.Render("  (empty)"))
	}

	// Column header with the sort marker on the active column.
	header := m.immTableHeader(w)
	lines = append(lines, header, dimStyle.Render(strings.Repeat("─", w)))
	return append(lines, m.immTableRows(w, rows-len(lines), tracks)...)
}

func (m Model) immTableHeader(w int) string {
	durW := 6
	numW := 4
	albumW := max(10, w/4)
	titleW := max(10, w-numW-albumW-durW-3)
	mark := func(col immersiveTrackSort) string {
		if m.immersive.trackSort == col && m.immersive.trackSort != immSortTrackOrder {
			return " ↓"
		}
		return ""
	}
	return dimStyle.Render(
		fitCell("  #", numW) +
			fitCell("Title"+mark(immSortTrackTitle), titleW) +
			fitCell("Album"+mark(immSortTrackAlbum), albumW) +
			fitCell("⏱"+mark(immSortTrackDuration), durW))
}

func (m Model) immTableRows(w, rows int, tracks []playlist.Track) []string {
	durW := 6
	numW := 4
	albumW := max(10, w/4)
	titleW := max(10, w-numW-albumW-durW-3)
	budget := max(1, rows)
	scroll := clampedScroll(m.immersive.trackScroll, m.immersive.trackCursor, len(tracks), budget)
	playing, _ := m.currentPlaybackTrack()
	var lines []string
	for i := scroll; i < len(tracks) && len(lines) < budget; i++ {
		t := tracks[i]
		selected := i == m.immersive.trackCursor
		name := trackViewName(t)
		if t.Title != "" && t.Artist != "" {
			name = t.Title + " · " + t.Artist
		}
		nameCell := fitCell(ansi.Truncate(name, max(1, titleW-2), "…"), titleW)
		albumCell := fitCell(ansi.Truncate(t.Album, max(1, albumW-1), "…"), albumW)
		dur := formatTrackTime(t.DurationSecs)
		durCell := fitCell(dur, durW)
		num := fmt.Sprintf("%*d", numW-1, i+1) + " "
		style := playlistItemStyle
		numStyle := dimStyle
		switch {
		case t.Path == playing.Path && playing.Path != "":
			num = fmt.Sprintf("%*s", numW-1, "♪") + " "
			style = playlistActiveStyle
			numStyle = playlistActiveStyle
		case selected:
			style = playlistSelectedStyle
			numStyle = playlistSelectedStyle
		}
		if t.Unplayable {
			style = playlistUnavailableStyle
			if selected {
				style = dimStyle
			}
		}
		lines = append(lines, numStyle.Render(num)+style.Render(nameCell)+dimStyle.Render(albumCell)+dimStyle.Render(durCell))
	}
	return lines
}

// — right rail —

func (m Model) renderImmRight(w, rows int) []string {
	// Tabs.
	var tabs []string
	for t := immersiveRightTab(0); t < immTabCount; t++ {
		label := "Now playing"
		if t == immTabQueue {
			label = "Queue"
		}
		if t == m.immersive.rightTab {
			tabs = append(tabs, helpKeyStyle.Render(" "+label+" "))
		} else {
			tabs = append(tabs, dimStyle.Render(" "+label+" "))
		}
	}
	lines := []string{strings.Join(tabs, ""), ""}
	if m.immersive.rightTab == immTabQueue {
		return append(lines, m.renderImmQueue(w, rows-len(lines))...)
	}
	return append(lines, m.renderImmNowPlaying(w, rows-len(lines))...)
}

func (m Model) renderImmNowPlaying(w, rows int) []string {
	track, _ := m.currentPlaybackTrack()
	name := track.Title
	if name == "" {
		name = trackViewName(track)
	}
	if name == "" {
		name = "Nothing playing"
	}
	var lines []string
	ctx := m.playingContextName()
	if ctx != "" {
		lines = append(lines, labelStyle.Render(ansi.Truncate(ctx, max(1, w), "…")), "")
	}
	art := immArtBlock(name, w-4, min(6, rows/3), true)
	lines = append(lines, art...)
	lines = append(lines, "")
	liked := ""
	if m.favSet != nil {
		if _, ok := m.favSet[track.Path]; ok {
			liked = " " + favMarkerStyle.Render(favHeart)
		}
	}
	lines = append(lines,
		playlistActiveStyle.Render(ansi.Truncate(name, max(1, w-3), "…"))+liked,
		dimStyle.Render(ansi.Truncate(track.Artist, max(1, w), "…")),
		"")
	if m.immersive.artistLoading {
		lines = append(lines, dimStyle.Render("About the artist"), dimStyle.Render("  loading…"))
	} else if d := m.immersive.artistMeta; d.Info.Name != "" {
		lines = append(lines, labelStyle.Render("About the artist"), "")
		lines = append(lines, immArtBlock(d.Info.Name, w-4, 3, false)...)
		lines = append(lines, playlistItemStyle.Render(d.Info.Name))
		if d.Followers > 0 {
			lines = append(lines, dimStyle.Render(fmt.Sprintf("%s monthly listeners", commaNum(d.Followers))))
		}
		if len(d.Genres) > 0 {
			lines = append(lines, dimStyle.Render(ansi.Truncate(strings.Join(d.Genres, ", "), max(1, w), "…")))
		}
	}
	return lines
}

func (m Model) renderImmQueue(w, rows int) []string {
	var lines []string
	track, _ := m.currentPlaybackTrack()
	if name := trackViewName(track); name != "" {
		lines = append(lines, dimStyle.Render("Now playing"),
			playlistActiveStyle.Render("♪ "+ansi.Truncate(name, max(1, w-3), "…")))
		if track.Artist != "" {
			lines = append(lines, "  "+dimStyle.Render(ansi.Truncate(track.Artist, max(1, w-3), "…")))
		}
		lines = append(lines, "")
	}
	ctx := m.playingContextName()
	if ctx == "" {
		ctx = "queue"
	}
	lines = append(lines, dimStyle.Render("Next from: "+ctx))
	total := m.playlist.QueueLen()
	if total == 0 {
		lines = append(lines, "", dimStyle.Render("  (queue empty — 'a' on a track adds it)"))
		return lines
	}
	budget := max(1, rows-len(lines))
	scroll := clampedScroll(m.immersive.rightScroll, m.immersive.rightCursor, total, budget)
	for i := scroll; i < total && len(lines) < rows; i++ {
		tracks := m.playlist.QueueWindow(i, 1)
		if len(tracks) == 0 {
			break
		}
		t := tracks[0]
		name := t.Title
		if name == "" {
			name = trackViewName(t)
		}
		if t.Artist != "" && t.Title != "" {
			name += " · " + t.Artist
		}
		style := playlistItemStyle
		num := dimStyle.Render(fmt.Sprintf("%2d ", i+1))
		if i == m.immersive.rightCursor && m.immersive.focus == immPaneRight {
			style = playlistSelectedStyle
			num = playlistSelectedStyle.Render(fmt.Sprintf("%2d ", i+1))
		}
		lines = append(lines, num+style.Render(ansi.Truncate(name, max(1, w-4), "…")))
	}
	return lines
}

// playingContextName names the list the live queue is playing from.
func (m Model) playingContextName() string {
	if m.loadedPlaylist != "" {
		return m.loadedPlaylist
	}
	if m.activeProviderPlaylistID != "" {
		for _, l := range m.immersive.lists {
			if l.ID == m.activeProviderPlaylistID {
				return l.Name
			}
		}
	}
	return ""
}

// — player bar —

func (m Model) renderImmPlayerBar(w int) string {
	track, _ := m.currentPlaybackTrack()
	name := trackViewName(track)
	if name == "" {
		name = "—"
	}
	liked := " "
	if m.favSet != nil {
		if _, ok := m.favSet[track.Path]; ok {
			liked = " " + favMarkerStyle.Render(favHeart) + " "
		}
	}
	left := dimStyle.Render("♪ ") + trackStyle.Render(name)
	if track.Artist != "" {
		left += dimStyle.Render(" — " + track.Artist)
	}
	left += liked
	left = ansi.Truncate(left, max(1, w/3), "…")

	shufStyle, repStyle := dimStyle, dimStyle
	if m.playlist != nil && m.playlist.Shuffled() {
		shufStyle = playlistActiveStyle
	}
	if m.playlist != nil && m.playlist.Repeat() != 0 {
		repStyle = playlistActiveStyle
	}
	play := "▶"
	if m.isPlaying() {
		play = "⏸"
	}
	transport := shufStyle.Render("≀") + "  " + dimStyle.Render("⏮") + "  " +
		statusStyle.Render(play) + "  " + dimStyle.Render("⏭") + "  " + repStyle.Render("↻")

	var vol float64
	volMin := -60.0
	if m.player != nil {
		vol = m.player.Volume()
		volMin = m.player.VolumeMin()
	}
	volFrac := 0
	if vol > volMin {
		volFrac = clampInt(int(6*(vol-volMin)/(6-volMin)), 0, 6)
	}
	volBar := strings.Repeat("▮", volFrac) + strings.Repeat("▯", 6-volFrac)
	right := dimStyle.Render(volBar+fmt.Sprintf(" %+0.0fdB  ", vol)) +
		dimStyle.Render("≣ q") + "  " + dimStyle.Render("◈ V")

	line := left
	midPad := max(1, (w-lipgloss.Width(transport))/2-lipgloss.Width(line))
	line += strings.Repeat(" ", midPad) + transport
	rightPad := max(1, w-lipgloss.Width(line)-lipgloss.Width(right))
	line += strings.Repeat(" ", rightPad) + right
	return fitCell(line, w)
}

func (m Model) renderImmSeekRow(w int) string {
	pos := m.cachedPos
	dur := m.cachedDur
	posText := formatTrackTime(int(pos.Seconds()))
	durText := formatTrackTime(int(dur.Seconds()))
	if durText == "" {
		durText = "--:--"
	}
	if posText == "" {
		posText = "0:00"
	}
	barW := max(4, w-lipgloss.Width(posText)-lipgloss.Width(durText)-4)
	var fill float64
	if dur > 0 {
		fill = float64(pos) / float64(dur)
	}
	head := clampInt(int(fill*float64(barW)), 0, barW-1)
	bar := seekFillStyle.Render(strings.Repeat(seekFillGlyph, head)) +
		seekFillStyle.Render(seekHeadGlyph) +
		seekDimStyle.Render(strings.Repeat(seekEmptyGlyph, barW-head-1))
	return dimStyle.Render(posText) + " " + bar + " " + dimStyle.Render(durText)
}

// renderImmStatusLine is the transient message + key-hint row.
func (m Model) renderImmStatusLine(w int) string {
	if line := m.renderTransient(); line != "" {
		return fitCell(line, w)
	}
	hints := "I exit · tab panes · hjkl move · ⏎ open/play · / search · o rolodex · q queue · V vis"
	return fitCell(dimStyle.Render(hints), w)
}

// commaNum renders 1234567 as 1,234,567 for artist follower counts.
func commaNum(n int) string {
	s := fmt.Sprintf("%d", n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
