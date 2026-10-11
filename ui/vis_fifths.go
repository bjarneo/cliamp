package ui

import (
	"math"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// Fifths puts every note of every octave on the circle of fifths, after the
// classic view of audioforma. A note's direction from the centre is its place
// on the circle, so neighbours a fifth apart sit side by side, and how loud
// it is moves its dot outwards and makes it bigger. The octaves are rings
// stacked into a cylinder seen from above and in front, as if it stood on a
// desk, with no outline drawn round it: the lowest octave at the bottom, C at the back, and the far half
// of every ring drawn faint so the near side reads in front of it. It turns
// slowly while music plays.
//
// The bands of Analyze are not aligned to notes, so the mode reads raw
// samples and runs its own FFT over the largest window the tap holds,
// binned by semitone. Bass peaks are placed between bins, so a single bass
// note lands on the right semitone; two bass notes a few hertz apart still
// share one main lobe and blur together.
//
// The colours are the terminal's own ANSI hues, which an Omarchy theme sets:
// each hue covers two neighbouring fifths, the first in its normal shade and
// the second in its bright one, so the sharp side runs red to green and the
// flat side cyan to magenta.

const (
	fifthsFFTSize = 4096
	fifthsOctaves = 10
	fifthsNotes   = 12 * fifthsOctaves
	// C0 in equal temperament at A4 = 440 Hz.
	fifthsC0 = 16.351597831287414
	// Treble tilt in dB per octave from 1 kHz, so the falling slope of most
	// records does not leave the top octaves empty.
	fifthsTiltDB = 3.0
	// The loudness span shown, in dB below the auto-gain reference.
	fifthsRangeDB = 42.0
	// The auto-gain reference falls this fast, in dB per second, and never
	// below fifthsAGCFloorDB, so quiet passages fill out without lifting hiss
	// in silence to full size.
	fifthsAGCFallDB  = 4.0
	fifthsAGCFloorDB = -30.0
	// Easing rates per second towards a new level: fast attack, slow decay.
	fifthsRise = 28.0
	fifthsFall = 6.0
	// Levels under this are not drawn.
	fifthsGate = 0.03
	// Quiet notes sit on a ring this fraction of the radius out, not on the
	// centre, so their direction stays readable.
	fifthsInnerRadius = 0.18
	// The cylinder at its tallest: ring depth and stack height as fractions
	// of the ring radius. A short panel squashes both by up to
	// fifthsMinSquash, which reads as looking from nearer eye level.
	fifthsTilt       = 0.55
	fifthsStackRatio = 0.85
	fifthsMinSquash  = 0.4
	// Near dots grow by up to this fraction and far dots shrink by it.
	fifthsPerspective = 0.2
	// The loudest dot's radius as a fraction of the ring radius, and never
	// under fifthsMinDot dots.
	fifthsDotRatio = 0.10
	fifthsMinDot   = 2.0
	// The cylinder turns once every this many seconds while music plays.
	fifthsTurnSeconds = 90.0
	// Where a note is narrower than a bin, what the spectrum reads at its
	// centre counts for this much next to a peak that lands on it.
	fifthsLeak    = 0.1
	fifthsEpsilon = 0.01
)

// fifthsOrder is the pitch class at each step clockwise from the top: C, G, D...
var fifthsOrder = [12]int{0, 7, 2, 9, 4, 11, 6, 1, 8, 3, 10, 5}

// fifthsStep is the inverse of fifthsOrder: the step of each pitch class.
var fifthsStep [12]int

var fifthsLabels = [12]string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}

// fifthsHue is the ANSI hue of each pair of fifths: red, yellow, green, cyan,
// blue and magenta, the order they take round a colour wheel.
var fifthsHue = [6]int{1, 3, 2, 6, 4, 5}

// Styles: 0 is unstyled, 1+pc is the colour of pitch class pc, and 13+pc the
// same colour faint, for the far half of a ring.
const (
	fifthsStyleNone = 0
	fifthsStylePC   = 1
	fifthsStyleFar  = fifthsStylePC + 12
)

