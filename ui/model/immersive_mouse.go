package model

// immersive_mouse.go wires pointer input into immersive mode: click/drag
// seeking on the player-bar progress row, single-click open/play on rail
// rows, track rows, grid cards and the rolodex, transport-button clicks,
// top-bar shortcuts, and queue jumps. Hit-testing reuses the same width and
// scroll math as the renderers so clicks land where the frame drew.
//
// Geometry is recorded each frame into m.immMouse (a shared pointer, like
// m.mouse) because View() renders from a value copy. The seek row mirrors
// into m.mouse.seekRow/seekX/seekW so the existing drag machinery in
// mouse.go (motion + release) works unchanged. Wheel scrolling already
// reaches immersive panes through handleKey routing, so it needs nothing
// here.

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/ui"
)

// Content-row layout of the immersive frame: top bar, spacer, bodyRows pane
// rows, spacer, transport, seek, status.
const (
	immBodyTopRel    = 2  // first pane row, in content rows
	immRailRowsTop   = 6  // first rail row when expanded (title,"",pills,"",filter,"")
	immRailRowsSmall = 2  // first rail row when collapsed ("≡","")
	immTableRowsTop  = 10 // hero 6 + action + blank + header + separator
)

// immMouseGeom is the per-frame hit geometry of the immersive frame: the
// frame origin plus pane widths. Row origins inside each pane are derived
// from these at click time with the same constants the renderers use.
type immMouseGeom struct {
	valid    bool
	frameX   int // screen cell of content column 0
	topRow   int // screen row of content row 0
	bodyTop  int // screen row of the first pane row
	bodyRows int
	railW    int
	centerW  int
	rightW   int
	centerX  int // content column of the center pane
	rightX   int // content column of the right pane
	w        int // content width
}

// immersivePaneWidths splits w into rail/center/right widths with the same
// fallback renderImmersive uses: the right rail drops first on narrow
// terminals so the center never squeezes below 20 cells.
func (m Model) immersivePaneWidths(w int) (railW, centerW, rightW int) {
	railW, rightW = m.immersiveRailWidth(w), m.immersiveRightWidth(w)
	centerW = w - railW - rightW
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
	return railW, centerW, rightW
}

// recordImmersiveMouseGeometry maps the immersive frame to absolute screen
// coordinates with the same centering math recordMouseGeometry uses.
func (m *Model) recordImmersiveMouseGeometry(content string) {
	if m.mouse == nil {
		return
	}
	ms := m.mouse
	ms.seekRow, ms.bodyRow = -1, -1
	if m.immMouse == nil {
		return
	}
	im := m.immMouse
	im.valid = false
	if m.fullVis || m.layout.tooSmall() {
		return
	}
	frame := ui.FrameStyle.Render(content)
	padTop := max(0, (m.height-lipgloss.Height(frame))/2)
	padLeft := max(0, (m.width-lipgloss.Width(frame))/2)
	x := padLeft + ui.PaddingH
	row := padTop + ui.VerticalPadding()

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
	railW, centerW, rightW := m.immersivePaneWidths(w)
	centerX := railW
	if railW > 0 {
		centerX += immPaneSep
	}
	rightX := centerX + centerW
	if rightW > 0 {
		rightX += immPaneSep
	}

	im.valid = true
	im.frameX = x
	im.topRow = row
	im.bodyTop = row + immBodyTopRel
	im.bodyRows = bodyRows
	im.railW, im.centerW, im.rightW = railW, centerW, rightW
	im.centerX, im.rightX = centerX, rightX
	im.w = w

	// The seek bar sits inset between the position and duration labels;
	// record its exact cells so a drag maps 1:1 onto the drawn bar.
	posText := formatTrackTime(int(m.cachedPos.Seconds()))
	durText := formatTrackTime(int(m.cachedDur.Seconds()))
	if durText == "" {
		durText = "--:--"
	}
	if posText == "" {
		posText = "0:00"
	}
	barW := max(4, w-lipgloss.Width(posText)-lipgloss.Width(durText)-4)
	ms.seekRow = row + immBodyTopRel + bodyRows + 2
	ms.seekX = x + lipgloss.Width(posText) + 1
	ms.seekW = barW
}

