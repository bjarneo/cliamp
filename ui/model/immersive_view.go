package model

// immersive_view.go renders the immersive frame: a visualizer band, the
// nav-pill row, the two-column body (Now Playing + Queue | canvas), a
// controls row, and the eighth-block progress bar. It draws into the panel
// rectangle the normal layout already computes, so frame padding and
// FitRect clipping come for free.
//
// Every art box drawn here is a fixed cell-aligned rect; the same math
// lives in the layout helpers (immGeom, immCanvasItemsGeom) so the mouse
// hit test and step-2 image placement see the same rectangles.

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// Frame band heights in content rows.
const (
	immVisMinRows = 3  // visualizer band floor
	immVisMaxRows = 12 // visualizer band ceiling
	immBodyMinH   = 12 // body rows kept before the visualizer band grows
	immNavRows    = 3  // nav pill row (pills are 3 cells tall)
	immCtrlRows   = 3  // transport buttons are 3 cells tall
	immSeekRows   = 1
	immStatusRows = 1
	// Vertical order: vis, gap, nav, gap, body, gap, controls, seek, status.
	// immFixedRows is everything except the visualizer band and the body.
	immFixedRows = 1 + immNavRows + 1 + 1 + immCtrlRows + immSeekRows + immStatusRows // = 11

	immGridTileW   = 14 // minimum tile width; actual width divides the canvas
	immRowsArtW    = 6  // art box width in rows view (3 rows tall ≈ square)
	immRowsItemH   = 3  // rows-view item height in cells
	immCtrlBtnW    = 6  // small transport button width (two-cell glyphs center)
	immCtrlPlayW   = 9  // wide play/pause button width
	immVolBarCells = 6
)

// immArtChars are the shade glyphs a text-art cover picks from. The pick is a
// hash of the item name, so covers stay stable for a session.
var immArtChars = []rune{'█', '▓', '▒', '░'}

// Progress bar line glyphs: a heavy line for the played part, a light line
// for the rest, and a heavy-left/light-right head for half-cell steps.
const (
	immBarPlayed = "━"
	immBarHalf   = "╾"
	immBarTrack  = "─"
)

// immRect is a cell-aligned rectangle in frame content coordinates (0,0 is
// the top-left content cell). Step 2's image layer draws into these.
type immRect struct {
	X, Y, W, H int
}

// ImmersiveArtRects returns every art placeholder rect of the last rendered
// immersive frame, translated to absolute screen cells: the Now Playing
// cover box plus each canvas tile/row art box. Empty when the immersive
// frame is not showing.
func (m Model) ImmersiveArtRects() []immRect {
	im := m.immMouse
	if im == nil || !im.valid || m.activeScreen() != screenImmersive {
		return nil
	}
	out := make([]immRect, len(im.artRects))
	for i, r := range im.artRects {
		out[i] = immRect{X: r.X + im.frameX, Y: r.Y + im.topRow, W: r.W, H: r.H}
	}
	return out
}

// immGeom is the immersive frame's layout in content coordinates.
type immGeom struct {
	w, h               int
	visH               int // visualizer band height
	navY               int // first nav pill row
	bodyY              int
	bodyH              int
	ctrlY              int
	seekY              int
	statY              int
	leftW              int
	canvasX            int // canvas outer box
	canvasW            int
	canvasIW, canvasIH int
	leftH              int // left column height: the body plus the controls rows
	npH                int // Now Playing panel height
	npArt              immRect
	queueY             int // Queue panel top
	queueH             int
}

// immFrameRows is the content height the immersive frame fills.
func (m Model) immFrameRows() int {
	if h := m.height - 2*m.layout.paddingV; h > 0 {
		return h
	}
	return 22
}

// immVisRowsFor sizes the visualizer band to about a fifth of the frame, the
// wireframe's proportion, while leaving the body at least immBodyMinH rows.
func immVisRowsFor(h int) int {
	rows := clampInt(h/5, immVisMinRows, immVisMaxRows)
	if room := h - immFixedRows - immBodyMinH; rows > room {
		rows = max(immVisMinRows, room)
	}
	return rows
}

// immGeom computes the frame layout for the current terminal size; it is
// the single source both the renderer and the mouse hit test read.
func (m Model) immGeom() immGeom {
	w := m.layout.panelWidth
	if w <= 0 {
		w = 74
	}
	h := m.immFrameRows()
	visH := immVisRowsFor(h)
	bodyH := max(3, h-immFixedRows-visH)
	navY := visH + 1
	bodyY := navY + immNavRows + 1
	g := immGeom{
		w:     w,
		h:     h,
		visH:  visH,
		navY:  navY,
		bodyY: bodyY,
		bodyH: bodyH,
		ctrlY: bodyY + bodyH + 1,
		seekY: bodyY + bodyH + 1 + immCtrlRows,
		statY: bodyY + bodyH + 1 + immCtrlRows + immSeekRows,
	}
	g.leftW = clampInt(w/5, 18, 26)
	g.canvasX = g.leftW + 1
	g.canvasW = w - g.canvasX
	g.canvasIW = g.canvasW - 2
	g.canvasIH = bodyH - 2
	// The controls sit under the canvas only, so the left column runs on
	// beside them down to the progress bar.
	g.leftH = bodyH + 1 + immCtrlRows

	// Now Playing panel: border + square-ish art + title/artist/album +
	// border. The art box shrinks before the Queue panel does: the queue
	// keeps at least 5 rows (borders, header, two entries).
	inner := g.leftW - 2
	artH := inner / 2
	if maxArt := g.leftH - 10; artH > maxArt {
		artH = max(0, maxArt)
	}
	npH := artH + 5
	g.npH = npH
	g.queueY = npH
	g.queueH = g.leftH - npH
	// Cells are about twice as tall as wide, so a square cover is 2h cells
	// wide; center it when the height cap made it narrower than the panel.
	artW := min(inner, 2*artH)
	g.npArt = immRect{X: 1 + (inner-artW)/2, Y: g.bodyY + 1, W: artW, H: artH}
	return g
}

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

