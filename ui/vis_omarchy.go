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
		omarchyNoise[i] = float64(lcgNext(&s)%100000) / 100000.0
	}
}

// omarchyNoiseAt samples the field bilinearly with a smoothstep, wrapping at
// its edges, so the texture drifts as blobs rather than as loose pixels.
func omarchyNoiseAt(u, v float64) float64 {
	const (
		size = omarchyNoiseSize
		mask = size - 1
	)
	fu, fv := math.Floor(u), math.Floor(v)
	fx, fy := u-fu, v-fv
	x0 := int(fu) & mask
	y0 := int(fv) & mask
	x1 := (x0 + 1) & mask
	y1 := (y0 + 1) & mask
	sx := fx * fx * (3 - 2*fx)
	sy := fy * fy * (3 - 2*fy)
	row0 := y0 * size
	row1 := y1 * size
	a := omarchyNoise[row0+x0]
	b := omarchyNoise[row0+x1]
	c := omarchyNoise[row1+x0]
	d := omarchyNoise[row1+x1]
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

// omarchyPixelCache stores static per-pixel values for the current panel size.
type omarchyPixelCache struct {
	shade     float64 // -1 marks a logo pixel, 0 marks cleared pixels below 0.004
	jitterSin float64
	jitterCos float64
	threshold float64
}

// omarchyRenderCache holds panel-sized static pixel tables and per-column work space.
type omarchyRenderCache struct {
	pxRows, pxCols int
	pixels         []omarchyPixelCache
	levels         []float64
}

func (v *Visualizer) omarchyCacheFor(pxRows, pxCols int) *omarchyRenderCache {
	c := v.omarchyCache
	if c != nil && c.pxRows == pxRows && c.pxCols == pxCols {
		return c
	}
	need := pxRows * pxCols
	if c == nil {
		c = &omarchyRenderCache{}
		v.omarchyCache = c
	}
	c.pxRows, c.pxCols = pxRows, pxCols
	if cap(c.pixels) < need {
		c.pixels = make([]omarchyPixelCache, need)
	} else {
		c.pixels = c.pixels[:need]
	}
	if cap(c.levels) < pxCols {
		c.levels = make([]float64, pxCols)
	} else {
		c.levels = c.levels[:pxCols]
	}

	mark, scale := omarchyMarkFor(pxRows, pxCols)
	markW, markH := mark.w*scale, mark.h*scale
	markX, markY := (pxCols-markW)/2, (pxRows-markH)/2

	for pr := range pxRows {
		rowOff := pr * pxCols
		bayerRow := (pr & 7) * 8
		for pc := range pxCols {
			var p omarchyPixelCache
			if scale > 0 && pc >= markX && pr >= markY && pc < markX+markW && pr < markY+markH &&
				mark.at((pc-markX)/scale, (pr-markY)/scale) {
				p.shade = -1
				c.pixels[rowOff+pc] = p
				continue
			}

			shade := 1.0
			if scale > 0 {
				dx := max(0.0, max(float64(markX-pc), float64(pc-(markX+markW-1))))
				dy := max(0.0, max(float64(markY-pr), float64(pr-(markY+markH-1))))
				if dx < omarchyClearReach && dy < omarchyClearReach {
					r := min(1.0, math.Hypot(dx, dy)/omarchyClearReach)
					shade = r * r * r
				}
			}
			if shade < 0.004 {
				p.shade = 0
				c.pixels[rowOff+pc] = p
				continue
			}

			p.shade = shade
			phase := omarchyJitter(pr, pc) * 2 * math.Pi
			p.jitterSin, p.jitterCos = math.Sincos(phase)
			p.threshold = 0.55*(float64(omarchyBayer[bayerRow+(pc&7)])+0.5)/64 +
				0.45*omarchyJitter(pr+7, pc+13)
			c.pixels[rowOff+pc] = p
		}
	}
	return c
}

// renderOmarchy draws the field. Half blocks give two pixels per character
// cell; a lit pixel takes its tier from how hard the music is pushing it.
func (v *Visualizer) renderOmarchy(bands []float64) string {
	rows, cols := v.Rows, v.columns()
	if rows <= 0 || cols <= 0 {
		return strings.Repeat("\n", max(0, rows-1))
	}
	pxRows, pxCols := rows*2, cols
	cache := v.omarchyCacheFor(pxRows, pxCols)

	t := float64(v.frame) * 0.03

	// The mean level lets the whole field breathe with the music.
	amp := 0.0
	if len(bands) > 0 {
		for _, b := range bands {
			amp += b
		}
		amp /= float64(len(bands))
	}

	// Precompute the mirrored band level once per column for this frame.
	levels := cache.levels
	n := len(bands)
	if n == 0 {
		clear(levels)
	} else {
		half := float64(pxCols) / 2
		denom := math.Max(half, 1)
		for pc := range pxCols {
			side := math.Min(1, math.Abs(float64(pc)+0.5-half)/denom)
			pos := (1-side)*float64(n) - 0.5
			b0 := min(max(int(math.Floor(pos)), 0), n-1)
			b1 := min(b0+1, n-1)
			mixB := math.Max(0, math.Min(1, pos-float64(b0)))
			raw := bands[b0]*(1-mixB) + bands[b1]*mixB
			levels[pc] = math.Max(0, (raw-omarchySpectrumFloor)/(1-omarchySpectrumFloor))
		}
	}

	sinT, cosT := math.Sincos(t * 1.1)
	t14, t055 := t*0.14, t*0.055
	t08, t06 := t*0.08, t*0.06
	ampRest := amp * 0.22
	ampTier := amp * 0.12
	ampMark := 0.34 + amp*0.45
	pxRowsF := float64(pxRows)
	pixels := cache.pixels

	pixel := func(pr, pc int) (bool, int) {
		p := pixels[pr*pxCols+pc]
		if p.shade < 0 {
			return true, specTag(ampMark + levels[pc]*0.35)
		}
		if p.shade == 0 {
			return false, 0
		}

		spec := 0.0
		if level := levels[pc]; level > 0 {
			up := float64(pxRows - 1 - pr)
			if tall := level * pxRowsF * omarchySpectrumReach; up < tall {
				spec = level * math.Pow(1-up/tall, 0.85)
			}
		}

		u, w := float64(pc)/omarchyCellsPerNoise, float64(pr)/omarchyCellsPerNoise
		base := 0.6*omarchyNoiseAt(u+t14, w-t055) +
			0.4*omarchyNoiseAt(u*0.55-t08, w*0.55+t06)
		tw := 0.5 + 0.5*(sinT*p.jitterCos+cosT*p.jitterSin)
		lum := p.shade*(0.30+0.52*base*base+0.18*tw+ampRest)*omarchyRest +
			spec*0.72*min(1.0, p.shade*3)

		if lum <= p.threshold {
			return false, 0
		}
		return true, specTag(spec*0.55 + ampTier)
	}

	var sb strings.Builder
	sb.Grow(rows * (pxCols*6 + 16))
	w := styleRunWriter{sb: &sb}
	for row := range rows {
		if row > 0 {
			sb.WriteByte('\n')
		}
		tag := -1
		w.setTag(-1)
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
				tag = cellTag
				w.setTag(tag)
			}
			w.writeRune(glyph)
		}
		w.flush()
	}

	return sb.String()
}
