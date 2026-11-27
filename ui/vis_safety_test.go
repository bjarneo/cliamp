package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestVisualizersHandleZeroAndShortBands(t *testing.T) {
	bandCases := []struct {
		name  string
		bands []float64
	}{
		{name: "nil", bands: nil},
		{name: "empty", bands: []float64{}},
		{name: "one", bands: []float64{0.6}},
		{name: "three", bands: []float64{0.2, 0.7, 0.4}},
		{name: "nine", bands: []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9}},
	}

	for mode := VisMode(0); mode < VisCount; mode++ {
		if mode == VisNone {
			continue
		}
		for _, tc := range bandCases {
			t.Run(fmt.Sprintf("%s/%s", visModes[mode].name, tc.name), func(t *testing.T) {
				v := NewVisualizer(44100)
				v.Mode = mode
				v.Cols = 40
				v.Rows = 5
				// Prime once with full bands so stateful drivers like Mosaic
				// build grids before switching to short bands.
				v.SetBands(uniformBands(0.5))
				v.Tick(VisTickContext{Now: time.Unix(1, 0), Playing: true})

				v.SetBands(tc.bands)
				v.smoothedBands = append([]float64(nil), tc.bands...)
				v.Tick(VisTickContext{Now: time.Unix(2, 0), Playing: true})
				out := v.Render()

				lines := strings.Split(out, "\n")
				if len(lines) != v.Rows {
					t.Fatalf("line count = %d, want %d", len(lines), v.Rows)
				}
				for i, line := range lines {
					if width := lipgloss.Width(line); width != v.Cols {
						t.Fatalf("row %d width = %d, want %d", i, width, v.Cols)
					}
				}
			})
		}
	}

	// Verify direct renderers with empty bands produce non-NaN output.
	t.Run("direct_empty_bands", func(t *testing.T) {
		v := NewVisualizer(44100)
		v.Cols = 40
		v.Rows = 5
		v.frame = 17
		direct := []struct {
			name string
			fn   func([]float64) string
		}{
			{"Pulse", v.renderPulse},
			{"Firework", v.renderFirework},
			{"Retro", v.renderRetro},
			{"Butterfly", v.renderButterfly},
			{"Logo", v.renderLogo},
			{"Bubbles", v.renderBubbles},
			{"Sakura", v.renderSakura},
		}
		for _, d := range direct {
			for _, bands := range [][]float64{nil, {}, {0.5}, {0.2, 0.5, 0.8}} {
				out := d.fn(bands)
				if out == "" {
					t.Fatalf("%s with %d bands returned empty output", d.name, len(bands))
				}
			}
		}
	})
}

func TestGeyserParticleCapAndIdleRelease(t *testing.T) {
	v := NewVisualizer(44100)
	v.Cols = 100
	v.Rows = 20
	activateMode(t, v, VisGeyser)
	d := v.driverFor(VisGeyser).(*geyserDriver)

	// Drive repeated strong bass kicks to exceed the cap if unbounded.
	for i := range 20 {
		v.bands = uniformBands(0.0)
		v.smoothedBands = uniformBands(0.0)
		d.prevBass = 0
		v.bands = uniformBands(1.0)
		v.smoothedBands = uniformBands(1.0)
		d.Tick(v, VisTickContext{Now: time.Unix(int64(i+1), 0), Playing: true})
		if len(d.particles) > geyserMaxParticles {
			t.Fatalf("tick %d: particles = %d, exceeds cap %d", i, len(d.particles), geyserMaxParticles)
		}
	}

	// Let particles expire during silence and confirm backing storage releases.
	v.bands = uniformBands(0)
	v.smoothedBands = uniformBands(0)
	for i := range 250 {
		d.Tick(v, VisTickContext{Now: time.Unix(int64(100+i), 0), Playing: false})
		if len(d.particles) == 0 {
			break
		}
	}
	if len(d.particles) != 0 {
		t.Fatalf("particles did not drain: %d remaining", len(d.particles))
	}
	if cap(d.particles) > geyserMaxIdleCap {
		t.Fatalf("idle particles cap = %d, want <= %d", cap(d.particles), geyserMaxIdleCap)
	}
}

func TestSandExplosionReleasesParticleBackingArray(t *testing.T) {
	d := newSandDriver().(*sandDriver)
	d.grid.resize(16, 32)
	for i := range d.grid.cells {
		d.grid.cells[i] = 1
	}
	d.startExplosion()
	if len(d.particles) == 0 {
		t.Fatal("startExplosion spawned no particles")
	}
	for range 200 {
		d.tickExplosion()
		if len(d.particles) == 0 {
			break
		}
	}
	if len(d.particles) != 0 || d.particles != nil {
		t.Fatalf("post-explosion particles = len %d cap %d, want nil", len(d.particles), cap(d.particles))
	}
}

func TestEmptyBandsDrawVisibleParticlesWithoutNaN(t *testing.T) {
	cases := []struct {
		name string
		fn   func(*Visualizer) string
	}{
		{"Bubbles", func(v *Visualizer) string { return v.renderBubbles(nil) }},
		{"Sakura", func(v *Visualizer) string { return v.renderSakura(nil) }},
		{"Firework", func(v *Visualizer) string { return v.renderFirework(nil) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := NewVisualizer(44100)
			v.Cols = 40
			v.Rows = 5
			v.frame = 15
			plain := ansi.Strip(tc.fn(v))
			if strings.Count(plain, "\n")+1 != v.Rows {
				t.Fatalf("rows = %d, want %d", strings.Count(plain, "\n")+1, v.Rows)
			}
			lit := false
			for _, r := range plain {
				if r > '\u2800' && r <= '\u28ff' {
					lit = true
					break
				}
			}
			if !lit {
				t.Fatalf("%s drew no visible Braille dots with nil bands", tc.name)
			}
		})
	}
}
