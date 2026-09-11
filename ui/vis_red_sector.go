package ui

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// A wireframe equalizer after the vector part of the Red Sector Inc. RSI
// Megademo (Amiga, 1989). Five hollow bars stand on a common ground line, each
// driven by two spectrum bands, and the whole group tumbles as a rigid body
// while a starfield drifts behind it. Hidden edges are removed with per-face
// backface culling, so a bar shows its front, one flank and its cap, the way
// the original vector objects do.
//
// A cell keeps the highest tag drawn into it, which is what puts every bar in
// front of every star: the four star tags sit below the three bar tags. That
// ordering is why this mode rasterises into a grid of its own rather than the
// shared brailleGrid, whose three tiers leave no room below the bars.

const (
	redSectorBars      = 5
	redSectorHalfWidth = 0.26
	redSectorHalfDepth = 0.26
	// Centre-to-centre distance between neighbouring bars.
	redSectorPitch = 0.78
	// The common base line all bars stand on.
	redSectorGroundY   = -1.05
	redSectorMinHeight = 0.40
	redSectorMaxHeight = 2.30
	redSectorFocal     = 3.2
	redSectorCameraZ   = 6.0
	// How far the object drifts towards the viewer and back.
	redSectorZoomAmplitude = 1.5
	// How far the picture may be widened past its own height.
	redSectorMaxStretch = 2.0
	// Dot rows at which no widening is needed any more.
	redSectorComfortDotRows = 40

	// Rotation in radians per frame. The demo tumbles the object around two
	// axes at unrelated rates, so it never repeats a pose for long.
	redSectorSpinY    = 0.105
	redSectorSpinX    = 0.073
	redSectorZoomRate = 0.011

	// Per frame, how fast a band's ceiling and floor close in on each other.
	redSectorEnvelopeRelax = 0.001
	// The narrowest span that still counts as movement.
	redSectorMinEnvelope = 0.06

	// Cell tags, low to high. The four star tags sit below the three bar tags,
	// so a bar always wins the cell it shares with a star.
	redSectorStarTags = 4
	redSectorTagLow   = 5
	redSectorTagMid   = 6
	redSectorTagHigh  = 7
	redSectorTagCount = redSectorTagHigh

	// How much of a spectrum colour a star keeps. Only themes without an ANSI
	// twin are scaled; see redSectorDim.
	redSectorStarDim = 0.6

	// Dot cells per star, and the bounds the count is held inside.
	redSectorDotsPerStar = 130
	redSectorMinStars    = 6
	redSectorMaxStars    = 90
)

// The eight corners of a bar, as signs on its base centre. Height is filled in
// per frame, so only the signs live here.
var (
	redSectorCornerX   = [8]float64{-1, 1, 1, -1, -1, 1, 1, -1}
	redSectorCornerZ   = [8]float64{-1, -1, -1, -1, 1, 1, 1, 1}
	redSectorCornerTop = [8]bool{false, false, true, true, false, false, true, true}
)

// Faces wound so the cross product of the first two edges points inwards. A
// face is then visible when that normal points away from the viewer.
var redSectorFaces = [6][4]int{
	{0, 1, 2, 3}, // front
	{5, 4, 7, 6}, // back
	{0, 3, 7, 4}, // left
	{1, 5, 6, 2}, // right
	{3, 2, 6, 7}, // top
	{0, 4, 5, 1}, // bottom
}

// redSectorDriver keeps the per-band envelope and the eased bar heights across
// frames. Both outlive a single render, which is why this mode carries a
// driver of its own rather than a plain render function.
type redSectorDriver struct {
	grid    redSectorGrid
	ceiling [redSectorBars]float64
	floor   [redSectorBars]float64
	heights [redSectorBars]float64
}

func newRedSectorDriver() visModeDriver {
	d := &redSectorDriver{}
	d.reset()
	return d
}

func (d *redSectorDriver) reset() {
	for i := range d.ceiling {
		// Start wide open, so the first frames pull both ends onto the real range.
		d.ceiling[i] = 0
		d.floor[i] = 1
		d.heights[i] = redSectorMinHeight
	}
	d.grid = redSectorGrid{}
}