// handleImmersiveClick dispatches a button-down event inside immersive mode.
// Left clicks act (open/play/seek); right clicks select without acting, and
// queue the focused table row.
func (m *Model) handleImmersiveClick(msg tea.MouseClickMsg) tea.Cmd {
	im := m.immMouse
	if im == nil || !im.valid {
		return nil
	}
	if msg.Button != tea.MouseLeft && msg.Button != tea.MouseRight {
		return nil
	}
	cx, cy := msg.X-im.frameX, msg.Y-im.topRow
	if cx < 0 || cx >= im.w {
		return nil
	}
	totalRows := immTopBarRows + im.bodyRows + immPlayerBarRows
	if cy < 0 || cy >= totalRows {
		return nil
	}
	right := msg.Button == tea.MouseRight
	switch cy {
	case 0:
		return m.immClickTopBar(cx, right)
	case immBodyTopRel + im.bodyRows + 1:
		return m.immClickTransport(cx, right)
	case immBodyTopRel + im.bodyRows + 2:
		if right || msg.Button != tea.MouseLeft {
			return nil
		}
		m.mouse.dragging = true
		return m.seekToBarCell(msg.X - m.mouse.seekX)
	}
	if cy < immBodyTopRel || cy >= immBodyTopRel+im.bodyRows {
		return nil
	}
	switch {
	case im.railW > 0 && cx < im.railW:
		return m.immClickRail(cy-(im.bodyTop-im.topRow), right)
	case cx >= im.centerX && cx < im.centerX+im.centerW:
		return m.immClickCenter(cx-im.centerX, cy-(im.bodyTop-im.topRow), right)
	case im.rightW > 0 && cx >= im.rightX:
		return m.immClickRight(cx-im.rightX, cy-(im.bodyTop-im.topRow), right)
	}
	return nil
}

// immClickTopBar hits the ‹ back, ⌂ home, search prompt, and I exit zones.
func (m *Model) immClickTopBar(cx int, right bool) tea.Cmd {
	im := m.immMouse
	if right {
		return nil
	}
	switch {
	case cx < 3:
		m.immersiveGoBack()
	case cx == 5:
		m.immersive.focus = immPaneCenter
		m.immersiveGoBack()
		m.immersive.back = nil
		m.immersive.view = immViewHome
		m.immersive.zone = zoneRolo
	case cx >= im.w-8:
		m.exitImmersive()
	case cx >= 8:
		if m.immersive.searching || m.immersive.railFiltering {
			return nil
		}
		return m.handleImmersiveKey(tea.KeyPressMsg{Text: "/"})
	}
	return nil
}

// immTransportBtn is one clickable glyph on the player-bar transport row.
type immTransportBtn struct {
	x0, x1 int // content columns, [x0, x1)
	key    string
}

// immTransportButtons lays the transport glyphs out with the same centering
// math renderImmPlayerBar uses.
func (m Model) immTransportButtons() []immTransportBtn {
	im := m.immMouse
	glyphs, keys := m.immTransportGlyphs()
	leftW := lipgloss.Width(m.immPlayerLeft())
	transportW := 0
	widths := make([]int, len(glyphs))
	for i, g := range glyphs {
		widths[i] = lipgloss.Width(g)
		transportW += widths[i]
	}
	transportW += 2 * (len(glyphs) - 1)
	x := leftW + max(1, (im.w-transportW)/2-leftW)
	var out []immTransportBtn
	for i := range glyphs {
		out = append(out, immTransportBtn{x0: x, x1: x + widths[i], key: keys[i]})
		x += widths[i] + 2
	}
	return out
}

// immClickTransport routes a transport-row click through the matching key so
// clicks and keys share one code path.
func (m *Model) immClickTransport(cx int, right bool) tea.Cmd {
	if right {
		return nil
	}
	for _, b := range m.immTransportButtons() {
		if cx >= b.x0 && cx < b.x1 {
			return m.handleImmersiveKey(tea.KeyPressMsg{Text: b.key})
		}
	}
	return nil
}

// immClickRail selects a library row; left click also opens it.
func (m *Model) immClickRail(r int, right bool) tea.Cmd {
	im := m.immMouse
	rows := m.railRows()
	if len(rows) == 0 || m.immersive.loadingLists && m.immersive.railSection == immSectionPlaylists ||
		m.immersive.loadingAlbums && m.immersive.railSection == immSectionAlbums ||
		m.immersive.loadingArtists && m.immersive.railSection == immSectionArtists {
		return nil
	}
	var idx int
	if m.immersive.railCollapsed {
		if r < immRailRowsSmall || r-immRailRowsSmall >= im.bodyRows-immRailRowsSmall {
			return nil
		}
		idx = r - immRailRowsSmall
	} else {
		if r < immRailRowsTop {
			return nil
		}
		budget := max(1, (im.bodyRows-immRailRowsTop)/2)
		scroll := clampedScroll(m.immersive.railScroll, m.immersive.railCursor, len(rows), budget)
		// Count drawn rows exactly like the renderer so clicks on the
		// blank rows under a short list fall through.
		drawn := 0
		for i := scroll; i < len(rows) && immRailRowsTop+2*drawn < im.bodyRows; i++ {
			drawn++
		}
		if (r-immRailRowsTop)/2 >= drawn {
			return nil
		}
		idx = scroll + (r-immRailRowsTop)/2
	}
	if idx < 0 || idx >= len(rows) {
		return nil
	}
	m.immersive.focus = immPaneRail
	m.immersive.railCursor = idx
	m.immersive.railScroll = clampedScroll(m.immersive.railScroll, idx, len(rows), m.immersiveRailBudget())
	if right {
		return nil
	}
	return m.immersiveActivate()
}