// immPadFrame wraps the immersive content, which renderImmersive already
// cut to exactly panel width x frame rows, in the frame's padding: the cheap
// equivalent of centerFrame(FrameStyle.Render(content)) for content that
// needs no measuring.
func (m Model) immPadFrame(content string) string {
	padH := strings.Repeat(" ", m.layout.paddingH)
	blank := strings.Repeat(" ", m.layout.frameWidth)
	lines := make([]string, 0, m.height)
	for range m.layout.paddingV {
		lines = append(lines, blank)
	}
	for l := range strings.SplitSeq(content, "\n") {
		lines = append(lines, padH+l+padH)
	}
	for range m.layout.paddingV {
		lines = append(lines, blank)
	}
	return strings.Join(lines, "\n")
}

// renderImmersive lays out the full frame.
func (m Model) renderImmersive() string {
	g := m.immGeom()
	lines := make([]string, 0, g.h)
	blank := strings.Repeat(" ", g.w)
	lines = append(lines, m.renderImmVis(g.w, g.visH)...)
	lines = append(lines, blank)
	lines = append(lines, m.renderImmNav(g.w)...)
	lines = append(lines, blank)
	lines = append(lines, m.renderImmBody(g)...) // includes the controls rows
	lines = append(lines, m.renderImmProgress(g.w))
	lines = append(lines, m.renderImmStatusLine(g.w))
	return strings.Join(lines, "\n")
}

// — visualizer band —

func (m Model) renderImmVis(w, rows int) []string {
	if m.immPixelVisOn() {
		return padPane(nil, w, rows) // blank cells under the Sixel frames
	}
	if m.vis == nil || m.vis.Mode == ui.VisNone || m.visualizerDisabled() {
		lines := padPane(nil, w, rows)
		lines[rows/2] = fitCell(dimStyle.Render("  ♪ visualizer off"), w)
		return lines
	}
	out := strings.Split(strings.TrimRight(m.vis.Render(), "\n"), "\n")
	lines := padPane(out, w, rows)
	return lines
}

// — nav pills —

// immPillGeom is one nav pill's box in content coordinates.
type immPillGeom struct {
	section immSection
	box     immRect
}

// immNavGeom places the pill row: Playlists and Artists on the left, a wide
// highlighted Search pill centered, Albums and Podcasts on the right.
func (m Model) immNavGeom(w int) []immPillGeom {
	navY := immVisRowsFor(m.immFrameRows()) + 1
	labelW := func(s immSection) int {
		if s < 0 {
			return 5 // history button: │ ◀ │
		}
		return len(immSectionLabels[s]) + 4
	}
	left := []immSection{immNavBack, immNavForward, immSecPlaylists, immSecArtists}
	right := []immSection{immSecAlbums, immSecPodcasts}
	searchW := max(20, w/3)

	leftEnd := 0
	for _, s := range left {
		leftEnd += labelW(s) + 1
	}
	rightW := 0
	for _, s := range right {
		rightW += labelW(s) + 1
	}
	searchX := clampInt((w-searchW)/2, leftEnd, max(leftEnd, w-rightW-searchW))
	rightX := max(searchX+searchW+1, w-rightW+1)

	geoms := make([]immPillGeom, 0, immSecCount)
	x := 0
	for _, s := range left {
		pw := labelW(s)
		geoms = append(geoms, immPillGeom{section: s, box: immRect{X: x, Y: navY, W: pw, H: immNavRows}})
		x += pw + 1
	}
	geoms = append(geoms, immPillGeom{section: immSecSearch, box: immRect{X: searchX, Y: navY, W: searchW, H: immNavRows}})
	x = rightX
	for _, s := range right {
		pw := labelW(s)
		geoms = append(geoms, immPillGeom{section: s, box: immRect{X: x, Y: navY, W: pw, H: immNavRows}})
		x += pw + 1
	}
	return geoms
}

// immPillLabel is the pill's inner text — the section name, or the live
// query while the search input is open.
func (m Model) immPillLabel(s immSection) string {
	switch s {
	case immNavBack:
		return "◀"
	case immNavForward:
		return "▶"
	}
	if s == immSecSearch {
		if m.immersive.searching {
			return "⌕ " + m.immersive.searchQuery + "▌"
		}
		if m.immersive.view == immViewSearch && m.immersive.ctxName != "" {
			return "⌕ " + m.immersive.ctxName
		}
		return "⌕ Search"
	}
	return immSectionLabels[s]
}