func (*redSectorDriver) AnalysisSpec(*Visualizer) VisAnalysisSpec {
	return spectrumAnalysisSpec(DefaultSpectrumBands)
}

func (d *redSectorDriver) Tick(v *Visualizer, ctx VisTickContext) {
	defaultDriverTick(v, ctx, d.AnalysisSpec(v))
	if ctx.OverlayActive {
		return
	}
	d.advance(v.SmoothedBands())
}

func (*redSectorDriver) TickInterval(_ *Visualizer, ctx VisTickContext) time.Duration {
	return defaultDriverTickInterval(ctx)
}

func (d *redSectorDriver) OnEnter(*Visualizer) { d.reset() }

func (*redSectorDriver) OnLeave(*Visualizer) {}

// advance reads each bar's pair of bands and eases its height.
//
// Every band sits at its own resting level and moves only a little around it:
// in a measured stream the bass band hovers near the top while the treble
// bands stay low, and every one of them swings by about a tenth. Read as
// absolute heights that draws the fixed shape of the mix, not the beat. So
// every bar tracks its own ceiling and floor and shows where the level sits
// between them. The ceiling sinks and the floor climbs slowly, which lets the
// pair follow a change of track without flattening a steady passage.
func (d *redSectorDriver) advance(bands []float64) {
	for i := range redSectorBars {
		// Two bands per bar, taking the louder of the pair. Averaging would let
		// a silent band halve its partner, and the top band is empty on most
		// material because the encoder cuts everything above 16 kHz.
		level := max(redSectorBand(bands, i*2), redSectorBand(bands, i*2+1))

		if level > d.ceiling[i] {
			d.ceiling[i] = level
		} else {
			d.ceiling[i] -= redSectorEnvelopeRelax
		}
		if level < d.floor[i] {
			d.floor[i] = level
		} else {
			d.floor[i] += redSectorEnvelopeRelax
		}
		if d.ceiling[i] < d.floor[i]+redSectorMinEnvelope {
			d.ceiling[i] = d.floor[i] + redSectorMinEnvelope
		}

		norm := min(1, max(0, (level-d.floor[i])/(d.ceiling[i]-d.floor[i])))
		target := redSectorMinHeight + norm*(redSectorMaxHeight-redSectorMinHeight)
		// Rising fast and falling slow keeps a transient visible long enough
		// to read as a hit rather than a flicker.
		rate := 0.28
		if target > d.heights[i] {
			rate = 0.75
		}
		d.heights[i] += (target - d.heights[i]) * rate
	}
}

func redSectorBand(bands []float64, i int) float64 {
	if i >= len(bands) {
		return 0
	}
	return min(1, max(0, bands[i]))
}

func (d *redSectorDriver) Render(v *Visualizer) string {
	dotRows, dotCols := v.Rows*4, PanelWidth*2
	if dotRows < 4 || dotCols < 16 {
		return strings.Repeat("\n", max(0, v.Rows-1))
	}
	d.grid.ensure(dotRows, dotCols)

	frame := float64(v.Frame())
	d.drawStars(dotRows, dotCols, frame)
	d.drawBars(dotRows, dotCols, frame)

	return d.grid.render(v.Rows)
}

// Deterministic pseudo-random value in [0, 1) for star index i, one slot per
// coordinate. The index is mixed rather than scaled: a value merely scaled by
// a constant stays a straight line in i, which puts every star on one of a few
// diagonals and leaves neighbouring stars a fixed step apart. The avalanche
// below is splitmix64's finaliser, which carries a one-bit change through the
// whole word and breaks that structure.
func redSectorStarHash(i, slot int) float64 {
	x := uint64(i)*0x9E3779B97F4A7C15 + uint64(slot+1)*0xD1B54A32D192ED03
	x ^= x >> 30
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 27
	x *= 0x94D049BB133111EB
	x ^= x >> 31
	// The top 53 bits are the ones a float64 can hold without rounding.
	return float64(x>>11) / float64(uint64(1)<<53)
}