// immClickCenter routes a center-pane click to the rolodex, grid, or table.
func (m *Model) immClickCenter(cx, r int, right bool) tea.Cmd {
	if m.immersive.roloMode {
		return m.immClickRoloBig(cx, r, right)
	}
	switch m.immersive.view {
	case immViewHome:
		return m.immClickHome(cx, r, right)
	default:
		return m.immClickTable(r, right)
	}
}

// immClickTable plays the clicked track row, or queues it on right click.
func (m *Model) immClickTable(r int, right bool) tea.Cmd {
	im := m.immMouse
	if m.immersive.tracksLoading || m.immersive.artistLoading {
		return nil
	}
	tracks := m.sortedTracks()
	if len(tracks) == 0 || r < immTableRowsTop {
		return nil
	}
	budget := max(1, im.bodyRows-immTableRowsTop)
	scroll := clampedScroll(m.immersive.trackScroll, m.immersive.trackCursor, len(tracks), budget)
	idx := scroll + (r - immTableRowsTop)
	if idx < 0 || idx >= len(tracks) || r-immTableRowsTop >= budget {
		return nil
	}
	m.immersive.focus = immPaneCenter
	m.immersive.zone = zoneTable
	m.immersive.trackCursor = idx
	m.immersive.trackScroll = clampedScroll(m.immersive.trackScroll, idx, len(tracks), m.immersiveTableBudget())
	if right {
		return m.immersiveQueueAppend()
	}
	return m.playImmersiveTrack(idx)
}

// immClickHome hits the rolodex strip, then the card grid below it.
func (m *Model) immClickHome(cx, r int, right bool) tea.Cmd {
	im := m.immMouse
	items := m.roloItems()
	if len(items) == 0 {
		return nil
	}
	stripRows := len(m.renderImmRolodexStrip(im.centerW))
	if r < stripRows {
		return m.immClickStrip(cx, r, items, right)
	}
	gridTop := stripRows + 3 // strip + blank + "Jump back in" + blank
	if r < gridTop {
		return nil
	}
	cols := m.immersiveGridCols()
	cardW := max(10, (im.centerW-(cols-1)*immCardGap)/cols)
	rowH := immCardArtRows + 3
	gr := r - gridTop
	if gr%rowH == rowH-1 {
		return nil // blank separator between card rows
	}
	if cx%(cardW+immCardGap) == cardW {
		return nil // gap between cards
	}
	rowsAvail := im.bodyRows - gridTop
	visibleRows := max(1, rowsAvail/rowH)
	gridCursor := clampInt(m.immersive.gridCursor, 0, len(items)-1)
	curRow := gridCursor / cols
	firstRow := 0
	if curRow >= visibleRows {
		firstRow = curRow - visibleRows + 1
	}
	idx := (firstRow+gr/rowH)*cols + cx/(cardW+immCardGap)
	if idx < 0 || idx >= len(items) {
		return nil
	}
	m.immersive.focus = immPaneCenter
	m.immersive.zone = zoneGrid
	m.immersive.gridCursor = idx
	if right {
		return nil
	}
	return m.activateRoloItem(items[idx])
}

// immClickStrip spins a neighbor card to the center, or opens the focused
// card when it is clicked.
func (m *Model) immClickStrip(cx, r int, items []roloItem, right bool) tea.Cmd {
	im := m.immMouse
	w := im.centerW
	focusW := clampInt(w/4, 14, 22)
	neighW := clampInt(w/9, 8, 14)
	widths := []int{neighW, neighW, focusW, neighW, neighW}
	totalW := 0
	for _, c := range widths {
		totalW += c + 1
	}
	pad := max(0, (w-totalW)/2)
	x := pad
	cur := wrapIndex(m.immersive.roloCursor, len(items))
	for i, cw := range widths {
		if cx >= x && cx < x+cw {
			off := i - 2
			m.immersive.focus = immPaneCenter
			m.immersive.zone = zoneRolo
			if off == 0 {
				if right {
					return nil
				}
				return m.activateRoloItem(items[cur])
			}
			m.immersive.roloCursor = wrapIndex(cur+off, len(items))
			return nil
		}
		x += cw + 1
	}
	return nil
}