// immHistoryAvailable reports whether a history button has anywhere to go.
func (m Model) immHistoryAvailable(button immSection) bool {
	im := m.immersive
	if button == immNavForward {
		return len(im.fwd) > 0
	}
	return len(im.back) > 0 || im.view != immRootView(im.section) || im.ctxName != ""
}

// renderImmNav draws the pill row: three rows of rounded box-drawing pills.
func (m Model) renderImmNav(w int) []string {
	rows := make([]string, immNavRows)
	for i := range rows {
		rows[i] = strings.Repeat(" ", w)
	}
	geoms := m.immNavGeom(w)
	for r := 0; r < immNavRows; r++ {
		var line strings.Builder
		x := 0
		for _, p := range geoms {
			if p.box.X > x {
				line.WriteString(strings.Repeat(" ", p.box.X-x))
			}
			active := p.section == m.immersive.section
			border := dimStyle
			text := playlistItemStyle
			if active {
				border = playlistSelectedStyle
				text = playlistSelectedStyle
			}
			// The search pill stays highlighted even when it is not the
			// active section, and shows the input caret while typing.
			if p.section == immSecSearch && !active {
				border = playlistActiveStyle
				text = labelStyle
			}
			if m.immersive.focus == immPaneNav && p.section == m.immersive.section {
				border = helpKeyStyle
			}
			if p.section < 0 && !m.immHistoryAvailable(p.section) {
				text = dimStyle // nothing to go back/forward to
			}
			label := m.immPillLabel(p.section)
			pw := p.box.W
			switch r {
			case 0:
				line.WriteString(border.Render("╭" + strings.Repeat("─", pw-2) + "╮"))
			case 1:
				line.WriteString(border.Render("│") + text.Render(fitCell(" "+label, pw-2)) + border.Render("│"))
			case 2:
				line.WriteString(border.Render("╰" + strings.Repeat("─", pw-2) + "╯"))
			}
			x = p.box.X + pw
		}
		rows[r] = fitCell(line.String(), w)
	}
	return rows
}

// — body —

func (m Model) renderImmBody(g immGeom) []string {
	left := padPane(m.renderImmLeft(g), g.leftW, g.leftH)
	right := padPane(m.renderImmCanvas(g), g.canvasW, g.bodyH)
	right = append(right, strings.Repeat(" ", g.canvasW))
	for _, row := range m.renderImmControls(g) {
		right = append(right, fitCell(ansi.Cut(row, g.canvasX, g.w), g.canvasW))
	}
	// padPane fixed both columns' widths, so rows join without measuring.
	out := make([]string, g.leftH)
	for i := range out {
		out[i] = left[i] + " " + right[i]
	}
	return out
}

// — left column: Now Playing + Queue —

func (m Model) renderImmLeft(g immGeom) []string {
	np := m.renderImmNowPlaying(g)
	q := m.renderImmQueue(g)
	return append(np, q...)
}

// boxTop/boxBottom/boxSide draw a rounded panel border line with a centered-ish
// label inset after the corner.
func boxTop(label string, w int, focused bool) string {
	border := dimStyle
	text := labelStyle
	if focused {
		border = playlistSelectedStyle
	}
	if label != "" {
		return border.Render("╭─ ") + text.Render(label) + border.Render(" "+strings.Repeat("─", max(0, w-lipgloss.Width(label)-5))+"╮")
	}
	return border.Render("╭" + strings.Repeat("─", w-2) + "╮")
}

func boxBottom(w int, focused bool) string {
	border := dimStyle
	if focused {
		border = playlistSelectedStyle
	}
	return border.Render("╰" + strings.Repeat("─", w-2) + "╯")
}

func boxSide(focused bool) string {
	border := dimStyle
	if focused {
		border = playlistSelectedStyle
	}
	return border.Render("│")
}

// renderImmNowPlaying draws the Now Playing panel: a square art box then
// title / artist / album lines.
func (m Model) renderImmNowPlaying(g immGeom) []string {
	w := g.leftW
	lines := []string{boxTop("Now Playing", w, false)}
	inner := w - 2

	track, _ := m.currentPlaybackTrack()
	name := track.Title
	if name == "" {
		name = trackViewName(track)
	}
	art := m.immArtOr(track.AlbumArtURL, firstNonEmpty(name, "cliamp"), g.npArt.W, g.npArt.H, name != "")
	side := boxSide(false)
	indent := strings.Repeat(" ", g.npArt.X-1)
	for i := 0; i < g.npArt.H; i++ {
		row := ""
		if i < len(art) {
			row = art[i]
		}
		lines = append(lines, side+fitCell(indent+row, inner)+side)
	}

	liked := ""
	if m.favSet != nil {
		if _, ok := m.favSet[track.Path]; ok {
			liked = " " + favMarkerStyle.Render(favHeart)
		}
	}
	if name == "" {
		name = "Nothing playing"
	}
	text := []string{
		playlistActiveStyle.Render("♪ "+ansi.Truncate(name, max(1, inner-4), "…")) + liked,
		dimStyle.Render(ansi.Truncate(track.Artist, max(1, inner-2), "…")),
		dimStyle.Render(ansi.Truncate(track.Album, max(1, inner-2), "…")),
	}
	for _, t := range text {
		lines = append(lines, side+" "+fitCell(t, inner-1)+side)
	}
	for len(lines) < g.npH-1 {
		lines = append(lines, side+strings.Repeat(" ", inner)+side)
	}
	lines = append(lines, boxBottom(w, false))
	return lines
}

