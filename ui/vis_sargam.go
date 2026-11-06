package ui

import (
	"math"
	"strings"
	"time"
)

// Sargam draws the melody itself instead of the spectrum: a playhead sweeps
// left to right, leaving a Braille contour whose height is the detected pitch.
// The vertical axis is a swara ladder spanning two octaves, Sa. (C3) at the
// bottom to Sa' (C5) at the top, so high notes climb and low notes fall. The
// labels are Roman transliterations, not Devanagari.
const (
	sargamFFTSize = 4096
	// Two octaves, C3..C5. Most vocal melodies sit inside this range, and the
	// span is narrow enough that a semitone still lands on its own dot row in
	// the default panel.
	sargamLowMidi  = 48.0
	sargamHighMidi = 72.0
	// One character column per sargamColumnSeconds. A 48-column panel sweeps
	// in about 12 seconds, a 140-column one in about 35.
	sargamColumnSeconds = 0.25
	// Pitch analysis cadence. The render cadence is faster; the note does not
	// change meaningfully between these samples.
	sargamPitchInterval = 66 * time.Millisecond
	// A new note must persist this long before it replaces the current one,
	// so a wobbling detector does not repaint the contour every sample.
	sargamNoteHold = 140 * time.Millisecond
	// Detection range. Notes outside the drawn ladder still count as pitches;
	// they simply clamp to the edge of the panel.
	sargamPitchMinHz = 55.0
	sargamPitchMaxHz = 1600.0
	// Candidate notes for the harmonic-sum detector.
	sargamNoteLo         = 33
	sargamNoteHi         = 88
	sargamCandidateCount = sargamNoteHi - sargamNoteLo + 1
	// The winning harmonic sum must beat the average candidate by this factor
	// and reach this fraction of the loudest FFT bin.
	sargamConfidence = 1.5
	sargamPeakFloor  = 0.08
	// Below this RMS the frame is silence, not a pitch.
	sargamSilenceRMS = 1e-4
)

// sargamHarmonicWeights favour the fundamental over the upper partials, so a
// strong second harmonic does not pull the detector an octave up.
var sargamHarmonicWeights = [5]float64{1, 0.8, 0.6, 0.45, 0.3}

// sargamNames is the twelve-tone swara ladder. Lowercase names are komal,
// "ma" is tivra Ma. The axis uses these only for labelled rows.
var sargamNames = [12]string{
	"Sa", "re", "Re", "ga", "Ga", "Ma",
	"ma", "Pa", "dha", "Dha", "ni", "Ni",
}

type sargamDriver struct {
	// Pitch state, in MIDI note numbers. note is smoothed across small drifts
	// so the contour glides instead of flickering between neighbours.
	note        float64
	noteOK      bool
	candidate   float64
	candidateN  int
	lastPitchAt time.Time
	holdUntil   time.Time
	// Playhead and the per-dot-column trace. track holds a MIDI note per dot
	// column, or 0 for a rest. filled is the next column to write.
	lastTick time.Time
	pos      float64
	filled   int
	track    []int16
	lastY    int
	// FFT scratch.
	mags   []float64
	scores [sargamCandidateCount]float64
}

func newSargamDriver() visModeDriver {
	return &sargamDriver{}
}

func (*sargamDriver) AnalysisSpec(*Visualizer) VisAnalysisSpec {
	return VisAnalysisSpec{FFTSize: sargamFFTSize}
}

func (*sargamDriver) TickInterval(_ *Visualizer, ctx VisTickContext) time.Duration {
	if ctx.OverlayActive {
		return TickSlow
	}
	if ctx.Playing {
		return TickFast
	}
	return TickSlow
}

func (d *sargamDriver) OnEnter(*Visualizer) {
	*d = sargamDriver{lastY: -1}
}

func (*sargamDriver) OnLeave(*Visualizer) {}

// sargamFlush colors the Sargam grid by tier: 1 is the dim swara guide, 2 to 4
// are the low, mid and high spectrum colors. The contour starts at tier 2, so
// even its lowest notes stay distinct from the guides.
func sargamFlush(sb, run *strings.Builder, tier int) {
	if tier < 1 || tier > 4 {
		writeStyledRun(sb, run, styleANSI{})
		return
	}
	if tier == 1 {
		writeStyledRun(sb, run, dimANSI)
		return
	}
	writeStyledRun(sb, run, specANSI[tier-2])
}

