package ui

import (
	"math"
	"math/cmplx"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/madelynnblue/go-dsp/fft"
)

const (
	numBands       = 10
	fftSize        = 2048
	defaultVisRows = 5
)

// VisMode selects the visualizer rendering style.
type VisMode int

const (
	VisBars    VisMode = iota // smooth fractional blocks
	VisBricks                 // solid bricks with gaps
	VisColumns                // many thin columns
	VisWave                   // braille waveform oscilloscope
	VisScatter                // braille particle sparkle
	VisFlame                  // braille rising flame tendrils
	VisRetro                  // 80s synthwave perspective grid with wave
	VisNone                   // hidden — no visualizer
	visCount                  // sentinel for cycling
)

// Unicode block elements for bar height (9 levels including space)
var barBlocks = []string{" ", "▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}

// brailleBit maps (row, col) in a 4×2 Braille dot grid to its bit value.
var brailleBit = [4][2]rune{
	{0x01, 0x08}, // row 0
	{0x02, 0x10}, // row 1
	{0x04, 0x20}, // row 2
	{0x40, 0x80}, // row 3
}

// visBandWidth returns the character width for band b so that all 10 bands
// plus 1-char gaps exactly fill panelWidth (74). The remainder is distributed
// across the first few bands (7 chars) while the rest get 6.
func visBandWidth(b int) int {
	const gap = 1
	base := (panelWidth - (numBands-1)*gap) / numBands
	extra := (panelWidth - (numBands-1)*gap) % numBands
	if b < extra {
		return base + 1
	}
	return base
}

// Frequency edges for 10 spectrum bands (Hz)
var bandEdges = [11]float64{20, 100, 200, 400, 800, 1600, 3200, 6400, 12800, 16000, 20000}

// Pre-built styles for spectrum bar colors to avoid per-frame allocation.
var (
	specLowStyle  = lipgloss.NewStyle().Foreground(spectrumLow)
	specMidStyle  = lipgloss.NewStyle().Foreground(spectrumMid)
	specHighStyle = lipgloss.NewStyle().Foreground(spectrumHigh)
)

// Visualizer performs FFT analysis and renders spectrum bars.
type Visualizer struct {
	prev    [numBands]float64 // previous frame for temporal smoothing
	sr      float64
	buf     []float64 // reusable FFT buffer to avoid per-frame allocation
	Mode    VisMode
	Rows    int       // display height in terminal rows (default 5)
	waveBuf []float64 // raw samples for wave mode
	frame   uint64    // frame counter for scatter animation
}

// NewVisualizer creates a Visualizer for the given sample rate.
func NewVisualizer(sampleRate float64) *Visualizer {
	return &Visualizer{
		sr:   sampleRate,
		buf:  make([]float64, fftSize),
		Rows: defaultVisRows,
	}
}

// CycleMode advances to the next visualizer mode.
func (v *Visualizer) CycleMode() {
	v.Mode = (v.Mode + 1) % visCount
}

// ModeName returns the display name of the current mode.
func (v *Visualizer) ModeName() string {
	switch v.Mode {
	case VisBricks:
		return "Bricks"
	case VisColumns:
		return "Columns"
	case VisWave:
		return "Wave"
	case VisScatter:
		return "Scatter"
	case VisFlame:
		return "Flame"
	case VisRetro:
		return "Retro"
	case VisNone:
		return "None"
	default:
		return "Bars"
	}
}

// StringToVisMode converts a visualizer mode name (case-insensitive) to VisMode.
// Returns VisBars (default) if the name is not recognized or empty.
func StringToVisMode(name string) VisMode {
	if name == "" {
		return VisBars
	}
	switch strings.ToLower(name) {
	case "bars":
		return VisBars
	case "bricks":
		return VisBricks
	case "columns":
		return VisColumns
	case "wave":
		return VisWave
	case "scatter":
		return VisScatter
	case "flame":
		return VisFlame
	case "retro":
		return VisRetro
	case "none":
		return VisNone
	default:
		return VisBars
	}
}

// Analyze runs FFT on raw audio samples and returns 10 normalized band levels (0-1).
func (v *Visualizer) Analyze(samples []float64) [numBands]float64 {
	v.frame++

	// Store raw samples for wave mode.
	if n := len(samples); n > 0 {
		if cap(v.waveBuf) >= n {
			v.waveBuf = v.waveBuf[:n]
		} else {
			v.waveBuf = make([]float64, n)
		}
		copy(v.waveBuf, samples)
	} else {
		v.waveBuf = v.waveBuf[:0]
	}

	var bands [numBands]float64
	if len(samples) == 0 {
		// Decay previous values when no audio data
		for b := range numBands {
			bands[b] = v.prev[b] * 0.8
			v.prev[b] = bands[b]
		}
		return bands
	}

	// Zero-fill and copy into reusable buffer
	clear(v.buf)
	copy(v.buf, samples)

	// Apply Hann window to reduce spectral leakage
	for i := range fftSize {
		w := 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(fftSize-1)))
		v.buf[i] *= w
	}

	// Compute FFT
	spectrum := fft.FFTReal(v.buf)

	binHz := v.sr / float64(fftSize)

	// Sum magnitudes per frequency band
	for b := range numBands {
		loIdx := int(bandEdges[b] / binHz)
		hiIdx := int(bandEdges[b+1] / binHz)
		if loIdx < 1 {
			loIdx = 1
		}
		halfLen := len(spectrum) / 2
		if hiIdx >= halfLen {
			hiIdx = halfLen - 1
		}

		var sum float64
		count := 0
		for i := loIdx; i <= hiIdx; i++ {
			sum += cmplx.Abs(spectrum[i])
			count++
		}
		if count > 0 {
			sum /= float64(count)
		}

		// Convert to dB-like scale and normalize to 0-1
		if sum > 0 {
			bands[b] = (20*math.Log10(sum) + 10) / 50
		}
		bands[b] = max(0, min(1, bands[b]))

		// Temporal smoothing: fast attack, slow decay
		if bands[b] > v.prev[b] {
			bands[b] = bands[b]*0.6 + v.prev[b]*0.4
		} else {
			bands[b] = bands[b]*0.25 + v.prev[b]*0.75
		}
		v.prev[b] = bands[b]
	}

	return bands
}