// immQueueMax bounds the Queue panel's lookahead into the playing list.
const immQueueMax = 100

// immQueueRows is what the Queue panel lists: play-next entries first, then
// the playing list's upcoming tracks in play order.
func (m Model) immQueueRows() []playlist.QueueEntry {
	rows := m.playlist.QueueEntries()
	if n := immQueueMax - len(rows); n > 0 {
		rows = append(rows, m.playlist.Upcoming(n)...)
	}
	return rows
}

// renderImmQueue draws the Queue panel: "Next from" header then numbered
// upcoming tracks.
func (m Model) renderImmQueue(g immGeom) []string {
	w := g.leftW
	focused := m.immersive.focus == immPaneQueue
	lines := []string{boxTop("Queue", w, focused)}
	inner := w - 2
	side := boxSide(focused)
	rows := g.queueH

	header := "Up next"
	if ctx := m.playingContextName(); ctx != "" {
		header = "Next from " + ctx
	}
	innerRows := rows - 2
	if innerRows > 0 {
		lines = append(lines, side+" "+fitCell(dimStyle.Render(header), inner-1)+side)
	}
	entries := m.immQueueRows()
	total := len(entries)
	if total == 0 {
		if innerRows > 1 {
			lines = append(lines, side+" "+fitCell(dimStyle.Render("(empty)"), inner-1)+side)
		}
	} else {
		budget := max(1, innerRows-1)
		scroll := clampedScroll(m.immersive.queueScroll, m.immersive.queueCursor, total, budget)
		for i := scroll; i < total && i < scroll+budget && len(lines) < rows-1; i++ {
			t := entries[i].Track
			name := t.Title
			if name == "" {
				name = trackViewName(t)
			}
			if t.Artist != "" && t.Title != "" {
				name += " · " + t.Artist
			}
			num := fmt.Sprintf("%2d ", i+1)
			style := playlistItemStyle
			numStyle := dimStyle
			if i < m.playlist.QueueLen() {
				numStyle = playlistActiveStyle // queued: plays before the list order
			}
			if i == m.immersive.queueCursor && focused {
				style = playlistSelectedStyle
				numStyle = playlistSelectedStyle
			}
			row := numStyle.Render(num) + style.Render(ansi.Truncate(name, max(1, inner-4), "…"))
			lines = append(lines, side+" "+fitCell(row, inner-1)+side)
		}
	}
	for len(lines) < rows-1 {
		lines = append(lines, side+strings.Repeat(" ", inner)+side)
	}
	lines = append(lines, boxBottom(w, focused))
	return lines
}

// — canvas —

// immCanvasTitle labels the canvas border: the section name while browsing,
// the opened collection's name inside a detail view.
func (m Model) immCanvasTitle() string {
	im := m.immersive
	var title string
	switch im.view {
	case immViewBrowse:
		title = immSectionLabels[im.section]
		if im.sort != immBrowseSortRecents {
			title += " · " + immBrowseSortLabels[im.sort]
		}
		if im.filtering || im.filter != "" {
			title += " · filter: " + im.filter
			if im.filtering {
				title += "▏"
			}
		}
	case immViewSettings:
		return "Settings"
	case immViewSearch:
		title = "Search"
		if im.ctxName != "" {
			title = im.ctxName
		}
	default:
		kind := "Playlist"
		switch im.view {
		case immViewAlbum:
			kind = "Album"
		case immViewArtist:
			kind = "Artist"
		case immViewShow:
			kind = "Podcast"
		}
		title = kind + ": " + firstNonEmpty(im.ctxName, "…")
		if im.trackSort != immSortTrackOrder {
			title += " · " + immSortTrackLabels[im.trackSort]
		}
	}
	return title + " · " + immCanvasModeNames[im.mode]
}

func (m Model) renderImmCanvas(g immGeom) []string {
	w := g.canvasW
	focused := m.immersive.focus == immPaneCanvas
	lines := []string{boxTop(m.immCanvasTitle(), w, focused)}

	inner := m.renderImmCanvasInner(g.canvasIW, g.canvasIH)
	side := boxSide(focused)
	for i := 0; i < g.canvasIH; i++ {
		row := ""
		if i < len(inner) {
			row = inner[i]
		}
		lines = append(lines, side+fitCell(row, g.canvasIW)+side)
	}
	lines = append(lines, boxBottom(w, focused))
	return lines
}

