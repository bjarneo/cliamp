package model

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// hitKind identifies what action a clickable region maps to.
type hitKind int

const (
	hitNone        hitKind = iota
	hitPlaylistRow         // a track row in the main playlist
	hitProviderRow         // a playlist row inside the provider browser
	hitSeekBar             // the seek progress bar
	hitVolumeBar           // the volume bar in the controls row
	hitBody                // whole body band, used for wheel scrolling
)

// volumeMaxDB is the top of the volume bar range; the bottom is the engine's
// configurable volume minimum.
const volumeMaxDB = 6.0

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// hitRegion is a clickable rectangle in panel coordinates: x counted from the
// frame's left padding edge, y from the first content row of the frame. y1 and
// x1 are exclusive. idx carries the payload (track index or provider index).
type hitRegion struct {
	kind hitKind
	y0   int
	y1   int
	x0   int
	x1   int
	idx  int
}

// mouseHitState accumulates clickable regions while View renders the current
// frame. It is held behind a pointer so regions survive Update's value
// receiver copies (same pattern as pluginEmitState).
type mouseHitState struct {
	offX, offY int // terminal coords of the panel origin for the last frame

	regions []hitRegion // panel-coord regions for the last frame

	// Scratch filled by section renderers during rendering, converted into
	// regions by mainSections/renderFullVisualizer once each section's line
	// offset within the joined content is known.
	bodyRows []hitRegion // y relative to the body section start
	seekBar  *hitRegion  // y assigned at flush time (always single-line)
	volume   *hitRegion  // y assigned at flush time (always single-line)
}

func (h *mouseHitState) resetFrame() {
	h.regions = h.regions[:0]
	h.bodyRows = h.bodyRows[:0]
	h.seekBar = nil
	h.volume = nil
}

// mouseTrackRow records a body row at body-relative line y carrying idx.
func (m Model) mouseTrackRow(y, idx int, kind hitKind) {
	if m.mouseHits == nil {
		return
	}
	m.mouseHits.bodyRows = append(m.mouseHits.bodyRows, hitRegion{
		kind: kind,
		y0:   y,
		y1:   y + 1,
		x0:   0,
		x1:   ui.PanelWidth,
		idx:  idx,
	})
}

func (m Model) mouseTrackSeekBar(x0, x1 int) {
	if m.mouseHits == nil {
		return
	}
	m.mouseHits.seekBar = &hitRegion{kind: hitSeekBar, x0: x0, x1: x1}
}

func (m Model) mouseTrackVolumeBar(x0, x1 int) {
	if m.mouseHits == nil {
		return
	}
	m.mouseHits.volume = &hitRegion{kind: hitVolumeBar, x0: x0, x1: x1}
}

// mouseFlushBody converts scratch body rows into frame regions using base as
// the absolute first content line of the body section and height as the number
// of lines it occupies. Rows clipped away by FitRect are dropped.
func (m Model) mouseFlushBody(base, height int) {
	h := m.mouseHits
	if h == nil || len(h.bodyRows) == 0 {
		return
	}
	// The coarse body band is registered before individual rows so the
	// reversed scan in mouseRegionAt resolves clicks to the most specific
	// region under the cursor.
	h.regions = append(h.regions, hitRegion{
		kind: hitBody,
		y0:   base,
		y1:   base + height,
		x0:   0,
		x1:   ui.PanelWidth,
	})
	for _, r := range h.bodyRows {
		r.y0 += base
		r.y1 += base
		if r.y0 >= base+height {
			continue
		}
		if r.y1 > base+height {
			r.y1 = base + height
		}
		h.regions = append(h.regions, r)
	}
}

func (m Model) mouseFlushSeekBar(base int) {
	h := m.mouseHits
	if h == nil || h.seekBar == nil {
		return
	}
	r := *h.seekBar
	r.y0 = base
	r.y1 = base + 1
	h.regions = append(h.regions, r)
}

func (m Model) mouseFlushVolume(base int) {
	h := m.mouseHits
	if h == nil || h.volume == nil {
		return
	}
	r := *h.volume
	r.y0 = base
	r.y1 = base + 1
	h.regions = append(h.regions, r)
}

// mousePanelPoint converts terminal coordinates to panel coordinates for the
// last rendered frame.
func (m Model) mousePanelPoint(x, y int) (px, py int, ok bool) {
	if m.mouseHits == nil || m.width <= 0 || m.height <= 0 {
		return 0, 0, false
	}
	px = x - m.mouseHits.offX
	py = y - m.mouseHits.offY
	if px < 0 || py < 0 {
		return 0, 0, false
	}
	return px, py, true
}

// mouseRegionAt returns the most specific region registered under the given
// terminal coordinates.
func (m Model) mouseRegionAt(x, y int) (hitRegion, bool) {
	px, py, ok := m.mousePanelPoint(x, y)
	if !ok {
		return hitRegion{}, false
	}
	for i := len(m.mouseHits.regions) - 1; i >= 0; i-- {
		r := m.mouseHits.regions[i]
		if py >= r.y0 && py < r.y1 && px >= r.x0 && px < r.x1 {
			return r, true
		}
	}
	return hitRegion{}, false
}