// Render dispatches to the active visualizer mode.
func (v *Visualizer) Render(bands [numBands]float64) string {
	switch v.Mode {
	case VisBricks:
		return v.renderBricks(bands)
	case VisColumns:
		return v.renderColumns(bands)
	case VisWave:
		return v.renderWave()
	case VisScatter:
		return v.renderScatter(bands)
	case VisFlame:
		return v.renderFlame(bands)
	case VisRetro:
		return v.renderRetro(bands)
	case VisNone:
		return ""
	default:
		return v.renderBars(bands)
	}
}

// renderBars is the default smooth spectrum with fractional Unicode blocks.
func (v *Visualizer) renderBars(bands [numBands]float64) string {
	height := v.Rows

	lines := make([]string, height)

	for row := range height {
		var sb strings.Builder
		rowBottom := float64(height-1-row) / float64(height)
		rowTop := float64(height-row) / float64(height)

		for i, level := range bands {
			bw := visBandWidth(i)
			var block string
			if level >= rowTop {
				block = "█"
			} else if level > rowBottom {
				frac := (level - rowBottom) / (rowTop - rowBottom)
				idx := int(frac * float64(len(barBlocks)-1))
				idx = max(0, min(idx, len(barBlocks)-1))
				block = barBlocks[idx]
			} else {
				block = " "
			}

			style := specStyle(rowBottom)
			sb.WriteString(style.Render(strings.Repeat(block, bw)))
			if i < numBands-1 {
				sb.WriteString(" ")
			}
		}
		lines[row] = sb.String()
	}

	return strings.Join(lines, "\n")
}

// renderBricks draws solid block columns with visible gaps between rows and bands.
// Uses half-height blocks (▄) so each brick is half a terminal row, with blank
// gaps between them, keeping total height equal to the bars visualizer.
func (v *Visualizer) renderBricks(bands [numBands]float64) string {
	height := v.Rows

	lines := make([]string, height)

	for row := range height {
		var sb strings.Builder
		rowThreshold := float64(height-1-row) / float64(height)

		for i, level := range bands {
			bw := visBandWidth(i)
			style := specStyle(rowThreshold)
			if level > rowThreshold {
				sb.WriteString(style.Render(strings.Repeat("▄", bw)))
			} else {
				sb.WriteString(strings.Repeat(" ", bw))
			}
			if i < numBands-1 {
				sb.WriteString(" ")
			}
		}
		lines[row] = sb.String()
	}

	return strings.Join(lines, "\n")
}

