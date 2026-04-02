package ui

import (
	"math"
	"strings"
	"time"
)

// vkDriver renders concentric iris rings inspired by the Blade Runner
// Voight-Kampff eye scanner. Rings expand and contract with audio amplitude.
// A vertical bellows indicator on the right edge pulses with bass energy.
type vkDriver struct {
	breathPhase float64 // 0.0-1.0 bellows animation phase
}

func newVKDriver() visModeDriver {
	return &vkDriver{}
}

func (*vkDriver) AnalysisSpec(*Visualizer) VisAnalysisSpec {
	return spectrumAnalysisSpec(DefaultSpectrumBands)
}

func (*vkDriver) TickInterval(_ *Visualizer, ctx VisTickContext) time.Duration {
	return defaultDriverTickInterval(ctx)
}

func (d *vkDriver) OnEnter(*Visualizer) {
	d.breathPhase = 0
}

func (*vkDriver) OnLeave(*Visualizer) {}

func (d *vkDriver) Tick(v *Visualizer, ctx VisTickContext) {
	defaultDriverTick(v, ctx, d.AnalysisSpec(v))
	if ctx.OverlayActive {
		return
	}

	// Advance bellows phase — speed driven by bass energy.
	bass := 0.0
	if len(v.bands) >= 2 {
		bass = (v.bands[0] + v.bands[1]) / 2
	}
	d.breathPhase += 0.03 + bass*0.12
	if d.breathPhase >= 1.0 {
		d.breathPhase -= 1.0
	}
}

// ringRadii defines the relative radii for the concentric iris rings (0-1).
// Four rings: pupil, iris inner, iris outer, sclera — matches the film's eye scanner.
var ringRadii = [4]float64{0.20, 0.45, 0.70, 0.90}

// ringTags maps each ring to a spectrum color tag (inner=high, outer=low).
var ringTags = [4]int{2, 2, 1, 0}

func (d *vkDriver) Render(v *Visualizer) string {
	height := v.Rows
	dotRows := height * 4
	dotCols := PanelWidth * 2
	bandCount := len(v.bands)

	centerX := float64(dotCols) / 2.0
	centerY := float64(dotRows) / 2.0

	// Squash X axis so circles appear round in the wide terminal.
	xScale := centerY / centerX
	maxR := centerY - 1

	// Overall energy for ring modulation.
	var avgEnergy float64
	for _, b := range v.bands {
		avgEnergy += b
	}
	if bandCount > 0 {
		avgEnergy /= float64(bandCount)
	}

	// Bellows height (right-side indicator) driven by bass.
	bass := 0.0
	if bandCount >= 2 {
		bass = (v.bands[0] + v.bands[1]) / 2
	}
	bellowsHeight := 0.1 + bass*0.8 + math.Sin(d.breathPhase*2*math.Pi)*0.1
	bellowsHeight = max(0, min(1, bellowsHeight))
	bellowsDots := int(bellowsHeight * float64(dotRows))

	// Allocate dot grid: 0=empty, 1-5=ring index (1=outermost), 6=bellows.
	grid := make([]byte, dotRows*dotCols)

	// Draw concentric rings.
	for dy := range dotRows {
		for dx := range dotCols {
			ddx := (float64(dx) - centerX) * xScale
			ddy := float64(dy) - centerY
			dist := math.Sqrt(ddx*ddx + ddy*ddy)

			// Check each ring (inner to outer).
			for ri := len(ringRadii) - 1; ri >= 0; ri-- {
				// Modulate ring radius with energy — inner rings respond more.
				// Floor at 45% so rings remain readable at silence.
				weight := 1.0 - float64(ri)*0.15
				modulation := max(0.45, 0.45+0.55*avgEnergy*weight)
				r := maxR * ringRadii[ri] * modulation

				// Ring thickness — thinner rings look more precise.
				thickness := 1.2 + avgEnergy*0.5
				if math.Abs(dist-r) < thickness {
					// Anti-alias with scatterHash for textured edge.
					edgeDist := math.Abs(dist-r) / thickness
					if edgeDist < 0.7 || scatterHash(ri, dy, dx, v.frame) < (1.0-edgeDist)*0.8 {
						grid[dy*dotCols+dx] = byte(ri + 1)
					}
				}
			}
		}
	}

	// Draw bellows column on the right edge (4 dot-columns wide).
	bellowsLeft := dotCols - 6
	bellowsTop := dotRows - bellowsDots
	for dy := bellowsTop; dy < dotRows; dy++ {
		for dx := bellowsLeft; dx < dotCols; dx++ {
			if grid[dy*dotCols+dx] == 0 {
				grid[dy*dotCols+dx] = 6
			}
		}
	}

	// Render braille characters.
	lines := make([]string, height)
	for row := range height {
		var sb, run strings.Builder
		tag := -1
		base := row * 4

		for ch := range PanelWidth {
			var braille rune = '\u2800'
			maxTag := -1

			for dr := range 4 {
				for dc := range 2 {
					dy := base + dr
					dx := ch*2 + dc
					if dy >= dotRows || dx >= dotCols {
						continue
					}
					cell := grid[dy*dotCols+dx]
					if cell > 0 {
						braille |= brailleBit[dr][dc]
						var cellTag int
						if cell == 6 {
							cellTag = 1 // bellows in mid color
						} else {
							ri := int(cell) - 1
							if ri < len(ringTags) {
								cellTag = ringTags[ri]
							}
						}
						if cellTag > maxTag {
							maxTag = cellTag
						}
					}
				}
			}

			newTag := maxTag
			if braille == '\u2800' {
				newTag = -1
			}
			if newTag != tag {
				flushStyleRun(&sb, &run, tag)
				tag = newTag
			}
			run.WriteRune(braille)
		}
		flushStyleRun(&sb, &run, tag)
		lines[row] = sb.String()
	}

	return strings.Join(lines, "\n")
}
