package ui

import (
	"math"
	"strings"
)

// The Omarchy field, brought into the panel from omarchy.org's hero: drifting
// value noise thresholded through an ordered dither into hard on/off pixels,
// the spectrum thickening columns from the bottom with the bass at the outer
// edges, and the Omarchy mark stamped into the same lattice rather than drawn
// on top of it. The mark never dissolves; what passes over it recolours it.
//
// Two pixels to a character cell, stacked, so a pixel comes out roughly square
// in a terminal: the pair is drawn as a half block and wears the warmer of its
// two tiers. The wordmark arrives already in that form, which is why it can be
// pasted in as the art it is rather than transcribed into a bitmap.

const (
	// Pixels per unit of noise: how big the drifting blobs read.
	omarchyCellsPerNoise = 6.0
	// Below this a band is resting and its column shows nothing extra.
	omarchySpectrumFloor = 0.06
	// How much of the field's height the loudest band may climb.
	omarchySpectrumReach = 0.95
	// How much of a pixel's luminance the resting texture is worth. Much above
	// this and the ordered matrix stops reading as texture and starts reading
	// as a checkerboard, which is the one thing this field must not look like.
	omarchyRest = 0.34
	// How far, in pixels, the resting texture stays clear of the mark.
	omarchyClearReach = 6.0
	// How it comes back over that distance. A smoothstep is half strength at
	// the halfway mark, which packs texture right against the mark; cubed, it
	// is an eighth there, so the mark keeps its edge.
	omarchyClearCurve = 3.0
	// The largest whole multiple a mark is allowed to grow to.
	omarchyMaxScale = 3
	// Pixels of field kept around a mark, so it never touches the panel edge.
	omarchyMarkMargin = 2
)

// omarchyWordmarkArt is logo.txt from omacom/omarchy, verbatim. It is already
// half-block art, which makes it a bitmap eighty-one pixels across and twenty
// down - the same eighty-one-cell lattice omarchy.org cuts its own wordmark
// into, arriving here for free.
var omarchyWordmarkArt = []string{
	"                 ▄▄▄",
	" ▄█████▄    ▄███████████▄    ▄███████   ▄███████   ▄███████   ▄█   █▄    ▄█   █▄",
	"███   ███  ███   ███   ███  ███   ███  ███   ███  ███   ███  ███   ███  ███   ███",
	"███   ███  ███   ███   ███  ███   ███  ███   ███  ███   █▀   ███   ███  ███   ███",
	"███   ███  ███   ███   ███ ▄███▄▄▄███ ▄███▄▄▄██▀  ███       ▄███▄▄▄███▄ ███▄▄▄███",
	"███   ███  ███   ███   ███ ▀███▀▀▀███ ▀███▀▀▀▀    ███      ▀▀███▀▀▀███  ▀▀▀▀▀▀███",
	"███   ███  ███   ███   ███  ███   ███ ██████████  ███   █▄   ███   ███  ▄██   ███",
	"███   ███  ███   ███   ███  ███   ███  ███   ███  ███   ███  ███   ███  ███   ███",
	" ▀█████▀    ▀█   ███   █▀   ███   █▀   ███   ███  ███████▀   ███   █▀    ▀█████▀",
	"                                       ███   █▀",
}

// omarchySquareBits is the square-spiral Omarchy mark, for panels too narrow
// to hold the wordmark. Fifteen pixels square, from omarchy-logo.svg, whose
// every path coordinate is a multiple of 80 in a 1200 viewBox.
var omarchySquareBits = []string{
	"111111111111111",
	"100000010000001",
	"101111110001101",
	"101000000000101",
	"101000000000101",
	"101000000000101",
	"101000000000101",
	"111000000000101",
	"101000000000101",
	"101000000000101",
	"101000000000101",
	"101000000000101",
	"101111111111101",
	"100000010000001",
	"111111110111111",
}

// omarchyGlyph is a mark as pixels on the field's own lattice.
type omarchyGlyph struct {
	w, h int
	on   []bool
}

func (g omarchyGlyph) at(x, y int) bool {
	return x >= 0 && y >= 0 && x < g.w && y < g.h && g.on[y*g.w+x]
}

// decodeHalfBlockArt reads half-block art as pixels: one line of art is two
// rows of them, which is the same trick the renderer uses to put them back.
func decodeHalfBlockArt(art []string) omarchyGlyph {
	width := 0
	for _, line := range art {
		width = max(width, len([]rune(line)))
	}
	g := omarchyGlyph{w: width, h: len(art) * 2}
	g.on = make([]bool, g.w*g.h)
	for row, line := range art {
		for col, r := range []rune(line) {
			upper := r == '\u2588' || r == '\u2580'
			lower := r == '\u2588' || r == '\u2584'
			if upper {
				g.on[(row*2)*g.w+col] = true
			}
			if lower {
				g.on[(row*2+1)*g.w+col] = true
			}
		}
	}
	return g
}