// renderColumns draws many thin single-character-wide columns, interpolating
// between bands so adjacent columns vary slightly for a dense, organic look.
func (v *Visualizer) renderColumns(bands [numBands]float64) string {
	height := v.Rows

	// Compute per-band column counts and flat-array offsets.
	var bandCols [numBands]int
	var offsets [numBands]int
	totalCols := 0
	for b := range numBands {
		offsets[b] = totalCols
		bandCols[b] = visBandWidth(b)
		totalCols += bandCols[b]
	}

	// Build per-column levels by interpolating between neighboring bands.
	cols := make([]float64, totalCols)
	for b, level := range bands {
		nextLevel := level
		if b+1 < numBands {
			nextLevel = bands[b+1]
		}
		for c := range bandCols[b] {
			t := float64(c) / float64(bandCols[b])
			cols[offsets[b]+c] = level*(1-t) + nextLevel*t
		}
	}

	lines := make([]string, height)

	for row := range height {
		var sb strings.Builder
		rowBottom := float64(height-1-row) / float64(height)
		rowTop := float64(height-row) / float64(height)

		for b := range numBands {
			for c := range bandCols[b] {
				level := cols[offsets[b]+c]
				var block string
				if level >= rowTop {
					block = "█"
				} else if level > rowBottom {
					frac := (level - rowBottom) / (rowTop - rowBottom)
					idx := int(frac * float64(len(barBlocks)-1))
					idx = max(0, min(idx, len(barBlocks)-1))
					block = barBlocks[idx]
				} else {
					block = " "
				}
				sb.WriteString(specStyle(rowBottom).Render(block))
			}
			if b < numBands-1 {
				sb.WriteString(" ")
			}
		}
		lines[row] = sb.String()
	}

	return strings.Join(lines, "\n")
}

// renderWave draws a Braille-character oscilloscope waveform from raw audio samples.
// Each Braille character covers a 2×4 dot grid, giving smooth sub-cell resolution.
func (v *Visualizer) renderWave() string {
	height := v.Rows
	const charCols = panelWidth
	dotRows := height * 4
	dotCols := charCols * 2

	samples := v.waveBuf
	n := len(samples)

	// Downsample audio to one y-position per horizontal dot column.
	ypos := make([]int, dotCols)
	for x := range dotCols {
		var sample float64
		if n > 0 {
			idx := x * n / dotCols
			if idx >= n {
				idx = n - 1
			}
			sample = samples[idx]
		}
		// Map sample [-1, 1] to dot row [0, dotRows-1]; center is dotRows/2.
		y := int((1.0 - sample) * float64(dotRows-1) / 2.0)
		ypos[x] = max(0, min(dotRows-1, y))
	}

	lines := make([]string, height)
	for row := range height {
		var sb strings.Builder
		dotRowStart := row * 4

		for ch := range charCols {
			var braille rune = '\u2800'
			dotColStart := ch * 2

			for dc := range 2 {
				x := dotColStart + dc
				y := ypos[x]

				// Connect to previous point so the waveform is continuous.
				prevY := y
				if x > 0 {
					prevY = ypos[x-1]
				}
				yMin := min(y, prevY)
				yMax := max(y, prevY)

				for dr := range 4 {
					dotY := dotRowStart + dr
					if dotY >= yMin && dotY <= yMax {
						braille |= brailleBit[dr][dc]
					}
				}
			}

			style := specStyle(float64(height-1-row) / float64(height))
			sb.WriteString(style.Render(string(braille)))
		}
		lines[row] = sb.String()
	}

	return strings.Join(lines, "\n")
}

// renderScatter draws a twinkling particle field using Braille dots.
// Dot density per band is proportional to the squared energy level, with a
// gravity bias that makes particles denser near the bottom.
func (v *Visualizer) renderScatter(bands [numBands]float64) string {
	height := v.Rows
	dotRows := height * 4

	lines := make([]string, height)

	for row := range height {
		var sb strings.Builder

		for b := range numBands {
			charsPerBand := visBandWidth(b)
			for c := range charsPerBand {
				var braille rune = '\u2800'

				for dr := range 4 {
					for dc := range 2 {
						dotRow := row*4 + dr
						dotCol := c*2 + dc

						h := scatterHash(b, dotRow, dotCol, v.frame)

						// Gravity bias: more particles settle near the bottom.
						heightFactor := 0.5 + 0.5*float64(dotRow)/float64(dotRows-1)
						threshold := bands[b] * bands[b] * heightFactor

						if h < threshold {
							braille |= brailleBit[dr][dc]
						}
					}
				}

				rowNorm := float64(height-1-row) / float64(height)
				style := specStyle(rowNorm)
				sb.WriteString(style.Render(string(braille)))
			}
			if b < numBands-1 {
				sb.WriteString(" ")
			}
		}
		lines[row] = sb.String()
	}

	return strings.Join(lines, "\n")
}

