package ui

import (
	"image/color"
	"math"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// Backface culling is where a silent mistake would live: a face wound the
// wrong way still draws, it just draws the edges that should be hidden, and
// nothing else in the package would notice. A bar seen head-on shows its
// front, never its back.
func TestRedSectorFaceVisibility(t *testing.T) {
	// The unrotated bar, standing on the ground line with a full-height cap.
	corner := func(i int) [3]float64 {
		y := redSectorGroundY
		if redSectorCornerTop[i] {
			y = redSectorGroundY + redSectorMaxHeight
		}
		return [3]float64{
			redSectorCornerX[i] * redSectorHalfWidth,
			y,
			redSectorCornerZ[i] * redSectorHalfDepth,
		}
	}
	visible := func(face [4]int) bool {
		return redSectorFaceVisible(corner(face[0]), corner(face[1]), corner(face[2]), redSectorCameraZ)
	}

	front, back := redSectorFaces[0], redSectorFaces[1]
	if !visible(front) {
		t.Error("the front face should be visible to a camera in front of the bar")
	}
	if visible(back) {
		t.Error("the back face should be culled")
	}

	// Every face is either visible or culled, and a closed box never shows all
	// six at once. Four is the most a box can turn towards a viewer.
	count := 0
	for _, face := range redSectorFaces {
		if visible(face) {
			count++
		}
	}
	if count == 0 || count > 3 {
		t.Errorf("%d of six faces visible head-on, want between one and three", count)
	}
}

// The bars read their level against a range they track themselves, so a quiet
// band and a loud one both use the full height of the picture. Reading the
// level absolutely would leave the treble bars flat on nearly every track.
func TestRedSectorEnvelopeTracksBandRange(t *testing.T) {
	d := &redSectorDriver{}
	d.reset()

	// Bar 0 swings between 0.7 and 0.9, bar 1 between 0.1 and 0.3. Same span,
	// very different levels.
	bands := make([]float64, DefaultSpectrumBands)
	var peak0, peak1 float64
	for frame := range 400 {
		swing := 0.0
		if frame%2 == 0 {
			swing = 0.2
		}
		bands[0], bands[1] = 0.7+swing, 0.7+swing
		bands[2], bands[3] = 0.1+swing, 0.1+swing
		d.advance(bands)
		if frame > 200 {
			peak0 = max(peak0, d.heights[0])
			peak1 = max(peak1, d.heights[1])
		}
	}

	// Both reach up into the picture, and neither is pinned to an end stop.
	for i, peak := range [2]float64{peak0, peak1} {
		if peak <= redSectorMinHeight+0.1 {
			t.Errorf("bar %d peaks at %.3f, barely above the floor of %.2f",
				i, peak, redSectorMinHeight)
		}
	}
	if diff := math.Abs(peak0 - peak1); diff > 0.3 {
		t.Errorf("a loud bar peaks at %.3f and a quiet one at %.3f; the envelope "+
			"should bring them close", peak0, peak1)
	}

	// The envelope never closes past its own floor, whatever the material.
	for i := range 2 {
		if span := d.ceiling[i] - d.floor[i]; span < redSectorMinEnvelope-1e-9 {
			t.Errorf("bar %d envelope = %.4f, want at least %.2f", i, span, redSectorMinEnvelope)
		}
	}
}

// A band held at one level is not movement, and the bar it drives sinks to
// rest. This is what keeps a steady passage from standing at full height.
func TestRedSectorConstantBandSettlesToRest(t *testing.T) {
	d := &redSectorDriver{}
	d.reset()

	bands := make([]float64, DefaultSpectrumBands)
	bands[0], bands[1] = 0.8, 0.8
	for range 400 {
		d.advance(bands)
	}
	if d.heights[0] > redSectorMinHeight+1e-6 {
		t.Errorf("a constant band leaves the bar at %.4f, want it back at %.2f",
			d.heights[0], redSectorMinHeight)
	}
}

// A silent band must not divide out its partner: the pair takes the louder of
// the two, which is what keeps a bar alive on material cut above 16 kHz.
func TestRedSectorBarTakesLouderBand(t *testing.T) {
	loud := &redSectorDriver{}
	loud.reset()
	both := &redSectorDriver{}
	both.reset()

	oneSided := make([]float64, DefaultSpectrumBands)
	oneSided[0] = 0.9
	balanced := make([]float64, DefaultSpectrumBands)
	balanced[0], balanced[1] = 0.9, 0.9

	for range 100 {
		loud.advance(oneSided)
		both.advance(balanced)
	}
	if math.Abs(loud.heights[0]-both.heights[0]) > 1e-9 {
		t.Errorf("one loud band gives height %.4f, two give %.4f; they should agree",
			loud.heights[0], both.heights[0])
	}
}

func TestRedSectorBarTagThresholds(t *testing.T) {
	span := redSectorMaxHeight - redSectorMinHeight
	at := func(fraction float64) int8 {
		return redSectorBarTag(redSectorMinHeight + fraction*span)
	}
	cases := []struct {
		fraction float64
		want     int8
	}{
		{0.0, redSectorTagLow},
		{0.29, redSectorTagLow},
		{0.30, redSectorTagMid},
		{0.59, redSectorTagMid},
		{0.60, redSectorTagHigh},
		{1.0, redSectorTagHigh},
	}
	for _, tc := range cases {
		if got := at(tc.fraction); got != tc.want {
			t.Errorf("tag at %.2f of the range = %d, want %d", tc.fraction, got, tc.want)
		}
	}
}

// The whole point of the seven-tag order: a bar is drawn in front of a star
// whatever colour either of them wears. If a star tag ever outranked a bar tag,
// the field would punch holes through the object and nothing would catch it but
// the eye.
func TestRedSectorBarsOutrankStars(t *testing.T) {
	span := redSectorMaxHeight - redSectorMinHeight
	for _, fraction := range []float64{0, 0.3, 0.6, 1} {
		barTag := redSectorBarTag(redSectorMinHeight + fraction*span)
		if int(barTag) <= redSectorStarTags {
			t.Errorf("bar tag %d at %.1f of the range does not outrank the %d star tags",
				barTag, fraction, redSectorStarTags)
		}
		if int(barTag) > redSectorTagCount {
			t.Errorf("bar tag %d is outside the palette of %d", barTag, redSectorTagCount)
		}
	}

	// A star drawn into a cell a bar already owns leaves it alone.
	var g redSectorGrid
	g.ensure(4, 4)
	g.set(0, 0, redSectorTagLow)
	g.set(0, 0, redSectorStarTags)
	if got := g.cells[0]; got != redSectorTagLow {
		t.Errorf("cell holds tag %d after a star was drawn over a bar, want %d",
			got, redSectorTagLow)
	}
}

// Stars wear the quiet twin of a spectrum colour. On the default theme that
// twin is exact: the bright ANSI colours are 8 above their plain counterparts.
func TestRedSectorDimFindsANSITwin(t *testing.T) {
	cases := []struct {
		in   color.Color
		want color.Color
	}{
		{lipgloss.ANSIColor(10), lipgloss.ANSIColor(2)}, // bright green -> green
		{lipgloss.ANSIColor(11), lipgloss.ANSIColor(3)}, // bright yellow -> yellow
		{lipgloss.ANSIColor(9), lipgloss.ANSIColor(1)},  // bright red -> red
		{lipgloss.ANSIColor(7), lipgloss.ANSIColor(7)},  // already plain, left alone
	}
	for _, tc := range cases {
		if got := redSectorDim(tc.in); got != tc.want {
			t.Errorf("dim(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}

	// A theme colour has no twin, so each channel is scaled instead.
	r, g, b, _ := redSectorDim(lipgloss.Color("#64c864")).RGBA()
	channels := [3]uint32{r >> 8, g >> 8, b >> 8}
	want := [3]uint32{0x3c, 0x78, 0x3c} // six tenths of 0x64, 0xc8, 0x64
	if channels != want {
		t.Errorf("dim(#64c864) = %v, want %v", channels, want)
	}
}

// The picture is fitted to a hull the object can never exceed, so a full-height
// object still lands inside the panel.
func TestRedSectorFitKeepsHullInsidePanel(t *testing.T) {
	const dotRows, dotCols = 28, 152

	for _, frame := range []float64{0, 7, 31, 250} {
		cosY, sinY := math.Cos(frame*redSectorSpinY), math.Sin(frame*redSectorSpinY)
		cosX, sinX := math.Cos(frame*redSectorSpinX), math.Sin(frame*redSectorSpinX)
		camZ := redSectorCameraZ + math.Sin(frame*redSectorZoomRate)*redSectorZoomAmplitude
		fitX, fitY, centreX, centreY := redSectorFit(dotRows, dotCols, frame, cosX, sinX, cosY, sinY, camZ)

		hullX := float64(redSectorBars-1)/2*redSectorPitch + redSectorHalfWidth
		for _, sx := range [2]float64{-1, 1} {
			for _, y := range [2]float64{redSectorGroundY, redSectorGroundY + redSectorMaxHeight} {
				for _, sz := range [2]float64{-1, 1} {
					_, _, _, hx, hy := redSectorPlace(sx*hullX, y, sz*redSectorHalfDepth,
						cosX, sinX, cosY, sinY, camZ)
					px := centreX + hx*fitX
					py := centreY - hy*fitY
					if px < 0 || px > float64(dotCols-1) || py < 0 || py > float64(dotRows-1) {
						t.Errorf("frame %.0f: hull corner lands at (%.1f, %.1f), outside %dx%d",
							frame, px, py, dotCols, dotRows)
					}
				}
			}
		}
	}
}

// Stars keep their place from frame to frame and drift left, which is what
// separates a starfield from per-frame noise.
func TestRedSectorStarsDriftLeft(t *testing.T) {
	const dotRows, dotCols = 20, 100

	first := &redSectorDriver{}
	first.grid.ensure(dotRows, dotCols)
	first.drawStars(dotRows, dotCols, 0)

	again := &redSectorDriver{}
	again.grid.ensure(dotRows, dotCols)
	again.drawStars(dotRows, dotCols, 0)

	if !equalGrids(first.grid, again.grid) {
		t.Error("the same frame drew a different field; the star hash is not deterministic")
	}

	later := &redSectorDriver{}
	later.grid.ensure(dotRows, dotCols)
	later.drawStars(dotRows, dotCols, 40)
	if equalGrids(first.grid, later.grid) {
		t.Error("the field did not move over forty frames")
	}

	lit := 0
	for _, cell := range later.grid.cells {
		if cell != 0 {
			lit++
		}
		if cell != 0 && (cell < 1 || int(cell) > redSectorStarTags) {
			t.Fatalf("star drawn on tag %d, want one of the %d star tags",
				cell, redSectorStarTags)
		}
	}
	if lit == 0 {
		t.Error("no stars drawn")
	}
}

// A field whose coordinates are a straight line in the star index puts every
// star on one of a few diagonals, and short ruled chains of stars are what a
// viewer actually notices. The exact step between neighbours is not the tell,
// because wrapping breaks it up; the direction of that step is, since a lattice
// only ever walks along its own gradient. Measured on the field this mode draws,
// the linear hash the plugin used offered sixteen directions across eighty-eight
// steps, an avalanche hash eighty-six.
func TestRedSectorStarsAreNotOnALattice(t *testing.T) {
	const dotRows, dotCols = 28, 174

	type direction struct{ dx, dy int }
	directions := map[direction]int{}
	previous := [2]int{}
	for i := 1; i <= redSectorMaxStars; i++ {
		x := int(redSectorStarHash(i, 0) * dotCols)
		y := int(redSectorStarHash(i, 1) * dotRows)
		if i > 1 {
			dx, dy := x-previous[0], y-previous[1]
			step := greatestCommonDivisor(dx, dy)
			directions[direction{dx / step, dy / step}]++
		}
		previous = [2]int{x, y}
	}

	steps := redSectorMaxStars - 1
	if want := steps / 2; len(directions) < want {
		t.Errorf("%d distinct directions across %d steps, want at least %d; "+
			"the field is walking a lattice", len(directions), steps, want)
	}
}

// greatestCommonDivisor reduces a step to its direction. It returns 1 for the
// zero step, which has no direction to speak of.
func greatestCommonDivisor(a, b int) int {
	a, b = abs(a), abs(b)
	for b != 0 {
		a, b = b, a%b
	}
	if a == 0 {
		return 1
	}
	return a
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func equalGrids(a, b redSectorGrid) bool {
	if len(a.cells) != len(b.cells) {
		return false
	}
	for i := range a.cells {
		if a.cells[i] != b.cells[i] {
			return false
		}
	}
	return true
}

// A panel too narrow to draw into still has to return the exact number of
// lines the layout reserved, or the frame below it shifts.
func TestRedSectorNarrowPanelKeepsRowCount(t *testing.T) {
	defer WithPanelWidth(4)()

	v := NewVisualizer(44100)
	v.Rows = 5
	v.Mode = VisRedSector

	d := newRedSectorDriver()
	lines := strings.Split(d.Render(v), "\n")
	if len(lines) != v.Rows {
		t.Errorf("%d lines at a four-column panel, want %d", len(lines), v.Rows)
	}
}