// renderImmCanvasInner draws the canvas contents: settings tab, loading or
// empty states, or the item list in the active canvas mode.
func (m Model) renderImmCanvasInner(w, rows int) []string {
	im := m.immersive
	if im.view == immViewSettings {
		return m.renderImmSettings(w, rows)
	}
	lines := make([]string, rows)
	if im.needsAuth && im.view == immViewBrowse {
		name := "your account"
		if im.prov != nil {
			name = im.prov.Name()
		}
		lines[0] = playlistActiveStyle.Render("  Sign in to " + name)
		if rows > 1 {
			lines[1] = dimStyle.Render("  Enter or click here opens the sign-in page in your browser.")
		}
		if m.provAuthURL != "" && rows > 3 {
			lines[3] = dimStyle.Render("  If it did not open: " + m.provAuthURL)
		}
		return lines
	}
	if im.tracksLoading && im.view.isTrackView() {
		lines[0] = "  " + m.immSpin() + " loading " + firstNonEmpty(im.ctxName, "tracks") + "…"
		return lines
	}
	if im.section == immSecPlaylists && im.loadingLists && im.view == immViewBrowse {
		lines[0] = "  " + m.immSpin() + " loading playlists…"
		return lines
	}
	if im.section == immSecAlbums && im.loadingAlbums && im.view == immViewBrowse {
		lines[0] = "  " + m.immSpin() + " loading albums…"
		return lines
	}
	if im.section == immSecArtists && im.loadingArtists && im.view == immViewBrowse {
		lines[0] = "  " + m.immSpin() + " loading artists…"
		return lines
	}
	if im.searchLoading && im.view == immViewSearch {
		lines[0] = "  " + m.immSpin() + " searching…"
		return lines
	}
	items := m.canvasItems()
	if len(items) == 0 {
		switch {
		case im.view == immViewSearch:
			lines[0] = dimStyle.Render("  " + m.immGlyphs().search + " what do you want to play?")
			if rows > 1 {
				lines[1] = dimStyle.Render("  / or enter on the Search pill to search")
			}
		case im.view == immViewBrowse:
			lines[0] = dimStyle.Render("  (empty)")
		default:
			lines[0] = dimStyle.Render("  (empty)")
		}
		return lines
	}

	// Item geometry is in content coordinates; translate to canvas-inner rows.
	frame := m.immGeom()
	ox, oy := frame.canvasX+1, frame.bodyY+1
	playingPath := ""
	if playing, _ := m.currentPlaybackTrack(); playing.Path != "" {
		playingPath = playing.Path
	}
	if im.mode == immCanvasGrid {
		// Tiles in one band are disjoint columns: concat their lines in x
		// order per band row rather than overwriting.
		bands := map[int][]immItemGeom{}
		var bandYs []int
		for _, g := range m.immCanvasItemsGeom(w, rows) {
			if len(bands[g.box.Y]) == 0 {
				bandYs = append(bandYs, g.box.Y)
			}
			bands[g.box.Y] = append(bands[g.box.Y], g)
		}
		for _, bandY := range bandYs {
			y := bandY - oy
			band := bands[bandY]
			tiles := make([][]string, len(band)) // render each tile once
			for i, g := range band {
				sel := g.idx == im.cursor && im.focus == immPaneCanvas
				tiles[i] = m.immItemTileLines(items[g.idx], g.box.W, g.box.H, sel)
			}
			for li := 0; li < band[0].box.H && y+li < rows; li++ {
				var b strings.Builder
				cx := 0
				for i, g := range band {
					tl := tiles[i]
					if li >= len(tl) {
						continue
					}
					x := g.box.X - ox
					if x > cx {
						b.WriteString(strings.Repeat(" ", x-cx))
					}
					b.WriteString(fitCell(tl[li], g.box.W))
					cx = x + g.box.W
				}
				if y+li >= 0 {
					lines[y+li] = b.String()
				}
			}
		}
		return lines
	}
	for _, g := range m.immCanvasItemsGeom(w, rows) {
		item := items[g.idx]
		sel := g.idx == im.cursor && im.focus == immPaneCanvas
		y := g.box.Y - oy
		switch im.mode {
		case immCanvasList:
			if y >= 0 && y < rows {
				lines[y] = m.immItemListLine(item, g.idx, w, sel, playingPath)
			}
		case immCanvasRows:
			for i, rl := range m.immItemRowLines(item, w, sel, playingPath) {
				if y+i >= 0 && y+i < rows {
					lines[y+i] = rl
				}
			}
		}
	}
	return lines
}

// immItemListLine draws a list-mode row: number, title, and right-aligned
// details.
func (m Model) immItemListLine(item immItem, idx, w int, sel bool, playingPath string) string {
	num := fmt.Sprintf("%3d ", idx+1)
	right := item.sub
	if item.sub2 != "" {
		if right != "" {
			right += " · "
		}
		right += item.sub2
	}
	if item.dur > 0 {
		right = firstNonEmpty(right, "") + " " + formatTrackTime(item.dur)
	}
	style := playlistItemStyle
	numStyle := dimStyle
	if sel {
		style, numStyle = playlistSelectedStyle, playlistSelectedStyle
	}
	if item.path != "" && item.path == playingPath {
		num = "  ♪ "
		if !sel {
			style = playlistActiveStyle
		}
	}
	titleW := max(1, w-lipgloss.Width(num)-lipgloss.Width(right)-2)
	return numStyle.Render(num) + style.Render(ansi.Truncate(item.title, titleW, "…")) +
		strings.Repeat(" ", max(0, w-lipgloss.Width(num)-titleW-lipgloss.Width(right))) +
		dimStyle.Render(right)
}