// immRoloBigCol is one wheel column's hit span plus its deck offset.
type immRoloBigCol struct {
	x, w int
	off  int
}

// immRoloBigLayout shares the wheel-column math between the renderer and the
// click hit test so a click always lands on the drawn column.
func (m Model) immRoloBigLayout(w, rows int) (cols []immRoloBigCol, pad, topPad int) {
	items := m.roloItems()
	if len(items) == 0 {
		return nil, 0, 0
	}
	focusW := clampInt(w/3, 16, 30)
	artRows := clampInt(rows/3, immRoloArtRows, 9)
	type cw struct {
		w   int
		off int
	}
	var widths []cw
	for off := -3; off <= 3; off++ {
		depth := off
		if depth < 0 {
			depth = -depth
		}
		c := focusW - 4*depth
		if c < 6 {
			continue
		}
		widths = append(widths, cw{w: c, off: off})
	}
	totalW := 0
	for _, c := range widths {
		totalW += c.w + 1
	}
	for len(widths) > 1 && totalW > w {
		totalW -= widths[0].w + 1 + widths[len(widths)-1].w + 1
		widths = widths[1 : len(widths)-1]
	}
	pad = max(0, (w-totalW)/2)
	topPad = max(0, (rows-artRows-4)/2)
	x := pad
	for _, c := range widths {
		cols = append(cols, immRoloBigCol{x: x, w: c.w, off: c.off})
		x += c.w + 1
	}
	return cols, pad, topPad
}

// immClickRoloBig spins a wheel column to the center, or activates the
// focused card when it is clicked.
func (m *Model) immClickRoloBig(cx, r int, right bool) tea.Cmd {
	im := m.immMouse
	items := m.roloItems()
	if len(items) == 0 {
		return nil
	}
	cols, _, topPad := m.immRoloBigLayout(im.centerW, im.bodyRows)
	artRows := clampInt(im.bodyRows/3, immRoloArtRows, 9)
	if r < topPad || r >= topPad+artRows+3 {
		return nil
	}
	cur := wrapIndex(m.immersive.roloCursor, len(items))
	for _, c := range cols {
		if cx >= c.x && cx < c.x+c.w {
			m.immersive.focus = immPaneCenter
			if c.off == 0 {
				if right {
					return nil
				}
				return m.activateRoloItem(items[cur])
			}
			m.immersive.roloCursor = wrapIndex(cur+c.off, len(items))
			m.immersive.roloSpin = 0
			return nil
		}
	}
	return nil
}

// immClickRight switches tabs on the tab row, or jumps the queue on a
// queue-row click.
func (m *Model) immClickRight(cx, r int, right bool) tea.Cmd {
	im := m.immMouse
	if r == 0 {
		if right {
			return nil
		}
		if cx < len(" Now playing ") {
			if m.immersive.rightTab != immTabNowPlaying {
				m.immersive.rightTab = immTabNowPlaying
				return m.maybeFetchNowPlayingArtist()
			}
			return nil
		}
		if cx < len(" Now playing ")+len(" Queue ") {
			m.immersive.rightTab = immTabQueue
		}
		return nil
	}
	if m.immersive.rightTab != immTabQueue {
		return nil
	}
	track, _ := m.currentPlaybackTrack()
	np := 0
	if trackViewName(track) != "" {
		np = 2
		if track.Artist != "" {
			np++
		}
		np++ // blank after the now-playing block
	}
	rowsTop := 2 + np + 1 // tabs + blank + now-playing + "Next from:"
	if r < rowsTop {
		return nil
	}
	total := m.playlist.QueueLen()
	if total == 0 {
		return nil
	}
	budget := max(1, im.bodyRows-2-(np+1))
	scroll := clampedScroll(m.immersive.rightScroll, m.immersive.rightCursor, total, budget)
	idx := scroll + (r - rowsTop)
	if idx < 0 || idx >= total || r-rowsTop >= budget {
		return nil
	}
	m.immersive.focus = immPaneRight
	m.immersive.rightCursor = idx
	m.immersive.rightScroll = clampedScroll(m.immersive.rightScroll, idx, total, m.immersiveRightBudget())
	if right {
		return nil
	}
	return m.immersiveQueueJump()
}