// sargamGutter is the width of the note-label margin, including its trailing
// space. Narrow panels drop the labels rather than crowd the contour.
func sargamGutter(cols int) int {
	switch {
	case cols >= 24:
		return 5
	case cols >= 12:
		return 4
	default:
		return 0
	}
}

// sargamPlotCols is the character width left for the contour once the label
// margin is taken out.
func sargamPlotCols(cols int) int {
	gutter := sargamGutter(cols)
	if gutter >= cols {
		return cols
	}
	return cols - gutter
}

// sargamY maps a MIDI note to a dot row, with the low note at the bottom.
func sargamY(midi float64, dotRows int) int {
	if dotRows <= 1 {
		return 0
	}
	t := (midi - sargamLowMidi) / (sargamHighMidi - sargamLowMidi)
	t = max(0, min(1, t))
	return int(math.Round((1 - t) * float64(dotRows-1)))
}

// sargamLabel names a MIDI note on the swara ladder, with MIDI 60 (C4) as Sa.
// A dot marks the lower octave and an apostrophe the upper one, as in the
// reference notation.
func sargamLabel(midi int) string {
	degree := ((midi-60)%12 + 12) % 12
	diff := midi - 60
	octave := diff / 12
	if diff < 0 && diff%12 != 0 {
		octave--
	}
	name := sargamNames[degree]
	switch {
	case octave > 0:
		return name + strings.Repeat("'", octave)
	case octave < 0:
		return name + strings.Repeat(".", -octave)
	}
	return name
}

// sargamAxisLabels fills one label per character row. Notes are tried in
// priority order and a row keeps only its first label, so a short panel shows
// the important ones (Sa in the middle, then the octave edges, then Pa).
func sargamAxisLabels(dotRows, rows, maxWidth int) []string {
	labels := make([]string, rows)
	if maxWidth <= 0 || dotRows <= 0 {
		return labels
	}
	for _, note := range [...]int{60, 72, 48, 67, 55} {
		label := sargamLabel(note)
		if len(label) > maxWidth {
			continue
		}
		row := sargamY(float64(note), dotRows) / 4
		if row < 0 || row >= rows || labels[row] != "" {
			continue
		}
		labels[row] = label
	}
	return labels
}

func (d *sargamDriver) resize(dotCols int) {
	if dotCols <= 0 || len(d.track) == dotCols {
		return
	}
	d.track = make([]int16, dotCols)
	d.pos = 0
	d.filled = 0
	d.lastY = -1
}

// writeColumns stamps the current note into every dot column the playhead has
// crossed since the last write. A rest writes zero, which breaks the line.
func (d *sargamDriver) writeColumns(upTo int) {
	if len(d.track) == 0 {
		return
	}
	upTo = max(0, min(upTo, len(d.track)-1))
	var note int16
	if d.noteOK {
		note = int16(int(math.Round(d.note)))
	}
	for x := d.filled; x <= upTo; x++ {
		d.track[x] = note
	}
	d.filled = max(d.filled, upTo+1)
}

func (d *sargamDriver) Tick(v *Visualizer, ctx VisTickContext) {
	if ctx.OverlayActive {
		d.lastTick = time.Time{}
		d.lastPitchAt = time.Time{}
		return
	}

	dotCols := sargamPlotCols(v.columns()) * 2
	if dotCols <= 0 {
		return
	}
	d.resize(dotCols)

	if !ctx.Playing {
		// Freeze the trace while paused or stopped. Clearing the clocks keeps
		// the playhead from jumping forward on resume.
		d.lastTick = time.Time{}
		return
	}

	now := ctx.Now
	if !now.IsZero() && (d.lastPitchAt.IsZero() || now.Sub(d.lastPitchAt) >= sargamPitchInterval) {
		if ctx.Analyze != nil {
			ctx.Analyze(d.AnalysisSpec(v))
		}
		d.analyse(v, now)
		d.lastPitchAt = now
	}

	if now.IsZero() {
		return
	}
	if d.lastTick.IsZero() {
		d.lastTick = now
		d.writeColumns(int(d.pos))
		return
	}

	dt := clampFrameDT(now, d.lastTick, TickFast)
	d.lastTick = now
	d.pos += dt.Seconds() / sargamColumnSeconds * 2
	if d.pos >= float64(len(d.track)) {
		clear(d.track)
		d.filled = 0
		d.pos = math.Mod(d.pos, float64(len(d.track)))
	}
	d.writeColumns(int(d.pos))
}