func (d *redSectorDriver) drawStars(dotRows, dotCols int, frame float64) {
	count := min(redSectorMaxStars, max(redSectorMinStars, dotRows*dotCols/redSectorDotsPerStar))
	for i := 1; i <= count; i++ {
		// Three speed lanes give the field a shallow sense of depth.
		speed := 0.10 + float64(i%3)*0.09
		x := math.Mod(redSectorStarHash(i, 0)*float64(dotCols)-frame*speed, float64(dotCols))
		if x < 0 {
			x += float64(dotCols)
		}
		y := int(redSectorStarHash(i, 1) * float64(dotRows))
		// Colour is drawn once per star index, so a star keeps its hue while it
		// drifts instead of flickering from frame to frame.
		hue := min(redSectorStarTags, 1+int(redSectorStarHash(i, 2)*redSectorStarTags))
		d.grid.set(int(x), y, int8(hue))
	}
}

// redSectorPlace rotates a model point around Y then X and projects it. It
// returns the rotated point plus its projected position in world units, before
// any panel scaling.
func redSectorPlace(x, y, z, cosX, sinX, cosY, sinY, camZ float64) (rx, ry, rz, px, py float64) {
	x1 := x*cosY + z*sinY
	z1 := -x*sinY + z*cosY
	y1 := y*cosX - z1*sinX
	z2 := y*sinX + z1*cosX
	depth := max(0.35, z2+camZ)
	f := redSectorFocal / depth
	return x1, y1, z2, x1 * f, y1 * f
}

func (d *redSectorDriver) drawBars(dotRows, dotCols int, frame float64) {
	cosY, sinY := math.Cos(frame*redSectorSpinY), math.Sin(frame*redSectorSpinY)
	cosX, sinX := math.Cos(frame*redSectorSpinX), math.Sin(frame*redSectorSpinX)
	camZ := redSectorCameraZ + math.Sin(frame*redSectorZoomRate)*redSectorZoomAmplitude

	fitX, fitY, centreX, centreY := redSectorFit(dotRows, dotCols, frame, cosX, sinX, cosY, sinY, camZ)

	var rx, ry, rz, px, py [8]float64
	for bar := range redSectorBars {
		baseX := (float64(bar) - float64(redSectorBars-1)/2) * redSectorPitch
		topY := redSectorGroundY + d.heights[bar]
		tag := redSectorBarTag(d.heights[bar])

		for c := range 8 {
			y := redSectorGroundY
			if redSectorCornerTop[c] {
				y = topY
			}
			x1, y1, z2, sx, sy := redSectorPlace(
				baseX+redSectorCornerX[c]*redSectorHalfWidth, y,
				redSectorCornerZ[c]*redSectorHalfDepth,
				cosX, sinX, cosY, sinY, camZ)
			rx[c], ry[c], rz[c] = x1, y1, z2
			px[c] = centreX + sx*fitX
			py[c] = centreY - sy*fitY
		}

		for _, face := range redSectorFaces {
			i1, i2, i3 := face[0], face[1], face[2]
			visible := redSectorFaceVisible(
				[3]float64{rx[i1], ry[i1], rz[i1]},
				[3]float64{rx[i2], ry[i2], rz[i2]},
				[3]float64{rx[i3], ry[i3], rz[i3]}, camZ)
			if !visible {
				continue
			}
			for e := range 4 {
				from, to := face[e], face[(e+1)%4]
				d.drawLine(dotRows, dotCols, px[from], py[from], px[to], py[to], tag)
			}
		}
	}
}

// redSectorFaceVisible reports whether a face is turned towards the viewer.
// The faces are wound so the cross product of the first two edges points into
// the bar, which makes a face visible exactly when that normal points away
// from the camera.
func redSectorFaceVisible(p1, p2, p3 [3]float64, camZ float64) bool {
	ux, uy, uz := p2[0]-p1[0], p2[1]-p1[1], p2[2]-p1[2]
	vx, vy, vz := p3[0]-p2[0], p3[1]-p2[1], p3[2]-p2[2]
	nx := uy*vz - uz*vy
	ny := uz*vx - ux*vz
	nz := ux*vy - uy*vx
	// The view vector runs from the camera to the first corner of the face.
	return nx*p1[0]+ny*p1[1]+nz*(p1[2]+camZ) > 0
}

