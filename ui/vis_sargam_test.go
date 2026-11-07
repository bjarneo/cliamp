package ui

import (
	"math"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
)

// sargamTestSamples builds a two-harmonic tone at freq.
func sargamTestSamples(freq, sr float64, n int) []float64 {
	samples := make([]float64, n)
	for i := range samples {
		t := float64(i) / sr
		samples[i] = 0.5*math.Sin(2*math.Pi*freq*t) + 0.25*math.Sin(2*math.Pi*2*freq*t)
	}
	return samples
}

// TestSargamPitchDetection checks the harmonic-sum detector across octaves.
func TestSargamPitchDetection(t *testing.T) {
	tests := []struct {
		freq     float64
		wantMidi float64
	}{
		{110, 45},
		{220, 57},
		{440, 69},
		{523.25, 72},
	}
	for _, tt := range tests {
		t.Run(sargamLabel(int(tt.wantMidi)), func(t *testing.T) {
			v := NewVisualizer(44100)
			d := &sargamDriver{}
			got, ok := d.detect(v, sargamTestSamples(tt.freq, v.sr, sargamFFTSize))
			if !ok {
				t.Fatalf("%.2f Hz: no pitch detected", tt.freq)
			}
			if math.Abs(got-tt.wantMidi) > 0.6 {
				t.Errorf("%.2f Hz: midi = %.2f, want %.2f", tt.freq, got, tt.wantMidi)
			}
		})
	}
}

// TestSargamPitchDetectionSilence rejects silence and sub-threshold signals.
func TestSargamPitchDetectionSilence(t *testing.T) {
	v := NewVisualizer(44100)
	d := &sargamDriver{}
	if _, ok := d.detect(v, make([]float64, sargamFFTSize)); ok {
		t.Fatal("silence detected as a pitch")
	}
	quiet := make([]float64, sargamFFTSize)
	for i := range quiet {
		quiet[i] = 1e-6 * math.Sin(2*math.Pi*220*float64(i)/v.sr)
	}
	if _, ok := d.detect(v, quiet); ok {
		t.Fatal("sub-threshold signal detected as a pitch")
	}
}

// TestSargamLabel checks the Roman swara names and octave markers.
func TestSargamLabel(t *testing.T) {
	tests := []struct {
		midi int
		want string
	}{
		{48, "Sa."},
		{55, "Pa."},
		{60, "Sa"},
		{67, "Pa"},
		{72, "Sa'"},
		{61, "re"},
		{66, "ma"},
	}
	for _, tt := range tests {
		if got := sargamLabel(tt.midi); got != tt.want {
			t.Errorf("sargamLabel(%d) = %q, want %q", tt.midi, got, tt.want)
		}
	}
}

// TestSargamAxisLabelsKeepRowsDistinct ensures labels do not collide on a
// short panel.
func TestSargamAxisLabelsKeepRowsDistinct(t *testing.T) {
	labels := sargamAxisLabels(36, 9, 4)
	seen := 0
	for _, label := range labels {
		if label != "" {
			seen++
		}
	}
	if seen != 5 {
		t.Fatalf("labelled rows = %d, want 5 (%q)", seen, labels)
	}
}

// TestSargamTierColors maps guides to the dim tier and the contour to the
// spectrum tiers.
func TestSargamTierColors(t *testing.T) {
	tests := []struct {
		tier  int
		style styleANSI
	}{
		{1, dimANSI},     // swara guide and rest playhead
		{2, specANSI[0]}, // low contour
		{3, specANSI[1]}, // mid contour
		{4, specANSI[2]}, // high contour and live playhead
	}
	for _, tt := range tests {
		var sb, run strings.Builder
		run.WriteString("x")
		sargamFlush(&sb, &run, tt.tier)
		want := tt.style.prefix + "x" + tt.style.suffix
		if got := sb.String(); got != want {
			t.Errorf("tier %d = %q, want %q", tt.tier, got, want)
		}
	}
	if dimANSI.prefix == specANSI[0].prefix {
		t.Error("guide and low contour share a colour")
	}
}