// analyse runs the detector and applies note hysteresis: a small drift smooths
// the current note, while a real leap has to repeat before the contour moves.
func (d *sargamDriver) analyse(v *Visualizer, now time.Time) {
	midi, ok := d.detect(v, v.waveBuf)
	if ok {
		switch {
		case !d.noteOK:
			d.note = midi
			d.noteOK = true
			d.candidateN = 0
		case math.Abs(midi-d.note) <= 0.65:
			d.note = d.note*0.55 + midi*0.45
			d.candidateN = 0
		default:
			if d.candidateN > 0 && math.Abs(midi-d.candidate) <= 0.65 {
				d.candidateN++
			} else {
				d.candidate = midi
				d.candidateN = 1
			}
			if d.candidateN >= 2 {
				d.note = midi
				d.candidateN = 0
			}
		}
		d.holdUntil = now.Add(sargamNoteHold)
		return
	}
	if !d.holdUntil.IsZero() && now.After(d.holdUntil) {
		d.noteOK = false
		d.candidateN = 0
	}
}

// detect returns the fundamental as a fractional MIDI note. It scores every
// candidate note by the weighted magnitude of its first five harmonics, then
// refines the winner with a parabola across the neighbouring candidates.
func (d *sargamDriver) detect(v *Visualizer, samples []float64) (float64, bool) {
	if v == nil || v.sr <= 0 || len(samples) < 1024 {
		return 0, false
	}
	size := sargamFFTSize
	if len(samples) < size {
		size = 1
		for size*2 <= len(samples) {
			size <<= 1
		}
	}
	if size < 1024 {
		return 0, false
	}

	buf := v.fftComplexBuffer(size)
	window := v.hannWindow(size)
	offset := len(samples) - size
	var energy float64
	for i := range size {
		s := samples[offset+i]
		energy += s * s
		buf[i] = complex(s*window[i], 0)
	}
	if math.Sqrt(energy/float64(size)) < sargamSilenceRMS {
		return 0, false
	}

	fftInPlace(buf, v.fftTwiddles(size))

	half := size / 2
	if cap(d.mags) < half {
		d.mags = make([]float64, half)
	} else {
		d.mags = d.mags[:half]
	}
	d.mags[0] = 0
	maxMag := 0.0
	for i := 1; i < half; i++ {
		re, im := real(buf[i]), imag(buf[i])
		mag := math.Hypot(re, im)
		d.mags[i] = mag
		maxMag = max(maxMag, mag)
	}
	if maxMag <= 0 {
		return 0, false
	}

	binHz := v.sr / float64(size)
	nyquistBin := float64(half - 1)
	sumScore := 0.0
	bestScore := 0.0
	bestNote := 0
	for i := range sargamCandidateCount {
		note := sargamNoteLo + i
		freq := 440 * math.Pow(2, float64(note-69)/12)
		if freq < sargamPitchMinHz || freq > sargamPitchMaxHz {
			d.scores[i] = 0
			continue
		}
		score := 0.0
		for h := 1; h <= len(sargamHarmonicWeights); h++ {
			pos := freq * float64(h) / binHz
			if pos >= nyquistBin {
				break
			}
			score += sargamHarmonicWeights[h-1] * sargamMagAt(d.mags, pos)
		}
		d.scores[i] = score
		sumScore += score
		if score > bestScore {
			bestScore = score
			bestNote = note
		}
	}
	if bestScore <= 0 {
		return 0, false
	}
	if bestScore < sargamPeakFloor*maxMag ||
		bestScore < sargamConfidence*sumScore/float64(sargamCandidateCount) {
		return 0, false
	}

	midi := float64(bestNote)
	idx := bestNote - sargamNoteLo
	if idx > 0 && idx < sargamCandidateCount-1 {
		y0, y1, y2 := d.scores[idx-1], d.scores[idx], d.scores[idx+1]
		if den := y0 - 2*y1 + y2; math.Abs(den) > 1e-9 {
			delta := max(-0.5, min(0.5, 0.5*(y0-y2)/den))
			midi += delta
		}
	}
	return midi, true
}