var fifthsANSI [fifthsStyleFar + 12]styleANSI

func init() {
	for step, pc := range fifthsOrder {
		fifthsStep[pc] = step
		ansi := fifthsHue[step/2]
		if step%2 == 1 {
			ansi += 8
		}
		fifthsANSI[fifthsStylePC+pc] = foregroundANSI(lipgloss.ANSIColor(ansi))
		fifthsANSI[fifthsStyleFar+pc] = colorANSI(lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(ansi)).Faint(true))
	}
}

type fifthsDriver struct {
	level  [fifthsNotes]float64 // eased levels, 0-1, as drawn
	target [fifthsNotes]float64 // levels of the last analysis
	dB     [fifthsNotes]float64 // scratch for one analysis
	peak   [fifthsNotes]float64 // scratch: power of the bass peaks per note
	agc    float64              // auto-gain reference in dBFS
	turn   float64              // how far the cylinder has turned, radians
	// Per-frame raster, one entry per Braille dot: how near the viewer the
	// nearest thing on the dot is, and the style it is drawn in.
	dotZ     []float32
	dotStyle []int8

	lastTick   time.Time
	analyzedAt time.Time
}

func newFifthsDriver() visModeDriver {
	return &fifthsDriver{agc: fifthsAGCFloorDB}
}

func (*fifthsDriver) AnalysisSpec(*Visualizer) VisAnalysisSpec {
	return VisAnalysisSpec{FFTSize: fifthsFFTSize}
}

func (d *fifthsDriver) OnEnter(*Visualizer) {
	dotZ, dotStyle := d.dotZ, d.dotStyle
	*d = fifthsDriver{agc: fifthsAGCFloorDB, dotZ: dotZ, dotStyle: dotStyle}
}

func (*fifthsDriver) OnLeave(*Visualizer) {}

func (d *fifthsDriver) TickInterval(_ *Visualizer, ctx VisTickContext) time.Duration {
	if ctx.OverlayActive {
		return TickSlow
	}
	if ctx.Playing || !d.pauseSettled() {
		return TickAnim
	}
	return TickSlow
}

func (d *fifthsDriver) pauseSettled() bool {
	for _, l := range d.level {
		if l >= fifthsEpsilon {
			return false
		}
	}
	return true
}

func (d *fifthsDriver) Tick(v *Visualizer, ctx VisTickContext) {
	if ctx.OverlayActive {
		d.lastTick = time.Time{}
		d.analyzedAt = time.Time{}
		return
	}
	due := d.analyzedAt.IsZero() || ctx.Now.IsZero() || ctx.Now.Sub(d.analyzedAt) >= TickAnalyze
	if due {
		var samples []float64
		if ctx.Analyze != nil && ctx.Playing {
			// A raw-sample spec only fills v.waveBuf with the audible window.
			ctx.Analyze(d.AnalysisSpec(v))
			samples = v.waveBuf
		}
		dt := TickAnalyze.Seconds()
		if !d.analyzedAt.IsZero() && !ctx.Now.IsZero() {
			dt = min(ctx.Now.Sub(d.analyzedAt).Seconds(), 1)
		}
		d.analyze(v, samples, dt)
		d.analyzedAt = ctx.Now
	}

	dt := clampFrameDT(ctx.Now, d.lastTick, TickAnim).Seconds()
	d.lastTick = ctx.Now
	if ctx.Playing {
		d.turn = math.Mod(d.turn+dt*2*math.Pi/fifthsTurnSeconds, 2*math.Pi)
	}
	for i := range d.level {
		d.level[i] = easeToward(d.level[i], d.target[i], fifthsRise, fifthsFall, dt)
	}
}

// fifthsNoteHz is the frequency of note n, counted in semitones from C0.
func fifthsNoteHz(n int) float64 {
	return fifthsC0 * math.Pow(2, float64(n)/12)
}