// TestSargamReadsAudibleSamplesDirectly checks the pane path: the driver pulls
// its own audible window instead of relying on the primary's sample buffer.
func TestSargamReadsAudibleSamplesDirectly(t *testing.T) {
	v := NewVisualizer(44100)
	v.Cols = 48
	v.Rows = 9
	v.Mode = VisSargam

	tone := sargamTestSamples(220, v.sr, sargamFFTSize)
	reads := 0
	now := time.Unix(1_700_000_000, 0)
	ctx := VisTickContext{
		Now:     now,
		Playing: true,
		WaveSamplesInto: func(dst []float64) int {
			reads++
			return copy(dst, tone)
		},
	}
	for i := range 8 {
		ctx.Now = now.Add(time.Duration(i) * TickFast)
		v.Tick(ctx)
	}

	if reads == 0 {
		t.Fatal("driver never read the audible tap")
	}
	if want := 4; reads != want {
		t.Fatalf("tap reads = %d, want %d at the %v pitch interval", reads, want, sargamPitchInterval)
	}
	d := v.driverFor(VisSargam).(*sargamDriver)
	if !d.noteOK || math.Abs(d.note-57) > 0.6 {
		t.Fatalf("note = %.2f ok=%v, want around MIDI 57", d.note, d.noteOK)
	}
	if len(v.WaveSamples()) == 0 {
		t.Fatal("driver did not refresh the shared wave buffer")
	}
}

// TestSargamSweepsAndRenders checks that the playhead advances and every frame
// keeps the panel size.
func TestSargamSweepsAndRenders(t *testing.T) {
	v := NewVisualizer(44100)
	v.Cols = 48
	v.Rows = 9
	v.Mode = VisSargam

	samples := sargamTestSamples(220, v.sr, sargamFFTSize)
	now := time.Unix(1_700_000_000, 0)
	ctx := VisTickContext{
		Now:     now,
		Playing: true,
		Analyze: func(spec VisAnalysisSpec) []float64 {
			return v.Analyze(samples, spec)
		},
	}

	d := v.driverFor(VisSargam).(*sargamDriver)
	for i := range 60 {
		ctx.Now = now.Add(time.Duration(i) * TickAnim)
		v.Tick(ctx)
		out := v.Render()
		lines := strings.Split(out, "\n")
		if len(lines) != v.Rows {
			t.Fatalf("frame %d: %d lines, want %d", i, len(lines), v.Rows)
		}
		for _, line := range lines {
			if width := lipgloss.Width(line); width != v.Cols {
				t.Fatalf("frame %d: width = %d, want %d", i, width, v.Cols)
			}
		}
	}

	if !d.noteOK {
		t.Fatal("no note detected from a steady 220 Hz tone")
	}
	if math.Abs(d.note-57) > 0.6 {
		t.Fatalf("note = %.2f, want around MIDI 57", d.note)
	}
	if d.pos <= 4 {
		t.Fatalf("playhead pos = %.2f, want it to advance left to right", d.pos)
	}
	if d.filled == 0 {
		t.Fatal("trace wrote no columns")
	}
}

// TestSargamPauseFreezesPlayhead checks that pausing stops the sweep.
func TestSargamPauseFreezesPlayhead(t *testing.T) {
	v := NewVisualizer(44100)
	v.Cols = 48
	v.Rows = 9
	v.Mode = VisSargam

	samples := sargamTestSamples(220, v.sr, sargamFFTSize)
	now := time.Unix(1_700_000_000, 0)
	playing := VisTickContext{
		Now:     now,
		Playing: true,
		Analyze: func(spec VisAnalysisSpec) []float64 {
			return v.Analyze(samples, spec)
		},
	}
	for i := range 20 {
		playing.Now = now.Add(time.Duration(i) * TickAnim)
		v.Tick(playing)
	}

	d := v.driverFor(VisSargam).(*sargamDriver)
	pos := d.pos
	paused := VisTickContext{Now: now.Add(time.Second), Paused: true}
	for i := range 20 {
		paused.Now = now.Add(time.Second + time.Duration(i)*TickFast)
		v.Tick(paused)
	}
	if d.pos != pos {
		t.Fatalf("paused playhead moved from %.2f to %.2f", pos, d.pos)
	}
}