// decodeBitRows reads a plain 1/0 bitmap, one pixel per character.
func decodeBitRows(rows []string) omarchyGlyph {
	width := 0
	for _, line := range rows {
		width = max(width, len(line))
	}
	g := omarchyGlyph{w: width, h: len(rows)}
	g.on = make([]bool, g.w*g.h)
	for row, line := range rows {
		for col := 0; col < len(line); col++ {
			if line[col] == '1' {
				g.on[row*g.w+col] = true
			}
		}
	}
	return g
}

// omarchyMarks are tried in order: the wordmark is the mark, and the square is
// what a narrow panel gets instead of nothing.
var omarchyMarks []omarchyGlyph

// omarchyBayer is the classic 8x8 ordered dither matrix, 0..63.
var omarchyBayer = [64]int{
	0, 32, 8, 40, 2, 34, 10, 42,
	48, 16, 56, 24, 50, 18, 58, 26,
	12, 44, 4, 36, 14, 46, 6, 38,
	60, 28, 52, 20, 62, 30, 54, 22,
	3, 35, 11, 43, 1, 33, 9, 41,
	51, 19, 59, 27, 49, 17, 57, 25,
	15, 47, 7, 39, 13, 45, 5, 37,
	63, 31, 55, 23, 61, 29, 53, 21,
}

const omarchyNoiseSize = 64

// omarchyNoise is the value-noise field the texture drifts through, built once
// from a fixed sequence rather than math/rand so the field looks the same on
// every run and a test can assert on it.
var omarchyNoise [omarchyNoiseSize * omarchyNoiseSize]float64

func init() {
	omarchyMarks = []omarchyGlyph{
		decodeHalfBlockArt(omarchyWordmarkArt),
		decodeBitRows(omarchySquareBits),
	}
	s := uint64(0x9E3779B97F4A7C15)
	for i := range omarchyNoise {
		s = s*6364136223846793005 + 1442695040888963407
		omarchyNoise[i] = float64((s>>33)%100000) / 100000.0
	}
}

// omarchyNoiseAt samples the field bilinearly with a smoothstep, wrapping at
// its edges, so the texture drifts as blobs rather than as loose pixels.
func omarchyNoiseAt(u, v float64) float64 {
	const size = omarchyNoiseSize
	u -= math.Floor(u/size) * size
	v -= math.Floor(v/size) * size
	x0, y0 := int(u), int(v)
	x1, y1 := (x0+1)%size, (y0+1)%size
	fx, fy := u-float64(x0), v-float64(y0)
	sx := fx * fx * (3 - 2*fx)
	sy := fy * fy * (3 - 2*fy)
	a := omarchyNoise[y0*size+x0]
	b := omarchyNoise[y0*size+x1]
	c := omarchyNoise[y1*size+x0]
	d := omarchyNoise[y1*size+x1]
	return (a+(b-a)*sx)*(1-sy) + (c+(d-c)*sx)*sy
}

// omarchyJitter is a fixed offset per pixel, held steady across frames. Bayer
// on its own lights the same low-index cells everywhere, which at this density
// reads as a regular lattice; this scatters the resting field while the
// ordered structure still shows through wherever a loud band pushes a column
// bright.
func omarchyJitter(row, col int) float64 {
	h := uint64(row)*6271 + uint64(col)*3037 + 0x9E3779B9
	h ^= h >> 16
	h *= 0x45d9f3b37197344b
	h ^= h >> 16
	return float64(h%10000) / 10000.0
}

// omarchyFitScale returns the whole multiple g can be drawn at inside the
// panel, or 0 if it does not fit at all. Neither mark survives being scaled
// down, so below its own size it simply does not appear.
func omarchyFitScale(g omarchyGlyph, pxRows, pxCols int) int {
	if g.w == 0 || g.h == 0 {
		return 0
	}
	if pxRows < g.h+omarchyMarkMargin || pxCols < g.w+omarchyMarkMargin {
		return 0
	}
	return min(min((pxRows-omarchyMarkMargin)/g.h, (pxCols-omarchyMarkMargin)/g.w), omarchyMaxScale)
}

// omarchyMarkFor picks the largest mark the panel can hold: the wordmark
// first, then the square, then none - a five-row panel has room for neither,
// and the field carries it alone.
func omarchyMarkFor(pxRows, pxCols int) (omarchyGlyph, int) {
	for _, g := range omarchyMarks {
		if s := omarchyFitScale(g, pxRows, pxCols); s > 0 {
			return g, s
		}
	}
	return omarchyGlyph{}, 0
}