// scatterHash returns a pseudo-random value in [0, 1) for a given dot position
// and frame. Dots persist for a few frames to create a twinkling effect.
func scatterHash(band, row, col int, frame uint64) float64 {
	// Stagger per-dot so they don't all change simultaneously.
	f := (frame + uint64(row*3+col)) / 3
	h := uint64(band)*7919 + uint64(row)*6271 + uint64(col)*3037 + f*104729
	h ^= h >> 16
	h *= 0x45d9f3b37197344b
	h ^= h >> 16
	return float64(h%10000) / 10000.0
}

// renderFlame draws rising flame tendrils using Braille dots. Each band produces
// a column of flickering fire that rises proportionally to energy, with lateral
// wobble driven by a sine-based displacement for an organic, dancing look.
func (v *Visualizer) renderFlame(bands [numBands]float64) string {
	height := v.Rows
	dotRows := height * 4

	lines := make([]string, height)

	for row := range height {
		var sb strings.Builder

		for b := range numBands {
			charsPerBand := visBandWidth(b)
			bandDotCols := charsPerBand * 2
			for c := range charsPerBand {
				var braille rune = '\u2800'

				for dr := range 4 {
					for dc := range 2 {
						dotRow := row*4 + dr
						dotCol := c*2 + dc

						// Invert: flames rise from bottom, so row 0 = top of flame.
						flameY := float64(dotRows-1-dotRow) / float64(dotRows-1)

						// Flame reaches up to flameY proportional to band level.
						if flameY > bands[b] {
							continue
						}

						// Lateral wobble: sine wave displaced by height and time.
						t := float64(v.frame) * 0.3
						wobble := math.Sin(t+flameY*6.0+float64(b)*2.1) * 1.5
						centerCol := float64(bandDotCols) / 2.0

						// Flame narrows toward the tip.
						tipNarrow := 1.0 - flameY/max(bands[b], 0.01)
						flameWidth := (0.3 + 0.7*tipNarrow) * centerCol

						dist := math.Abs(float64(dotCol) - centerCol + 0.5 - wobble) // distance from flame center
						if dist < flameWidth {
							// Add flicker at the edges using hash.
							edge := dist / flameWidth
							if edge < 0.7 || scatterHash(b, dotRow, dotCol, v.frame) < 0.6 {
								braille |= brailleBit[dr][dc]
							}
						}
					}
				}

				// Color: bottom rows (base) are red/hot, upper rows (tips) are green/cool.
				// This inverts the normal spectrum coloring for a fire gradient effect.
				rowNorm := float64(row) / float64(height)
				style := specStyle(rowNorm)
				sb.WriteString(style.Render(string(braille)))
			}
			if b < numBands-1 {
				sb.WriteString(" ")
			}
		}
		lines[row] = sb.String()
	}

	return strings.Join(lines, "\n")
}