// analyze sets the target level of every note from samples. No samples, or
// silence, sends every target to zero.
func (d *fifthsDriver) analyze(v *Visualizer, samples []float64, dt float64) {
	silent := true
	for _, s := range samples {
		if math.Abs(s) >= 1e-5 {
			silent = false
			break
		}
	}
	if silent || v.sr <= 0 {
		clear(d.target[:])
		return
	}

	n := fifthsFFTSize
	cbuf := v.fftComplexBuffer(n)
	window := v.hannWindow(n)
	// The newest samples sit at the end; a short read is zero-padded at the front.
	have := min(len(samples), n)
	offset := n - have
	for i := range offset {
		cbuf[i] = 0
	}
	for i, s := range samples[len(samples)-have:] {
		cbuf[offset+i] = complex(s*window[offset+i], 0)
	}
	fftInPlace(cbuf, v.fftTwiddles(n))

	half := n / 2
	powers := v.fftBuffer(n)[:half]
	powers[0] = 0
	for i := 1; i < half; i++ {
		re, im := real(cbuf[i]), imag(cbuf[i])
		powers[i] = re*re + im*im
	}

	// A full-scale sine through a Hann window peaks at N/4.
	ref := float64(n*n) / 16
	binHz := v.sr / float64(n)
	semitone := math.Pow(2, 1.0/24) - 1
	d.bassPeaks(powers, binHz, semitone)
	peak := math.Inf(-1)
	for note := range fifthsNotes {
		f := fifthsNoteHz(note)
		c := f / binHz
		if c >= float64(half-1) {
			d.dB[note] = math.Inf(-1)
			continue
		}
		var p float64
		if w := c * semitone; w < 1 {
			p = max(d.peak[note], fifthsLeak*sampleBandLinear(powers, c))
		} else {
			p = fifthsNotePower(powers, c, w)
		}
		db := math.Inf(-1)
		if p > 0 {
			db = 10*math.Log10(p/ref) + fifthsTiltDB*math.Log2(f/1000)
		}
		d.dB[note] = db
		peak = max(peak, db)
	}

	d.agc = max(fifthsAGCFloorDB, d.agc-fifthsAGCFallDB*dt, peak)
	floor := d.agc - fifthsRangeDB
	for note, db := range d.dB {
		level := 0.0
		if !math.IsInf(db, -1) {
			level = max(0, min(1, (db-floor)/fifthsRangeDB))
		}
		if level < fifthsGate {
			level = 0
		}
		d.target[note] = level
	}
}

// bassPeaks files each spectral peak below the point where a semitone is a
// bin wide under the note nearest its true frequency. Bass notes a semitone
// apart share the main lobe of the window there, so the spectrum at a note's
// centre barely tells it from its neighbours; the peak, placed between bins
// by a parabola through the log power, does.
func (d *fifthsDriver) bassPeaks(powers []float64, binHz, semitone float64) {
	clear(d.peak[:])
	// Bins up to where a semitone is a bin wide, plus one for the parabola.
	top := min(len(powers)-2, int(math.Ceil(1/semitone))+1)
	for k := 2; k <= top; k++ {
		p := powers[k]
		if p <= 0 || p <= powers[k-1] || p < powers[k+1] || powers[k-1] <= 0 || powers[k+1] <= 0 {
			continue
		}
		a, b, c := math.Log(powers[k-1]), math.Log(p), math.Log(powers[k+1])
		den := a - 2*b + c
		delta := 0.0
		if den < 0 {
			delta = max(-0.5, min(0.5, 0.5*(a-c)/den))
		}
		f := (float64(k) + delta) * binHz
		note := int(math.Round(12 * math.Log2(f/fifthsC0)))
		if note < 0 || note >= fifthsNotes {
			continue
		}
		d.peak[note] = max(d.peak[note], math.Exp(b-0.25*(a-c)*delta))
	}
}