// renderOmarchy draws the field. Half blocks give two pixels per character
// cell; a lit pixel takes its tier from how hard the music is pushing it.
func (v *Visualizer) renderOmarchy(bands []float64) string {
	rows := v.Rows
	if rows <= 0 || PanelWidth <= 0 {
		return strings.Repeat("\n", max(0, rows-1))
	}
	pxRows, pxCols := rows*2, PanelWidth

	t := float64(v.frame) * 0.03

	// The mean level, so the whole field breathes with the record rather than
	// only the columns a loud band happens to land on.
	amp := 0.0
	if len(bands) > 0 {
		for _, b := range bands {
			amp += b
		}
		amp /= float64(len(bands))
	}

	mark, scale := omarchyMarkFor(pxRows, pxCols)
	markW, markH := mark.w*scale, mark.h*scale
	markX, markY := (pxCols-markW)/2, (pxRows-markH)/2

	// This column's band, mirrored about the middle: the bass at the outer
	// edges where the field has the most room, the treble in towards the mark.
	// Blended with its neighbour so the bands do not read as bars.
	levelAt := func(pc int) float64 {
		n := len(bands)
		if n == 0 {
			return 0
		}
		half := float64(pxCols) / 2
		side := math.Min(1, math.Abs(float64(pc)+0.5-half)/math.Max(half, 1))
		pos := (1-side)*float64(n) - 0.5
		b0 := min(max(int(math.Floor(pos)), 0), n-1)
		b1 := min(b0+1, n-1)
		mixB := math.Max(0, math.Min(1, pos-float64(b0)))
		raw := bands[b0]*(1-mixB) + bands[b1]*mixB
		return math.Max(0, (raw-omarchySpectrumFloor)/(1-omarchySpectrumFloor))
	}

	// How far the resting texture has come back from the mark at this pixel.
	shadeAt := func(pr, pc int) float64 {
		if scale == 0 {
			return 1
		}
		dx := math.Max(0, math.Max(float64(markX-pc), float64(pc-(markX+markW-1))))
		dy := math.Max(0, math.Max(float64(markY-pr), float64(pr-(markY+markH-1))))
		return math.Pow(math.Min(1, math.Hypot(dx, dy)/omarchyClearReach), omarchyClearCurve)
	}

	pixel := func(pr, pc int) (bool, int) {
		// The mark is cells of this same lattice, not a layer over them.
		// Bounds before the lookup: Go truncates division towards zero, so a
		// pixel one column left of the mark would divide to column zero and
		// smear its leftmost pixels outwards at any scale above one.
		if scale > 0 && pc >= markX && pr >= markY && pc < markX+markW && pr < markY+markH &&
			mark.at((pc-markX)/scale, (pr-markY)/scale) {
			return true, specTag(0.34 + levelAt(pc)*0.35 + amp*0.45)
		}

		shade := shadeAt(pr, pc)
		if shade < 0.004 {
			return false, 0
		}

		// The spectrum thickens the column from the bottom up, as high as the
		// band is loud, easing off towards the top rather than thinning in a
		// straight line so the body of a column stays full.
		spec := 0.0
		if level := levelAt(pc); level > 0 {
			up := float64(pxRows - 1 - pr)
			if tall := level * float64(pxRows) * omarchySpectrumReach; up < tall {
				spec = level * math.Pow(1-up/tall, 0.85)
			}
		}

		u, w := float64(pc)/omarchyCellsPerNoise, float64(pr)/omarchyCellsPerNoise
		base := 0.6*omarchyNoiseAt(u+t*0.14, w-t*0.055) +
			0.4*omarchyNoiseAt(u*0.55-t*0.08, w*0.55+t*0.06)
		// Each pixel also blinks on its own rhythm, so one appearing is a local
		// event rather than the whole pattern drifting past.
		tw := 0.5 + 0.5*math.Sin(t*1.1+omarchyJitter(pr, pc)*2*math.Pi)
		lum := shade*(0.30+0.52*base*base+0.18*tw+amp*0.22)*omarchyRest +
			spec*0.72*math.Min(1, shade*3)

		// The web field can lean on Bayer for most of its threshold because it
		// has hundreds of cells across. A panel has tens, so the 8x8 tile
		// repeats often enough to read as a checkerboard; the jitter carries
		// more of the weight here, and the ordered structure only surfaces
		// where a loud band pushes a column bright.
		threshold := 0.55*(float64(omarchyBayer[(pr&7)*8+(pc&7)])+0.5)/64 +
			0.45*omarchyJitter(pr+7, pc+13)
		if lum <= threshold {
			return false, 0
		}
		return true, specTag(spec*0.55 + amp*0.12)
	}

	lines := make([]string, rows)
	for row := range rows {
		var sb, run strings.Builder
		tag := -1
		for col := range pxCols {
			upLit, upTier := pixel(row*2, col)
			loLit, loTier := pixel(row*2+1, col)

			glyph := ' '
			cellTag := -1
			switch {
			case upLit && loLit:
				glyph, cellTag = '\u2588', max(upTier, loTier)
			case upLit:
				glyph, cellTag = '\u2580', upTier
			case loLit:
				glyph, cellTag = '\u2584', loTier
			}

			if cellTag != tag {
				flushStyleRun(&sb, &run, tag)
				tag = cellTag
			}
			run.WriteRune(glyph)
		}
		flushStyleRun(&sb, &run, tag)
		lines[row] = sb.String()
	}

	return strings.Join(lines, "\n")
}