// sargamMagAt linearly interpolates the magnitude spectrum at a fractional bin.
func sargamMagAt(mags []float64, pos float64) float64 {
	if pos <= 0 {
		return 0
	}
	i := int(pos)
	if i >= len(mags)-1 {
		return 0
	}
	frac := pos - float64(i)
	return mags[i]*(1-frac) + mags[i+1]*frac
}

func (d *sargamDriver) Render(v *Visualizer) string {
	rows, cols := v.Rows, v.columns()
	if rows <= 0 || cols <= 0 {
		return strings.Repeat("\n", max(0, rows-1))
	}

	gutter := sargamGutter(cols)
	plotCols := cols - gutter
	if plotCols <= 0 {
		gutter, plotCols = 0, cols
	}
	dotRows := rows * 4
	dotCols := plotCols * 2
	d.resize(dotCols)

	grid := &v.dotGrid
	grid.ensure(dotRows, dotCols)
	grid.flush = sargamFlush
	defer func() { grid.flush = nil }()

	// Dim swara guides every three semitones. The contour uses tiers 2 to 4,
	// so it never blends into a guide, even on its lowest notes.
	if dotRows >= 12 {
		for note := int(sargamLowMidi); note <= int(sargamHighMidi); note += 3 {
			y := sargamY(float64(note), dotRows)
			for x := 0; x < dotCols; x += 4 {
				grid.set(x, y, 1)
			}
		}
	}

	// The melody trace. Each column holds a quantised note, so a pitch change
	// becomes the vertical step of the reference notation.
	cur := max(0, min(int(d.pos), dotCols-1))
	prevY := -1
	for x := 0; x <= cur && x < len(d.track); x++ {
		note := int(d.track[x])
		if note == 0 {
			prevY = -1
			continue
		}
		y := sargamY(float64(note), dotRows)
		tier := int8(2 + specTag((float64(note)-sargamLowMidi)/(sargamHighMidi-sargamLowMidi)))
		grid.set(x, y, tier)
		if prevY >= 0 {
			for yy := min(prevY, y); yy <= max(prevY, y); yy++ {
				grid.set(x, yy, tier)
			}
		}
		prevY = y
	}

	// The playhead dot. It keeps sweeping through rests, dimmed and parked at
	// the last pitch so the eye can still follow the time axis.
	dotY := dotRows / 2
	switch {
	case d.noteOK:
		dotY = sargamY(d.note, dotRows)
	case d.lastY >= 0:
		dotY = d.lastY
	}
	d.lastY = dotY
	dotTier := int8(4)
	if !d.noteOK {
		dotTier = 1
	}
	for _, off := range [][2]int{{0, 0}, {-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
		grid.set(cur+off[0], dotY+off[1], dotTier)
	}
	// Connect the dot to the last written note when the pitch changed between
	// two dot columns.
	if d.noteOK && cur < len(d.track) && d.track[cur] != 0 {
		y := sargamY(float64(d.track[cur]), dotRows)
		for yy := min(y, dotY); yy <= max(y, dotY); yy++ {
			grid.set(cur, yy, dotTier)
		}
	}

	lines := strings.Split(grid.render(rows, plotCols), "\n")
	if gutter > 0 {
		labels := sargamAxisLabels(dotRows, rows, gutter-1)
		for i := range lines {
			if i >= len(labels) || labels[i] == "" {
				lines[i] = strings.Repeat(" ", gutter) + lines[i]
				continue
			}
			label := labels[i]
			pad := max(0, gutter-1-len(label))
			lines[i] = dimANSI.prefix + strings.Repeat(" ", pad) + label + dimANSI.suffix +
				" " + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}