// immItemRowLines draws a rows-mode item: a 3-row art box on the left, then
// title / artist / album (+duration) text filling the rest of the row.
func (m Model) immItemRowLines(item immItem, w int, sel bool, playingPath string) []string {
	art := m.immArtOr(item.art, item.title, immRowsArtW, immRowsItemH, sel)
	textW := max(1, w-immRowsArtW-2)
	titleStyle := playlistItemStyle
	subStyle := dimStyle
	if sel {
		titleStyle = playlistSelectedStyle
		subStyle = playlistSelectedStyle
	}
	dur := ""
	if item.dur > 0 {
		dur = formatTrackTime(item.dur)
	}
	title := item.title
	if item.path != "" && item.path == playingPath {
		title = "♪ " + title
	}
	titleCell := titleStyle.Render(ansi.Truncate(title, max(1, textW-lipgloss.Width(dur)-1), "…"))
	titleCell += strings.Repeat(" ", max(0, textW-lipgloss.Width(titleCell)-lipgloss.Width(dur))) + dimStyle.Render(dur)
	lines := []string{
		art[0] + "  " + fitCell(titleCell, textW),
		art[1] + "  " + fitCell(subStyle.Render(ansi.Truncate(item.sub, textW, "…")), textW),
		art[2] + "  " + fitCell(subStyle.Render(ansi.Truncate(item.sub2, textW, "…")), textW),
	}
	return lines
}

// immItemTileLines draws a grid tile: a square art block then title and sub
// lines, clipped to the tile box.
func (m Model) immItemTileLines(item immItem, w, h int, sel bool) []string {
	artH := h - 2
	if artH < 1 {
		artH = h
	}
	art := m.immArtOr(item.art, item.title, w, artH, sel)
	lines := make([]string, 0, h)
	for _, r := range art {
		lines = append(lines, fitCell(r, w))
	}
	titleStyle := playlistItemStyle
	if sel {
		titleStyle = playlistSelectedStyle
	}
	if len(lines) < h {
		lines = append(lines, titleStyle.Render(ansi.Truncate(item.title, max(1, w), "…")))
	}
	if len(lines) < h {
		lines = append(lines, dimStyle.Render(ansi.Truncate(item.sub, max(1, w), "…")))
	}
	return lines[:h]
}

// — canvas item geometry (shared with the mouse hit test) —

// immItemGeom is one drawn canvas item's box in content coordinates, plus
// the art rect inside it for step-2 image placement.
type immItemGeom struct {
	idx int
	box immRect
	art immRect // zero rect when the mode draws no art box
}

// immCanvasItemsGeom places the currently visible items inside the canvas
// content area, in content coordinates.
func (m Model) immCanvasItemsGeom(iw, ih int) []immItemGeom {
	im := m.immersive
	items := m.canvasItems()
	if len(items) == 0 || im.view == immViewSettings {
		return nil
	}
	g := m.immGeom()
	ox, oy := g.canvasX+1, g.bodyY+1
	switch im.mode {
	case immCanvasRows:
		per := ih / immRowsItemH
		if per < 1 {
			per = 1
		}
		scroll := clampedScroll(im.scroll, im.cursor, len(items), per)
		out := make([]immItemGeom, 0, per)
		for i := 0; i < per && scroll+i < len(items); i++ {
			y := oy + i*immRowsItemH
			out = append(out, immItemGeom{
				idx: scroll + i,
				box: immRect{X: ox, Y: y, W: iw, H: min(immRowsItemH, ih-i*immRowsItemH)},
				art: immRect{X: ox, Y: y, W: immRowsArtW, H: min(immRowsItemH, ih-i*immRowsItemH)},
			})
		}
		return out
	case immCanvasGrid:
		cols, tileW, tileH := m.immGridTile(iw)
		tileRows := (len(items) + cols - 1) / cols
		visible := ih / (tileH + 1)
		if visible < 1 {
			visible = 1
		}
		curRow := im.cursor / cols
		scrollRow := clampedScroll(im.scroll, curRow, tileRows, visible)
		var out []immItemGeom
		for r := scrollRow; r < tileRows && (r-scrollRow) < visible; r++ {
			for c := 0; c < cols; c++ {
				idx := r*cols + c
				if idx >= len(items) {
					break
				}
				x := ox + c*(tileW+1)
				y := oy + (r-scrollRow)*(tileH+1)
				box := immRect{X: x, Y: y, W: min(tileW, ox+iw-x), H: min(tileH, oy+ih-y)}
				out = append(out, immItemGeom{
					idx: idx,
					box: box,
					art: immRect{X: x, Y: y, W: box.W, H: max(0, box.H-2)},
				})
			}
		}
		return out
	default: // immCanvasList
		scroll := clampedScroll(im.scroll, im.cursor, len(items), ih)
		out := make([]immItemGeom, 0, ih)
		for i := 0; i < ih && scroll+i < len(items); i++ {
			out = append(out, immItemGeom{
				idx: scroll + i,
				box: immRect{X: ox, Y: oy + i, W: iw, H: 1},
			})
		}
		return out
	}
}

// immGridTile sizes grid tiles for a canvas inner width. Even widths keep
// the art (tileW x tileW/2 cells) square in pixels; tileH adds the title
// and subtitle rows.
func (m Model) immGridTile(iw int) (cols, tileW, tileH int) {
	cols = m.immGridCols(iw)
	tileW = max(4, ((iw-(cols-1))/cols)&^1)
	return cols, tileW, tileW/2 + 2
}

// immGridCols returns the tile column count for a canvas inner width.
func (m Model) immGridCols(iw int) int {
	cols := (iw + 1) / (immGridTileW + 1)
	return clampInt(cols, 1, 8)
}

// — settings canvas tab —