// handleMouseClick maps a left click onto the regions recorded while
// rendering the current frame.
func (m *Model) handleMouseClick(msg tea.MouseClickMsg) tea.Cmd {
	if m.mouseHits == nil || m.quitting || m.layout.tooSmall() {
		return nil
	}
	if msg.Button != tea.MouseLeft {
		return nil
	}
	r, ok := m.mouseRegionAt(msg.X, msg.Y)
	if !ok {
		return nil
	}
	switch r.kind {
	case hitPlaylistRow:
		return m.clickPlaylistRow(r.idx)
	case hitProviderRow:
		return m.clickProviderRow(r.idx)
	case hitSeekBar:
		dur := m.cachedDur
		if dur <= 0 || m.buffering {
			return nil
		}
		frac := clamp01(float64(msg.X-m.mouseHits.offX-r.x0) / float64(max(1, r.x1-r.x0)))
		target := time.Duration(frac * float64(dur))
		return m.seekAbsolute(target)
	case hitVolumeBar:
		volMin := m.player.VolumeMin()
		frac := clamp01(float64(msg.X-m.mouseHits.offX-r.x0) / float64(max(1, r.x1-r.x0)))
		m.player.SetVolume(volMin + frac*(volumeMaxDB-volMin))
		m.notifyPlayback()
	case hitBody:
		// Clicks on empty body padding do nothing; wheel uses this band.
	}
	return nil
}

func (m *Model) clickPlaylistRow(idx int) tea.Cmd {
	if m.playlist == nil || idx < 0 || idx >= m.playlist.Len() {
		return nil
	}
	// Clicking an already-focused selected row plays it; otherwise select.
	if m.plCursor == idx && m.focus == focusPlaylist {
		if m.buffering && m.plCursor == m.playlist.Index() {
			return nil
		}
		m.scrobbleCurrent()
		m.playlist.SetIndex(m.plCursor)
		cmd := m.playCurrentTrack()
		m.notifyPlayback()
		return cmd
	}
	m.focus = focusPlaylist
	if m.plCursor != idx {
		m.plCursor = idx
		m.adjustScroll()
	}
	return nil
}

func (m *Model) clickProviderRow(idx int) tea.Cmd {
	if m.provider == nil || idx < 0 || idx >= len(m.providerLists) {
		return nil
	}
	if m.provCursor == idx && m.focus == focusProvider {
		if m.provSignIn {
			if auth, ok := m.provider.(playlist.Authenticator); ok {
				m.provSignIn = false
				m.provLoading = true
				return authenticateProviderCmd(auth, m.provider.Name(), nextRequest(&m.requests.auth))
			}
			return nil
		}
		if !m.provLoading {
			m.provLoading = true
			m.activeProviderPlaylistID = m.providerLists[idx].ID
			return m.fetchProviderTracks(m.providerLists[idx].ID)
		}
		return nil
	}
	m.focus = focusProvider
	if m.provCursor != idx {
		m.provCursor = idx
		m.providerMaybeAdjustScroll()
	}
	return nil
}

// handleMouseWheel scrolls the list occupying the clicked area without
// wrapping at either end.
func (m *Model) handleMouseWheel(msg tea.MouseWheelMsg) tea.Cmd {
	if m.mouseHits == nil || m.quitting || m.layout.tooSmall() {
		return nil
	}
	r, ok := m.mouseRegionAt(msg.X, msg.Y)
	if !ok {
		return nil
	}
	var providerArea, playlistArea bool
	switch r.kind {
	case hitProviderRow:
		providerArea = true
	case hitPlaylistRow:
		playlistArea = true
	case hitBody:
		providerArea = m.focus == focusProvider
		playlistArea = !providerArea
	}
	up := msg.Button == tea.MouseWheelUp
	down := msg.Button == tea.MouseWheelDown
	if !up && !down {
		return nil
	}
	if providerArea {
		return m.wheelProvider(down)
	}
	if playlistArea {
		m.wheelPlaylist(down)
	}
	return nil
}

func (m *Model) wheelProvider(down bool) tea.Cmd {
	if down {
		if m.provCursor < len(m.providerLists)-1 {
			m.provCursor++
			m.providerMaybeAdjustScroll()
		}
		return m.maybeLoadCatalogBatch()
	}
	if m.provCursor > 0 {
		m.provCursor--
		m.providerMaybeAdjustScroll()
	}
	return nil
}

func (m *Model) wheelPlaylist(down bool) {
	if m.playlist == nil {
		return
	}
	if down {
		if m.plCursor < m.playlist.Len()-1 {
			m.plCursor++
			m.adjustScroll()
		}
		return
	}
	if m.plCursor > 0 {
		m.plCursor--
		m.adjustScroll()
	}
}