// fifthsNotePower is the power of the note centred on bin c, which owns the
// bins within w of it (half a semitone either side), w at least one bin. Bins
// count less the further they are off pitch, as in audioforma. The sum
// is capped at two bins' worth, which holds the whole main lobe of a Hann
// window, so a tone keeps its energy but broadband noise does not swell the
// wide treble notes.
func fifthsNotePower(powers []float64, c, w float64) float64 {
	lo := max(1, int(math.Ceil(c-w)))
	hi := min(len(powers)-1, int(math.Floor(c+w)))
	var sum, weight float64
	for k := lo; k <= hi; k++ {
		wt := 1 - math.Abs(float64(k)-c)/w
		sum += powers[k] * wt
		weight += wt
	}
	return sum / max(1, weight/2)
}

// fifthsCylinder is the stack of octave rings, in dots. Ring oct is centred
// on (cx, baseY - oct*step), rx across and ry deep.
type fifthsCylinder struct {
	cx, baseY float64
	rx, ry    float64
	step      float64
}

// at projects a point of ring oct at clockwise angle a from the back and
// distance r from the axis. z grows towards the viewer.
func (c fifthsCylinder) at(oct int, a, r float64) (x, y, z float64) {
	depth := r * math.Cos(a) // positive at the back
	return c.cx + r*math.Sin(a), c.baseY - float64(oct)*c.step - depth*c.ry/c.rx, -depth
}

// fifthsLayout fits the cylinder to the panel. labels reports whether there
// is room for note names round its base. ok is false when nothing fits.
func fifthsLayout(dotRows, dotCols int) (c fifthsCylinder, labels, ok bool) {
	// A label is a cell tall and up to two cells wide: four dots by four.
	labels = dotRows >= 64 && dotCols >= 60
	marginY, marginX := 1.0, 1.0
	if labels {
		marginY, marginX = 6, 7
	}
	availH := float64(dotRows) - 2*marginY
	availW := float64(dotCols)/2 - marginX
	shape := fifthsStackRatio + 2*fifthsTilt
	c.rx = min(availW, availH/(fifthsMinSquash*shape))
	if c.rx < 2 || availH < 2 {
		return c, false, false
	}
	squash := min(1, availH/(c.rx*shape))
	c.ry = fifthsTilt * squash * c.rx
	stack := fifthsStackRatio * squash * c.rx
	c.step = stack / (fifthsOctaves - 1)
	top := marginY + (availH-stack-2*c.ry)/2
	c.baseY = top + c.ry + stack - 0.5
	c.cx = float64(dotCols)/2 - 0.5
	return c, labels, true
}

// fifthsAngle is the clockwise angle from the back, in radians, of pitch
// class pc once the cylinder has turned by turn.
func fifthsAngle(pc int, turn float64) float64 {
	return float64(fifthsStep[pc])*math.Pi/6 + turn
}