// renderRetro draws a retro 80s synthwave scene: a striped setting sun above
// the horizon, a smooth audio-reactive wave, and a perspective grid floor that
// scrolls toward the viewer. Uses Braille characters for sub-cell resolution.
func (v *Visualizer) renderRetro(bands [numBands]float64) string {
	height := v.Rows
	const charCols = panelWidth
	dotRows := height * 4
	dotCols := charCols * 2

	// Horizon at 40% from top — gives room for wave and sun above.
	horizonDot := dotRows * 2 / 5
	if horizonDot < 2 {
		horizonDot = 2
	}
	floorRows := dotRows - horizonDot
	centerX := float64(dotCols-1) / 2.0

	// Dot types: 0=empty, 1=grid, 2=wave, 3=sun.
	grid := make([][]byte, dotRows)
	for i := range grid {
		grid[i] = make([]byte, dotCols)
	}

	// ── SUN ── striped semicircle above horizon.
	sunR := float64(horizonDot) * 0.85
	for dy := range horizonDot {
		rowDist := float64(horizonDot - dy) // dots above horizon
		if rowDist > sunR {
			continue
		}
		halfW := math.Sqrt(sunR*sunR - rowDist*rowDist)

		// Bottom half of sun has horizontal stripe gaps.
		if rowDist < sunR*0.5 {
			sw := max(1, int(sunR*0.15))
			if (int(rowDist)/sw)%2 == 1 {
				continue
			}
		}

		left := max(0, int(centerX-halfW))
		right := min(dotCols-1, int(centerX+halfW))
		for dx := left; dx <= right; dx++ {
			grid[dy][dx] = 3
		}
	}

	// ── HORIZON LINE ──
	for dx := range dotCols {
		grid[horizonDot][dx] = 1
	}

	// ── PERSPECTIVE GRID FLOOR ──

	// Vertical lines converging to vanishing point at (centerX, horizonDot).
	const numVLines = 18
	for i := range numVLines + 1 {
		bottomX := float64(i) * float64(dotCols-1) / float64(numVLines)
		for dy := horizonDot + 1; dy < dotRows; dy++ {
			t := float64(dy-horizonDot) / float64(max(1, floorRows-1))
			screenX := centerX + (bottomX-centerX)*t
			ix := int(math.Round(screenX))
			if ix >= 0 && ix < dotCols {
				grid[dy][ix] = 1
			}
		}
	}

	// Horizontal lines scrolling toward the viewer.
	scroll := math.Mod(float64(v.frame)*0.08, 1.0)
	const numHLines = 10
	for i := range numHLines {
		z := (float64(i) + scroll) / float64(numHLines)
		if z > 1.0 {
			z -= 1.0
		}
		// Quadratic perspective: dense near horizon, spread near viewer.
		dy := horizonDot + 1 + int(z*z*float64(max(1, floorRows-2)))
		if dy > horizonDot && dy < dotRows {
			for dx := range dotCols {
				grid[dy][dx] = 1
			}
		}
	}

	// ── AUDIO WAVE AT HORIZON ──
	waveY := make([]int, dotCols)
	maxWave := float64(horizonDot) * 0.85
	for dx := range dotCols {
		bandF := float64(dx) / float64(max(1, dotCols-1)) * float64(numBands-1)
		bi := int(bandF)
		frac := bandF - float64(bi)

		// Cosine interpolation for a smooth curve between bands.
		t := (1 - math.Cos(frac*math.Pi)) / 2

		var level float64
		if bi >= numBands-1 {
			level = bands[numBands-1]
		} else {
			level = bands[bi]*(1-t) + bands[bi+1]*t
		}

		// Small floor so the wave never fully vanishes.
		level = max(0.03, level)

		wy := horizonDot - int(level*maxWave)
		waveY[dx] = max(0, min(dotRows-1, wy))
	}

	// Draw wave with continuous line connections.
	for dx := range dotCols {
		y := waveY[dx]
		grid[y][dx] = 2
		if dx > 0 {
			lo, hi := min(y, waveY[dx-1]), max(y, waveY[dx-1])
			for fy := lo; fy <= hi; fy++ {
				grid[fy][dx] = 2
			}
		}
	}

	// ── RENDER BRAILLE ──
	lines := make([]string, height)
	for row := range height {
		var sb strings.Builder
		base := row * 4

		for ch := range charCols {
			var braille rune = '\u2800'
			colBase := ch * 2
			hasWave, hasSun := false, false

			for dr := range 4 {
				for dc := range 2 {
					dy := base + dr
					dx := colBase + dc
					if dy >= dotRows || dx >= dotCols {
						continue
					}
					switch grid[dy][dx] {
					case 1:
						braille |= brailleBit[dr][dc]
					case 2:
						braille |= brailleBit[dr][dc]
						hasWave = true
					case 3:
						braille |= brailleBit[dr][dc]
						hasSun = true
					}
				}
			}

			// Priority: wave (red) > sun (yellow) > grid (green).
			var style lipgloss.Style
			switch {
			case hasWave:
				style = specHighStyle
			case hasSun:
				style = specMidStyle
			default:
				style = specLowStyle
			}
			sb.WriteString(style.Render(string(braille)))
		}
		lines[row] = sb.String()
	}

	return strings.Join(lines, "\n")
}

// specStyle returns the spectrum color style for a given row height (0-1).
func specStyle(rowBottom float64) lipgloss.Style {
	switch {
	case rowBottom >= 0.6:
		return specHighStyle
	case rowBottom >= 0.3:
		return specMidStyle
	default:
		return specLowStyle
	}
}