// immSettingsRows enumerates the settings tab rows: EQ preset, the ten
// bands, volume, speed, visualizer mode.
const (
	immSetPreset = iota
	immSetBand0  // bands occupy immSetBand0..immSetBand0+eqBandCount-1
)
const immSetVol = immSetBand0 + eqBandCount
const (
	immSetSpeed = immSetVol + 1
	immSetVis   = immSetSpeed + 1
	immSetCount = immSetVis + 1
)

var immEQBandLabels = [eqBandCount]string{"70Hz", "180Hz", "320Hz", "600Hz", "1kHz", "3kHz", "6kHz", "12kHz", "14kHz", "16kHz"}

// immSettingsStart is the first settings row drawn in a canvas rows tall:
// the list scrolls just enough to keep the cursor row visible.
func immSettingsStart(cursor, rows int) int {
	return clampInt(cursor-rows+1, 0, max(0, immSetCount-rows))
}

func (m Model) renderImmSettings(w, rows int) []string {
	lines := make([]string, 0, rows)
	var bands [eqBandCount]float64
	if m.player != nil {
		bands = m.player.EQBands()
	}
	vol := 0.0
	volMin := -60.0
	if m.player != nil {
		vol = m.player.Volume()
		volMin = m.player.VolumeMin()
	}
	volFrac := clampInt(int(immVolBarCells*(vol-volMin)/(6-volMin)), 0, immVolBarCells)

	visName := "off"
	if m.vis != nil {
		visName = m.vis.ModeName()
	}
	vals := make([]string, immSetCount)
	names := make([]string, immSetCount)
	names[immSetPreset] = "EQ preset"
	vals[immSetPreset] = m.EQPresetName()
	for i := 0; i < eqBandCount; i++ {
		names[immSetBand0+i] = immEQBandLabels[i]
		gain := bands[i]
		fill := int(gain)
		if fill < 0 {
			fill = -fill
		}
		fill = fill * 8 / 12
		vals[immSetBand0+i] = strings.Repeat("▓", fill) + strings.Repeat("░", 8-fill) + fmt.Sprintf(" %+0.0f dB", gain)
	}
	names[immSetVol] = "Volume"
	vals[immSetVol] = strings.Repeat("▮", volFrac) + strings.Repeat("▯", immVolBarCells-volFrac) + fmt.Sprintf(" %+0.0f dB", vol)
	names[immSetSpeed] = "Speed"
	speed := 1.0
	if m.player != nil {
		speed = m.player.Speed()
	}
	vals[immSetSpeed] = fmt.Sprintf("%0.2fx", speed)
	names[immSetVis] = "Visualizer"
	vals[immSetVis] = visName

	for i := immSettingsStart(m.immersive.settingsCursor, rows); i < immSetCount && len(lines) < rows; i++ {
		style := playlistItemStyle
		if i == m.immersive.settingsCursor && m.immersive.focus == immPaneCanvas {
			style = playlistSelectedStyle
		}
		name := fmt.Sprintf("%-12s", names[i])
		lines = append(lines, style.Render("  "+name+ansi.Truncate(vals[i], max(1, w-16), "")))
	}
	return lines
}

// — controls row —

// immGlyphSet selects between plain Unicode and Nerd Font transport glyphs.
type immGlyphSet struct {
	shuffle   string
	prev      string
	play      string
	pause     string
	next      string
	repeat    string
	repeatOne string
	vol       string
	search    string
}

var immGlyphsUnicode = immGlyphSet{
	shuffle: "⇄", prev: "▕◀", play: "▶", pause: "❚❚", next: "▶▏",
	repeat: "↻", repeatOne: "↺", vol: "♪", search: "⌕",
}

var immGlyphsNerd = immGlyphSet{
	shuffle: "\uf074", prev: "\uf048", play: "\uf04b", pause: "\uf04c", next: "\uf051",
	repeat: "\U000f0456", repeatOne: "\U000f0458", vol: "\uf028", search: "\uf002",
}

func (m Model) immGlyphs() immGlyphSet {
	if m.nerdFontGlyphs {
		return immGlyphsNerd
	}
	return immGlyphsUnicode
}

// immCtrlGeom is one controls-row button's box in content coordinates.
type immCtrlGeom struct {
	key string
	box immRect
}

// immControlsGeom lays out the centered transport buttons and returns the
// volume bar rect beside them.
func (m Model) immControlsGeom(w int) ([]immCtrlGeom, immRect) {
	keys := []string{"z", "<", " ", ">", "r"}
	widths := []int{immCtrlBtnW, immCtrlBtnW, immCtrlPlayW, immCtrlBtnW, immCtrlBtnW}
	total := 0
	for _, bw := range widths {
		total += bw + 1
	}
	g := m.immGeom()
	x := g.canvasX + max(0, (g.canvasW-total)/2) // centered under the canvas
	y := g.ctrlY
	btns := make([]immCtrlGeom, 0, len(keys))
	for i, k := range keys {
		btns = append(btns, immCtrlGeom{key: k, box: immRect{X: x, Y: y, W: widths[i], H: immCtrlRows}})
		x += widths[i] + 1
	}
	// The volume bar sits on the middle controls row just right of the group.
	// x already includes the gap after the last button; the bar follows the
	// glyph and one space (see renderImmControls).
	vol := immRect{X: x + 1 + lipgloss.Width(m.immGlyphs().vol), Y: y + 1, W: immVolBarCells, H: 1}
	return btns, vol
}