func (d *fifthsDriver) Render(v *Visualizer) string {
	rows, cols := v.Rows, v.columns()
	if rows <= 0 || cols <= 0 {
		return ""
	}
	dotRows, dotCols := rows*4, cols*2
	size := dotRows * dotCols
	if cap(d.dotZ) < size {
		d.dotZ = make([]float32, size)
		d.dotStyle = make([]int8, size)
	}
	d.dotZ, d.dotStyle = d.dotZ[:size], d.dotStyle[:size]
	clear(d.dotStyle)

	// plot keeps whatever is nearest the viewer on each dot.
	plot := func(x, y, z float64, style int8) {
		xi, yi := int(math.Round(x)), int(math.Round(y))
		if xi < 0 || yi < 0 || xi >= dotCols || yi >= dotRows {
			return
		}
		i := yi*dotCols + xi
		if d.dotStyle[i] == fifthsStyleNone || float32(z) > d.dotZ[i] {
			d.dotZ[i], d.dotStyle[i] = float32(z), style
		}
	}

	c, labels, ok := fifthsLayout(dotRows, dotCols)
	if ok {
		inner := c.rx * fifthsInnerRadius
		// The biggest dot grows to a dot or two even in a small panel, and
		// the loudest note sits that far inside the outermost ring.
		sizeMax := max(fifthsMinDot, c.rx*fifthsDotRatio)
		if c.rx < 6 {
			sizeMax = 0
		}
		outer := c.rx - sizeMax*(1+fifthsPerspective)
		for oct := range fifthsOctaves {
			for pc := range 12 {
				level := d.level[oct*12+pc]
				if level < fifthsGate {
					continue
				}
				a := fifthsAngle(pc, d.turn)
				x, y, z := c.at(oct, a, inner+(outer-inner)*level)
				near := -math.Cos(a) // 1 at the front, -1 at the back
				s := level * level * sizeMax * (1 + fifthsPerspective*near)
				style := int8(fifthsStylePC + pc)
				if near < 0 {
					style = int8(fifthsStyleFar + pc)
				}
				reach := int(math.Ceil(s))
				for dy := -reach; dy <= reach; dy++ {
					for dx := -reach; dx <= reach; dx++ {
						if float64(dx*dx+dy*dy) <= s*s+0.25 {
							// A dot is a ball: its middle is nearer than its edge.
							plot(x+float64(dx), y+float64(dy), z+s-math.Hypot(float64(dx), float64(dy)), style)
						}
					}
				}
			}
		}
	}

	// Pack the dots into Braille cells. A cell wears the style of its
	// nearest dot.
	glyphs := make([]rune, rows*cols)
	styles := make([]int8, rows*cols)
	for row := range rows {
		for col := range cols {
			glyph := rune(0x2800)
			var best float32
			style := int8(fifthsStyleNone)
			for dr := range 4 {
				for dc := range 2 {
					i := (row*4+dr)*dotCols + col*2 + dc
					if d.dotStyle[i] == fifthsStyleNone {
						continue
					}
					glyph |= brailleBit[dr][dc]
					if style == fifthsStyleNone || d.dotZ[i] > best {
						best, style = d.dotZ[i], d.dotStyle[i]
					}
				}
			}
			glyphs[row*cols+col], styles[row*cols+col] = glyph, style
		}
	}

	if labels {
		// Note names round the cylinder: the near half on the desk round the
		// base, the far half above the top ring, where the stack would hide
		// them. A name gives way, whole, to a note.
		top := c.baseY - float64(fifthsOctaves-1)*c.step
		for pc := range 12 {
			a := fifthsAngle(pc, d.turn)
			ringY := c.baseY
			if math.Cos(a) > 0.01 {
				ringY = top
			}
			x := (c.cx + (c.rx+5)*math.Sin(a)) / 2
			y := (ringY - (c.ry+4)*math.Cos(a)) / 4
			label := fifthsLabels[pc]
			row := int(math.Round(y))
			col := int(math.Round(x - float64(len(label)-1)/2))
			if row < 0 || row >= rows {
				continue
			}
			if col < 0 || col+len(label) > cols {
				continue
			}
			cells := glyphs[row*cols+col : row*cols+col+len(label)]
			blocked := false
			for i := range cells {
				blocked = blocked || styles[row*cols+col+i] >= fifthsStylePC
			}
			if blocked {
				continue
			}
			for i, ch := range label {
				cells[i], styles[row*cols+col+i] = ch, int8(fifthsStylePC+pc)
			}
		}
	}

	lines := make([]string, rows)
	var sb, run strings.Builder
	for row := range rows {
		sb.Reset()
		cur := int8(-1)
		for col := range cols {
			i := row*cols + col
			style := styles[i]
			// A blank cell paints nothing, so it joins whatever run is open.
			if glyphs[i] != 0x2800 && style != cur {
				if cur >= 0 {
					writeStyledRun(&sb, &run, fifthsANSI[cur])
				} else {
					sb.WriteString(run.String())
					run.Reset()
				}
				cur = style
			}
			run.WriteRune(glyphs[i])
		}
		if cur >= 0 {
			writeStyledRun(&sb, &run, fifthsANSI[cur])
		} else {
			sb.WriteString(run.String())
			run.Reset()
		}
		lines[row] = sb.String()
	}
	return strings.Join(lines, "\n")
}