// redSectorBarTag reads a bar's colour off its height, using the thresholds of
// cliamp's own spectrum.
func redSectorBarTag(height float64) int8 {
	shown := (height - redSectorMinHeight) / (redSectorMaxHeight - redSectorMinHeight)
	switch {
	case shown >= 0.6:
		return redSectorTagHigh
	case shown >= 0.3:
		return redSectorTagMid
	default:
		return redSectorTagLow
	}
}

// redSectorFit scales the picture against a hull the object can never exceed,
// not against the bars as they stand this frame. Fitting the live shape would
// blow a quiet picture up to full height and leave the bars looking motionless.
func redSectorFit(dotRows, dotCols int, frame, cosX, sinX, cosY, sinY, camZ float64) (fitX, fitY, centreX, centreY float64) {
	hullX := float64(redSectorBars-1)/2*redSectorPitch + redSectorHalfWidth
	minX, maxX := math.Inf(1), math.Inf(-1)
	minY, maxY := math.Inf(1), math.Inf(-1)
	for _, sx := range [2]float64{-1, 1} {
		for _, y := range [2]float64{redSectorGroundY, redSectorGroundY + redSectorMaxHeight} {
			for _, sz := range [2]float64{-1, 1} {
				_, _, _, hx, hy := redSectorPlace(sx*hullX, y, sz*redSectorHalfDepth,
					cosX, sinX, cosY, sinY, camZ)
				minX, maxX = min(minX, hx), max(maxX, hx)
				minY, maxY = min(minY, hy), max(maxY, hy)
			}
		}
	}

	spanX := max(maxX-minX, 0.001)
	spanY := max(maxY-minY, 0.001)

	// Breathe in and out, the way the demo pulls the object towards the viewer
	// and back. The upper bound leaves a margin at full size.
	zoom := 0.55 + 0.30*(0.5+0.5*math.Sin(frame*redSectorZoomRate))
	fitY = float64(dotRows-1) / spanY * zoom

	// A short panel starves the object of height, so the bars are widened to
	// stay apart. Once the panel is tall enough the picture keeps the object's
	// own proportions.
	stretch := min(redSectorMaxStretch, max(1, redSectorComfortDotRows/float64(dotRows)))
	fitX = min(fitY*stretch, float64(dotCols-1)/spanX)

	centreX = float64(dotCols)/2 - (minX+maxX)/2*fitX
	centreY = float64(dotRows)/2 + (minY+maxY)/2*fitY
	return fitX, fitY, centreX, centreY
}

// redSectorMaxLineSteps caps a single edge, so a degenerate projection cannot
// turn one line into an unbounded loop.
const redSectorMaxLineSteps = 512

func (d *redSectorDriver) drawLine(dotRows, dotCols int, x0, y0, x1, y1 float64, tag int8) {
	dx, dy := x1-x0, y1-y0
	span := max(math.Abs(dx), math.Abs(dy))
	if math.IsNaN(span) || math.IsInf(span, 0) {
		return
	}
	steps := min(redSectorMaxLineSteps, int(span)+1)
	for s := 0; s <= steps; s++ {
		t := float64(s) / float64(steps)
		x := int(math.Floor(x0 + dx*t + 0.5))
		y := int(math.Floor(y0 + dy*t + 0.5))
		if x < 0 || x >= dotCols || y < 0 || y >= dotRows {
			continue
		}
		d.grid.set(x, y, tag)
	}
}

// redSectorGrid is a 4x2 dot-per-cell rasteriser with a tag per dot. It is the
// shared brailleGrid with a wider palette: seven tags instead of three, so the
// stars have somewhere to sit below the bars. A cell wears the highest tag any
// of its eight dots carries.
type redSectorGrid struct {
	cells   []int8
	dotRows int
	dotCols int
}

func (g *redSectorGrid) ensure(rows, cols int) {
	if rows == g.dotRows && cols == g.dotCols && len(g.cells) == rows*cols {
		for i := range g.cells {
			g.cells[i] = 0
		}
		return
	}
	g.cells = make([]int8, rows*cols)
	g.dotRows = rows
	g.dotCols = cols
}