// renderImmControls draws the 3-row transport buttons centered under the
// canvas with the volume bar beside them.
func (m Model) renderImmControls(g immGeom) []string {
	rows := []string{strings.Repeat(" ", g.w), strings.Repeat(" ", g.w), strings.Repeat(" ", g.w)}
	btns, _ := m.immControlsGeom(g.w)
	gl := m.immGlyphs()
	glyphs := []string{gl.shuffle, gl.prev, gl.play, gl.next, gl.repeat}
	if m.isPlaying() {
		glyphs[2] = gl.pause
	}
	if m.playlist != nil && m.playlist.Smart() {
		glyphs[0] = gl.shuffle + "✦" // Smart Shuffle: the third shuffle state
	}
	if m.playlist != nil && m.playlist.Repeat() == playlist.RepeatOne {
		glyphs[4] = gl.repeatOne
	}
	active := func(i int) bool {
		if m.playlist == nil {
			return false
		}
		switch i {
		case 0:
			return m.playlist.Shuffled()
		case 4:
			return m.playlist.Repeat() != 0
		}
		return false
	}
	for r := 0; r < immCtrlRows; r++ {
		var line strings.Builder
		x := 0
		for i, b := range btns {
			if b.box.X > x {
				line.WriteString(strings.Repeat(" ", b.box.X-x))
			}
			border := dimStyle
			glyphStyle := playlistItemStyle
			if active(i) {
				border = playlistActiveStyle
			}
			if i == 2 {
				glyphStyle = statusStyle
			}
			bw := b.box.W
			switch r {
			case 0:
				line.WriteString(border.Render("╭" + strings.Repeat("─", bw-2) + "╮"))
			case 1:
				mid := (bw - 2 - lipgloss.Width(glyphs[i])) / 2
				line.WriteString(border.Render("│") +
					glyphStyle.Render(strings.Repeat(" ", mid)+glyphs[i]+strings.Repeat(" ", max(0, bw-2-mid-lipgloss.Width(glyphs[i])))) +
					border.Render("│"))
			case 2:
				line.WriteString(border.Render("╰" + strings.Repeat("─", bw-2) + "╯"))
			}
			x = b.box.X + bw
		}
		if r == 1 {
			volText := " " + gl.vol + " " + m.immVolBar() + fmt.Sprintf(" %+0.0fdB", m.playerVolume())
			line.WriteString(fitCell(volText, max(0, g.w-x)))
		}
		rows[r] = fitCell(line.String(), g.w)
	}
	return rows
}

// playerVolume guards the render path against a nil player in tests.
func (m Model) playerVolume() float64 {
	if m.player == nil {
		return 0
	}
	return m.player.Volume()
}

// immVolBar draws the six-cell volume meter used in the controls row.
func (m Model) immVolBar() string {
	volMin := -60.0
	vol := m.playerVolume()
	if m.player != nil {
		volMin = m.player.VolumeMin()
	}
	frac := 0
	if vol > volMin {
		frac = clampInt(int(immVolBarCells*(vol-volMin)/(6-volMin)), 0, immVolBarCells)
	}
	return volBarStyle.Render(strings.Repeat("▮", frac) + strings.Repeat("▯", immVolBarCells-frac))
}

// — progress bar —

// renderImmProgress draws `elapsed ━━━━╾──── total` as a thin line that
// moves in half-cell steps.
func (m Model) renderImmProgress(w int) string {
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
	barW := max(4, w-lipgloss.Width(posText)-lipgloss.Width(durText)-2)
	halves := 0
	if dur > 0 {
		halves = clampInt(int(float64(pos)/float64(dur)*float64(barW)*2), 0, barW*2)
	}
	full, half := halves/2, halves%2
	var bar strings.Builder
	bar.WriteString(seekFillStyle.Render(strings.Repeat(immBarPlayed, full) + strings.Repeat(immBarHalf, half)))
	bar.WriteString(seekDimStyle.Render(strings.Repeat(immBarTrack, barW-full-half)))
	return dimStyle.Render(posText) + " " + bar.String() + " " + dimStyle.Render(durText)
}

// renderImmStatusLine is the transient message + key-hint row.
func (m Model) renderImmStatusLine(w int) string {
	if line := m.renderTransient(); line != "" {
		return fitCell(line, w)
	}
	hints := "? keys · I exit · 1-5 pills · / search · c view · v visualizer · ; menu · Bksp back · e EQ · f filter · tab focus · V full vis"
	return fitCell(dimStyle.Render(hints), w)
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

// immArtBlock draws a deterministic shade-glyph mosaic stand-in for cover art.
// immArtOr is the art box content: the cover when it is ready (see
// immersive_art.go), otherwise the text placeholder.
func (m Model) immArtOr(url, name string, w, h int, bright bool) []string {
	if lines, ok := m.immArtCells(url, w, h); ok {
		return lines
	}
	return immArtBlock(name, w, h, bright)
}

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
	// Center a note glyph on the bright card so it reads as a cover, not noise.
	if bright && h >= 3 && w >= 4 {
		mid := h / 2
		padL := (w - 3) / 2
		fill := string(immArtChars[int(seed)%4])
		lines[mid] = style.Render(strings.Repeat(fill, padL) + " ♪ " + strings.Repeat(fill, max(0, w-padL-3)))
	}
	return lines
}
