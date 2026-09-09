// Package globe renders an orthographic view of the Earth into Braille cells:
// land as dots, countries of interest lit, and weighted markers on top. It is
// the terminal counterpart of the listener globe on cliamp.stream and backs
// `cliamp radio --stats --globe`.
package globe

import (
	"cmp"
	"math"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Terrain classifies a point on the surface.
type Terrain uint8

const (
	Sea Terrain = iota
	Land
	Lit // land the caller wants highlighted
)

// Surface answers what lies at a coordinate in degrees.
type Surface interface {
	At(lat, lon float64) Terrain
}

// Mark is a dot drawn at a coordinate, sized by Weight in (0, 1].
type Mark struct {
	Lat, Lon float64
	Weight   float64
	// Label is written beside the dot when there is room. Only the heaviest
	// few visible marks get theirs drawn, so the globe stays legible.
	Label string
}

// Palette styles each layer as SGR sequences; the zero value renders plain
// text. A style wraps whole runs of cells, so only attributes that fit in one
// sequence (colours, bold, faint) belong here.
type Palette struct {
	Grid    ansi.Style // graticule dots over the sea
	Rim     ansi.Style // the disc's edge
	Land    ansi.Style
	Lit     ansi.Style
	Mark    ansi.Style // markers facing the viewer
	MarkFar ansi.Style // markers near the limb
	Label   ansi.Style
}

// Layer priorities, highest wins when dots share a cell.
const (
	tierNone int8 = iota
	tierGrid
	tierRim
	tierLand
	tierLit
	tierMarkFar
	tierMark
	tierLabel
)

const (
	maxLabels = 3    // labels drawn per frame
	gridStep  = 30.0 // graticule spacing in degrees
)

// Globe projects a Surface into a character grid. The zero value is unusable;
// call New.
type Globe struct {
	surface Surface
	cols    int
	rows    int
	lon     float64 // longitude facing the viewer, degrees in [-180, 180)
	tilt    float64 // latitude facing the viewer, degrees
	dots    []dot   // per Braille dot, row-major over 2*cols x 4*rows
	cells   []cell  // per character cell, reused between frames
	radius  float64 // disc radius in dots
	cx, cy  float64 // disc centre in dot coordinates
	stale   bool
}

// dot caches everything about one Braille dot that does not change as the
// globe spins: only the longitude offset does.
type dot struct {
	lat      float32
	lon      float32 // relative to the longitude facing the viewer
	inside   bool
	rim      bool
	parallel bool // a latitude gridline passes between this dot and the one below
}

type cell struct {
	r    rune
	tier int8
}

// New returns a globe over surface with no size; call Resize before Render.
func New(surface Surface) *Globe {
	return &Globe{surface: surface, tilt: 14, stale: true}
}

// Resize sets the character grid the globe is drawn into.
func (g *Globe) Resize(cols, rows int) {
	if cols == g.cols && rows == g.rows {
		return
	}
	g.cols, g.rows = cols, rows
	g.stale = true
}

// Lon is the longitude facing the viewer, in degrees.
func (g *Globe) Lon() float64 { return g.lon }

// Spin turns the globe by deg degrees; positive brings the east round.
func (g *Globe) Spin(deg float64) { g.lon = wrapLon(g.lon + deg) }

// SetLon faces the viewer at a longitude.
func (g *Globe) SetLon(deg float64) { g.lon = wrapLon(deg) }

// TiltBy tips the globe by deg degrees, clamped so the poles stay in view.
func (g *Globe) TiltBy(deg float64) { g.SetTilt(g.tilt + deg) }

// SetTilt faces the viewer at a latitude, clamped to [-80, 80].
func (g *Globe) SetTilt(deg float64) {
	deg = min(max(deg, -80), 80)
	if deg != g.tilt {
		g.tilt = deg
		g.stale = true
	}
}

// wrapLon brings any longitude into [-180, 180).
func wrapLon(deg float64) float64 {
	deg = math.Mod(deg+180, 360)
	if deg < 0 {
		deg += 360
	}
	return deg - 180
}

// wrapNear is wrapLon for longitudes within one turn of the range, which is
// all the per-dot hot path ever sees; it saves a math.Mod per dot.
func wrapNear(deg float64) float64 {
	if deg >= 180 {
		return deg - 360
	}
	if deg < -180 {
		return deg + 360
	}
	return deg
}

// prepare recomputes the per-dot geometry after a resize or tilt change.
func (g *Globe) prepare() {
	g.stale = false
	dotCols, dotRows := 2*g.cols, 4*g.rows
	g.dots = make([]dot, dotCols*dotRows)
	g.cells = make([]cell, g.cols*g.rows)
	g.radius = 0
	if dotCols < 4 || dotRows < 4 {
		return
	}
	g.radius = float64(min(dotCols, dotRows))/2 - 1
	g.cx = float64(dotCols-1) / 2
	g.cy = float64(dotRows-1) / 2
	sinT, cosT := math.Sincos(g.tilt * math.Pi / 180)
	rimInner := 1 - 1.5/g.radius

	for y := range dotRows {
		for x := range dotCols {
			dx := (float64(x) - g.cx) / g.radius
			dy := (g.cy - float64(y)) / g.radius
			rr := dx*dx + dy*dy
			if rr > 1 {
				continue
			}
			z := math.Sqrt(1 - rr)
			// Undo the tilt: rotate the view vector back to a globe whose
			// axis is vertical, then read latitude and longitude off it.
			ys := dy*cosT + z*sinT
			zs := -dy*sinT + z*cosT
			d := &g.dots[y*dotCols+x]
			d.inside = true
			d.rim = math.Sqrt(rr) > rimInner
			d.lat = float32(math.Asin(min(max(ys, -1), 1)) * 180 / math.Pi)
			d.lon = float32(math.Atan2(dx, zs) * 180 / math.Pi)
		}
	}
	for y := 0; y+1 < dotRows; y++ {
		for x := range dotCols {
			d, below := &g.dots[y*dotCols+x], &g.dots[(y+1)*dotCols+x]
			if d.inside && below.inside {
				d.parallel = math.Floor(float64(d.lat)/gridStep) != math.Floor(float64(below.lat)/gridStep)
			}
		}
	}
}

// project maps a coordinate to dot coordinates and the depth towards the
// viewer; depth <= 0 means the point is on the far side.
func (g *Globe) project(lat, lon float64) (x, y, depth float64) {
	sinLat, cosLat := math.Sincos(lat * math.Pi / 180)
	sinLon, cosLon := math.Sincos((lon - g.lon) * math.Pi / 180)
	sinT, cosT := math.Sincos(g.tilt * math.Pi / 180)
	px := cosLat * sinLon
	ys := sinLat
	zs := cosLat * cosLon
	py := ys*cosT - zs*sinT
	depth = ys*sinT + zs*cosT
	return g.cx + px*g.radius, g.cy - py*g.radius, depth
}

// Render draws the globe as Rows lines of Cols cells each.
func (g *Globe) Render(marks []Mark, p Palette) string {
	if g.stale {
		g.prepare()
	}
	if g.cols <= 0 || g.rows <= 0 {
		return ""
	}
	for i := range g.cells {
		g.cells[i] = cell{r: ' '}
	}
	g.paintSurface()
	g.paintMarks(marks)
	return g.compose(p)
}

func (g *Globe) paintSurface() {
	dotCols := 2 * g.cols
	for row := range g.rows {
		for col := range g.cols {
			var bits rune
			tier := tierNone
			for dr := range 4 {
				for dc := range 2 {
					x, y := col*2+dc, row*4+dr
					d := &g.dots[y*dotCols+x]
					if !d.inside {
						continue
					}
					t := g.dotTier(d, x, y)
					if t == tierNone {
						continue
					}
					bits |= brailleBit[dr][dc]
					tier = max(tier, t)
				}
			}
			if bits != 0 {
				g.cells[row*g.cols+col] = cell{r: 0x2800 | bits, tier: tier}
			}
		}
	}
}

// dotTier classifies one dot: land first, otherwise the rim or a gridline
// over the sea.
func (g *Globe) dotTier(d *dot, x, y int) int8 {
	lon := wrapNear(float64(d.lon) + g.lon)
	switch g.surface.At(float64(d.lat), lon) {
	case Lit:
		return tierLit
	case Land:
		return tierLand
	}
	if d.rim {
		return tierRim
	}
	if d.parallel {
		return tierGrid
	}
	if math.Abs(float64(d.lat)) < 80 && x+1 < 2*g.cols {
		right := &g.dots[y*2*g.cols+x+1]
		if right.inside && math.Floor(lon/gridStep) != math.Floor(wrapNear(float64(right.lon)+g.lon)/gridStep) {
			return tierGrid
		}
	}
	return tierNone
}

func (g *Globe) paintMarks(marks []Mark) {
	if g.radius <= 0 {
		return
	}
	ordered := slices.Clone(marks)
	slices.SortStableFunc(ordered, func(a, b Mark) int { return cmp.Compare(b.Weight, a.Weight) })

	type placed struct {
		col, row int
		label    string
	}
	var visible []placed
	for _, m := range ordered {
		x, y, depth := g.project(m.Lat, m.Lon)
		if depth < 0.08 {
			continue
		}
		col, row := int(x/2), int(y/4)
		if col < 0 || col >= g.cols || row < 0 || row >= g.rows {
			continue
		}
		c := &g.cells[row*g.cols+col]
		if c.tier >= tierMarkFar {
			continue // a heavier mark already owns this cell
		}
		c.r = markGlyph(m.Weight)
		c.tier = tierMark
		if depth < 0.35 {
			c.tier = tierMarkFar
		}
		visible = append(visible, placed{col, row, m.Label})
	}

	labels := 0
	for _, v := range visible {
		if labels == maxLabels {
			break
		}
		if v.label != "" && g.placeLabel(v.col, v.row, v.label) {
			labels++
		}
	}
}

// placeLabel writes a label, with one blank cell as a gap, to the right of a
// mark, or to its left when the right side runs off the grid. It gives up
// when any of those cells already holds a mark or another label.
func (g *Globe) placeLabel(col, row int, label string) bool {
	text := []rune(label)
	span, start := append([]rune{' '}, text...), col+1
	if start+len(span) > g.cols {
		span, start = append(text, ' '), col-len(text)-1
	}
	if start < 0 || start+len(span) > g.cols {
		return false
	}
	line := g.cells[row*g.cols+start : row*g.cols+start+len(span)]
	for _, c := range line {
		if c.tier >= tierMarkFar {
			return false
		}
	}
	for i, r := range span {
		line[i] = cell{r: r, tier: tierLabel}
	}
	return true
}

func markGlyph(weight float64) rune {
	switch {
	case weight >= 0.7:
		return '◉'
	case weight >= 0.35:
		return '●'
	default:
		return '•'
	}
}

// compose turns the cell grid into lines, wrapping each run of equal tier in
// its palette style, so a line costs a handful of escape sequences.
func (g *Globe) compose(p Palette) string {
	styles := [...]ansi.Style{
		tierGrid:    p.Grid,
		tierRim:     p.Rim,
		tierLand:    p.Land,
		tierLit:     p.Lit,
		tierMarkFar: p.MarkFar,
		tierMark:    p.Mark,
		tierLabel:   p.Label,
	}
	var prefix [len(styles)]string
	for tier, s := range styles {
		if len(s) > 0 {
			prefix[tier] = s.String()
		}
	}
	var b strings.Builder
	b.Grow(g.rows * (3*g.cols + 64))
	for row := range g.rows {
		if row > 0 {
			b.WriteByte('\n')
		}
		line := g.cells[row*g.cols : (row+1)*g.cols]
		for i := 0; i < len(line); {
			tier := line[i].tier
			j := i + 1
			for j < len(line) && line[j].tier == tier {
				j++
			}
			b.WriteString(prefix[tier])
			for _, c := range line[i:j] {
				b.WriteRune(c.r)
			}
			if prefix[tier] != "" {
				b.WriteString(ansi.ResetStyle)
			}
			i = j
		}
	}
	return b.String()
}

// brailleBit maps (row, col) in a 4x2 Braille dot grid to its bit value.
var brailleBit = [4][2]rune{
	{0x01, 0x08},
	{0x02, 0x10},
	{0x04, 0x20},
	{0x40, 0x80},
}