func (g *redSectorGrid) set(x, y int, tag int8) {
	if x < 0 || x >= g.dotCols || y < 0 || y >= g.dotRows {
		return
	}
	if tag > g.cells[y*g.dotCols+x] {
		g.cells[y*g.dotCols+x] = tag
	}
}

// render flattens the dot grid to len(rows) lines, packing 4x2 dot blocks into
// Braille glyphs and emitting tag-coloured runs. An empty cell keeps whatever
// colour is running: a blank Braille glyph paints nothing, so breaking the run
// there would only add ANSI noise.
func (g *redSectorGrid) render(rows int) string {
	if g.dotRows < rows*4 || g.dotCols < PanelWidth*2 {
		return strings.Repeat("\n", max(0, rows-1))
	}
	lines := make([]string, rows)
	for row := range rows {
		var sb, run strings.Builder
		tag := 0
		for col := range PanelWidth {
			var braille rune = '⠀'
			cellTag := 0
			for dr := range 4 {
				for dc := range 2 {
					t := g.cells[(row*4+dr)*g.dotCols+col*2+dc]
					if t == 0 {
						continue
					}
					braille |= brailleBit[dr][dc]
					cellTag = max(cellTag, int(t))
				}
			}
			if cellTag != 0 && cellTag != tag {
				flushRedSectorRun(&sb, &run, tag)
				tag = cellTag
			}
			run.WriteRune(braille)
		}
		flushRedSectorRun(&sb, &run, tag)
		lines[row] = sb.String()
	}
	return strings.Join(lines, "\n")
}

func flushRedSectorRun(sb *strings.Builder, run *strings.Builder, tag int) {
	if run.Len() == 0 {
		return
	}
	var prefix, suffix string
	if tag >= 1 && tag <= redSectorTagCount {
		prefix, suffix = redSectorPrefix[tag-1], redSectorSuffix[tag-1]
	}
	if prefix != "" {
		sb.WriteString(prefix)
	}
	// run.String() aliases the builder's backing array (no allocation) and we
	// copy those bytes into sb before run.Reset() releases the slice.
	sb.WriteString(run.String())
	if suffix != "" {
		sb.WriteString(suffix)
	}
	run.Reset()
}

// Raw ANSI wrappers for the seven tags, cached the way the spectrum styles are
// and rebuilt from refreshSpecANSI when the theme changes.
var (
	redSectorPrefix [redSectorTagCount]string
	redSectorSuffix [redSectorTagCount]string
)

func refreshRedSectorANSI() {
	// Four star colours below three bar colours: the field carries the quiet
	// twins of the spectrum plus the dim foreground, the bars the spectrum
	// itself. That is the contrast the Amiga original draws between its
	// starfield and its vector object.
	tags := [redSectorTagCount]color.Color{
		ColorDim,
		redSectorDim(SpectrumLow),
		redSectorDim(SpectrumMid),
		redSectorDim(SpectrumHigh),
		SpectrumLow,
		SpectrumMid,
		SpectrumHigh,
	}
	for i, c := range tags {
		redSectorPrefix[i], redSectorSuffix[i] = splitStyleAroundProbe(
			lipgloss.NewStyle().Foreground(c))
	}
}

// redSectorDim returns the quieter twin of a colour. The sixteen ANSI colours
// come in pairs, 8 to 15 being the bright half of 0 to 7, so a default-theme
// spectrum colour has an exact twin to fall back on. A theme colour is a hex
// value with no such pair and is scaled down instead.
func redSectorDim(c color.Color) color.Color {
	if c == nil {
		return c
	}
	if ansi, ok := c.(lipgloss.ANSIColor); ok {
		if ansi >= 8 && ansi <= 15 {
			return lipgloss.ANSIColor(ansi - 8)
		}
		// Already the plain half of a pair, or a colour from the 256-colour
		// cube whose value carries no brightness the terminal would honour.
		return c
	}
	r, g, b, _ := c.RGBA()
	scale := func(channel uint32) int {
		return int(math.Round(float64(channel>>8) * redSectorStarDim))
	}
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", scale(r), scale(g), scale(b)))
}
